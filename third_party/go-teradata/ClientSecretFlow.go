// Copyright 2024 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"encoding/base64"
	"fmt"
	"net/url"
)

func (con *TeradataConnection) validateClientSecretFlow () (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter validateClientSecretFlow %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave validateClientSecretFlow %v", con)) } ()
	}

	// user and password must be omitted when this logmech is specified
	if con.m_params.M_sUserName != "" {
		err = con.makeDriverError (nil, 552, fmt.Sprintf ("Do not specify user with logmech %v", con.m_params.M_sLogMech))
		return
	}

	if con.m_params.M_sPassword != "" {
		err = con.makeDriverError (nil, 553, fmt.Sprintf ("Do not specify password with logmech %v", con.m_params.M_sLogMech))
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

} // end validateClientSecretFlow

func (con *TeradataConnection) doClientSecretFlow (sUserName string, sPassword string, sIdProURL string, sClientID string, sOIDCScope string, bOAuthFlow bool, cc *CacheContext) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter doClientSecretFlow %v bOAuthFlow=%v sOIDCScope=%v", con, bOAuthFlow, sOIDCScope))
		defer func () { traceLog (fmt.Sprintf ("< leave doClientSecretFlow %v bOAuthFlow=%v sOIDCScope=%v", con, bOAuthFlow, sOIDCScope)) } ()
	}

	resp, err := getOIDCMetadataResponse (con, sIdProURL)
	if err != nil { return }

	sJSON := string (resp.M_abyBody)

	if con.hasDebugLog () {
		debugLog ("doClientSecretFlow: sJSON=" + sJSON)
	}

	sTokenURL, err := con.getStringFromJSON ("token_endpoint", sJSON, 0, "Failed to parse metadata JSON from Identity Provider")
	if err != nil { return }

	if con.hasDebugLog () {
		debugLog ("doClientSecretFlow: sTokenURL=" + sTokenURL)
	}

	// https://datatracker.ietf.org/doc/html/rfc6749#section-2.3.1

	// Clients in possession of a client password MAY use the HTTP Basic authentication scheme as defined in [RFC2617] to authenticate with the authorization server.
	// The client identifier is ... used as the username; the client password is ... used as the password.
	// The authorization server MUST support the HTTP Basic authentication scheme for authenticating clients that were issued a client password.

	sClientSecret      := con.m_params.M_sLogData
	sCredentials       := sClientID + ":" + sClientSecret
	sCredentialsBase64 := base64.StdEncoding.EncodeToString ([] byte (sCredentials))
	sAuthorization     := "Basic " + sCredentialsBase64

	// https://datatracker.ietf.org/doc/html/rfc6749#section-4.4.2

	// grant_type REQUIRED Value MUST be set to "client_credentials".
	// scope      OPTIONAL The scope of the access request

	sFormData := "grant_type=client_credentials" +
		"&scope=" + url.QueryEscape (sOIDCScope)

	if con.hasDebugLog () {
		debugLog ("doClientSecretFlow: sClientSecret=" + sClientSecret)
		debugLog ("doClientSecretFlow: sCredentials=" + sCredentials)
		debugLog ("doClientSecretFlow: sCredentialsBase64=" + sCredentialsBase64)
		debugLog ("doClientSecretFlow: sAuthorization=" + sAuthorization)
		debugLog ("doClientSecretFlow: sFormData=" + sFormData)
	}

	resp, err = con.doHttpRequest ("POST", sTokenURL, [][] string {
		{ "Authorization", sAuthorization },
		{ "Content-Type", "application/x-www-form-urlencoded" },
		}, [] byte (sFormData), /*anRequiredRespCodes*/ [] int {200})
	if err != nil {
		err = con.makeDriverError (err, 567, "Failed to obtain token from Identity Provider")
		return
	}

	sJSON = string (resp.M_abyBody)

	err = con.putCacheEntry (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow, sTokenURL, sJSON, cc)
	return

} // end doClientSecretFlow
