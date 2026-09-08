// Copyright 2023 by Teradata Corporation. All Rights Reserved.
package goteragss

/* Definitions for Implicit Tagging of sequences in tokens */

/* NegotiationToken */
const (
	NEGTOKENINIT_TAG = iota
	NEGTOKENRESP_TAG 
)

/* NegTokenInit */
const (
	NEGTOKENINIT_TAG_NEGMECHLIST      = iota
	NEGTOKENINIT_TAG_NEGSTATE        
	NEGTOKENINIT_TAG_AUTHENTICATIONID 
	NEGTOKENINIT_TAG_ISSSO            
	NEGTOKENINIT_TAG_USERNAME         
	NEGTOKENINIT_TAG_PROFILENAME      
)

/* This tag is used as substitute for NEGTOKENRESP_TAG_NEGMECHLIST
	* and NEGTOKENINIT_TAG_NEGMECHLIST 
	*/
const NEGMECHLIST_TAG                   = 0x0

/* NegMechList */
const NEGMECH_TAG_NEGMECH               = 0x0

/* NegMech */
const (
	NEGMECH_TAG_MECHTYPE              =  iota
	NEGMECH_TAG_NEGMECHSTATE
	NEGMECH_TAG_NEGMECHREASON
	NEGMECH_TAG_GSSMAJORSTATUS
	NEGMECH_TAG_GSSMINORSTATUS
	NEGMECH_TAG_GSSMINORSTATUSTEXT
	NEGMECH_TAG_MECHTOKEN
	NEGMECH_TAG_SSOAUTHENTICATIONID
)

type negTokenInit struct {
	m_nNegState              int
	m_negMechHashTable       *negMechHashTable
	m_sAuthenticationID      string
	m_bIsAuthenticationIDSet bool
	m_nIsSSO                 int
	m_bIsSSOSet              bool
	m_sUserName              string
	m_bIsUserNameSet         bool
	m_sProfileName           string
	m_bIsProfilenameSet      bool
}

func newNegTokenInit () (*negTokenInit) {
	return &negTokenInit {}
}

func (tokenInit *negTokenInit) getNegMechHashTable () *negMechHashTable {  
	return tokenInit.m_negMechHashTable
}

func (tokenInit *negTokenInit) setNegMechHashTable (negHashTable *negMechHashTable) { 
	tokenInit.m_negMechHashTable = negHashTable
}

func (tokenInit *negTokenInit) getNegState () byte {
	return byte (tokenInit.m_nNegState)
}

func (tokenInit *negTokenInit) setNegState (nNegState int) (status TdgssStatus) {
	if NEG_STATE_INVALID >= nNegState || nNegState >= NEG_STATE_INVALID_LAST {
		err := tdgssError (nil, "Neg state is invalid")
		return tdnegoIntFormatStatus (err, GSS_S_FAILURE, TDNEGO_ERR_INVALID_STATE)
	}
	tokenInit.m_nNegState = nNegState
	return
}

func (tokenInit *negTokenInit) getAuthenticationID () [] byte {
	return []byte (tokenInit.m_sAuthenticationID)
}

func (tokenInit *negTokenInit) setAuthenticationID (sAuthID string) {
	tokenInit.m_sAuthenticationID = sAuthID
	tokenInit.m_bIsAuthenticationIDSet = true
}

func (tokenInit *negTokenInit) getIsSSO () byte {
	return byte (tokenInit.m_nIsSSO)
}

func (tokenInit *negTokenInit) setSSO (bIsSSO bool) {
	if bIsSSO {
		tokenInit.m_nIsSSO = 1
	} else {
		tokenInit.m_nIsSSO = 0
	}

	tokenInit.m_bIsSSOSet = true
}

func (tokenInit *negTokenInit) setUserName (sUserName string) {
	tokenInit.m_sUserName = sUserName
	tokenInit.m_bIsUserNameSet = true
}

func (tokenInit *negTokenInit) getUserName () []byte {
	return [] byte (tokenInit.m_sUserName)
}

func (tokenInit *negTokenInit) setProfileName (sProfileName string) {
	tokenInit.m_sProfileName = sProfileName
	tokenInit.m_bIsProfilenameSet = true
}

func (tokenInit *negTokenInit) getProfileName () []byte {
	return []byte (tokenInit.m_sProfileName)
}

func (tokenInit *negTokenInit) isAuthenticationIDSet () bool {
	return tokenInit.m_bIsAuthenticationIDSet
}

func (tokenInit *negTokenInit) isSSOSet () bool {
	return tokenInit.m_bIsSSOSet
}

func (tokenInit *negTokenInit) isUserNameSet () bool {
	return tokenInit.m_bIsUserNameSet
}

func (tokenInit *negTokenInit) isProfileNameSet () bool {
	return tokenInit.m_bIsProfilenameSet
}

//    Encode the Gssp2TdNegTokenInit sequence of a TDNEGO context token
//
//    NegTokenInit is only used by the Context Initiator and only on the first token
//    The negotiation can never succeed in this first step, but it could fail
//    NegTokenInit won't be constructed and sent to server if NegState is neg state reject
//
//    The ASN.1 for the NegTokenInit sequence is:
//    NegTokenInit ::= SEQUENCE {
//        negMechList         [0] NegMechList,
//        negState            [1] NegState,                  -- Overall negotiation state
//        authenticationID    [2] UTF8String,      OPTIONAL, -- required unless SSO
//        isSSO               [3] BOOLEAN          OPTIONAL, -- only if this is SSO
//        userName            [4] UTF8String       OPTIONAL, -- only if supplied to tdnego
//        profileName         [5] UTF8String       OPTIONAL  -- only if supplied to tdnego }
func (tokenInit *negTokenInit) encodeNegTokenInit () [] byte {

	abyAsn1AuthId   := [] byte {}
	abyAsn1IsSSO    := [] byte {}
	abyAsn1UserName := [] byte {}
	abyAsn1ProfName := [] byte {}

	abyTokens := tokenInit.m_negMechHashTable.encodeNegMechList ()
	abyAsn1MechValues := makeASN1Value (GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | NEGTOKENINIT_TAG_NEGMECHLIST, abyTokens...)
	abyAsn1State := makeASN1Value (GSSP_TAG_PRIVATE | NEGTOKENINIT_TAG_NEGSTATE, tokenInit.getNegState ())

	if tokenInit.m_bIsAuthenticationIDSet {
		abyAsn1AuthId = makeASN1Value (GSSP_TAG_PRIVATE | NEGTOKENINIT_TAG_AUTHENTICATIONID, tokenInit.getAuthenticationID ()...)
	}
		
	if tokenInit.m_bIsSSOSet {
		abyAsn1IsSSO = makeASN1Value (GSSP_TAG_PRIVATE | NEGTOKENINIT_TAG_ISSSO, tokenInit.getIsSSO ())
	}

	if tokenInit.m_bIsUserNameSet {
		abyAsn1UserName = makeASN1Value (GSSP_TAG_PRIVATE | NEGTOKENINIT_TAG_USERNAME, tokenInit.getUserName ()...)
	}

	if tokenInit.m_bIsProfilenameSet {
		abyAsn1ProfName = makeASN1Value (GSSP_TAG_PRIVATE | NEGTOKENINIT_TAG_PROFILENAME, tokenInit.getProfileName ()...)
	}

	return makeASN1Value (GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | NEGTOKENINIT_TAG, 
		concat (abyAsn1MechValues, abyAsn1State, abyAsn1AuthId, abyAsn1IsSSO, abyAsn1UserName, abyAsn1ProfName)...)

} // end encodeNegTokenInit
