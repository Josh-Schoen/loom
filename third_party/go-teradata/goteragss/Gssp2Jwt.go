package goteragss

import (
	"encoding/hex"
	"fmt"
)

const (
	JWT_CTX_NEW = iota
	JWT_CTX_SEND_KEYS
	JWT_CTX_COMPUTE_KEY
	JWT_CTX_SETUP_COMPLETE
)

type jwtSecContext struct {
	mechInfo         *mechanismInfo
	logger           TdgssLogger
	cI               confInteg
	completionStatus uint8
	peerCapabilities uint32
	userProperties   []byte
	jwtCred          tdatCred
	bCIBypass        bool
}

var _ gssp = &jwtSecContext{}

func newJwtMech(loggerArg TdgssLogger, mi *mechanismInfo) gssp {

	return &jwtSecContext{
		logger:           loggerArg,
		mechInfo:         mi,
		completionStatus: JWT_CTX_NEW,
		cI: confInteg{
			algDH:    algDHCtx{},
			conf:     mi.mechProperties.ConfidentialityDesired,
			integ:    mi.mechProperties.IntegrityDesired,
			replay:   mi.mechProperties.ReplayDetection,
			sequence: mi.mechProperties.OutOfSequenceDetection,
		},
	}
}

func (secCtx *jwtSecContext) String() string { return fmt.Sprintf("jwtSecContext:%p", secCtx) }

// gssp interface
func (secCtx *jwtSecContext) gsspConfigure() (status TdgssStatus) {
	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspConfigure %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspConfigure %v", secCtx)) }()
	}

	status.Major = GSS_S_COMPLETE
	return
}

func (secCtx *jwtSecContext) gsspInitSecContext(targetName string, in []byte) (status TdgssStatus, retFlags uint32, timeRec uint32, needTrailer bool, out []byte) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspInitSecContext %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspInitSecContext %v", secCtx)) }()
	}

	// If the TDGSS_C_CI_BYPASS_FLAG is set in the desiredServices, immediately transfer
	// control to gsspInitSecContextTls to bypass TDGSS's DH/CI duties.

	if (secCtx.mechInfo.desiredServices & TDGSS_C_CI_BYPASS_FLAG) != 0 {
		return secCtx.gsspInitSecContextTls(targetName, in)
	}

	status.Major = GSS_S_FAILURE

	/* JWT Token Should not be NULL */
	if len(secCtx.jwtCred.principalName) == 0 {
		status.Major = GSS_S_NO_CRED
		status.Minor = JWT_ERR_NO_CRED
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	switch secCtx.completionStatus {
	case JWT_CTX_NEW:

		/* Get the Cipher suites parcel */
		_, cipherSuites := gsspGetCipherSuites()

		/* JWT token header */
		jwtTokenHdr := jwtTokenHeader{
			version:        VERSION1,
			msgType:        INITCTXMSG,
			clientOrServer: CLIENT,
			capabilities:   UNIQUEIVKEYEDMIC | ENHANCEDQOP | ASN1WRAPTOKEN,
			requestFlags:   byte(secCtx.mechInfo.desiredServices),
		}

		// Encode token header and cipher suites
		status, out = jwtEncodeClientInfo(jwtTokenHdr, cipherSuites)

		if status.Major != GSS_S_COMPLETE {
			status.Minor = TDGSS_ERR_CANT_INIT_SEC_CONTEXT
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		secCtx.completionStatus = JWT_CTX_SEND_KEYS

		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("JWT gsspInitSecContext Initial token:\n%v", hex.Dump(out)))
		}

		/* Set output parameters for returns */
		retFlags, timeRec = jwtIntClientReturns(JWT_S_CONTINUE_NEEDED, secCtx.bCIBypass)

		status.Major = GSS_S_CONTINUE_NEEDED
		status.Minor = JWT_S_CONTINUE_NEEDED
		needTrailer = true
		return

	case JWT_CTX_SEND_KEYS:

		// Validate input token
		if len(in) == 0 {
			status.Major = GSS_S_DEFECTIVE_TOKEN
			status.Minor = JWT_ERR_INVALID_TOKEN
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("JWT gsspInitSecContext Bad input data!"))
			}
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		// Decode input_token get ServerInfoToken
		var jwtServerTokenHdr jwtTokenHeader
		var pValue, gValue, peerPublicKey, qOPs []byte
		status, jwtServerTokenHdr, pValue, gValue, peerPublicKey, qOPs = jwtDecodeServerInfo(in)
		secCtx.peerCapabilities = jwtServerTokenHdr.capabilities
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext peerCapabilities %x", secCtx.peerCapabilities))
		}


		if status.Major != GSS_S_COMPLETE {
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("JWT gsspInitSecContext Unable to retrieve P, G, Server public key, and qops!"))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("JWT gsspInitSecContext qOPS:\n%v", hex.Dump(qOPs)))
		}

		status, secCtx.cI.algQopEx = gsspGetSelectedQops(qOPs)
		if status.Major != GSS_S_COMPLETE {
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("JWT gsspInitSecContext Unable to convert QOPs from ASN.1!"))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		var abyPublicKey []byte
		status, abyPublicKey = tdatComputeKeys(secCtx.logger, &secCtx.cI.algDH, pValue, gValue, peerPublicKey)
		if status.Major != GSS_S_COMPLETE {
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("JWT gsspInitSecContext Unable to compute keys!"))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		status = tdatIntSetAlgCtx(secCtx.logger, &secCtx.cI.algDH, secCtx.cI.algQopEx, secCtx.peerCapabilities)
		if status.Major != GSS_S_COMPLETE {
			status.Minor = getTd2MinorStatus(status.Minor)
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("JWT gsspInitSecContext tdatIntSetAlgCtx failed!"))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		/* Initialize mySeqNum and peerSeqNum  */
		secCtx.cI.mySeqNum = 1
		secCtx.cI.peerSeqNum = 1

		// By this time we should be able to call wrap and unwrap
		secCtx.cI.protReady = true

		// Encode JWT
		var abyTemp []byte
		status, abyTemp = jwtEncodeJWT([]byte(secCtx.jwtCred.principalName))
		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		// Encrypt JWT
		var abyWrappedJWT []byte
		status, _, abyWrappedJWT = secCtx.gsspWrap(1, 0, abyTemp)
		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		// JWT token header
		jwtTokenHdr := jwtTokenHeader {
			version: VERSION3,
			msgType: INITCTXMSG,
			clientOrServer: CLIENT,
			capabilities: UNIQUEIVKEYEDMIC,
			requestFlags: byte(secCtx.mechInfo.desiredServices),
		}

		// Encode EncryptedJwtToken
		status, out = jwtEncodeEncryptedJWT(jwtTokenHdr, abyPublicKey, abyWrappedJWT)
		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		secCtx.completionStatus = JWT_CTX_SETUP_COMPLETE
		status.Major = GSS_S_COMPLETE
		status.Minor = JWT_S_COMPLETE
		retFlags, timeRec = jwtIntClientReturns(status.Minor, secCtx.bCIBypass)
		return

	case JWT_CTX_SETUP_COMPLETE:
		status.Major = GSS_S_COMPLETE
		status.Minor = JWT_S_COMPLETE
		retFlags, timeRec = jwtIntClientReturns(status.Minor, secCtx.bCIBypass)
		out = nil
		return

	default:
		status.Major = GSS_S_FAILURE
		status.Minor = JWT_ERR_INT_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	return
}

/***************************************************************
* This function is invoked by gsspInitSecContext only when DH/CI Bypass
* has been initiated by the client (and thus TLS is active for the connection).
* The Invocation is triggered when TDGSS_C_CI_BYPASS_FLAG is set in
* secCtx.mechInfo.desiredServices. Note that this function is never called directly.
*
* The behavior of this parallel function mirrors that of gsspInitSecContext,
* but all Diffie-Hellman and Confidentiality-Integrity processing (such as
* establishing DH keys, and handling cipher suites) has been removed, as TLS handles
* all cryptographic duties.
***************************************************************/
func (secCtx *jwtSecContext) gsspInitSecContextTls(targetName string, in []byte) (status TdgssStatus, retFlags uint32, timeRec uint32, needTrailer bool, out []byte) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter JWT gsspInitSecContextTls %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave JWT gsspInitSecContextTls %v", secCtx)) }()
	}

	status.Major = GSS_S_FAILURE

	/* JWT Token Should not be NULL */
	if len(secCtx.jwtCred.principalName) == 0 {
		status.Major = GSS_S_NO_CRED
		status.Minor = JWT_ERR_NO_CRED
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	switch secCtx.completionStatus {
	case JWT_CTX_NEW:

		/* JWT token header */
		jwtTokenHdr := jwtTokenHeader{
			version:         VERSION1,
			msgType:         INITCTXMSG,
			clientOrServer:  CLIENT,
			capabilities:    NOCAPABILITIES,
			requestFlags:    byte(secCtx.mechInfo.desiredServices),
			newCapabilities: DHCIBYPASS,
		}

		// Encode token header
		status, out = jwtEncodeClientInfoTls(jwtTokenHdr, []byte(secCtx.jwtCred.principalName))

		if status.Major != GSS_S_COMPLETE {
			status.Minor = TDGSS_ERR_CANT_INIT_SEC_CONTEXT
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		secCtx.completionStatus = JWT_CTX_SEND_KEYS
		secCtx.bCIBypass = true

		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("JWT gsspInitSecContext Initial token:\n%v", hex.Dump(out)))
		}

		/* Set output parameters for returns */
		retFlags, timeRec = jwtIntClientReturns(JWT_S_CONTINUE_NEEDED, secCtx.bCIBypass)

		status.Major = GSS_S_CONTINUE_NEEDED
		status.Minor = JWT_S_CONTINUE_NEEDED
		needTrailer = true
		return

	case JWT_CTX_SEND_KEYS:

		// Validate input token
		if len(in) == 0 {
			status.Major = GSS_S_DEFECTIVE_TOKEN
			status.Minor = JWT_ERR_INVALID_TOKEN
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("JWT gsspInitSecContext Bad input data!"))
			}
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		// Decode input_token get jwtServerTokenHdr
		var jwtServerTokenHdr jwtTokenHeader
		status, jwtServerTokenHdr = jwtDecodeServerInfoTls (in)
		if status.Major != GSS_S_COMPLETE {
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("JWT gsspInitSecContextTls Unable to retrieve server token header!"))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		// Verify that the bypass was successful
		if jwtServerTokenHdr.newCapabilities & DHCIBYPASS == 0 {
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("JWT gsspInitSecContextTls Server does not support DHCIBYPASS!"))
			}
			status.Major = GSS_S_FAILURE
			status.Minor = JWT_ERR_CIBYPASSFAIL
			return
		}

		secCtx.peerCapabilities = jwtServerTokenHdr.capabilities
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext peerCapabilities %x", secCtx.peerCapabilities))
		}

		/* Initialize mySeqNum and peerSeqNum  */
		secCtx.cI.mySeqNum = 1
		secCtx.cI.peerSeqNum = 1

		secCtx.cI.protReady = true

		secCtx.completionStatus = JWT_CTX_SETUP_COMPLETE
		status.Major = GSS_S_COMPLETE
		status.Minor = JWT_S_COMPLETE
		retFlags, timeRec = jwtIntClientReturns(status.Minor, secCtx.bCIBypass)
		out = nil
		return

	case JWT_CTX_SETUP_COMPLETE:
		status.Major = GSS_S_COMPLETE
		status.Minor = JWT_S_COMPLETE
		retFlags, timeRec = jwtIntClientReturns(status.Minor, secCtx.bCIBypass)
		out = nil
		return

	default:
		status.Major = GSS_S_FAILURE
		status.Minor = JWT_ERR_INT_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	return
}

func (secCtx *jwtSecContext) gsspWrap(confReqFlag int, qopReq uint32, in []byte) (status TdgssStatus, confState int, out []byte) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspWrap JWT %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspWrap JWT %v", secCtx)) }()
	}

    /* Shouldn't call gsspWrap if TDGSS_C_CI_BYPASS_FLAG is enabled */
	if secCtx.bCIBypass {
		status.Major = GSS_S_FAILURE
		status.Minor = JWT_ERR_CONF_INTEG_NOT_AVAILABLE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status, confState, out = tdatWrap(secCtx.logger, &secCtx.cI, secCtx.peerCapabilities, confReqFlag, qopReq, in)
	status.Minor = getJwtMinorStatus(status.Minor)
	if status.Major != GSS_S_COMPLETE {
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("JWT gsspWrap failed"))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
	}

	return
}

func (secCtx *jwtSecContext) gsspUnwrap(in []byte) (status TdgssStatus, confState int, qopState uint32, out []byte) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspUnwrap JWT %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspUnwrap JWT %v", secCtx)) }()
	}

    /* Shouldn't call gsspUnwrap if TDGSS_C_CI_BYPASS_FLAG is enabled */
	if secCtx.bCIBypass {
		status.Major = GSS_S_FAILURE
		status.Minor = JWT_ERR_CONF_INTEG_NOT_AVAILABLE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status, confState, qopState, out = tdatUnwrap(secCtx.logger, &secCtx.cI, secCtx.peerCapabilities, in)
	status.Minor = getJwtMinorStatus(status.Minor)
	if status.Major != GSS_S_COMPLETE {
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("JWT gsspUnwrap failed"))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
	}

	return
}

func (secCtx *jwtSecContext) gsspAcquireCred(userLgnMechName string, credUsage int, desiredName string) (status TdgssStatus, timeRec uint32) {
	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspAcquireCred %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspAcquireCred %v", secCtx)) }()
	}

	status.Major = GSS_S_COMPLETE
	status.Minor = GSS_S_COMPLETE

	if credUsage != GSS_C_BOTH && credUsage != GSS_C_INITIATE && credUsage != GSS_C_ACCEPT {
		status.Major = GSS_S_FAILURE
		status.Minor = JWT_ERR_INVALID_INPUT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	secCtx.jwtCred.credUsage = credUsage
	secCtx.jwtCred.lifeTime = GSS_C_INDEFINITE
	timeRec = GSS_C_INDEFINITE

	if credUsage == GSS_C_ACCEPT {
		return
	}

	if desiredName == GSS_C_NO_NAME {
		status.Major = GSS_S_FAILURE
		status.Minor = JWT_ERR_NO_JWT_TOKEN
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Parse Token
	var token_string string
	status, token_string = jwtParseToken(secCtx.logger, desiredName)
	secCtx.jwtCred.principalName = token_string
	if secCtx.logger.HasDebugLog() {
		secCtx.logger.DebugLog(fmt.Sprintf("principleName[%s]", secCtx.jwtCred.principalName))
	}

	return
}

func (secCtx *jwtSecContext) gsspReleaseCred() (status TdgssStatus) {
	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspReleaseCred %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspReleaseCred %v", secCtx)) }()
	}
	status.Major = GSS_S_COMPLETE
	return
}

func (secCtx *jwtSecContext) gsspDeleteContext() (status TdgssStatus) {
	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspDeleteContext %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspDeleteContext %v", secCtx)) }()
	}
	status.Major = GSS_S_COMPLETE
	return
}

func (secCtx *jwtSecContext) gsspGetMechInfo() (mechInfo *mechanismInfo) {
	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspGetMechInfo JWT %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspGetMechInfo JWT %v", secCtx)) }()
	}
	mechInfo = secCtx.mechInfo
	return
}

func (secCtx *jwtSecContext) gsspIsEstablished() bool {
	return secCtx.completionStatus == JWT_CTX_SETUP_COMPLETE
}
