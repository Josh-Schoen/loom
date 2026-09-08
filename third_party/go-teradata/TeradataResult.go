// Copyright 2017 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"context"
	"database/sql/driver"
	"fmt"
)

var _ driver.Result = &teradataResult {} // implements driver.Result
var _ fmt.Stringer  = &teradataResult {} // implements fmt.Stringer

type teradataResult struct {

	m_rows *TeradataRows

} // end type teradataResult

// Note: This pointer-receiver method will not work with a value inside an interface, which we do not expect or intend.

func (res *teradataResult) String () string { // satisfies fmt.Stringer

	return fmt.Sprintf ("%v res=%p", res.m_rows, res)

} // end method String

func newTeradataResult (stmt *teradataStatement, ctx context.Context, aBindValues [] driver.NamedValue) (res *teradataResult, err error) {

	if stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter newTeradataResult %v bind=%v", stmt, aBindValues))
		defer func () { traceLog (fmt.Sprintf ("< leave newTeradataResult %v", res)) } ()
	}

	res = &teradataResult {}

	res.m_rows, err = newTeradataRows (stmt, ctx, aBindValues, /*bResultSetExpected*/ false)
	if err != nil { return }

	if ! res.m_rows.m_bSendOnly {
		err = res.m_rows.Close ()
		if err != nil { return }
	}

	return

} // end func newTeradataResult

func (res *teradataResult) LastInsertId () (int64, error) { // satisfies driver.Result

	if res.m_rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Result.LastInsertId %v", res))
		defer func () { traceLog (fmt.Sprintf ("< leave Result.LastInsertId %v", res)) } ()
	}

	return 0, res.m_rows.m_stmt.m_con.makeDriverError (nil, 0, "Method driver.Result.LastInsertId is not implemented yet")

} // end method LastInsertId

func (res *teradataResult) RowsAffected () (int64, error) { // satisfies driver.Result

	if res.m_rows.m_stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Result.RowsAffected %v", res))
		defer func () { traceLog (fmt.Sprintf ("< leave Result.RowsAffected %v", res)) } ()
	}

	return int64 (res.m_rows.m_uActivityCount), nil

} // end method RowsAffected
