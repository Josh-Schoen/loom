// Copyright 2024 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

type CacheEntry struct {

	sEndpointURL      string
	sRefreshToken     string
	sJWT              string
	nExpireTimeSecs   int
	sClaimValue       string
	sUserDesc         string
	nReuseCount       int
	nCreateTimeSecs   int
	nAccessTimeMs     int

} // end type CacheEntry

var g_mutexTokenCache sync.Mutex // protects g_mapTokenCache
var g_mapTokenCache = map [string] *CacheEntry {}

type CacheContext struct {

	sCacheStatus      string // empty string for uncached OIDC flow
	sRefreshToken     string
	sJWT              string
	nExpireTimeSecs   int
	sClaimValue       string
	sUserDesc         string
	nReuseCount       int

} // end type CacheContext

func (cc *CacheContext) tokenTTLSecs () int { return cc.nExpireTimeSecs - currentTimeSeconds () }

func newCacheEntry (sEndpointURL string, cc *CacheContext) (entry *CacheEntry) {

	entry = &CacheEntry {}
	entry.sEndpointURL    = sEndpointURL
	entry.sRefreshToken   = cc.sRefreshToken
	entry.sJWT            = cc.sJWT
	entry.nExpireTimeSecs = cc.nExpireTimeSecs
	entry.sClaimValue     = cc.sClaimValue
	entry.sUserDesc       = cc.sUserDesc
	entry.nReuseCount     = 0
	entry.nAccessTimeMs   = currentTimeMillis ()
	entry.nCreateTimeSecs = entry.nAccessTimeMs / 1000
	return

} // end newCacheEntry

func copyCacheEntry (entry *CacheEntry) *CacheEntry {

	if entry == nil {
		return nil
	}

	entry.nReuseCount++
	entry.nAccessTimeMs = currentTimeMillis ()

	clone := *entry // clone a struct containing only primitive types by copying its value to a new variable
	return &clone

} // end copyCacheEntry

func composeCacheKey (sLogMech string, sIdProURL string, sClientID string, sOIDCScope string, sOIDCToken string, sOIDCClaim string, sUserName string, sPassword string, sLogData string) string {

	sUserName = strings.ToLower (sUserName) // case insensitive for email address used as login hint

	return url.QueryEscape (sLogMech) +
		"&" + url.QueryEscape (sIdProURL) +
		"&" + url.QueryEscape (sClientID) +
		"&" + url.QueryEscape (sOIDCScope) +
		"&" + url.QueryEscape (sOIDCToken) +
		"&" + url.QueryEscape (sOIDCClaim) +
		"&" + url.QueryEscape (sUserName) +
		"&" + url.QueryEscape (sPassword) +
		"&" + url.QueryEscape (sLogData)

} // end composeCacheKey

func (con *TeradataConnection) getJWTHeaderAndPayload (sJWT string) (sJWTHeader string, sJWTPayload string, err error) {

	asParts := strings.Split (sJWT, ".")

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("getJWTHeaderAndPayload: len(asParts)=%v asParts=%v", len (asParts), asParts))
	}

	if len (asParts) != 3 { return }

	abyHeader, err := base64.RawURLEncoding.DecodeString (asParts [0])
	if err != nil { return }

	abyPayload, err := base64.RawURLEncoding.DecodeString (asParts [1])
	if err != nil { return }

	sJWTHeader  = string (abyHeader)
	sJWTPayload = string (abyPayload)

	if con.hasDebugLog () {
		debugLog ("getJWTHeaderAndPayload: parsed sJWTHeader=" + sJWTHeader)
		debugLog ("getJWTHeaderAndPayload: parsed sJWTPayload=" + sJWTPayload)
	}

	return

} // end getJWTHeaderAndPayload

func (con *TeradataConnection) parseIdPResponse (sEndpointURL string, sIdPResponse string, cc *CacheContext) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter parseIdPResponse %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave parseIdPResponse %v err=%v", con, err)) } ()
	}

	if con.hasDebugLog () {
		debugLog ("parseIdPResponse: M_sLogMech = " + con.m_params.M_sLogMech)
		debugLog ("parseIdPResponse: M_sOIDCToken = " + con.m_params.M_sOIDCToken)
		debugLog ("parseIdPResponse: M_sOIDCClaim = " + con.m_params.M_sOIDCClaim)
		debugLog ("parseIdPResponse: sEndpointURL = " + sEndpointURL)
		debugLog ("parseIdPResponse: sIdPResponse = " + sIdPResponse)
	}

	cc.sRefreshToken, _ = con.getStringFromJSON ("refresh_token", sIdPResponse, /*nErrorCode*/ 0, /*sErrorMsg*/ "") // no error, default empty string

	cc.sJWT, err = con.getStringFromJSON (con.m_params.M_sOIDCToken, sIdPResponse, 568, fmt.Sprintf ("Missing %v in response from Identity Provider %v", con.m_params.M_sOIDCToken, sEndpointURL))
	if err != nil { return }

	sJWTHeader, sJWTPayload, err := con.getJWTHeaderAndPayload (cc.sJWT)
	if err != nil {
		err = con.makeDriverError (err, 568, fmt.Sprintf ("Missing %v in response from Identity Provider %v", con.m_params.M_sOIDCToken, sEndpointURL))
		return
	}

	if con.hasDebugLog () {
		debugLog ("parseIdPResponse: sJWTHeader=" + sJWTHeader)
		debugLog ("parseIdPResponse: sJWTPayload=" + sJWTPayload)
	}

	if sJWTHeader == "" || sJWTPayload == "" {
		err = con.makeDriverError (nil, 568, fmt.Sprintf ("Missing %v in response from Identity Provider %v", con.m_params.M_sOIDCToken, sEndpointURL))
		return
	}

	cc.nExpireTimeSecs, _ = con.getIntFromJSON ("exp", sJWTPayload, /*nErrorCode*/ 0, /*sErrorMsg*/ "", /*nDefault*/ 0) // no error, default 0 is always expired
	cc.sClaimValue, _ = con.getStringFromJSON (con.m_params.M_sOIDCClaim, sJWTPayload, /*nErrorCode*/ 0, /*sErrorMsg*/ "") // no error, default empty string

	cc.sUserDesc = con.m_params.M_sOIDCToken + " " + con.m_params.M_sOIDCClaim + " " + coalesce (cc.sClaimValue, "unavailable")

	if con.hasDebugLog () {
		debugLog ("parseIdPResponse: sCacheStatus="    +       cc.sCacheStatus)
		debugLog ("parseIdPResponse: sRefreshToken="   +       cc.sRefreshToken)
		debugLog ("parseIdPResponse: sJWT="            +       cc.sJWT)
		debugLog ("parseIdPResponse: nExpireTimeSecs=" + itoa (cc.nExpireTimeSecs))
		debugLog ("parseIdPResponse: sClaimValue="     +       cc.sClaimValue)
		debugLog ("parseIdPResponse: sUserDesc="       +       cc.sUserDesc)
		debugLog ("parseIdPResponse: nReuseCount="     + itoa (cc.nReuseCount))
	}

	return

} // end parseIdPResponse

const FINDER_SEP = "\u001f" // ASCII Unit Separator (US) control character

func makeFinder (sUserName string, sPassword string, sIdProURL string, sClientID string, sOIDCScope string, bOAuthFlow bool) string {

	return sUserName + FINDER_SEP +
		sPassword + FINDER_SEP +
		sIdProURL + FINDER_SEP +
		sClientID + FINDER_SEP +
		sOIDCScope + FINDER_SEP +
		btoa (bOAuthFlow)

} // end makeFinder

func (con *TeradataConnection) findToken (sFinder string) ([] string, error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter findToken %v sFinder=%v", con, sFinder))
		defer func () { traceLog (fmt.Sprintf ("< leave findToken %v sFinder=%v", con, sFinder)) } ()
	}

	asParts := strings.Split (sFinder, FINDER_SEP)

	sUserName  := asParts [0]
	sPassword  := asParts [1]
	sIdProURL  := asParts [2]
	sClientID  := asParts [3]
	sOIDCScope := asParts [4]
	bOAuthFlow := atob (asParts [5])

	return con.doSingleOIDCFlow (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow)

} // end findToken

func (con *TeradataConnection) doSingleOIDCFlow (sUserName string, sPassword string, sIdProURL string, sClientID string, sOIDCScope string, bOAuthFlow bool) (asOutput [] string, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter doSingleOIDCFlow %v bOAuthFlow=%v sOIDCScope=%v", con, bOAuthFlow, sOIDCScope))
		defer func () { traceLog (fmt.Sprintf ("< leave doSingleOIDCFlow %v bOAuthFlow=%v sOIDCScope=%v err=%v", con, bOAuthFlow, sOIDCScope, err)) } ()
	}

	cc, err := con.getCacheEntry (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow)
	if err != nil { return }

	if cc.sJWT == "" { // cache miss

		switch con.m_params.M_sLogMech {
			case "BEARER"  : err = con.doBearerFlow       (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow, cc)
			case "BROWSER" : err = con.doBrowserAuth      (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow, cc)
			case "CODE"    : err = con.doDeviceCodeFlow   (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow, cc)
			case "CRED"    : err = con.doClientCredFlow   (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow, cc)
			case "ROPC"    : err = con.doPasswordFlow     (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow, cc)
			case "SECRET"  : err = con.doClientSecretFlow (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow, cc)
		}

		if err != nil { return }

	} // end if cache miss

	if cc.sJWT != "" {
		asOutput = [] string { sOIDCScope, cc.sJWT }
	}

	return

} // end doSingleOIDCFlow

func (con *TeradataConnection) doAllNeededOIDCFlows () (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter doAllNeededOIDCFlows %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave doAllNeededOIDCFlows %v err=%v", con, err)) } ()
	}

	switch con.m_params.M_sLogMech {
		case "BEARER"  : err = con.validateBearerFlow       ()
		case "BROWSER" : err = con.validateBrowserAuth      ()
		case "CODE"    : err = con.validateDeviceCodeFlow   ()
		case "CRED"    : err = con.validateClientCredFlow   ()
		case "ROPC"    : err = con.validatePasswordFlow     ()
		case "SECRET"  : err = con.validateClientSecretFlow ()
		default        : return // this LOGMECH is not an OIDC flow
	}

	if err != nil { return }

	if con.m_params.M_sHostName != "-none" && (con.m_gtwConfig.M_sIdentityProviderURL == "" || con.m_gtwConfig.M_sIdentityProviderClientID == "") {
		err = con.makeDriverError (nil, 556, fmt.Sprintf ("This database does not support logmech %v", con.m_params.M_sLogMech))
		return
	}

	var sClientID, sOIDCScope string

	if con.m_params.M_sLogMech == "CRED" {

		sFormData := con.m_params.M_sLogData
		if con.hasDebugLog () {
			debugLog ("doAllNeededOIDCFlows: sFormData=" + sFormData)
		}

		mapFormData := parseFormData (sFormData)
		sClientID  = mapFormData ["client_id"]
		sOIDCScope = mapFormData ["scope"]

	} else { // not CRED

		// Connection parameter is higher priority than Gateway config
		sClientID  = coalesce  (con.m_params.M_sOIDCClientID, con.m_gtwConfig.M_sIdentityProviderClientID)
		sOIDCScope = coalesce3 (con.m_params.M_sOIDCScope   , con.m_gtwConfig.M_sIdentityProviderOIDCScope, "openid") // default is "openid" when missing both connection parameter and Gateway config

	} // end else not CRED

	if con.hasDebugLog () {
		debugLog ("doAllNeededOIDCFlows: sClientID=" + sClientID)
		debugLog ("doAllNeededOIDCFlows: sOIDCScope=" + sOIDCScope)
	}

	// Connection parameter is higher priority than Gateway config
	sIdProURL := coalesce  (con.m_params.M_sOIDCMetadata, con.m_gtwConfig.M_sIdentityProviderURL)
	if con.hasDebugLog () {
		debugLog ("doAllNeededOIDCFlows: sIdProURL=" + sIdProURL)
	}

	sIdProURL = strings.TrimRight (sIdProURL, "/") // trim any trailing slashes

	sMetadataEndpointURI := "/.well-known/openid-configuration"
	if ! strings.HasSuffix (strings.ToLower (sIdProURL), sMetadataEndpointURI) { // add the URI if missing
		sIdProURL += sMetadataEndpointURI
		if con.hasDebugLog () {
			debugLog ("doAllNeededOIDCFlows: Modified sIdProURL=" + sIdProURL)
		}
	}

	asScopes := strings.Split (joinStrings (sOIDCScope, "|", con.m_params.M_sOAuthScopes), "|")

	for i := 0 ; i < len (asScopes) ; i++ {

		_, err = con.doSingleOIDCFlow (con.m_params.M_sUserName, con.m_params.M_sPassword, sIdProURL, sClientID, asScopes [i], i > 0)
		if err != nil { return }
	}

	return

} // end doAllNeededOIDCFlows

func (con *TeradataConnection) completeOIDCFlow (sUserName string, sPassword string, sIdProURL string, sClientID string, sOIDCScope string, bOAuthFlow bool, cc *CacheContext) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter completeOIDCFlow %v bOAuthFlow=%v sOIDCScope=%v", con, bOAuthFlow, sOIDCScope))
		defer func () { traceLog (fmt.Sprintf ("< leave completeOIDCFlow %v bOAuthFlow=%v sOIDCScope=%v", con, bOAuthFlow, sOIDCScope)) } ()
	}

	// sClientID and sOIDCScope may be empty for ClientCredFlow

	sReuseCount := ternary (cc.sCacheStatus != "", itoa (cc.nReuseCount), "")
	sRefreshTokenAvail := yn (cc.sRefreshToken != "")
	sTokenTTLSecs := itoa (cc.tokenTTLSecs ())

	// If we completed the OIDC logon flow, then this is an additional OAuth flow whose refresh token status we must save.
	// This sequence of refresh token Y/N values is stored in the ClientAttributesEx OAS name-value pair.
	// The OIDC logon flow refresh token status is skipped here, because it is stored in the ClientAttributes RT name-value pair.
	if bOAuthFlow && sOIDCScope != "" {
		con.m_extra.m_sOAuthReuseCounts  = joinStrings (con.m_extra.m_sOAuthReuseCounts , ",", sReuseCount)
		con.m_extra.m_sOAuthRefreshAvail = joinStrings (con.m_extra.m_sOAuthRefreshAvail, "" , sRefreshTokenAvail)
		con.m_extra.m_sOAuthScopes       = joinStrings (con.m_extra.m_sOAuthScopes      , "|", sOIDCScope)
	}

	bSendOIDCToken  := (con.m_params.M_uOAuthLevel & 1) != 0
	bSendOAuthToken := (con.m_params.M_uOAuthLevel & 2) != 0
	bAddToken := bOAuthFlow && bSendOAuthToken || ! bOAuthFlow && bSendOIDCToken || con.m_params.M_bTokenMode
	if bAddToken && sOIDCScope != "" {
		con.m_setTokenFinders.Add (makeFinder (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow))
	}

	if con.hasDebugLog () {
		debugLog ("completeOIDCFlow: M_sLogMech="            +       con.m_params.M_sLogMech)
		debugLog ("completeOIDCFlow: bOAuthFlow="            + btoa (bOAuthFlow))
		debugLog ("completeOIDCFlow: m_sOAuthReuseCounts="   +       con.m_extra.m_sOAuthReuseCounts)
		debugLog ("completeOIDCFlow: m_sOAuthRefreshAvail="  +       con.m_extra.m_sOAuthRefreshAvail)
		debugLog ("completeOIDCFlow: m_sOAuthScopes="        +       con.m_extra.m_sOAuthScopes)
		debugLog ("completeOIDCFlow: M_uOAuthLevel="         + utoa (con.m_params.M_uOAuthLevel))
		debugLog ("completeOIDCFlow: bSendOIDCToken="        + btoa (bSendOIDCToken))
		debugLog ("completeOIDCFlow: bSendOAuthToken="       + btoa (bSendOAuthToken))
		debugLog ("completeOIDCFlow: M_bTokenMode="          + btoa (con.m_params.M_bTokenMode))
		debugLog ("completeOIDCFlow: bAddToken="             + btoa (bAddToken))
		debugLog ("completeOIDCFlow: m_setTokenFinders.Len=" + itoa (con.m_setTokenFinders.Len ()))
		debugLog ("completeOIDCFlow: sIdProURL="             +       sIdProURL)
		debugLog ("completeOIDCFlow: sClientID="             +       sClientID)
		debugLog ("completeOIDCFlow: sOIDCScope="            +       sOIDCScope)
		debugLog ("completeOIDCFlow: sUserName="             +       sUserName)
		debugLog ("completeOIDCFlow: sPassword="             +       sPassword)
		debugLog ("completeOIDCFlow: sUserDesc="             +       cc.sUserDesc)
		debugLog ("completeOIDCFlow: sCacheStatus="          +       cc.sCacheStatus)
		debugLog ("completeOIDCFlow: sReuseCount="           +       sReuseCount)
		debugLog ("completeOIDCFlow: sRefreshTokenAvail="    +       sRefreshTokenAvail)
		debugLog ("completeOIDCFlow: sTokenTTLSecs="         +       sTokenTTLSecs)
		debugLog ("completeOIDCFlow: sJWT="                  +       cc.sJWT)
	}

	// If we completed the OIDC logon flow, then this is an additional OAuth flow, and we are done.
	if bOAuthFlow { return }

	con.m_extra.m_sOIDCMetadataURL   = sIdProURL
	con.m_extra.m_sOIDCClientID      = sClientID
	con.m_extra.m_sLogonOIDCScope    = sOIDCScope
	con.m_extra.m_sOIDCLoginHint     = sUserName
	con.m_extra.m_sOIDCTokenKind     = cc.sUserDesc
	con.m_extra.m_sTokenCacheStatus  = cc.sCacheStatus
	con.m_extra.m_sTokenReuseCount   = sReuseCount
	con.m_extra.m_sRefreshTokenAvail = sRefreshTokenAvail
	con.m_extra.m_sTokenTTLSecs      = sTokenTTLSecs

	// Switch to logmech=JWT for logon.
	con.m_sLogMechForTDGSS = "JWT"
	con.m_sLogDataForTDGSS = "token=" + cc.sJWT

	if con.hasDebugLog () {
		debugLog ("completeOIDCFlow: m_sLogMechForTDGSS=" + con.m_sLogMechForTDGSS)
		debugLog ("completeOIDCFlow: m_sLogDataForTDGSS=" + con.m_sLogDataForTDGSS)
	}

	if cc.sCacheStatus != "" {
		con.m_bNoTransmitCredentials = true
	}
} // end completeOIDCFlow

func (con *TeradataConnection) getCacheEntry (sUserName string, sPassword string, sIdProURL string, sClientID string, sOIDCScope string, bOAuthFlow bool) (cc *CacheContext, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter getCacheEntry %v bOAuthFlow=%v sOIDCScope=%v", con, bOAuthFlow, sOIDCScope))
		defer func () { traceLog (fmt.Sprintf ("< leave getCacheEntry %v bOAuthFlow=%v sOIDCScope=%v err=%v", con, bOAuthFlow, sOIDCScope, err)) } ()
	}

	if con.hasDebugLog () {
		debugLog ("getCacheEntry: M_uOIDCCacheSize=" + utoa (con.m_params.M_uOIDCCacheSize) + " len(g_mapTokenCache)=" + itoa (len (g_mapTokenCache)))
		debugLog ("getCacheEntry: M_sLogMech="       + con.m_params.M_sLogMech)
		debugLog ("getCacheEntry: sIdProURL="        + sIdProURL)
		debugLog ("getCacheEntry: sClientID="        + sClientID)
		debugLog ("getCacheEntry: sOIDCScope="       + sOIDCScope)
		debugLog ("getCacheEntry: bOAuthFlow="       + btoa (bOAuthFlow))
		debugLog ("getCacheEntry: M_sOIDCToken="     + con.m_params.M_sOIDCToken)
		debugLog ("getCacheEntry: M_sOIDCClaim="     + con.m_params.M_sOIDCClaim)
		debugLog ("getCacheEntry: sUserName="        + sUserName)
		debugLog ("getCacheEntry: sPassword="        + sPassword)
		debugLog ("getCacheEntry: M_sLogData="       + con.m_params.M_sLogData)
	}

	cc = &CacheContext {}
	var sCacheStatusDesc string

	if con.m_params.M_uOIDCCacheSize == 0 {

		cc.sCacheStatus = "O"
		sCacheStatusDesc = "turned off"

	} else { // cache turned on

		sKey := composeCacheKey (con.m_params.M_sLogMech, sIdProURL, sClientID, sOIDCScope, con.m_params.M_sOIDCToken, con.m_params.M_sOIDCClaim, sUserName, sPassword, con.m_params.M_sLogData)

		if con.hasDebugLog () {
			debugLog ("getCacheEntry: sKey=" + sKey)
		}

		g_mutexTokenCache.Lock () // synchronized (g_mapTokenCache)

		entry := copyCacheEntry (g_mapTokenCache [sKey])

		g_mutexTokenCache.Unlock () // end synchronized (g_mapTokenCache)

		if entry == nil { // cache miss

			cc.sCacheStatus = "M"
			sCacheStatusDesc = "miss"

		} else { // cache hit

			// oidc_refresh_percent=0 (zero) means we use the full token lifetime with no early refresh
			// oidc_refresh_percent=100      means we never reuse the token and we always refresh
			nEarlyPercent   := int (con.m_params.M_uOIDCRefreshPercent)
			bRefreshEnabled := con.m_params.M_bOIDCRefresh

			nLifespanSecs := 0
			if entry.nExpireTimeSecs > entry.nCreateTimeSecs {
				nLifespanSecs = entry.nExpireTimeSecs - entry.nCreateTimeSecs
			}

			nEarlyAmountSecs := nLifespanSecs * nEarlyPercent / 100
			nDeadlineSecs := entry.nExpireTimeSecs - nEarlyAmountSecs
			nCurrentTimeSecs := currentTimeSeconds ()
			bValid := nCurrentTimeSecs < nDeadlineSecs

			if con.hasDebugLog () {
				debugLog ("getCacheEntry:" +
					" nEarlyPercent="    + itoa (nEarlyPercent) +
					" bRefreshEnabled="  + btoa (bRefreshEnabled) +
					" nExpireTimeSecs="  + itoa (entry.nExpireTimeSecs) +
					" nLifespanSecs="    + itoa (nLifespanSecs) +
					" nEarlyAmountSecs=" + itoa (nEarlyAmountSecs) +
					" nDeadlineSecs="    + itoa (nDeadlineSecs) +
					" nCurrentTimeSecs=" + itoa (nCurrentTimeSecs) +
					" bValid="           + btoa (bValid) +
					" sOIDCScope="       + sOIDCScope)
			}

			sTokenTitle := con.m_params.M_sOIDCToken + " reused " + ternary (entry.nReuseCount == 1, "once", itoa (entry.nReuseCount) + " times")

			if bValid { // valid unexpired token

				cc.sCacheStatus    = "H"
				sCacheStatusDesc   = "hit with valid " + sTokenTitle
				cc.sRefreshToken   = entry.sRefreshToken
				cc.sJWT            = entry.sJWT
				cc.nExpireTimeSecs = entry.nExpireTimeSecs
				cc.sClaimValue     = entry.sClaimValue
				cc.sUserDesc       = entry.sUserDesc
				cc.nReuseCount     = entry.nReuseCount

			} else if entry.sRefreshToken == "" { // no refresh token

				cc.sCacheStatus  = "X"
				sCacheStatusDesc = "hit with expired " + sTokenTitle ;

			} else if bRefreshEnabled { // refresh token available and refresh enabled

				err = con.doRefreshFlow (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow, entry.sRefreshToken, cc)
				if err != nil { return }

				if cc.sJWT != "" {
					cc.sCacheStatus = "R"
					sCacheStatusDesc = "hit with expired " + sTokenTitle + ", used refresh token to obtain new " + con.m_params.M_sOIDCToken

				} else {
					cc.sCacheStatus = "F"
					sCacheStatusDesc = "hit with expired " + sTokenTitle + ", refresh failed"
				}
			} else { // refresh token available, but refresh disabled

				cc.sCacheStatus = "D"
				sCacheStatusDesc = "hit with expired " + sTokenTitle + " and refresh disabled"
			}
		} // end else cache hit
	} // end else cache turned on

	if con.hasDebugLog () {
		debugLog ("getCacheEntry: OIDC token cache " + sCacheStatusDesc +
			" for " + ternary (sUserName != "", "user " + sUserName, "default user") +
			" scope " + sOIDCScope)
	}

	// cc.sJWT != null   cache hit with valid unexpired token
	// cc.sJWT == null   cache miss or expired

	if cc.sJWT != "" {
		con.completeOIDCFlow (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow, cc)
	}

	return

} // end getCacheEntry

func (con *TeradataConnection) putCacheEntry (sUserName string, sPassword string, sIdProURL string, sClientID string, sOIDCScope string, bOAuthFlow bool, sEndpointURL string, sIdPResponse string, cc *CacheContext) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter putCacheEntry %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave putCacheEntry %v err=%v", con, err)) } ()
	}

	if con.hasDebugLog () {

		debugLog ("putCacheEntry: M_uOIDCCacheSize=" + utoa (con.m_params.M_uOIDCCacheSize) + " len(g_mapTokenCache)=" + itoa (len (g_mapTokenCache)))
		debugLog ("putCacheEntry: sEndpointURL="     + sEndpointURL)
		debugLog ("putCacheEntry: sIdPResponse="     + sIdPResponse)
		debugLog ("putCacheEntry: M_sLogMech="       + con.m_params.M_sLogMech)
		debugLog ("putCacheEntry: sIdProURL="        + sIdProURL)
		debugLog ("putCacheEntry: sClientID="        + sClientID)
		debugLog ("putCacheEntry: sOIDCScope="       + sOIDCScope)
		debugLog ("putCacheEntry: bOAuthFlow="       + btoa (bOAuthFlow))
		debugLog ("putCacheEntry: M_sOIDCToken="     + con.m_params.M_sOIDCToken)
		debugLog ("putCacheEntry: M_sOIDCClaim="     + con.m_params.M_sOIDCClaim)
		debugLog ("putCacheEntry: sUserName="        + sUserName)
		debugLog ("putCacheEntry: sPassword="        + sPassword)
		debugLog ("putCacheEntry: M_sLogData="       + con.m_params.M_sLogData)
	}

	err = con.parseIdPResponse (sEndpointURL, sIdPResponse, cc)
	if err != nil { return }

	// If the username is specified as a login hint, then the claim value used as part of the cache key.
	// If the username is omitted, then an empty claim value portion is used as part of the cache key.
	sKeyClaimValue := ternary (sUserName != "", cc.sClaimValue, "")

	if con.hasDebugLog () {
		debugLog ("putCacheEntry: sKeyClaimValue=" + sKeyClaimValue)
	}

	if con.m_params.M_uOIDCCacheSize > 0 {

		// If the username is specified as a login hint, then the claim value is required for use as part of the cache key, and we fail if the claim value is not available.
		if (sUserName != "" && sKeyClaimValue == "") {
			err = con.makeDriverError (nil, 568, fmt.Sprintf ("Missing %v in response from Identity Provider %v", con.m_params.M_sOIDCClaim, sEndpointURL))
			return
		}

		sKey := composeCacheKey (con.m_params.M_sLogMech, sIdProURL, sClientID, sOIDCScope, con.m_params.M_sOIDCToken, con.m_params.M_sOIDCClaim, sKeyClaimValue, sPassword, con.m_params.M_sLogData)

		if con.hasDebugLog () {
			debugLog ("putCacheEntry: sKey=" + sKey)
		}

		entry := newCacheEntry (sEndpointURL, cc)
		asRemovedKeys := [] string {}

		g_mutexTokenCache.Lock () // synchronized (g_mapTokenCache)

		g_mapTokenCache [sKey] = entry

		for len (g_mapTokenCache) > int (con.m_params.M_uOIDCCacheSize) {

			ame := entrySet (g_mapTokenCache)
			iOldest := 0

			for i := 1 ; i < len (ame) ; i++ {
				if ame [i].Value.nAccessTimeMs < ame [iOldest].Value.nAccessTimeMs {
					iOldest = i
				}
			}

			sOldestKey := ame [iOldest].Key
			delete (g_mapTokenCache, sOldestKey)
			asRemovedKeys = append (asRemovedKeys, sOldestKey)
		}

		g_mutexTokenCache.Unlock () // end synchronized (g_mapTokenCache)

		if con.hasDebugLog () {

			for i := 0 ; i < len (asRemovedKeys) ; i++ {
				debugLog ("putCacheEntry: removed key " + itoa (i + 1) + " of " + itoa (len (asRemovedKeys)) + ": " + asRemovedKeys [i])
			}

			debugLog ("putCacheEntry: after put: M_uOIDCCacheSize=" + utoa (con.m_params.M_uOIDCCacheSize) + " len(g_mapTokenCache)=" + itoa (len (g_mapTokenCache)))
		}
	} // end if M_uOIDCCacheSize > 0

	con.completeOIDCFlow (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow, cc)
	return

} // end putCacheEntry

func (con *TeradataConnection) doRefreshFlow (sUserName string, sPassword string, sIdProURL string, sClientID string, sOIDCScope string, bOAuthFlow bool, sRefreshToken string, cc *CacheContext) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter doRefreshFlow %v bOAuthFlow=%v sOIDCScope=%v", con, bOAuthFlow, sOIDCScope))
		defer func () { traceLog (fmt.Sprintf ("< leave doRefreshFlow %v bOAuthFlow=%v sOIDCScope=%v err=%v", con, bOAuthFlow, sOIDCScope, err)) } ()
	}

	resp, err := getOIDCMetadataResponse (con, sIdProURL)
	if err != nil { return }

	sJSON := string (resp.M_abyBody)

	// https://datatracker.ietf.org/doc/html/rfc6749#section-6
	//
	// 6. Refreshing an Access Token
	//
	// If the authorization server issued a refresh token to the client, the
	// client makes a refresh request to the token endpoint by adding the
	// following parameters using the "application/x-www-form-urlencoded"
	// format per Appendix B with a character encoding of UTF-8 in the HTTP
	// request entity-body:
	//
	// grant_type  REQUIRED.  Value MUST be set to "refresh_token".
	//
	// refresh_token  REQUIRED.  The refresh token issued to the client.
	//
	// scope  OPTIONAL.  The scope of the access request as described by Section 3.3.
	//        The requested scope MUST NOT include any scope not originally granted by the resource owner,
	//        and if omitted is treated as equal to the scope originally granted by the resource owner.

	sTokenURL, err := con.getStringFromJSON ("token_endpoint", sJSON, 0, "Failed to parse metadata JSON from Identity Provider")
	if err != nil { return }

	sFormData := "grant_type=refresh_token" +
		"&refresh_token=" + url.QueryEscape (sRefreshToken) +
		"&scope=" + url.QueryEscape (sOIDCScope)

	if con.hasDebugLog () {
		debugLog ("doRefreshFlow: sJSON=" + sJSON)
		debugLog ("doRefreshFlow: sTokenURL=" + sTokenURL)
		debugLog ("doRefreshFlow: sFormData=" + sFormData)
	}

	resp, err = con.doHttpRequest ("POST", sTokenURL, [][] string {{ "Content-Type", "application/x-www-form-urlencoded" }}, [] byte (sFormData),
		/*anRequiredRespCodes*/ [] int {200, 400})
	if err != nil {
		err = con.makeDriverError (err, 567, "Failed to obtain token from Identity Provider")
		return
	}

	sJSON = string (resp.M_abyBody)

	if con.hasDebugLog () {
		debugLog ("doRefreshFlow: Refresh result: resp.M_nCode=" + itoa (resp.M_nCode) + " sJSON=" + sJSON)
	}

	if resp.M_nCode == 200 { // but not 400
		err = con.putCacheEntry (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow, sTokenURL, sJSON, cc)
		if err != nil { return }
	}

	return

} // end doRefreshFlow
