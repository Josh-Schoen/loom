// Copyright 2022 by Teradata Corporation. All Rights Reserved.

//go:build windows

package goteragss

import (
	"encoding/binary"
	"fmt"
	"strings"
	"syscall"
)

const (
	SSPI_CTX_NEW = iota
	SSPI_CTX_SETUP_PARTIAL
	SSPI_CTX_SETUP_COMPLETE
	SSPI_CTX_SETUP_COMPLETE_AND_CONTINUE_FOR_CLIENT_LOGDATA
)

const GSS_C_QOP_DEFAULT = 0

const CAP_LOGDATA = 0x2

type LUID struct {
	LowPart  uint32
	HighPart int32
}

type sspiCredHandle struct {
	Lower uintptr
	Upper uintptr
}

type sspiCtxtHandle struct {
	Lower uintptr
	Upper uintptr
}

type sspiSecContext struct {
	mechInfo             *mechanismInfo
	logger               TdgssLogger
	version              byte
	hostType             byte      /* GSSSTYLE or SSPISTYLE */
	hostMachineType      byte
	peerType             byte      /* GSSSTYLE or SSPISTYLE */
	peerMachineType      byte
	clientOrServer       byte
	peerCapabilities     uint32
	peerLibraryType      byte
	fDelegation          uint32
	fMechanismIgnoresQop uint32
	completionStatus     uint8
	userProperties       []byte
	extraLogdata         string
	sspiCtx              sspiCtxtHandle
	sspiCred             sspiCredHandle
	expiry               syscall.Filetime
}

var _ gssp = &sspiSecContext{} // implements gssp

func newKrb5Mech(loggerArg TdgssLogger, mi *mechanismInfo) gssp {

	return &sspiSecContext{
		logger:           loggerArg,
		mechInfo:         mi,
		completionStatus: SSPI_CTX_NEW,
	}
}

// String interface
func (secCtx *sspiSecContext) String() string { return fmt.Sprintf("sspiSecContext: %p", secCtx) }

// gssp interface
func (secCtx *sspiSecContext) gsspConfigure() (status TdgssStatus) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspConfigure %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspConfigure %v", secCtx)) }()
	}

	status.Major = GSS_S_COMPLETE
	return
}

func (secCtx *sspiSecContext) gsspInitSecContext(targetName string, in []byte) (status TdgssStatus, retFlags uint32, timeRec uint32, needTrailer bool, out []byte) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspInitSecContext %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspInitSecContext %v", secCtx)) }()
	}

	if secCtx.logger.HasDebugLog() {
		secCtx.logger.DebugLog(fmt.Sprintf("Target Name in gsspInitSecContext %s", targetName))
	}

	if secCtx.completionStatus == SSPI_CTX_SETUP_COMPLETE  && len(in) == 0 {
		status.Major = GSS_S_COMPLETE
		status.Minor = SSPI_S_COMPLETE
		out = nil
		return
	}

	var capabilities uint32 = 0
	newConversation := false
	haveLogdata := false
	if secCtx.completionStatus == SSPI_CTX_NEW {
		// Initially, the input token should have length zero. 
		if len(in) != 0 {
			status.Major = GSS_S_DEFECTIVE_TOKEN
			status.Minor = SSPI_ERR_INVALID_TOKEN
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		// Set if logdata is available
		if len(secCtx.extraLogdata) > 0 {
			haveLogdata = true;
		}

		// Set logdata capability
		capabilities |= CAP_LOGDATA
		newConversation = true
		needTrailer = true
	}

	var peerTokenHdr sspiV2TokenHeader
	var sspiCtx *sspiCtxtHandle = nil
	var inputToken []byte = nil
	if !newConversation {
		// Check the input buffer if it is not the first call and not complete.
		if len(in) == 0 {
			status.Major = GSS_S_DEFECTIVE_TOKEN
			status.Minor = SSPI_ERR_INVALID_TOKEN
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		// The input buffer contains a token header follow by the token.
		// Retrieve the token header.
		status, peerTokenHdr = sspiIntRetrieveHeader(in)
		if status.Major != GSS_S_COMPLETE {
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		tokenLength := peerTokenHdr.MsgLength
		if len(in) < int(SSPI_TOKEN_HEADER_LENGTH + tokenLength) {
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}
	
		inputToken = in[SSPI_TOKEN_HEADER_LENGTH:SSPI_TOKEN_HEADER_LENGTH + tokenLength]

		// Save the Peer Capabiilties
		if peerTokenHdr.ByteVar == INITMSG {
			secCtx.peerCapabilities = peerTokenHdr.Capabilities

			/* Check if peer has logdata capabilities */
			if haveLogdata && (secCtx.peerCapabilities & CAP_LOGDATA) != CAP_LOGDATA {
				status.Major = GSS_S_FAILURE
				status.Minor = SSPI_ERR_NO_LOGDATA_CAPABILITIES
				status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
				return
			}
		}

		sspiCtx = &secCtx.sspiCtx
	}

	// Get the maximum length of the output token
	tmpStatus, packageInfo := sspiIntQuerySecurityPackageInfo(secCtx.logger, KERBEROS_NAME)
	if tmpStatus.Major != GSS_S_COMPLETE {
		status = tmpStatus
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	// GSS_C_MUTUAL_FLAG needs to be added if delegation is requested
	// because Microsoft requires mutual authentication to have delegaiton work.
	reqFlags := secCtx.mechInfo.desiredServices
	if (reqFlags & GSS_C_DELEG_FLAG) != 0 {
		reqFlags |= GSS_C_MUTUAL_FLAG
	}

	// Set the two flags GSS_C_CONF_FLAG and GSS_C_INTEG_FLAG
	// whenever one of them is requested by user.
	if (reqFlags & GSS_C_CONF_FLAG) != 0 && (reqFlags & GSS_C_INTEG_FLAG) == 0 {
		reqFlags |= GSS_C_INTEG_FLAG
	} else if (reqFlags & GSS_C_INTEG_FLAG) != 0 && (reqFlags & GSS_C_CONF_FLAG) == 0 {
		reqFlags |= GSS_C_CONF_FLAG
	}

	// Enable mutual authentication as client has logdata capabilities 
	reqFlags |= GSS_C_MUTUAL_FLAG

	// Convert flags to SSPI flags, req_or_ret == 0 (req), gssp_to_sspi = 1 (yes).
	contextreq := SSPIIntConvertFlags(reqFlags)

	var sspiNewCtx *sspiCtxtHandle = &secCtx.sspiCtx

	initStatus, abyOutputToken, _, ret := sspiIntInitializeSecurityContext(
		secCtx.logger,
		&secCtx.sspiCred,
		sspiCtx,
		targetName,
		contextreq,
		inputToken,
		packageInfo.MaxToken,
		sspiNewCtx,
		&secCtx.expiry,
	)

	status = initStatus
	if status.Major != GSS_S_COMPLETE && status.Major != GSS_S_CONTINUE_NEEDED {
		if newConversation {
			sspIntDeleteSecurityContext(secCtx.logger, sspiNewCtx)
		}

		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext: sspiIntInitializeSecurityContext failed %+v %d", ret, ret))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}


	sspiIntBuildSecCtx(newConversation, reqFlags & GSS_C_DELEG_FLAG, secCtx, peerTokenHdr)

	var msgSeqType byte
	if newConversation {
		msgSeqType = INITMSG
	} else {
		msgSeqType = CONTMSG
	}

	hdrStatus, abyTokenHdr := sspiIntBuildTokenHdr(
			secCtx.logger,
			INITCTXMSG,
			msgSeqType,
			NOTAPPLICABLE,
			secCtx.fDelegation,
			uint32(len(abyOutputToken)),
			capabilities)

	if hdrStatus.Major != GSS_S_COMPLETE {
		status = hdrStatus
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	if ret == SEC_E_OK {
		if (secCtx.peerCapabilities & CAP_LOGDATA) == CAP_LOGDATA {

			if !haveLogdata {
				/* Zero length logdata */

				/* DR195030-en185000-01 --> */
				/* This is a workaround to address DR195030, which causes
				   a KRB5/SSO logon failure on Windows when using SSPI and
				   the server side is using KRB5 with authorization and
				   MIT KRB5 1.8+ installed.
				   When the logdata value is a zero-length string and
				   is encrypted here on the client side, then an error
				   occurs on the server side with MIT KRB5 1.8 or later.
				   Note that MIT KRB5 1.6 on the server side has no problem
				   with decrypting a zero-length string.
				   This workaround substitutes a logdata value of a single space
				   instead of a zero-length string in order to avoid the failure
				   with MIT KRB5 1.8+ on the server side.
				*/
				secCtx.extraLogdata = " "
				/* <-- DR195030-en185000-01 */
			}

			// Encrypt the logdata and send to server
			wrapStatus, _, abyEncryptedLogdata := secCtx.gsspWrap(1, 0, []byte(secCtx.extraLogdata))
			
			if wrapStatus.Major != GSS_S_COMPLETE {
				status = wrapStatus
				status.Err = tdgssError(wrapStatus.Err, formatTdgssStatusErrOnly(status))
				return
			}

			/* NTLM - Piggy back the logdata */
			if len(abyOutputToken) > 0 {
				encryptedLogdataLen := len(abyEncryptedLogdata)

				abyEncryptedLogdataLen := make([]byte, 4)
				binary.BigEndian.PutUint32(abyEncryptedLogdataLen, uint32(encryptedLogdataLen))

				abyNewOutputToken := concat(abyEncryptedLogdataLen, abyEncryptedLogdata, abyOutputToken)
			
				hdrStatus, abyTokenHdr = sspiIntBuildTokenHdr(
					secCtx.logger,
					INITCTXMSG,
					LOGDATAMSG,
					NOTAPPLICABLE,
					secCtx.fDelegation,
					uint32(len(abyNewOutputToken)),
					capabilities)

					if hdrStatus.Major != GSS_S_COMPLETE {
						status = hdrStatus
						status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
						return
					}

				out = concat(abyTokenHdr, abyNewOutputToken)

			} else {

				hdrStatus, abyTokenHdr = sspiIntBuildTokenHdr(
					secCtx.logger,
					INITCTXMSG,
					LOGDATAMSG,
					NOTAPPLICABLE,
					secCtx.fDelegation,
					uint32(len(abyEncryptedLogdata)),
					capabilities)

					if hdrStatus.Major != GSS_S_COMPLETE {
						status = hdrStatus
						status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
						return
					}

				out = concat(abyTokenHdr, abyEncryptedLogdata)

			}

			secCtx.completionStatus = SSPI_CTX_SETUP_COMPLETE
			status.Major = GSS_S_COMPLETE
			status.Minor = SSPI_S_COMPLETE
			return
		}
	}

	if len(abyOutputToken) > 0 {
		out = concat(abyTokenHdr, abyOutputToken)
	}

	if status.Major == GSS_S_COMPLETE {
		secCtx.completionStatus = SSPI_CTX_SETUP_COMPLETE
	}

	return
}

func (secCtx *sspiSecContext) gsspWrap(confReqFlag int, qopReq uint32, in []byte) (status TdgssStatus, confState int, out []byte) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspWrap %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspWrap %v", secCtx)) }()
	}

	if secCtx.sspiCtx.Lower == 0 && secCtx.sspiCtx.Upper == 0 {
		status.Major = GSS_S_NO_CONTEXT | GSS_S_CALL_INACCESSIBLE_READ
		status.Minor = SSPI_ERR_CANT_WRAP
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Make sure MechanismIgnoresQop property is set to TRUE
	if !secCtx.mechInfo.mechProperties.MechanismIgnoresQop {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_BAD_MECHANISMIGNORESQOP
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	/* Ignore QoP */
	qopReq = GSS_C_QOP_DEFAULT

	/* make sure we have an input buffer. */
	if in == nil {
		status.Major = GSS_S_FAILURE | GSS_S_CALL_INACCESSIBLE_READ
		status.Minor = SSPI_ERR_CALL_INACCESSIBLE_READ
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	var qop uint32 = 0
	if confReqFlag == 0 {
		qop = SECQOP_WRAP_NO_ENCRYPT
	}

	/* The output token lenth consists of the following three parts(in order):
	 *  -- Security trailer buffer
	 *  -- Data buffer that contains the input message.
	 *  -- Padding buffer
	 *  -- 4 bytes for holding the length of the Security Trailer
	 *  -- 4 bytes for holding the length of the Padding
	 */

	tmpStatus, tmpOutput := sspiIntEncryptMessage(secCtx.logger, &secCtx.sspiCtx, in, qop, 0 /*seqno*/)
	if tmpStatus.Major != GSS_S_COMPLETE {
		status = tmpStatus
		status.Err = tdgssError(tmpStatus.Err, formatTdgssStatusErrOnly(status))
		return
	}

	// Build the token header
	tokenLength := len(tmpOutput)
	tmpStatus, abyTokenHdr := sspiIntBuildTokenHdr(secCtx.logger, ENCRYPTMSG, NOTAPPLICABLE, confReqFlag, secCtx.fDelegation, uint32(tokenLength), 0)
	if tmpStatus.Major != GSS_S_COMPLETE {
		status = tmpStatus
		status.Err = tdgssError(tmpStatus.Err, formatTdgssStatusErrOnly(status))
		return
	}
	
	confState = confReqFlag
	out = concat(abyTokenHdr, tmpOutput)
	status = tmpStatus
	return
}

func (secCtx *sspiSecContext) gsspUnwrap(in []byte) (status TdgssStatus, confState int, qopState uint32, out []byte) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspUnwrap %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspUnwrap %v", secCtx)) }()
	}

	if secCtx.sspiCtx.Lower == 0 && secCtx.sspiCtx.Upper == 0 {
		status.Major = GSS_S_NO_CONTEXT | GSS_S_CALL_INACCESSIBLE_READ
		status.Minor = SSPI_ERR_CANT_UNWRAP
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if in == nil {
		status.Major = GSS_S_FAILURE | GSS_S_CALL_INACCESSIBLE_READ
		status.Minor = SSPI_ERR_CANT_UNWRAP
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	/* Decode the input message buffer.
	 * The wrapped message coming from GSS, it is decoded as:
	 * -- Token header - 16 bytes (size of token header)
	 * -- Data buffer being passed to SSPI as a stream buffer (SECBUFFER_STREAM)
	 *       TokenLength = len(in) - 16 (size of token header) - 8 (trailing zero bytes)
	 *    trailing 8 bytes
	 */

	var peerTokenHdr sspiV2TokenHeader
	status, peerTokenHdr = sspiIntRetrieveHeader(in)
	if status.Major != GSS_S_COMPLETE {
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	
	tokenLength := peerTokenHdr.MsgLength - TRAILING_BYTES_LENGTH /* strip off the 8 trailing bytes */

	if len(in) < int(SSPI_TOKEN_HEADER_LENGTH + tokenLength) {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	encryptedMsg := in[SSPI_TOKEN_HEADER_LENGTH:SSPI_TOKEN_HEADER_LENGTH + tokenLength]

	var qop uint32
	status, qop, out = sspiIntDecryptMessage(secCtx.logger, &secCtx.sspiCtx, encryptedMsg, 0 /*seqno */)
	if status.Major != GSS_S_COMPLETE {
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	if qop == SECQOP_WRAP_NO_ENCRYPT {
		confState = 0
	} else {
		confState = 1
	}

	qopState = 0
	return
}

func (secCtx *sspiSecContext) gsspAcquireCred(userLgnMechName string, credUsage int, desiredName string) (status TdgssStatus, timeRec uint32) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspAcquireCred %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspAcquireCred %v", secCtx)) }()
	}

	/* KRB5 does not support authcid style logon properties in logdata so if any one of the
	*  properties exist in logdata, then return an unknown property message.
	*/
	if strings.Contains(desiredName, "authcid=") || strings.Contains(desiredName, "password=") {
		status.Major = GSS_S_FAILURE
		status.Minor = TDAT_LDAP_ERR_UNKNOWN_PROPERTY
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status, secCtx.extraLogdata = sspIntAcquireCredentialsHandle(secCtx.logger, desiredName, credUsage, &secCtx.sspiCred, &secCtx.expiry)
	if status.Major != GSS_S_COMPLETE {
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("gsspAcquireCred: sspIntAcquireCredentialsHandle failed"))
		}
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_CANT_INQUIRE_CRED
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = GSS_S_COMPLETE
	return
}

func (secCtx *sspiSecContext) gsspReleaseCred() (status TdgssStatus) {
	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspReleaseCred %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspReleaseCred %v", secCtx)) }()
	}

	status = sspIntFreeCredentialsHandle(secCtx.logger, &secCtx.sspiCred)
	if status.Major != GSS_S_COMPLETE {
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("gsspAcquireCred: sspIntFreeCredentialsHandle failed."))
		}
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_CANT_INQUIRE_CRED
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = GSS_S_COMPLETE
	return
}

func (secCtx *sspiSecContext) gsspDeleteContext() (status TdgssStatus) {
	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspDeleteContext %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspDeleteContext %v", secCtx)) }()
	}

	status = sspIntDeleteSecurityContext(secCtx.logger, &secCtx.sspiCtx)
	if status.Major != GSS_S_COMPLETE {
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("gsspDeleteContext: sspIntDeleteSecurityContext failed."))
		}
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = GSS_S_COMPLETE
	return
}

func (secCtx *sspiSecContext) gsspGetMechInfo() (mechInfo *mechanismInfo) {
	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspGetMechInfo %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspGetMechInfo %v", secCtx)) }()
	}
	return secCtx.mechInfo
}

func (secCtx *sspiSecContext) gsspIsEstablished() bool {
	return secCtx.completionStatus == SSPI_CTX_SETUP_COMPLETE
}
