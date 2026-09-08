// Copyright 2024 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"
)

func (con *TeradataConnection) validateDeviceCodeFlow () (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter validateDeviceCodeFlow %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave validateDeviceCodeFlow %v", con)) } ()
	}

	// password and logdata must be omitted when this logmech is specified
	if con.m_params.M_sPassword != "" {
		err = con.makeDriverError (nil, 553, fmt.Sprintf ("Do not specify password with logmech %v", con.m_params.M_sLogMech))
		return
	}

	if con.m_params.M_sLogData != "" {
		err = con.makeDriverError (nil, 554, fmt.Sprintf ("Do not specify logdata with logmech %v", con.m_params.M_sLogMech))
		return
	}

	return

} // end validateDeviceCodeFlow

func (con *TeradataConnection) doDeviceCodeFlow (sUserName string, sPassword string, sIdProURL string, sClientID string, sOIDCScope string, bOAuthFlow bool, cc *CacheContext) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter doDeviceCodeFlow %v bOAuthFlow=%v sOIDCScope=%v", con, bOAuthFlow, sOIDCScope))
		defer func () { traceLog (fmt.Sprintf ("< leave doDeviceCodeFlow %v bOAuthFlow=%v sOIDCScope=%v", con, bOAuthFlow, sOIDCScope)) } ()
	}

	resp, err := getOIDCMetadataResponse (con, sIdProURL)
	if err != nil { return }

	sJSON := string (resp.M_abyBody)

	if con.hasDebugLog () {
		debugLog ("doDeviceCodeFlow: sJSON=" + sJSON)
	}

	var idp struct {
		M_sAuthURL  string `json:"device_authorization_endpoint"`
		M_sTokenURL string `json:"token_endpoint"`
	}

	err = shredJSON (sJSON, &idp)
	if err != nil {
		err = con.makeDriverError (err, 0, "Failed to parse metadata JSON from Identity Provider")
		return
	}

	sFormData := "client_id=" + url.QueryEscape (sClientID) + "&scope=" + url.QueryEscape (sOIDCScope)

	if con.hasDebugLog () {
		debugLog ("doDeviceCodeFlow: idp.M_sAuthURL=" + idp.M_sAuthURL)
		debugLog ("doDeviceCodeFlow: idp.M_sTokenURL=" + idp.M_sTokenURL)
		debugLog ("doDeviceCodeFlow: sFormData=" + sFormData)
	}

	resp, err = con.doHttpRequest ("POST", idp.M_sAuthURL, [][] string {{ "Content-Type", "application/x-www-form-urlencoded" }}, [] byte (sFormData), /*anRequiredRespCodes*/ [] int {200})
	if err != nil {
		err = con.makeDriverError (err, 0, "Failed to obtain Device Authorization Response from Identity Provider")
		return
	}

	sJSON = string (resp.M_abyBody)

	if con.hasDebugLog () {
		debugLog ("doDeviceCodeFlow: sJSON=" + sJSON)
	}

	// https://datatracker.ietf.org/doc/html/rfc8628/#section-3.2

	// (Optional) interval                   Minimum time in seconds that the client SHOULD wait between polling requests to the token endpoint. When not provided, clients MUST use 5 as the default.
	// (Always)   expires_in                 Lifetime in seconds of the device_code and user_code.
	// (Always)   device_code                Device verification code.
	// (Always)   user_code                  End-user verification code.
	// (Always)   verification_uri           End-user verification URI on the authorization server.
	// (Optional) verification_uri_complete  End-user verification URI that includes the user_code.

	var dar struct {
		M_nIntervalInSecs  int    `json:"interval"`
		M_nExpiresInSecs   int    `json:"expires_in"`
		M_sDeviceCode      string `json:"device_code"`
		M_sUserCode        string `json:"user_code"`
		M_sVerificationURL string `json:"verification_uri"`
		M_sCompleteURL     string `json:"verification_uri_complete"`
	}

	dar.M_nIntervalInSecs = 5 // optional value, default 5 seconds

	err = shredJSON (sJSON, &dar)
	if err != nil {
		err = con.makeDriverError (err, 0, "Failed to parse Device Authorization Response from Identity Provider")
		return
	}

	sDisplayURL := coalesce (
		dar.M_sCompleteURL,
		dar.M_sVerificationURL + "?user_code=" + url.QueryEscape (dar.M_sUserCode))

	sFormData = "grant_type=urn:ietf:params:oauth:grant-type:device_code" +
		"&client_id="   + url.QueryEscape (sClientID) +
		"&device_code=" + url.QueryEscape (dar.M_sDeviceCode)

	if con.hasDebugLog () {
		debugLog ("doDeviceCodeFlow: dar=" + fmt.Sprintf ("%+v", dar))
		debugLog ("doDeviceCodeFlow: sDisplayURL=" + sDisplayURL)
		debugLog ("doDeviceCodeFlow: sFormData=" + sFormData)
	}

	err = con.displayCodeToUser (sDisplayURL, dar.M_sUserCode)
	if err != nil { return }

	for {

		threadSleep (con.hasTimingLog (), time.Duration (dar.M_nIntervalInSecs) * time.Second)

		resp, errToken := con.doHttpRequest ("POST", idp.M_sTokenURL, [][] string {{ "Content-Type", "application/x-www-form-urlencoded" }}, [] byte (sFormData), /*anRequiredRespCodes*/ nil)
		if errToken != nil {
			err = con.makeDriverError (errToken, 0, "Failed to obtain token from Identity Provider")
			return
		}

		sJSON := string (resp.M_abyBody)

		if con.hasDebugLog () {
			debugLog ("doDeviceCodeFlow: resp.M_nCode=" + itoa (resp.M_nCode) + " resp.M_sMessage=" + resp.M_sMessage)
			debugLog ("doDeviceCodeFlow: JSON=" + sJSON)
			debugLog ("doDeviceCodeFlow: M_sOIDCToken=" + con.m_params.M_sOIDCToken)
		}

		// https://datatracker.ietf.org/doc/html/rfc8628/#section-3.5

		// If the user has approved the grant, the token endpoint responds with a success response having HTTP 200 status code.

		if resp.M_nCode == 200 {

			err = con.putCacheEntry (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow, idp.M_sTokenURL, sJSON, cc)
			return
		}

		// Otherwise, the token endpoint responds with an error having a HTTP 400 (Bad Request) status code.

		sError, errParse := con.parseHttpErrorResponse (idp.M_sTokenURL, resp.M_nCode, resp.M_sMessage, sJSON)

		// The following error codes are specified for use with the device authorization grant in token endpoint responses:

		// authorization_pending
		//  The authorization request is still pending as the end user hasn't yet completed the user-interaction steps.
		//  The client SHOULD repeat the access token request to the token endpoint (a process known as polling).

		if resp.M_nCode == 400 && "authorization_pending" == sError {
			continue
		}

		// slow_down
		//  A variant of "authorization_pending", the authorization request is still pending and polling should continue,
		//  but the interval MUST be increased by 5 seconds for this and all subsequent requests.

		if resp.M_nCode == 400 && "slow_down" == sError {
			dar.M_nIntervalInSecs += 5 // increase by 5 seconds
			continue
		}

		// Other error codes correspond to non-recoverable errors, and the client SHOULD NOT continue polling.

		err = errParse
		return

	} // end for

} // end doDeviceCodeFlow

func (con *TeradataConnection) displayCodeToUser (sDisplayURL string, sUserCode string) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter displayCodeToUser %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave displayCodeToUser %v", con)) } ()
	}

	var wOutput io.Writer

	if con.m_params.M_sCodeAppendFile == "" || strings.EqualFold (con.m_params.M_sCodeAppendFile, "-out") {

		wOutput = os.Stdout

	} else if strings.EqualFold (con.m_params.M_sCodeAppendFile, "-err") {

		wOutput = os.Stderr

	} else { // a filename

		pFile, errOpen := os.OpenFile (con.m_params.M_sCodeAppendFile, os.O_CREATE | os.O_WRONLY | os.O_APPEND, 0666)
		if errOpen != nil {
			err = con.makeDriverError (errOpen, 0, fmt.Sprintf ("Unable to open file %v specified by code_append_file connection parameter", con.m_params.M_sCodeAppendFile))
			return
		}

		defer pFile.Close ()

		wOutput = pFile
	}

	sTimestamp := formatTime (time.Now ())
	fmt.Fprintln (wOutput, sTimestamp)
	fmt.Fprintln (wOutput, sTimestamp + " -----------------------------------------------------------------------------------------")
	fmt.Fprintln (wOutput, sTimestamp)
	fmt.Fprintln (wOutput, sTimestamp + " To proceed with Teradata authentication, use your web browser to visit the following URL:")
	fmt.Fprintln (wOutput, sTimestamp)
	fmt.Fprintln (wOutput, sTimestamp + "   " + sDisplayURL)
	fmt.Fprintln (wOutput, sTimestamp)
	fmt.Fprintln (wOutput, sTimestamp + " Then confirm the following code:")
	fmt.Fprintln (wOutput, sTimestamp)
	fmt.Fprintln (wOutput, sTimestamp + "   " + sUserCode)
	fmt.Fprintln (wOutput, sTimestamp)
	fmt.Fprintln (wOutput, sTimestamp + " -----------------------------------------------------------------------------------------")
	fmt.Fprintln (wOutput, sTimestamp)
	return

} // end displayCodeToUser
