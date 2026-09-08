// Copyright 2024 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"fmt"
)

func (con *TeradataConnection) validateClientCredFlow () (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter validateClientCredFlow %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave validateClientCredFlow %v", con)) } ()
	}

	// user, password, oidc_clientid, and oidc_scope must be omitted when this logmech is specified
	if con.m_params.M_sUserName != "" {
		err = con.makeDriverError (nil, 552, fmt.Sprintf ("Do not specify user with logmech %v", con.m_params.M_sLogMech))
		return
	}

	if con.m_params.M_sPassword != "" {
		err = con.makeDriverError (nil, 553, fmt.Sprintf ("Do not specify password with logmech %v", con.m_params.M_sLogMech))
		return
	}

	if con.m_params.M_sOIDCClientID != "" {
		err = con.makeDriverError (nil, 554, fmt.Sprintf ("Do not specify oidc_clientid with logmech %v", con.m_params.M_sLogMech))
		return
	}

	if con.m_params.M_sOIDCScope != "" {
		err = con.makeDriverError (nil, 554, fmt.Sprintf ("Do not specify oidc_scope with logmech %v", con.m_params.M_sLogMech))
		return
	}

	if con.m_params.M_sOAuthScopes != "" {
		err = con.makeDriverError (nil, 554, fmt.Sprintf ("Do not specify oauth_scopes with logmech %v", con.m_params.M_sLogMech))
		return
	}

	// logdata must be specified when this logmech is specified.
	if con.m_params.M_sLogData == "" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Connection parameter logdata is required with logmech %v", con.m_params.M_sLogMech))
		return
	}

	return

} // end validateClientCredFlow

func (con *TeradataConnection) doClientCredFlow (sUserName string, sPassword string, sIdProURL string, sClientID string, sOIDCScope string, bOAuthFlow bool, cc *CacheContext) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter doClientCredFlow %v bOAuthFlow=%v sOIDCScope=%v", con, bOAuthFlow, sOIDCScope))
		defer func () { traceLog (fmt.Sprintf ("< leave doClientCredFlow %v bOAuthFlow=%v sOIDCScope=%v", con, bOAuthFlow, sOIDCScope)) } ()
	}

	resp, err := getOIDCMetadataResponse (con, sIdProURL)
	if err != nil { return }

	sJSON := string (resp.M_abyBody)

	if con.hasDebugLog () {
		debugLog ("doClientCredFlow: JSON=" + sJSON)
	}

	sTokenURL, err := con.getStringFromJSON ("token_endpoint", sJSON, 0, "Failed to parse metadata JSON from Identity Provider")
	if err != nil { return }

	sFormData := con.m_params.M_sLogData

	if con.hasDebugLog () {
		debugLog ("doClientCredFlow: sTokenURL=" + sTokenURL)
		debugLog ("doClientCredFlow: sFormData=" + sFormData)
	}

	resp, err = con.doHttpRequest ("POST", sTokenURL, [][] string {{ "Content-Type", "application/x-www-form-urlencoded" }}, [] byte (sFormData), /*anRequiredRespCodes*/ [] int {200})
	if err != nil {
		err = con.makeDriverError (err, 567, "Failed to obtain token from Identity Provider")
		return
	}

	sJSON = string (resp.M_abyBody)

	err = con.putCacheEntry (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow, sTokenURL, sJSON, cc)
	return

} // end doClientCredFlow
