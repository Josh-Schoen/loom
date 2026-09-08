// Copyright 2021 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"database/sql/driver"

	"github.com/apache/arrow-go/v18/arrow"
)

type fileImporter interface {

	getBatchSegment (nMaxSize uint32, bFastloading bool) (aaBatchRecords [][] driver.NamedValue, nLastRecordProcessed int, bParseError bool, err error)
	getFileName () string
	close () error
	getFileType () int
	getColumnDataType (nColIndex int) (arrow.DataType, uint16, string, uint64)

} // end type fileImporter
