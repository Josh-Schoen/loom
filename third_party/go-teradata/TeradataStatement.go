// Copyright 2017 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"context"
	"database/sql/driver"
	"fmt"
)

var _ driver.ColumnConverter    = &teradataStatement {} // implements driver.ColumnConverter
var _ driver.Stmt               = &teradataStatement {} // implements driver.Stmt
var _ driver.StmtExecContext    = &teradataStatement {} // implements driver.StmtExecContext
var _ driver.StmtQueryContext   = &teradataStatement {} // implements driver.StmtQueryContext
var _ fmt.Stringer              = &teradataStatement {} // implements fmt.Stringer

type teradataStatement struct {

	m_con            *TeradataConnection
	m_sRequestText   string

} // end type teradataStatement

// Note: This pointer-receiver method will not work with a value inside an interface, which we do not expect or intend.

func (stmt *teradataStatement) String () string { // satisfies fmt.Stringer

	return fmt.Sprintf ("%v stmt=%p sql=%v", stmt.m_con, stmt, abbreviateString (stmt.m_sRequestText, 100))

} // end method String

func newTeradataStatement (con *TeradataConnection, ctx context.Context, sRequestText string) (stmt *teradataStatement, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter newTeradataStatement %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave newTeradataStatement %v", stmt)) } ()
	}

	stmt = &teradataStatement {
		m_con          : con,
		m_sRequestText : sRequestText,
	}

	return

} // end func newTeradataStatement

func (stmt *teradataStatement) ColumnConverter (iParam int) driver.ValueConverter { // satisfies driver.ColumnConverter

	if stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter ColumnConverter.ColumnConverter param=%v %v", iParam, stmt))
		defer func () { traceLog (fmt.Sprintf ("< leave ColumnConverter.ColumnConverter param=%v %v", iParam, stmt)) } ()
	}

	return driver.DefaultParameterConverter

} // end method ColumnConverter

func (stmt *teradataStatement) Close () error { // satisfies driver.Stmt

	if stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Stmt.Close %v", stmt))
		defer func () { traceLog (fmt.Sprintf ("< leave Stmt.Close %v", stmt)) } ()
	}

	return nil

} // end method Close

func (stmt *teradataStatement) NumInput () int { // satisfies driver.Stmt

	if stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Stmt.NumInput %v", stmt))
		defer func () { traceLog (fmt.Sprintf ("< leave Stmt.NumInput %v", stmt)) } ()
	}

	return -1 // suppress bind value count checking by sql package

} // end method NumInput

func (stmt *teradataStatement) Exec (args [] driver.Value) (driver.Result, error) { // satisfies driver.Stmt

	if stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Stmt.Exec %v", stmt))
		defer func () { traceLog (fmt.Sprintf ("< leave Stmt.Exec %v", stmt)) } ()
	}

	return nil, stmt.m_con.makeDriverError (nil, 0, "Deprecated method driver.Stmt.Exec")

} // end method Exec

func (stmt *teradataStatement) Query (args [] driver.Value) (driver.Rows, error) { // satisfies driver.Stmt

	if stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Stmt.Query %v", stmt))
		defer func () { traceLog (fmt.Sprintf ("< leave Stmt.Query %v", stmt)) } ()
	}

	return nil, stmt.m_con.makeDriverError (nil, 0, "Deprecated method driver.Stmt.Query")

} // end method Query

func (stmt *teradataStatement) ExecContext (ctx context.Context, aBindValues [] driver.NamedValue) (driver.Result, error) { // satisfies driver.StmtExecContext

	if stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Stmt.ExecContext %v bind=%v", stmt, aBindValues))
		defer func () { traceLog (fmt.Sprintf ("< leave Stmt.ExecContext %v", stmt)) } ()
	}

	return newTeradataResult (stmt, ctx, aBindValues)

} // end method ExecContext

func (stmt *teradataStatement) QueryContext (ctx context.Context, aBindValues [] driver.NamedValue) (rows driver.Rows, err error) { // satisfies driver.StmtQueryContext

	if stmt.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Stmt.QueryContext %v bind=%v", stmt, aBindValues))
		defer func () { traceLog (fmt.Sprintf ("< leave Stmt.QueryContext %v", stmt)) } ()
	}

	return newTeradataRows (stmt, ctx, aBindValues, /*bResultSetExpected*/ true)

} // end method QueryContext
