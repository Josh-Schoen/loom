// Copyright 2021 by Teradata Corporation. All Rights Reserved.
package goteragss

import (
	"fmt"
	"strings"
)

// Map TDAT Minor Status codes to TD2 Minor Status codes
var jwtMinorStatusMap = map[uint32]uint32{
	TDAT_S_COMPLETE:                   JWT_S_COMPLETE,
	TDAT_S_CONTINUE_NEEDED:            JWT_S_CONTINUE_NEEDED,
	TDAT_ERR_INT_FAILURE:              JWT_ERR_INT_FAILURE,
	TDAT_ERR_ALG_NO_CONTEXT:           JWT_ERR_NO_CONTEXT,
	TDAT_ERR_INVALID_NO_QOPS:          JWT_ERR_INVALID_NO_QOPS,
	TDAT_ERR_INVALID_TOKEN:            JWT_ERR_INVALID_TOKEN,
	TDAT_ERR_CONTEXT_NOT_READY:        JWT_ERR_CONTEXT_NOT_READY,
	TDAT_ERR_INVALID_INPUT:            JWT_ERR_INVALID_INPUT,
	TDAT_ERR_CONF_INTEG_NOT_AVAILABLE: JWT_ERR_CONF_INTEG_NOT_AVAILABLE,
	TDAT_ERR_CIBYPASSFAIL:	           JWT_ERR_CIBYPASSFAIL,
}

func getJwtMinorStatus(tdatMinorStatus uint32) (jwtMinorStatus uint32) {
	jwtMinorStatus = jwtMinorStatusMap[tdatMinorStatus]
	if jwtMinorStatus == 0 {
		jwtMinorStatus = tdatMinorStatus
	}
	return
}

func jwtParseToken(logger TdgssLogger, accessToken string) (status TdgssStatus, token string) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter jwtParseToken"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave jwtParseToken")) }()
	}

	status.Major = GSS_S_COMPLETE

	// Find token= identifier
	tokenMarkerIndex := strings.Index(accessToken, "token=")
	if tokenMarkerIndex == -1 {
		if logger.HasDebugLog() {
			logger.DebugLog("jwtParseToken: token missing from content")
		}
		status.Major = GSS_S_FAILURE
		status.Minor = TDAT_LDAP_ERR_UNKNOWN_PROPERTY
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Cut off left of token=
	newTokenString := accessToken[tokenMarkerIndex:]

	// Check if there is a duplicate token
	dupeTokenMarkerIndex := strings.Index(newTokenString[6:], "token=")
	if dupeTokenMarkerIndex != -1 {
		if logger.HasDebugLog() {
			logger.DebugLog("jwtParseToken: multiple tokens found")
		}
		status.Major = GSS_S_FAILURE
		status.Minor = TDAT_LDAP_ERR_DUPLICATE_TOKEN
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Find end of token body
	newTokenStringEndIndex := strings.Index(newTokenString, " ")
	if newTokenStringEndIndex != -1 {
		// Slice off the extra content after the token
		newTokenString = newTokenString[:newTokenStringEndIndex]
	}

	// Slice off the token= portion of string
	token = newTokenString[6:]

	return
}

func jwtIntClientReturns(minorStatus uint32, bCIBypass bool) (retFlags uint32, timeRec uint32) {
	
	timeRec = GSS_C_INDEFINITE

	retFlags = 0
	if minorStatus == JWT_S_CONTINUE_NEEDED {
		retFlags = func () uint32 { if bCIBypass { return TDGSS_C_CI_BYPASS_FLAG } ; return GSS_C_INTEG_FLAG | GSS_C_CONF_FLAG } ()
	} else if minorStatus == JWT_S_COMPLETE {
		retFlags = func () uint32 { if bCIBypass { return TDGSS_C_CI_BYPASS_FLAG } ; return GSS_C_INTEG_FLAG | GSS_C_CONF_FLAG | GSS_C_PROT_READY_FLAG } ()
	}
	return
}
