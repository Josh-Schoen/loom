// Copyright 2021 by Teradata Corporation. All Rights Reserved.
package goteragss

import (
	"bytes"
	"encoding/asn1"
	"encoding/binary"
	"fmt"
)

type mechanismInfo struct {
	/***************************************************************
	*  Information Directly From The Configuration File
	***************************************************************/
	/* Mechanism attributes */
	mechName          string
	libraryName       string
	mechPrefix        string
	interfaceType     string
	mechOid           asn1.ObjectIdentifier
	mechProperties    mechanismProperties
	negotiatedMechs   [] *mechanismInfo

	/***************************************************************
	*  Derived Information
	***************************************************************/
	/* 14.0 and later QOP info (Default, Low, Medium, High) */
	defaultQOPs []*algQOPEx
	lowQOPs     []*algQOPEx
	mediumQOPs  []*algQOPEx
	highQOPs    []*algQOPEx

	/* Pre-14.0 QOP info (uses first legacy QOP only) */
	legacyQopCount byte
	legacyQopValue int
	legacyQopInfo  qopInfo

	/* Miscellaneous derived information */
	libHandle       []byte
	mechPropBuffer  []byte /* returned by tdgss_inquire_prop_for_mech */
	enabled         bool
	useMech         int  /* Enabled plus no other problems */
	defaultMech     bool /* TRUE if this is the default mech */
	repackageBuffer int  /* TRUE if mech supplies _release_buffer */
	repackageOidSet int  /* TRUE if mech supplies _release_oid_set */
	desiredServices uint32
}

type mechanismProperties struct {
	AuthenticationSupported      bool
	AuthorizationSupported       bool
	SingleSignOnSupported        bool
	NegotiationSupported         bool
	DefaultNegotiatingMechanism  bool
	DefaultMechanism             bool
	MechanismEnabled             bool
	MechanismRank                int
	MechanismIgnoresQop          bool
	GenerateCredentialFromLogon  bool
	DelegateCredentials          bool
	MutualAuthentication         bool
	ReplayDetection              bool
	OutOfSequenceDetection       bool
	ConfidentialityDesired       bool
	IntegrityDesired             bool
	AnonymousAuthentication      bool
	DesiredContextTime           uint32
	DesiredCredentialTime        uint32
	CredentialUsage              int
	CredentialIsUPN              bool
	VerifyDHKey                  bool
	LdapNeedcanon                bool
	JWTDecryptionKeyFile         string
	JWTVerificationKeyFile       string
	JWTSkewTime                  uint32
	JWTKeyDirectory              string
	JWTDynamicKey                bool
	JWTTokenExchange             bool
	JWTClientTlsCACertDir        string
	JWTKeyCacheRefreshTime       uint32
	JWTClientUseTls              bool
	JWTRestAPITimeLimit          uint32
	JWTRestAPIMaxTimeAllowed     uint32
	LdapServerName               string
	LdapServerPort               uint32
	LdapServerRealm              string
	LdapSystemFQDN               string
	LdapBaseFQDN                 string
	LdapGroupBaseFQDN            string
	LdapUserBaseFQDN             string
	LdapClientReferrals          string
	LdapClientDeref              string
	LdapClientDebug              uint32
	LdapClientRebindAuth         bool
	LdapClientRandomDevice       string
	LdapClientMechanism          string
	LdapClientUseTls             bool
	LdapServiceFQDN              string
	LdapServicePasswordProtected bool
	LdapServicePassword          string
	LdapClientSaslSecProps       string
	UseLdapConfig                bool
	TeradataKeyTab               string
	CIBypassSupported            bool
}

const AUTH_MECH_PROPERTY_LENGTH = 12

type authMechProperty struct {
	PropDataId   uint16
	PropLength   uint16
	PropDataType uint32
	PropValue    uint32
}

type negMechanismInfo struct {
	enabled        bool
	negotiatedMech string
}

type tdgssChannelBindings struct {
	initiatorAddrType uint32
	initiatorAddress  string
	acceptorAddrType  uint32
	acceptorAddress   string
	applicationData   []byte
}

var gTd2DefaultQops = GetQops(gTd2DefaultQopsList)
var gLdapDefaultQops = GetQops(gLdapDefaultQopsList)
var gJwtDefaultQops = GetQops(gJwtDefaultQopsList)

var gTd2LegacyQopInfo = GetTd2LegacyQop()
var gLdapLegacyQopInfo = GetLdapLegacyQop()

var gTd2MechProperties = mechanismProperties{
	AuthenticationSupported:     false,
	AuthorizationSupported:      false,
	SingleSignOnSupported:       false,
	NegotiationSupported:        false,
	DefaultNegotiatingMechanism: false,
	DefaultMechanism:            false,
	MechanismEnabled:            true,
	MechanismRank:               20,
	MechanismIgnoresQop:         false,
	GenerateCredentialFromLogon: false,
	DelegateCredentials:         false,
	MutualAuthentication:        true,
	ReplayDetection:             true,
	OutOfSequenceDetection:      true,
	ConfidentialityDesired:      true,
	IntegrityDesired:            true,
	AnonymousAuthentication:     false,
	DesiredContextTime:          0,
	DesiredCredentialTime:       0,
	CredentialUsage:             0,
	VerifyDHKey:                 false,
	CIBypassSupported:           true,
}

var gLdapMechProperties = mechanismProperties{
	AuthenticationSupported:     true,
	AuthorizationSupported:      false,
	SingleSignOnSupported:       false,
	NegotiationSupported:        false,
	DefaultNegotiatingMechanism: false,
	DefaultMechanism:            false,
	MechanismEnabled:            true,
	MechanismRank:               70,
	MechanismIgnoresQop:         false,
	GenerateCredentialFromLogon: true,
	DelegateCredentials:         false,
	MutualAuthentication:        true,
	ReplayDetection:             true,
	OutOfSequenceDetection:      true,
	ConfidentialityDesired:      true,
	IntegrityDesired:            true,
	AnonymousAuthentication:     false,
	DesiredContextTime:          0,
	DesiredCredentialTime:       0,
	CredentialUsage:             0,
	CredentialIsUPN:             true,
	VerifyDHKey:                 false,
}

var gJwtMechProperties = mechanismProperties{
	AuthenticationSupported:     true,
	AuthorizationSupported:      false,
	SingleSignOnSupported:       true,
	NegotiationSupported:        false,
	DefaultNegotiatingMechanism: false,
	DefaultMechanism:            false,
	MechanismEnabled:            true,
	MechanismRank:               30,
	MechanismIgnoresQop:         false,
	GenerateCredentialFromLogon: true,
	DelegateCredentials:         false,
	MutualAuthentication:        false,
	ReplayDetection:             true,
	OutOfSequenceDetection:      true,
	ConfidentialityDesired:      true,
	IntegrityDesired:            true,
	AnonymousAuthentication:     false,
	CIBypassSupported:           true,
	DesiredContextTime:          0,
	DesiredCredentialTime:       0,
	CredentialUsage:             0,
	JWTDecryptionKeyFile:        "",
	JWTVerificationKeyFile:      "",
	JWTSkewTime:                 300,
	JWTKeyDirectory:             "",
	JWTDynamicKey:               false,
	JWTTokenExchange:            false,
	JWTClientTlsCACertDir:       "",
	JWTKeyCacheRefreshTime:      1440,
	JWTClientUseTls:             true,
	JWTRestAPITimeLimit:         10,
	JWTRestAPIMaxTimeAllowed:    20,
}

var gKrb5MechProperties = mechanismProperties{
	AuthenticationSupported:      true,
	AuthorizationSupported:       false,
	SingleSignOnSupported:        true,
	NegotiationSupported:         false,
	DefaultNegotiatingMechanism:  false,
	DefaultMechanism:             false,
	MechanismEnabled:             true,
	MechanismRank:                40,
	MechanismIgnoresQop:          true,
	GenerateCredentialFromLogon:  true,
	DelegateCredentials:          true,
	MutualAuthentication:         true,
	ReplayDetection:              true,
	OutOfSequenceDetection:       true,
	ConfidentialityDesired:       true,
	IntegrityDesired:             true,
	AnonymousAuthentication:      false,
	DesiredContextTime:           0,
	DesiredCredentialTime:        0,
	CredentialUsage:              0,
	LdapServerName:               "",
	LdapServerPort:               389,
	LdapServerRealm:              "",
	LdapSystemFQDN:               "",
	LdapBaseFQDN:                 "",
	LdapGroupBaseFQDN:            "",
	LdapUserBaseFQDN:             "",
	LdapClientReferrals:          "off",
	LdapClientDeref:              "never",
	LdapClientDebug:              0,
	LdapClientRebindAuth:         true,
	LdapClientRandomDevice:       "/dev/urandom",
	LdapClientMechanism:          "simple",
	LdapClientUseTls:             false,
	LdapServiceFQDN:              "",
	LdapServicePasswordProtected: false,
	LdapServicePassword:          "",
	LdapClientSaslSecProps:       "",
	UseLdapConfig:                false,
	TeradataKeyTab:               "/etc/teradata.keytab",
}

var gTdnegoMechProperties = mechanismProperties{
	AuthenticationSupported:      true,  // cannot be overridden
	AuthorizationSupported:       true,  // cannot be overridden
	SingleSignOnSupported:        true,  // cannot be overridden
	NegotiationSupported:         true,  // cannot be overridden
	DefaultNegotiatingMechanism:  false, // can be overridden
	DefaultMechanism:             false, // can be overridden
	MechanismEnabled:             true,  // can be overridden - if false TDNEGO cannot be used
	MechanismRank:                10,    // can be overridden
	GenerateCredentialFromLogon:  true,  // cannot be overridden
	DelegateCredentials:          false,
	MutualAuthentication:         true,  // cannot be overridden
	ReplayDetection:              true,  // cannot be overridden
	OutOfSequenceDetection:       true,  // cannot be overridden
	ConfidentialityDesired:       true,  // cannot be overridden
	IntegrityDesired:             true,  // cannot be overridden
	AnonymousAuthentication:      false,
	//TeradataKeyTab:               "/etc/teradata.keytab",
}

var gTd2MechName = "TD2"
var gTd2MechOid = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 191, 1, 1012, 1, 1, 9}

var gLdapMechName = "LDAP"
var gLdapMechOid = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 191, 1, 1012, 1, 20}

var gJwtMechName = "JWT"
var gJwtMechOid = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 28698, 4, 302, 1, 4}

var gKrb5MechName = "KRB5"
var gKrb5MechOid = asn1.ObjectIdentifier{1, 2, 840, 113554, 1, 2, 2}

var gTdnegoMechName = "TDNEGO"
var gTdnegoMechOid = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 28698, 4, 302, 1, 3}

var gMechOidStringToMechName = map[/*mechOidString*/ string] /*mechName*/ string {
	gTd2MechOid.String():    gTd2MechName,
	gLdapMechOid.String():   gLdapMechName,
	gJwtMechOid.String():    gJwtMechName,
	gKrb5MechOid.String():   gKrb5MechName,
	gTdnegoMechOid.String(): gTdnegoMechName,
}

func makeMechanismInfoMap () map [/*mechOidString*/ string] *mechanismInfo {

	miTd2 := &mechanismInfo {
		mechName:        gTd2MechName,
		libraryName:     "gssp2td2",
		mechPrefix:      "TD2",
		interfaceType:   "teradata",
		mechOid:         gTd2MechOid,
		mechProperties:  gTd2MechProperties,
		defaultQOPs:     gTd2DefaultQops,
		legacyQopCount:  1,
		legacyQopValue:  0,
		legacyQopInfo:   gTd2LegacyQopInfo,
		desiredServices: getDesiredServices(gTd2MechProperties),
	}

	miLdap := &mechanismInfo {
		mechName:        gLdapMechName,
		libraryName:     "gssp2ldap",
		mechPrefix:      "ldapv3",
		interfaceType:   "custom",
		mechOid:         gLdapMechOid,
		mechProperties:  gLdapMechProperties,
		defaultQOPs:     gLdapDefaultQops,
		legacyQopCount:  1,
		legacyQopValue:  0,
		legacyQopInfo:   gLdapLegacyQopInfo,
		desiredServices: getDesiredServices(gLdapMechProperties),
	}

	miJwt := &mechanismInfo {
		mechName:        gJwtMechName,
		libraryName:     "gssp2jwt",
		mechPrefix:      "JWT",
		interfaceType:   "custom",
		mechOid:         gJwtMechOid,
		mechProperties:  gJwtMechProperties,
		defaultQOPs:     gJwtDefaultQops,
		legacyQopCount:  0,
		legacyQopValue:  0,
		desiredServices: getDesiredServices(gJwtMechProperties),
	}

	miKrb5 := &mechanismInfo {
		mechName:        gKrb5MechName,
		libraryName:     "gssp2gss",
		mechPrefix:      "gssp2gss",
		interfaceType:   "gss",
		mechOid:         gKrb5MechOid,
		mechProperties:  gKrb5MechProperties,
		legacyQopCount:  0,
		legacyQopValue:  0,
		desiredServices: getDesiredServices(gKrb5MechProperties),
	}

	miTdnego := &mechanismInfo {
		mechName:        gTdnegoMechName,
		libraryName:     "gssp2tdnego",
		mechPrefix:      "TDNEGO",
		interfaceType:   "negotiate",
		mechOid:         gTdnegoMechOid,
		mechProperties:  gTdnegoMechProperties,
		//legacyQopCount:  0,
		//legacyQopValue:  0,
		//legacyQopInfo:   gLdapLegacyQopInfo,
		desiredServices: getDesiredServices (gTdnegoMechProperties),
		negotiatedMechs: [] *mechanismInfo {
			miKrb5,
			miLdap,
			miTd2,
		},
	}

	return map [/*mechOidString*/ string] *mechanismInfo {
		gTd2MechOid.String()    : miTd2,
		gLdapMechOid.String()   : miLdap,
		gJwtMechOid.String()    : miJwt,
		gKrb5MechOid.String()   : miKrb5,
		gTdnegoMechOid.String() : miTdnego,
	}
}

/***************************************************************
* Build the trailer for the first token message.
* The trailer contains the Mech OID, legacy Qop OID, and the
* fixed trailer (legacy Qop count, trailer length including fixed
* part and the version)
***************************************************************/
func (mechInfo *mechanismInfo) buildTrailer(logger TdgssLogger) (status TdgssStatus, abyTrailer []byte) {
	// Get the mech OID.
	abyMech, err := asn1.Marshal(mechInfo.mechOid)
	if err != nil {
		status.Major = GSS_S_FAILURE
		status.Minor = TD2_ERR_TRAILERERROR
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("asn1.Marshal returned an error for mechOid: %v error: %v", mechInfo.mechOid, err))
		}
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}

	var abyQop []byte
	if mechInfo.legacyQopCount > 0 {
		// Get the legacy Qop.
		legacyQop := mechInfo.legacyQopInfo

		// This is not really an ASN1 OID. We are just using it to store 5 integers.
		legacyQopOid := asn1.ObjectIdentifier{0, 0, int(legacyQop.algQop.cipher), int(legacyQop.algQop.keySize), int(legacyQop.algQop.mode), int(legacyQop.algQop.padding), int(legacyQop.algQop.digest), int(legacyQop.mechQop)}
		var err error
		abyQop, err = asn1.Marshal(legacyQopOid)

		if err != nil {
			status.Major = GSS_S_FAILURE
			status.Minor = TD2_ERR_TRAILERERROR
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("asn1.Marshal returned an error for legacyQopOid: %v error: %v", legacyQopOid, err))
			}
			status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
			return
		}

		// The tag is aplication.
		abyQop[0] |= GSSP_TAG_APPLICATION
	}

	// Get the fixed trailer.
	trailerFixed := gsspTrailerFixed{
		QopOidCount:    mechInfo.legacyQopCount,
		TrailerLength:  uint32(TDGSS_TRAILER_FIXED_LENGTH + len(abyMech) + len(abyQop)),
		TrailerVersion: TDGSS_TRAILER_VERSION,
	}

	var buf bytes.Buffer
	err = binary.Write(&buf, binary.BigEndian, trailerFixed)
	if err != nil {
		status.Major = GSS_S_FAILURE
		status.Minor = TD2_ERR_TRAILERERROR
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("binary.Write returned an error for fixed Trailer: %v error: %v", trailerFixed, err))
		}
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}

	abyTrailer = concat(abyMech, abyQop, buf.Bytes())
	status.Major = GSS_S_COMPLETE
	status.Minor = TDGSS_S_COMPLETE
	return
}

/***************************************************************
* Given a mechanismProperties, return the desired services
* based on the property attributes.
***************************************************************/
func getDesiredServices(mechProps mechanismProperties) (desiredServices uint32) {

	desiredServices = 0

	if mechProps.DelegateCredentials {
		desiredServices |= GSS_C_DELEG_FLAG
	}
	if mechProps.MutualAuthentication {
		desiredServices |= GSS_C_MUTUAL_FLAG
	}
	if mechProps.ReplayDetection {
		desiredServices |= GSS_C_REPLAY_FLAG
	}
	if mechProps.OutOfSequenceDetection {
		desiredServices |= GSS_C_SEQUENCE_FLAG
	}
	if mechProps.ConfidentialityDesired {
		desiredServices |= GSS_C_CONF_FLAG
	}
	if mechProps.IntegrityDesired {
		desiredServices |= GSS_C_INTEG_FLAG
	}
	if mechProps.AnonymousAuthentication {
		desiredServices |= GSS_C_ANON_FLAG
	}

	return
}
