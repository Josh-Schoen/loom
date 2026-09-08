
// Copyright 2022 by Teradata Corporation. All Rights Reserved.

//go:build !windows && !wasm && krb5

package goteragss

import (
	"fmt"
	"strings"
	"encoding/binary"
	"bytes"
)

const BIT0 byte = 0x1
const BIT1 byte = 0x2
const BIT2 byte = 0x4
const BIT3 byte = 0x8

const GSS_C_QOP_DEFAULT = 0

const GSS_TOKEN_HEADER_LENGTH = 16

type gssV2TokenHeader struct {
	Version         byte
	MsgType         byte
	ByteVar         byte
	ClientOrServer  byte
	MsgLength       uint32
	Flags           byte
	Capabilities    uint32
	ResForExp       [3]byte
}

var abyKRB5OID = []byte{'\x2a', '\x86', '\x48', '\x86', '\xf7', '\x12', '\x01', '\x02', '\x02'}
var abyPrincipalNameOID = []byte{'\x2a', '\x86', '\x48', '\x86', '\xf7', '\x12', '\x01', '\x02', '\x02', '\x01'}

func gssIntBuildTokenHdr(logger TdgssLogger, msgType byte, msgSeq byte, confReqFlag int, tokenLength uint32, fCapabilities uint32) (status TdgssStatus, tokenHdr []byte) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter gssIntBuildTokenHdr"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave gssIntBuildTokenHdr")) }()
	}

	var reqFlags byte = 0

	/* Set up the first bit for SSPI(0) or GSS(1) */
	reqFlags |= BIT0

	/* Set up the second bit for BIG_ENDIEN(1) */
	reqFlags |= BIT1

	/* Used for TD2 style wrap and unwrap */
	if confReqFlag != NOTAPPLICABLE && confReqFlag != 0 {
		/* Both confidentiality and integrity */
		reqFlags |= BIT2
	}

	var capabilities uint32
	if fCapabilities != NOTAPPLICABLE {
		capabilities = fCapabilities
	}

	gssTokenHdr := gssV2TokenHeader{
		Version:          VERSION2,
		MsgType:          msgType,
		ByteVar:          msgSeq,
		ClientOrServer:   CLIENT,
		MsgLength:        tokenLength,
		Flags:            reqFlags,
		Capabilities:     capabilities,
		//ResForExp:      //initialized to zero be default
	}

	var buf bytes.Buffer
	err := binary.Write(&buf, binary.BigEndian, gssTokenHdr)
	if err != nil {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("gssIntBuildTokenHdr: binary.Write failed: %v", err))
		}
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}
	tokenHdr = buf.Bytes()

	status.Major = GSS_S_COMPLETE	
	return
}

func gssIntRetrieveHeader(logger TdgssLogger, in []byte) (status TdgssStatus, tokenHdr gssV2TokenHeader) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter gssIntRetrieveHeader"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave gssIntRetrieveHeader")) }()
	}

	if len(in) < GSS_TOKEN_HEADER_LENGTH {
		status.Major = GSS_S_FAILURE
		status.Minor = GSSP2GSS_ERR_INVALID_INPUT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// De-serialize the V2 token header
	r := bytes.NewReader(in)
	err := binary.Read(r, binary.BigEndian, &tokenHdr)
	if err != nil {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}

	if tokenHdr.Version != VERSION2 {
		status.Major = GSS_S_FAILURE
		status.Minor = GSSP2GSS_ERR_NOT_SUPPORTED_VERSION
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = GSS_S_COMPLETE
	return
}

func gssIntImportGssName(logger TdgssLogger, inputName string, inputOID []byte) (status TdgssStatus, newName *tdgssName) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter gssIntImportGssName"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave gssIntImportGssName")) }()
	}

	if len(inputName) == 0 || len(inputOID) == 0 {
		if logger.HasDebugLog() {
			logger.DebugLog("gssIntImportGssName: invalid inputName or inputOID")
		}
		status.Major = GSS_S_FAILURE
		status.Minor = GSSP2GSS_ERR_INVALID_INPUT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	newOid := NewTdgssOID(inputOID)
	defer newOid.Release()

	newBuffer := NewTdgssBufferFromString(inputName)
	defer newBuffer.Release()

	newName = NewTdgssName()

	var minor uint32

	major := gss_import_name(&minor, newBuffer, newOid, newName)

	if major != GSS_S_COMPLETE {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("gssIntImportGssName: gss_import_name failed. Major = 0x%x Minor = 0x%x", major, minor))
		}
		status.Major = major
		status.Minor = minor
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = GSS_S_COMPLETE
	return
}

func gssIntAcquireCred(logger TdgssLogger, desiredName string, timeReq uint32) (status TdgssStatus, timeRec uint32, newCred *tdgssCredId, extraLogdata string) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter gssIntAcquireCred"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave gssIntAcquireCred")) }()
	}

	var minor uint32

	newName := NewTdgssName()
	defer newName.Release()

	var mapExtra map[string]string

	if len(desiredName) != 0 {

		var user string
		var domain string
		var password string
		status, user, domain, password, mapExtra = tdatParseLogData(logger, desiredName, []string {"user", "profile"})

		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("gssIntAcquireCred: user: %s domain: %s password: %s mapExtra %v", user, domain, password, mapExtra))
		}
		if status.Major != GSS_S_COMPLETE {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("gssIntAcquireCred: tdatParseLogData failed"))
			}
			status.Major = GSS_S_FAILURE
			status.Minor = GSSP2GSS_ERR_INVALID_INPUT
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		if len(user) > 0 {

			principalName := user
			if len(domain) > 0 {
				principalName += "@" + strings.ToUpper(domain)
			}

			newOid := NewTdgssOID(abyPrincipalNameOID)
			defer newOid.Release()

			newBuffer := NewTdgssBufferFromString(principalName)
			defer newBuffer.Release()
		
		
			major := gss_import_name(&minor, newBuffer, newOid, newName)
		
			if major != GSS_S_COMPLETE {
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("gssIntAcquireCred: gss_import_name failed. Major = 0x%x Minor = 0x%x", major, minor))
				}
				status.Major = major
				status.Minor = minor
				status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
				return
			}
		}
	}

	newOidSet := NewTdgssOIDSet(abyKRB5OID)
	defer newOidSet.Release()

	newCred = NewTdgssCredId()

	major := gss_acquire_cred(&minor, 
						newName,
						timeReq,
						newOidSet,
						newCred,
						&timeRec)

	if major != GSS_S_COMPLETE {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("gssIntAcquireCred: gss_acquire_cred failed. Major = 0x%x Minor = 0x%x", major, minor))
		}
		status.Major = major
		status.Minor = minor
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	for key, value := range mapExtra {
		extraLogdata = extraLogdata + key + "=" + value + " "
	}

	status.Major = GSS_S_COMPLETE
	return
}

func gssIntReleaseCred(logger TdgssLogger, credId *tdgssCredId) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter gssIntReleaseCred"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave gssIntReleaseCred")) }()
	}

	if credId != nil {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("GSS gssIntReleaseCred for credId %+v", credId))
		}
		credId.Release()
	}
}

func gssIntWrap(logger TdgssLogger, ctxId *tdgssCtxId, confReqFlag int, inputMsg []byte) (status TdgssStatus, outputMsg []byte, confState int) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter gssIntWrap"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave gssIntWrap")) }()
	}

	var major uint32
	var minor uint32
	
	inputBuffer := NewTdgssBufferFromBytes(inputMsg)
	defer inputBuffer.Release()

	outputBuffer := NewTdgssBuffer(ALLOCTYPE_GSSAPI)
	defer outputBuffer.Release()

	major = gss_wrap(
			&minor,
			ctxId,
			confReqFlag,
			GSS_C_QOP_DEFAULT,
			inputBuffer,
			&confState,
			outputBuffer)

	if major != GSS_S_COMPLETE {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("gssIntWrap: gss_wrap failed. Major = 0x%x Minor = 0x%x", major, minor))
		}
		status.Major = major
		status.Minor = minor
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	outputMsg = outputBuffer.Bytes()
	return
}

func gssIntUnwrap(logger TdgssLogger, ctxId *tdgssCtxId, inputMsg []byte) (status TdgssStatus, outputMsg []byte, confState int) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter gssIntUnwrap"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave gssIntUnwrap")) }()
	}

	var major uint32
	var minor uint32
	
	inputBuffer := NewTdgssBufferFromBytes(inputMsg)
	defer inputBuffer.Release()

	outputBuffer := NewTdgssBuffer(ALLOCTYPE_GSSAPI)
	defer outputBuffer.Release()

	major = gss_unwrap(
		&minor,
		ctxId,
		inputBuffer,
		outputBuffer,
		&confState,
		nil)


	if major != GSS_S_COMPLETE {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("gssIntUnwrap: gss_unwrap failed. Major = 0x%x Minor = 0x%x", major, minor))
		}
		status.Major = major
		status.Minor = minor
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	outputMsg = outputBuffer.Bytes()
	return
}

func gssIntInitSecContext(logger TdgssLogger, credId *tdgssCredId, ctxId *tdgssCtxId, targetName string,
			reqFlags uint32, timeReq uint32, inputToken []byte) (status TdgssStatus, outputToken []byte) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter gssIntInitSecContext"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave gssIntInitSecContext")) }()
	}

	if credId == nil || ctxId == nil {
		if logger.HasDebugLog() {
			logger.DebugLog("gssIntInitSecContext: invalid credId or ctxId")
		}
		status.Major = GSS_S_FAILURE
		status.Minor = GSSP2GSS_ERR_INVALID_INPUT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	var major uint32
	var minor uint32

	var retFlags uint32
	var timeRec uint32

	newOid := NewTdgssOID(abyKRB5OID)
	defer newOid.Release()

	newName := NewTdgssName()
	defer newName.Release()

	if len(targetName) > 0 {

		newPrincipalNameOid := NewTdgssOID(abyPrincipalNameOID)
		defer newPrincipalNameOid.Release()

		newBuffer := NewTdgssBufferFromString(targetName)
		defer newBuffer.Release()
	
	
		major := gss_import_name(&minor, newBuffer, newPrincipalNameOid, newName)
	
		if major != GSS_S_COMPLETE {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("gssIntInitSecContext: gss_import_name failed. Major = 0x%x Minor = 0x%x", major, minor))
			}
			status.Major = major
			status.Minor = minor
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}
	}

	inputBuffer := NewTdgssBufferFromBytes(inputToken)
	defer inputBuffer.Release()

	outputBuffer := NewTdgssBuffer(ALLOCTYPE_GSSAPI)
	defer outputBuffer.Release()

	major = gss_init_sec_context(&minor,
							credId,
							ctxId,
							newName,
							newOid,
							reqFlags,
							timeReq,
							inputBuffer,
							outputBuffer,
							&retFlags,
							&timeRec)

	status.Major = major
	status.Minor = minor

	if status.Major != GSS_S_COMPLETE && status.Major != GSS_S_CONTINUE_NEEDED {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("gssIntInitSecContext: gss_init_sec_context failed. Major = 0x%x Minor = 0x%x", major, minor))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	outputToken = outputBuffer.Bytes()
	return
}

func gssIntDeleteContext(logger TdgssLogger, ctxId *tdgssCtxId) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter gssIntDeleteContext"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave gssIntDeleteContext")) }()
	}

	if ctxId != nil {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("GSS gssIntDeleteContext for ctxId %+v", ctxId))
		}
		ctxId.Release()
	}
}
