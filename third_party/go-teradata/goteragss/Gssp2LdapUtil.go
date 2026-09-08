// Copyright 2021 by Teradata Corporation. All Rights Reserved.
package goteragss

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// Map TDAT Minor Status codes to LDAP Minor Status codes
var ldapMinorStatusMap = map[uint32]uint32 {
	TDAT_S_COMPLETE:              LDAPV3_S_COMPLETE,
	/* LDAP has no minor status for continue needed */
	TDAT_S_CONTINUE_NEEDED:       LDAPV3_S_COMPLETE,
	TDAT_ERR_INT_FAILURE:         LDAPV3_ERR_LDAP,
	/* LDAP has no equivalent minor status for ERR_ALG_NO_CONTEXT */
	TDAT_ERR_ALG_NO_CONTEXT:      LDAPV3_ERR_LDAP,
	TDAT_ERR_INVALID_NO_QOPS:     LDAPV3_ERR_INVALID_NO_QOPS,
	TDAT_ERR_LEGACY_QOP_DISABLED: LDAPV3_ERR_LEGACY_QOP_DISABLED,
	TDAT_ERR_INVALID_TOKEN:       LDAPV3_ERR_INVALID_TOKEN,
	TDAT_ERR_CONTEXT_NOT_READY:   LDAPV3_ERR_CONTEXT_NOT_READY,
	TDAT_ERR_INVALID_INPUT:       LDAPV3_ERR_INVALID_INPUT,
}

func getLdapMinorStatus(tdatMinorStatus uint32) (ldapMinorStatus uint32) {
	ldapMinorStatus = ldapMinorStatusMap[tdatMinorStatus]
	if ldapMinorStatus == 0 {
		ldapMinorStatus = tdatMinorStatus;
	}
	return
}

type ldapProperty struct {
	dataId         uint16
	frameLength    uint16
	userNameLength uint16
	userName       string
}

type ldapPropertyHead struct {
	DataId         uint16
	FrameLength    uint16
	UserNameLength uint16
}

func ldapIntNormalizeProps(logger TdgssLogger, abyInput []byte) (status TdgssStatus, ldapProperties []ldapProperty) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter ldapIntNormalizeProps"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave ldapIntNormalizeProps")) }()
	}

	status.Major = GSS_S_COMPLETE
	var err error
	var ldapPropertyP ldapPropertyHead
	var ldapPropertyItem ldapProperty

	if len(abyInput) == 0 {
		if logger.HasDebugLog() {
			logger.DebugLog("ldapIntNormalizeProps: empty aby provided")
		}
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	r := bytes.NewReader(abyInput)

	for r.Len() > 0 {
		// Read the static information about frame
		err = binary.Read(r, binary.BigEndian, &ldapPropertyP)
		if err != nil {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("Error with reading header of ldap property: %d", err))
			}
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
			return
		}
		ldapPropertyItem.dataId = ldapPropertyP.DataId
		ldapPropertyItem.frameLength = ldapPropertyP.FrameLength
		ldapPropertyItem.userNameLength = ldapPropertyP.UserNameLength

		// Read the username based on UserNameLength
		userName := make([]byte, ldapPropertyItem.userNameLength)
		err = binary.Read(r, binary.BigEndian, &userName)
		if err != nil {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("Error with reading userName of ldap property: %d", err))
			}
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
			return
		}
		ldapPropertyItem.userName = string(userName)

		// Discard remaining frame padding
		_, err = io.CopyN(io.Discard, r, int64(ldapPropertyItem.frameLength-ldapPropertyItem.userNameLength-6))
		if err != nil {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("Error with processing padding of ldap property: %d", err))
			}
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
			return
		}

		// Append new ldap property to slice
		ldapProperties = append(ldapProperties, ldapPropertyItem)
	}

	return
}

func ldapIntClientReturns(logger TdgssLogger, secCtx *ldapSecContext) (retFlags uint32, timeRec uint32) {

	timeRec = GSS_C_INDEFINITE

	if secCtx.cI.conf      { retFlags |= GSS_C_CONF_FLAG }
	if secCtx.cI.integ     { retFlags |= GSS_C_INTEG_FLAG }
	if secCtx.cI.protReady { retFlags |= GSS_C_PROT_READY_FLAG }
	if secCtx.cI.replay    { retFlags |= GSS_C_REPLAY_FLAG }
	if secCtx.cI.sequence  { retFlags |= GSS_C_SEQUENCE_FLAG }
	if secCtx.deleg        { retFlags |= GSS_C_DELEG_FLAG }
	if secCtx.mutual       { retFlags |= GSS_C_MUTUAL_FLAG }
	if secCtx.trans        { retFlags |= GSS_C_TRANS_FLAG }

	return
}
