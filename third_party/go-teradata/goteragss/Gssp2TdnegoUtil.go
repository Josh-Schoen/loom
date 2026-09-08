// Copyright 2023 by Teradata Corporation. All Rights Reserved.
package goteragss

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"
)

func (secCtx *tdnegoSecContext) tdnegoIntInitializeData () (status TdgssStatus) {

	secCtx.m_logBuffer.WriteString ("\nTDNEGO Negotiation Log\n\n\n")
	secCtx.m_logBuffer.WriteString ("Package:  TERAGSSJAVA\n")
	secCtx.m_sVersion = fmt.Sprintf ("%v.%02d.%02d.%02d", GoTeraGSSMajorVersion, GoTeraGSSMinorVersion, GoTeraGSSMaintVersion, GoTeraGSSEfixVersion)
	secCtx.m_logBuffer.WriteString ("version:  " + secCtx.m_sVersion + "\n")
	sHostName, err := os.Hostname ()
	if err != nil {
		if secCtx.m_logger.HasDebugLog () {
			secCtx.m_logger.DebugLog ("os.Hostname () failed")
		}
	}
	secCtx.m_logBuffer.WriteString ("Run on:   " + sHostName + "\n")
	dNow := time.Now ().Format ("Mon Jan 2 15:04:05 MST 2006")
	secCtx.m_logBuffer.WriteString ("Run at:   " + dNow + "\n\n")

	// Initialise each individual mechanism's data.
	status = secCtx.initMechData ()
	if status.Major != GSS_S_COMPLETE { return }

	// If there is no names it means we can try SSO logon only, for now it
	// is only KRB5 SO drop all other non SSO mechanisms.
	if secCtx.m_bIsSSO {
		secCtx.rejectNonSSOmechs ()
	}

	return

} // end tdnegoIntInitializeData

// Used instead of hex.Dump when you don't want that much formatting
func formatBuf (abyInBuf [] byte) string {
	var b bytes.Buffer
	for i, by := range (abyInBuf) {

		b.WriteString (fmt.Sprintf ("0x%02x, ", by))
		if (i + 1) % 16 == 0 { b.WriteString ("\n") }
	}
	b.WriteString ("\n\n")
	return b.String ()
}

func (secCtx *tdnegoSecContext) initMechData () (status TdgssStatus) {
	status.Major = GSS_S_COMPLETE
	status.Minor = TDNEGO_S_COMPLETE

	if len (secCtx.m_mechInfo.negotiatedMechs) == 0 {
		status.Major = GSS_S_DEFECTIVE_TOKEN
		status.Minor = GSSP2GSS_ERR_INVALID_TOKEN
		return
	}

	//loop through commonMechs and create NegMech for each as well as context
	for _, miSub := range secCtx.m_mechInfo.negotiatedMechs {
		sMechName := miSub.mechName
		mechOID := miSub.mechOid
		negMech := newNegMech ()
		negMech.setMechType (mechOID)
		negMech.setNegMechState (NEG_MECH_STATE_AVAILABLE)
		negMech.setNegMechReason (NEG_MECH_REASON_AVAILABLE)

		if len (secCtx.m_sAuthID) == 0 && sMechName == gKrb5MechName {
			secCtx.m_bIsSSO = true
		}

		var mechCtx gssp
		status, mechCtx = newSecContext (secCtx.m_logger, miSub)

		if status.Major != GSS_S_COMPLETE {
			if (secCtx.m_logger.HasDebugLog ()) {
				secCtx.m_logger.DebugLog (fmt.Sprintf ("GetContext failed for mechanism: %v", sMechName))
				secCtx.m_logger.DebugLog (formatMajorStatusErrOnly (status))
			}

			negMech.setNegMechState (NEG_MECH_STATE_REJECTED)
			negMech.setNegMechReason (NEG_MECH_REASON_GSSAPI_ERROR)
			negMech.setMajorStatus (status.Major)
			negMech.setMinorStatus (status.Minor)
			if status.Err != nil { negMech.setMinorText (getFirstLine (status.Err.Error ())) }

			secCtx.m_nRecordNumber ++
			secCtx.m_logRecord = newTdnegoLogRecord (
				negMech,
				"InitSecContext()",
				secCtx.m_nInitCall,
				secCtx.m_nRecordNumber,
				"initMechData(): Mechanism is rejected at client",
				secCtx.m_bIsPolicyApplied,
				secCtx.m_nNegState,
				secCtx.m_nNegStateFromPeer,
				secCtx.m_startTime,
			)

			if secCtx.m_logger.HasDebugLog () {
				secCtx.m_logger.DebugLog (secCtx.m_logRecord.String ())
			}
			secCtx.m_logBuffer.WriteString (secCtx.m_logRecord.String ())

			if secCtx.m_bIsSSO && sMechName == gKrb5MechName {
				status.Major = GSS_S_DEFECTIVE_CREDENTIAL
				status.Minor = TDNEGO_ERR_BAD_CRED_USAGE
				return
			}

			continue
		}

		negMech.m_mechCtx = mechCtx
		negMech.setContextEstablished (false)

		status = mechCtx.gsspConfigure ()
		if status.Major != GSS_S_COMPLETE {
			if (secCtx.m_logger.HasDebugLog ()) {
				secCtx.m_logger.DebugLog (fmt.Sprintf ("gsspConfigure failed for mechanism: %v", sMechName))
				secCtx.m_logger.DebugLog (formatTdgssStatusErrOnly (status))
			}

			negMech.setNegMechState (NEG_MECH_STATE_REJECTED)
			negMech.setNegMechReason (NEG_MECH_REASON_GSSAPI_ERROR)
			negMech.setMajorStatus (status.Major)
			negMech.setMinorStatus (status.Minor)
			negMech.setMinorText (formatTdgssStatusErrOnly (status))

			continue
		}

		// Sets credential for each mechanism
		status, _ /*timeRec*/ = mechCtx.gsspAcquireCred (sMechName, GSS_C_INITIATE, secCtx.m_tdnegoCred.principalName)

		if status.Major != GSS_S_COMPLETE {
			if (secCtx.m_logger.HasDebugLog ()) {
				secCtx.m_logger.DebugLog (fmt.Sprintf ("gsspAcquireCred failed for mechanism: %v", sMechName))
				secCtx.m_logger.DebugLog (formatTdgssStatusErrOnly (status))
			}

			negMech.setNegMechState (NEG_MECH_STATE_REJECTED)
			negMech.setNegMechReason (NEG_MECH_REASON_GSSAPI_ERROR)
			negMech.setMajorStatus (status.Major)
			negMech.setMinorStatus (status.Minor)
			negMech.setMinorText (formatTdgssStatusErrOnly (status))

			continue
		}

		secCtx.m_nRecordNumber++
		secCtx.m_logRecord = newTdnegoLogRecord (
			negMech,
			"InitSecContext()",
			secCtx.m_nInitCall,
			secCtx.m_nRecordNumber,
			"initMechData(): Mechanism is available for negotiation",
			secCtx.m_bIsPolicyApplied,
			secCtx.m_nNegState,
			secCtx.m_nNegStateFromPeer,
			secCtx.m_startTime,
		)

		if secCtx.m_logger.HasDebugLog () {
			secCtx.m_logger.DebugLog (secCtx.m_logRecord.String ())
		}
		secCtx.m_logBuffer.WriteString (secCtx.m_logRecord.String ())

		secCtx.m_initMechHT.put (mechOID, negMech)
	}

	return
} // end initMechData

func (secCtx *tdnegoSecContext) rejectNonSSOmechs () {
	// Using SSO so loop through mechanisms and reject all mechanism but KRB5
	secCtx.m_initMechHT.m_negMechHashTable.Range (func (key, value any) bool {
		negMech := value.(*negMech)

		if negMech.getNegMechOidName () != gKrb5MechName {
			negMech.setNegMechState (NEG_MECH_STATE_REJECTED)
			negMech.setNegMechReason (NEG_MECH_REASON_SSO_REQUIRED)
			negMech.setContext (nil)

			secCtx.m_nRecordNumber ++
			secCtx.m_logRecord = newTdnegoLogRecord (
				negMech,
				"InitSecContext()",
				secCtx.m_nInitCall,
				secCtx.m_nRecordNumber,
				"rejectNonSSOmechs() NonSSO Mechanism is rejected based on credential",
				secCtx.m_bIsPolicyApplied,
				secCtx.m_nNegState,
				secCtx.m_nNegStateFromPeer,
				secCtx.m_startTime,
			)

			if secCtx.m_logger.HasDebugLog () {
				secCtx.m_logger.DebugLog (secCtx.m_logRecord.String ())
			}
			secCtx.m_logBuffer.WriteString (secCtx.m_logRecord.String ())
		}
		return true
	})
} // end rejectNonSSOmechs

func (secCtx *tdnegoSecContext) tdnegoIntProcessNames (sDesiredName string) {

	asExtProp := [] string {"user=", "profile=", "authcid="}
	sInput := sDesiredName
	for _, s := range (asExtProp) {
		bQuoteStart := false
		bQuoteEnd := false
		nEndIndex := 0
		quotedChar := ""
		nIndx := strings.Index (sDesiredName, s)

		if nIndx == -1 { continue }

		if sDesiredName [nIndx + len (s)] == '\'' || sDesiredName [nIndx + len (s)] == '"' {
			quotedChar = string (sDesiredName  [nIndx + len (s)])
			bQuoteStart = true
		}

		nEndQuoteIndex := 0
		for nEndQuoteIndex = nIndx + len (s) + 1 ; bQuoteStart && nEndQuoteIndex < len (sDesiredName); nEndQuoteIndex++ {
			if  quotedChar == string (sDesiredName [nEndQuoteIndex]) {
				bQuoteEnd = true
				break
			}
		}

		if bQuoteEnd {
			nEndIndex = nEndQuoteIndex + 1
		} else if strings.Index (sDesiredName[nIndx:], " ") == -1 {
			nEndIndex = len (sDesiredName)
		} else {
			nEndIndex = nIndx + strings.Index (sDesiredName[nIndx:], " ")
		}

		sProperty := sDesiredName [nIndx : nEndIndex]

		nPropIndex := 0
		if len (sProperty) != 0 {
			nPropIndex = strings.Index (sProperty, "user=")
			if nPropIndex != -1 {
				secCtx.m_sUserID = sProperty [nPropIndex + 5 :]
			}
			nPropIndex = strings.Index (sProperty, "profile=")
			if nPropIndex != -1 {
				secCtx.m_sProfileID = sProperty [nPropIndex + 8:]
			}
			nPropIndex = strings.Index (sProperty, "authcid=")
			if nPropIndex != -1 {
				secCtx.m_sAuthID = sProperty [nPropIndex + 8:]
			}
			sInput = strings.Replace (sDesiredName, sProperty, "", 1)
		}
	} // end for

	if len (sInput) > 0 {
		sInput = strings.TrimSpace (sInput)

		nDoubleAmpIndex := strings.Index (sInput, "@@")
		if nDoubleAmpIndex != -1 {
			secCtx.m_sAuthID = sInput [:nDoubleAmpIndex]
		} else {
			secCtx.m_sAuthID = sInput
		}
	}
} // end tdnegoIntProcessNames

func tdnegoIntFormatStatus (err error, nMajor uint32, nMinor uint32) (status TdgssStatus) {
	status.Major = nMajor
	status.Minor = nMinor
	status.Err = tdgssError (err, formatTdgssStatusErrOnly (status))
	return
}

func getFirstLine (s string) string {
	if i := strings.Index (s, "\n") ; i >= 0 {
		s = s [ : i ] // trim second and subsequent lines if present
	}
	return s
} // end getFirstLine

type tdnegoLogRecord struct {
	logRecordBuffer   bytes.Buffer
}

func newTdnegoLogRecord (
		negMech *negMech,
		sRecord string,
		nInitCall int,
		nRecordNum int,
		sText string,
		bIsPolicyApplied bool,
		nNegState int,
		nNegStateFromPeer int,
		startTime time.Time,
	) *tdnegoLogRecord {

	logRec := &tdnegoLogRecord {}
	logRec.logRecordBuffer.WriteString (fmt.Sprintf ("\nRecord:%v %v %v\n", nRecordNum, sRecord, nInitCall))
	logRec.logRecordBuffer.WriteString (fmt.Sprintf ("    Text:                 %v\n", sText))
	logRec.writeNegLog (negMech, bIsPolicyApplied, nNegState, nNegStateFromPeer, startTime)
	return logRec
} // end newTdnegoLogRecord

func (logRec *tdnegoLogRecord) String () string {
	return logRec.logRecordBuffer.String ()
}

func (logRec *tdnegoLogRecord) writeNegLog (
		negMech *negMech,
		bIsPolicyApplied bool,
		nNegState int,
		nNegStateFromPeer int,
		startTime time.Time,
	) {

	/* Printable string for each Mechanism State */
	/* MUST BE KEPT IN SYNC WITH NegMech class NEG_MECH_STATE */
	asMechState := [] string { "Invalid", "Available", "Selected", "Rejected" }

	/* Printable string for each Mechanism Reason */
	/* MUST BE KEPT IN SYNC WITH NegMech class NEG_MECH_REASON */
	asMechReason := [] string { "Invalid", "Available", "GSSAPI Error", "Not Available at Client", "Not Available at Server",
			"Due to Policy", "Due to Rank", "Single Sign-On Required", "Authentication ID not Available",
			"Authorization Required",
	}

	/* Printable string for each Negotiation State */
	/* MUST BE KEPT IN SYNC WITH NegState_et IN gssp2tdnegoint.h */
	asNegState := [] string { "Invalid", "Completed", "Incomplete", "Reject", "Request MIC" }

	if negMech != nil {

		logRec.logRecordBuffer.WriteString (fmt.Sprintf ("    Mechanism:            %v\n", negMech.getNegMechOidName ()))
		logRec.logRecordBuffer.WriteString (fmt.Sprintf ("    MechState:            %v\n", asMechState [negMech.getNegMechState ()]))
		if negMech.isMechReasonSet () {
			logRec.logRecordBuffer.WriteString (fmt.Sprintf ("    MechReason:           %v\n", asMechReason [negMech.getNegMechReason ()]))
		}

		if negMech.getNegMechReason () == NEG_MECH_REASON_GSSAPI_ERROR {
			if negMech.isMajorStatusSet () {
				logRec.logRecordBuffer.WriteString (fmt.Sprintf ("    MajorStatus:          %0x\n", 0x00000000 | negMech.getMajorStatus ()))
			}
			if negMech.isMinorStatusSet () {
				logRec.logRecordBuffer.WriteString (fmt.Sprintf ("    MinorStatus:          %0x\n", negMech.getMinorStatus ()))
			}
			if negMech.isMinorTextSet () {
				logRec.logRecordBuffer.WriteString (fmt.Sprintf ("    MinorStatusText:      %v\n", negMech.getMinorText ()))
			}
		}

		logRec.logRecordBuffer.WriteString (fmt.Sprintf ("    IsContextEstablished: %v\n", negMech.isContextEstablished ()))
	} // if negMech not nil

	logRec.logRecordBuffer.WriteString (fmt.Sprintf ("    IsPolicyApplied:      %v\n", bIsPolicyApplied))
	logRec.logRecordBuffer.WriteString (fmt.Sprintf ("    NegState:             %v\n", asNegState [nNegState]))
	logRec.logRecordBuffer.WriteString (fmt.Sprintf ("    NegStateFromPeer:     %v\n", asNegState [nNegStateFromPeer]))
	logRec.logRecordBuffer.WriteString (fmt.Sprintf ("    Elapsed Time:         %v\n", time.Since (startTime)))

} // end writeNegLog
