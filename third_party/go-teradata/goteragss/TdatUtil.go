// Copyright 2021 by Teradata Corporation. All Rights Reserved.
package goteragss

/*********************************************************************
* Generic constants, structs and functions for use by any mechanism.
*********************************************************************/

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
)

/*
 * Note functions in this module return generic (TDAT) minor status codes.
 * The calling mechanism must map these to mechanism-specific codes.
 */
const (
	TDAT_S_COMPLETE                    = 0
	TDAT_S_CONTINUE_NEEDED             = 1
	TDAT_ERR_INT_FAILURE               = 2
	TDAT_ERR_ALG_NO_CONTEXT            = 3
	TDAT_ERR_INVALID_NO_QOPS           = 4
	TDAT_ERR_LEGACY_QOP_DISABLED       = 5
	TDAT_ERR_INVALID_TOKEN 		       = 6
	TDAT_ERR_CONTEXT_NOT_READY 	       = 7
	TDAT_ERR_INVALID_INPUT 		       = 8
	TDAT_ERR_CONF_INTEG_NOT_AVAILABLE  = 9
	TDAT_ERR_CIBYPASSFAIL 			   = 10 // 0xA
	TDAT_LDAP_ERR_UNKNOWN_PROPERTY     = 16 // 0x10
	TDAT_LDAP_ERR_DUPLICATE_TOKEN      = 26 // 0x1A
	TDAT_INVALID_ARGUMENT              = 27 // 0x1B
)

/* Mechanisms using tdat functions must include this in security context */
type confInteg struct {
	algDH       algDHCtx
	algQopEx    []algQOPEx /* Array for storing AlgQopEx's */
	mySeqNum    uint64
	peerSeqNum  uint64
	cbPKeySize  uint32 /* Length of P key size in bytes */
	conf        bool
	integ       bool
	verifyDHKey bool /* Verify the secret key */
	replay      bool
	sequence    bool
	deleg       bool
	mutual      bool
	protReady   bool /* The context is ready to be used */
}

/* Generic credential structure */
type tdatCred struct {
	principalName string
	lifeTime      uint32
	credUsage     int
}

const VERSION1 = 0x1
const VERSION2 = 0x2
const VERSION3 = 0x3
const CLIENT = 0

const TDATQOP = 0

/* Message Types */
const INITCTXMSG = 1
const ACCEPTCTXMSG = 2
const ENCRYPTMSG = 3
const SIGNATUREMSG = 4
const LOGINMSG = 5
const ENCRYPTMSG2 = 7
const SIGNATUREMSG2 = 8

/* ByteVar values */
const INITMSG = 1     // initial message
const CONTMSG = 2     // continuing message
const KEYMSG = 3      // key message
const CONFMSG = 4     // last confirmation message
const LOGDATAMSG = 5  // Logdata from client
const NOTAPPLICABLE = 255


/* The following definitions defines the Capabilities of the tdgss
 * server and the client
 */
const NOCAPABILITIES = 0
const UNIQUEIVKEYEDMIC = 0x1
const LOGDATA = 0x2
const ENHANCEDQOP = 0x4
const ELIMINATETOKEN = 0x8 /* This capability is used by LDAP mechanism to eliminate a token exchange. See DR 93566 for more information */
const ASN1WRAPTOKEN = 0x10 /* DR169785 10(Hex)=16(Decimal) This is used to to indicate the capability to handle ASN.1 encoded wrap unwrap tokens */
const EXPIMPORTCRED = 0x20 /* Pob-88 This is used by Proxy mechanism to handle an exported credential token then import it to a credential and returned it as a delegated credential. */
const DHCIBYPASS = 0x80000000 /* This is used to indicate that TDGSS may bypass its Diffie-Hellman and confidentiality/integrity duties since TLS is on for the connection. */

const TDAT_TOKEN_HEADER_LENGTH = 16

type tdatV1TokenHeader struct {
	Version        byte
	MsgType        byte
	ByteVar        byte
	ClientOrServer byte
	MsgLength      uint32
	FQop           uint32
	Flags          byte
	_              byte // unused padding
	_              byte // unused padding
	_              byte // unused padding
}

type tdatV3TokenHeader struct {
	Version     byte
	MsgType     byte
	Flags       byte
	FQop        byte
	UlMsgLength uint32
	SeqNum      uint64
}

const TDAT_INIT_MESSAGE_LENGTH = 64

type tdatInitMessage struct {
	MajorVersion       byte
	MinorVersion       byte
	MaintVersion       byte
	EfixVersion        byte
	PKeyLength         uint32
	GKeyLength         uint32
	PublicKeyLength    uint32
	VerifyDHKeyFlag    uint32
	CipherSuitesLength uint32
	EncLogDataLength   uint32
	_                  uint32 // unused padding
	_                  uint64 // unused padding
	_                  uint64 // unused padding
	_                  uint64 // unused padding
	_                  uint64 // unused padding
}

func tdatIntBuildTokenHdr(logger TdgssLogger, version byte, msgType byte, byteVar byte, fCapabilities uint32, reqFlags byte, fQop uint32, tokenLength uint32, seqNum uint64) (status TdgssStatus, tokenHdr []byte) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter tdatIntBuildTokenHdr"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave tdatIntBuildTokenHdr")) }()
	}

	status.Major = GSS_S_FAILURE

	if (fCapabilities&UNIQUEIVKEYEDMIC != 0) && (msgType == ENCRYPTMSG2 || msgType == SIGNATUREMSG2) {
		if version != VERSION3 {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatIntBuildTokenHdr: wrong version for version 3 header %d", version))
			}
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		tdatTokenHdr := tdatV3TokenHeader{
			Version:     version,
			MsgType:     msgType,
			Flags:       reqFlags,
			FQop:        byte(fQop),
			UlMsgLength: tokenLength,
			SeqNum:      seqNum,
		}

		var buf bytes.Buffer
		err := binary.Write(&buf, binary.BigEndian, tdatTokenHdr)
		if err != nil {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatIntBuildTokenHdr: binary.Write failed: %v", err))
			}
			status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
			return
		}
		tokenHdr = buf.Bytes()

	} else {
		// Allow for version3 value because the C code allowed for a version3 value in a version1 header.
		// Don't want to break something on the server side.
		tdatTokenHdr := tdatV1TokenHeader{
			Version:        version,
			MsgType:        msgType,
			ByteVar:        byteVar,
			ClientOrServer: CLIENT,
			Flags:          reqFlags,
			MsgLength:      tokenLength,
		}

		if (msgType == INITCTXMSG || msgType == ACCEPTCTXMSG) && byteVar == INITMSG {
			tdatTokenHdr.FQop = fCapabilities
		} else {
			tdatTokenHdr.FQop = TDATQOP
		}

		var buf bytes.Buffer
		err := binary.Write(&buf, binary.BigEndian, tdatTokenHdr)
		if err != nil {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatIntBuildTokenHdr: binary.Write failed: %v", err))
			}
			status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
			return
		}
		tokenHdr = buf.Bytes()
	}
	status.Major = GSS_S_COMPLETE
	return
}

func tdatIntBuildInitMsg(logger TdgssLogger, tdatInitMsg *tdatInitMessage) (status TdgssStatus, initMsg []byte) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter tdatIntBuildInitMsg"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave tdatIntBuildInitMsg")) }()
	}

	tdatInitMsg.MajorVersion = GoTeraGSSMajorVersion
	tdatInitMsg.MinorVersion = GoTeraGSSMinorVersion
	tdatInitMsg.MaintVersion = GoTeraGSSMaintVersion
	tdatInitMsg.EfixVersion  = GoTeraGSSEfixVersion

	var buf bytes.Buffer
	err := binary.Write(&buf, binary.BigEndian, tdatInitMsg)
	if err != nil {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatIntBuildInitMsg: binary.Write failed: %v", err))
		}
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}
	initMsg = buf.Bytes()
	status.Major = GSS_S_COMPLETE
	return
}

/***************************************************************
* tdatIntRetrieveHeadersV3 only handles V3 Token Headers.
* GSS_S_FAILURE is returned for any other token header version.
***************************************************************/
func tdatIntRetrieveHeadersV3(in []byte) (status TdgssStatus, tdatTokHder tdatV3TokenHeader, tdatInitMsg tdatInitMessage) {

	status.Major = GSS_S_FAILURE
	if in == nil {
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// De-serialize the V3 token header
	r := bytes.NewReader(in)
	err := binary.Read(r, binary.BigEndian, &tdatTokHder)
	if err != nil {
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}

	// De-serialize the InitMessage header
	err = binary.Read(r, binary.BigEndian, &tdatInitMsg)
	if err != nil {
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}

	// Only V3 headers are handled
	if tdatTokHder.Version != VERSION3 {
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Only accept messages from a server are handled:
	if tdatTokHder.MsgType != ACCEPTCTXMSG {
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Minimum message length is size of init message
	if tdatTokHder.UlMsgLength < TDAT_INIT_MESSAGE_LENGTH {
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = GSS_S_COMPLETE
	return
}

/***************************************************************
* tdatIntRetrieveHeadersV1 currently only handles V1 Token Headers.
* However, due to the C code allowing a version number 3 in
* a V1 Token header, we will allow either version number in the header.
***************************************************************/
func tdatIntRetrieveHeadersV1(in []byte) (status TdgssStatus, tdatTokHder tdatV1TokenHeader, tdatInitMsg tdatInitMessage) {

	status.Major = GSS_S_FAILURE
	if in == nil {
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// De-serialize the V1 token header
	r := bytes.NewReader(in)
	err := binary.Read(r, binary.BigEndian, &tdatTokHder)
	if err != nil {
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}

	// De-serialize the InitMessage header
	err = binary.Read(r, binary.BigEndian, &tdatInitMsg)
	if err != nil {
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}

	// V3 and V1 headers are handled
	if tdatTokHder.Version != VERSION1 && tdatTokHder.Version != VERSION3 {
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Only accept messages from a server are handled:
	if tdatTokHder.MsgType != ACCEPTCTXMSG {
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Minimum message length is size of init message
	if tdatTokHder.MsgLength < TDAT_INIT_MESSAGE_LENGTH {
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = GSS_S_COMPLETE
	return
}

func tdatIntRetrievePGAndPublicKeys(tdatInitMsg tdatInitMessage, in []byte) (status TdgssStatus, pValue []byte, gValue []byte, peerPublicKey []byte) {

	headersLen := TDAT_TOKEN_HEADER_LENGTH + TDAT_INIT_MESSAGE_LENGTH
	pSize := int(tdatInitMsg.PKeyLength)
	gSize := int(tdatInitMsg.GKeyLength)
	peerPublicSize := int(tdatInitMsg.PublicKeyLength)

	if len(in) < headersLen+pSize+gSize+peerPublicSize {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	pValue = in[headersLen : headersLen+pSize]
	gValue = in[headersLen+pSize : headersLen+pSize+gSize]
	peerPublicKey = in[headersLen+pSize+gSize : headersLen+pSize+gSize+peerPublicSize]
	status.Major = GSS_S_COMPLETE
	return
}

func tdatIntRetrieveQOPs(tdatInitMsg tdatInitMessage, in []byte) (status TdgssStatus, qOPs []byte) {

	headersLen := TDAT_TOKEN_HEADER_LENGTH + TDAT_INIT_MESSAGE_LENGTH
	pSize := int(tdatInitMsg.PKeyLength)
	gSize := int(tdatInitMsg.GKeyLength)
	peerPublicSize := int(tdatInitMsg.PublicKeyLength)
	qOPsSize := int(tdatInitMsg.CipherSuitesLength)

	if len(in) < headersLen+pSize+gSize+peerPublicSize+qOPsSize {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	qOPs = in[headersLen+pSize+gSize+peerPublicSize : headersLen+pSize+gSize+peerPublicSize+qOPsSize]
	status.Major = GSS_S_COMPLETE
	return
}

func tdatIntSetAlgCtx(logger TdgssLogger, algDH *algDHCtx, algQopEx []algQOPEx, peerCapabilities uint32) (status TdgssStatus) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter tdatIntSetAlgCtx"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave tdatIntSetAlgCtx")) }()
	}

	status.Major = GSS_S_FAILURE

	if algDH == nil {
		status.Minor = TDAT_ERR_ALG_NO_CONTEXT
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatIntSetAlgCtx algDH context is nil"))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	statusAlg, secretKey := algDH.getSecretKey(logger)
	if statusAlg.Major != ALG_SUCCESS {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatIntSetAlgCtx secretKey is missing"))
		}
		status = statusAlg
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
		return
	}

	iStart := 0
	if (peerCapabilities & ENHANCEDQOP) != 0 {
		if len(algQopEx) == 0 {
			status.Minor = TDAT_ERR_INVALID_NO_QOPS
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatIntSetAlgCtx length of qops is zero."))
			}
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		// Create ciphers and digests for each qop.
		for i := range algQopEx {
			qop := &algQopEx[i]

			statusAlg = newCipher(&qop.legacyQOP)
			if statusAlg.Major != ALG_SUCCESS {
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("tdatIntSetAlgCtx newCipher failed! %v", qop.legacyQOP))
				}
				status = statusAlg
				status.Major = GSS_S_FAILURE
				status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
				return
			}

			statusAlg = newDigest(&qop.legacyQOP)
			if statusAlg.Major != ALG_SUCCESS {
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("tdatIntSetAlgCtx newDigest failed! %v", qop.legacyQOP))
				}
				status = statusAlg
				status.Major = GSS_S_FAILURE
				status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
				return
			}

			if len(secretKey) < iStart+1 {
				status.Minor = TDAT_ERR_INT_FAILURE
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("tdatIntSetAlgCtx secretKey is too short %d", len(secretKey)))
				}
				status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
				return
			}

			if qop.legacyQOP.cipherCtx != nil {
				statusAlg = qop.legacyQOP.cipherCtx.aesInitializeCipher(logger, qop.legacyQOP, secretKey[iStart:])

				if statusAlg.Major != ALG_SUCCESS {
					if logger.HasDebugLog() {
						logger.DebugLog(fmt.Sprintf("tdatIntSetAlgCtx aesInitializeCipher failed! %v", qop.legacyQOP))
					}
					status = statusAlg
					status.Major = GSS_S_FAILURE
					status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
					return
				}

			} else {
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("tdatIntSetAlgCtx qop.legacyQOP.cipherCtx is nil %d", qop.legacyQOP.cipher))
				}
			}

			digestFn := qop.legacyQOP.digestCtx
			if digestFn != nil {
				statusAlg, keyLen := digestFn.setKeyInDigestCtx(logger, qop.legacyQOP, secretKey[iStart:])

				if statusAlg.Major != ALG_SUCCESS {
					if logger.HasDebugLog() {
						logger.DebugLog(fmt.Sprintf("tdatIntSetAlgCtx setKeyInDigestCtx failed! %v", qop.legacyQOP))
					}
					status = statusAlg
					status.Major = GSS_S_FAILURE
					status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
					return
				}

				// Hash the secret key
				if (peerCapabilities & UNIQUEIVKEYEDMIC) != 0 {
					if logger.HasDebugLog() {
						logger.DebugLog(fmt.Sprintf("tdatIntSetAlgCtx keyLen=%d", keyLen))
					}
					statusAlg, qop.hashKey = digestFn.generateDigest(logger, secretKey[iStart:iStart + int(keyLen)])
					if statusAlg.Major != ALG_SUCCESS {
						if logger.HasDebugLog() {
							logger.DebugLog(fmt.Sprintf("tdatIntSetAlgCtx generateDigest failed! %v", qop.legacyQOP))
						}
						status = statusAlg
						status.Major = GSS_S_FAILURE
						status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
						return
					}
				}

			} else {
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("tdatIntSetAlgCtx digestFn is nil %d", qop.legacyQOP.digest))
				}
			}

			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatIntSetAlgCtx qop %+v", qop))
			}

			iStart += int(qop.legacyQOP.keySize) / 8
		}
	} else {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatIntSetAlgCtx legacy Qops are not supported!"))
		}
		status.Minor = TDAT_ERR_LEGACY_QOP_DISABLED
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status.Minor = TDAT_S_COMPLETE
	status.Major = GSS_S_COMPLETE
	return
}

func tdatIntSetMsgLength(msgLength uint32, tokenHdr []byte) (status TdgssStatus) {
	// Make sure the token header is 16 bytes
	if len(tokenHdr) < TDAT_TOKEN_HEADER_LENGTH {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// the message length starts at tokenHdr[4] for both version 1 and version 3 token headers.
	binary.BigEndian.PutUint32(tokenHdr[4:], msgLength)
	status.Major = GSS_S_COMPLETE
	return
}

func tdatIntGetFlags(tokenHdr []byte) (status TdgssStatus, flags byte) {
	// Make sure the token header is 16 bytes
	if len(tokenHdr) < TDAT_TOKEN_HEADER_LENGTH {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if tokenHdr[0] == VERSION3 {
		// Version 3
		flags = tokenHdr[2]
	} else {
		// Version 1
		flags = tokenHdr[12]
	}
	status.Major = GSS_S_COMPLETE
	return
}

func tdatIntGetVersion(tokenHdr []byte) (status TdgssStatus, version uint32) {
	// Make sure the token header is 16 bytes
	if len(tokenHdr) < TDAT_TOKEN_HEADER_LENGTH {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	version = uint32(tokenHdr[0])
	status.Major = GSS_S_COMPLETE
	return
}

func tdatIntGetMsgType(tokenHdr []byte) (status TdgssStatus, msgType uint32) {
	// Make sure the token header is 16 bytes
	if len(tokenHdr) < TDAT_TOKEN_HEADER_LENGTH {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	msgType = uint32(tokenHdr[1])
	status.Major = GSS_S_COMPLETE
	return
}

func tdatIntCheckTokenSequence(localSeqNum uint64, remoteSeqNum uint64, replayDetection bool, outOfSequenceDetection bool) (supplementary uint32) {

	supplementary = 0

	/* Only do Detection if requested */
	if replayDetection || outOfSequenceDetection {

		/* The sequence number from remote system should match the one from the local system */
		if remoteSeqNum != localSeqNum {

			/* If the remote repeats the last sequence number, the token is a duplicate */
			if replayDetection {
				if remoteSeqNum+1 == localSeqNum {
					supplementary |= GSS_S_DUPLICATE_TOKEN
				}
			}

			/* If the remote sequence number is older than that, we have already processed a later token */
			/* The token is also too old to know if it's a duplicate */
			if remoteSeqNum+1 < localSeqNum {
				if outOfSequenceDetection {
					supplementary |= GSS_S_UNSEQ_TOKEN
				}
				supplementary |= GSS_S_OLD_TOKEN
			}

			/* If the remote is greater than the local sequence, we have skipped one or more tokesn */
			if outOfSequenceDetection {
				if remoteSeqNum > localSeqNum {
					supplementary |= GSS_S_GAP_TOKEN // From .NET
				}
			}
		}
	}
	return
}

func tdatIntGetSequenceNumber(tokenHdrV3 []byte) (status TdgssStatus, seqNum uint64) {
	if len(tokenHdrV3) < TDAT_TOKEN_HEADER_LENGTH {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	seqNum = binary.BigEndian.Uint64(tokenHdrV3[8:])
	status.Major = GSS_S_COMPLETE
	return
}

func tdatIntGetQOP(tokenHdr []byte) (status TdgssStatus, qop uint32) {
	if len(tokenHdr) < TDAT_TOKEN_HEADER_LENGTH {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if (tokenHdr[1] != ENCRYPTMSG2) && (tokenHdr[1] != SIGNATUREMSG2) {
		// version 1
		qop = binary.BigEndian.Uint32(tokenHdr[8:12])
	} else {
		// version 3
		qop = uint32(tokenHdr[3])
	}

	status.Major = GSS_S_COMPLETE
	return
}

func tdatWrap(myLogger TdgssLogger, myCI *confInteg, peerCapabilities uint32, confReqFlag int, qopReq uint32, in []byte) (status TdgssStatus, confState int, out []byte) {

	if myLogger.HasTraceLog() {
		myLogger.TraceLog(fmt.Sprintf("> enter tdatWrap"))
		defer func() { myLogger.TraceLog(fmt.Sprintf("< leave tdatWrap")) }()
	}

	if !myCI.protReady {
		status.Major = GSS_S_FAILURE
		status.Minor = TDAT_ERR_CONTEXT_NOT_READY
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if (peerCapabilities & ENHANCEDQOP) != 0 {
		// check for a valid qop index

		if qopReq >= uint32(len(myCI.algQopEx)) {
			status.Major = GSS_S_BAD_QOP
			status.Minor = TDAT_ERR_INVALID_INPUT
			if myLogger.HasDebugLog() {
				myLogger.DebugLog(formatMajorStatusErrOnly(status))
			}
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}
		if myLogger.HasDebugLog() {
			myLogger.DebugLog(fmt.Sprintf("tdatWrap QOP %d %+v", qopReq, myCI.algQopEx[qopReq].legacyQOP))
		}
	} else {
		status.Major = GSS_S_FAILURE
		status.Minor = TDAT_ERR_INVALID_INPUT
		if myLogger.HasDebugLog() {
			myLogger.DebugLog(fmt.Sprintf("tdatWrap legacy QOP not supported"))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	var reqFlags byte = 0
	if confReqFlag != 0 {
		reqFlags |= CONFIDENTIALITY
	}

	var seqNum uint64 = 0
	tokenHdr := []byte{}
	if (peerCapabilities & UNIQUEIVKEYEDMIC) == 0 {
		// Version 1
		status, tokenHdr = tdatIntBuildTokenHdr(
			myLogger,
			VERSION1,
			ENCRYPTMSG,
			CONTMSG,
			NOTAPPLICABLE,
			reqFlags,
			qopReq,
			0,
			0)
		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			if myLogger.HasDebugLog() {
				myLogger.DebugLog(fmt.Sprintf("tdatWrap: tdatIntBuildTokenHdr failed status=%v", status))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}
	} else {
		// Version 3
		seqNum = myCI.mySeqNum
		status, tokenHdr = tdatIntBuildTokenHdr(
			myLogger,
			VERSION3,
			ENCRYPTMSG2,
			CONTMSG,
			UNIQUEIVKEYEDMIC,
			reqFlags,
			qopReq,
			0,
			seqNum)

		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			if myLogger.HasDebugLog() {
				myLogger.DebugLog(fmt.Sprintf("tdatWrap: tdatIntBuildTokenHdr failed status=%v", status))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}
	}

	/*If it supports ASN.1 and new QOPS and new way of encryption */
	if (peerCapabilities & ASN1WRAPTOKEN) != 0 {
		/* After encryption we need to encode */
		var message, hmac, authTag []byte
		status, message, hmac, authTag = tdatGenericNewWrap(myLogger, tokenHdr, &myCI.algQopEx[qopReq].legacyQOP, in)
		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		status, out = tdatCryptoASN1Encode(message, tokenHdr, hmac, authTag)

		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			if myLogger.HasDebugLog() {
				myLogger.DebugLog(fmt.Sprintf("tdatWrap: tdatGenericNewWrap failed status=%v", status))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

	} else {

		status, out = tdatGenericOldWrap(myLogger, tokenHdr, &myCI.algQopEx[qopReq].legacyQOP, in, myCI.algQopEx[qopReq].hashKey, peerCapabilities)

		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			status.Minor = TDAT_ERR_INVALID_INPUT
			if myLogger.HasDebugLog() {
				myLogger.DebugLog(fmt.Sprintf("tdatWrap tdatGenericOldWrap failed=%v", status))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}
	}

	myCI.mySeqNum++

	status, flags := tdatIntGetFlags(tokenHdr)
	if status.Major != GSS_S_COMPLETE {
		status.Major = GSS_S_FAILURE
		if myLogger.HasDebugLog() {
			myLogger.DebugLog(fmt.Sprintf("tdatWrap: tdatIntGetFlags failed status=%v", status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	if (flags & CONFIDENTIALITY) != 0 {
		confState = 1
	}

	status.Major = GSS_S_COMPLETE
	status.Minor = TDAT_S_COMPLETE
	return
}

func tdatUnwrap(myLogger TdgssLogger, myCI *confInteg, peerCapabilities uint32, in []byte) (status TdgssStatus, confState int, qopState uint32, out []byte) {

	if myLogger.HasTraceLog() {
		myLogger.TraceLog(fmt.Sprintf("> enter tdatUnwrap"))
		defer func() { myLogger.TraceLog(fmt.Sprintf("< leave tdatUnwrap")) }()
	}

	if !myCI.protReady {
		status.Major = GSS_S_FAILURE
		status.Minor = TDAT_ERR_CONTEXT_NOT_READY
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if len(in) == 0 {
		status.Major = GSS_S_FAILURE
		status.Minor = TDAT_ERR_INVALID_INPUT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	var message []byte
	var tokenHdr []byte
	var hmac []byte
	var authTag []byte

	if (peerCapabilities & ASN1WRAPTOKEN) != 0 {
		/*Decode the ASN.1 token*/

		status, message, tokenHdr, hmac, authTag = tdatCryptoASN1Decode(in)

		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}
	} else {
		inMsgLength := len(in)
		if inMsgLength < TDAT_TOKEN_HEADER_LENGTH {
			status.Major = GSS_S_FAILURE
			status.Minor = TDAT_ERR_INVALID_INPUT
			if myLogger.HasDebugLog() {
				myLogger.DebugLog(fmt.Sprintf("tdatUnwrap invalid input message length %d", inMsgLength))
			}
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}
		tokenHdr = in[inMsgLength - TDAT_TOKEN_HEADER_LENGTH:]
	}

	/* Get the service information used by the peer in Wrap. */
	status, flags := tdatIntGetFlags(tokenHdr)
	if status.Major != GSS_S_COMPLETE {
		status.Major = GSS_S_FAILURE
		if myLogger.HasDebugLog() {
			myLogger.DebugLog(fmt.Sprintf("tdatUnwrap: tdatIntGetFlags failed status=%v", status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	if (flags & CONFIDENTIALITY) != 0 {
		confState = 1
	}

	status, qop := tdatIntGetQOP(tokenHdr)
	if status.Major != GSS_S_COMPLETE {
		status.Major = GSS_S_FAILURE
		if myLogger.HasDebugLog() {
			myLogger.DebugLog(fmt.Sprintf("tdatUnwrap: tdatIntGetQOP failed %d", status.Major))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	qopState = qop

	/* Check for valid QOP value */
	if (peerCapabilities & ENHANCEDQOP) != 0 {
		// check for a valid qop index

		if int(qop) >= len(myCI.algQopEx) {
			status.Major = GSS_S_BAD_QOP
			status.Minor = TDAT_ERR_INVALID_INPUT
			if myLogger.HasDebugLog() {
				myLogger.DebugLog(formatMajorStatusErrOnly(status))
			}
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}
		if myLogger.HasDebugLog() {
			myLogger.DebugLog(fmt.Sprintf("tdatUnwrap QOP %d %+v", qop, myCI.algQopEx[qop].legacyQOP))
		}
	} else {
		status.Major = GSS_S_FAILURE
		status.Minor = TDAT_ERR_INVALID_INPUT
		if myLogger.HasDebugLog() {
			myLogger.DebugLog(fmt.Sprintf("tdatUnwrap legacy QOP not supported"))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if (peerCapabilities & ASN1WRAPTOKEN) != 0 {
		/*It supports new way of decryption and new QOPS */
		var plaintext []byte
		status, plaintext = tdatGenericNewUnwrap(myLogger, tokenHdr, &myCI.algQopEx[qop].legacyQOP, message, hmac, authTag)
		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			if myLogger.HasDebugLog() {
				myLogger.DebugLog(fmt.Sprintf("tdatUnwrap: tdatGenericNewUnwrap failed status=%v", status))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}
		out = plaintext

	} else {

		status, out = tdatGenericOldUnwrap(myLogger,  &myCI.algQopEx[qop].legacyQOP, in, myCI.algQopEx[qop].hashKey, peerCapabilities)
		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			status.Minor = TDAT_ERR_INVALID_INPUT
			if myLogger.HasDebugLog() {
				myLogger.DebugLog(fmt.Sprintf("tdatWrap tdatGenericOldUnwrap failed=%v", status))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}
	}

	var supplementary uint32 = 0
	if (peerCapabilities & UNIQUEIVKEYEDMIC) != 0 {
		/* DR155352: Compare local/remote token sequence numbers and set Supplementary
		* Status bits accordingly for OR'ing into the Major Status. Bits possibly set:
		* GSS_S_DUPLICATE_TOKEN, GSS_S_OLD_TOKEN, GSS_S_UNSEQ_TOKEN, GSS_S_GAP_TOKEN
		 */
		var localSeqNum uint64
		status, localSeqNum = tdatIntGetSequenceNumber(tokenHdr)
		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			if myLogger.HasDebugLog() {
				myLogger.DebugLog(fmt.Sprintf("gsspUnwrap: tdatIntGetSequenceNumber failed %d", status.Major))
			}
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}
		supplementary = tdatIntCheckTokenSequence(myCI.peerSeqNum, localSeqNum, myCI.replay, myCI.sequence)
	}

	myCI.peerSeqNum++
	status.Major = GSS_S_COMPLETE | supplementary
	status.Minor = TDAT_S_COMPLETE
	return
}

func findCloseQuote(input string, pastQuote int) int {

	for {
		nextQuote := strings.Index(input[pastQuote : ], `"`)
		if nextQuote < 0 {
			return -1
		}

		nextQuote += pastQuote

		// stop if the found double-quote is at end of the input or is followed by something other than a double-quote
		if nextQuote == len(input) - 1 || input[nextQuote + 1] != '"' {
			return nextQuote
		}

		// otherwise the found double-quote is followed by another double-quote
		pastQuote = nextQuote + 2
	}
}


// Parse the logdata string. The logdata string has the following format:
// username@domain@@password user=username profile=myprofile
//
// Example logdata strings.
//     username@@password user=username profile=myprofile
//     username user=username profile=myprofile
//     username
//     username@domain user=username profile=myprofile
//     username@domain
//     username@domain@@password user=username
//     username@domain@@password profile=myprofile
//
// The extraKeys contains an array of keys(strings) to check for valid keys in the extra data:
//     user=username profile=myprofile
// The values for the keys are returned in the mapExtra
//
func tdatParseLogData(logger TdgssLogger, input string, extraKeys []string) (status TdgssStatus, user string, domain string, password string, mapExtra map[string]string) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter tdatParseLogData"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave tdatParseLogData")) }()
	}

	input = strings.TrimSpace(input)

	// If the string starts with a double quote, look for the closing double quote while skipping over two double quotes.
	if strings.HasPrefix(input, `"`) {

		iCloseQuote := findCloseQuote(input, 1)
		if iCloseQuote < 0 {
			if logger.HasDebugLog() {
				logger.DebugLog("Initial token containing User Principal Name (UPN) and password was started but not terminated by a double-quote character")
			}
			status.Major = GSS_S_FAILURE
			status.Minor = TDAT_INVALID_ARGUMENT
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		user = strings.ReplaceAll(input[1:iCloseQuote], `""`, `"`)
		input = strings.TrimSpace(input[iCloseQuote + 1: ])

	} else { // input does not start with double-quote

		startsWithKey := false

		for _, key := range(extraKeys) {
			if strings.HasPrefix(input, key + "=") {
				startsWithKey = true
				break
			}
		}

		if !startsWithKey {
			iSpace := strings.Index(input, " ")
			if iSpace < 0 {
				user = input
				input = ""
			} else {
				user = input[ :iSpace]
				input = strings.TrimSpace(input[iSpace: ])
			}
		}
	}

	// Find the password
	iDoubleAmp := strings.Index(user, "@@")
	if iDoubleAmp >= 0 {
		escPass := user[iDoubleAmp + 2: ]
		user = user[ :iDoubleAmp]

		// Unescape \b \n \t \@ \/ \\ in the password.
		for i := 0 ; i < len(escPass) ; {

			remaining := len(escPass) - i
			switch {
				case remaining >= 2 && escPass[i:i + 2] == `\b` : password += "\b"                  ; i += 2
				case remaining >= 2 && escPass[i:i + 2] == `\n` : password += "\n"                  ; i += 2
				case remaining >= 2 && escPass[i:i + 2] == `\t` : password += "\t"                  ; i += 2
				case remaining >= 2 && escPass[i:i + 1] == `\`  : password += escPass [i + 1:i + 2] ; i += 2
				default                                         : password += escPass [i    :i + 1] ; i++
			}
		}
	}

	iAmp := strings.Index(user, "@")
	if iAmp >= 0 {
		domain = user[iAmp + 1: ]
		user = user[ :iAmp]
	}

	mapExtra = map[string]string {}

	for input != "" {
		bKnownKey := false

		for _, key := range(extraKeys) {

			sPrefix := key + `="`
			if strings.HasPrefix(input, sPrefix) {
				iCloseQuote := findCloseQuote(input, len(sPrefix))
				if iCloseQuote < 0 {
					if logger.HasDebugLog() {
						logger.DebugLog(fmt.Sprintf("Property %s value was started but not terminated by a double-quote character", key))
					}
					status.Major = GSS_S_FAILURE
					status.Minor = TDAT_INVALID_ARGUMENT
					status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
					return
				}

				mapExtra[key] = strings.ReplaceAll(input[len(sPrefix):iCloseQuote], `""`, `"`)
				input = strings.TrimSpace(input[iCloseQuote + 1: ])

				bKnownKey = true
				break

			} else {
				sPrefix = key + "="
				if strings.HasPrefix(input, sPrefix) {
					iSpace := strings.Index(input, " ")
					if iSpace < 0 {
						mapExtra[key] = input[len(sPrefix): ]
						input = ""
					} else {
						mapExtra[key] = input[len(sPrefix):iSpace]
						input = strings.TrimSpace(input[iSpace: ])
					}

					bKnownKey = true
					break
				}
			}
		}

		if !bKnownKey {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("Unexpected property %s", input))
			}
			status.Major = GSS_S_FAILURE
			status.Minor = TDAT_LDAP_ERR_UNKNOWN_PROPERTY
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}
	}
	status.Major = GSS_S_COMPLETE
	return
}
