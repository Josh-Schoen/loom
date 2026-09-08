// Copyright 2017 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
)

var _ driver.Driver = &teradataDriver {} // implements driver.Driver

type teradataDriver struct {}

func init () {

	sql.Register ("teradata", &teradataDriver {})

} // end init

func (*teradataDriver) Open (sConParams string) (con driver.Conn, err error) { // satisfies driver.Driver

	params, err := ParseConParams (sConParams)
	if err != nil { return }

	if params.hasTraceLog () {
		traceLog ("> enter Driver.Open")
		defer func () { traceLog (fmt.Sprintf ("< leave Driver.Open %v err=%v", con, err)) } ()
	}

	con, err = newTeradataConnection (params)
	return

} // end method Open
