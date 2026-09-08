// Copyright 2022 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"runtime"
	"strings"
	"sync"
)

const PLACEHOLDER = "PLACEHOLDER"
var g_mutexActiveBrowser sync.Mutex

func (con *TeradataConnection) validateBrowserAuth () (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter validateBrowserAuth %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave validateBrowserAuth %v", con)) } ()
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

} // end validateBrowserAuth

func (con *TeradataConnection) doBrowserAuth (sUserName string, sPassword string, sIdProURL string, sClientID string, sOIDCScope string, bOAuthFlow bool, cc *CacheContext) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter doBrowserAuth %v bOAuthFlow=%v sOIDCScope=%v", con, bOAuthFlow, sOIDCScope))
		defer func () { traceLog (fmt.Sprintf ("< leave doBrowserAuth %v bOAuthFlow=%v sOIDCScope=%v", con, bOAuthFlow, sOIDCScope)) } ()
	}

	sBrowser := con.m_params.M_sBrowser
	if sBrowser == "" { // browser was not specified, so use platform-specific default

		switch runtime.GOOS {

			case "windows": sBrowser = "cmd /c start \"title\" \"" + PLACEHOLDER + "\"" // Windows requires quoted "title" to precede quoted URL

			case "darwin": sBrowser = "open " + PLACEHOLDER // Mac open command does not allow quotes around the URL

			default: // browser must be explicitly specified if not on Windows or Mac
				err = con.makeDriverError (nil, 555, fmt.Sprintf ("Browser Authentication Mechanism requires browser to be specified for %v", runtime.GOOS))
				return

		} // end switch GOOS
	} // end if sBrowser

	resp, err := getOIDCMetadataResponse (con, sIdProURL)
	if err != nil { return }

	sJSON := string (resp.M_abyBody)

	if con.hasDebugLog () {
		debugLog ("doBrowserAuth: sJSON=" + sJSON)
	}

	var idp struct {
		M_sAuthURL  string `json:"authorization_endpoint"`
		M_sTokenURL string `json:"token_endpoint"`
	}

	err = shredJSON (sJSON, &idp)
	if err != nil {
		err = con.makeDriverError (err, 0, "Failed to parse metadata JSON from Identity Provider")
		return
	}

	if con.hasDebugLog () {
		debugLog ("doBrowserAuth: idp.M_sAuthURL=" + idp.M_sAuthURL)
		debugLog ("doBrowserAuth: idp.M_sTokenURL=" + idp.M_sTokenURL)
	}

	sCodeVerifier  := base64.RawURLEncoding.EncodeToString (randomBytes (32))
	sCodeChallenge := base64.RawURLEncoding.EncodeToString (hashBytesSHA256 ([] byte (sCodeVerifier)))

	if con.hasDebugLog () {
		debugLog ("doBrowserAuth: sCodeVerifier=" + sCodeVerifier)
		debugLog ("doBrowserAuth: sCodeChallenge=" + sCodeChallenge)
	}

	sCallbackURI           := "/openid-callback"
	nBrowserTimeoutInMs    := int (con.m_params.M_uBrowserTimeout   ) * 1000
	nBrowserTabTimeoutInMs := int (con.m_params.M_nBrowserTabTimeout) * 1000
	if con.hasTimingLog () {
		timingLog (fmt.Sprintf ("Starting HttpServer with Browser Authentication timeout %v ms and browser tab timeout %v ms", nBrowserTimeoutInMs, nBrowserTabTimeoutInMs))
	}

	http, err := newHttpServer (con, con.m_params.m_anRedirectPortRange, sCallbackURI + "?", nBrowserTimeoutInMs, nBrowserTabTimeoutInMs)
	if err != nil { return }

	sRedirectURL := "http://127.0.0.1:" + http.getLocalPort () + sCallbackURI

	con.m_extra.m_sOIDCRedirectPort = ternary (con.m_params.m_anRedirectPortRange [0] == 0, "E", "") + http.getLocalPort () // E = ephemeral port

	bUseLoginHint := sUserName != ""
	bUsePrompt := bUseLoginHint && con.m_params.M_sOIDCPrompt != ""

	if con.hasDebugLog () {
		debugLog ("doBrowserAuth: sRedirectURL=" + sRedirectURL)
		debugLog ("doBrowserAuth: sUserName=" + sUserName + " bUseLoginHint=" + btoa (bUseLoginHint))
		debugLog ("doBrowserAuth: M_sOIDCPrompt=" + con.m_params.M_sOIDCPrompt + " bUsePrompt=" + btoa (bUsePrompt))
		debugLog ("doBrowserAuth: (before " + PLACEHOLDER + " swap) sBrowser=" + sBrowser)
	}

	// IETF RFC 6749 - The OAuth 2.0 Authorization Framework
	//
	// https://datatracker.ietf.org/doc/html/rfc6749#section-4.1.1
	//
	// 4.1.1.  Authorization Request
	//
	//    The client constructs the request URI by adding the following
	//    parameters to the query component of the authorization endpoint URI
	//    using the "application/x-www-form-urlencoded" format, per Appendix B:
	//
	//    response_type
	//          REQUIRED.  Value MUST be set to "code".
	//
	//    client_id
	//          REQUIRED.  The client identifier as described in Section 2.2.
	//
	//    redirect_uri
	//          OPTIONAL.  As described in Section 3.1.2.
	//
	//    scope
	//          OPTIONAL.  The scope of the access request as described by
	//          Section 3.3.
	//
	//    state
	//          RECOMMENDED.  An opaque value used by the client to maintain
	//          state between the request and callback.  The authorization
	//          server includes this value when redirecting the user-agent back
	//          to the client.  The parameter SHOULD be used for preventing
	//          cross-site request forgery as described in Section 10.12.

	// IETF RFC 7636 - Proof Key for Code Exchange by OAuth Public Clients
	//
	// https://datatracker.ietf.org/doc/html/rfc7636#section-4.3
	//
	// 4.3.  Client Sends the Code Challenge with the Authorization Request
	//
	//    The client sends the code challenge as part of the OAuth 2.0
	//    Authorization Request (Section 4.1.1 of [RFC6749]) using the
	//    following additional parameters:
	//
	//    code_challenge
	//       REQUIRED.  Code challenge.
	//
	//    code_challenge_method
	//       OPTIONAL, defaults to "plain" if not present in the request.  Code
	//       verifier transformation method is "S256" or "plain".

	// OpenID Connect Core 1.0
	//
	// https://openid.net/specs/openid-connect-core-1_0.html#AuthRequest
	//
	// 3.1.2.1.  Authentication Request
	//
	//    An Authentication Request is an OAuth 2.0 Authorization Request that requests that the End-User be authenticated by the Authorization Server.
	//
	//    Authorization Servers MUST support the use of the HTTP GET and POST methods defined in RFC 7231 [RFC7231] at the Authorization Endpoint.
	//    Clients MAY use the HTTP GET or POST methods to send the Authorization Request to the Authorization Server.
	//    If using the HTTP GET method, the request parameters are serialized using URI Query String Serialization, per Section 13.1.
	//    If using the HTTP POST method, the request parameters are serialized using Form Serialization, per Section 13.2.
	//
	//    OpenID Connect uses the following OAuth 2.0 request parameters with the Authorization Code Flow:
	//
	//       scope
	//          REQUIRED. OpenID Connect requests MUST contain the openid scope value.
	//          If the openid scope value is not present, the behavior is entirely unspecified.
	//          Other scope values MAY be present. Scope values used that are not understood by an implementation SHOULD be ignored.
	//          See Sections 5.4 and 11 for additional scope values defined by this specification.
	//
	//       response_type
	//          REQUIRED. OAuth 2.0 Response Type value that determines the authorization processing flow to be used,
	//          including what parameters are returned from the endpoints used.
	//          When using the Authorization Code Flow, this value is code.
	//
	//       client_id
	//          REQUIRED. OAuth 2.0 Client Identifier valid at the Authorization Server.
	//
	//       redirect_uri
	//          REQUIRED. Redirection URI to which the response will be sent.
	//          This URI MUST exactly match one of the Redirection URI values for the Client pre-registered at the OpenID Provider,
	//          with the matching performed as described in Section 6.2.1 of [RFC3986] (Simple String Comparison).
	//          When using this flow, the Redirection URI SHOULD use the https scheme; however, it MAY use the http scheme, provided that the Client Type
	//          is confidential, as defined in Section 2.1 of OAuth 2.0, and provided the OP allows the use of http Redirection URIs in this case.
	//          Also, if the Client is a native application, it MAY use the http scheme with localhost or the IP loopback literals 127.0.0.1 or [::1] as the hostname.
	//          The Redirection URI MAY use an alternate scheme, such as one that is intended to identify a callback into a native application.
	//
	//       state
	//          RECOMMENDED. Opaque value used to maintain state between the request and the callback.
	//          Typically, Cross-Site Request Forgery (CSRF, XSRF) mitigation is done by cryptographically
	//          binding the value of this parameter with a browser cookie.
	//
	// This specification also defines the following request parameters:
	//
	//    nonce
	//       OPTIONAL. String value used to associate a Client session with an ID Token, and to mitigate replay attacks.
	//       The value is passed through unmodified from the Authentication Request to the ID Token.
	//       Sufficient entropy MUST be present in the nonce values used to prevent attackers from guessing values.
	//       For implementation notes, see Section 15.5.2.
	//
	//    display
	//       OPTIONAL. ASCII string value that specifies how the Authorization Server displays the authentication and consent user interface pages to the End-User.
	//       The defined values are:
	//          page
	//             The Authorization Server SHOULD display the authentication and consent UI consistent with a full User Agent page view.
	//             If the display parameter is not specified, this is the default display mode.
	//          popup
	//             The Authorization Server SHOULD display the authentication and consent UI consistent with a popup User Agent window.
	//             The popup User Agent window should be of an appropriate size for a login-focused dialog and should not obscure the entire window that it is popping up over.
	//          touch
	//             The Authorization Server SHOULD display the authentication and consent UI consistent with a device that leverages a touch interface.
	//          wap
	//             The Authorization Server SHOULD display the authentication and consent UI consistent with a "feature phone" type display.
	//             The Authorization Server MAY also attempt to detect the capabilities of the User Agent and present an appropriate display.
	//             If an OP receives a display value outside the set defined above that it does not understand, it MAY return an error or it MAY ignore it;
	//             in practice, not returning errors for not-understood values will help facilitate phasing in extensions using new display values.
	//
	//    prompt
	//       OPTIONAL. Space-delimited, case-sensitive list of ASCII string values that specifies whether the Authorization Server prompts the End-User for reauthentication and consent.
	//       The defined values are:
	//          none
	//             The Authorization Server MUST NOT display any authentication or consent user interface pages.
	//             An error is returned if an End-User is not already authenticated or the Client does not have pre-configured consent
	//             for the requested Claims or does not fulfill other conditions for processing the request.
	//             The error code will typically be login_required, interaction_required, or another code defined in Section 3.1.2.6.
	//             This can be used as a method to check for existing authentication and/or consent.
	//          login
	//             The Authorization Server SHOULD prompt the End-User for reauthentication.
	//             If it cannot reauthenticate the End-User, it MUST return an error, typically login_required.
	//          consent
	//             The Authorization Server SHOULD prompt the End-User for consent before returning information to the Client.
	//             If it cannot obtain consent, it MUST return an error, typically consent_required.
	//          select_account
	//             The Authorization Server SHOULD prompt the End-User to select a user account.
	//             This enables an End-User who has multiple accounts at the Authorization Server to select amongst the multiple accounts that they might have current sessions for.
	//             If it cannot obtain an account selection choice made by the End-User, it MUST return an error, typically account_selection_required.
	//             The prompt parameter can be used by the Client to make sure that the End-User is still present for the current session or to bring attention to the request.
	//             If this parameter contains none with any other value, an error is returned.
	//             If an OP receives a prompt value outside the set defined above that it does not understand, it MAY return an error or it MAY ignore it;
	//             in practice, not returning errors for not-understood values will help facilitate phasing in extensions using new prompt values.
	//
	//    login_hint
	//       OPTIONAL. Hint to the Authorization Server about the login identifier the End-User might use to log in (if necessary).
	//       This hint can be used by an RP if it first asks the End-User for their e-mail address (or other identifier) and then wants
	//       to pass that value as a hint to the discovered authorization service.
	//       It is RECOMMENDED that the hint value match the value used for discovery.
	//       This value MAY also be a phone number in the format specified for the phone_number Claim.
	//       The use of this parameter is left to the OP's discretion.

	sBrowser = strings.ReplaceAll (sBrowser, PLACEHOLDER, idp.M_sAuthURL +
		"?response_type=code" +
		"&client_id="      + url.QueryEscape (sClientID) +
		"&redirect_uri="   + url.QueryEscape (sRedirectURL) +
		"&code_challenge=" + url.QueryEscape (sCodeChallenge) +
		"&code_challenge_method=S256" +
		"&scope="          + url.QueryEscape (sOIDCScope) +
		"&state="          + base64.RawURLEncoding.EncodeToString (randomBytes (16)) + // some identity providers require state
		ternary (bUseLoginHint,
		"&login_hint="     + url.QueryEscape (sUserName), "") +
		ternary (bUsePrompt,
		"&prompt="         + url.QueryEscape (con.m_params.M_sOIDCPrompt), ""))

	// https://support.pingidentity.com/s/article/Intermittent-Page-Expired-errors-due-to-multiple-browser-tabs
	//
	// Sometimes, PingFederate renders the state.not.found.error.page.template.html Velocity template with a Page Expired message when requests are made from multiple tabs at the same time.
	// A typical example would be setting the browser's startup pages to multiple SSO URLs on the same PingFederate instance.
	// The reason this happens is due to the PF session cookie in one tab's request being overwritten by a request/response in another browser tab.
	// As a result, one or more of the tabs may end up with a Page Expired message from PingFederate due to the session state being overwritten by another request.

	var mapRequestParams map [string] string

	g_mutexActiveBrowser.Lock ()
	func () {
		defer g_mutexActiveBrowser.Unlock ()

		if con.hasTimingLog () {
			timingLog ("Launching browser " + sBrowser)
		}

		err = execCommandLine (sBrowser)
		if err != nil {
			err = con.makeDriverError (err, 564, fmt.Sprintf ("Cannot start browser command %v", sBrowser))
			return
		}

		mapRequestParams, err = http.waitForRequestParams ()
		if err != nil { return }
	} ()

	if err != nil { return }

	sCode := mapRequestParams ["code"]
	if sCode == "" {
		err = con.makeDriverError (nil, 565, fmt.Sprintf ("Did not receive authorization code for logmech %v", con.m_params.M_sLogMech))
		return
	}

	// IETF RFC 6749 - The OAuth 2.0 Authorization Framework
	//
	// https://datatracker.ietf.org/doc/html/rfc6749#section-4.1.3
	//
	// 4.1.3.  Access Token Request
	//
	//    The client makes a request to the token endpoint by sending the
	//    following parameters using the "application/x-www-form-urlencoded"
	//    format per Appendix B with a character encoding of UTF-8 in the HTTP
	//    request entity-body:
	//
	//    grant_type
	//          REQUIRED.  Value MUST be set to "authorization_code".
	//
	//    code
	//          REQUIRED.  The authorization code received from the
	//          authorization server.
	//
	//    redirect_uri
	//          REQUIRED, if the "redirect_uri" parameter was included in the
	//          authorization request as described in Section 4.1.1, and their
	//          values MUST be identical.
	//
	//    client_id
	//          REQUIRED, if the client is not authenticating with the
	//          authorization server as described in Section 3.2.1.

	// IETF RFC 7636 - Proof Key for Code Exchange by OAuth Public Clients
	//
	// https://datatracker.ietf.org/doc/html/rfc7636#section-4.5
	//
	// 4.5.  Client Sends the Authorization Code and the Code Verifier to the Token Endpoint
	//
	//    Upon receipt of the Authorization Code, the client sends the Access
	//    Token Request to the token endpoint.  In addition to the parameters
	//    defined in the OAuth 2.0 Access Token Request (Section 4.1.3 of
	//    [RFC6749]), it sends the following parameter:
	//
	//    code_verifier
	//       REQUIRED.  Code verifier

	sFormData := "grant_type=authorization_code" +
		"&client_id="     + url.QueryEscape (sClientID) +
		"&code="          + url.QueryEscape (sCode) +
		"&redirect_uri="  + url.QueryEscape (sRedirectURL) +
		"&code_verifier=" + url.QueryEscape (sCodeVerifier)

	if con.hasDebugLog () {
		debugLog ("doBrowserAuth: sClientID=" + sClientID)
		debugLog ("doBrowserAuth: sCode=" + sCode)
		debugLog ("doBrowserAuth: sRedirectURL=" + sRedirectURL)
		debugLog ("doBrowserAuth: sCodeVerifier=" + sCodeVerifier)
		debugLog ("doBrowserAuth: sFormData=" + sFormData)
	}

	resp, err = con.doHttpRequest ("POST", idp.M_sTokenURL, [][] string {{ "Content-Type", "application/x-www-form-urlencoded" }}, [] byte (sFormData), /*anRequiredRespCodes*/ [] int {200})
	if err != nil {
		err = con.makeDriverError (err, 567, "Failed to obtain token from Identity Provider")
		return
	}

	sJSON = string (resp.M_abyBody)

	err = con.putCacheEntry (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow, idp.M_sTokenURL, sJSON, cc)
	return

} // end doBrowserAuth
