// Copyright 2021 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"bufio"
	"bytes"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

var _ fileImporter = &fileImporterCSV {} // implements fileImporter
var _ fmt.Stringer = &fileImporterCSV {} // implements fmt.Stringer

type fileImporterCSV struct {

	fileImporterBase

	m_rFieldSeparator    rune                  // field delimiter (set to ',' by NewReader)
	m_rFieldQuote        rune                  // quote character

	m_errChain           error                 // contains error for each failed record
	m_nLineNumber        int                   // current line number used for errors
	m_nRecordNumber      int                   // current record starts used for errors
	m_nColumnNumber      int                   // current column number used for errors
	m_nSegmentNumber     int                   // current segment number

	m_nFieldsPerRecord   int                   // number of fields in each record
	m_nRecordByteCount   uint32                // byte size of record
	m_aoLastRecord       [] driver.NamedValue  // record processed but would not fit in buffer
	m_reader             *bufio.Reader         // reader for csv file

	m_abyLine            [] byte               // line where data read from file is stored
	m_aFieldIndex        [] int                // array of indexes where each field starts (store here so doesn't need to be allocated each time)
	m_nBufferIndex       int64                 // index into into buffer

} // end fileImporterCSV struct

func (fic *fileImporterCSV) String () string { // satisfies fmt.Stringer

	return fmt.Sprintf ("fileImporterCSV: %v m_rFieldSeparator=%v m_rFieldQuote=%v m_nSegmentNumber=%v m_RecNumber=%v m_errChain=%v",
		fic.fileImporterBase, fic.m_rFieldSeparator, fic.m_rFieldQuote, fic.m_nSegmentNumber, fic.m_nRecordNumber, fic.m_errChain)

} // end method String

func newFileImporterCSV (fib fileImporterBase, sFieldSeparator string, sFieldQuote string) (fileImporter) {

	return &fileImporterCSV {
		fileImporterBase   : fib,
		m_rFieldSeparator  : [] rune (sFieldSeparator) [0],
		m_rFieldQuote      : [] rune (sFieldQuote) [0],
		m_reader           : bufio.NewReader (fib.m_file),
		m_nRecordNumber       : -1,
	}
} // end newFileImporterCSV

func (fic *fileImporterCSV) parseError (sErrMsg string) error {

	if fic.m_nColumnNumber > 0 {
		return fmt.Errorf ("parse error in record %d; line %d, column %d: %s", fic.m_nRecordNumber, fic.m_nLineNumber, fic.m_nColumnNumber, sErrMsg)
	}

	return fmt.Errorf ("parse error in record %d; line %d: %s", fic.m_nRecordNumber, fic.m_nLineNumber, sErrMsg)

} // end parseError

func (fic *fileImporterCSV) getBatchSegment (nMaxSize uint32, bFastloading bool) (aaBatchRecords [][] driver.NamedValue, nLastRecordProcessed int, bParseError bool, err error) { // satisfies fileImporter

	nByteSize := uint32 (0)
	fic.m_nSegmentNumber ++
	fic.m_bFastloading = bFastloading

	// Check if there was a record that was processed but did not fit into the last segment
	if fic.m_aoLastRecord != nil {
		aaBatchRecords = append (aaBatchRecords, fic.m_aoLastRecord)
		fic.m_aoLastRecord = nil
		nByteSize = fic.m_nRecordByteCount
		fic.m_nRecordNumber ++
	}

	for {
		var aoRecord [] driver.NamedValue
		aoRecord, bParseError, err = fic.processRecord ()

		if err != nil && ! errors.Is (err, io.EOF) {
			if ! bParseError || fic.m_bFastloading { return }
			fic.m_errChain = chainErrors (fic.m_errChain, err)
			continue
		}

		if nMaxSize > 0 && nByteSize + fic.m_nRecordByteCount > nMaxSize || fic.m_nRecordNumber % 16383 == 16382 {

			// The last record processed does not fit into the current buffer, save for
			// next segment and subtract one from the number of records being returned.
			fic.m_aoLastRecord = aoRecord
			fic.m_nRecordNumber --

			if fic.m_nSegmentNumber == 1 { aaBatchRecords = aaBatchRecords [1:] }

			if fic.m_errChain != nil {
				err = fic.m_errChain
				fic.m_errChain = nil
				bParseError = true
			}

			return aaBatchRecords, fic.m_nRecordNumber, bParseError, err
		}

		if len (aoRecord) > 0 {
			nByteSize += fic.m_nRecordByteCount
			aaBatchRecords = append (aaBatchRecords, aoRecord)
		}

		if errors.Is (err, io.EOF) {

			// If first segment, remove column names and check to make sure the file contains
			// records. It is possible that each record had a parser error so it checks for this.
			if fic.m_nSegmentNumber == 1 {
				if len (aaBatchRecords) > 1 {
					aaBatchRecords = aaBatchRecords [1:]
				} else if fic.m_errChain == nil {
					return aaBatchRecords, fic.m_nRecordNumber, false /*parse error*/, fmt.Errorf ("the file %v contains no records", fic.m_sFileName)
				}
			}

			if fic.m_errChain != nil {
				err = fic.m_errChain
				fic.m_errChain = nil
				bParseError = true
			} else {
				// We know err is EOF and we don't return EOF
				err = nil
			}

			return aaBatchRecords, fic.m_nRecordNumber, bParseError, err
		}
	} // end for

} // end getBatchSegment

func (fic *fileImporterCSV) readLine (bAppend bool) (err error) {

	var abyLine [] byte

	if ! bAppend {

		fic.m_abyLine, err = fic.m_reader.ReadBytes ('\n')
		fic.m_nLineNumber ++
		fic.m_nColumnNumber = -1

	} else {

		// Since we are appending this line, we don't need to read bytes but read slice instead
		// Data returned from ReadSlice will be overwritten by the next I/O operation
		abyLine, err = fic.m_reader.ReadSlice ('\n')
		fic.m_abyLine = append (fic.m_abyLine, abyLine...)

		fic.m_nLineNumber ++
		fic.m_nColumnNumber = -1
	}

	for err == bufio.ErrBufferFull {
		abyLine, err = fic.m_reader.ReadSlice ('\n')
		fic.m_abyLine = append (fic.m_abyLine, abyLine...)
	}

	return

} // end readLine

func (fic *fileImporterCSV) peekRune (abyBuf [] byte) (rune) {

	r, _ := utf8.DecodeRune (abyBuf)
	return r

} // end peekRune

func (fic *fileImporterCSV) lengthOfNewLine () (nLen int) {

	// Using csv writer possible to end up with \r\r\n at end of line
	if len (fic.m_abyLine) > 0 && fic.m_abyLine [len (fic.m_abyLine) - 1] == '\n' {
		nLen ++

		for i := 1 ; len (fic.m_abyLine) > i && fic.m_abyLine [len (fic.m_abyLine) - (i + 1)] == '\r' ; i++ { nLen++ }
	}

	return nLen

} // end lengthOfNewLine

func (fic *fileImporterCSV) isEndOfLine (nIndex int64) bool {

	// Possible to have more than one '\r' before '\n'

	for ; nIndex < int64 (len (fic.m_abyLine)) && fic.m_abyLine [nIndex] == '\r' ; nIndex ++ {}
	if len (fic.m_abyLine [nIndex : ]) > 0 && fic.m_abyLine [nIndex] == '\n' { return true }

	return false

} // end isEndOfLine

func (fic *fileImporterCSV) processRecord () (aNamedValues [] driver.NamedValue, bParseError bool, err error) {

	// Reads and processes each record. Each record starts on a new line but
	// can occupy one or more lines due to embedded line feeds in quotes. The
	// line number starts at 1 not 0 and character starts at 0. The column is
	// set to -1 so as when readRune is called it points to the character we read.

	fic.m_nRecordByteCount = 0

	// Read and ignore any empty lines
	for err == nil {
		err = fic.readLine (false /*append to existing line */)
		if err == nil && len (fic.m_abyLine) == fic.lengthOfNewLine () {
			fic.m_abyLine = nil
			continue
		} else if err != nil && ! errors.Is (err, io.EOF) {
			return nil, false /*parse error*/, err
		}

		break
	}

	// If at end of file or at end of record, return
	if errors.Is (err, io.EOF) && (fic.isEndOfLine (0) || len (fic.m_abyLine) == 0) {
		return
	}

	fic.m_nBufferIndex = 0
	fic.m_nRecordNumber ++
	bRowProcessed := false
	var teraval TeradataValue

	nSepLen := utf8.RuneLen (fic.m_rFieldSeparator)
	nQuoteLen := utf8.RuneLen (fic.m_rFieldQuote)
	nFieldNum := 0
	fic.m_aFieldIndex = fic.m_aFieldIndex [:0]

	// Loop until all the fields in the record are read or until an error.
	// Store the fields as named varchar Teradata values

	for {

		if fic.m_nBufferIndex >= int64 (len (fic.m_abyLine)) {
			break
		}

		if fic.peekRune (fic.m_abyLine [fic.m_nBufferIndex:]) == fic.m_rFieldQuote {

			// process a quoted field
			fic.m_nBufferIndex += int64 (nQuoteLen)
			bInQuote := true

			for bInQuote {

				nTmpIndex := fic.m_nBufferIndex
				i := bytes.IndexRune (fic.m_abyLine [nTmpIndex : ], fic.m_rFieldQuote)

				for i == -1 && bInQuote && err == nil {
					err = fic.readLine (true /*append to existing line */)
					if err != nil && ! errors.Is (err, io.EOF) { return nil, false /*parse error*/, err}
					i = bytes.IndexRune (fic.m_abyLine [nTmpIndex : ], fic.m_rFieldQuote)
				}

				if i == -1 {
					// At the end of the record and there is no closing quote
					return nil, true /*parse error*/, fic.parseError (fmt.Sprintf ("extraneous quote (%v) in field", string (fic.m_rFieldQuote)))
				}

				// move past this quote
				nTmpIndex += int64 (i + nQuoteLen)
				r := fic.peekRune (fic.m_abyLine [nTmpIndex : ])

				if i == 0 && fic.isEndOfLine (nTmpIndex) {
					// This is an empty quote "", which is not null but instead a zero length string
					bInQuote = false
					if len (fic.m_aFieldIndex) == 0 {
						fic.m_aFieldIndex = append (fic.m_aFieldIndex, [] int { int (fic.m_nBufferIndex), int (nTmpIndex) - nQuoteLen }...)
					}
					fic.m_nBufferIndex = nTmpIndex + int64 (nQuoteLen + fic.lengthOfNewLine ())

				} else if r == fic.m_rFieldQuote {

					fic.m_aFieldIndex = append (fic.m_aFieldIndex, [] int { int (fic.m_nBufferIndex), int (nTmpIndex) }...)
					fic.m_nBufferIndex = nTmpIndex + int64 (nQuoteLen)

				} else if r == fic.m_rFieldSeparator || fic.isEndOfLine (nTmpIndex) {

					nDelimLen := func () int { if r == fic.m_rFieldSeparator { return nSepLen } ; return fic.lengthOfNewLine () } ()
					bInQuote = false

					fic.m_aFieldIndex = append (fic.m_aFieldIndex, [] int { int (fic.m_nBufferIndex), int (nTmpIndex) - nQuoteLen }...)
					fic.m_nBufferIndex = nTmpIndex + int64 (nDelimLen)

				} else if fic.peekRune (fic.m_abyLine [nTmpIndex - int64 (nQuoteLen) : ]) == fic.m_rFieldQuote && len (fic.m_abyLine [nTmpIndex :]) == 0 {

					bInQuote = false
					fic.m_aFieldIndex = append (fic.m_aFieldIndex, [] int { int (fic.m_nBufferIndex), int (nTmpIndex) - nQuoteLen }...)
					fic.m_nBufferIndex = nTmpIndex

				} else {
					return nil, true /* parse error */, fic.parseError (fmt.Sprintf ("extraneous quote (%v) in field", string (fic.m_rFieldQuote)))

				}
			} // end if bInQuote

			// Done parsing quoted field now convert to TeradataValue

			if len (fic.m_aFieldIndex) == 2 {

				// Since field is quoted, do not turn zero length field into NULL.
				// Zero length in quotes is a zero length string.
				teraval = newTeradataValue (T_VARCHAR, 0, fic.m_abyLine [fic.m_aFieldIndex [0] : fic.m_aFieldIndex [1]] )

			} else if len (fic.m_aFieldIndex) > 2 {

				// There must have been some embedded quotes in the quoted field
				var abyValue [] byte
				for i := 0 ; i < len (fic.m_aFieldIndex) - 1 ; i += 2 {
					abyValue = append (abyValue, fic.m_abyLine [fic.m_aFieldIndex [i] : fic.m_aFieldIndex [i + 1]]...)
				}

				teraval = newTeradataValue (T_VARCHAR, 0, abyValue )
			}

		} else {

			// process an unquoted field

			i := bytes.IndexRune (fic.m_abyLine [fic.m_nBufferIndex : ], fic.m_rFieldSeparator)
			if i >= 0 {

				// More fields to process in the record

				if i == 0 {
					teraval = NewTeradataNullVarchar ()
				} else {
					teraval = newTeradataValue (T_VARCHAR, 0, fic.m_abyLine [fic.m_nBufferIndex : int64 (i) + fic.m_nBufferIndex] )
				}

				fic.m_nBufferIndex += int64 (i + nSepLen)

			} else {

				// No more separators in line so process the last field in the record

				bRowProcessed = true
				if fic.m_nBufferIndex == int64 (len (fic.m_abyLine) - fic.lengthOfNewLine ()) {
					teraval = NewTeradataNullVarchar ()
				} else {
					teraval = newTeradataValue (T_VARCHAR, 0, fic.m_abyLine [fic.m_nBufferIndex : len (fic.m_abyLine) - fic.lengthOfNewLine ()] )
				}
				fic.m_nBufferIndex = int64 (len (fic.m_abyLine) + fic.lengthOfNewLine ())

			}

			// Make sure no inner quotes appeared in this unquoted field (ex. abcd"ef"ghi or abc"defghi)
			if nQuoteIndex := bytes.IndexRune (teraval.m_abyValue, fic.m_rFieldQuote) ; nQuoteIndex >= 0 {
				return nil, true /* parse error */, fic.parseError (fmt.Sprintf ("bare quote (%v) in non-quoted-field", string (fic.m_rFieldQuote)))
			}

		} // end processing unquoted field

		// Add TeradataValue field as NamedValue to record
		aNamedValues = append (aNamedValues, driver.NamedValue {Ordinal: 1, Value: teraval})
		nFieldSize := len (teraval.m_abyValue)

		// If the field is greater than 32000 bytes and fastload is not being used, then
		// it will be treated as a deferred clob so add the size of deferred lob instead
		fic.m_nRecordByteCount += func () uint32 { if nFieldSize > 32000 && ! fic.m_bFastloading { return 4 } ; return uint32 (nFieldSize) } ()
		if bRowProcessed { break }

		nFieldNum ++
		fic.m_aFieldIndex = fic.m_aFieldIndex [:0]

	} // Done processing all fields in record

	// Make sure this record has the same number of fields as the previous records.
	nFieldsInRecord := len (aNamedValues)

	if fic.m_nFieldsPerRecord > 0 {
		if nFieldsInRecord != fic.m_nFieldsPerRecord {
			fic.m_nColumnNumber = 0 // report at start of record
			return nil, true /* parse error */, fic.parseError (fmt.Sprintf ("wrong number of fields in record, expected value is %v actual value is %v", fic.m_nFieldsPerRecord, nFieldsInRecord))
		}
	} else if fic.m_nFieldsPerRecord == 0 {
		fic.m_nFieldsPerRecord = nFieldsInRecord
	}

	if errors.Is (err, io.EOF) {
		return aNamedValues, false /* parse error */, err
	}

	return

} // end processRecord
