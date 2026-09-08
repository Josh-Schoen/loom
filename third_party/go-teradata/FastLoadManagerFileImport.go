// Copyright 2019 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"context"
	"database/sql/driver"
	"fmt"
	"math"

	"github.com/apache/arrow-go/v18/arrow"
)

var _ fastLoadManager = &fastLoadManagerFileImport {} // implements fastLoadManager

type fastLoadManagerFileImport struct {
	*fastLoadManagerBase
	m_fileImporter fileImporter
}

func newFastLoadManagerFileImport (fldManager *fastLoadManagerBase, fileImporter fileImporter) (fastLoadManager, error) {

	var err error
	fldFileManager := &fastLoadManagerFileImport { fastLoadManagerBase : fldManager }

	// Set the file importer before calling getTeradataTypeCast so type detection works
	fldFileManager.m_fileImporter = fileImporter

	// Calculate the maximum IndicData parcel size, excluding the parcel header
	// Add 1 for RowNumber parameter and add 4 for RowNumber INTEGER
	anMaxDataParcelSize := [] uint64 { uint64 (((len (fldFileManager.m_preparedRows.m_aParamMetadata) + 1 + 7) / 8) + 4) }

	var unquotedDbName string
	var unquotedTableName string

	fldFileManager.m_aBindMetadata = make ([] bindMetadata, len (fldFileManager.m_preparedRows.m_aParamMetadata))

	usingClause := "USING "
	insertStmt := " INSERT INTO "
	valuesList := " VALUES ( "
	for i := 0 ; i < len (fldFileManager.m_preparedRows.m_aParamMetadata) ; i++ {
		colMetadata := fldFileManager.m_preparedRows.m_aParamMetadata [i]

		if (i == 0) {
			unquotedDbName = colMetadata.M_sDatabaseName
			unquotedTableName = colMetadata.M_sObjectName
			fldFileManager.m_sTableName = doubleQuote (unquotedDbName) + "." + doubleQuote (unquotedTableName)
			insertStmt += doubleQuote (unquotedDbName) + "." + doubleQuote (unquotedTableName) + " ( "
		} else {
			usingClause += ", "
			insertStmt  += ", "
			valuesList  += ", "
		}

		fldFileManager.patchColumnMetadata (&colMetadata, i, /*aBindValues*/ nil)

		var colTypeCast string
		colTypeCast, err = fldFileManager.getTeradataTypeCast (i + 1, colMetadata, anMaxDataParcelSize)

		if err != nil || colTypeCast == "" {
			for j := range fldFileManager.m_fastloadCon {
				fldFileManager.m_fastloadCon[j].m_fastloadBindMetadata = nil
			}
			return fldFileManager, err
		}

		unquotedColumnName := colMetadata.M_sName
		usingClause += doubleQuote ("F_" + unquotedColumnName) + " (" + colTypeCast + ")"
		insertStmt  += doubleQuote (unquotedColumnName)
		valuesList  += ":" + doubleQuote ("F_" + unquotedColumnName)
	}

	insertStmt += " )"
	valuesList += " )"

	if !(fldFileManager.m_appCon.m_config.M_uMaxUsingDataSizeInBytes > uint32 (64230)) && anMaxDataParcelSize[0] > 64000 {

		err = fldFileManager.m_controlSession.makeDriverError (nil, 509,
			fmt.Sprintf ("Cannot FastLoad because the potential row data size of %v bytes exceeds 64000 bytes",
				anMaxDataParcelSize [0]))
	}

	fldFileManager.m_sUsingInsertValues = usingClause + insertStmt + valuesList

	errTableDbase := func ()(string) { if fldFileManager.m_appRows.m_sErrorTableDatabase != "" { return fldFileManager.m_appRows.m_sErrorTableDatabase } ; return unquotedDbName } ()
	fldFileManager.m_sTableNameErr1 = doubleQuote (errTableDbase) + "." + doubleQuote (unquotedTableName + fldFileManager.m_appRows.m_sErrorTable1Suffix)
	fldFileManager.m_sTableNameErr2 = doubleQuote (errTableDbase) + "." + doubleQuote (unquotedTableName + fldFileManager.m_appRows.m_sErrorTable2Suffix)
	fldFileManager.m_sBeginLoading = "BEGIN LOADING " + fldFileManager.m_sTableName + " ERRORFILES " + fldFileManager.m_sTableNameErr1 + ", " + fldFileManager.m_sTableNameErr2 + " WITH INTERVAL"

	return fldFileManager, err

}  // end method newFastLoadManagerFileImport

func (fldFileManager *fastLoadManagerFileImport) setFileImporter (vals fileImporter) { fldFileManager.m_fileImporter = vals }

func (fldFileManager *fastLoadManagerFileImport) Value () fastLoadManager { return fldFileManager }

func (fldFileManager *fastLoadManagerFileImport) executeInsert (activityCount *uint64, aBindValues [] driver.NamedValue, ctx context.Context) (err error) {

	var numRowsToSend uint64
	var uFirstRowNumber uint64
	var numRowsSent uint64

	if fldFileManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter fastLoadManagerFileImport.executeInsert m_sTableName=%v m_uNumTotalRowsSent=%v getErrorCount=%v len(m_asWarnings)=%v %v",
			fldFileManager.m_sTableName, fldFileManager.m_uNumTotalRowsSent, fldFileManager.getErrorCount (), len (fldFileManager.m_asWarnings), fldFileManager))
		defer func () { traceLog (fmt.Sprintf ("< leave fastLoadManagerFileImport.executeInsert m_sTableName=%v m_bAlreadyAutoCommitted=%v m_bBatchInsert=%v numRowsToSend=%v uFirstRowNumber=%v numRowsSent=%v m_uNumTotalRowsSent=%v getErrorCount=%v len(m_asWarnings)=%v %v",
			fldFileManager.m_sTableName, fldFileManager.m_bAlreadyAutoCommitted, fldFileManager.m_bBatchInsert, numRowsToSend, uFirstRowNumber, numRowsSent, fldFileManager.m_uNumTotalRowsSent, fldFileManager.getErrorCount (), len(fldFileManager.m_asWarnings), fldFileManager)) } ()
	}

	if fldFileManager.m_appCon.m_bAutoCommit {
		fldFileManager.m_bSaveErrorsWarnings = true // save errors and warnings
	}

	aaoRecords, _, _, readErr := fldFileManager.m_fileImporter.getBatchSegment ( fldFileManager.m_appRows.m_stmt.m_con.m_params.M_uMaxMessageBodySize, true /* bFastloading */ )

	if readErr != nil {
		return fldFileManager.m_appCon.makeDriverError (readErr, 543,
			fmt.Sprintf ("Cannot FastLoad because an error occured while reading data from the file %v",
				fldFileManager.m_fileImporter.getFileName ()))
	}

	aBindValues = [] driver.NamedValue {driver.NamedValue {Ordinal: 1, Value: aaoRecords}}

	fldFileManager.m_bBatchInsert = true
	numRowsToSend = uint64 (len (aaoRecords))
	uFirstRowNumber = uint64 (fldFileManager.m_nCurrentRowNumber + 1)

	err = fldFileManager.connectFastLoad (ctx)
	if err != nil { return }

	aBindValuesInternal, err := fldFileManager.distributeRows (aBindValues, numRowsToSend, uFirstRowNumber)
	if err != nil || len (fldFileManager.m_asWarningsAtTypeCast) != 0 { return }

	err = fldFileManager.beginFastLoad (ctx)
	if err != nil { return }

	for {

		for i := 0; i < len (fldFileManager.m_fastloadCon); i++ {
			if aBindValuesInternal[i] != nil {
				_, err := fldFileManager.m_fastloadCon[i].ExecContext (ctx, "{fn teradata_provide(request_scope_send_only)}", aBindValuesInternal[i])
				if err != nil { return err }
			}
		}

		for i := 0; i < len (fldFileManager.m_fastloadCon); i++ {
			if aBindValuesInternal[i] != nil {
				resFastLoadCon, err := fldFileManager.m_fastloadCon[i].ExecContext (ctx, "{fn teradata_provide(request_scope_receive_only)}", nil)
				if err != nil { return err }

				rowsAffected, err := resFastLoadCon.RowsAffected ()
				if err != nil { return err }

				numRowsSent += uint64 (rowsAffected)
			}
		}

		for i := 0 ; i < len (fldFileManager.m_fastloadCon) ; i++ {
			if aBindValuesInternal[i] != nil && fldFileManager.m_fastloadCon[i].m_nFastLoadCurrentRow > 0 {
				if fldFileManager.m_fastloadCon[i].m_nFastLoadCurrentRow > len(aBindValuesInternal[i][0].Value.([][]driver.NamedValue)) {
					aBindValuesInternal [i][0].Value = aBindValuesInternal[i][0].Value.([][]driver.NamedValue)[0:]
				} else {
					aBindValuesInternal [i][0].Value = aBindValuesInternal[i][0].Value.([][]driver.NamedValue)[fldFileManager.m_fastloadCon[i].m_nFastLoadCurrentRow : ]
				}
			} else {
				aBindValuesInternal [i] = nil
			}
		}

		bDoneLoadingSegment := true
		for i := 0 ; i < len (fldFileManager.m_fastloadCon) ; i++ {
			if aBindValuesInternal [i] != nil {
				bDoneLoadingSegment = false
				break
			}
		}

		if bDoneLoadingSegment {

			aaoRecords, _, _, readErr := fldFileManager.m_fileImporter.getBatchSegment ( fldFileManager.m_appRows.m_stmt.m_con.m_params.M_uMaxMessageBodySize, true /* bFastloading */ )

			if readErr != nil {
				return fldFileManager.m_appCon.makeDriverError (readErr, 543,
					fmt.Sprintf ("Cannot FastLoad because an error occured while reading data from the file %v",
						fldFileManager.m_fileImporter.getFileName ()))
			}

			if len (aaoRecords) == 0 { break }

			aBindValues [0] = driver.NamedValue {Ordinal : 1, Value : aaoRecords }
			numNewRowsToSend := uint64 (len (aaoRecords))
			numRowsToSend += numNewRowsToSend

			aBindValuesInternal, err = fldFileManager.distributeRows (aBindValues, numNewRowsToSend, uFirstRowNumber)
			if err != nil || len (fldFileManager.m_asWarningsAtTypeCast) != 0 { return }

		} // end if bDoneLoadingSegment

	} // end for

	var errSave error
	if numRowsToSend != numRowsSent && fldFileManager.m_appRows.m_bManageErrorTables {
		errSave = fldFileManager.verifyFastLoadErrorTable1RowCount (numRowsToSend - numRowsSent, uFirstRowNumber, ctx)
		if errSave == nil {
			errSave = fldFileManager.selectFastLoadErrorTable1Rows (numRowsToSend, numRowsSent, uFirstRowNumber, ctx)
		}
		fmt.Println ("DEBUG: numRowsToSend=", numRowsToSend, " numRowsSent=", numRowsSent, " errSave=", errSave)
	}

	fldFileManager.m_uNumTotalRowsSent += numRowsSent
	*activityCount = numRowsSent

	if fldFileManager.m_appCon.m_bAutoCommit {
		fmt.Println ("fastLoadManagerFileImport.executeInsert auto-committing after sending ", numRowsSent, " rows to FastLoad")
		fldFileManager.m_bAlreadyAutoCommitted = true
		err = fldFileManager.endFastLoad (ctx)
		if err != nil { return chainErrors (errSave, err) }
	}

	return errSave

} // end method executeInsert

func (fldFileManager *fastLoadManagerFileImport) distributeRows (aBindValues [] driver.NamedValue, numRowsToSend uint64, uFirstRowNumber uint64) (aBindValuesInternal [][] driver.NamedValue, err error) {

	aBindValuesInternal = make([][]driver.NamedValue, len(fldFileManager.m_fastloadCon))
	numRowsInFastLoadCon := make([]int, len(fldFileManager.m_fastloadCon))
	numExpectedBoundParameterValues := len(fldFileManager.m_preparedRows.m_aParamMetadata)
	var numActualBoundParameterValues int
	var numFastloadConsToBeUsed int

	if fldFileManager.m_appCon.hasTraceLog () {
		traceLog(fmt.Sprintf("> enter fastLoadManagerFileImport.distributeRows m_sTableName=%v numExpectedBoundParameterValues=%v len(m_fastloadCon)=%v numRowsToSend=%v uFirstRowNumber=%v",
			fldFileManager.m_sTableName, numExpectedBoundParameterValues, len(fldFileManager.m_fastloadCon), numRowsToSend, uFirstRowNumber))
		defer func () { traceLog(fmt.Sprintf("< leave fastLoadManagerFileImport.distributeRows m_sTableName=%v numActualBoundParameterValues=%v numFastloadConsToBeUsed=%v numRowsInFastLoadCon=%v",
			fldFileManager.m_sTableName, numActualBoundParameterValues, numFastloadConsToBeUsed, numRowsInFastLoadCon)) } ()
	}

	fldFileManager.m_preparedRows.m_bSendSIP = false // force intervals, date, times and timestamps to convert to VARCHAR

	if fldFileManager.m_appCon.hasDebugLog () {
		for i := range fldFileManager.m_aBindMetadata {
			debugLog(fmt.Sprintf("fastLoadManagerFileImport.distributeRows m_sTableName=%v m_aBindMetadata[%v]=%v", fldFileManager.m_sTableName, i, fldFileManager.m_aBindMetadata[i]))
		}
	}

	aaBatchValues := aBindValues [0].Value.([][] driver.NamedValue)
	for i := 0; i < len(aaBatchValues); i++ {

		numActualBoundParameterValues = len(aaBatchValues[i])

		if numExpectedBoundParameterValues != numActualBoundParameterValues {

			return nil, fldFileManager.m_controlSession.makeDriverError (nil, 513,
				fmt.Sprintf ("Batched row %v actual bound parameter count %v differs from expected bound parameter count %v",
					i + 1,
					numActualBoundParameterValues,
					numExpectedBoundParameterValues))
		}

		if i == 0 {
			if fldFileManager.m_fastloadCon[0].m_fastloadBindMetadata != nil {
				for j := range fldFileManager.m_fastloadCon[0].m_fastloadBindMetadata {

					actualCookedDataType   := fldFileManager.m_aBindMetadata[j].m_uCookedDataType
					expectedCookedDataType := fldFileManager.m_fastloadCon[0].m_fastloadBindMetadata[j].m_uCookedDataType

					if actualCookedDataType != expectedCookedDataType {

						return nil, fldFileManager.m_controlSession.makeDriverError (nil, 515,
							fmt.Sprintf ("Bound parameter %v type %v (%v) differs from expected type %v (%v)",
								j + 1,
								fldFileManager.getTeradataTypeName (actualCookedDataType),
								actualCookedDataType,
								fldFileManager.getTeradataTypeName (expectedCookedDataType),
								expectedCookedDataType))
					}
				}
			} else {
				for j := range fldFileManager.m_fastloadCon {
					fldFileManager.m_fastloadCon[j].m_fastloadBindMetadata = fldFileManager.m_aBindMetadata
				}
				if len(fldFileManager.m_asWarningsAtTypeCast) != 0 {
					for j := range fldFileManager.m_asWarningsAtTypeCast {
						fldFileManager.m_asWarnings = append(fldFileManager.m_asWarnings, fldFileManager.m_asWarningsAtTypeCast[j])
					}
					err = fldFileManager.closeFastLoad()
					return
				}
			}
		}

		if aBindValuesInternal[fldFileManager.m_nFastLoadConIndex] == nil {

			aBindValuesInternal[fldFileManager.m_nFastLoadConIndex] = make([]driver.NamedValue, 1)
			aaBatchValuesInternal := make([][]driver.NamedValue, 1)
			copy(aaBatchValuesInternal, aaBatchValues[i:(i+1)])
			aBindValuesInternal[fldFileManager.m_nFastLoadConIndex][0] = driver.NamedValue{Ordinal:1, Value:aaBatchValuesInternal}

		} else {

			aaBatchValuesInternal := aBindValuesInternal[fldFileManager.m_nFastLoadConIndex][0].Value.([][]driver.NamedValue)
			aaBatchValuesInternal = append(aaBatchValuesInternal, aaBatchValues[i:(i+1)]...)
			aBindValuesInternal[fldFileManager.m_nFastLoadConIndex][0].Value = aaBatchValuesInternal
		}

		aaBatchValuesInternal := aBindValuesInternal[fldFileManager.m_nFastLoadConIndex][0].Value.([][]driver.NamedValue)
		idx := len(aaBatchValuesInternal)-1

		if fldFileManager.m_appCon.hasDebugLog () {
			for j := 0; j < len(aaBatchValuesInternal[idx]); j++ {
				var metadata string
				if j < len(fldFileManager.m_aBindMetadata) {
					metadata = fmt.Sprintf("m_aBindMetadata[%v]=%v", j, fldFileManager.m_aBindMetadata[j])
				} else {
					metadata = "appended RowNumber"
				}
				str := fmt.Sprintf("%v", aaBatchValuesInternal[idx][j])
				strlen := int(math.Min(float64(50), float64(len(str))))
				debugLog(fmt.Sprintf("fastLoadManagerFileImport.distributeRows m_sTableName=%v m_nFastLoadConIndex=%v %v %T%v aaBatchValuesInternal[%v][%v]=%T%v (len=%v)",
					fldFileManager.m_sTableName, fldFileManager.m_nFastLoadConIndex, metadata, aaBatchValuesInternal[idx][j].Value,
					fldFileManager.getTeradataValueTypeName(aaBatchValuesInternal[idx][j].Value), idx, j, aaBatchValuesInternal[idx][j], str[:strlen], len(str)))
			}
		}

		numRowsInFastLoadCon [fldFileManager.m_nFastLoadConIndex]++
		fldFileManager.m_nFastLoadConIndex++
		if fldFileManager.m_nFastLoadConIndex >= len(fldFileManager.m_fastloadCon) {
			fldFileManager.m_nFastLoadConIndex = 0
		}
	}

	for i := range numRowsInFastLoadCon {
		if numRowsInFastLoadCon[i] > 0 {
			numFastloadConsToBeUsed++
		}
	}

	return

} // end method distributeRows

func (fldFileManager *fastLoadManagerFileImport) getTeradataTypeCast (col int, colMetadata columnMetadata, anMaxDataParcelSize [] uint64) (colTypeCast string, err error) {

	if fldFileManager.m_fileImporter == nil {
		return "", fldFileManager.m_appCon.makeDriverError (nil, 511,
			"Cannot FastLoad because file importer is not set")
	}

	// Determine file type and delegate to appropriate method
	switch fldFileManager.m_fileImporter.getFileType () {

		case CSV_FILE_TYPE:
			return fldFileManager.getTeradataTypeCastCSV (col, colMetadata, anMaxDataParcelSize)

		case PARQUET_FILE_TYPE:
			return fldFileManager.getTeradataTypeCastParquet (col, colMetadata, anMaxDataParcelSize)

		default:
			return "", fldFileManager.m_appCon.makeDriverError (nil, 511,
				fmt.Sprintf ("Cannot FastLoad because file type %v is not supported", fldFileManager.m_fileImporter.getFileType ()))
	}

} // end method getTeradataTypeCast

func (fldFileManager *fastLoadManagerFileImport) getTeradataTypeCastCSV (col int, colMetadata columnMetadata, anMaxDataParcelSize [] uint64) (colTypeCast string, err error) {

	var sWarning string

	colTypeName := colMetadata.M_sTypeName
	colCookedDataType := colMetadata.M_uCookedDataType
	colByteCount := colMetadata.M_uByteCount
	colPrecision := colMetadata.M_uPrecision
	colScale := colMetadata.M_uScale

	if fldFileManager.m_appCon.hasTraceLog () {
		traceLog(fmt.Sprintf("> enter fastLoadManagerFileImport.getTeradataTypeCastCSV m_sTableName=%v col=%v type=%v (cookedType=%v, byteCount=%v, scale=%v) anMaxDataParcelSize[0]=%v",
			fldFileManager.m_sTableName, col, colTypeName, colCookedDataType, colByteCount, colScale, anMaxDataParcelSize[0]))
		defer func () { traceLog(fmt.Sprintf("< leave fastLoadManagerFileImport.getTeradataTypeCastCSV m_sTableName=%v col=%v colTypeCast=%v anMaxDataParcelSize[0]=%v sWarning=%v",
			fldFileManager.m_sTableName, col, colTypeCast, anMaxDataParcelSize[0], sWarning)) } ()
	}

	if fldFileManager.m_appCon.hasDebugLog () {
		debugLog(fmt.Sprintf("getTeradataTypeCastCSV incoming: m_sTableName=%v col=%v colMetadata=%+v", fldFileManager.m_sTableName, col, colMetadata))
		debugLog(fmt.Sprintf("getTeradataTypeCastCSV processed: colCookedDataType=%v colByteCount=%v", colCookedDataType, colByteCount))
	}

	switch colCookedDataType {

		case T_BIGINT,
			 T_BYTEINT,
			 T_DECIMAL, // supported as target column type, but not as bind type
			 T_FLOAT,
			 T_INTEGER,
			 T_NUMBER,
			 T_SMALLINT:

			nLength := fldFileManager.preferredVarCharLength(colCookedDataType, colPrecision, colScale)
			colTypeCast = fmt.Sprintf("VARCHAR(%v)", nLength)
			anMaxDataParcelSize[0] += uint64(nLength + 2) // plus 2-byte length
			fldFileManager.m_aBindMetadata[col-1].m_uCookedDataType = T_VARCHAR
			fldFileManager.m_aBindMetadata[col-1].m_uNullableDataType = T_VARCHAR + 1
			fldFileManager.m_aBindMetadata[col-1].m_uByteCount = uint64(nLength)

		case T_CHAR,
			 T_VARCHAR,
			 T_LONG_VARCHAR:

			// This code is specific to UTF8 and would differ for other session character sets, e.g. nLength = colByteCount for ASCII and UTF16
			nLength := minUint64(colByteCount, MAX_UTF8_VARCHAR_BYTE_LENGTH)
			colTypeCast = fmt.Sprintf("VARCHAR(%v)", nLength)
			anMaxDataParcelSize[0] += nLength + 2 // plus 2-byte length
			fldFileManager.m_aBindMetadata[col-1].m_uCookedDataType = T_VARCHAR
			fldFileManager.m_aBindMetadata[col-1].m_uNullableDataType = T_VARCHAR + 1
			fldFileManager.m_aBindMetadata[col-1].m_uByteCount = uint64(nLength)

		case T_DATE_ANSI,
			 T_DATE_INTEGER:

			colTypeCast = "VARCHAR(10)"
			anMaxDataParcelSize[0] += 10 + 2 // plus 2-byte length
			fldFileManager.m_aBindMetadata[col-1].m_uCookedDataType = T_VARCHAR
			fldFileManager.m_aBindMetadata[col-1].m_uNullableDataType = T_VARCHAR + 1
			fldFileManager.m_aBindMetadata[col-1].m_uByteCount = uint64(10)

		case T_TIME,
			 T_TIME_WITH_TIME_ZONE,
			 T_TIMESTAMP,
			 T_TIMESTAMP_WITH_TIME_ZONE,
			 T_INTERVAL_YEAR,
			 T_INTERVAL_YEAR_TO_MONTH,
			 T_INTERVAL_MONTH,
			 T_INTERVAL_DAY,
			 T_INTERVAL_DAY_TO_HOUR,
			 T_INTERVAL_DAY_TO_MINUTE,
			 T_INTERVAL_DAY_TO_SECOND,
			 T_INTERVAL_HOUR,
			 T_INTERVAL_HOUR_TO_MINUTE,
			 T_INTERVAL_HOUR_TO_SECOND,
			 T_INTERVAL_MINUTE,
			 T_INTERVAL_MINUTE_TO_SECOND,
			 T_INTERVAL_SECOND:

			colTypeCast = fmt.Sprintf("VARCHAR(%v)", colByteCount)
			anMaxDataParcelSize[0] += colByteCount + 2 // plus 2-byte length
			fldFileManager.m_aBindMetadata[col-1].m_uCookedDataType = T_VARCHAR
			fldFileManager.m_aBindMetadata[col-1].m_uNullableDataType = T_VARCHAR + 1
			fldFileManager.m_aBindMetadata[col-1].m_uByteCount = uint64(colByteCount)

		case T_PERIOD_DATE,
			 T_PERIOD_TIME,
			 T_PERIOD_TIME_WITH_TIME_ZONE,
			 T_PERIOD_TIMESTAMP,
			 T_PERIOD_TIMESTAMP_WITH_TIME_ZONE:

			digits := 0
			if colScale > 0 {
				digits = (int(colScale) + 1) * 2 // plus 1 for the decimal point '.' and times 2 for the two time/timestamp values in a period
			}

			if colCookedDataType == T_PERIOD_DATE {
				colTypeCast = fmt.Sprintf("VARCHAR(%v)", 28) // e.g. "('1970-01-02', '1973-04-05')"
				anMaxDataParcelSize[0] += 28 + 2 // plus 2-byte length
			} else if colCookedDataType == T_PERIOD_TIME {
				colTypeCast = fmt.Sprintf("VARCHAR(%v)", 24 + digits) // e.g. "('11:22:33.123456', '22:33:44.123456')"
				anMaxDataParcelSize[0] += uint64(24 + digits + 2) // plus 2-byte length
			} else if colCookedDataType == T_PERIOD_TIME_WITH_TIME_ZONE {
				colTypeCast = fmt.Sprintf("VARCHAR(%v)", 36 + digits) // e.g. "('11:22:33.123456+05:30', '22:33:44.123456+05:30')"
				anMaxDataParcelSize[0] += uint64(36 + digits + 2) // plus 2-byte length
			} else if colCookedDataType == T_PERIOD_TIMESTAMP {
				colTypeCast = fmt.Sprintf("VARCHAR(%v)", 46 + digits) // e.g. "('1970-01-02 03:04:05.123456', '1976-07-08 09:10:11.123456')"
				anMaxDataParcelSize[0] += uint64(46 + digits + 2) // plus 2-byte length
			} else { // if colCookedDataType == T_PERIOD_TIMESTAMP_WITH_TIME_ZONE {
				colTypeCast = fmt.Sprintf("VARCHAR(%v)", 58 + digits) // e.g. "('1970-01-02 03:04:05.123456+05:30', '1976-07-08 09:10:11.123456+05:30')"
				anMaxDataParcelSize[0] += uint64(58 + digits + 2) // plus 2-byte length
			}
	} // end switch colCookedDataType

	if colTypeCast == "" {

		msg := fmt.Sprintf("Cannot FastLoad because a data type of %v in column %v of the destination database table is not supported",
			colTypeName,
			col)

		if fldFileManager.m_appRows.m_bTryFastLoad {
			sWarning = fldFileManager.m_appCon.makeDriverWarning (526, msg)
			fldFileManager.m_asWarnings = append(fldFileManager.m_asWarnings, sWarning)

		} else {
			err = fldFileManager.m_appCon.makeDriverError (nil, 510, msg)
		}

		return
	}

	return

} // end method getTeradataTypeCastCSV

func (fldFileManager *fastLoadManagerFileImport) getTeradataTypeCastParquet (col int, colMetadata columnMetadata, anMaxDataParcelSize [] uint64) (colTypeCast string, err error) {

	var sWarning string

	colTypeName := colMetadata.M_sTypeName
	colCookedDataType := colMetadata.M_uCookedDataType
	colByteCount := colMetadata.M_uByteCount
	colPrecision := colMetadata.M_uPrecision
	colScale := colMetadata.M_uScale

	// Retrieve the Arrow column type, bind type, type cast name, and byte count directly from the file importer.
	arrowType, nBindType, sBindName, nByteCount := fldFileManager.m_fileImporter.getColumnDataType (col)

	if fldFileManager.m_appCon.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter fastLoadManagerFileImport.getTeradataTypeCastParquet m_sTableName=%v col=%v type=%v (cookedType=%v, byteCount=%v, scale=%v) arrowType=%v anMaxDataParcelSize [0]=%v",
			fldFileManager.m_sTableName, col, colTypeName, colCookedDataType, colByteCount, colScale, arrowType, anMaxDataParcelSize [0]))
		defer func () { traceLog (fmt.Sprintf ("< leave fastLoadManagerFileImport.getTeradataTypeCastParquet m_sTableName=%v col=%v colTypeCast=%v anMaxDataParcelSize [0]=%v sWarning=%v",
			fldFileManager.m_sTableName, col, colTypeCast, anMaxDataParcelSize [0], sWarning)) } ()
	}

	if fldFileManager.m_appCon.hasDebugLog () {
		debugLog (fmt.Sprintf ("getTeradataTypeCastParquet incoming: m_sTableName=%v col=%v colMetadata=%+v arrowType=%v", fldFileManager.m_sTableName, col, colMetadata, arrowType))
		debugLog (fmt.Sprintf ("getTeradataTypeCastParquet processed: colCookedDataType=%v colByteCount=%v colPrecision=%v colScale=%v", colCookedDataType, colByteCount, colPrecision, colScale))
	}

	switch colCookedDataType {

	// Parquet uses native Teradata types where possible; each case validates the Arrow type
	// and errors if the Arrow-encoded type is incompatible with the target Teradata column type.
		case T_BIGINT,
			 T_BYTEINT,
			 T_DECIMAL, // supported as target column type, but not as bind type
			 T_FLOAT,
			 T_INTEGER,
			 T_NUMBER,
			 T_SMALLINT:

			switch nBindType {
				case T_BIGINT,
					 T_BYTEINT,
					 T_FLOAT,
					 T_INTEGER,
					 T_NUMBER,
					 T_SMALLINT:

					colTypeCast = sBindName
					anMaxDataParcelSize [0] += nByteCount
					fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = nBindType
					fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = nBindType + 1
					fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = nByteCount

				case T_DECIMAL:

					colTypeCast = sBindName
					anMaxDataParcelSize [0] += nByteCount
					decType := arrowType.(*arrow.Decimal128Type)
					fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = T_DECIMAL
					fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = T_DECIMAL + 1
					fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = nByteCount
					fldFileManager.m_aBindMetadata [col-1].m_uPrecision        = uint8 (decType.Precision)
					fldFileManager.m_aBindMetadata [col-1].m_uScale            = uint8 (decType.Scale)

				case T_CHAR,
					 T_VARCHAR,
					 T_LONG_VARCHAR:

					nLength := fldFileManager.preferredVarCharLength (colCookedDataType, colPrecision, colScale)
					colTypeCast = fmt.Sprintf ("VARCHAR(%v)", nLength)
					anMaxDataParcelSize [0] += uint64 (nLength + 2) // plus 2-byte length
					fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = T_VARCHAR
					fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = T_VARCHAR + 1
					fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = uint64 (nLength)

				default:
					msg := fmt.Sprintf ("Parquet bound parameter %v type %v (%v) is not supported with target column type %v (%v)",
						col,
						sBindName,
						nBindType,
						colTypeName,
						colCookedDataType)

					if fldFileManager.m_appRows.m_bTryFastLoad {
						sWarning = fldFileManager.m_appCon.makeDriverWarning (527, msg)
						fldFileManager.m_asWarningsAtTypeCast = append (fldFileManager.m_asWarningsAtTypeCast, sWarning)

					} else {
						err = fldFileManager.m_appCon.makeDriverError (nil, 516, msg)
					}

					return
			}

		case T_BYTE,
			 T_VARBYTE,
			 T_LONG_VARBYTE:

			if nBindType == fldFileManager.preferredDataType (colCookedDataType) || nBindType == T_CHAR || nBindType == T_VARCHAR || nBindType == T_LONG_VARCHAR {
				colTypeCast = fmt.Sprintf ("VARBYTE(%v)", colByteCount)
				anMaxDataParcelSize [0] += colByteCount + 2 // plus 2-byte length
				fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = T_VARBYTE
				fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = T_VARBYTE + 1
				fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = colByteCount

			} else {

				msg := fmt.Sprintf ("Parquet bound parameter %v type %v (%v) is not supported with target column type %v (%v)",
					col,
					sBindName,
					nBindType,
					colTypeName,
					colCookedDataType)

				if fldFileManager.m_appRows.m_bTryFastLoad {
					sWarning = fldFileManager.m_appCon.makeDriverWarning (527, msg)
					fldFileManager.m_asWarningsAtTypeCast = append (fldFileManager.m_asWarningsAtTypeCast, sWarning)

				} else {
					err = fldFileManager.m_appCon.makeDriverError (nil, 516, msg)
				}

				return
			}

		case T_CHAR,
			 T_VARCHAR,
			 T_LONG_VARCHAR:

			if nBindType == fldFileManager.preferredDataType (colCookedDataType) {
				// This code is specific to UTF8 and would differ for other session character sets, e.g. nLength = colByteCount for ASCII and UTF16
				nLength := minUint64 (colByteCount, MAX_UTF8_VARCHAR_BYTE_LENGTH)
				colTypeCast = fmt.Sprintf ("VARCHAR(%v)", nLength)
				anMaxDataParcelSize [0] += nLength + 2 // plus 2-byte length
				fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = T_VARCHAR
				fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = T_VARCHAR + 1
				fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = nLength

			} else {

				msg := fmt.Sprintf ("Parquet bound parameter %v type %v (%v) is not supported with target column type %v (%v)",
					col,
					sBindName,
					nBindType,
					colTypeName,
					colCookedDataType)

				if fldFileManager.m_appRows.m_bTryFastLoad {
					sWarning = fldFileManager.m_appCon.makeDriverWarning (527, msg)
					fldFileManager.m_asWarningsAtTypeCast = append (fldFileManager.m_asWarningsAtTypeCast, sWarning)

				} else {
					err = fldFileManager.m_appCon.makeDriverError (nil, 516, msg)
				}

				return
			}

		case T_DATE_INTEGER:

			if nBindType == T_DATE_INTEGER {
				colTypeCast = sBindName
				anMaxDataParcelSize [0] += nByteCount
				fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = T_DATE_INTEGER
				fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = T_DATE_INTEGER + 1
				fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = nByteCount
			} else if nBindType == T_VARCHAR {
				colTypeCast = "VARCHAR(10)"
				anMaxDataParcelSize [0] += 10 + 2 // plus 2-byte length
				fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = T_VARCHAR
				fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = T_VARCHAR + 1
				fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = uint64 (10)
			} else {

				msg := fmt.Sprintf ("Parquet bound parameter %v type %v (%v) is not supported with target column type %v (%v)",
					col,
					sBindName,
					nBindType,
					colTypeName,
					colCookedDataType)

				if fldFileManager.m_appRows.m_bTryFastLoad {
					sWarning = fldFileManager.m_appCon.makeDriverWarning (527, msg)
					fldFileManager.m_asWarningsAtTypeCast = append (fldFileManager.m_asWarningsAtTypeCast, sWarning)

				} else {
					err = fldFileManager.m_appCon.makeDriverError (nil, 516, msg)
				}

				return
			}

		case T_TIME,
			 T_TIMESTAMP,
			 T_INTERVAL_YEAR,
			 T_INTERVAL_YEAR_TO_MONTH,
			 T_INTERVAL_MONTH,
			 T_INTERVAL_DAY,
			 T_INTERVAL_DAY_TO_HOUR,
			 T_INTERVAL_DAY_TO_MINUTE,
			 T_INTERVAL_DAY_TO_SECOND,
			 T_INTERVAL_HOUR,
			 T_INTERVAL_HOUR_TO_MINUTE,
			 T_INTERVAL_HOUR_TO_SECOND,
			 T_INTERVAL_MINUTE,
			 T_INTERVAL_MINUTE_TO_SECOND,
			 T_INTERVAL_SECOND:

			if nBindType == colCookedDataType {
				colTypeCast = fmt.Sprintf ("CHAR(%v)", colByteCount)
				anMaxDataParcelSize [0] += colByteCount
				fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = nBindType
				fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = nBindType + 1
				fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = colByteCount
				fldFileManager.m_aBindMetadata [col-1].m_uScale            = colScale

			} else if nBindType == T_VARCHAR {
				colTypeCast = fmt.Sprintf ("VARCHAR(%v)", colByteCount)
				anMaxDataParcelSize [0] += colByteCount + 2 // plus 2-byte length
				fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = T_VARCHAR
				fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = T_VARCHAR + 1
				fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = colByteCount
			} else {

				msg := fmt.Sprintf ("Parquet bound parameter %v type %v (%v) is not supported with target column type %v (%v)",
					col,
					sBindName,
					nBindType,
					colTypeName,
					colCookedDataType)

				if fldFileManager.m_appRows.m_bTryFastLoad {
					sWarning = fldFileManager.m_appCon.makeDriverWarning (527, msg)
					fldFileManager.m_asWarningsAtTypeCast = append (fldFileManager.m_asWarningsAtTypeCast, sWarning)

				} else {
					err = fldFileManager.m_appCon.makeDriverError (nil, 516, msg)
				}

				return
			}

		case T_TIME_WITH_TIME_ZONE:

			// Arrow TIME32/TIME64 maps to T_TIME (no Arrow type maps to T_TIME_WITH_TIME_ZONE).
			// Promote T_TIME → T_TIME_WITH_TIME_ZONE by binding the plain-time ASCII string (no TZ
			// offset); the server coerces it to TIME WITH TIME ZONE using the session timezone.
			// Byte count: "HH:MM:SS" (8) + "." + scale digits (when scale > 0)
			if nBindType == T_TIME {
				nTimeByteCount := uint64 (8)
				if colScale > 0 {
					nTimeByteCount += uint64 (colScale) + 1
				}
				colTypeCast = fmt.Sprintf ("CHAR(%v)", nTimeByteCount)
				anMaxDataParcelSize [0] += nTimeByteCount
				fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = T_TIME
				fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = T_TIME + 1
				fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = nTimeByteCount
				fldFileManager.m_aBindMetadata [col-1].m_uScale            = colScale
			} else if nBindType == T_VARCHAR {
				colTypeCast = fmt.Sprintf ("VARCHAR(%v)", colByteCount)
				anMaxDataParcelSize [0] += colByteCount + 2 // plus 2-byte length
				fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = T_VARCHAR
				fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = T_VARCHAR + 1
				fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = colByteCount
			} else {

				msg := fmt.Sprintf ("Parquet bound parameter %v type %v (%v) is not supported with target column type %v (%v)",
					col,
					sBindName,
					nBindType,
					colTypeName,
					colCookedDataType)

				if fldFileManager.m_appRows.m_bTryFastLoad {
					sWarning = fldFileManager.m_appCon.makeDriverWarning (527, msg)
					fldFileManager.m_asWarningsAtTypeCast = append (fldFileManager.m_asWarningsAtTypeCast, sWarning)

				} else {
					err = fldFileManager.m_appCon.makeDriverError (nil, 516, msg)
				}

				return
			}

		case T_TIMESTAMP_WITH_TIME_ZONE:

			// Arrow TIMESTAMP maps to T_TIMESTAMP (no Arrow type maps to T_TIMESTAMP_WITH_TIME_ZONE).
			// Promote T_TIMESTAMP → T_TIMESTAMP_WITH_TIME_ZONE by binding the plain-timestamp ASCII
			// string (no TZ offset); the server coerces it to TIMESTAMP WITH TIME ZONE using the
			// session timezone. Byte count: "YYYY-MM-DD HH:MM:SS" (19) + "." + scale digits (when scale > 0)
			if nBindType == T_TIMESTAMP {
				nTsBaseByteCount := uint64 (19)
				if colScale > 0 {
					nTsBaseByteCount += uint64 (colScale) + 1
				}
				colTypeCast = fmt.Sprintf ("CHAR(%v)", nTsBaseByteCount)
				anMaxDataParcelSize [0] += nTsBaseByteCount
				fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = T_TIMESTAMP
				fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = T_TIMESTAMP + 1
				fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = nTsBaseByteCount
				fldFileManager.m_aBindMetadata [col-1].m_uScale            = colScale
			} else if nBindType == T_VARCHAR {
				colTypeCast = fmt.Sprintf ("VARCHAR(%v)", colByteCount)
				anMaxDataParcelSize [0] += colByteCount + 2 // plus 2-byte length
				fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = T_VARCHAR
				fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = T_VARCHAR + 1
				fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = colByteCount
			} else {

				msg := fmt.Sprintf ("Parquet bound parameter %v type %v (%v) is not supported with target column type %v (%v)",
					col,
					sBindName,
					nBindType,
					colTypeName,
					colCookedDataType)

				if fldFileManager.m_appRows.m_bTryFastLoad {
					sWarning = fldFileManager.m_appCon.makeDriverWarning (527, msg)
					fldFileManager.m_asWarningsAtTypeCast = append (fldFileManager.m_asWarningsAtTypeCast, sWarning)

				} else {
					err = fldFileManager.m_appCon.makeDriverError (nil, 516, msg)
				}

				return
			}

		case T_PERIOD_DATE,
			 T_PERIOD_TIME,
			 T_PERIOD_TIME_WITH_TIME_ZONE,
			 T_PERIOD_TIMESTAMP,
			 T_PERIOD_TIMESTAMP_WITH_TIME_ZONE:

			digits := 0
			if colScale > 0 {
				digits = (int (colScale) + 1) * 2 // plus 1 for the decimal point '.' and times 2 for the two time/timestamp values in a period
			}

			if nBindType == colCookedDataType  {
				colTypeCast = sBindName
				anMaxDataParcelSize [0] += nByteCount
				fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = nBindType
				fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = nBindType + 1
				fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = nByteCount
			} else if nBindType == T_VARCHAR && colCookedDataType == T_PERIOD_DATE {
				colTypeCast = fmt.Sprintf ("VARCHAR(%v)", 28) // e.g. "('1970-01-02', '1973-04-05')"
				anMaxDataParcelSize [0] += 28 + 2 // plus 2-byte length
				fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = T_VARCHAR
				fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = T_VARCHAR + 1
				fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = uint64 (28)
			} else if nBindType == T_VARCHAR && colCookedDataType == T_PERIOD_TIME {
				colTypeCast = fmt.Sprintf ("VARCHAR(%v)", 24 + digits) // e.g. "('11:22:33.123456', '22:33:44.123456')"
				anMaxDataParcelSize [0] += uint64 (24 + digits + 2) // plus 2-byte length
				fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = T_VARCHAR
				fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = T_VARCHAR + 1
				fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = uint64 (24 + digits)
			} else if nBindType == T_VARCHAR && colCookedDataType == T_PERIOD_TIME_WITH_TIME_ZONE {
				colTypeCast = fmt.Sprintf ("VARCHAR(%v)", 36 + digits) // e.g. "('11:22:33.123456+05:30', '22:33:44.123456+05:30')"
				anMaxDataParcelSize [0] += uint64 (36 + digits + 2) // plus 2-byte length
				fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = T_VARCHAR
				fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = T_VARCHAR + 1
				fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = uint64 (36 + digits)
			} else if nBindType == T_VARCHAR && colCookedDataType == T_PERIOD_TIMESTAMP {
				colTypeCast = fmt.Sprintf ("VARCHAR(%v)", 46 + digits) // e.g. "('1970-01-02 03:04:05.123456', '1976-07-08 09:10:11.123456')"
				anMaxDataParcelSize [0] += uint64 (46 + digits + 2) // plus 2-byte length
				fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = T_VARCHAR
				fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = T_VARCHAR + 1
				fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = uint64 (46 + digits)
			} else if nBindType == T_VARCHAR && colCookedDataType == T_PERIOD_TIMESTAMP_WITH_TIME_ZONE {
				colTypeCast = fmt.Sprintf ("VARCHAR(%v)", 58 + digits) // e.g. "('1970-01-02 03:04:05.123456+05:30', '1976-07-08 09:10:11.123456+05:30')"
 				anMaxDataParcelSize [0] += uint64 (58 + digits + 2) // plus 2-byte length
				fldFileManager.m_aBindMetadata [col-1].m_uCookedDataType   = T_VARCHAR
				fldFileManager.m_aBindMetadata [col-1].m_uNullableDataType = T_VARCHAR + 1
				fldFileManager.m_aBindMetadata [col-1].m_uByteCount        = uint64 (58 + digits)
			} else {

				msg := fmt.Sprintf ("Parquet bound parameter %v type %v (%v) is not supported with target column type %v (%v)",
					col,
					sBindName,
					nBindType,
					colTypeName,
					colCookedDataType)

				if fldFileManager.m_appRows.m_bTryFastLoad {
					sWarning = fldFileManager.m_appCon.makeDriverWarning (527, msg)
					fldFileManager.m_asWarningsAtTypeCast = append (fldFileManager.m_asWarningsAtTypeCast, sWarning)

				} else {
					err = fldFileManager.m_appCon.makeDriverError (nil, 516, msg)
				}

				return
			}

	}

	if colTypeCast == "" {

		msg := fmt.Sprintf ("Cannot FastLoad because a data type of %v in column %v of the destination database table is not supported",
			colTypeName,
			col)

		if fldFileManager.m_appRows.m_bTryFastLoad {
			sWarning = fldFileManager.m_appCon.makeDriverWarning (526, msg)
			fldFileManager.m_asWarnings = append (fldFileManager.m_asWarnings, sWarning)

		} else {
			err = fldFileManager.m_appCon.makeDriverError (nil, 510, msg)
		}
		return
	}

	return
} // end method getTeradataTypeCastParquet

func (fldFileManager *fastLoadManagerFileImport) endFastLoad (ctx context.Context) (err error) {

	var sWarning string

	if fldFileManager.m_appCon.hasTraceLog () {
		traceLog(fmt.Sprintf("> enter fastLoadManagerFileImport.endFastLoad m_sTableName=%v m_bConnected=%v m_bFastloading=%v m_uNumTotalRowsSent=%v getErrorCount=%v len(m_asWarnings)=%v",
			fldFileManager.m_sTableName, fldFileManager.m_bConnected, fldFileManager.m_bFastloading, fldFileManager.m_uNumTotalRowsSent, fldFileManager.getErrorCount (), len(fldFileManager.m_asWarnings)))
		defer func () { traceLog(fmt.Sprintf("< leave fastLoadManagerFileImport.endFastLoad m_sTableName=%v m_bConnected=%v m_bFastloading=%v getErrorCount=%v len(m_asWarnings)=%v sWarning=%v",
			fldFileManager.m_sTableName, fldFileManager.m_bConnected, fldFileManager.m_bFastloading, fldFileManager.getErrorCount (), len(fldFileManager.m_asWarnings), sWarning)) } ()
	}

	if fldFileManager.m_bConnected && fldFileManager.m_bFastloading {
		defer func () {
			defer func () {
				fldFileManager.m_bFastloading = false
				fldFileManager.m_uNumTotalRowsSent = 0
				fldFileManager.m_nCurrentRowNumber = 0
				fldFileManager.m_bCheckFastloadErrorTable2 = false
			} ()
			if err != nil {
				err = chainErrors(err, fldFileManager.abortFastLoad(ctx))
			}
		} ()

		_, err = fldFileManager.m_controlSession.ExecContext (ctx, "{fn teradata_rpo(E)}CHECKPOINT LOADING END", nil)
		if err != nil { return err }

		_, err = fldFileManager.m_controlSession.ExecContext (ctx, "{fn teradata_commit}", nil)
		if err != nil { return err }

		resControlSession, err := fldFileManager.m_controlSession.ExecContext (ctx, "{fn teradata_rpo(E)}END LOADING", nil)
		if err != nil { return err }

		rowsAffected, err := resControlSession.RowsAffected()
		if err != nil { return err }

		if uint64 (rowsAffected) != fldFileManager.m_uNumTotalRowsSent {

			sWarning = fldFileManager.m_controlSession.makeDriverWarning (518,
				fmt.Sprintf ("Found %v duplicate or faulty row(s) while ending FastLoad of database table %v: expected a row count of %v, got a row count of %v",
					fldFileManager.m_uNumTotalRowsSent - uint64 (rowsAffected),
					fldFileManager.m_sTableName,
					fldFileManager.m_uNumTotalRowsSent,
					rowsAffected))

			fldFileManager.m_asWarnings = append(fldFileManager.m_asWarnings, sWarning)
		}

		_, err = fldFileManager.m_controlSession.ExecContext (ctx, "{fn teradata_commit}", nil)
		if err != nil { return err }

		_, err = fldFileManager.m_controlSession.ExecContext (ctx,  "{fn teradata_nativesql}{fn teradata_autocommit_on}", nil)
		if err != nil { return err }

		var errSave error
		if fldFileManager.m_appRows.m_bManageErrorTables {

			errSave = fldFileManager.selectFastLoadErrorTable2 (ctx)

			err = fldFileManager.dropFastLoadErrorTables (ctx)
			if err != nil { return chainErrors (errSave, err) }
		}

		err = fldFileManager.closeFastLoad ()
		if err != nil { return chainErrors (errSave, err) }
	}

	return

} // end method endFastLoad
