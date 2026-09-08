// Copyright 2023 by Teradata Corporation. All Rights Reserved.

/**
 * This represents the token received from context acceptor. This contains information
 * about the negotiation state and the final negotiated mechanism if one is negotiated.
 * 
 */

package goteragss

import (
	"encoding/asn1"
	"fmt"
)

/* NegTokenResp */
const (
	NEGTOKENRESP_TAG_NEGMECHLIST      = iota
	NEGTOKENRESP_TAG_NEGSTATE
	NEGTOKENRESP_TAG_MECHTYPESELECTED
	NEGTOKENRESP_TAG_NEGPOLICYAPPLIED
)

type negTokenResp struct {
	m_nNegState              int
	m_negMechHashTable       *negMechHashTable
	m_negMechSelected        asn1.ObjectIdentifier
	m_bIsNegMechSelected     bool
	m_nNegPolicyApplied      int
	m_bIsNegPolicyApplied    bool
}

func newNegTokenResp () (*negTokenResp) {
	return &negTokenResp {m_negMechHashTable : newNegMechHashTable ()}
}

func (tokenResp *negTokenResp) getNegMechHashTable () *negMechHashTable {  
    return tokenResp.m_negMechHashTable
}

func (tokenResp *negTokenResp) setNegMechHashTable (negHashTable *negMechHashTable) { 
    tokenResp.m_negMechHashTable = negHashTable
}

func (tokenResp *negTokenResp) getNegState () byte {
    return byte (tokenResp.m_nNegState)
}

func (tokenResp *negTokenResp) setNegState (nNegState int) (status TdgssStatus) {
	if NEG_STATE_INVALID >= nNegState || nNegState >= NEG_STATE_INVALID_LAST {
		return tdnegoIntFormatStatus (nil, GSS_S_FAILURE, TDNEGO_ERR_INVALID_NEG_STATE)
	}
	tokenResp.m_nNegState = nNegState
	return
}

func (tokenResp *negTokenResp) getNegMechSelectedAsBytes () [] byte {
	if tokenResp.m_bIsNegMechSelected {
        abyType, err := asn1.Marshal (tokenResp.m_negMechSelected)
		if err == nil {
			return abyType [2 : ]
		}
	}
	return []byte {}
}

func (tokenResp *negTokenResp) setNegMechSelected (negMechSelected asn1.ObjectIdentifier) {
	tokenResp.m_negMechSelected = negMechSelected
	tokenResp.m_bIsNegMechSelected = true
}

func (tokenResp *negTokenResp) getNegPolicyApplied () bool {
	return tokenResp.m_nNegPolicyApplied == 1
}

func (tokenResp *negTokenResp) getNegPolicyAppliedAsByte () byte {
	return byte (tokenResp.m_nNegPolicyApplied)
}

func (tokenResp *negTokenResp) setNegPolicyApplied (nPolicyApplied int) {
	tokenResp.m_nNegPolicyApplied = nPolicyApplied
	tokenResp.m_bIsNegPolicyApplied = true
}

func (tokenResp *negTokenResp) isNegMechSelected () bool {
	return tokenResp.m_bIsNegMechSelected
}

func (tokenResp *negTokenResp) isNegPolicyApplied () bool {
	return tokenResp.m_bIsNegPolicyApplied
}

//    Encode the Gssp2TdNegTokenResp sequence of a TDNEGO context token
//
//    NegTokenResp is used by both the Context Initiator and Acceptor
//    The only time it is not used is for the first token sent by gsspInitSecContext
//
//    The ASN.1 for the NegTokenResp sequence is:
//    NegTokenResp ::= SEQUENCE {
//        negMechList         [0] NegMechList,
//        negState            [1] NegState,                  -- Overall negotiation state
//        negMechSelected     [2] MechType         OPTIONAL, -- only if Mech selected
//        negPolicyApplied    [4] NegPolicyApplied OPTIONAL  -- only if policy just applied }
 func (tokenResp *negTokenResp) encodeNegTokenResp () (negTokenResponse [] byte) {

	abyNegMechSelected := [] byte {}
	abyNegPolicy := [] byte {}

	abyMechTokens := tokenResp.m_negMechHashTable.encodeNegMechList ()

	abyState := makeASN1Value (GSSP_TAG_PRIVATE | NEGTOKENRESP_TAG_NEGSTATE, tokenResp.getNegState ()) 

	if tokenResp.isNegMechSelected () {
		abyNegMechSelected = makeASN1Value (GSSP_TAG_PRIVATE | NEGTOKENRESP_TAG_MECHTYPESELECTED, tokenResp.getNegMechSelectedAsBytes ()...) 
	}

	if tokenResp.isNegPolicyApplied () {
		abyNegPolicy = makeASN1Value (GSSP_TAG_PRIVATE | NEGTOKENRESP_TAG_NEGPOLICYAPPLIED, tokenResp.getNegPolicyAppliedAsByte ())
	}

	return makeASN1Value (GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | NEGTOKENRESP_TAG, concat (
		makeASN1Value (GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | NEGTOKENRESP_TAG_NEGMECHLIST, abyMechTokens...),
	     abyState, abyNegMechSelected, abyNegPolicy)...)
	
} // end encodeNegTokenResp

//    Decode the Gssp2TdnegoTokenResp sequence of a TDNEGO context token.
//
//    NegTokenResp is used by both the Context Initiator and Acceptor
//    The only time it is not used is for the first token sent by the Initiator
//
//    The ASN.1 for the NegTokenResp sequence is:
//    NegTokenResp ::= SEQUENCE {
//        negMechList         [0] NegMechList,
//        negState            [1] NegState,                  -- Overall negotiation state
//        negMechSelected     [2] MechType         OPTIONAL, -- only if Mech selected
//        negPolicyApplied    [4] NegPolicyApplied OPTIONAL  -- only if policy just applied
//    }
func (tokenResp *negTokenResp) decodeNegTokenResp (abyInBuf []byte) (status TdgssStatus) {
    responseTokenAsn1, err := readASN1Values (abyInBuf) 

	if err != nil {
		return tdnegoIntFormatStatus (err, GSS_S_FAILURE, TDGSS_ERR_ASN_TAG_INVALID)
	}

	if responseTokenAsn1[0].parent.Class != asn1.ClassPrivate || ! responseTokenAsn1[0].parent.IsCompound || responseTokenAsn1[0].parent.Tag != NEGTOKENRESP_TAG {
		return tdnegoIntFormatStatus (nil, GSS_S_FAILURE, TDGSS_ERR_ASN_TAG_INVALID)
	}

	//printAsn1RespToken (responseTokenAsn1)
	bNegMechListTagFound := false
	bNegStateTagFound := false

	for _, asn1ValPar := range responseTokenAsn1[0].children {
		if asn1ValPar.parent.Class != asn1.ClassPrivate  {
			return tdnegoIntFormatStatus (nil, GSS_S_FAILURE, TDGSS_ERR_ASN_INVALID_CLASS_TAG)
		}

		switch (asn1ValPar.parent.Tag) {
			case NEGTOKENRESP_TAG_NEGMECHLIST:
				if ! asn1ValPar.parent.IsCompound {
					return tdnegoIntFormatStatus (nil, GSS_S_FAILURE, TDGSS_ERR_ASN_TAG_INVALID)
				}

				status = tokenResp.m_negMechHashTable.decodeNegMechList (asn1ValPar)
				if status.Major != GSS_S_COMPLETE { return }
				bNegMechListTagFound = true

			case NEGTOKENRESP_TAG_NEGSTATE:
				if asn1ValPar.parent.IsCompound {
					return tdnegoIntFormatStatus (nil, GSS_S_FAILURE, TDGSS_ERR_ASN_TAG_INVALID)
				}

				status = tokenResp.setNegState (int (asn1ValPar.parent.Bytes [0]))
				if status.Major == GSS_S_FAILURE { return }
				bNegStateTagFound = true

			case NEGTOKENRESP_TAG_MECHTYPESELECTED:    // optional tag
				if asn1ValPar.parent.IsCompound {
					return tdnegoIntFormatStatus (nil, GSS_S_FAILURE, TDGSS_ERR_ASN_TAG_INVALID)
				}

				var negMechSelected asn1.ObjectIdentifier
				status, negMechSelected = readASN1ObjectIdentifier (asn1ValPar.parent.Bytes)
				if status.Major == GSS_S_FAILURE { return }
				tokenResp.setNegMechSelected (negMechSelected)

			case NEGTOKENRESP_TAG_NEGPOLICYAPPLIED:    // optional tag
				if asn1ValPar.parent.IsCompound {
					return tdnegoIntFormatStatus (nil, GSS_S_FAILURE, TDGSS_ERR_ASN_TAG_INVALID)
				}

				tokenResp.setNegPolicyApplied (int (asn1ValPar.parent.Bytes [0]))

			default:
				return tdnegoIntFormatStatus (nil, GSS_S_FAILURE, TDGSS_ERR_ASN_INVALID_DECODE_TYPE)

		}
	}

	if ! bNegMechListTagFound {
        err := tdgssError (nil, "Token Response mechanism list tag not found")
		return tdnegoIntFormatStatus (err, GSS_S_FAILURE, TDGSS_ERR_ASN_TAG_NOT_FOUND)
	}

	if ! bNegStateTagFound {
        err := tdgssError (nil, "Token Response mechanism state tag not found")
		return tdnegoIntFormatStatus (err, GSS_S_FAILURE, TDGSS_ERR_ASN_TAG_NOT_FOUND)
	}

	return
} // end decodeNegTokenResp

func printAsn1RespToken (responseTokenAsn1 [] asn1Value) {
	fmt.Printf ("Length of asn1ServerIno is %v\n", len (responseTokenAsn1[0].children))

	asNegParentTags := [] string { "NEGTOKENINIT_TAG", "NEGTOKENRESP_TAG" }
	asNegTokenTags := [] string { "NEGTOKENRESP_TAG_NEGMECHLIST", "NEGTOKENRESP_TAG_NEGSTATE", "NEGTOKENRESP_TAG_MECHTYPESELECTED", "NEGTOKENRESP_TAG_NEGPOLICYAPPLIED", "ERROR" }
	asNegMegListTags := [] string { "NEGMECH_TAG_NEGMECH" }
	asNegMechTags := [] string { "NEGMECH_TAG_MECHTYPE", "NEGMECH_TAG_NEGMECHSTATE", "NEGMECH_TAG_NEGMECHREASON", "NEGMECH_TAG_GSSMAJORSTATUS", "NEGMECH_TAG_GSSMINORSTATUS", "NEGMECH_TAG_GSSMINORSTATUSTEXT", "NEGMECH_TAG_MECHTOKEN", "NEGMECH_TAG_SSOAUTHENTICATIONID", "ERROR" }

	for nPar, asn1ValPar := range responseTokenAsn1 {
		fmt.Printf ("Parent %v class %v isCompound %v parent tag %v (%v) Byte length %v\n", nPar, asn1ValPar.parent.Class, asn1ValPar.parent.IsCompound, asn1ValPar.parent.Tag, asNegParentTags [asn1ValPar.parent.Tag], len (asn1ValPar.parent.Bytes))
		for nChild, child := range asn1ValPar.children {
			fmt.Printf ("\tAsn1 Info parent %v child %v class %v isCompound %v tag %v (%v) Byte length %v number of children %v\n", nPar, nChild, child.parent.Class, child.parent.IsCompound, child.parent.Tag, asNegTokenTags [child.parent.Tag], len (child.parent.Bytes), len (child.children))
			for ngk, gk := range child.children {
				fmt.Printf ("\t\tAsn1 Info parent %v child %v ngk %v class %v isCompound %v tag %v (%v) Byte length %v number of children %v\n", nPar, nChild, ngk, gk.parent.Class, gk.parent.IsCompound, gk.parent.Tag, asNegMegListTags [gk.parent.Tag], len (gk.parent.Bytes), len (gk.children))
				for nggk, ggk := range gk.children {
					fmt.Printf ("\t\t\tAsn1 Info parent %v child %v ngk %v nggk %v class %v isCompound %v tag %v (%v) Byte length %v number of children %v\n", nPar, nChild, ngk, nggk, ggk.parent.Class, ggk.parent.IsCompound, ggk.parent.Tag, asNegMechTags [ggk.parent.Tag], len (ggk.parent.Bytes), len (ggk.children))
				}
	
			}
		}
	}
}
