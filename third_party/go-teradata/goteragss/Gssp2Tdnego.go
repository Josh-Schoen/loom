// Copyright 2023 by Teradata Corporation. All Rights Reserved.

package goteragss

import (
	"bytes"
	"encoding/asn1"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	NEG_STATE_INVALID = iota
	NEG_STATE_COMPLETED
	NEG_STATE_INCOMPLETE
	NEG_STATE_REJECT
	NEG_STATE_REQUEST_MIC
	NEG_STATE_INVALID_LAST
)

const (
	TDNEGO_CTX_NEW = iota
	TDNEGO_CTX_SEND_KEYS
	TDNEGO_CTX_COMPUTE_KEY
	TDNEGO_CTX_VERIFY_KEY
	TDNEGO_CTX_SETUP_COMPLETE
)

type tdnegoSecContext struct {
	m_mechInfo            *mechanismInfo
	m_logger              TdgssLogger
	m_cI                  confInteg

	m_nTokenNumber        int
	m_completionStatus    uint8
	m_peerCapabilities    uint32
	m_abyUserProperties   [] byte
	
	m_initToken           *negTokenInit
	m_nNegState           int
	m_nNegStateFromPeer   int
	m_bIsPolicyApplied    bool
	m_initMechHT          *negMechHashTable
	m_respMechHT          *negMechHashTable

	m_sAuthID             string
	m_sProfileID          string
	m_sUserID             string
	m_bIsSSO              bool
	m_selectedCtx         gssp

	m_nLifetimeContext    int
	m_tdnegoCred          tdatCred

	m_nRequestedServices  int16
	m_startTime           time.Time

	m_logBuffer           bytes.Buffer
	m_nInitCall           int
	m_nRecordNumber       int
	m_sVersion            string
	m_logRecord           *tdnegoLogRecord
}

var _ gssp = &tdnegoSecContext{}

func newTdnegoMech (loggerArg TdgssLogger, mi *mechanismInfo) gssp {

	return &tdnegoSecContext {
		m_logger:                      loggerArg,
		m_mechInfo:                    mi,
		m_initMechHT:                  newNegMechHashTable (),
		m_completionStatus:            TDNEGO_CTX_NEW,
		m_nInitCall:                   1,  
		m_nNegState:                   NEG_STATE_INCOMPLETE,
		m_nNegStateFromPeer:           NEG_STATE_INCOMPLETE,
		m_startTime:                   time.Now (),
	}
}

func (secCtx *tdnegoSecContext) String () string { return fmt.Sprintf("tdNegoSecContext:%p", secCtx) }

func (secCtx *tdnegoSecContext) gsspInitSecContext (targetName string, in []byte) (status TdgssStatus, retFlags uint32, timeRec uint32, needTrailer bool, out []byte) {

	if secCtx.m_logger.HasTraceLog () {
		secCtx.m_logger.TraceLog (fmt.Sprintf ("> enter gsspInitSecContext %v", secCtx))
		defer func() { secCtx.m_logger.TraceLog (fmt.Sprintf ("< leave gsspInitSecContext %v", secCtx)) }()
	}

	if secCtx.m_logger.HasDebugLog () {
		secCtx.m_logger.DebugLog (fmt.Sprintf ("Target Name in gsspInitSecContext %s", targetName))
	}

	if (secCtx.m_completionStatus == TDNEGO_CTX_NEW) {

		// Initially, the input token should have length zero.
		if len (in) != 0 {
			status.Major = GSS_S_DEFECTIVE_TOKEN
			status.Minor = SSPI_ERR_INVALID_TOKEN
			status.Err = tdgssError (nil, formatTdgssStatusErrOnly (status))
			return
		}

		// build init token
		if secCtx.m_logger.HasDebugLog () {
			secCtx.m_logger.DebugLog ("Establishing TDNEGO context")
		}

		status = secCtx.tdnegoIntInitializeData ()
		if status.Major != GSS_S_COMPLETE { return }

		status, out = secCtx.buildInitToken (in, 0, len (in), targetName)
		if status.Major != GSS_S_COMPLETE { return }
	
		status.Major = GSS_S_CONTINUE_NEEDED
		status.Minor = TDNEGO_S_CONTINUE_NEEDED
		secCtx.m_completionStatus = TDNEGO_CTX_SEND_KEYS

		needTrailer = true

	} else {
		// build response token
		status, out = secCtx.buildRespToken (in, targetName)
	}

	if status.Major == GSS_S_COMPLETE {
		status.Minor = TDNEGO_S_COMPLETE
		secCtx.m_completionStatus = TDNEGO_CTX_SETUP_COMPLETE
		secCtx.m_cI.protReady = true
	}

	secCtx.m_nTokenNumber ++
	if secCtx.m_logger.HasDebugLog () {
		secCtx.m_logger.DebugLog (fmt.Sprintf ("Leaving after incrementing msgsequence to %v", secCtx.m_nTokenNumber))
	}
	secCtx.m_nInitCall++

	return

} // end gsspInitSecContext

func (secCtx *tdnegoSecContext) gsspConfigure () (status TdgssStatus) {

	if secCtx.m_logger.HasTraceLog () {
		secCtx.m_logger.TraceLog (fmt.Sprintf ("> enter gsspConfigure %v", secCtx))
		defer func() { secCtx.m_logger.TraceLog (fmt.Sprintf("< leave gsspConfigure %v", secCtx)) }()
	}

	status.Major = GSS_S_COMPLETE
	return
} // end gsspConfigure

func (secCtx *tdnegoSecContext) gsspWrap (confReqFlag int, qopReq uint32, abyDesiredName []byte) (status TdgssStatus, confState int, out []byte) { 

	if ! secCtx.m_cI.protReady || secCtx.m_selectedCtx == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = TDNEGO_ERR_NO_CONTEXT
		status.Err = tdgssError (status.Err, formatTdgssStatusErrOnly (status))
		if secCtx.m_logger.HasDebugLog () {
			secCtx.m_logger.DebugLog (formatMajorStatusErrOnly (status))
		}
		return
	}

	return secCtx.m_selectedCtx.gsspWrap (confReqFlag, qopReq, abyDesiredName)
} // end gsspWrap

func (secCtx *tdnegoSecContext) gsspUnwrap (abyIn []byte) (status TdgssStatus, confState int, qopState uint32, out []byte) { 
	
	if ! secCtx.m_cI.protReady || secCtx.m_selectedCtx == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = TDNEGO_ERR_NO_CONTEXT
		status.Err = tdgssError (nil, formatTdgssStatusErrOnly (status)) // ???
		if secCtx.m_logger.HasDebugLog () {
			secCtx.m_logger.DebugLog (formatMinorStatusErrOnly (status))
		}
		return
	}

	return secCtx.m_selectedCtx.gsspUnwrap (abyIn)
} // end gsspUnwrap

// The credentials passed to TDNEGO are really meant for the underlying mechanism that will be 
// negotiated. So TDNEGO gsspAcquireCred will simply store a copy of the input parameters. 
func (secCtx *tdnegoSecContext) gsspAcquireCred (userLgnMechName string, credUsage int, desiredName string) (status TdgssStatus, timeRec uint32) { 
	
	if secCtx.m_logger.HasTraceLog () {
		secCtx.m_logger.TraceLog (fmt.Sprintf ("> enter gsspAcquireCred for TDNEGO %v", secCtx))
		defer func() { secCtx.m_logger.TraceLog (fmt.Sprintf ("< leave gsspAcquireCred for TDNEGO %v", secCtx)) }()
	}

	status.Major = GSS_S_COMPLETE
	status.Minor = GSS_S_COMPLETE

	if credUsage != GSS_C_BOTH && credUsage != GSS_C_INITIATE && credUsage != GSS_C_ACCEPT {
		status.Major = GSS_S_FAILURE
		status.Minor = TDNEGO_ERR_INVALID_INPUT
		status.Err = tdgssError (nil, formatTdgssStatus (status))
		return
	}

	secCtx.tdnegoIntProcessNames (desiredName)
	secCtx.m_tdnegoCred.principalName = desiredName
	secCtx.m_tdnegoCred.credUsage = credUsage
	secCtx.m_tdnegoCred.lifeTime = GSS_C_INDEFINITE
	timeRec = GSS_C_INDEFINITE

	return
} // end gsspAcquireCred

func (secCtx *tdnegoSecContext) gsspReleaseCred () (status TdgssStatus) {

	if secCtx.m_logger.HasTraceLog () {
		secCtx.m_logger.TraceLog (fmt.Sprintf ("> enter gsspReleaseCred %v", secCtx))
		defer func() { secCtx.m_logger.TraceLog (fmt.Sprintf ("< leave gsspReleaseCred %v", secCtx)) }()
	}
	status.Major = GSS_S_COMPLETE

	if  secCtx.m_cI.protReady && secCtx.m_selectedCtx != nil {
		return secCtx.m_selectedCtx.gsspReleaseCred ()
	}
	return 
} // end gsspReleaseCred

func (secCtx *tdnegoSecContext) gsspDeleteContext () (status TdgssStatus) {

	if secCtx.m_logger.HasTraceLog () {
		secCtx.m_logger.TraceLog (fmt.Sprintf ("> enter gsspDeleteContext %v", secCtx))
		defer func() { secCtx.m_logger.TraceLog (fmt.Sprintf ("< leave gsspDeleteContext %v", secCtx)) }()
	}
	status.Major = GSS_S_COMPLETE

	if secCtx.m_cI.protReady && secCtx.m_selectedCtx != nil {
		return secCtx.m_selectedCtx.gsspDeleteContext ()
	}
	return 
} // end gsspDeleteContext

func (secCtx *tdnegoSecContext) gsspGetMechInfo () (mechInfo *mechanismInfo) {
	return secCtx.m_mechInfo
}

func (secCtx *tdnegoSecContext) buildRespToken (abyInBuf [] byte, sTargetName string) (status TdgssStatus, abyOutBuf []byte) {

	if len (abyInBuf) == 0 {
		status.Major = GSS_S_FAILURE
		status.Minor = TD2_ERR_INVALID_ARGUMENT
		status.Err = tdgssError (nil, formatTdgssStatusErrOnly (status))
		if secCtx.m_logger.HasDebugLog () {
			secCtx.m_logger.DebugLog (fmt.Sprintf ("gsspInitSecContext Bad input data!"))
		}
		return
	}

	respToken := newNegTokenResp ()
	status = respToken.decodeNegTokenResp (abyInBuf)

	if status.Major != GSS_S_COMPLETE {
		return
	}

	secCtx.m_respMechHT = respToken.getNegMechHashTable ()
	if secCtx.m_respMechHT.size () == 0 {
		status.Major =  GSS_S_FAILURE
		status.Minor = TDNEGO_ERR_NEGMECHLIST_EMPTY
		return
	}

	// Get the negState and assign to local
	secCtx.m_nNegStateFromPeer = int (respToken.getNegState ())
	secCtx.m_bIsPolicyApplied = respToken.getNegPolicyApplied ()

	var selectedMechOid asn1.ObjectIdentifier
	if respToken.isNegMechSelected () {
		selectedMechOid = respToken.m_negMechSelected
	}

	
	// Initial available client mechanisms list. We have to get the context
	// object of mechanism only from this initToken object.
	
	clientMechHT := secCtx.m_initToken.getNegMechHashTable ()
	secCtx.m_respMechHT.m_negMechHashTable.Range (func (key, value any) bool {

		mechResp := value.(*negMech)
		secCtx.m_nRecordNumber ++
		secCtx.m_logRecord = newTdnegoLogRecord (
			mechResp, 
			"InitSecContext()", 
			secCtx.m_nInitCall, 
			secCtx.m_nRecordNumber,
			"BuildRespToken(): Mechanism Data sent by server",
			secCtx.m_bIsPolicyApplied,
			secCtx.m_nNegState,
			secCtx.m_nNegStateFromPeer,
			secCtx.m_startTime,
		)

		if secCtx.m_logger.HasDebugLog () {
			secCtx.m_logger.DebugLog (secCtx.m_logRecord.String ())
		}
		secCtx.m_logBuffer.WriteString (secCtx.m_logRecord.String ())

		return true
	})

	// This means the tdnego context is established. Store the established
	// mechanism's context in tdnego context

	if secCtx.m_nNegStateFromPeer == NEG_STATE_COMPLETED && selectedMechOid != nil {

		clientMech := clientMechHT.get (selectedMechOid)

		// we can get context information only from initToken mech object.
		if clientMech != nil {
			if secCtx.m_logger.HasDebugLog () {
				secCtx.m_logger.DebugLog ("Negotiation context established")
			}

			// This context is used for wrap and unwrap
			secCtx.m_selectedCtx = clientMech.getContext ()
			if (!secCtx.m_selectedCtx.gsspIsEstablished ()) {
				selectedNegMech := secCtx.m_respMechHT.get (selectedMechOid)

				if selectedNegMech != nil && len (selectedNegMech.getMechToken ()) != 0 {
					status, _ /*retFlags*/, _/*timeRec*/, _/*bNeedTrailer*/, abyOutBuf = secCtx.m_selectedCtx.gsspInitSecContext (sTargetName, selectedNegMech.getMechToken ())
					if status.Major != GSS_S_COMPLETE { return }
				}

			} // isEstablished

		} // if clientMech != nil

		clientMech.setContextEstablished (true)
		secCtx.m_nNegState = NEG_STATE_COMPLETED
		secCtx.m_nRecordNumber++
		secCtx.m_logRecord = newTdnegoLogRecord (
			clientMech, 
			"InitSecContext()", 
			secCtx.m_nInitCall, 
			secCtx.m_nRecordNumber,
			"BuildRespToken() Negotiation State set to Completed due to Server selection",
			secCtx.m_bIsPolicyApplied,
			secCtx.m_nNegState,
			secCtx.m_nNegStateFromPeer,
			secCtx.m_startTime,
		)

		secCtx.m_logBuffer.WriteString (secCtx.m_logRecord.String ())
		if secCtx.m_logger.HasDebugLog () {
			secCtx.m_logger.DebugLog (secCtx.m_logBuffer.String ())
		}

		return
	
	} // end if nNegStateFromPeer == NEG_STATE_COMPLETED

	// This means that tdnego context establishment failed
	if (secCtx.m_nNegStateFromPeer == NEG_STATE_REJECT) {

		if secCtx.m_logger.HasDebugLog () {
			secCtx.m_logger.DebugLog ("Negotiation context establishment failed")
			secCtx.m_logger.DebugLog (secCtx.m_logBuffer.String ())
		}

		status.Major = TDNEGO_ERR_NO_CONTEXT
		status.Minor = TDNEGO_ERR_NO_MECH_SELECTED
		return
	}

	// This means that context establishment is in progress. Always update clientToken
	if (secCtx.m_nNegStateFromPeer == NEG_STATE_INCOMPLETE) {
		status.Major = GSS_S_CONTINUE_NEEDED
		secCtx.m_respMechHT.m_negMechHashTable.Range (func (key, value any) bool {
			secCtx.formRespMechHT (value.(*negMech), sTargetName)
			return true
		})
	}

	// Frame the token and send to peer
	clientTokenToPeer := newNegTokenResp ()
	clientTokenToPeer.setNegState (NEG_STATE_INCOMPLETE)
	if clientMechHT.size () > 0 {
		clientTokenToPeer.setNegMechHashTable (clientMechHT)
		abyOutBuf = clientTokenToPeer.encodeNegTokenResp ()
	}

	return

} // end buildRespToken

func (secCtx *tdnegoSecContext) formRespMechHT (respMech *negMech, sTargetName string) {
	clientMech := secCtx.m_initToken.getNegMechHashTable ().get (respMech.getMechType ())
	if clientMech != nil {
		if respMech.getNegMechState () == NEG_MECH_STATE_AVAILABLE {

			if secCtx.m_logger.HasDebugLog () {
				secCtx.m_logger.DebugLog (fmt.Sprintf ("gsspInitSecContext: %v", secCtx.m_nTokenNumber + 1))
			}

			abyMechTokenIn := respMech.getMechToken () 
			if len (abyMechTokenIn) > 0 {
				mechCtx := clientMech.getContext ()

				status, _ /*retFlags*/, _/*timeRec*/, _/*bNeedTrailer*/, abyMechTokenOut := mechCtx.gsspInitSecContext (sTargetName, abyMechTokenIn)
				if status.Major != GSS_S_FAILURE {
					clientMech.setMechToken (abyMechTokenOut)
				} else {
					clientMech.setNegMechReason (NEG_MECH_REASON_GSSAPI_ERROR)
					clientMech.setMajorStatus (status.Major)
					clientMech.setMinorStatus (status.Minor)
					if status.Err != nil { clientMech.setMinorText (getFirstLine (status.Err.Error ())) }

					if secCtx.m_logger.HasDebugLog () {
						secCtx.m_logger.DebugLog (fmt.Sprintf ("gsspInitSecContext:1 falied for %v", clientMech.getContext ().gsspGetMechInfo ().mechName))
					}
		
				}
			} // end if length of abyMechTokenIn > 0
	
		} else { // end of if NEG_MECH_STATE_AVAILABLE

			clientMech.setNegMechState (NEG_MECH_STATE_REJECTED)
			clientMech.setNegMechReason (NEG_MECH_REASON_NOT_AVAIL_AT_SERVER)
		}
	}

	return
} // end formRespMechHT

func (secCtx *tdnegoSecContext) buildInitToken (abyInBuf [] byte, nOffset int, nLength int, sTargetName string) (status TdgssStatus, abyOutBuf []byte) {

	if secCtx.m_initMechHT.size () == 0 { // == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = TDNEGO_ERR_NO_MECHS_AVAILABLE
		status.Err = tdgssError (nil, formatMinorStatusErrOnly (status)) // Not sure
		if secCtx.m_logger.HasDebugLog () {
			secCtx.m_logger.DebugLog ("No mechanism is available for negotiation")
		}
		return
	}

	secCtx.m_initMechHT.m_negMechHashTable.Range (func (key, value any) bool {
		bIsMechErr := false
		var abyOutToken [] byte
		negMech := value.(*negMech)

		if  negMech.m_nMechState == NEG_MECH_STATE_AVAILABLE {
			mechCtx := negMech.getContext ()
			if mechCtx != nil {

				if secCtx.m_logger.HasDebugLog () {
					secCtx.m_logger.DebugLog ("InitContext:1 called for " + mechCtx.gsspGetMechInfo ().mechName)
				}

				status, _ /*retFlags*/, _ /*timeRec uint32*/, _ /*needTrailer*/, abyOutToken = mechCtx.gsspInitSecContext (sTargetName, abyInBuf)
				if status.Major != GSS_S_CONTINUE_NEEDED && status.Major != GSS_S_COMPLETE {
					bIsMechErr = true
					if secCtx.m_logger.HasDebugLog () {
						secCtx.m_logger.DebugLog ("InitSecContext:1 failed for " + mechCtx.gsspGetMechInfo ().mechName + ". It was rejected because it failed to generate a token. Error is " + getFirstLine (status.Err.Error ()))
					}

					negMech.setNegMechState (NEG_MECH_STATE_REJECTED)
					negMech.setNegMechReason (NEG_MECH_REASON_GSSAPI_ERROR)
					negMech.setMajorStatus (status.Major)
					negMech.setMinorStatus (status.Minor)
					if status.Err != nil { negMech.setMinorText (getFirstLine (status.Err.Error ())) }

					secCtx.m_nRecordNumber++
					secCtx.m_logRecord = newTdnegoLogRecord (
						negMech, 
						"InitSecContext()", 
						secCtx.m_nInitCall, 
						secCtx.m_nRecordNumber,
						"BuildInitToken() Mechanism is rejected because it failed to generate a token",
						secCtx.m_bIsPolicyApplied,
						secCtx.m_nNegState,
						secCtx.m_nNegStateFromPeer,
						secCtx.m_startTime,
					)
			
					if secCtx.m_logger.HasDebugLog () {
						secCtx.m_logger.DebugLog (secCtx.m_logRecord.String ())
					}
					secCtx.m_logBuffer.WriteString (secCtx.m_logRecord.String ())
			
				} else { // Success

					if secCtx.m_bIsSSO {
						negMech.setSsoAuthID (secCtx.m_sAuthID)
					}
				}

			} // end if mechCtx != nil

			if !bIsMechErr {
				negMech.setMechToken (abyOutToken)
				secCtx.m_nRecordNumber++
				secCtx.m_logRecord = newTdnegoLogRecord (
					negMech, 
					"InitSecContext()", 
					secCtx.m_nInitCall, 
					secCtx.m_nRecordNumber,
					"BuildInitToken() Continue needed for mechanism to establish context",
					secCtx.m_bIsPolicyApplied,
					secCtx.m_nNegState,
					secCtx.m_nNegStateFromPeer,
					secCtx.m_startTime,
				)
		
				secCtx.m_logBuffer.WriteString (secCtx.m_logRecord.String ())
				if secCtx.m_logger.HasDebugLog () {
					secCtx.m_logger.DebugLog (secCtx.m_logRecord.String ())
				}
			}
		} // end if negMech.m_nMechState == Gssp2TdnegoNegMech.NEG_MECH_STATE_AVAILABLE

		return true

	})  // end looping through available Neg mechs

	nNoMechs := 0
	// Loop through init mech hashtable to count number of mechanisms that are rejected
	// to make sure we still have at least one mechanism left
	secCtx.m_initMechHT.m_negMechHashTable.Range (func (key, value any) bool {
		negMech := value.(*negMech)

		if negMech.getNegMechState () == NEG_MECH_STATE_REJECTED { nNoMechs ++ }
		return true
	})

	if secCtx.m_initMechHT.size () == nNoMechs {

		status.Major = GSS_S_FAILURE
		status.Minor = TDNEGO_ERR_NO_MECHS_AVAILABLE
		status.Err = tdgssError (nil, formatMinorStatusErrOnly (status))
		if secCtx.m_logger.HasDebugLog () {
			secCtx.m_logger.DebugLog ("No mechanism is available for negotiation")
		}
		return
	}

	secCtx.m_initToken = newNegTokenInit ()
	secCtx.m_initToken.setNegState (secCtx.m_nNegState)
	secCtx.m_initToken.setNegMechHashTable (secCtx.m_initMechHT)

	if secCtx.m_sAuthID != "" {
		secCtx.m_initToken.setProfileName (secCtx.m_sProfileID)
	}

	if secCtx.m_sUserID != "" {
		secCtx.m_initToken.setUserName (secCtx.m_sUserID)
	}

	secCtx.m_initToken.setAuthenticationID (secCtx.m_sAuthID)

	if secCtx.m_bIsSSO {
		secCtx.m_initToken.setSSO (secCtx.m_bIsSSO)
	}

	abyOutBuf = secCtx.m_initToken.encodeNegTokenInit ()

	if secCtx.m_logger.HasDebugLog () {
		secCtx.m_logger.DebugLog (fmt.Sprintf ("gsspInitSecContext Initial token:\n%v", hex.Dump(abyOutBuf)))
	}

	status.Major = GSS_S_COMPLETE

	return 

} // end buildInitToken

func (secCtx *tdnegoSecContext) gsspIsEstablished () bool {
	return secCtx.m_completionStatus == TDNEGO_CTX_SETUP_COMPLETE
}
