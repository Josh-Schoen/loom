// Copyright 2019 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"context"
	"database/sql/driver"
)

type fastLoadManager interface {

	alreadyAutoCommitted   () bool
	closeFastLoad          () (err error)
	endFastLoad            (ctx context.Context) (err error)
	executeInsert          (activityCount *uint64, aBindValues [] driver.NamedValue, ctx context.Context) (err error)
	fastloading            () bool
	getAppCon              () *TeradataConnection
	getBindMetadata        () [] bindMetadata
	getControlSession      () *TeradataConnection
	getWarnings            () [] string
	hadWarningsAtNew       () bool
	getWarningsAtTypeCast  () [] string
	isBatchInsert          () bool
	isConnected            () bool
	rollbackFastLoad       (ctx context.Context) (err error)
	saveErrorsAndWarnings  () bool
	setFileImporter        (fileImporter)
	setWarnings            (asWarnings [] string)
	setWarningsAtNew       (b bool)
	Value                  () fastLoadManager

} // end fastLoadManager
