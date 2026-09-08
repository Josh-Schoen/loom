// Copyright 2025 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet/file"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
)

var _ fileImporter = &fileImporterParquet {} // implements fileImporter
var _ fmt.Stringer = &fileImporterParquet {} // implements fmt.Stringer

type fileImporterParquet struct {

	fileImporterBase

	m_fileReader         *file.Reader          // Low-level Parquet file reader (needs explicit Close)
	m_parquetReader      *pqarrow.FileReader   // Apache Arrow Parquet file reader
	m_recordReader       pqarrow.RecordReader  // Record reader for iterating through file

	m_errChain           error                 // contains error for each failed record (like CSV)
	m_nRecordNumber      int                   // current record number (like CSV)
	m_nColumnNumber      int                   // current column number for errors (like CSV)
	m_nSegmentNumber     int                   // current segment number (like CSV)

	m_nFieldsPerRecord   int                   // number of fields per record (like CSV)
	m_nRecordByteCount   uint32                // byte size of current record (like CSV)
	m_aoLastRecord       [] driver.NamedValue  // record processed but did not fit in buffer (like CSV)
	m_bReturnedRecords   bool                  // true if we've returned at least one record (prevents false "no records" error)

	m_schema             *arrow.Schema         // schema from first record batch (used for type-aware null handling)
	m_currentRecord      arrow.RecordBatch     // current Arrow record batch being processed
	m_currentRecordIndex int64                 // index within current record batch

} // end fileImporterParquet struct

func (fip *fileImporterParquet) String () string { // satisfies fmt.Stringer

	return fmt.Sprintf ("fileImporterParquet: %v m_nSegmentNumber=%v m_nRecordNumber=%v m_errChain=%v",
		fip.fileImporterBase, fip.m_nSegmentNumber, fip.m_nRecordNumber, fip.m_errChain)

} // end method String

func newFileImporterParquet (fib fileImporterBase) (fileImporter, error) {

	fip := &fileImporterParquet {
		fileImporterBase    : fib,
		m_nSegmentNumber    : 0,
		m_nRecordNumber     : -1,
	}

	var err error

	// Open the Parquet file readers eagerly to read the file schema from the footer.
	// This reads only Parquet metadata (no row data) so it is inexpensive.
	// m_schema must be available before any rows are processed so that FastLoad can
	// inspect column types and set up bind metadata correctly.
	fip.m_fileReader, err = file.NewParquetReader (fip.m_file)
	if err != nil {
		return nil, fmt.Errorf ("failed to open Parquet file: %w", err)
	}

	fip.m_parquetReader, err = pqarrow.NewFileReader (fip.m_fileReader, pqarrow.ArrowReadProperties {}, memory.DefaultAllocator)
	if err != nil {
		fip.m_fileReader.Close ()
		return nil, fmt.Errorf ("failed to create Arrow file reader: %w", err)
	}

	fip.m_schema, err = fip.m_parquetReader.Schema ()
	if err != nil {
		fip.m_fileReader.Close ()
		return nil, fmt.Errorf ("failed to read Parquet schema: %w", err)
	}

	return fip, nil

} // end newFileImporterParquet

func (fip *fileImporterParquet) close () error {

	// NOTE: Unlike CSV, Parquet file.Reader takes ownership of the os.File and closes it.
	// CSV uses bufio.Reader which doesn't take ownership, so CSV calls fileImporterBase.close().
	// Parquet must:
	// 1. Release m_currentRecord (Arrow record batch with memory-mapped data)
	// 2. Close m_fileReader (low-level Parquet reader which closes the underlying os.File)
	// 3. NOT call fileImporterBase.close() because file.Reader already closed the os.File
	//
	// If file.NewParquetReader failed (e.g. corrupted file), m_fileReader is nil and never took
	// ownership of the os.File. In that case, close the os.File directly to avoid a leaked handle.

	// Release current Arrow record if any
	if fip.m_currentRecord != nil {
		fip.m_currentRecord.Release ()
		fip.m_currentRecord = nil
	}

	// Release Arrow record reader if used
	if fip.m_recordReader != nil {
		fip.m_recordReader.Release ()
		fip.m_recordReader = nil
	}

	// Close low-level Parquet reader (this also closes the underlying os.File)
	if fip.m_fileReader != nil {
		err := fip.m_fileReader.Close ()
		fip.m_fileReader = nil
		fip.m_file = nil // Clear reference since file.Reader closed it
		if err != nil {
			return fmt.Errorf ("failed to close Parquet reader: %w", err)
		}
	} else if fip.m_file != nil {
		// If file.NewParquetReader failed (e.g. corrupted magic bytes), m_fileReader was never
		// created so it never took ownership of the os.File. Close the file directly to avoid
		// leaving a locked file handle on Windows.
		fip.m_file.Close ()
		fip.m_file = nil
	}

	// Note: pqarrow.FileReader doesn't have explicit Close() method

	// Do NOT call fileImporterBase.close() because file.Reader already closed the os.File

	return nil

} // end method close

func (fip *fileImporterParquet) parseError (sErrMsg string) error {

	if fip.m_nColumnNumber > 0 {
		return fmt.Errorf ("parse error in record %d; column %d: %s",
			fip.m_nRecordNumber, fip.m_nColumnNumber, sErrMsg)
	}

	return fmt.Errorf ("parse error in record %d: %s", fip.m_nRecordNumber, sErrMsg)

} // end parseError

func (fip *fileImporterParquet) getBatchSegment (nMaxSize uint32, bFastloading bool) (aaBatchRecords [][] driver.NamedValue, nLastRecordProcessed int, bParseError bool, err error) {

	nByteSize := uint32 (0)
	fip.m_nSegmentNumber ++
	fip.m_bFastloading = bFastloading

	// Check if there was a record that was processed but did not fit into the last segment
	// This mirrors the CSV implementation
	if fip.m_aoLastRecord != nil {
		aaBatchRecords = append (aaBatchRecords, fip.m_aoLastRecord)
		fip.m_aoLastRecord = nil
		nByteSize = fip.m_nRecordByteCount
		fip.m_nRecordNumber ++
	}

	for {
		var aoRecord [] driver.NamedValue
		aoRecord, bParseError, err = fip.processRecord ()

		if err != nil && ! errors.Is (err, io.EOF) {
			if ! bParseError || fip.m_bFastloading { return }
			fip.m_errChain = chainErrors (fip.m_errChain, err)
			continue
		}

		// Check buffer size and record count limits (same as CSV: max 16383 records per segment)
		if (nMaxSize > 0 && nByteSize + fip.m_nRecordByteCount > nMaxSize) || fip.m_nRecordNumber % 16383 == 16382 {

			// The last record processed does not fit into the current buffer, save for
			// next segment and subtract one from the number of records being returned.
			fip.m_aoLastRecord = aoRecord
			fip.m_nRecordNumber --

			if fip.m_errChain != nil {
				err = fip.m_errChain
				fip.m_errChain = nil
				bParseError = true
			}

			return aaBatchRecords, fip.m_nRecordNumber, bParseError, err
		}

		if len (aoRecord) > 0 {
			nByteSize += fip.m_nRecordByteCount
			aaBatchRecords = append (aaBatchRecords, aoRecord)
		}

		if errors.Is (err, io.EOF) {

			// Check if we have no records and no errors on the FIRST call
			// (if m_bReturnedRecords is true, we've already returned records in a previous segment)
			if len (aaBatchRecords) == 0 && fip.m_errChain == nil && ! fip.m_bReturnedRecords {
				return aaBatchRecords, fip.m_nRecordNumber, false, fmt.Errorf ("the file %v contains no records", fip.m_sFileName)
			}

			// Mark that we've returned records (or tried to)
			if len (aaBatchRecords) > 0 {
				fip.m_bReturnedRecords = true
			}

			if fip.m_errChain != nil {
				err = fip.m_errChain
				fip.m_errChain = nil
				bParseError = true
			} else {
				// We know err is EOF and we don't return EOF
				err = nil
			}

			return aaBatchRecords, fip.m_nRecordNumber, bParseError, err
		}
	} // end for

} // end getBatchSegment

func (fip *fileImporterParquet) processRecord () (aNamedValues [] driver.NamedValue, bParseError bool, err error) {

	fip.m_nRecordByteCount = 0

	// Lazy initialization of the record reader.
	// m_fileReader, m_parquetReader and m_schema are opened eagerly in newFileImporterParquet
	// so that FastLoad can read the schema before processing any rows.
	if fip.m_recordReader == nil {
		// Create record reader to iterate through the file
		// Pass context, column indices (nil=all), row group indices (nil=all)
		fip.m_recordReader, err = fip.m_parquetReader.GetRecordReader (context.Background (), nil, nil)
		if err != nil {
			return nil, true, fip.parseError (fmt.Sprintf ("failed to create record reader: %v", err))
		}

	}

	// If we don't have a current record batch, or we've exhausted it, read the next one
	if fip.m_currentRecord == nil || fip.m_currentRecordIndex >= fip.m_currentRecord.NumRows () {

		// Release previous record
		if fip.m_currentRecord != nil {
			fip.m_currentRecord.Release ()
			fip.m_currentRecord = nil
		}

		// Try to read next record batch
		hasNext := fip.m_recordReader.Next ()

		if ! hasNext {
			// No more records
			if err = fip.m_recordReader.Err (); err != nil {
				return nil, true, fip.parseError (fmt.Sprintf ("error reading records: %v", err))
			}
			return nil, false, io.EOF
		}

		fip.m_currentRecord = fip.m_recordReader.RecordBatch ()
		fip.m_currentRecordIndex = 0
	}

	// Process one row from the current record batch
	fip.m_nRecordNumber ++

	// Extract values from each column
	for colIdx := 0; colIdx < int (fip.m_currentRecord.NumCols ()); colIdx++ {

		fip.m_nColumnNumber = colIdx + 1
		col := fip.m_currentRecord.Column (colIdx)

		var teraval TeradataValue
		var nFieldByteSize int
		var bParseError bool

		// Convert Arrow column value to TeradataValue
		teraval, nFieldByteSize, bParseError, err = fip.convertArrowValueToTeradataValue (col, int (fip.m_currentRecordIndex), colIdx)
		if err != nil {
			// Increment row index before returning error to avoid infinite loop
			fip.m_currentRecordIndex ++
			return nil, bParseError, err
		}

		// Add to record
		aNamedValues = append (aNamedValues, driver.NamedValue {Ordinal: 1, Value: teraval})

		// Track byte size. Deferred LOB identifiers are 4-byte integers.
		// VARCHAR is deferred when >32000 bytes; VARBYTE is deferred when >64000 bytes.
		fip.m_nRecordByteCount += func () uint32 {
			if ! fip.m_bFastloading {
				if teraval.CookedDataType () == T_VARCHAR && nFieldByteSize > 32000 { return 4 }
				if teraval.CookedDataType () == T_VARBYTE && nFieldByteSize > 64000 { return 4 }
			}
			return uint32 (nFieldByteSize)
		} ()
	}

	// Validate field count (same as CSV)
	nFieldsInRecord := len (aNamedValues)

	if fip.m_nFieldsPerRecord > 0 {
		if nFieldsInRecord != fip.m_nFieldsPerRecord {
			fip.m_nColumnNumber = 0
			return nil, true, fip.parseError (fmt.Sprintf ("wrong number of fields in record, expected value is %v actual value is %v", fip.m_nFieldsPerRecord, nFieldsInRecord))
		}
	} else if fip.m_nFieldsPerRecord == 0 {
		fip.m_nFieldsPerRecord = nFieldsInRecord
	}

	fip.m_currentRecordIndex ++

	return aNamedValues, false, nil

} // end processRecord

func (fip *fileImporterParquet) convertArrowValueToTeradataValue (col arrow.Array, rowIdx int, colIdx int) (teraval TeradataValue, nFieldByteSize int, bParseError bool, err error) {

	// Check for null and return appropriate null type based on schema
	if col.IsNull (rowIdx) {
		return fip.convertNullArrowValue (col, rowIdx, colIdx)
	}

	// Convert Arrow values to TeradataValues with appropriate data types
	// Each Arrow type maps directly to its corresponding Teradata type

	return fip.convertArrowValue (col, rowIdx, colIdx)

} // end convertArrowValueToTeradataValue

func (fip *fileImporterParquet) convertNullArrowValue (col arrow.Array, rowIdx int, colIdx int) (teraval TeradataValue, nFieldByteSize int, bParseError bool, err error) {

	if fip.m_schema != nil && colIdx < len (fip.m_schema.Fields ()) {
		field := fip.m_schema.Field (colIdx)

		// Map Arrow type ID to appropriate Teradata null type
		// nFieldByteSize must reflect the actual byte size in FastLoad format
		switch field.Type.ID () {

			case arrow.INT8,
				 arrow.BOOL:
				return NewTeradataNullByteint (), 1, false, nil

			case arrow.INT16,
				 arrow.UINT8:
				return NewTeradataNullSmallint (), 2, false, nil

			case arrow.INT32,
				 arrow.UINT16:
				return NewTeradataNullInteger (), 4, false, nil

			case arrow.INT64,
				 arrow.UINT32:
				return NewTeradataNullBigint (), 8, false, nil

			case arrow.FLOAT32,
				 arrow.FLOAT64:
				return NewTeradataNullFloat (), 8, false, nil

			case arrow.DECIMAL128:
				decType := field.Type.(*arrow.Decimal128Type)
				return NewTeradataNullDecimal (uint8(decType.Precision), uint8(decType.Scale)), int (byteCountFromDecimalPrecision (uint8 (decType.Precision))), false, nil

			case arrow.DECIMAL256:
				decType := field.Type.(*arrow.Decimal256Type)
				if decType.Precision > 38 {
					err = fip.parseError (fmt.Sprintf ("Decimal256 precision %d exceeds Teradata maximum of 38", decType.Precision))
					return TeradataValue { m_err : err }, 0, false, err
				}
				return NewTeradataNullDecimal (uint8(decType.Precision), uint8(decType.Scale)), int (byteCountFromDecimalPrecision (uint8 (decType.Precision))), false, nil

			case arrow.STRING,
				 arrow.LARGE_STRING:
				// VARCHAR: 2 bytes for length prefix + 0 for content
				return NewTeradataNullVarchar (), 2, false, nil

			case arrow.BINARY,
				 arrow.LARGE_BINARY:
				// VARBYTE: 2 bytes for length prefix + 0 for content
				return NewTeradataNullVarbyte (), 2, false, nil

			case arrow.DATE32:
				return NewTeradataNullDateInteger (), 4, false, nil

			case arrow.TIME32,
				 arrow.TIME64:
				// TIME: min 8 bytes (HH:MM:SS) plus precision+1 bytes for fractional seconds
				nByteSize := uint32 (8)
				nPrecision := 6 // default to microseconds
				if paramMeta, ok := fip.m_mapParameterDataTypes[colIdx + 1] ; ok {
					nPrecision = int (paramMeta.m_uScale)
				}
				if nPrecision > 0 {
					nByteSize += uint32 (nPrecision + 1)
				}
				return NewTeradataNullTime (uint8 (nPrecision)), int (nByteSize), false, nil

			case arrow.TIMESTAMP:
				// TIMESTAMP: min 19 bytes (YYYY-MM-DD HH:MM:SS) plus precision+1 bytes for fractional seconds
				nByteSize := uint32 (19)
				nPrecision := 6 // default to microseconds
				if paramMeta, ok := fip.m_mapParameterDataTypes[colIdx + 1] ; ok {
					nPrecision = int (paramMeta.m_uScale)
				}
				if nPrecision > 0 {
					nByteSize += uint32 (nPrecision + 1)
				}
				return NewTeradataNullTimestamp (uint8 (nPrecision)), int (nByteSize), false, nil

			case arrow.DICTIONARY:
				// Unwrap dictionary encoding: recurse with the value type substituted into the field
				valueType := field.Type.(*arrow.DictionaryType).ValueType
				fakeField := arrow.Field {Name: field.Name, Type: valueType, Nullable: field.Nullable}
				fakeSchema := arrow.NewSchema ([] arrow.Field {fakeField}, nil)
				fip2 := *fip
				fip2.m_schema = fakeSchema
				return fip2.convertNullArrowValue (col, rowIdx, 0)

			default:
				return NewTeradataNullVarchar (), 2, false, nil
		} // end switch on Arrow type ID for nulls

	}

	// Fallback if schema not available
	return NewTeradataNullVarchar (), 2, false, nil

} // end convertNullArrowValue

func (fip *fileImporterParquet) convertArrowValue (col arrow.Array, rowIdx int, colIdx int) (teraval TeradataValue, nFieldByteSize int, bParseError bool, err error) {

	switch typedCol := col.(type) {

		case *array.Boolean:
			nFieldByteSize = 1
			if typedCol.Value (rowIdx) {
				teraval = NewTeradataValueByteint (1)
			} else {
				teraval = NewTeradataValueByteint (0)
			}
			return

		case *array.Int8:
			nFieldByteSize = 1
			teraval = NewTeradataValueByteint (typedCol.Value (rowIdx))
			return

		case *array.Int16:
			nFieldByteSize = 2
			teraval = NewTeradataValueSmallint (typedCol.Value (rowIdx))
			return

		case *array.Int32:
			nFieldByteSize = 4
			teraval = NewTeradataValueInteger  (typedCol.Value (rowIdx))
			return

		case *array.Int64:
			nFieldByteSize = 8
			teraval = NewTeradataValueBigint  (typedCol.Value (rowIdx))
			return

		case *array.Uint8:
			nFieldByteSize = 2
			teraval = NewTeradataValueSmallint (int16 (typedCol.Value (rowIdx)))
			return

		case *array.Uint16:
			nFieldByteSize = 4
			teraval = NewTeradataValueInteger (int32(typedCol.Value (rowIdx)))
			return

		case *array.Uint32:
			nFieldByteSize = 8
			teraval = NewTeradataValueBigint (int64(typedCol.Value (rowIdx)))
			return

		case *array.Float32:
			nFieldByteSize = 8
			teraval = NewTeradataValueFloat (float64 (typedCol.Value (rowIdx)))
			return

		case *array.Float64:
			nFieldByteSize = 8
			teraval = NewTeradataValueFloat (typedCol.Value (rowIdx))
			return

		case *array.String:
			teraval = NewTeradataValueVarchar (typedCol.Value (rowIdx))
			nFieldByteSize = len (teraval.m_abyValue)
			return

		case *array.LargeString:
			teraval = NewTeradataValueVarchar (typedCol.Value (rowIdx))
			nFieldByteSize = len (teraval.m_abyValue)
			return

		case *array.Binary:
			teraval = NewTeradataValueVarbyte (typedCol.Value (rowIdx))
			nFieldByteSize = len (teraval.m_abyValue)
			return

		case *array.LargeBinary:
			teraval = NewTeradataValueVarbyte (typedCol.Value (rowIdx))
			nFieldByteSize = len (teraval.m_abyValue)
			return

		case *array.Date32:
			return fip.convertDate32 (typedCol, rowIdx)

		case *array.Timestamp:
			return fip.convertTimestamp (col, typedCol, rowIdx, colIdx)

		case *array.Time32:
			return fip.convertTime32 (col, typedCol, rowIdx, colIdx)

		case *array.Time64:
			return fip.convertTime64 (col, typedCol, rowIdx, colIdx)

		case *array.Decimal128:
			return fip.convertDecimal128 (col, typedCol, rowIdx)

		case *array.Decimal256:
			return fip.convertDecimal256 (col, typedCol, rowIdx)

		case *array.Dictionary:
			// Unwrap dictionary encoding: look up the decoded value and recurse
			return fip.convertArrowValue (typedCol.Dictionary (), typedCol.GetValueIndex (rowIdx), colIdx)

		case *array.List:
			return fip.convertList (typedCol, rowIdx)

		case *array.FixedSizeList:
			return fip.convertFixedSizeList (typedCol, rowIdx)

		default:
			// Fatal error - unsupported column types must stop processing immediately
			err = fmt.Errorf ("unsupported Arrow column type: %T", typedCol)
			return TeradataValue { m_err : err }, 0, false, err

	} // end switch on column type

} // end convertArrowValue

func (fip *fileImporterParquet) convertDate32 (col *array.Date32, rowIdx int) (teraval TeradataValue, nFieldByteSize int, bParseError bool, err error) {

	// Date32 is days since Unix epoch (1970-01-01)
	// Format: YYYY-MM-DD
	val := col.Value (rowIdx)

	var parquetEpoch = time.Date (1970, 1, 1, 0, 0, 0, 0, time.UTC)

	d := parquetEpoch.AddDate (0, 0, int (val))

	year, month, day := d.Date ()
	// Go's time allows year >= 0001, which we need (e.g., 0001-01-01).
	// Optional sanity checks (Gregorian range):
	if month < time.January || month > time.December {
		return TeradataValue { m_err : fip.parseError ("computed month out of range") }, 0, true, fip.parseError ("computed month out of range")
	}

	td := int64 ((year-1900) * 10000 + int (month) * 100 + day)

	// Bounds check: Teradata INTEGER DATE fits safely in int32
	if td < -2147483648 || td > 2147483647 {
		return TeradataValue { m_err : fip.parseError ("Teradata INTEGER DATE overflows int32") }, 0, true, fip.parseError ("Teradata INTEGER DATE overflows int32")
	}

	teraval = NewTeradataValueDateInteger (fourBytes (uint32 (td)))
	nFieldByteSize = len (teraval.m_abyValue)
	return

} // end convertDate32

func (fip *fileImporterParquet) convertTimestamp (col arrow.Array, typedCol *array.Timestamp, rowIdx int, colIdx int) (teraval TeradataValue, nFieldByteSize int, bParseError bool, err error) {

	// Timestamp - format as YYYY-MM-DD HH:MM:SS.ffffff
	// Arrow Timestamp is time since Unix epoch (1970-01-01 00:00:00 UTC)
	// Precision varies by Arrow timestamp unit (seconds, milliseconds, microseconds, nanoseconds)
	val := typedCol.Value (rowIdx)
	tsType := col.DataType().(*arrow.TimestampType)
	var t time.Time

	switch tsType.Unit {
		case arrow.Second:
			t = time.Unix (int64(val), 0).UTC ()
		case arrow.Millisecond:
			t = time.Unix (int64(val)/1000, (int64(val)%1000)*1000000).UTC ()
		case arrow.Microsecond:
			t = time.Unix (int64(val)/1000000, (int64(val)%1000000)*1000).UTC ()
		case arrow.Nanosecond:
			t = time.Unix (int64(val)/1000000000, int64(val)%1000000000).UTC ()
		default:
			err := fip.parseError (fmt.Sprintf ("unsupported timestamp unit: %v", tsType.Unit))
			return TeradataValue { m_err : err }, 0, true, err
	}

	// Determine precision from parameter metadata if available
	precision := 6 // default to microseconds
	if paramMeta, ok := fip.m_mapParameterDataTypes[colIdx + 1] ; ok {
		precision = int (paramMeta.m_uScale)
	}

	// Format with appropriate precision
	var tsStr string
	if precision == 0 {
		tsStr = t.Format ("2006-01-02 15:04:05")
	} else if precision <= 6 {
		// Format with requested precision (truncate fractional seconds)
		baseStr := t.Format ("2006-01-02 15:04:05")
		nanos := t.Nanosecond()
		// Convert nanoseconds to requested precision
		divisor := int64(math.Pow10(9 - precision))
		truncated := nanos / int(divisor)
		fracFormat := fmt.Sprintf(".%%0%dd", precision)
		tsStr = baseStr + fmt.Sprintf(fracFormat, truncated)
	} else {
		// precision > 6, format with full microsecond precision (max 6 for Teradata)
		tsStr = t.Format ("2006-01-02 15:04:05.000000")
	}

	teraval = NewTeradataValueTimestamp (tsStr)
	nFieldByteSize = len (tsStr)
	return

} // end convertTimestamp

func (fip *fileImporterParquet) convertTime32 (col arrow.Array, typedCol *array.Time32, rowIdx int, colIdx int) (teraval TeradataValue, nFieldByteSize int, bParseError bool, err error) {

	// Time32 - seconds or milliseconds since midnight
	// Format: HH:MM:SS[.fff] (precision depends on unit and target column)
	val := typedCol.Value (rowIdx)
	timeType := col.DataType().(*arrow.Time32Type)
	var timeStr string

	// Determine precision from parameter metadata if available
	precision := 0 // default for seconds
	if timeType.Unit == arrow.Millisecond {
		precision = 3 // default for milliseconds
	}
	if paramMeta, ok := fip.m_mapParameterDataTypes[colIdx + 1]; ok {
		precision = int(paramMeta.m_uScale)
	}

	if timeType.Unit == arrow.Second {
		hours := val / 3600
		minutes := (val % 3600) / 60
		seconds := val % 60
		if precision == 0 {
			timeStr = fmt.Sprintf ("%02d:%02d:%02d", hours, minutes, seconds)
		} else {
			// For seconds-based Time32, fractional seconds are 0
			fracFormat := fmt.Sprintf("%%0%dd", precision)
			timeStr = fmt.Sprintf ("%02d:%02d:%02d." + fracFormat, hours, minutes, seconds, 0)
		}
	} else { // milliseconds
		ms := int64(val)
		hours := (ms / (60 * 60 * 1000)) % 24
		minutes := (ms / (60 * 1000)) % 60
		seconds := (ms / 1000) % 60
		millis := ms % 1000

		if precision == 0 {
			timeStr = fmt.Sprintf ("%02d:%02d:%02d", hours, minutes, seconds)
		} else if precision <= 3 {
			// Truncate milliseconds to requested precision
			truncated := millis / int64(math.Pow10(3 - precision))
			fracFormat := fmt.Sprintf("%%0%dd", precision)
			timeStr = fmt.Sprintf ("%02d:%02d:%02d." + fracFormat, hours, minutes, seconds, truncated)
		} else {
			// Pad with zeros if more precision requested than available
			fracFormat := fmt.Sprintf("%%0%dd", precision)
			paddedMillis := millis * int64(math.Pow10(precision - 3))
			timeStr = fmt.Sprintf ("%02d:%02d:%02d." + fracFormat, hours, minutes, seconds, paddedMillis)
		}
	}

	teraval = NewTeradataValueTime (timeStr)
	nFieldByteSize = len (timeStr)
	return

} // end convertTime32

func (fip *fileImporterParquet) convertTime64 (col arrow.Array, typedCol *array.Time64, rowIdx int, colIdx int) (teraval TeradataValue, nFieldByteSize int, bParseError bool, err error) {

	// Time64 - microseconds or nanoseconds since midnight
	// Format: HH:MM:SS.ffffff (precision depends on target column)
	val := typedCol.Value (rowIdx)
	timeType := col.DataType().(*arrow.Time64Type)
	var timeStr string

	// Determine precision from parameter metadata if available
	precision := 6 // default to microseconds
	if paramMeta, ok := fip.m_mapParameterDataTypes [colIdx + 1] ; ok {
		precision = int(paramMeta.m_uScale)
	}

	if timeType.Unit == arrow.Microsecond {
		us := int64(val)
		hours := (us / (60 * 60 * 1000000)) % 24
		minutes := (us / (60 * 1000000)) % 60
		seconds := (us / 1000000) % 60
		micros := us % 1000000

		// Format with appropriate precision
		if precision == 0 {
			timeStr = fmt.Sprintf ("%02d:%02d:%02d", hours, minutes, seconds)
		} else if precision <= 6 {
			// Truncate microseconds to requested precision
			divisor := int64(1)
			for i := precision; i < 6; i++ {
				divisor *= 10
			}
			truncatedMicros := (micros / divisor) * divisor
			formatStr := fmt.Sprintf ("%%02d:%%02d:%%02d.%%0%dd", precision)
			timeStr = fmt.Sprintf (formatStr, hours, minutes, seconds, truncatedMicros / divisor)
		} else {
			timeStr = fmt.Sprintf ("%02d:%02d:%02d.%06d", hours, minutes, seconds, micros)
		}
	} else { // nanoseconds
		ns := int64(val)
		hours := (ns / (60 * 60 * 1000000000)) % 24
		minutes := (ns / (60 * 1000000000)) % 60
		seconds := (ns / 1000000000) % 60
		nanos := ns % 1000000000

		if precision == 0 {
			timeStr = fmt.Sprintf ("%02d:%02d:%02d", hours, minutes, seconds)
		} else if precision <= 9 {
			// Truncate nanoseconds to requested precision
			divisor := int64(1)
			for i := precision; i < 9; i++ {
				divisor *= 10
			}
			truncatedNanos := (nanos / divisor) * divisor
			formatStr := fmt.Sprintf ("%%02d:%%02d:%%02d.%%0%dd", precision)
			timeStr = fmt.Sprintf (formatStr, hours, minutes, seconds, truncatedNanos / divisor)
		} else {
			timeStr = fmt.Sprintf ("%02d:%02d:%02d.%09d", hours, minutes, seconds, nanos)
		}
	}

	teraval = NewTeradataValueTime (timeStr)
	nFieldByteSize = len (timeStr)
	return

} // end convertTime64

func (fip *fileImporterParquet) convertDecimal128 (col arrow.Array, typedCol *array.Decimal128, rowIdx int) (teraval TeradataValue, nFieldByteSize int, bParseError bool, err error) {

	// Decimal - arbitrary precision fixed-point decimal
	// Arrow Decimal128 value - convert to byte array with proper scale
	decType := col.DataType().(*arrow.Decimal128Type)
	val := typedCol.Value (rowIdx)

	// Get the underlying big integer value and convert to byte slice
	bigInt := val.BigInt()
	aby := convertBigIntToByteSlice (bigInt)

	// Use existing adjustDecimalLength to pad to required byte length (1, 2, 4, 8, or 16 bytes)
	aby = adjustDecimalLength (aby, uint8(decType.Precision), uint8(decType.Scale))

	teraval = NewTeradataValueDecimal (aby, uint8(decType.Scale))
	nFieldByteSize = len(aby)
	return

} // end convertDecimal128

func (fip *fileImporterParquet) convertDecimal256 (col arrow.Array, typedCol *array.Decimal256, rowIdx int) (teraval TeradataValue, nFieldByteSize int, bParseError bool, err error) {

	// Decimal256 - arbitrary precision fixed-point decimal
	// Arrow Decimal256 value - convert to byte array with proper scale
	// Teradata supports DECIMAL up to precision 38; reject anything larger
	decType := col.DataType().(*arrow.Decimal256Type)
	if decType.Precision > 38 {
		err = fip.parseError (fmt.Sprintf ("Decimal256 precision %d exceeds Teradata maximum of 38", decType.Precision))
		return TeradataValue { m_err : err }, 0, false, err
	}

	// Get the underlying big integer value and convert to byte slice
	val := typedCol.Value (rowIdx)
	bigInt := val.BigInt ()
	aby := convertBigIntToByteSlice (bigInt)

	// Use existing adjustDecimalLength to pad to required byte length (1, 2, 4, 8, or 16 bytes)
	aby = adjustDecimalLength (aby, uint8(decType.Precision), uint8(decType.Scale))

	teraval = NewTeradataValueDecimal (aby, uint8(decType.Scale))
	nFieldByteSize = len (aby)
	return

} // end convertDecimal256

// convertListElements converts a range of list elements to a comma-separated VARCHAR string.
// Supports: BOOL, INT8, INT16, INT32, INT64, UINT8, UINT16, UINT32, UINT64,
//           FLOAT32, FLOAT64, STRING, LARGE_STRING,
//           DECIMAL128, DECIMAL256, DATE32, TIME32, TIME64, TIMESTAMP
func (fip *fileImporterParquet) convertListElements (elemArray arrow.Array, elemType arrow.DataType, start, end int, listTypeName string) (teraval TeradataValue, nFieldByteSize int, bParseError bool, err error) {

	// Unwrap dictionary encoding on the element array
	if dictArray, ok := elemArray.(*array.Dictionary) ; ok {
		elemType = dictArray.Dictionary ().DataType ()
		elemArray = dictArray.Dictionary ()
	}

	var elements [] string
	for i := start ; i < end ; i++ {
		if elemArray.IsNull (i) {
			elements = append (elements, "NULL")
			continue
		}

		var elemStr string
		switch elemType.ID () {

			case arrow.BOOL:
				if elemArray.(*array.Boolean).Value (i) {
					elemStr = "true"
				} else {
					elemStr = "false"
				}

			case arrow.INT8:
				elemStr = fmt.Sprintf ("%d", elemArray.(*array.Int8).Value (i))

			case arrow.INT16:
				elemStr = fmt.Sprintf ("%d", elemArray.(*array.Int16).Value (i))

			case arrow.INT32:
				elemStr = fmt.Sprintf ("%d", elemArray.(*array.Int32).Value (i))

			case arrow.INT64:
				elemStr = fmt.Sprintf ("%d", elemArray.(*array.Int64).Value (i))

			case arrow.UINT8:
				elemStr = fmt.Sprintf ("%d", elemArray.(*array.Uint8).Value (i))

			case arrow.UINT16:
				elemStr = fmt.Sprintf ("%d", elemArray.(*array.Uint16).Value (i))

			case arrow.UINT32:
				elemStr = fmt.Sprintf ("%d", elemArray.(*array.Uint32).Value (i))

			case arrow.UINT64:
				elemStr = fmt.Sprintf ("%d", elemArray.(*array.Uint64).Value (i))

			case arrow.FLOAT32:
				elemStr = fmt.Sprintf ("%.6g", elemArray.(*array.Float32).Value (i))

			case arrow.FLOAT64:
				elemStr = fmt.Sprintf ("%.6g", elemArray.(*array.Float64).Value (i))

			case arrow.STRING:
				elemStr = elemArray.(*array.String).Value (i)

			case arrow.LARGE_STRING:
				elemStr = elemArray.(*array.LargeString).Value (i)

			case arrow.DECIMAL128:
				elemStr = elemArray.(*array.Decimal128).ValueStr (i)

			case arrow.DECIMAL256:
				elemStr = elemArray.(*array.Decimal256).ValueStr (i)

			case arrow.DATE32:
				val := elemArray.(*array.Date32).Value (i)
				d := time.Date (1970, 1, 1, 0, 0, 0, 0, time.UTC).AddDate (0, 0, int (val))
				elemStr = d.Format ("2006-01-02")

			case arrow.TIME32:
				val := int64 (elemArray.(*array.Time32).Value (i))
				timeType := elemType.(*arrow.Time32Type)
				if timeType.Unit == arrow.Second {
					h, m, s := val/3600, (val%3600)/60, val%60
					elemStr = fmt.Sprintf ("%02d:%02d:%02d", h, m, s)
				} else { // Millisecond
					h := (val / (3600 * 1000)) % 24
					m := (val / (60 * 1000)) % 60
					s := (val / 1000) % 60
					ms := val % 1000
					elemStr = fmt.Sprintf ("%02d:%02d:%02d.%03d", h, m, s, ms)
				}

			case arrow.TIME64:
				val := int64 (elemArray.(*array.Time64).Value (i))
				timeType := elemType.(*arrow.Time64Type)
				if timeType.Unit == arrow.Microsecond {
					h := (val / (3600 * 1000000)) % 24
					m := (val / (60 * 1000000)) % 60
					s := (val / 1000000) % 60
					us := val % 1000000
					elemStr = fmt.Sprintf ("%02d:%02d:%02d.%06d", h, m, s, us)
				} else { // Nanosecond
					h := (val / (3600 * 1000000000)) % 24
					m := (val / (60 * 1000000000)) % 60
					s := (val / 1000000000) % 60
					ns := val % 1000000000
					elemStr = fmt.Sprintf ("%02d:%02d:%02d.%09d", h, m, s, ns)
				}

			case arrow.TIMESTAMP:
				val := int64 (elemArray.(*array.Timestamp).Value (i))
				tsType := elemType.(*arrow.TimestampType)
				var t time.Time
				switch tsType.Unit {
					case arrow.Second:
						t = time.Unix (val, 0).UTC ()
						elemStr = t.Format ("2006-01-02 15:04:05")
					case arrow.Millisecond:
						t = time.Unix (val/1000, (val%1000)*1000000).UTC ()
						elemStr = t.Format ("2006-01-02 15:04:05.000")
					case arrow.Microsecond:
						t = time.Unix (val/1000000, (val%1000000)*1000).UTC ()
						elemStr = t.Format ("2006-01-02 15:04:05.000000")
					case arrow.Nanosecond:
						t = time.Unix (val/1000000000, val%1000000000).UTC ()
						elemStr = t.Format ("2006-01-02 15:04:05.000000000")
					default:
						err = fip.parseError (fmt.Sprintf ("%s TIMESTAMP element has unsupported unit %v", listTypeName, tsType.Unit))
						return TeradataValue { m_err : err }, 0, true, err
				}

			default:
				err = fip.parseError (fmt.Sprintf ("%s element type %v not supported", listTypeName, elemType))
				return TeradataValue { m_err : err }, 0, true, err

		} // end switch on element type

		elements = append (elements, elemStr)

	} // end for loop over list elements

	listStr := strings.Join (elements, ",")
	teraval = newTeradataValue (T_VARCHAR, 0, [] byte (listStr))
	nFieldByteSize = len (listStr)
	return

} // end convertListElements

func (fip *fileImporterParquet) convertList (listArray *array.List, rowIdx int) (teraval TeradataValue, nFieldByteSize int, bParseError bool, err error) {

	// List type - convert to comma-separated VARCHAR string
	// Note: List-level NULL is already handled in the switch at line 326
	// Lists are always inserted as strings; VECTOR columns may return binary but accept string input

	start, end := listArray.ValueOffsets (rowIdx)
	elemArray := listArray.ListValues ()
	elemType := listArray.DataType().(*arrow.ListType).Elem ()

	return fip.convertListElements (elemArray, elemType, int (start), int (end), "List")

} // end convertList

func (fip *fileImporterParquet) convertFixedSizeList (fixedListArray *array.FixedSizeList, rowIdx int) (teraval TeradataValue, nFieldByteSize int, bParseError bool, err error) {

	// FixedSizeList type - convert to comma-separated VARCHAR string
	// Note: List-level NULL is already handled in the switch at line 326
	// Lists are always inserted as strings; VECTOR columns may return binary but accept string input

	listSize := fixedListArray.DataType ().(*arrow.FixedSizeListType).Len ()
	start := rowIdx * int (listSize)
	end := start + int (listSize)

	elemArray := fixedListArray.ListValues ()
	elemType := fixedListArray.DataType ().(*arrow.FixedSizeListType).Elem ()

	return fip.convertListElements (elemArray, elemType, start, end, "FixedSizeList")

} // end convertFixedSizeList

// getColumnDataType returns the arrow type, Teradata bind type code, Teradata type cast name, and byte count
// for the Parquet column. It uses fip.m_schema to determine the Arrow type.
// For fixed-width types the byte count is exact; for variable-width types (VARCHAR, VARBYTE)
// and for TIME/TIMESTAMP (whose byte count comes from column metadata) it returns 0.
func (fip *fileImporterParquet) getColumnDataType (nColIndex int) (arrow.DataType, uint16, string, uint64) {

	if fip.m_schema == nil || nColIndex < 1 || nColIndex > fip.m_schema.NumFields () {
		return nil, T_VARCHAR, "VARCHAR", uint64 (0)
	}

	arrowType := fip.m_schema.Field (nColIndex - 1).Type

	switch arrowType.ID () {

		case arrow.INT8, arrow.BOOL:
			return arrowType, T_BYTEINT, "BYTEINT", uint64 (1)

		case arrow.INT16, arrow.UINT8:
			return arrowType, T_SMALLINT, "SMALLINT", uint64 (2)

		case arrow.INT32, arrow.UINT16:
			return arrowType, T_INTEGER, "INTEGER", uint64 (4)

		case arrow.INT64, arrow.UINT32:
			return arrowType, T_BIGINT, "BIGINT", uint64 (8)

		case arrow.FLOAT32, arrow.FLOAT64:
			return arrowType, T_FLOAT, "FLOAT", uint64 (8)

		case arrow.STRING, arrow.LARGE_STRING:
			return arrowType, T_VARCHAR, "VARCHAR", uint64 (0)

		case arrow.BINARY, arrow.LARGE_BINARY:
			return arrowType, T_VARBYTE, "VARBYTE", uint64 (0)

		case arrow.DECIMAL128:
			decType := arrowType.(*arrow.Decimal128Type)
			nByteCount := byteCountFromDecimalPrecision (uint8 (decType.Precision))
			return arrowType, T_DECIMAL, fmt.Sprintf ("DECIMAL(%v,%v)", decType.Precision, decType.Scale), nByteCount

		case arrow.DECIMAL256:
			decType := arrowType.(*arrow.Decimal256Type)
			if decType.Precision > 38 {
				// Precision exceeds Teradata maximum — return VARCHAR so that
				// getTeradataTypeCastParquet produces a clean type-mismatch error at setup time
				return arrowType, T_VARCHAR, "VARCHAR", uint64 (0)
			}
			nByteCount := byteCountFromDecimalPrecision (uint8 (decType.Precision))
			return arrowType, T_DECIMAL, fmt.Sprintf ("DECIMAL(%v,%v)", decType.Precision, decType.Scale), nByteCount

		case arrow.DATE32:
			return arrowType, T_DATE_INTEGER, "DATE", uint64 (4)

		case arrow.TIME32, arrow.TIME64:
			// Byte count comes from column metadata (colByteCount), not Arrow type
			return arrowType,T_TIME, "TIME", uint64 (0)

		case arrow.TIMESTAMP:
			// Byte count comes from column metadata (colByteCount), not Arrow type
			return arrowType, T_TIMESTAMP, "TIMESTAMP", uint64 (0)

		default:
			// Fallback: intervals, period types, and unknown types are stored as strings in Parquet
			return arrowType, T_VARCHAR, "VARCHAR", uint64 (0)

	} // end switch arrowType.ID

} // end getColumnDataType
