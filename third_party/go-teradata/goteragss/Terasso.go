// Copyright 2021 by Teradata Corporation. All Rights Reserved.
package goteragss

import (
	"bytes"
	"encoding/asn1"
	"encoding/binary"
	"fmt"
	"strings"
)

/** Implement Constants **/

/* Success and Error Codes */
const TERASSO_SUCCESS = 0                   /* no error */
const TERASSO_MEM_FAIL = 1200               /* couldn't acquire memory on behalf of caller */
const TERASSO_BADPARM_FAIL = 1201           /* problem in passed parameter(s) */
const SSPI_LOADSECDLL_FAIL = 1202           /* DEPRECATED: couldn't load security DLL. */
const SSPI_PROCSECDLL_FAIL = 1203           /* DEPRECATED: couldn't find security entry point. */
const SSPI_INITSECDLL_FAIL = 1204           /* DEPRECATED: couldn't initialize security DLL. */
const TERASSO_ENUMSECPKG_FAIL = 1205        /* DEPRECATED: call to EnumerateSecurityPackages failed */
const TERASSO_SECPKGMATCH_FAIL = 1206       /* couldn't find a compatible package match */
const SSPI_ACQCREDHNDL_FAIL = 1207          /* DEPRECATED: SSPI call to AcquireCredentialsHandle failed */
const TERASSO_BADSEQ_FAIL = 1208            /* DEPRECATED: gss_call or gss_term issued before gss_init */
const TERASSO_INITSECCNTXT_FAIL = 1209      /* DEPRECATED: call to InitializeSecurityContext failed */
const TERASSO_COMPAUTHTKN_FAIL = 1210       /* DEPRECATED: call to CompleteAuthToken failed */
const TERASSO_FREEBUFCNTXT_FAIL = 1211      /* DEPRECATED: call to FreeBufferContext failed */
const TERASSO_BADAUTHRESP_FAIL = 1212       /* bad authresp parcel */
const TDAT_LOADSECDLL_FAIL = 1213           /* couldn't load gss library/dll */
const TDAT_PROCSECDLL_FAIL = 1214           /* DEPRECATED: couldn't get specified function entry point */
const TERASSO_MECHUNAVAILABLE = 1215        /* user specified mechanism is not available */
const TERASSO_CHARSETNOTSUPP = 1216         /* desired mech char set is not supported */

/* TD Codes */

const TERASSO_CS_RAWDATA_CODE = 0
const TERASSO_CS_UTF8_CODE = 191  /* 0xBF */
const TERASSO_CS_UTF16_CODE = 190 /* 0xBE */

/* Mech Data Constants */

const MAX_LOGMECH_DATA_LEN = 65535 /* Max length for mech data */

/* Parcel flavor */

const PCLAUTHMECH uint16 = 167

/* Parcel mechanism configuration extension */

const (
	PCLGTWEXTNONE    = 0
	PCLGTWEXTSELFDEF = 1
)

/* Parcel mechanism property data IDs */

const MECHANISMPROPERTIES_DEFAULTMECHANISM_VALUE = 16
const MECHANISMPROPERTIES_DEFAULTNEGOTIATINGMECHANISM_VALUE = 112
const MECHANISMPROPERTIES_DHCIBYPASS_VALUE = 129

/** End Implement Constants **/

type terassoSecurity struct {
	logger                    TdgssLogger
	secContext                gssp
	credHdl                   []byte
	targetName                string
	allMechs                  map[/*mechOidString*/ string] *mechanismInfo
	commonMechs               map[/*mechName     */ string] *mechanismInfo
	serverDefaultMechName     string
	serverDefaultNegoMechName string
	desiredCharSet            asn1.ObjectIdentifier
	desiredServicesMask       uint32
	actualServices            uint32
	actualCtxTime             uint32
	actualCredTime            uint32
	minStat                   uint32
	ioBuffersLocal            uint32
	usingCache                uint32
	gtwNegomechLevel          uint32
	maxTokenSize              uint32
	userLgnMechName           string
	userLgnData               string
	terassoStatus             []byte
	libraryInitialized        bool
}

var _ Terasso = &terassoSecurity{} // implements Terasso

type TdgssLogger interface {
	HasTraceLog() bool
	HasTimingLog() bool
	HasDebugLog() bool
	TraceLog(a string)
	TimingLog(a string)
	DebugLog(a string)
}

type Terasso interface {
	FormatAvailableMechs() string
	GssAppendCommonMechsList(authRespBuf []byte, bTLSSocket bool) (status TdgssStatus)
	GssSetUserMechInfo(userLgnMechName string, userLgnData string) (status TdgssStatus)
	GssSetMechCharSet(mechCharSetCode byte) (status TdgssStatus)
	GssGetActualMechName() (status TdgssStatus, mechName string)
	GssSetGtwNegomechLevel(gtwNegomechLevel uint32) (status TdgssStatus)
	GssGenCredFromLogon() (genCredFromLogon bool)
	GssInit(TDPid string, userName string, password string) (status TdgssStatus, maxTokenSize uint32)
	GssCall(in []byte) (status TdgssStatus, out []byte)
	GssTerm() (status TdgssStatus)
	GssWrap(confReqFlag int, qopReq uint32, in []byte) (status TdgssStatus, confState int, out []byte)
	GssUnwrap(in []byte) (status TdgssStatus, confState int, qopState uint32, out []byte)
}

func NewTerasso(loggerArg TdgssLogger) Terasso {
	return &terassoSecurity {
		logger:              loggerArg,
		desiredCharSet:      GSS_C_NT_TDAT_MECHDATA,
		desiredServicesMask: 0xFFFFFFFF,
		allMechs:            makeMechanismInfoMap (),
		commonMechs:         map[string] *mechanismInfo {},
	}
}

// String interface
func (sec *terassoSecurity) String() string { return fmt.Sprintf("terassoSecurity:%p", sec) }

func (sec *terassoSecurity) FormatAvailableMechs() string { return fmt.Sprintf("default=%v mechs=%v", sec.serverDefaultMechName, sec.commonMechs) }

// Terasso interface

// GssAppendCommonMechsList populates this terassoSecurity's commonMechs list with the mechanism retrieved from
// the input authMechParcel. The mechanism is added to the list if only if it is supported by the client (i.e.
// it is present in the allMechs map).
//
// The common mechanism names (e.g. "TD2", "LDAP", etc.) are stored in the commonMechs list. The Gateway returns
// one parcel per supported mechanism.
//
// Example of an authMechParcel (TD2):
//
//    Parcel, raw:
//      00 a7 00 31 00 00 00 01 00 00 00 0d 2b 06 01 04
//      01 81 3f 01 87 74 01 01 09 00 10 00 0c 00 00 00
//      03 00 00 00 01 00 11 00 0c 00 00 00 01 00 00 00
//      14
//
//    Parcel, annotated:
//      00 a7 ----------------------------------------> Parcel flavor
//      00 31 ----------------------------------------> Parcel length
//      00 00 00 01 ----------------------------------> Mechanism configuration extension
//      00 00 00 0d ----------------------------------> Mechanism OID length
//      2b 06 01 04 01 81 3f 01 87 74 01 01 09 -------> Mechanism OID
//      00 10 00 0c 00 00 00 03 00 00 00 01 ----------> Mechanism property #1
//      00 11 00 0c 00 00 00 01 00 00 00 14 ----------> Mechanism property #2
//      00 81 00 0c 00 00 00 03 00 00 00 01 ----------> Mechanism property #3
func (sec *terassoSecurity) GssAppendCommonMechsList(authMechParcel []byte, bTLSSocket bool) (status TdgssStatus) {

	// Enable logging for this function.
	if sec.logger.HasTraceLog() {
		sec.logger.TraceLog(fmt.Sprintf("> enter GssAppendCommonMechsList %v", sec))
		defer func() { sec.logger.TraceLog(fmt.Sprintf("< leave GssAppendCommonMechsList %v", sec)) }()
	}

	// Ensure the input parcel is non-empty.
	if len(authMechParcel) == 0 {
		status.Major = GSS_S_FAILURE
		status.Minor = TERASSO_BADPARM_FAIL
		if (sec.logger.HasDebugLog()) {
			sec.logger.DebugLog(fmt.Sprintf("GssAppendCommonMechsList failed: The input authentication mechanism parcel is empty."))
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Walk through the input authMechParcel,
	// parsing out the parcel flavor in the first
	// two bytes.
	pclBuf := bytes.NewBuffer(authMechParcel)
	pclFlavor := pclBuf.Next(2)
	if len(pclFlavor) != 2 {
		status.Major = GSS_S_FAILURE
		status.Minor = TERASSO_BADPARM_FAIL
		if (sec.logger.HasDebugLog()) {
			sec.logger.DebugLog(fmt.Sprintf("GssAppendCommonMechsList failed: Unable to retrieve the parcel flavor contained in the authentication mechanism parcel."))
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Nothing for us to do if the parcel flavor is not a PCLAUTHMECH.
	// Simply return with success.
	var flavor uint16
	binary.Read(bytes.NewBuffer(pclFlavor), binary.BigEndian, &flavor)
	if PCLAUTHMECH != flavor {
		status.Major = GSS_S_COMPLETE
		status.Minor = TERASSO_SUCCESS
		return
	}

	// Next, retrieve the parcel
	// length in the next two bytes...
	pclLen := pclBuf.Next(2)
	if len(pclLen) != 2 {
		status.Major = GSS_S_FAILURE
		status.Minor = TERASSO_BADPARM_FAIL
		if (sec.logger.HasDebugLog()) {
			sec.logger.DebugLog(fmt.Sprintf("GssAppendCommonMechsList failed: Unable to retrieve the authentication mechanism parcel's length."))
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	var nRemainingParcelBytes uint16
	binary.Read(bytes.NewBuffer(pclLen), binary.BigEndian, &nRemainingParcelBytes)
	nRemainingParcelBytes -= uint16 (len(pclFlavor) + len(pclLen))
	if (pclBuf.Len() < int(nRemainingParcelBytes)) {
		status.Major = GSS_S_FAILURE
		status.Minor = TERASSO_BADPARM_FAIL
		if (sec.logger.HasDebugLog()) {
			sec.logger.DebugLog(fmt.Sprintf("GssAppendCommonMechsList failed: length of authparcel is larger than buffer.")) // REWORD
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// ...and then the mechanism configuration
	// extension in the next four bytes...
	pclMechConfigExt := pclBuf.Next(4)
	if len(pclMechConfigExt) != 4 {
		status.Major = GSS_S_FAILURE
		status.Minor = TERASSO_BADPARM_FAIL
		if (sec.logger.HasDebugLog()) {
			sec.logger.DebugLog(fmt.Sprintf("GssAppendCommonMechsList failed: Unable to retrieve the mechanism configuration extension contained in the authentication mechanism parcel."))
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	nRemainingParcelBytes -= uint16 (len(pclMechConfigExt))

	// ...then the length of the mechanism
	// OID in the next four bytes...
	pclMechOidLen := pclBuf.Next(4)
	if len(pclMechOidLen) != 4 {
		status.Major = GSS_S_FAILURE
		status.Minor = TERASSO_BADPARM_FAIL
		if (sec.logger.HasDebugLog()) {
			sec.logger.DebugLog(fmt.Sprintf("GssAppendCommonMechsList failed: Unable to retrieve the length of the mechanism OID contained in the authentication mechanism parcel."))
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	nRemainingParcelBytes -= uint16 (len(pclMechOidLen))

	// ...and finally, the mechanism OID in the next pclMechOidLen bytes.
	// For this, we must first convert the value stored in pclMechOidLen
	// from a []byte to an int in order to correctly read the Next() bytes
	// from the buffer.
	var oidLen uint32
	binary.Read(bytes.NewBuffer(pclMechOidLen), binary.BigEndian, &oidLen)
	pclRawMechOid := pclBuf.Next(int(oidLen))
	if len(pclRawMechOid) != int(oidLen) {
		status.Major = GSS_S_FAILURE
		status.Minor = TERASSO_BADPARM_FAIL
		if (sec.logger.HasDebugLog()) {
			sec.logger.DebugLog(fmt.Sprintf("GssAppendCommonMechsList failed: Unable to retrieve the mechanism OID contained in the authentication mechanism parcel."))
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	nRemainingParcelBytes -= uint16 (oidLen)

	// Pre-pend the tag type (asn1.TagOID == 6) and the
	// length of the mechanism OID to the raw mechanism
	// OID parsed from the parcel. This will allow us
	// to obtain the string format of the mechanism
	// OID further down.
	var mechOid []byte
	mechOid = append(mechOid, asn1.TagOID, byte(oidLen))
	mechOid = append(mechOid, pclRawMechOid...)

	// Get the mech string representation of the
	// mechOid.
	var mechUnmarshaled asn1.ObjectIdentifier
	_, err := asn1.Unmarshal(mechOid, &mechUnmarshaled)
	if err != nil {
		status.Major = GSS_S_FAILURE
		if (sec.logger.HasDebugLog()) {
			sec.logger.DebugLog(fmt.Sprintf("GssAppendCommonMechsList failed: Unable to unmarshal the mechanism OID."))
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}

	mechOidString := mechUnmarshaled.String()
	mechInfo, exists := sec.allMechs [mechOidString]
	if (sec.logger.HasDebugLog()) {
		sec.logger.DebugLog(fmt.Sprintf("GssAppendCommonMechsList mechOidString: %s exists: %v", mechOidString, exists))
	}

	// Simply skip the mechanism from the parcel if it is not supported by the client.
	if ! exists {
		status.Major = GSS_S_COMPLETE
		status.Minor = TERASSO_SUCCESS
		return
	}

	// If the mechanism from the parcel is present in the client's map of supported mechanisms,
	// then add the mechanismInfo to the map of commonMechs.
	mechName := mechInfo.mechName
	sec.commonMechs[mechName] = mechInfo

	if (sec.logger.HasDebugLog()) {
		sec.logger.DebugLog(fmt.Sprintf("GssAppendCommonMechsList mechname: %s", mechName))
	}

	// Retrieve the properties if the mechanism configuration extension is
	// PCLGTWEXTSELFDEF.
	var mechConfigExt uint32
	binary.Read(bytes.NewBuffer(pclMechConfigExt), binary.BigEndian, &mechConfigExt)
	if mechConfigExt == PCLGTWEXTSELFDEF {
		// Loop through the mechanism properties contained in the parcel.
		// We're looking for whether the mechanism is a DefaultMechanism
		// or a DefaultNegotiatingMechanism.
		for nRemainingParcelBytes > 0 {
			// Serialize the parcel's mechanism properties contained
			// in the remainder of the pclBuf into a mechanismProperty
			// struct.
			mechProp := authMechProperty{}
			err := binary.Read(pclBuf, binary.BigEndian, &mechProp)
			if err != nil {
				status.Major = GSS_S_FAILURE
				status.Minor = TERASSO_BADPARM_FAIL
				if (sec.logger.HasDebugLog()) {
					sec.logger.DebugLog(fmt.Sprintf("GssAppendCommonMechsList failed: Unable to binary.Read() the mechanism properties contained in the authentication mechanism parcel (err: %v).", err))
					sec.logger.DebugLog(formatMajorStatusErrOnly(status))
				}
				status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
				return
			}
			nRemainingParcelBytes -= AUTH_MECH_PROPERTY_LENGTH

			// If the mechanism is indeed either a DefaultMechanism or a
			// DefaultNegotiatingMechanism, save the mechanism name in
			// this terassoSecurity struct.
			switch mechProp.PropDataId {
			case MECHANISMPROPERTIES_DEFAULTMECHANISM_VALUE:
				if mechProp.PropValue != 0 {
					sec.serverDefaultMechName = mechName
				}

			case MECHANISMPROPERTIES_DEFAULTNEGOTIATINGMECHANISM_VALUE:
				if mechProp.PropValue != 0 {
					sec.serverDefaultNegoMechName = mechName
				}

			case MECHANISMPROPERTIES_DHCIBYPASS_VALUE:
				if mechProp.PropValue != 0 {
					// Bypass DHCI if the server & mechanism support this and using TLS Socket.
					if bTLSSocket && mechInfo.mechProperties.CIBypassSupported {
						mechInfo.desiredServices |= TDGSS_C_CI_BYPASS_FLAG
					}
				}

			} // End switch dataId...
			bytesleft := int(mechProp.PropLength - AUTH_MECH_PROPERTY_LENGTH)
			if bytesleft > 0 {
				pclBuf.Next(bytesleft)
			}
		} // End " for pclBuf.Len() > 0..."
	} // End "if mechConfigExt == PCLGTWEXTSELFDEF..."

	status.Major = GSS_S_COMPLETE
	status.Minor = TERASSO_SUCCESS
	return
}

func (sec *terassoSecurity) GssSetUserMechInfo(userLgnMechName string, userLgnData string) (status TdgssStatus) {

	if sec.logger.HasTraceLog() {
		sec.logger.TraceLog(fmt.Sprintf("> enter GssSetUserMechInfo %v", sec))
		defer func() { sec.logger.TraceLog(fmt.Sprintf("< leave GssSetUserMechInfo %v", sec)) }()
	}

	sec.userLgnMechName = userLgnMechName

	if len(userLgnData) > MAX_LOGMECH_DATA_LEN {
		status.Major = TERASSO_BADPARM_FAIL
		if sec.logger.HasDebugLog() {
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	sec.userLgnData = userLgnData

	if sec.logger.HasDebugLog() {
		sec.logger.DebugLog(fmt.Sprintf("GssSetUserMechInfo: userLgnMechName = %s, userLgnData = %s", sec.userLgnMechName, sec.userLgnData))
	}

	return
}

func (sec *terassoSecurity) GssSetMechCharSet(mechCharSetCode byte) (status TdgssStatus) {

	if sec.logger.HasTraceLog() {
		sec.logger.TraceLog(fmt.Sprintf("> enter GssSetMechCharSet %v", sec))
		defer func() { sec.logger.TraceLog(fmt.Sprintf("< leave GssSetMechCharSet %v", sec)) }()
	}

	switch mechCharSetCode {
	case TERASSO_CS_RAWDATA_CODE:
		sec.desiredCharSet = GSS_C_NT_TDAT_MECHDATA
	case TERASSO_CS_UTF8_CODE:
		sec.desiredCharSet = GSS_C_NT_TDAT_MECHDATA_UTF8
	case TERASSO_CS_UTF16_CODE:
		sec.desiredCharSet = GSS_C_NT_TDAT_MECHDATA_UTF16
	default:
		status.Major = TERASSO_CHARSETNOTSUPP
	}

	return
}

func (sec *terassoSecurity) GssGetActualMechName() (status TdgssStatus, mechName string) {

	if sec.logger.HasTraceLog() {
		sec.logger.TraceLog(fmt.Sprintf("> enter GssGetActualMechName %v", sec))
		defer func() { sec.logger.TraceLog(fmt.Sprintf("< leave GssGetActualMechName %v", sec)) }()
	}

	mechName = sec.userLgnMechName
	status.Major = GSS_S_COMPLETE
	return
}

func (sec *terassoSecurity) GssSetGtwNegomechLevel(gtwNegomechLevel uint32) (status TdgssStatus) {

	if sec.logger.HasTraceLog() {
		sec.logger.TraceLog(fmt.Sprintf("> enter GssSetGtwNegomechLevel %v", sec))
		defer func() { sec.logger.TraceLog(fmt.Sprintf("< leave GssSetGtwNegomechLevel %v", sec)) }()
	}

	sec.gtwNegomechLevel = gtwNegomechLevel
	status.Major = GSS_S_COMPLETE
	return
}

func (sec *terassoSecurity) GssGenCredFromLogon() (genCredFromLogon bool) {

	if sec.logger.HasTraceLog() {
		sec.logger.TraceLog(fmt.Sprintf("> enter GssGenCredFromLogon %v", sec))
		defer func() { sec.logger.TraceLog(fmt.Sprintf("< leave GssGenCredFromLogon %v", sec)) }()
	}

	userLgnMechName := strings.ToUpper(strings.TrimSpace(sec.userLgnMechName))
	if userLgnMechName == gTdnegoMechName {
		secCtx := sec.secContext.(*tdnegoSecContext)
		if (secCtx.m_selectedCtx != nil) {
			genCredFromLogon = secCtx.m_selectedCtx.gsspGetMechInfo ().mechProperties.GenerateCredentialFromLogon
			return
		}
	}

	if mechInfo, exists := sec.commonMechs[userLgnMechName] ; exists {
		genCredFromLogon = mechInfo.mechProperties.GenerateCredentialFromLogon
		return
	}
	return
}

func (sec *terassoSecurity) GssInit(TDPid string, userName string, password string) (status TdgssStatus, maxTokenSize uint32) {

	if sec.logger.HasTraceLog() {
		sec.logger.TraceLog(fmt.Sprintf("> enter GssInit %v", sec))
		defer func() { sec.logger.TraceLog(fmt.Sprintf("< leave GssInit %v", sec)) }()
	}

	/* NOTE: gssInit can be called multiple times in a row during reconnect attempts */
	/* Initialize the elements of the security structure that don't live across      */
	/* connections                                                                   */
	sec.actualServices = 0
	sec.actualCtxTime = 0
	sec.actualCredTime = 0
	sec.minStat = GSS_S_COMPLETE
	sec.maxTokenSize = 0
	sec.ioBuffersLocal = 0
	sec.terassoStatus = nil
	sec.credHdl = nil
	sec.targetName = ""
	sec.secContext = nil

	if (sec.logger.HasDebugLog()) {
		sec.logger.DebugLog(fmt.Sprintf("GssInit: sec.commonMechs = %v", sec.commonMechs))
	}

	userLgnMechName := strings.ToUpper(strings.TrimSpace(sec.userLgnMechName))
	mechInfo, ok := sec.commonMechs[userLgnMechName]
	if ! ok {
		status.Major = TERASSO_MECHUNAVAILABLE
		if (sec.logger.HasDebugLog()) {
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	status, sec.secContext = newSecContext(sec.logger, mechInfo)

	if status.Major != GSS_S_COMPLETE {
		status.Major = TERASSO_MECHUNAVAILABLE
		if (sec.logger.HasDebugLog()) {
			sec.logger.DebugLog(fmt.Sprintf("GssInit: Failed to create security context for mech = %s", sec.userLgnMechName))
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	status = sec.tdgssConfigure(CLIENT)
	if status.Major != GSS_S_COMPLETE {
		status.Major = TERASSO_MECHUNAVAILABLE
		if (sec.logger.HasDebugLog()) {
			sec.logger.DebugLog(fmt.Sprintf("GssInit: configure failed for mech = %s", sec.userLgnMechName))
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	/***********************/
	/* create desired name */
	/***********************/

	var desiredName string
	if len(userName) > 0 && len(password) > 0 {

		spn := getSPN(userName, password, sec.userLgnData)
		if sec.logger.HasDebugLog() {
			sec.logger.DebugLog(fmt.Sprintf("GssInit: direct sign-on spn = [%s]", spn))
		}

		desiredName = spn

	} else if len(sec.userLgnData) > 0 {

		if sec.logger.HasDebugLog() {
			sec.logger.DebugLog(fmt.Sprintf("GssInit: externally authenticated logon spn = [%s]", sec.userLgnData))
		}
		desiredName = sec.userLgnData
	} else {

		if sec.logger.HasDebugLog() {
			sec.logger.DebugLog(fmt.Sprintf("GssInit: desiredName not initialized"))
		}
	}

	if sec.logger.HasDebugLog() {
		sec.logger.DebugLog(fmt.Sprintf("GssInit: userLgnMechName = %s", sec.userLgnMechName))
	}

	status = sec.tdgssAcquireCred(desiredName)

	/**********************/
	/* create target name */
	/**********************/
	/* post-V2R6: TDPid is always supplied */
	if len(TDPid) > 0 {
		if strings.EqualFold(sec.userLgnMechName, gKrb5MechName) || strings.EqualFold(sec.userLgnMechName, gTdnegoMechName) {
			/* initialize target name */
			sec.targetName = "TERADATA/" + TDPid
			if sec.logger.HasDebugLog() {
				sec.logger.DebugLog(fmt.Sprintf("GssInit: sec.targetName = %s", sec.targetName))
			}
		}
	}

	if status.Major != GSS_S_COMPLETE {
		if sec.logger.HasDebugLog() {
			sec.logger.DebugLog(fmt.Sprintf("GssInit: status.Major = %d, status.Minor = %d", status.Major, status.Minor))
			sec.logger.DebugLog(formatMajorStatusErrOnly(status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
	}
	return
}

func (sec *terassoSecurity) GssCall(in []byte) (status TdgssStatus, out []byte) {

	if sec.logger.HasTraceLog() {
		sec.logger.TraceLog(fmt.Sprintf("> enter GssCall %v", sec))
		defer func() { sec.logger.TraceLog(fmt.Sprintf("< leave GssCall %v", sec)) }()
	}

	status, out = sec.tdgssInitSecContext(in)
	return
}

func (sec *terassoSecurity) GssTerm() (status TdgssStatus) {

	if sec.logger.HasTraceLog() {
		sec.logger.TraceLog(fmt.Sprintf("> enter GssTerm %v", sec))
		defer func() { sec.logger.TraceLog(fmt.Sprintf("< leave GssTerm %v", sec)) }()
	}

	status = sec.tdgssRelease()
	return
}

func (sec *terassoSecurity) GssWrap(confReqFlag int, qopReq uint32, in []byte) (status TdgssStatus, confState int, out []byte) {

	if sec.logger.HasTraceLog() {
		sec.logger.TraceLog(fmt.Sprintf("> enter GssWrap %v", sec))
		defer func() { sec.logger.TraceLog(fmt.Sprintf("< leave GssWrap %v", sec)) }()
	}

	status, confState, out = sec.tdgssWrap(confReqFlag, qopReq, in)
	return
}

func (sec *terassoSecurity) GssUnwrap(in []byte) (status TdgssStatus, confState int, qopState uint32, out []byte) {

	if sec.logger.HasTraceLog() {
		sec.logger.TraceLog(fmt.Sprintf("> enter GssUnwrap %v", sec))
		defer func() { sec.logger.TraceLog(fmt.Sprintf("< leave GssUnwrap %v", sec)) }()
	}

	status, confState, qopState, out = sec.tdgssUnwrap(in)
	return
}

func getSPN(userName string, password string, logdata string) (spn string) {
	/* Allow "myuser@@password user=myuser profile=myprofile" -> */
	if len(userName) > 0 && len(password) > 0 {

		/* escape '\b', '\n', '\t', '@', '/', '\' in the password -> */

		escapedPassword := strings.ReplaceAll(password, `\`, `\\`)  // DO THIS FIRST

		escapedPassword = strings.ReplaceAll(escapedPassword, "\b", `\b`)
		escapedPassword = strings.ReplaceAll(escapedPassword, "\n", `\n`)
		escapedPassword = strings.ReplaceAll(escapedPassword, "\t", `\t`)
		escapedPassword = strings.ReplaceAll(escapedPassword, "@", `\@`)
		escapedPassword = strings.ReplaceAll(escapedPassword, "/", `\/`)

		quotedUserName := false
		quotedPassword := false

		tempUserName := userName
		tempPassword := escapedPassword

		if strings.HasPrefix(userName, `"`) {
			quotedUserName = true
			tempUserName = tempUserName[1:len(userName) - 1]
		}

		if strings.HasPrefix(password, `"`) {
			quotedPassword = true
			tempPassword = tempPassword[1:len(escapedPassword) - 1]
		}

		if quotedUserName || quotedPassword {
			spn = `"` + tempUserName + "@@" + tempPassword + `"`
		} else {
			spn = tempUserName + "@@" + tempPassword
		}

		if len(logdata) > 0 {
			/* for "user=myuser profile=myprofile" */
			spn = spn + " " + logdata
		}
	}
	return
}
