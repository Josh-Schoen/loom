// Copyright 2021 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"fmt"
	"os"
)

var _ fmt.Stringer = &fileExporterBase {} // implements fmt.Stringer

type fileExporterBase struct {

	m_sExportFilePath    string
	m_file               *os.File
	m_bFastExporting     bool

} // end type fileExporterBase

func (feb *fileExporterBase) String () string { // satisfies fmt.Stringer

	return fmt.Sprintf ("fileExporterBase(m_sExportFilePath=%v m_bFastloading=%v)", feb.m_sExportFilePath, feb.m_bFastExporting)

} // end method String

func newFileExporter (sFilePath string, nFileType int, sFieldSeparator string, sFieldQuote string) (exp fileExporter, err error) {

	feb := fileExporterBase { m_sExportFilePath : sFilePath }

	feb.m_file, err = os.Create (sFilePath)
	if err != nil {
		return
	}

	if nFileType == CSV_FILE_TYPE {
		exp = newFileExporterCSV (feb, sFieldSeparator, sFieldQuote)
	}

	return

} // end newFileExporter

func (feb *fileExporterBase) getFileExportPath () string { return feb.m_sExportFilePath }  // satisfies fileExporter
func (feb *fileExporterBase) close () error { return feb.m_file.Close () } // satisfies fileExporter
