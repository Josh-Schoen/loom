// Copyright 2021 by Teradata Corporation. All Rights Reserved.
package goteragss

import (
)

// Map TDAT Minor Status codes to TD2 Minor Status codes
var td2MinorStatusMap = map[uint32]uint32 {
	TDAT_S_COMPLETE:              TD2_S_COMPLETE,
	TDAT_S_CONTINUE_NEEDED:       TD2_S_CONTINUE_NEEDED,
	TDAT_ERR_INT_FAILURE:         TD2_ERR_INT_FAILURE,
	TDAT_ERR_ALG_NO_CONTEXT:      TD2_ERR_ALG_NO_CONTEXT,
	TDAT_ERR_INVALID_NO_QOPS:     TD2_ERR_INVALID_NO_QOPS,
	TDAT_ERR_LEGACY_QOP_DISABLED: TD2_ERR_LEGACY_QOP_DISABLED,
	TDAT_ERR_INVALID_TOKEN:       TD2_ERR_INVALID_TOKEN,
	TDAT_ERR_CONTEXT_NOT_READY:   TD2_ERR_CONTEXT_NOT_READY,
	TDAT_ERR_INVALID_INPUT:       TD2_ERR_INVALID_INPUT,
	TDAT_ERR_CONF_INTEG_NOT_AVAILABLE: TD2_ERR_CONF_INTEG_NOT_AVAILABLE,
}

func getTd2MinorStatus(tdatMinorStatus uint32) (td2MinorStatus uint32) {
	td2MinorStatus = td2MinorStatusMap[tdatMinorStatus]
	if td2MinorStatus == 0 {
		td2MinorStatus = tdatMinorStatus;
	}
	return
}

func td2IntClientReturns(secCtx *td2SecContext, status TdgssStatus) (retFlags uint32, timeRec uint32) {

	timeRec = GSS_C_INDEFINITE

	if status.Minor == TD2_S_CONTINUE_NEEDED {
		retFlags = func () uint32 { if secCtx.bCIBypass { return TDGSS_C_CI_BYPASS_FLAG } ; return GSS_C_INTEG_FLAG | GSS_C_CONF_FLAG }()
	} else if status.Minor == TD2_S_COMPLETE {
		retFlags = func () uint32 { if secCtx.bCIBypass { return TDGSS_C_CI_BYPASS_FLAG | GSS_C_TRANS_FLAG } ;
				return GSS_C_INTEG_FLAG | GSS_C_CONF_FLAG | GSS_C_PROT_READY_FLAG | GSS_C_TRANS_FLAG
		} ()
	}

	/* if the Peer supports it, set the REPLAY and SEQUENCE flags */
	if (secCtx.peerCapabilities & UNIQUEIVKEYEDMIC) != 0 {
		if secCtx.cI.replay { retFlags |= GSS_C_REPLAY_FLAG }
		if secCtx.cI.sequence { retFlags |= GSS_C_SEQUENCE_FLAG }
	}
	return
}
