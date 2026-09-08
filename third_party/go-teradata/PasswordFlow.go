// Copyright 2024 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"fmt"
	"net/url"
)

func (con *TeradataConnection) validatePasswordFlow () (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter validatePasswordFlow %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave validatePasswordFlow %v", con)) } ()
	}

	// user must be specified when this logmech is specified.
	if con.m_params.M_sUserName == "" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Connection parameter user is required with logmech %v", con.m_params.M_sLogMech))
		return
	}

	// password must be specified when this logmech is specified.
	if con.m_params.M_sPassword == "" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Connection parameter password is required with logmech %v", con.m_params.M_sLogMech))
		return
	}

	// logdata must be omitted when this logmech is specified.
	if con.m_params.M_sLogData != "" {
		err = con.makeDriverError (nil, 554, fmt.Sprintf ("Do not specify logdata with logmech %v", con.m_params.M_sLogMech))
		return
	}

	return

} // end validatePasswordFlow

func (con *TeradataConnection) doPasswordFlow (sUserName string, sPassword string, sIdProURL string, sClientID string, sOIDCScope string, bOAuthFlow bool, cc *CacheContext) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter doPasswordFlow %v bOAuthFlow=%v sOIDCScope=%v", con, bOAuthFlow, sOIDCScope))
		defer func () { traceLog (fmt.Sprintf ("< leave doPasswordFlow %v bOAuthFlow=%v sOIDCScope=%v", con, bOAuthFlow, sOIDCScope)) } ()
	}

	resp, err := getOIDCMetadataResponse (con, sIdProURL)
	if err != nil { return }

	sJSON := string (resp.M_abyBody)

	if con.hasDebugLog () {
		debugLog ("doPasswordFlow: sJSON=" + sJSON)
	}

	sTokenURL, err := con.getStringFromJSON ("token_endpoint", sJSON, 0, "Failed to parse metadata JSON from Identity Provider")
	if err != nil { return }

	sFormData := "grant_type=password" +
		"&client_id=" + url.QueryEscape (sClientID) +
		"&scope=" + url.QueryEscape (sOIDCScope) +
		"&username=" + url.QueryEscape (con.m_params.M_sUserName) +
		"&password=" + url.QueryEscape (con.m_params.M_sPassword)

	if con.hasDebugLog () {
		debugLog ("doPasswordFlow: sTokenURL=" + sTokenURL)
		debugLog ("doPasswordFlow: sFormData=" + sFormData)
	}

	resp, err = con.doHttpRequest ("POST", sTokenURL, [][] string {{ "Content-Type", "application/x-www-form-urlencoded" }}, [] byte (sFormData), /*anRequiredRespCodes*/ [] int {200})
	if err != nil {
		err = con.makeDriverError (err, 567, "Failed to obtain token from Identity Provider")
		return
	}

	sJSON = string (resp.M_abyBody)

	err = con.putCacheEntry (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow, sTokenURL, sJSON, cc)
	return

} // end doPasswordFlow
