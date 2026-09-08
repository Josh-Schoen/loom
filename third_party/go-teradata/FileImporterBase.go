// Copyright 2021 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"fmt"
	"os"

	"github.com/apache/arrow-go/v18/arrow"
)

const CSV_FILE_TYPE int = 1
const PARQUET_FILE_TYPE int = 2

var _ fmt.Stringer = &fileImporterBase {} // implements fmt.Stringer

type fileImporterBase struct {

	m_sFileName          string
	m_file               *os.File
	m_fileInfo           os.FileInfo
	m_bFastloading       bool
	m_nFileType          int
	m_mapParameterDataTypes map [int] escParamDataTypeMetadata

} // end type fileImporterBase

func (fib *fileImporterBase) String () string { // satisfies fmt.Stringer

	return fmt.Sprintf ("fileImporterBase(m_sFileName=%v size=%v m_bFastloading=%v)", fib.m_sFileName, fib.m_fileInfo.Size (), fib.m_bFastloading)

} // end method String

func (fib *fileImporterBase) close () error { // satisfies fileImporter

	return fib.m_file.Close ()

} // end method close

func (fib *fileImporterBase) getFileName () string { // satisfies fileImporter

	return fib.m_sFileName

} // end method getFileName

func (fib *fileImporterBase) getColumnDataType (col int) (arrow.DataType, uint16, string, uint64) { // satisfies fileImporter

	// CSV and other text-based importers have no Arrow type information. If needed for another file type 
	// in the future, this can return any and be overridden in the specific importer (e.g. Parquet).
	return nil, T_VARCHAR, "VARCHAR", uint64(0)
} // end method getColumnDataType

func (fib *fileImporterBase) getFileType () int { // satisfies fileImporter

	return fib.m_nFileType

} // end method getFileType

func newFileImporter (sFileName string, nFileType int, sFieldSeparator string, sFieldQuote string, mapParmDataTypes map[int] escParamDataTypeMetadata) (imp fileImporter, err error) {

	fib := fileImporterBase { m_sFileName : sFileName, m_nFileType: nFileType, m_mapParameterDataTypes: mapParmDataTypes }

	fib.m_file, err = os.Open (sFileName)
	if err != nil {
		return
	}

	fib.m_fileInfo, err = fib.m_file.Stat ()
	if err != nil {
		fib.close ()
		return
	}

	if nFileType == CSV_FILE_TYPE {
		imp = newFileImporterCSV (fib, sFieldSeparator, sFieldQuote)

	} else if nFileType == PARQUET_FILE_TYPE {
		imp, err = newFileImporterParquet (fib)
		if err != nil {
			fib.close ()
			return
		}
	}

	fib.m_mapParameterDataTypes = mapParmDataTypes

	return

} // end newFileImporter
