// Copyright 2021 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"database/sql/driver"
)

type fileExporter interface {

	exportRecord (aRecords [] driver.Value, nRow int, bFastExporting bool) (err error)
	getFileExportPath () string
	close () error

} // end type fileImporter
