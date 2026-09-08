// Copyright 2024 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
)

func shredJSON (sJSON string, pOutput any) (err error) {

	err = json.Unmarshal ([] byte (sJSON), pOutput)
	return

} // end shredJSON

func makeMapFromJSON (sJSON string) (mapTokens map [string] any, err error) {

	mapTokens = map [string] any {}
	err = shredJSON (sJSON, &mapTokens)
	return

} // end makeMapFromJSON

func (con *TeradataConnection) searchForKVP (sKey string, sValuePattern string, sJSON string, nErrorCode int, sErrorMsg string) (sOutput string, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter searchForKVP %v sKey=%v sValuePattern=%v", con, sKey, sValuePattern))
		defer func () { traceLog (fmt.Sprintf ("< leave searchForKVP %v sOutput=%v err=%v", con, sOutput, err)) } ()
	}

	reKVP, err := regexp.Compile (fmt.Sprintf (`"%v"\s*:\s*%v`, sKey, sValuePattern))
	if err != nil {
		err = con.makeDriverError (err, 0, "Cannot compose regular expression for JSON key " + sKey)
		return
	}

	asGroup := reKVP.FindStringSubmatch (sJSON)

	bFound := asGroup != nil
	if bFound {
		sOutput = asGroup [1]
		return
	}

	bRequired := sErrorMsg != ""
	if bRequired {
		err = con.makeDriverError (err, nErrorCode, sErrorMsg)
		return
	}

	return

} // end searchForKVP

func (con *TeradataConnection) getStringFromJSON (sKey string, sJSON string, nErrorCode int, sErrorMsg string) (sOutput string, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter getStringFromJSON %v sKey=%v", con, sKey))
		defer func () { traceLog (fmt.Sprintf ("< leave getStringFromJSON %v sOutput=%v err=%v", con, sOutput, err)) } ()
	}

	sOutput, err = con.searchForKVP (sKey, `"([^"]*)"`, sJSON, nErrorCode, sErrorMsg)
	return

} // end getStringFromJSON

func (con *TeradataConnection) getIntFromJSON (sKey string, sJSON string, nErrorCode int, sErrorMsg string, nDefault int) (nOutput int, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter getIntFromJSON %v sKey=%v", con, sKey))
		defer func () { traceLog (fmt.Sprintf ("< leave getIntFromJSON %v nOutput=%v err=%v", con, nOutput, err)) } ()
	}

	sValue, err := con.searchForKVP (sKey, `([0-9]+)`, sJSON, nErrorCode, sErrorMsg)
	if err != nil { return }

	if sValue == "" {
		nOutput = nDefault
		return
	}

	nOutput, err = strconv.Atoi (sValue)
	if err != nil {
		err = con.makeDriverError (err, 0, fmt.Sprintf ("JSON key %v has non-numeric value %v", sKey, sValue))
		return
	}

	return

} // end getIntFromJSON
