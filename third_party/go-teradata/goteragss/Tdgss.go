// Copyright 2021 by Teradata Corporation. All Rights Reserved.
package goteragss

import (
	"encoding/hex"
	"fmt"
)

const TDGSS_TRAILER_VERSION = 1

const TDGSS_TRAILER_FIXED_LENGTH = 6

type gsspTrailerFixed struct {
	QopOidCount    byte
	TrailerLength  uint32
	TrailerVersion byte
}

/***************************************************************
* Load mechanisms into the global map.
***************************************************************/
func (sec *terassoSecurity) tdgssConfigure(clientServer uint32) (status TdgssStatus) {

	if sec.logger.HasTraceLog() {
		sec.logger.TraceLog(fmt.Sprintf("> enter tdgssConfigure %v", sec))
		defer func() { sec.logger.TraceLog(fmt.Sprintf("< leave tdgssConfigure %v", sec)) }()
	}

	secCtx := sec.secContext
	if secCtx == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_INVALID_CONTEXT
		if sec.logger.HasDebugLog() {
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status = secCtx.gsspConfigure()
	if status.Major != GSS_S_COMPLETE {
		if sec.logger.HasDebugLog() {
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = GSS_S_COMPLETE
	return
}

/***************************************************************
* Given an input token, creates the next (output) token in the DH
* key exchange with the server.
* The client will call tdgssInitSecContext multiple times
* until the key exchange is completed.
* Returns GSS_S_COMPLETE or GSS_S_CONTINUE_NEEDED in majorStatus
***************************************************************/
func (sec *terassoSecurity) tdgssInitSecContext(in []byte) (status TdgssStatus, out []byte) {

	if sec.logger.HasTraceLog() {
		sec.logger.TraceLog(fmt.Sprintf("> enter tdgssInitSecContext %v", sec))
		defer func() { sec.logger.TraceLog(fmt.Sprintf("< leave tdgssInitSecContext %v", sec)) }()
	}

	needTrailer := false

	secCtx := sec.secContext
	if secCtx == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_INVALID_CONTEXT
		if sec.logger.HasDebugLog() {
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status, sec.actualServices, sec.actualCtxTime, needTrailer, out = secCtx.gsspInitSecContext(sec.targetName, in)

	if status.Major == GSS_S_CONTINUE_NEEDED || status.Major == GSS_S_COMPLETE {
		if needTrailer {
			// Don't overwrite the majorStatus and minorStatus from gsspInitSecContext.
			status2, abyTrailer := secCtx.gsspGetMechInfo().buildTrailer(sec.logger)
			if status2.Major == GSS_S_COMPLETE {
				out = append(out, abyTrailer...)

				if sec.logger.HasDebugLog() {
					sec.logger.DebugLog(fmt.Sprintf("Initial token with trailer:\n%v", hex.Dump(out)))
				}
			} else {
				status = status2
				status.Major = GSS_S_FAILURE
				if sec.logger.HasDebugLog() {
					sec.logger.DebugLog(formatMajorStatusErrOnly(status))
				}
				status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
				return
			}
		}
	} else {
		status.Major = GSS_S_FAILURE
		if sec.logger.HasDebugLog() {
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	return
}

/***************************************************************
* Encrypts the in buffer and returns the encrypted
* data in the out buffer using the qopReq.
***************************************************************/
func (sec *terassoSecurity) tdgssWrap(confReqFlag int, qopReq uint32, in []byte) (status TdgssStatus, confState int, out []byte) {

	if sec.logger.HasTraceLog() {
		sec.logger.TraceLog(fmt.Sprintf("> enter tdgssWrap %v", sec))
		defer func() { sec.logger.TraceLog(fmt.Sprintf("< leave tdgssWrap %v", sec)) }()
	}

	secCtx := sec.secContext
	if secCtx == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_INVALID_CONTEXT
		if sec.logger.HasDebugLog() {
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status, confState, out = secCtx.gsspWrap(confReqFlag, qopReq, in)

	return
}

/***************************************************************
* Decrypts the in buffer and returns the decrypted
* data in the out buffer.
***************************************************************/
func (sec *terassoSecurity) tdgssUnwrap(in []byte) (status TdgssStatus, confState int, qopState uint32, out []byte) {

	if sec.logger.HasTraceLog() {
		sec.logger.TraceLog(fmt.Sprintf("> enter tdgssUnwrap %v", sec))
		defer func() { sec.logger.TraceLog(fmt.Sprintf("< leave tdgssUnwrap %v", sec)) }()
	}
	secCtx := sec.secContext
	if secCtx == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_INVALID_CONTEXT
		if sec.logger.HasDebugLog() {
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status, confState, qopState, out = secCtx.gsspUnwrap(in)

	return
}

/***************************************************************
* Acquire security credential for this connection.
***************************************************************/
func (sec *terassoSecurity) tdgssAcquireCred(desiredName string) (status TdgssStatus) {

	if sec.logger.HasTraceLog() {
		sec.logger.TraceLog(fmt.Sprintf("> enter tdgssAcquireCred %v", sec))
		defer func() { sec.logger.TraceLog(fmt.Sprintf("< leave tdgssAcquireCred %v", sec)) }()
	}

	secCtx := sec.secContext
	if secCtx == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_INVALID_CONTEXT
		if sec.logger.HasDebugLog() {
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	status, sec.actualCredTime = secCtx.gsspAcquireCred(sec.userLgnMechName, GSS_C_INITIATE, desiredName)
	if status.Major != GSS_S_COMPLETE {
		if sec.logger.HasDebugLog() {
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
	}
	return
}

/***************************************************************
* Release security credential for this connection.
***************************************************************/
func (sec *terassoSecurity) tdgssRelease() (status TdgssStatus) {

	if sec.logger.HasTraceLog() {
		sec.logger.TraceLog(fmt.Sprintf("> enter tdgssRelease %v", sec))
		defer func() { sec.logger.TraceLog(fmt.Sprintf("< leave tdgssRelease %v", sec)) }()
	}

	secCtx := sec.secContext
	if secCtx == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_INVALID_CONTEXT
		if sec.logger.HasDebugLog() {
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status = secCtx.gsspDeleteContext()
	if status.Major != GSS_S_COMPLETE {
		if sec.logger.HasDebugLog() {
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
	}

	status = secCtx.gsspReleaseCred()
	if status.Major != GSS_S_COMPLETE {
		if sec.logger.HasDebugLog() {
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
	}
	return
}
