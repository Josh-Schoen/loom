// Copyright 2021 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"bufio"
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

var _ fileExporter = &fileExporterCSV {} // implements fileExporter
var _ fmt.Stringer = &fileExporterCSV {} // implements fmt.Stringer

type fileExporterCSV struct {

	fileExporterBase

	m_rFieldSeparator    rune                  // field delimiter (set to ',' by NewReader)
	m_rFieldQuote        rune                  // quote character
	m_nQuoteLen          int
	m_nRecordNumber      int                   // current record starts used for errors
	m_writer             *bufio.Writer         // reader for csv file

} // end fileExporterCSV struct

func (fic *fileExporterCSV) String () string { // satisfies fmt.Stringer

	return fmt.Sprintf ("fileExporterCSV: %v m_rFieldSeparator=%v m_rFieldQuote=%v m_RecNumber=%v",
		fic.fileExporterBase, fic.m_rFieldSeparator, fic.m_rFieldQuote, fic.m_nRecordNumber)

} // end method String

func newFileExporterCSV (feb fileExporterBase, sFieldSeparator string, sFieldQuote string) (fileExporter) {

	return &fileExporterCSV {
		fileExporterBase   : feb,
		m_rFieldSeparator  : [] rune (sFieldSeparator) [0],
		m_rFieldQuote      : [] rune (sFieldQuote) [0],
		m_nQuoteLen        : utf8.RuneLen ([] rune (sFieldQuote) [0]),
		m_writer           : bufio.NewWriterSize (feb.m_file, 10240),
		m_nRecordNumber    : -1,
	}
} // end newFileExporterCSV

func (fic *fileExporterCSV) close () error { // satisfies fileExporter

	fic.m_writer.Flush ()
	return fic.m_file.Close ()

} // end method close

func (fec *fileExporterCSV) exportRecord (aRecord [] driver.Value, nRow int, bFastExporting bool) (err error) {

	for nCol, col := range aRecord {

		switch col.(type) {
			case nil:
				// do nothing

			case float64 :
				fec.writeField (strconv.FormatFloat (col.(float64), 'e', -1, 64), nRow, false /*bOuterQuotes*/)

			case string :
				fec.writeField (col.(string), nRow, false /*bOuterQuotes*/)

			default :
				sCol := fmt.Sprintf ("%v", col)
				fec.writeField (sCol, nRow, false /*bOuterQuotes*/)

		}

		if nCol < len (aRecord) - 1 {
			fec.m_writer.WriteRune (fec.m_rFieldSeparator)
		}
	}
	fec.m_writer.WriteString ("\n")

	return

} // end exportRecord

func (fec *fileExporterCSV) writeField (sCol string, nRow int, bOuterQuotes bool) (err error) {

	bNeedsOuterQuotes := bOuterQuotes
	bNeedsDoubleQuotes := false

	if len (sCol) == 0 {

		fec.m_writer.WriteRune (fec.m_rFieldQuote)
		fec.m_writer.WriteRune (fec.m_rFieldQuote)
		return

	}

	if strings.ContainsRune (sCol, fec.m_rFieldQuote) {
		bNeedsDoubleQuotes = true
	} else if (strings.ContainsRune (sCol, fec.m_rFieldSeparator) || strings.ContainsAny (sCol, "\r\n")) {
		// If the field starts with quotes but doesn't end with quotes, need double quotes.
		// If field contains quote after the first character
		bNeedsOuterQuotes = true
	}

	if ! bNeedsDoubleQuotes && ! bNeedsOuterQuotes {

		fec.m_writer.WriteString (sCol)

	} else if bNeedsOuterQuotes || bNeedsDoubleQuotes {

		fec.m_writer.WriteRune (fec.m_rFieldQuote)

		if ! bNeedsDoubleQuotes {

			fec.m_writer.WriteString (sCol)

		} else {

			for len (sCol) > 0 {

				index := strings.IndexRune (sCol, fec.m_rFieldQuote)
				nAdjustment := fec.m_nQuoteLen
				if index < 0 {
					index = len (sCol)
					nAdjustment = 0
				}


				fec.m_writer.WriteString (sCol [:index + nAdjustment])
				sCol = sCol [index :]
				if len (sCol) > 0 {
					fec.m_writer.WriteRune (fec.m_rFieldQuote)
				}
				sCol = sCol [nAdjustment:]

			}

		}

		fec.m_writer.WriteRune (fec.m_rFieldQuote)
	}

	return

} // end writeField
