// Copyright 2021 by Teradata Corporation. All Rights Reserved.
package goteragss

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

const (
	LDAP_CTX_NEW = iota
	LDAP_CTX_SEND_KEYS
	LDAP_CTX_COMPUTE_KEY
	LDAP_CTX_BINDABLE
	LDAP_CTX_USER_PROPERTIES
	LDAP_CTX_SETUP_COMPLETE
)

type ldapSecContext struct {
	mechInfo          *mechanismInfo
	logger            TdgssLogger
	cI                confInteg /* See TdatUtil.go */
	peerCapabilities  uint32
	deleg             bool
	mutual            bool
	trans             bool
	completionStatus  uint8
	userProperties    []ldapProperty
	srcName           string
	targetName        string
	ldapCred          tdatCred
}

var _ gssp = &ldapSecContext{} // implements gssp

func newLdapMech(loggerArg TdgssLogger, mi *mechanismInfo) gssp {

	return &ldapSecContext{
		logger:           loggerArg,
		mechInfo:         mi,
		completionStatus: LDAP_CTX_NEW,
		cI: confInteg{
			algDH:    algDHCtx{},
			conf:     mi.mechProperties.ConfidentialityDesired,
			integ:    mi.mechProperties.IntegrityDesired,
			replay:   mi.mechProperties.ReplayDetection,
			sequence: mi.mechProperties.OutOfSequenceDetection,
		},
		deleg:  mi.mechProperties.DelegateCredentials,
		mutual: mi.mechProperties.MutualAuthentication,
	}
}

// String interface
func (secCtx *ldapSecContext) String() string { return fmt.Sprintf("ldapSecContext: %p", secCtx) }

// gssp interface
func (secCtx *ldapSecContext) gsspConfigure() (status TdgssStatus) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspConfigure %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspConfigure %v", secCtx)) }()
	}

	status.Major = GSS_S_COMPLETE
	return
}

func (secCtx *ldapSecContext) gsspInitSecContext(targetName string, in []byte) (status TdgssStatus, retFlags uint32, timeRec uint32, needTrailer bool, out []byte) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspInitSecContext %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspInitSecContext %v", secCtx)) }()
	}

	if len(secCtx.ldapCred.principalName) == 0 {
		status.Major = GSS_S_FAILURE
		status.Minor = LDAPV3_ERR_DEFECTIVE_CREDENTIAL
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	switch secCtx.completionStatus {
	case LDAP_CTX_NEW:
		/* Get the Cipher suites parcel */
		_, cipherSuites := gsspGetCipherSuites()
		var cipherSuitesLength uint32 = uint32(len(cipherSuites))

		/* Build up token header to prepare the output token */
		var reqdFlags byte = 0

		var tokenHdr []byte
		status, tokenHdr = tdatIntBuildTokenHdr(
			secCtx.logger,
			VERSION1,
			INITCTXMSG,
			INITMSG,
			UNIQUEIVKEYEDMIC|ENHANCEDQOP|ELIMINATETOKEN|ASN1WRAPTOKEN,
			reqdFlags,
			0,
			uint32(TDAT_INIT_MESSAGE_LENGTH+len(cipherSuites)),
			0)

		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
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
			status.Major = GSS_S_FAILURE
			status.Minor = TDGSS_ERR_CANT_INIT_SEC_CONTEXT
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		secCtx.completionStatus = LDAP_CTX_SEND_KEYS

		out = concat(tokenHdr, initMsg, cipherSuites)
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext Initial token:\n%v", hex.Dump(out)))
		}

		/* Set output parameters for returns */
		status.Major = GSS_S_CONTINUE_NEEDED
		status.Minor = LDAPV3_S_COMPLETE
		retFlags, timeRec = ldapIntClientReturns(secCtx.logger, secCtx)
		needTrailer = true
		return

	case LDAP_CTX_SEND_KEYS:
		// Output token contains the following
		// Token Header (16 bytes)
		// Init Message (64 bytes)
		// Public key
		// Wrapped logdata for example "guest@@please"

		// Validate input
		if len(in) == 0 {
			status.Major = GSS_S_FAILURE
			status.Minor = LDAPV3_ERR_INVALID_ARGUMENT
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
			status.Major = GSS_S_FAILURE
			status.Minor = LDAPV3_ERR_INVALID_ARGUMENT
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

		if (secCtx.peerCapabilities & ELIMINATETOKEN) == 0 {
			status.Major = GSS_S_FAILURE
			status.Minor = LDAPV3_ERR_INVALID_ARGUMENT
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext does not support 6 token exchange!"))
			}
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		// Extract P, G, and Server public key
		var pValue, gValue, peerPublicKey []byte
		status, pValue, gValue, peerPublicKey = tdatIntRetrievePGAndPublicKeys(initMsg, in)
		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			status.Minor = LDAPV3_ERR_INVALID_ARGUMENT
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
				status.Major = GSS_S_FAILURE
				status.Minor = LDAPV3_ERR_INVALID_ARGUMENT
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
				status.Major = GSS_S_FAILURE
				if secCtx.logger.HasDebugLog() {
					secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext Unable to convert QOPs from ASN.1!"))
				}
				status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
				return
			}
		} else {
			status.Major = GSS_S_FAILURE
			status.Minor = LDAPV3_ERR_INVALID_ARGUMENT
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext legacy Qops are not supported!"))
			}
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		var abyPublicKey []byte
		status, abyPublicKey = tdatComputeKeys(secCtx.logger, &secCtx.cI.algDH, pValue, gValue, peerPublicKey)
		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext Unable to compute keys!"))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}
		var publicKeyLength uint32 = uint32(len(abyPublicKey))

		status = tdatIntSetAlgCtx(secCtx.logger, &secCtx.cI.algDH, secCtx.cI.algQopEx, secCtx.peerCapabilities)
		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			status.Minor = getLdapMinorStatus(status.Minor)
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

		secCtx.cI.protReady = true
		// Wrap logdata
		var abyLogData []byte
		status, _, abyLogData = secCtx.gsspWrap(1, 0, []byte(secCtx.ldapCred.principalName))
		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}
		var logDataLength uint32 = uint32(len(abyLogData))

		/* Build up the initial message to the server with public key length and logdata length. */
		tdatInitMsg := tdatInitMessage{
			PublicKeyLength:  publicKeyLength,
			EncLogDataLength: logDataLength,
		}

		var clientInitMsg []byte
		status, clientInitMsg = tdatIntBuildInitMsg(secCtx.logger, &tdatInitMsg)
		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			status.Minor = TDGSS_ERR_CANT_INIT_SEC_CONTEXT
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}
		var clientInitMsgLength uint32 = uint32(len(clientInitMsg))

		var reqdFlags byte = 0
		if secCtx.deleg {
			reqdFlags |= DELEGATION
		}

		/* Build up token header to prepare the output token */
		var tokenHdr []byte
		if (secCtx.peerCapabilities & UNIQUEIVKEYEDMIC) != 0 {
			status, tokenHdr = tdatIntBuildTokenHdr(
				secCtx.logger,
				VERSION3,
				LOGINMSG,
				CONTMSG,
				NOTAPPLICABLE,
				reqdFlags,
				0,
				clientInitMsgLength+publicKeyLength+logDataLength,
				0)
		} else {
			status, tokenHdr = tdatIntBuildTokenHdr(
				secCtx.logger,
				VERSION1,
				ACCEPTCTXMSG,
				CONTMSG,
				NOCAPABILITIES,
				reqdFlags,
				0,
				clientInitMsgLength+publicKeyLength+logDataLength,
				0)
		}

		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			status.Minor = TDGSS_ERR_CANT_INIT_SEC_CONTEXT
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext Unable to build header file!"))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		// add wrapped logdata
		out = concat(tokenHdr, clientInitMsg, abyPublicKey, abyLogData)

		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext send keys output token:\n%v", hex.Dump(out)))
		}

		secCtx.completionStatus = LDAP_CTX_USER_PROPERTIES
		status.Major = GSS_S_CONTINUE_NEEDED
		status.Minor = LDAPV3_S_COMPLETE
		retFlags, timeRec = ldapIntClientReturns(secCtx.logger, secCtx)
		return

	case LDAP_CTX_USER_PROPERTIES:
		/* Add the user properties to the security context */
		// Validate input
		if len(in) == 0 {
			status.Major = GSS_S_FAILURE
			status.Minor = LDAPV3_ERR_INVALID_ARGUMENT
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext Bad input data!"))
			}
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		// Retrieve token header
		var tdatTokHder tdatV3TokenHeader
		// De-serialize the V3 token header
		r := bytes.NewReader(in)
		err := binary.Read(r, binary.BigEndian, &tdatTokHder)
		if err != nil {
			status.Major = GSS_S_FAILURE
			status.Minor = LDAPV3_ERR_INVALID_ARGUMENT
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext Unable to read token header! %v", err))
			}
			status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
			return
		}

		abyUserProperties := in[TDAT_TOKEN_HEADER_LENGTH:]
		if uint32(len(abyUserProperties)) != tdatTokHder.UlMsgLength {
			status.Major = GSS_S_FAILURE
			status.Minor = LDAPV3_ERR_INVALID_ARGUMENT
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext invalid user properties length!"))
			}
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		status, secCtx.userProperties = ldapIntNormalizeProps(secCtx.logger, abyUserProperties)
		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext Unable to retrieve user properties!"))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		secCtx.completionStatus = LDAP_CTX_SETUP_COMPLETE
		secCtx.trans = true
		fallthrough

	case LDAP_CTX_SETUP_COMPLETE:
		status.Major = GSS_S_COMPLETE
		status.Minor = LDAPV3_S_COMPLETE
		retFlags, timeRec = ldapIntClientReturns(secCtx.logger, secCtx)
		out = nil
		return

	default:
		status.Major = GSS_S_FAILURE
		status.Minor = LDAPV3_ERR_BAD_CONTEXT_STATE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	return
}

func (secCtx *ldapSecContext) gsspWrap(confReqFlag int, qopReq uint32, in []byte) (status TdgssStatus, confState int, out []byte) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspWrap LDAP %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspWrap LDAP %v", secCtx)) }()
	}

	status, confState, out = tdatWrap(secCtx.logger, &secCtx.cI, secCtx.peerCapabilities, confReqFlag, qopReq, in)
	status.Minor = getLdapMinorStatus(status.Minor)
	if status.Major != GSS_S_COMPLETE {
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("ldap gsspWrap failed"))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
	}

	return
}

func (secCtx *ldapSecContext) gsspUnwrap(in []byte) (status TdgssStatus, confState int, qopState uint32, out []byte) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspUnwrap LDAP %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspUnwrap LDAP %v", secCtx)) }()
	}

	status, confState, qopState, out = tdatUnwrap(secCtx.logger, &secCtx.cI, secCtx.peerCapabilities, in)
	status.Minor = getLdapMinorStatus(status.Minor)
	if status.Major != GSS_S_COMPLETE {
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("ldap gsspUnwrap failed"))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
	}

	return
}

func (secCtx *ldapSecContext) gsspAcquireCred(userLgnMechName string, credUsage int, desiredName string) (status TdgssStatus, timeRec uint32) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspAcquireCred for LDAP %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspAcquireCred for LDAP %v", secCtx)) }()
	}

	status.Major = GSS_S_COMPLETE
	status.Minor = GSS_S_COMPLETE

	if credUsage != GSS_C_BOTH && credUsage != GSS_C_INITIATE && credUsage != GSS_C_ACCEPT {
		status.Major = GSS_S_FAILURE
		status.Minor = LDAPV3_ERR_INVALID_INPUT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if len(desiredName) == 0 && credUsage != GSS_C_ACCEPT {
		status.Major = GSS_S_FAILURE
		status.Minor = LDAPV3_ERR_NO_NAME
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	secCtx.ldapCred.principalName = desiredName
	secCtx.ldapCred.credUsage = credUsage
	secCtx.ldapCred.lifeTime = GSS_C_INDEFINITE
	timeRec = GSS_C_INDEFINITE
	return
}

func (secCtx *ldapSecContext) gsspReleaseCred() (status TdgssStatus) {
	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspReleaseCred %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspReleaseCred %v", secCtx)) }()
	}
	status.Major = GSS_S_COMPLETE
	return
}

func (secCtx *ldapSecContext) gsspDeleteContext() (status TdgssStatus) {
	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspDeleteContext %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspDeleteContext %v", secCtx)) }()
	}
	status.Major = GSS_S_COMPLETE
	return
}

func (secCtx *ldapSecContext) gsspGetMechInfo() (mechInfo *mechanismInfo) {
	return secCtx.mechInfo
}

func (secCtx *ldapSecContext) gsspIsEstablished() bool {
	return secCtx.completionStatus == LDAP_CTX_SETUP_COMPLETE
}
