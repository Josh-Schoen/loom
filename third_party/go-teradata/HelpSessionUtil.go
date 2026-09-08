// Copyright 2022 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"context"
	"fmt"
	"database/sql/driver"
	"strings"
)

// Zero-based HELP SESSION output column indexes
const HELP_SESSION_CURRENT_DATABASE_INDEX         = 4
const HELP_SESSION_QUERY_BAND_INDEX               = 43
const HELP_SESSION_FULL_CURRENT_DATABASE_INDEX    = 74
const HELP_SESSION_UNICODE_PASS_THROUGH_INDEX     = 124

const QUERY_BAND_TRANSACTION_INDEX = 0
const QUERY_BAND_SESSION_INDEX = 1

func executeHelpSession (con *TeradataConnection, ctx context.Context) (result [] driver.Value, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter executeHelpSession %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave executeHelpSession %v", con)) } ()
	}

	result, err = con.executeSessionQueryRow (ctx, "HELP SESSION")
	return

} // end executeHelpSession

// Parses the row value of the QueryBand column obtained from a HELP SESSION ResultSet
func getQueryBandFromHelpSessionResult (result [] driver.Value) (queryBandArray [] string) {

	// get the row value of the QueryBand column after executing HELP SESSION
	queryBandArray = make ([] string, 3)

	queryBand := safeHelpSessionValue (result, HELP_SESSION_QUERY_BAND_INDEX)

	if len (queryBand) == 0 { return }

	// parse query band
	transactionTag := "=T> "
	sessionTag := "=S> "
	transactionIndex := strings.Index (queryBand, transactionTag)
	sessionIndex := strings.Index (queryBand, sessionTag)

	if transactionIndex >= 0 && sessionIndex >= 0 {
		if transactionIndex < sessionIndex {
			queryBandArray [QUERY_BAND_TRANSACTION_INDEX] = strings.TrimSpace (queryBand [transactionIndex + len(transactionTag) : sessionIndex])
			queryBandArray [QUERY_BAND_SESSION_INDEX] = strings.TrimSpace (queryBand [sessionIndex + len(sessionTag) : ])

		} else {
			queryBandArray [QUERY_BAND_TRANSACTION_INDEX] = strings.TrimSpace (queryBand [transactionIndex + len (transactionTag) : ])
			queryBandArray [QUERY_BAND_SESSION_INDEX] = strings.TrimSpace (queryBand [sessionIndex + len (sessionTag) : transactionIndex])
		}

	} else if transactionIndex >= 0 {
		queryBandArray [QUERY_BAND_TRANSACTION_INDEX] = strings.TrimSpace (queryBand [transactionIndex + len (transactionTag) : ])

	} else if sessionIndex >= 0 {
		queryBandArray [QUERY_BAND_SESSION_INDEX] = strings.TrimSpace (queryBand [sessionIndex + len (sessionTag) : ])
	}

	return

} // end getQueryBandFromHelpSessionResult

// Gets the row value of the Current Database column after executing HELP SESSION.
func getCurrentDatabaseFromHelpSessionResult (result [] driver.Value) (currentDatabase string) {

	// The EON full current database name takes precedence over the old current database name.
	currentDatabase = safeHelpSessionValue (result, HELP_SESSION_FULL_CURRENT_DATABASE_INDEX)
	if len (currentDatabase) == 0 {
		currentDatabase = safeHelpSessionValue (result, HELP_SESSION_CURRENT_DATABASE_INDEX)
	}

	return

} // end getCurrentDatabaseFromHelpSessionResult

func getUnicodePassThroughFromHelpSessionResult (result [] driver.Value) bool {

	s := safeHelpSessionValue (result, HELP_SESSION_UNICODE_PASS_THROUGH_INDEX)
	return strings.EqualFold (strings.TrimSpace (s), "S")

} // end getUnicodePassThroughFromHelpSessionResult

func safeHelpSessionValue (result [] driver.Value, nIndex int) (sOutput string) {

	if nIndex < len (result) {
		if s, bValid := result [nIndex].(string) ; bValid {
			sOutput = s
		}
	}

	return

} // end safeHelpSessionValue
