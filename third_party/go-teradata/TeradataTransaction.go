// Copyright 2017 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"database/sql/driver"
	"fmt"
)

var _ driver.Tx     = &teradataTransaction {} // implements driver.Tx
var _ fmt.Stringer  = &teradataTransaction {} // implements fmt.Stringer

type teradataTransaction struct {

	m_con *TeradataConnection

} // end type teradataTransaction

// Note: This pointer-receiver method will not work with a value inside an interface, which we do not expect or intend.

func (tx *teradataTransaction) String () string { // satisfies fmt.Stringer

	return fmt.Sprintf ("%v tx=%p", tx.m_con, tx)

} // end method String

func newTeradataTransaction (con *TeradataConnection) (tx *teradataTransaction, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter newTeradataTransaction %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave newTeradataTransaction %v", tx)) } ()
	}

	tx = &teradataTransaction {
		m_con : con,
	}

	return

} // end func newTeradataTransaction

func (tx *teradataTransaction) Commit () error { // satisfies driver.Tx

	if tx.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Tx.Commit %v", tx))
		defer func () { traceLog (fmt.Sprintf ("< leave Tx.Commit %v", tx)) } ()
	}

	return nil

} // end method Commit

func (tx *teradataTransaction) Rollback () error { // satisfies driver.Tx

	if tx.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Tx.Rollback %v", tx))
		defer func () { traceLog (fmt.Sprintf ("< leave Tx.Rollback %v", tx)) } ()
	}

	return nil

} // end method Rollback
