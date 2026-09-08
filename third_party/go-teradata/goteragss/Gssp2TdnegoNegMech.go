// Copyright 2023 by Teradata Corporation. All Rights Reserved.

package goteragss

import (
	"bytes"
	"encoding/asn1"
	"fmt"
)

const (
	NEG_MECH_STATE_INVALID = iota // This value should never occur.
	NEG_MECH_STATE_AVAILABLE      // This mech is available to be selected.
	NEG_MECH_STATE_SELECTED       // This mech is the one that will be used.
	NEG_MECH_STATE_REJECTED       // This mech will not be used
	NEG_MECH_STATE_INVALID_LAST   // MUST BE LAST should never occur 
)

const (
	NEG_MECH_REASON_INVALID = iota            // This value should never occur.
	NEG_MECH_REASON_AVAILABLE                 // Only for NEG_MECH_STATE_AVAILABLE
	NEG_MECH_REASON_GSSAPI_ERROR              // Only for NEG_MECH_STATE_REJECTED
	NEG_MECH_REASON_NOT_AVAIL_AT_CLIENT       // Only for NEG_MECH_STATE_REJECTED
	NEG_MECH_REASON_NOT_AVAIL_AT_SERVER       // Only for NEG_MECH_STATE_REJECTED
	NEG_MECH_REASON_DUE_TO_POLICY             // Only for NEG_MECH_STATE_REJECTED
	NEG_MECH_REASON_DUE_TO_RANK               // Only for NEG_MECH_STATE_REJECTED
	NEG_MECH_REASON_SSO_REQUIRED              // Only for NEG_MECH_STATE_REJECTED
	NEG_MECH_REASON_AUTH_ID_NOT_AVAIL         // Only for NEG_MECH_STATE_REJECTED
	NEG_MECH_REASON_AUTHORIZATION_REQUIRED    // Only for NEG_MECH_STATE_REJECTED
	NEG_MECH_REASON_INVALID_LAST              // MUST BE LAST should never occur    
)

type negMech struct {
	m_bIsMechTypeSet         bool
	m_mechType               asn1.ObjectIdentifier
	m_bIsMechStateSet        bool
	m_nMechState             int
	m_bIsMechReasonSet       bool
	m_nMechReason            int
	m_bIsMajorStatusSet      bool
	m_uMajorStatus           uint32
	m_bIsMinorStatusSet      bool
	m_uMinorStatus           uint32
	m_bIsMinorTextSet        bool
	m_sMinorText             string
	m_bIsMechToken           bool
	m_abyMechToken           [] byte
	m_bIsSsoAuthID           bool
	m_sSsoAuthID             string
	m_mechCtx                gssp
	m_bIsContextEstablished  bool
}

func newNegMech () (*negMech) {
	return &negMech {}
}

func (mech *negMech) String () string {
	var b bytes.Buffer
	b.WriteString (fmt.Sprintf ("Name                 = %v\n", mech.getNegMechOidName ()))
	b.WriteString (fmt.Sprintf ("m_mechType           = %v\n", mech.m_mechType))
	b.WriteString (fmt.Sprintf ("m_nMechState         = %v\n", mech.m_nMechState))
	b.WriteString (fmt.Sprintf ("m_bIsMechReasonSet   = %v\n", mech.m_bIsMechReasonSet))
	b.WriteString (fmt.Sprintf ("m_nMechReason        = %v\n", mech.m_nMechReason))
	b.WriteString (fmt.Sprintf ("m_bIsMajorStatusSet  = %v\n", mech.m_bIsMajorStatusSet))
	b.WriteString (fmt.Sprintf ("m_uMajorStatus       = %v\n", mech.m_uMajorStatus))
	b.WriteString (fmt.Sprintf ("m_bIsMinorStatusSet  = %v\n", mech.m_bIsMinorStatusSet))
	b.WriteString (fmt.Sprintf ("m_uMinorStatus       = %v\n", mech.m_uMinorStatus))
	b.WriteString (fmt.Sprintf ("m_bIsMinorTextSet    = %v\n", mech.m_bIsMinorTextSet))
	b.WriteString (fmt.Sprintf ("m_sMinorText         = %v\n", mech.m_sMinorText))
	b.WriteString (fmt.Sprintf ("m_bIsMechToken       = %v\n", mech.m_bIsMechToken))
	b.WriteString (fmt.Sprintf ("m_abyMechToken       = % x\n", mech.m_abyMechToken))
	b.WriteString (fmt.Sprintf ("m_bIsSsoAuthID       = %v\n", mech.m_bIsSsoAuthID))
	b.WriteString (fmt.Sprintf ("m_sSsoAuthID         = %v\n", mech.m_sSsoAuthID))
	b.WriteString (fmt.Sprintf ("m_mechCtx            = %v\n", mech.m_mechCtx))
	b.WriteString (fmt.Sprintf ("m_bIsContextEstablished = %v\n", mech.m_bIsContextEstablished))
	return b.String ()
} // end String

func (mech *negMech) getMechType () (asn1.ObjectIdentifier) {
	return mech.m_mechType
}

func (mech *negMech) setMechType (mechType asn1.ObjectIdentifier) {
	mech.m_bIsMechTypeSet = true
	mech.m_mechType = mechType
}

func (mech *negMech) getNegMechState () (int) {
	return mech.m_nMechState
}

func (mech *negMech) setNegMechState (nNegMechState int) (status TdgssStatus) {
	if ((nNegMechState <= NEG_MECH_STATE_INVALID) || (nNegMechState >= NEG_MECH_STATE_INVALID_LAST)) {
		err := tdgssError (nil, "NegMech state is invalid")
		return tdnegoIntFormatStatus (err, GSS_S_FAILURE, TDNEGO_ERR_INVALID_STATE)
	}
	mech.m_bIsMechStateSet = true
	mech.m_nMechState = nNegMechState
	return
}

func (mech *negMech) getNegMechReason () (int) {
	return mech.m_nMechReason
}

func (mech *negMech) setNegMechReason (nMechReason int) (status TdgssStatus) {
	if ((nMechReason <= NEG_MECH_REASON_INVALID) || (nMechReason >= NEG_MECH_REASON_INVALID_LAST)) {
		err := tdgssError (nil, "NegMechReason is invalid")
		return tdnegoIntFormatStatus (err, GSS_S_FAILURE, TDNEGO_ERR_INVALID_REASON)
	}
	mech.m_bIsMechReasonSet = true
	mech.m_nMechReason = nMechReason
	return
}

func (mech *negMech) getMajorStatus () (uint32) {
	return mech.m_uMajorStatus
}

func (mech *negMech) setMajorStatus (uMajorStatus uint32) (status TdgssStatus) {
	mech.m_bIsMajorStatusSet = true
	mech.m_uMajorStatus = uMajorStatus
	return
}

func (mech *negMech) getMinorStatus () (uint32) {
	return mech.m_uMinorStatus
}

func (mech *negMech) setMinorStatus (uMinorStatus uint32) {
	mech.m_bIsMinorStatusSet = true
	mech.m_uMinorStatus = uMinorStatus
}

func (mech *negMech) getMinorText () (string) {
	return mech.m_sMinorText
}

func (mech *negMech) setMinorText (sMinorText string) {
	mech.m_bIsMinorTextSet = true
	mech.m_sMinorText = sMinorText
}

func (mech *negMech) getMechToken () ([] byte) {
	return mech.m_abyMechToken
}

func (mech *negMech) setMechToken (abyMechToken [] byte) {
	mech.m_bIsMechToken = true
	mech.m_abyMechToken = abyMechToken
}

func (mech *negMech) getSsoAuthID () (string) {
	return mech.m_sSsoAuthID
}

func (mech *negMech) setSsoAuthID (sIsSsoAuthID string) {
	mech.m_bIsSsoAuthID = true
	mech.m_sSsoAuthID = sIsSsoAuthID
}

func (mech *negMech) isMechTypeSet () bool {
	return mech.m_bIsMechTypeSet
}

func (mech *negMech) isMechStateSet () bool {
	return mech.m_bIsMechStateSet
}

func (mech *negMech) isMechReasonSet () bool {
	return mech.m_bIsMechReasonSet
}

func (mech *negMech) isSsoAuthid () (bool) {
	return mech.m_bIsSsoAuthID
}

func (mech *negMech) isMechToken () (bool) {
	return mech.m_bIsMechToken
}

func (mech *negMech) isMinorTextSet () (bool) {
	return mech.m_bIsMinorTextSet
}

func (mech *negMech) isMinorStatusSet () (bool) {
	return mech.m_bIsMinorStatusSet
}

func (mech *negMech) isMajorStatusSet () (bool) {
	return mech.m_bIsMajorStatusSet
}

func (mech *negMech) setContextEstablished (bIsContextEstablished bool) {
	mech.m_bIsContextEstablished = bIsContextEstablished
}

func (mech *negMech) isContextEstablished () (bool) {
	return mech.m_bIsContextEstablished
}

func (mech *negMech) getNegMechOidName () string {

	if mech.m_mechCtx != nil && mech.m_mechCtx.gsspGetMechInfo () != nil {
		return mech.m_mechCtx.gsspGetMechInfo ().mechName
	} else if mech.m_bIsMechTypeSet {
		if mechName, ok := gMechOidStringToMechName [mech.m_mechType.String ()] ; ok {
			return mechName
		}
	}

	return ""
} // end getNegMechOidName

/**
  Encode the negMech sequence

  The ASN.1 for the NegMech sequence is:
  NegMech ::= SEQUENCE {
	 mechType            [0] MechType,
	 MechState           [1] MechState,
	 MechReason          [2] MechReason,  OPTIONAL,  -- only if State changed
	 gssMajorStatus      [3] INTEGER      OPTIONAL,  -- only if GSSAPI Error
	 gssMinorStatus      [4] INTEGER      OPTIONAL,  -- only if GSSAPI Error
	 gssMinorStatusText  [5] IA5STRING    OPTIONAL,  -- only if GSSAPI Error
	 mechToken           [6] OCTET STRING OPTIONAL,  -- only if available
	 ssoauthenticationID [7] UTF8String   OPTIONAL   -- only if SSO needs }
*/
func (mech *negMech) encodeNegMech () (abyMechTokens []byte) {

	if mech.getNegMechState () == NEG_MECH_STATE_AVAILABLE {

		abyType, _ := asn1.Marshal (mech.m_mechType)
		abyMechTokens = append (abyMechTokens, makeASN1Value (GSSP_TAG_PRIVATE | NEGMECH_TAG_MECHTYPE, abyType [2 : ]...)...)

		abyMechTokens = append (abyMechTokens, makeASN1Value (GSSP_TAG_PRIVATE | NEGMECH_TAG_NEGMECHSTATE, byte (mech.m_nMechState))...) 

		if mech.m_bIsMechReasonSet {
			abyMechTokens = append (abyMechTokens, makeASN1Value (GSSP_TAG_PRIVATE | NEGMECH_TAG_NEGMECHREASON, byte (mech.m_nMechReason))...)  
		}

		if mech.m_bIsMajorStatusSet {
			abyMechTokens = append (abyMechTokens, makeASN1Value (GSSP_TAG_PRIVATE | NEGMECH_TAG_GSSMAJORSTATUS, makeASN1Uint32Payload (mech.m_uMajorStatus)...)...)  
		}

		if mech.m_bIsMinorStatusSet {
			abyMechTokens = append (abyMechTokens, makeASN1Value (GSSP_TAG_PRIVATE | NEGMECH_TAG_GSSMINORSTATUS, makeASN1Uint32Payload (mech.m_uMinorStatus)...)...)  
		}

		if mech.m_bIsMinorTextSet {
			abyMechTokens = append (abyMechTokens, makeASN1Value (GSSP_TAG_PRIVATE | NEGMECH_TAG_GSSMINORSTATUSTEXT, [] byte (mech.m_sMinorText)...)...)  
		}

		if mech.m_bIsMechToken {
			abyMechTokens = append (abyMechTokens, makeASN1Value (GSSP_TAG_PRIVATE | NEGMECH_TAG_MECHTOKEN, mech.m_abyMechToken...)...)  
		}

		if mech.m_bIsSsoAuthID {
			abyMechTokens = append (abyMechTokens, makeASN1Value (GSSP_TAG_PRIVATE | NEGTOKENINIT_TAG_AUTHENTICATIONID, [] byte (mech.m_sSsoAuthID)...)...)
		}

		return makeASN1Value (GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | NEGMECH_TAG_MECHTYPE, abyMechTokens...)
	}

	return
} // end encodeNegMech

/**
  Decode the negMech sequence

  The ASN.1 for the NegMech sequence is:
  NegMech ::= SEQUENCE {
	 mechType            [0] MechType,
	 MechState           [1] MechState,
	 MechReason          [2] MechReason,  OPTIONAL,  -- only if State changed
	 gssMajorStatus      [3] INTEGER      OPTIONAL,  -- only if GSSAPI Error
	 gssMinorStatus      [4] INTEGER      OPTIONAL,  -- only if GSSAPI Error
	 gssMinorStatusText  [5] IA5STRING    OPTIONAL,  -- only if GSSAPI Error
	 mechToken           [6] OCTET STRING OPTIONAL,  -- only if available
	 ssoauthenticationID [7] UTF8String   OPTIONAL   -- only if SSO needs }
*/
func (mech *negMech) decodeNegMech (asn1NegMech asn1Value) (status TdgssStatus) {

	if asn1NegMech.parent.Class != asn1.ClassPrivate || !asn1NegMech.parent.IsCompound || asn1NegMech.parent.Tag != NEGMECH_TAG_NEGMECH {
		return tdnegoIntFormatStatus (nil, GSS_S_FAILURE, TDGSS_ERR_ASN_TAG_INVALID)
	}

	for _, asn1MechProp := range asn1NegMech.children {
		if asn1MechProp.parent.Class != asn1.ClassPrivate || asn1MechProp.parent.IsCompound || asn1MechProp.parent.Tag < NEGMECH_TAG_MECHTYPE || asn1MechProp.parent.Tag > NEGMECH_TAG_SSOAUTHENTICATIONID {
			return tdnegoIntFormatStatus (nil, GSS_S_FAILURE, TDGSS_ERR_ASN_TAG_INVALID)
		}

		switch (asn1MechProp.parent.Tag) {
			case NEGMECH_TAG_MECHTYPE:
				var mechType asn1.ObjectIdentifier
				status, mechType = readASN1ObjectIdentifier (asn1MechProp.parent.Bytes)
				if status.Major == GSS_S_FAILURE { return }

				mech.setMechType (mechType)

			case NEGMECH_TAG_NEGMECHSTATE:
				status = mech.setNegMechState (int (asn1MechProp.parent.Bytes [0]))

			case NEGMECH_TAG_NEGMECHREASON:
				status = mech.setNegMechReason (int (asn1MechProp.parent.Bytes [0]))

			case NEGMECH_TAG_GSSMAJORSTATUS:
				tmpStatus, uint32Temp := readASN1Uint32 (asn1MechProp.parent.Bytes)
				if tmpStatus.Major != GSS_S_COMPLETE {
					return tdnegoIntFormatStatus (tmpStatus.Err, GSS_S_FAILURE, TDNEGO_ERR_INVALID_INPUT)
				}

				status = mech.setMajorStatus (uint32Temp)

			case NEGMECH_TAG_GSSMINORSTATUS:
				tmpStatus, uint32Temp := readASN1Uint32 (asn1MechProp.parent.Bytes)
				if tmpStatus.Major != GSS_S_COMPLETE {
					return tdnegoIntFormatStatus (tmpStatus.Err, GSS_S_FAILURE, TDNEGO_ERR_INVALID_INPUT)
				}

				mech.setMinorStatus (uint32Temp)

			case NEGMECH_TAG_GSSMINORSTATUSTEXT:
				mech.setMinorText (string (asn1MechProp.parent.Bytes))

			case NEGMECH_TAG_MECHTOKEN:
				mech.setMechToken (asn1MechProp.parent.Bytes)

			case NEGMECH_TAG_SSOAUTHENTICATIONID:
				mech.setSsoAuthID (string (asn1MechProp.parent.Bytes))

		}

		if status.Major == GSS_S_FAILURE { return }

	} // end for

	if !mech.isMechTypeSet () {
		err := tdgssError (nil, "Mechanism type tag not found")
		return tdnegoIntFormatStatus (err, GSS_S_FAILURE, TDGSS_ERR_ASN_TAG_NOT_FOUND)
	}

	if !mech.isMechStateSet () {
		err := tdgssError (nil, "Mechanism state tag not found")
		return tdnegoIntFormatStatus (err, GSS_S_FAILURE, TDGSS_ERR_ASN_TAG_NOT_FOUND)
	}

	return
} // end decodeNegMech

func (mech *negMech) getContext () (gssp) {
	return mech.m_mechCtx
}

func (mech *negMech) setContext (ctx gssp) {
	mech.m_mechCtx = ctx
}
