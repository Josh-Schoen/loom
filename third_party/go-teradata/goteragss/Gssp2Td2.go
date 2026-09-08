// Copyright 2021 by Teradata Corporation. All Rights Reserved.
package goteragss

import (
	"encoding/hex"
	"fmt"
)

const (
	TD2_CTX_NEW = iota
	TD2_CTX_SEND_KEYS
	TD2_CTX_COMPUTE_KEY
	TD2_CTX_VERIFY_KEY
	TD2_CTX_SETUP_COMPLETE
)

const SECRETKEY_MATCHED = 0

type td2SecContext struct {
	mechInfo         *mechanismInfo
	logger           TdgssLogger
	cI               confInteg /* See TdatUtil.go */
	completionStatus uint8
	peerCapabilities uint32
	userProperties   []byte
	td2Cred          tdatCred
	bCIBypass        bool
}

var _ gssp = &td2SecContext{} // implements gssp

func newTd2Mech(loggerArg TdgssLogger, mi *mechanismInfo) gssp {

	return &td2SecContext{
		logger:           loggerArg,
		mechInfo:         mi,
		completionStatus: TD2_CTX_NEW,
		cI: confInteg{
			algDH:       algDHCtx{},
			verifyDHKey: false,
			replay:      mi.mechProperties.ReplayDetection,
			sequence:    mi.mechProperties.OutOfSequenceDetection,
		},
	}
}

// String interface
func (secCtx *td2SecContext) String() string { return fmt.Sprintf("td2SecContext:%p", secCtx) }

// gssp interface
func (secCtx *td2SecContext) gsspConfigure() (status TdgssStatus) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspConfigure %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspConfigure %v", secCtx)) }()
	}

	status.Major = GSS_S_COMPLETE
	return
}

/***************************************************************
* Given an input token, creates the next (output) token in the DH
* key exchange with the server.
* The client will call tdgssInitSecContext/gsspInitSecContext
* multiple times until the key exchange is completed.
* Returns GSS_S_COMPLETE or GSS_S_CONTINUE_NEEDED in status.Major
***************************************************************/
func (secCtx *td2SecContext) gsspInitSecContext(targetName string, in []byte) (status TdgssStatus, retFlags uint32, timeRec uint32, needTrailer bool, out []byte) {

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

	switch secCtx.completionStatus {
	case TD2_CTX_NEW:
		/* Get the Cipher suites parcel */
		_, cipherSuites := gsspGetCipherSuites()
		var cipherSuitesLength uint32 = uint32(len(cipherSuites))

		/* Build up token header to prepare the output token */
		var tokenHdr []byte
		status, tokenHdr = tdatIntBuildTokenHdr(
			secCtx.logger,
			VERSION1,
			INITCTXMSG,
			INITMSG,
			UNIQUEIVKEYEDMIC|ENHANCEDQOP|ASN1WRAPTOKEN,
			0,
			0,
			uint32(TDAT_INIT_MESSAGE_LENGTH+len(cipherSuites)),
			0)

		if status.Major != GSS_S_COMPLETE {
			status.Minor = TDGSS_ERR_CANT_INIT_SEC_CONTEXT
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		/* Build up the initial message to the server. */
		tdatInitMsg := tdatInitMessage{
			CipherSuitesLength: cipherSuitesLength,
		}

		var initMsg []byte
		status, initMsg = tdatIntBuildInitMsg(secCtx.logger, &tdatInitMsg)
		if status.Major != GSS_S_COMPLETE {
			status.Minor = TDGSS_ERR_CANT_INIT_SEC_CONTEXT
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		secCtx.completionStatus = TD2_CTX_SEND_KEYS

		out = concat(tokenHdr, initMsg, cipherSuites)
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext Initial token:\n%v", hex.Dump(out)))
		}

		/* Set output parameters for returns */
		status.Major = GSS_S_CONTINUE_NEEDED
		status.Minor = TD2_S_CONTINUE_NEEDED
		retFlags, timeRec = td2IntClientReturns(secCtx, status)
		needTrailer = true
		return

	case TD2_CTX_SEND_KEYS:
		// Validate input
		if len(in) == 0 {
			status.Minor = TD2_ERR_INVALID_ARGUMENT
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext Bad input data!"))
			}
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		/* Retrieve V1 token header and init message */
		var tokenHdrFromServer tdatV1TokenHeader
		var initMsg tdatInitMessage
		status, tokenHdrFromServer, initMsg = tdatIntRetrieveHeadersV1(in)
		if status.Major != GSS_S_COMPLETE {
			status.Minor = TD2_ERR_INVALID_ARGUMENT
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext Unable to retrieve header!"))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		// Retrieve the capabilities
		secCtx.peerCapabilities = tokenHdrFromServer.FQop
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext peerCapabilities %x", secCtx.peerCapabilities))
		}

		// Extract P, G, and Server public key
		var pValue, gValue, peerPublicKey []byte
		status, pValue, gValue, peerPublicKey = tdatIntRetrievePGAndPublicKeys(initMsg, in)
		if status.Major != GSS_S_COMPLETE {
			status.Minor = TD2_ERR_INVALID_ARGUMENT
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext Unable to retrieve P, G, and Server public key!"))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		if (secCtx.peerCapabilities & ENHANCEDQOP) != 0 {
			// Extract the QoPs.
			var qOPs []byte
			status, qOPs = tdatIntRetrieveQOPs(initMsg, in)
			if status.Major != GSS_S_COMPLETE {
				status.Minor = TD2_ERR_INVALID_ARGUMENT
				if secCtx.logger.HasDebugLog() {
					secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext Unable to retrieve QOPs!"))
				}
				status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
				return
			}
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext qOPS:\n%v", hex.Dump(qOPs)))
			}

			status, secCtx.cI.algQopEx = gsspGetSelectedQops(qOPs)
			if status.Major != GSS_S_COMPLETE {
				if secCtx.logger.HasDebugLog() {
					secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext Unable to convert QOPs from ASN.1!"))
				}
				status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
				return
			}
		} else {
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext legacy Qops are not supported!"))
			}
			return
		}

		var abyPublicKey []byte
		status, abyPublicKey = tdatComputeKeys(secCtx.logger, &secCtx.cI.algDH, pValue, gValue, peerPublicKey)
		if status.Major != GSS_S_COMPLETE {
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext Unable to compute keys!"))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		status = tdatIntSetAlgCtx(secCtx.logger, &secCtx.cI.algDH, secCtx.cI.algQopEx, secCtx.peerCapabilities)
		if status.Major != GSS_S_COMPLETE {
			status.Minor = getTd2MinorStatus(status.Minor)
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext tdatIntSetAlgCtx failed!"))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		if (secCtx.peerCapabilities & UNIQUEIVKEYEDMIC) != 0 {
			/* Initialize mySeqNum and peerSeqNum  */
			secCtx.cI.mySeqNum = 1
			secCtx.cI.peerSeqNum = 1
		}

		secCtx.peerCapabilities = tokenHdrFromServer.FQop

		/* Build up token header to prepare the output token */
		var tokenHdr []byte
		if (secCtx.peerCapabilities & UNIQUEIVKEYEDMIC) != 0 {
			status, tokenHdr = tdatIntBuildTokenHdr(
				secCtx.logger,
				VERSION3,
				INITCTXMSG,
				CONTMSG,
				UNIQUEIVKEYEDMIC,
				0,
				0,
				uint32(len(abyPublicKey)),
				0)
		} else {
			status, tokenHdr = tdatIntBuildTokenHdr(
				secCtx.logger,
				VERSION1,
				INITCTXMSG,
				CONTMSG,
				NOCAPABILITIES,
				0,
				0,
				uint32(len(abyPublicKey)),
				0)
		}

		if status.Major != GSS_S_COMPLETE {
			status.Minor = TDGSS_ERR_CANT_INIT_SEC_CONTEXT
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext Unable to build header file!"))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		out = concat(tokenHdr, abyPublicKey)

		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext send keys output token:\n%v", hex.Dump(out)))
		}

		bVerifyKey := initMsg.VerifyDHKeyFlag != 0
		if bVerifyKey {
			secCtx.completionStatus = TD2_CTX_VERIFY_KEY
			secCtx.cI.protReady = true
			status.Major = GSS_S_CONTINUE_NEEDED
			status.Minor = TD2_S_CONTINUE_NEEDED
			retFlags, timeRec = td2IntClientReturns(secCtx, status)
			return
		}

		secCtx.completionStatus = TD2_CTX_SETUP_COMPLETE
		secCtx.cI.protReady = true
		status.Major = GSS_S_COMPLETE
		status.Minor = TD2_S_COMPLETE
		retFlags, timeRec = td2IntClientReturns(secCtx, status)
		return

	case TD2_CTX_VERIFY_KEY:
		// Validate input
		if len(in) == 0 {
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext verify key Bad input data!"))
			}
			return
		}
		out = nil
		if in[0] != SECRETKEY_MATCHED {
			status.Major = GSS_S_FAILURE
			status.Minor = TD2_ERR_INT_FAILURE
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}
		status.Major = GSS_S_COMPLETE
		status.Minor = TD2_S_COMPLETE
		retFlags, timeRec = td2IntClientReturns(secCtx, status)
		return

	case TD2_CTX_SETUP_COMPLETE:
		status.Major = GSS_S_COMPLETE
		status.Minor = TD2_S_COMPLETE
		retFlags, timeRec = td2IntClientReturns(secCtx, status)
		out = nil
		return

	default:
		status.Major = GSS_S_FAILURE
		status.Minor = TD2_ERR_INT_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	return
}

// This function is invoked by gsspInitSecContext only when DH/CI Bypass has been initiated by the client (and thus TLS is active for the connection).
// The Invocation is triggered when TDGSS_C_CI_BYPASS_FLAG is set in secCtx.mechInfo.desiredServices. Note that this function is never called directly.
// The behavior of this parallel function mirrors that of gsspInitSecContext, but all Diffie-Hellman and Confidentiality-Integrity processing (such as
// establishing DH keys, and handling cipher suites) has been removed, as TLS handles all cryptographic duties.

func (secCtx *td2SecContext) gsspInitSecContextTls(targetName string, in []byte) (status TdgssStatus, retFlags uint32, timeRec uint32, needTrailer bool, out []byte) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspInitSecContextTls %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspInitSecContextTls %v", secCtx)) }()
	}

	status.Major = GSS_S_FAILURE

	switch secCtx.completionStatus {
		case TD2_CTX_NEW:

			// Build up token header to prepare the output token
			var tokenHdr []byte
			status, tokenHdr = tdatIntBuildTokenHdr(
				secCtx.logger,
				VERSION1,
				INITCTXMSG,
				INITMSG,
				DHCIBYPASS,
				0,
				0,
				uint32(TDAT_INIT_MESSAGE_LENGTH),
				0)

			if status.Major != GSS_S_COMPLETE {
				status.Minor = TDGSS_ERR_CANT_INIT_SEC_CONTEXT
				status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
				return
			}

			/* Build up the initial message to the server. */
			tdatInitMsg := tdatInitMessage{}

			var initMsg []byte
			status, initMsg = tdatIntBuildInitMsg(secCtx.logger, &tdatInitMsg)
			if status.Major != GSS_S_COMPLETE {
				status.Minor = TDGSS_ERR_CANT_INIT_SEC_CONTEXT
				status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
				return
			}

			secCtx.completionStatus = TD2_CTX_SEND_KEYS

			out = concat(tokenHdr, initMsg)
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContextTls Initial token:\n%v", hex.Dump(out)))
			}

			/* Set the bCIBypass context flag to TRUE for subsequent use. */
			secCtx.bCIBypass = true

			/* Set output parameters for returns */
			status.Major = GSS_S_CONTINUE_NEEDED
			status.Minor = TD2_S_CONTINUE_NEEDED
			retFlags, timeRec = td2IntClientReturns(secCtx, status)
			needTrailer = true
			return

		case TD2_CTX_SEND_KEYS:
			// Validate input
			if len(in) == 0 {
				status.Minor = TD2_ERR_INVALID_ARGUMENT
				if secCtx.logger.HasDebugLog() {
					secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContextTls Bad input data!"))
				}
				status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
				return
			}

			/* Retrieve V1 token header and init message */
			var tokenHdrFromServer tdatV1TokenHeader
			status, tokenHdrFromServer, _ /*initMsg*/ = tdatIntRetrieveHeadersV1(in)
			if status.Major != GSS_S_COMPLETE {
				status.Minor = TD2_ERR_INVALID_ARGUMENT
				if secCtx.logger.HasDebugLog() {
					secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContextTls Unable to retrieve header!"))
				}
				status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
				return
			}

			// Retrieve the capabilities
			secCtx.peerCapabilities = tokenHdrFromServer.FQop
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContextTls peerCapabilities %x", secCtx.peerCapabilities))
			}

			/* Set the bCIBypass context flag to TRUE for subsequent use. */
			secCtx.bCIBypass = true

			secCtx.completionStatus = TD2_CTX_SETUP_COMPLETE
			secCtx.cI.protReady = false
			status.Major = GSS_S_COMPLETE
			status.Minor = TD2_S_COMPLETE
			retFlags, timeRec = td2IntClientReturns(secCtx, status)
			return

		case TD2_CTX_SETUP_COMPLETE:
			status.Major = GSS_S_COMPLETE
			status.Minor = TD2_S_COMPLETE
			retFlags, timeRec = td2IntClientReturns(secCtx, status)
			out = nil
			return

		default:
			status.Major = GSS_S_FAILURE
			status.Minor = TD2_ERR_INT_FAILURE
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return

	} // end switch

	return

} // end gsspInitSecContextTls

func (secCtx *td2SecContext) gsspWrap(confReqFlag int, qopReq uint32, in []byte) (status TdgssStatus, confState int, out []byte) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspWrap %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspWrap %v", secCtx)) }()
	}

	/* Shouldn't call gsspWrap if bCIBypass is set */
	if secCtx.bCIBypass {
		status.Major = GSS_S_FAILURE
		status.Minor = TD2_ERR_CONF_INTEG_NOT_AVAILABLE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status, confState, out = tdatWrap(secCtx.logger, &secCtx.cI, secCtx.peerCapabilities, confReqFlag, qopReq, in)
	status.Minor = getTd2MinorStatus(status.Minor)
	if status.Major != GSS_S_COMPLETE {
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("gsspWrap failed"))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
	}

	return
}

func (secCtx *td2SecContext) gsspUnwrap(in []byte) (status TdgssStatus, confState int, qopState uint32, out []byte) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspUnwrap %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspUnwrap %v", secCtx)) }()
	}

	/* Shouldn't call gsspUnwrap if bCIBypass is set */
	if secCtx.bCIBypass {
		status.Major = GSS_S_FAILURE
		status.Minor = TD2_ERR_CONF_INTEG_NOT_AVAILABLE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status, confState, qopState, out = tdatUnwrap(secCtx.logger, &secCtx.cI, secCtx.peerCapabilities, in)
	status.Minor = getTd2MinorStatus(status.Minor)
	if status.Major != GSS_S_COMPLETE {
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("gsspUnwrap failed"))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
	}

	return
}

func (secCtx *td2SecContext) gsspAcquireCred(userLgnMechName string, credUsage int, desiredName string) (status TdgssStatus, timeRec uint32) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspAcquireCred %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspAcquireCred %v", secCtx)) }()
	}

	status.Major = GSS_S_COMPLETE
	status.Minor = GSS_S_COMPLETE

	timeRec = 0

	if credUsage != GSS_C_BOTH && credUsage != GSS_C_INITIATE && credUsage != GSS_C_ACCEPT {
		status.Major = GSS_S_FAILURE
		status.Minor = TD2_ERR_INVALID_INPUT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	secCtx.td2Cred.principalName = userLgnMechName
	secCtx.td2Cred.credUsage = credUsage
	secCtx.td2Cred.lifeTime = GSS_C_INDEFINITE

	return
}

func (secCtx *td2SecContext) gsspReleaseCred() (status TdgssStatus) {
	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspReleaseCred %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspReleaseCred %v", secCtx)) }()
	}
	status.Major = GSS_S_COMPLETE
	return
}

func (secCtx *td2SecContext) gsspDeleteContext() (status TdgssStatus) {
	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspDeleteContext %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspDeleteContext %v", secCtx)) }()
	}
	status.Major = GSS_S_COMPLETE
	return
}

func (secCtx *td2SecContext) gsspGetMechInfo() (mechInfo *mechanismInfo) {
	return secCtx.mechInfo
}

func (secCtx *td2SecContext) gsspIsEstablished() bool {
	return secCtx.completionStatus == TD2_CTX_SETUP_COMPLETE
}
