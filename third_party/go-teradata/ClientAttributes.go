// Copyright 2021 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"crypto/fips140"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

const PCLCLIATT_HOSTNAME                        =  7 // ClientTdHostName VARCHAR(128)
const PCLCLIATT_PROCTHRID                       =  8 // ClientProcThreadId VARCHAR(128)
const PCLCLIATT_SYSUSERID                       =  9 // ClientSystemUserId VARCHAR(128)
const PCLCLIATT_PROGNAME                        = 10 // ClientProgramName VARCHAR(1024)
const PCLCLIATT_OSNAME                          = 11 // ClientOsName VARCHAR(128)
const PCLCLIATT_VMUSERID                        = 12 // ClientVmUserId VARCHAR(128) - unused by driver
const PCLCLIATT_JOBNAME                         = 13 // ClientJobName VARCHAR(128)
const PCLCLIATT_ENVNAME                         = 14 // ClientEnvName VARCHAR(128)
const PCLCLIATT_UIDSECPROD                      = 15 // ClientSecProdUserId VARCHAR(128) - unused by driver
const PCLCLIATT_GRPSECPROD                      = 16 // ClientSecProdGrp VARCHAR(128)
const PCLCLIATT_COORDNAME                       = 17 // ClientCoordName VARCHAR(128)
const PCLCLIATT_TRANID                          = 18 // ClientTransactionId VARCHAR(128) - unused by driver
const PCLCLIATT_TERMID                          = 19 // ClientTerminalId VARCHAR(128)
const PCLCLIATT_USEROPERID                      = 20 // ClientUserOperId VARCHAR(128)
const PCLCLIATT_JOBID                           = 21 // ClientJobId VARCHAR(128)
const PCLCLIATT_VMNAME                          = 22 // ClientVmName VARCHAR(128)
const PCLCLIATT_TDPRELID                        = 23 // ClientTDPReleaseId VARCHAR(128) - unused by driver
const PCLCLIATT_CLIV2RELID                      = 24 // ClientCLIv2ReleaseId VARCHAR(128) - unused by driver
const PCLCLIATT_SESSDESC                        = 25 // ClientSessionDesc VARCHAR(128)
const PCLCLIATT_WORKLOAD                        = 26 // ClientWorkload VARCHAR(128)
const PCLCLIATT_JOBDATA                         = 27 // ClientJobData VARCHAR(128)
const PCLCLIATT_CLIENTKIND                      = 28 // ClientInterfaceKind VARCHAR(1)
const PCLCLIATT_CLIENTVERSION                   = 29 // ClientInterfaceVersion VARCHAR(16)
const PCLCLIATT_ClIENTATTRIBUTESEX              = 30 // ClientAttributesEx VARCHAR(512)
const PCLCLIATT_CLIENT_IPADDR_BY_CLIENT         = 31 // ClientIPAddrByClient VARCHAR(45)
const PCLCLIATT_CLIENT_PORT_BY_CLIENT           = 32 // ClientPortByClient INTEGER
const PCLCLIATT_SERVER_IPADDR_BY_CLIENT         = 33 // ServerIPAddrByClient VARCHAR(45)
const PCLCLIATT_SERVER_PORT_BY_CLIENT           = 34 // ServerPortByClient INTEGER
const PCLCLIATT_COP_SUFFIXED_HOST_NAME          = 45 // ClientCOPSuffixedHostName VARCHAR(128)
const PCLCLIATT_TDP_SESSION_POOL_NAME           = 51 // ClientTDSessionPoolName VARCHAR(128)
const PCLCLIATT_CLIENT_CONFIDENTIALITY_TYPE     = 58 // ClientConfType CHAR(1)
const PCLCLIATT_END                             = 32767

var G_sGoPlatform = runtime.GOOS + " " + runtime.GOARCH
var G_sGoRuntime  = runtime.Version () + " " + G_sGoPlatform

type clientAttributesExtra struct {
	m_sIdpProxyUsed          string
	m_sIdpProtocol           string
	m_sIdpCipher             string
	m_sIdpCertStatus         string
	m_sOIDCMetadataURL       string
	m_sOIDCClientID          string
	m_sLogonOIDCScope        string
	m_sOIDCLoginHint         string
	m_sOIDCTokenKind         string
	m_sOIDCRedirectPort      string
	m_sTokenCacheStatus      string
	m_sTokenReuseCount       string
	m_sRefreshTokenAvail     string
	m_sTokenTTLSecs          string
	m_sJWSAlgorithm          string
	m_sJWTHeaderParams       string
	m_sOAuthReuseCounts      string
	m_sOAuthRefreshAvail     string
	m_sOAuthScopes           string
}

func composeClientAttributesParcel (con *TeradataConnection) [] byte {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter composeClientAttributesParcel %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave composeClientAttributesParcel %v", con)) } ()
	}

	sGoStack := simpleGoStack (" ")

	sProgramNameAndStack := getOSProgramName (con) + ternary (con.m_params.M_sClientStack != "", " ", "") + con.m_params.M_sClientStack + ternary (sGoStack != "", " ", "") + sGoStack

	aabyOutput := [][] byte {}

	aabyOutput = serializeStringAttribute (con, aabyOutput, PCLCLIATT_HOSTNAME,  con.m_params.M_sHostName)
	aabyOutput = serializeStringAttribute (con, aabyOutput, PCLCLIATT_PROCTHRID, itoa (getOSProcessID ()))
	aabyOutput = serializeStringAttribute (con, aabyOutput, PCLCLIATT_SYSUSERID, getOSUserName ())
	aabyOutput = serializeStringAttribute (con, aabyOutput, PCLCLIATT_PROGNAME,  sProgramNameAndStack)
	aabyOutput = serializeStringAttribute (con, aabyOutput, PCLCLIATT_OSNAME,    coalesce (con.m_params.M_sClientOsName, G_sGoPlatform))
	aabyOutput = serializeStringAttribute (con, aabyOutput, PCLCLIATT_VMNAME,    coalesce (con.m_params.M_sClientVmName, G_sGoRuntime))

	bFIPSMode := fips140.Enabled ()

	sSecurityDesc := joinStrings (getCryptoLibDesc (), " ", getNativeCryptoLib (con.m_params))

	aabyOutput = serializeStringAttribute (con, aabyOutput, PCLCLIATT_GRPSECPROD, sSecurityDesc)

	aabyOutput = condSerializeStringAttribute (con, aabyOutput, PCLCLIATT_COORDNAME, con.m_sProxyUsed)

	aabyOutput = condSerializeStringAttribute (con, aabyOutput, PCLCLIATT_TERMID, con.m_extra.m_sIdpProxyUsed)

	sDatabaseTLSProtocol, sDatabaseTLSCipher := getTLSProtocol (con.m_socket)

	aabyOutput = serializeStringAttribute (con, aabyOutput, PCLCLIATT_SESSDESC,
		"C=" + yn (con.m_params.M_sSSLCipher != "") + ";" +
		ternary (sDatabaseTLSCipher != "", "D=" + sDatabaseTLSCipher + ";", "") + // omit if not available
		ternary (con.m_extra.m_sIdpCipher != "", "I=" + con.m_extra.m_sIdpCipher + ";", "")) // omit if not available

	aabyOutput = serializeStringAttribute (con, aabyOutput, PCLCLIATT_CLIENTKIND, coalesce (con.m_params.M_sClientKind, "G")) // G = Go, P = Python, R = R, S = Node.js

	aabyOutput = serializeStringAttribute (con, aabyOutput, PCLCLIATT_CLIENTVERSION, G_sVersionNumber)

	aabyOutput = condSerializeStringAttribute (con, aabyOutput, PCLCLIATT_ENVNAME,    con.m_extra.m_sOIDCMetadataURL)
	aabyOutput = condSerializeStringAttribute (con, aabyOutput, PCLCLIATT_JOBID,      con.m_extra.m_sOIDCClientID)
	aabyOutput = condSerializeStringAttribute (con, aabyOutput, PCLCLIATT_JOBNAME,    con.m_extra.m_sLogonOIDCScope)
	aabyOutput = condSerializeStringAttribute (con, aabyOutput, PCLCLIATT_JOBDATA,    con.m_extra.m_sOIDCLoginHint)
	aabyOutput = condSerializeStringAttribute (con, aabyOutput, PCLCLIATT_USEROPERID, con.m_extra.m_sOIDCTokenKind)

	aabyOutput = condSerializeStringAttribute (con, aabyOutput, PCLCLIATT_WORKLOAD, con.m_extra.m_sOAuthScopes)

	aabyOutput = serializeStringAttribute (con, aabyOutput, PCLCLIATT_ClIENTATTRIBUTESEX,
		con.m_params.M_sClientExtra +
		ternary (con.m_params.M_sApplicationESN != "",
			"AS=" + con.m_params.M_sApplicationESN + ";", "") +
			"BA=" + yn (con.m_params.M_sLogMech == "BROWSER") + ";" +
			"CCS=UTF8;" +
			"CERT=" + coalesce (con.m_sCertStatus, "U") + ";" +
			"CF=" + format10Uint16 (con.m_params.M_uConnectFunction) + ";" +
			"CRC=" + con.m_params.M_sSSLCRC + ";" +
			"CRL=" + yn (con.m_params.M_bSSLCRL) + ";" +
		ternary (con.m_params.M_sControlESN != "",
			"CS=" + con.m_params.M_sControlESN + ";", "") +
		ternary (con.m_params.M_sDatabaseLSN != "",
			"DL=" + con.m_params.M_sDatabaseLSN + ";", "") +
			"DP=" + format10Uint32 (con.m_params.M_uDbsPort) + ";" +
		ternary (con.m_params.M_uLogonSequenceNumber != 0,
			"EL=" + format10Uint32 (con.m_params.M_uLogonSequenceNumber) + ";", "") +
			"ENC=" + yn (con.m_params.M_bEncryptData) + ";" +
			"ES=" + format10Uint32 (con.m_uSessionNumber) + ";" +
			"FIPS=" + yn (bFIPSMode) + ";" +
			"GD=" + G_sGoDistribution + ";" +
			"GO=" + G_sGoRuntime + ";" +
			"GOV=" + yn (con.m_params.M_bGovern) + ";" +
			"HP=" + format10Uint32 (con.m_params.M_uHTTPSPort) + ";" +
			"HR=" + format10Uint32 (con.m_params.M_uHTTPSRetry) + "," + itoa (con.m_nHTTPSRetries) + ";" +
		ternary (con.m_extra.m_sIdpCertStatus != "",
			"IDPC=" + con.m_extra.m_sIdpCertStatus + ";", "") +
		ternary (con.m_extra.m_sJWTHeaderParams != "",
			"JH=" + con.m_extra.m_sJWTHeaderParams + ";", "") +
		ternary (con.m_extra.m_sJWSAlgorithm != "",
			"JWS=" + con.m_extra.m_sJWSAlgorithm + ";", "") +
			"LM=" + con.m_params.M_sLogMech + ";" +
			"LOB=" + yn (con.m_params.M_bLOBSupport && con.m_config.M_uMultipartParcelAndLOBSupport > 0) + ";" +
			"OA=" + format10Uint32 (con.m_params.M_uOAuthLevel) + ";" +
		ternary (con.m_extra.m_sOAuthReuseCounts != "",
			"OAC=" + con.m_extra.m_sOAuthReuseCounts + ";", "") +
		ternary (con.m_extra.m_sOAuthRefreshAvail != "",
			"OAR=" + con.m_extra.m_sOAuthRefreshAvail + ";", "") +
		ternary (con.m_extra.m_sTokenCacheStatus != "",
			"OC=" + con.m_extra.m_sTokenCacheStatus + ";", "") +
			"OCSP=" + yn (con.m_params.M_bSSLOCSP) + ";" +
			"OP=" + yn (con.m_params.M_sOIDCPrompt != "") + ";" +
			"OSL=" + itoa (con.m_params.m_nOIDCSSLModeLevel) + ";" +
			"OSM=" + coalesce (con.m_params.M_sOIDCSSLMode, "DEFAULT") + ";" +
			"PART=" + con.m_params.M_sPartition + ";" +
			"RED=" + format10Uint32 (con.m_params.M_uRedrive) + "," + format10Uint32 (con.databaseCapableOfRedriveLevel ()) + ";" +
		ternary (con.m_extra.m_sOIDCRedirectPort != "",
			"RP=" + con.m_extra.m_sOIDCRedirectPort + ";", "") +
		ternary (con.m_extra.m_sRefreshTokenAvail != "",
			"RT=" + con.m_extra.m_sRefreshTokenAvail + ";", "") +
			"SC=" + itoa (con.m_nConnectAttempts) + "," + itoa (con.m_nConnectFailures) + ";" +
			"SCS=UTF8;" +
			"SIP=" + yn (con.m_params.M_bSIPSupport && con.m_config.M_uSIPResponseSupport > 0) + ";" +
			"SSL=" + itoa (con.m_params.m_nSSLModeLevel) + ";" +
			"SSLM=" + con.m_params.M_sSSLMode + ";" +
			"SSLP=" + con.m_params.M_sSSLProtocol + ";" +
		ternary (con.m_extra.m_sTokenReuseCount != "",
			"TC=" + con.m_extra.m_sTokenReuseCount + ";", "") +
			"TM=" + ternary (con.isAnsiMode (), "A", "T") + ";" +
		ternary (con.m_extra.m_sTokenTTLSecs != "",
			"TT=" + con.m_extra.m_sTokenTTLSecs + ";", "") +
			"TVD=" + sDatabaseTLSProtocol + ";" +
		ternary (con.m_extra.m_sIdpProtocol != "",
			"TVI=" + con.m_extra.m_sIdpProtocol + ";", "") +
		"")

	aabyOutput = serializeStringAttribute (con, aabyOutput, PCLCLIATT_CLIENT_IPADDR_BY_CLIENT, hostFromAddr (con.m_socket.LocalAddr ()))

	uLocalPort, _ := parse10Uint16 (portFromAddr (con.m_socket.LocalAddr ()))
	aabyOutput = append (aabyOutput,
		twoBytes (PCLCLIATT_CLIENT_PORT_BY_CLIENT),
		twoBytes (2),
		twoBytes (uLocalPort),
	)

	aabyOutput = serializeStringAttribute (con, aabyOutput, PCLCLIATT_SERVER_IPADDR_BY_CLIENT, hostFromAddr (con.m_socket.RemoteAddr ()))

	uRemotePort, _ := parse10Uint16 (portFromAddr (con.m_socket.RemoteAddr ()))
	aabyOutput = append (aabyOutput,
		twoBytes (PCLCLIATT_SERVER_PORT_BY_CLIENT),
		twoBytes (2),
		twoBytes (uRemotePort),
	)

	bConnectedWithIpAddress           := con.m_sConnectedHostName == ""
	bConnectedWithOriginalHostName    := con.m_sConnectedHostName == con.m_params.M_sHostName
	bConnectedWithCopSuffixedHostName := ! bConnectedWithIpAddress && ! bConnectedWithOriginalHostName
	if bConnectedWithCopSuffixedHostName {
		aabyOutput = serializeStringAttribute (con, aabyOutput, PCLCLIATT_COP_SUFFIXED_HOST_NAME, con.m_sConnectedHostName)
	}

	aabyOutput = condSerializeStringAttribute (con, aabyOutput, PCLCLIATT_TDP_SESSION_POOL_NAME, con.m_params.M_sClientDataSource)

	// The SecurityPolicyParcel is returned in the Connect Response message, after the Client Attributes are sent in the Connect Request message.
	// It is not possible to determine whether TDGSS Encryption will be used because the values from the SecurityPolicyParcel are not available when composing the Connect Request message.
	// In particular, the SecurityPolicyParcel's Confidentiality Required value is not available here, when composing the Connect Request message.
	// While C TeraGSS indicates to the caller whether the QOP dictates encryption, TeraGSSJava does not, so the JDBC Driver cannot easily determine ahead of time whether encryption will be used.
	// The best we can do here is indicate whether the connection parameter was specified or not. When the connection parameter is not specified, then TDGSS encryption depends on central administration.
	bUsingTDGSSEncryption := ! isTLSSocket (con.m_socket) && con.m_params.M_bEncryptData

	byClientConfType := func () byte { switch {

		case con.m_params.m_nSSLModeLevel == LEVEL_VERIFY_FULL:
			return 'V' // TLS used for encryption, with full certificate verification

		case con.m_params.m_nSSLModeLevel == LEVEL_VERIFY_CA:
			return 'C' // TLS used for encryption, with just CA certificate verification

		case isTLSSocket (con.m_socket):
			return 'R' // TLS used for encryption, with no certificate verification

		case con.m_params.m_nSSLModeLevel <= LEVEL_ALLOW:
			if bUsingTDGSSEncryption { return 'E' } // TLS was not attempted. TDGSS used for encryption.
			return 'U' // TLS was not attempted. TDGSS encryption depends on central administration.

		default:
			if bUsingTDGSSEncryption { return 'F' } // TLS was attempted, but the TLS handshake failed, so this is a fallback to using TDGSS for encryption.
			return 'H' // SSLMODE was set to PREFER, but a legacy port socket connection was made. TDGSS encryption depends on central administration.
		} } ()

	aabyOutput = append (aabyOutput,
		twoBytes (PCLCLIATT_CLIENT_CONFIDENTIALITY_TYPE),
		twoBytes (1),
		[] byte { byClientConfType },
	)

	aabyOutput = append (aabyOutput,
		twoBytes (PCLCLIATT_END),
		twoBytes (0), // attribute body length is zero
	)

	return makeParcel (PCLCLIENTATTRIBUTES, aabyOutput ...)

} // end composeClientAttributesParcel

func getCryptoLibDesc () string {

	buildinfo, bSuccess := debug.ReadBuildInfo ()
	if bSuccess {
		for _, module := range buildinfo.Deps {
			if strings.HasSuffix (module.Path, "/crypto") {
				return module.Path + " " + module.Version
			}
		}
	}

	return "crypto-version-unavailable"

} // end getCryptoLibDesc

func serializeStringAttribute (con *TeradataConnection, aabyInput [][] byte, nAttributeID uint16, sValue string) [][] byte {

	// Limit the size of each string, to ensure that the parcel stays under 64KB and can use standard parcel header.
	sValue = substringToRune (sValue, 1000)

	aabyTail := [][] byte {
		twoBytes (nAttributeID),
		twoBytes (uint16 (1 + len (sValue))), // attribute body length: charset code, and value
		[] byte { CHARSET_CODE_UTF8 },
		con.serializeChar (sValue),
	}

	return append (aabyInput, aabyTail ...)

} // end serializeStringAttribute

func condSerializeStringAttribute (con *TeradataConnection, aabyInput [][] byte, nAttributeID uint16, sValue string) [][] byte {

	if sValue == "" {
		return aabyInput
	}

	return serializeStringAttribute (con, aabyInput, nAttributeID, sValue)

} // end condSerializeStringAttribute
