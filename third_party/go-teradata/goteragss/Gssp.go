// Copyright 2021 by Teradata Corporation. All Rights Reserved.
package goteragss

import (
	"encoding/asn1"
	"fmt"
)

const TDGSS_C_FLAGOFFSET = 31
const GSS_C_INDEFINITE = 0xffffffff

const GSS_C_CALLING_ERROR_OFFSET = 24
const GSS_C_ROUTINE_ERROR_OFFSET = 16

/* Flag bits for context-level services. */
const GSS_C_DELEG_FLAG = 1
const GSS_C_MUTUAL_FLAG = 2
const GSS_C_REPLAY_FLAG = 4
const GSS_C_SEQUENCE_FLAG = 8
const GSS_C_CONF_FLAG = 16
const GSS_C_INTEG_FLAG = 32
const GSS_C_ANON_FLAG = 64
const GSS_C_PROT_READY_FLAG = 128
const GSS_C_TRANS_FLAG = 256

/*
	* GSS_C_* flag bits for context-level services (continued from above).
	* We'll add our own flags on the side opposite of the GSS_C_* flags in
	* the 32-bit word as to prevent any collisions in the event GSS-API adds
	* a new flag in the future.
	*
	* Note that we will grow our flag values towards the middle of the 32-bit word.
	* For example, the next flags added to this end of the word will have its values set
	* as: (uint32 (1) << (TDGSS_C_FLAG_OFFSET - 1)), (uint32 (1) << (TDGSS_C_FLAG_OFFSET - 2)), etc.
*/

const TDGSS_C_CI_BYPASS_FLAG = (uint32 (1) << (TDGSS_C_FLAGOFFSET - 0))

/* GSS Error Codes */

const GSS_C_NO_NAME = ""

const (
	GSS_C_BOTH = iota
	GSS_C_INITIATE
	GSS_C_ACCEPT
)

/* GSS OIDs */
var (
	GSS_C_NT_TDAT_MECHDATA       = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 191, 1, 1012, 1, 2, 2}
	GSS_C_NT_TDAT_MECHDATA_UTF8  = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 191, 1, 1012, 1, 2, 3}
	GSS_C_NT_TDAT_MECHDATA_UTF16 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 191, 1, 1012, 1, 2, 4}
)

const UNKNOWNSTYLE = 0xff
const SSPISTYLE = 1
const GSSSTYLE = 2

type gssp interface {
	gsspConfigure() (status TdgssStatus)
	gsspInitSecContext(targetName string, in []byte) (status TdgssStatus, retFlags uint32, timeRec uint32, needTrailer bool, out []byte)
	gsspWrap(confReqFlag int, qopReq uint32, in []byte) (status TdgssStatus, confState int, out []byte)
	gsspUnwrap(in []byte) (status TdgssStatus, confState int, qopState uint32, out []byte)
	gsspAcquireCred(userLgnMechName string, credUsage int, desiredName string) (status TdgssStatus, timeRec uint32)
	gsspReleaseCred() (status TdgssStatus)
	gsspDeleteContext() (status TdgssStatus)
	gsspGetMechInfo() (mechInfo *mechanismInfo)
	gsspIsEstablished() (bool)
}

func newSecContext(logger TdgssLogger, mi *mechanismInfo) (status TdgssStatus, secCtx gssp) {
	if mi.mechName == gTd2MechName {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("newSecContext() supported mechanism %s", mi.mechName))
		}
		secCtx = newTd2Mech(logger, mi)
		status.Major = GSS_S_COMPLETE
		return
	} else if mi.mechName == gLdapMechName {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("newSecContext() supported mechanism %s", mi.mechName))
		}
		secCtx = newLdapMech(logger, mi)
		status.Major = GSS_S_COMPLETE
		return
	} else if mi.mechName == gJwtMechName {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("newSecContext() supported mechanism %s", mi.mechName))
		}
		secCtx = newJwtMech(logger, mi)
		status.Major = GSS_S_COMPLETE
		return
	} else if mi.mechName == gKrb5MechName {
		secCtx = newKrb5Mech(logger, mi)
		if secCtx == nil { // KRB5 mechanism not compiled in (requires -tags krb5)
			status.Major = GSS_S_BAD_MECH
			status.Minor = TDGSS_ERR_BAD_MECH
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("newSecContext() unavailable mechanism %s", mi.mechName))
				logger.DebugLog(formatMajorStatusErrOnly(status))
			}
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("newSecContext() supported mechanism %s", mi.mechName))
		}
		status.Major = GSS_S_COMPLETE
		return
	} else if mi.mechName == gTdnegoMechName {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("newSecContext() supported mechanism %s", mi.mechName))
		}
		secCtx = newTdnegoMech(logger, mi)
		status.Major = GSS_S_COMPLETE
		return
	} else {
		status.Major = GSS_S_BAD_MECH
		status.Minor = TDGSS_ERR_BAD_MECH
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("newSecContext() unsupported mechanism %s", mi.mechName))
			logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
}
