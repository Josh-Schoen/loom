// Copyright 2021 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"context"
	"database/sql/driver"
)

type fastExportManager interface {

	closeFastExport        (ctx context.Context) (err error)
	copyColMDToControlRows ()
	executeSelect          (aBindValues [] driver.NamedValue, ctx context.Context) (err error)
	getAppCon              () *TeradataConnection
	getAppRows             () *TeradataRows
	getBlkNumberReceive    () uint64
	getBlkCount            () uint64
	getControlRows         () *TeradataRows
	getControlSession      () *TeradataConnection
	getFastExportRows      () *TeradataRows
	getFastExportRowsIndex () int
	getFileExporter        () fileExporter
	getFileExporterType    () int
	getNextFastExportRows  () (rows *TeradataRows)
	getStatementNumber     () uint32
	getWarnings            () [] string
	hadWarningsAtNew       () bool
	initBindMetadata       ()
	initStatementBlock     ()
	isConnected            () bool
	nextExportResultSet    (ctx context.Context) error
	receiveResults         (ctx context.Context) error
	requestResults         (ctx context.Context) error
	setFileExporter        (fileExp fileExporter)
	setFileExportPath      (fileExpPathName string)
	setWarnings            (asWarnings [] string)
	setWarningsAtNew       (b bool)
	Value                  () fastExportManager
	writeExportedData      (ctx context.Context) (err error)
}
