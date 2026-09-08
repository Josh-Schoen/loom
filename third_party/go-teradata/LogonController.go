// Copyright 2025 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/Teradata-TIO/go-teradata/goteragss"
	"net"
	"strings"
	"sync"
	"time"
)

type Reconnect struct {
	uCryptoNonce     uint64
	uRequestNumber   uint32
}

var mutexAppendMech sync.Mutex

func (con *TeradataConnection) configMessageHandshake (socket net.Conn, ctxLogin context.Context, uConfigCryptoNonce uint64) (abyConfigResponseMsg [] byte, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter configMessageHandshake %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave configMessageHandshake %v", con)) } ()
	}

	bWantControlData := con.m_params.M_uRedrive >= 1
	bWantRNP         := con.m_params.M_uRedrive >= 2
	bWantRedrive     := con.m_params.M_uRedrive >= 3

	aabyClientConfig := [][] byte {}

	aabyClientConfig = append (aabyClientConfig, fourBytes (1)) // Field IsSelfDefining: 1 Yes

	aabyClientConfig = append (aabyClientConfig, makeFeature (PCLCLIENTCONFIGTDGSS, [] byte {
		goteragss.GoTeraGSSMajorVersion,
		goteragss.GoTeraGSSMinorVersion,
		goteragss.GoTeraGSSMaintVersion,
		goteragss.GoTeraGSSEfixVersion,
	}))

	auVersionParts := parseAllUint32 (G_sVersionNumber)
	sTTUVersion := fmt.Sprintf ("TTU %02d.%02d.00", /*Major*/ auVersionParts [0], /*Minor*/ auVersionParts [1])

	aabyClientConfig = append (aabyClientConfig, makeFeature (PCLCLIENTCONFIGTTUVERSION, [] byte (sTTUVersion)))

	if bWantControlData {
		aabyClientConfig = append (aabyClientConfig, makeFeature (PCLCLIENTCONFIGCONTROLDATA))
		aabyClientConfig = append (aabyClientConfig, makeFeature (PCLCLIENTCONFIGSECURITYPOLICY, [] byte { 1 })) // Security Policy Level = 1
	}

	if bWantRNP {
		aabyClientConfig = append (aabyClientConfig, makeFeature (PCLCLIENTCONFIGRECOVERABLEPROTOCOL))
	}

	if bWantRedrive {
		aabyClientConfig = append (aabyClientConfig, makeFeature (PCLCLIENTCONFIGREDRIVE))
	}

	aabyClientConfig = append (aabyClientConfig, makeFeature (PCLCLIENTCONFIGESS, [] byte { 1 })) // ESS level = 1
	aabyClientConfig = append (aabyClientConfig, makeFeature (PCLCLIENTCONFIGNEGOMECH, [] byte { 1 })) // 1 = Supports negotiation mechanism
	aabyClientConfig = append (aabyClientConfig, makeFeature (PCLCLIENTCONFIGIDP))
	aabyClientConfig = append (aabyClientConfig, makeFeature (PCLCLIENTCONFIGTLS))

	// Config message handshake is always required by the Gateway, omission causes Error 8019 The parcel stream is invalid.

	abyConfigRequestMsg := con.makeRequestMessage (MESSAGE_KIND_CONFIG, uConfigCryptoNonce, /*uRequestNumber*/ 0, makeParcel (PCLCLIENTCONFIG, aabyClientConfig ...))

	abyConfigResponseMsg, _, err = con.sendAndReceive (&NetworkIO { socket: socket, ctx: ctxLogin, abyMsgToSend: abyConfigRequestMsg })
	return

} // end configMessageHandshake

func (con *TeradataConnection) logonController (socket net.Conn, ctxLogin context.Context, reconnect *Reconnect) (err error) {

	bConfigOnly := socket != nil

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter logonController %v reconnect=%v bConfigOnly=%v", con, reconnect != nil, bConfigOnly))
		defer func () { traceLog (fmt.Sprintf ("< leave logonController %v reconnect=%v bConfigOnly=%v", con, reconnect != nil, bConfigOnly)) } ()
	}

	nStartTimeMs := currentTimeMillis ()

	if socket == nil { socket = con.m_socket }

	uConfigCryptoNonce    := NEW_CRYPTO_NONCE
	uAssignCryptoNonce    := NEW_CRYPTO_NONCE
	byAssignKind          := MESSAGE_KIND_ASSIGN
	byConnectKind         := MESSAGE_KIND_CONNECT
	uConnectRequestNumber := uint32 (0)

	if reconnect != nil {
		uConfigCryptoNonce    = reconnect.uCryptoNonce
		uAssignCryptoNonce    = reconnect.uCryptoNonce
		byAssignKind          = MESSAGE_KIND_REASSIGN
		byConnectKind         = MESSAGE_KIND_RECONNECT
		uConnectRequestNumber = reconnect.uRequestNumber
	}

	bHaveHostname := con.m_params.M_sHostName != "-none"
	if bHaveHostname {

		var abyConfigResponseMsg [] byte
		abyConfigResponseMsg, err = con.configMessageHandshake (socket, ctxLogin, uConfigCryptoNonce)
		if err != nil { return }

		aabyParcelHeaders := getParcelHeadersFromMessage (abyConfigResponseMsg)

		if len (aabyParcelHeaders) == 0 || cookedParcelFlavor (aabyParcelHeaders [0]) != PCLCONFIGRESP {
			err = con.makeDriverError (nil, 0, "The Teradata Database did not provide the expected Config Response parcel")
			return
		}

		// Begin code that must be thread-safe with respect to the connection, because it is executed by multiple LCC threads.

		con.m_config = parseConfigResponse (con, constrainedParcelBody (aabyParcelHeaders [0])) // atomic swap

		for i, abyParcelHeader := range aabyParcelHeaders {

			uFlavor := cookedParcelFlavor (abyParcelHeader)

			if con.hasDebugLog () {
				debugLog (fmt.Sprintf ("Parcel %v of %v %v %v", i + 1, len (aabyParcelHeaders), parcelFlavor (uFlavor), uFlavor))
			}

			switch uFlavor {

				case PCLAUTHMECH:

					mutexAppendMech.Lock () // critical section for append mech

					status := con.m_pSecurity.GssAppendCommonMechsList (abyParcelHeader, isTLSSocket (socket))

					mutexAppendMech.Unlock ()

					if status.Major == goteragss.GSS_S_COMPLETE {
						if con.hasDebugLog () {
							debugLog ("GssAppendCommonMechsList SUCCESS")
						}
					} else {
						err = con.makeChainedSecurityError (status)
						return
					}

				case PCLGTWCONFIG:

					con.m_gtwConfig = parseGtwConfig (con, abyParcelHeader) // atomic swap

			} // end switch
		} // end for
	} // end if bHaveHostname

	// End of code that must be thread-safe with respect to the connection, because it is executed by multiple LCC threads.

	if bConfigOnly { return }

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("Available mechs %v", con.m_pSecurity.FormatAvailableMechs ()))
	}

	// Set initial values for TDGSS LogMech and LogData, which may be changed by OIDC flow below.
	con.m_sLogMechForTDGSS = con.m_params.M_sLogMech
	con.m_sLogDataForTDGSS = con.m_params.M_sLogData

	if con.hasDebugLog () {
		debugLog ("before: m_sLogMechForTDGSS=" + con.m_sLogMechForTDGSS + " m_sLogDataForTDGSS=" + con.m_sLogDataForTDGSS)
	}

	err = con.doAllNeededOIDCFlows () // do zero or more OIDC flows
	if err != nil { return }

	if con.hasDebugLog () {
		debugLog ("after: m_sLogMechForTDGSS=" + con.m_sLogMechForTDGSS + " m_sLogDataForTDGSS=" + con.m_sLogDataForTDGSS)
		debugLog ("after: m_setTokenFinders.Len=" + itoa (con.m_setTokenFinders.Len ()))
	}

	if ! bHaveHostname { return }

	nGatewayDeadlineMs := int (con.m_params.M_uGatewayDeadline * 1000)
	nEndTimeMs         := nStartTimeMs + nGatewayDeadlineMs
	nDeltaMs           := currentTimeMillis () - nEndTimeMs
	if nDeltaMs > 0 {

		if con.hasTimingLog () {
			timingLog ("Gateway deadline " + itoa (nGatewayDeadlineMs) + " ms exceeded by " + itoa (nDeltaMs) + " ms, will close socket on local port " + portFromAddr (socket.LocalAddr ()) + " and reconnect")
		}

		nStartTimeMs = currentTimeMillis ()

		// Close and reconnect the socket if the OIDC flow took longer than the Gateway denial-of-service protection timeout

		con.closeSocket (GRACEFUL, IGNORE_ERROR)

		err = con.connectToHost (ctxLogin, /*reconnect*/ nil) // connectToHost sets con.m_socket to the new socket
		if err != nil { return }

		socket = con.m_socket // set local variable to the new socket

		_, err = con.configMessageHandshake (socket, ctxLogin, uConfigCryptoNonce)
		if err != nil { return }

		if con.hasTimingLog () {
			timingLog ("Reconnecting socket on local port " + portFromAddr (socket.LocalAddr ()) + " took " + itoa (currentTimeMillis () - nStartTimeMs) + " ms")
		}
	} // end if nDeltaMs

	if con.m_sLogMechForTDGSS == "NONE" || con.m_params.M_bTokenMode {
		return
	}

	if con.hasDebugLog () {

		// Determine whether LOGDATA contains a JWT

		bHasToken := strings.HasPrefix (con.m_sLogDataForTDGSS, "token=")
		sJWT := ternary (bHasToken, strings.TrimPrefix (con.m_sLogDataForTDGSS, "token="), "")
		asParts := strings.Split (sJWT, ".")

		debugLog (fmt.Sprintf ("Original LOGMECH=%v Current LOGMECH=%v LOGDATA=%v (len=%v) bHasToken=%v len(asParts)=%v",
			con.m_params.M_sLogMech, con.m_sLogMechForTDGSS, con.m_sLogDataForTDGSS, len (con.m_sLogDataForTDGSS), bHasToken, len (asParts)))

		if len (asParts) == 3 {
			asTitles := [] string { "JWTHeader", "JWTPayload" }
			for i, sTitle := range asTitles {
				aby := [] byte {}
				aby, err = base64.RawURLEncoding.DecodeString (asParts [i])
				if err != nil {
					debugLog (fmt.Sprintf ("LOGMECH=%v cannot decode %v: %v", con.m_sLogMechForTDGSS, sTitle, err))
				} else {
					buf := bytes.Buffer {}
					json.Indent (&buf, aby, "", " ")
					debugLog (fmt.Sprintf ("Original LOGMECH=%v %v=%v", con.m_params.M_sLogMech, sTitle, buf.String ()))
				}
			} // end for i
		} // end if len
	} // end if hasDebugLog

	var status goteragss.TdgssStatus
	status = con.m_pSecurity.GssSetUserMechInfo (con.m_sLogMechForTDGSS, con.m_sLogDataForTDGSS)

	if status.Major == goteragss.GSS_S_COMPLETE {
		if con.hasDebugLog () {
			debugLog ("GssSetUserMechInfo SUCCESS")
		}
	} else {
		err = con.makeChainedSecurityError (status)
		return
	}

	status = con.m_pSecurity.GssSetMechCharSet (CHARSET_CODE_UTF8)

	if status.Major == goteragss.GSS_S_COMPLETE {
		if con.hasDebugLog () {
			debugLog ("GssSetMechCharSet SUCCESS")
		}
	} else {
		err = con.makeChainedSecurityError (status)
		return
	}

	status = con.m_pSecurity.GssSetGtwNegomechLevel (uint32 (con.m_gtwConfig.M_uNegoMech))
	if status.Major == goteragss.GSS_S_COMPLETE {
		if con.hasDebugLog () {
			debugLog ("GssSetGtwNegomechLevel SUCCESS")
		}
	} else {
		err = con.makeChainedSecurityError (status)
		return
	}

	// skip gss_set_desired_services

	sHostName := con.m_params.M_sHostName

	if con.m_sLogMechForTDGSS == "KRB5" || con.m_sLogMechForTDGSS == "TDNEGO" {

		sAddress := hostFromAddr (socket.RemoteAddr ())
		timeStart := time.Now ()
		var asHostNames [] string
		asHostNames, err = net.LookupAddr (sAddress) // reverse DNS lookup

		if con.hasTimingLog () {
			timingLog (fmt.Sprintf ("Reverse lookup for %v took %v and returned %v", sAddress, formatElapsed (timeStart),
				ternary (err != nil, fmt.Sprintf ("error %v", err), fmt.Sprintf ("%v address(es) %v", len (asHostNames), asHostNames))))
		}

		// ignore non-nil error and use the default hostname
		if err == nil {
			sHostName = strings.TrimRight (asHostNames [0], ".") // trim trailing period from FQDN
		}
	} // end if krb5 or tdnego

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("Using hostname %v", sHostName))
	}

	sQuotedUserName := con.m_params.M_sUserName
	if len (sQuotedUserName) > 0 {
		sQuotedUserName = doubleQuote (con.m_params.M_sUserName)
	}

	sQuotedPassword := con.m_params.M_sPassword
	if len (sQuotedPassword) > 0 {
		sQuotedPassword = doubleQuote (con.m_params.M_sPassword)
	}

	bMissingUsername   := len (con.m_params.M_sUserName) == 0
	bMissingPassword   := len (con.m_params.M_sPassword) == 0
	bMissingCredential := bMissingUsername || bMissingPassword
	bMissingLogData    := len (con.m_sLogDataForTDGSS) == 0

	if con.m_sLogMechForTDGSS == "TD2" && bMissingCredential {
		err = con.makeDriverError (nil, 544, "TD2 logon mechanism requires username and password")
		return
	} else if con.m_sLogMechForTDGSS == "LDAP" && bMissingCredential && bMissingLogData {
		err = con.makeDriverError (nil, 545, "LDAP logon mechanism requires username and password, or logdata")
		return
	}

	status, _ = con.m_pSecurity.GssInit (sHostName, sQuotedUserName, sQuotedPassword)

	if status.Major == goteragss.GSS_S_COMPLETE {
		if con.hasDebugLog () {
			debugLog ("GssInit SUCCESS")
		}
	} else {
		err = con.makeChainedSecurityError (status)
		return
	}

	bComplete := false
	abyToken := [] byte (nil)

	bComplete, abyToken, err = con.doGssCall (abyToken) // first
	if err != nil { return }

	//! if con.m_config.M_uExtendedObjectNamesInParcels < 2 && we are using TD2, then put the username into PCLASSIGN
	//! otherwise, PCLASSIGN username is all zeros

	var uTrip byte

	abyAssignRequestMsg := con.makeRequestMessage (byAssignKind, uAssignCryptoNonce, /*uRequestNumber*/ 0,

		makeParcel (PCLASSIGN,
			make ([] byte, 32)), // Field UserName = all zero bytes

		makeParcel (PCLSSOREQ,
			[] byte { /*AUTHNOTSPECIFIED*/ 0, uTrip },
			twoBytes (uint16 (len (abyToken))),
			abyToken))

	uTrip += 2

	abyAssignResponseMsg, _, err := con.sendAndReceive (&NetworkIO { ctx: ctxLogin, abyMsgToSend: abyAssignRequestMsg })
	if err != nil { return }

	con.parseAssignResponse (abyAssignResponseMsg)

	var abySSOResponseParcelBody [] byte

	aabyParcelHeaders := getParcelHeadersFromMessage (abyAssignResponseMsg)
	for i, abyParcelHeader := range aabyParcelHeaders {

		uFlavor := cookedParcelFlavor (abyParcelHeader)

		if con.hasDebugLog () {
			debugLog (fmt.Sprintf ("Parcel %v of %v %v %v", (i + 1), len (aabyParcelHeaders), parcelFlavor (uFlavor), uFlavor))
		}

		switch uFlavor {

			case PCLSSORESP:
				abySSOResponseParcelBody = constrainedParcelBody (abyParcelHeader)

			case PCLFAILURE, PCLERROR:
				err = con.makeDatabaseError (abyParcelHeader)
				return
		}
	} // end for

	if abySSOResponseParcelBody == nil {
		err = con.makeDriverError (nil, 0, "The Teradata Database did not provide the expected SSO Response parcel")
		return
	}

	skipByte (&abySSOResponseParcelBody) // method
	skipByte (&abySSOResponseParcelBody) // code
	skipByte (&abySSOResponseParcelBody) // trip
	skipByte (&abySSOResponseParcelBody) // must be zero
	uTokenLength := consumeShort (&abySSOResponseParcelBody)
	abyToken = consumeBytes (&abySSOResponseParcelBody, uint32 (uTokenLength))

	con.m_nDHKE = 0 // reset for reconnect

	if ! bComplete {
		bComplete, abyToken, err = con.doGssCall (abyToken) // second
		if err != nil { return }

		for (len (abyToken) != 0) { // repeatedly send SSO message while we get valid tokens
			abySSORequestMsg := con.makeRequestMessage (MESSAGE_KIND_SSO, NEW_CRYPTO_NONCE, /*uRequestNumber*/ 0,

				makeParcel (PCLSSOREQ,
					[] byte { /*AUTHNOTSPECIFIED*/ 0, uTrip },
					twoBytes (uint16 (len (abyToken))),
					abyToken))

				uTrip += 2

			var abySSOResponseMsg [] byte
			abySSOResponseMsg, _, err = con.sendAndReceive (&NetworkIO { ctx: ctxLogin, abyMsgToSend: abySSORequestMsg })
			if err != nil { return }

			abySSOResponseParcelBody = nil // reset

			aabyParcelHeaders = getParcelHeadersFromMessage (abySSOResponseMsg)
			for i, abyParcelHeader := range aabyParcelHeaders {

				uFlavor := cookedParcelFlavor (abyParcelHeader)

				if con.hasDebugLog () {
					debugLog (fmt.Sprintf ("Parcel %v of %v %v %v", (i + 1), len (aabyParcelHeaders), parcelFlavor (uFlavor), uFlavor))
				}

				switch uFlavor {

					case PCLSSORESP:
						abySSOResponseParcelBody = constrainedParcelBody (abyParcelHeader)
						con.m_nDHKE++

					case PCLFAILURE:
						err = con.makeDatabaseError (abyParcelHeader)
						return
				}
			} // end for

			if abySSOResponseParcelBody == nil {
				err = con.makeDriverError (nil, 0, "The Teradata Database did not provide the expected SSO Response parcel")
				return
			}

			if bComplete { break }

			skipByte (&abySSOResponseParcelBody) // method
			skipByte (&abySSOResponseParcelBody) // code
			skipByte (&abySSOResponseParcelBody) // trip
			skipByte (&abySSOResponseParcelBody) // must be zero
			uTokenLength = consumeShort (&abySSOResponseParcelBody)
			abyToken = consumeBytes (&abySSOResponseParcelBody, uint32 (uTokenLength))

			bComplete, abyToken, err = con.doGssCall (abyToken) // third
			if err != nil { return }

		} // end for abyToken
	} // end if ! bComplete

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("con.m_nDHKE=%v", con.m_nDHKE))
	}

	status, sMechName := con.m_pSecurity.GssGetActualMechName ()

	if status.Major == goteragss.GSS_S_COMPLETE {
		if con.hasDebugLog () {
			debugLog (fmt.Sprintf ("GssGetActualMechName SUCCESS sMechName=%v", sMechName))
		}
	} else {
		err = con.makeChainedSecurityError (status)
		return
	}

	byCheckWorkload := func () (byte) { if con.m_config.M_uCheckWorkloadSupport >= 1 {
		return '1'
	} else {
		return 0
	}} ()

	// Connection parameter   ControlData          RecoverableNP        Redrive              SessionOptions offset +6 Redrive
	// --------------------   ------------------   ------------------   ------------------   --------------------------------
	// redrive=0              do not use           do not use           do not use            0 (application has no preference)
	// redrive=1              use if DBS permits   do not use           do not use            0
	// redrive=2              use if DBS permits   use if DBS permits   do not use            0
	// redrive=3 (default)    use if DBS permits   use if DBS permits   use if DBS permits    0
	// redrive=4              use if DBS permits   use if DBS permits   use if DBS permits   'Y' (application explicitly asked for Redrive)

	byRedrive := func () (byte) { if con.m_params.M_uRedrive >= 4 && con.databaseCapableOfRedriveLevel () >= 3 {
		return 'Y' // application explicitly asked for Redrive
	} else {
		return 0 // application has not indicated its Redrive preference
	}} ()

	abyReturnCatalogName := func () [] byte {
		if con.m_config.M_uVSDSupport < 1 {
			return [] byte {} // omit ReturnCatalogName field when VSD support is unavailable
		} else if con.m_params.M_bLocalCatalog {
			return [] byte { 'Y' }
		} else {
			return [] byte { 'N' }
	}} ()

	aabyConnectRequestParcels := [][] byte {

		makeParcel (PCLLOGON, [] byte (con.composeCredString ())), // Field LogonString

		makeParcel (PCLSESSOPT, [] byte {
			con.m_params.M_sTMode [0],     //  +0 Field TransactionSemantics: D = Server default, A = ANSI, T = Teradata
			'N',                           //  +1 Field CommitProtocol: N = Not 2PC
			'N',                           //  +2 Field LanguageConformance: N = None
			'D',                           //  +3 Field DateForm: D = Server default
			'E',                           //  +4 Field EnhancedStatementStatus: E = Use Statement Status parcel
			byCheckWorkload,               //  +5 Field CheckWorkloadClientSupport
			byRedrive,                     //  +6 Field Redrive
			0,                             //  +7 Field ExtendedMultiLoad: 0 = unknown
			1,                             //  +8 Field EnhancedStatementStatusLevel: 1 = Use instead of Success parcels
			0},                            //  +9 Field Internal2PC for DBS use only
			abyReturnCatalogName,          // +10 Field ReturnCatalogName (only present when VSD support is available)
		),

		makeParcel (PCLCONNECT,
			fixedWidthBytes (con.m_params.M_sPartition, 16, ' '), // Field Partition, always in ASCII, padded with trailing ASCII spaces
			fourBytes (con.m_params.M_uLogonSequenceNumber), // Field LogonSequenceNumber
			twoBytes (con.m_params.M_uConnectFunction), // Field ConnectFunction
			twoBytes (0)), // unused
	}

	const LOGONSRC_ONLY           = 0
	const LOGONSRC_AND_CLIENTATTR = 1
	const CLIENTATTR_ONLY         = 2

	if con.m_config.M_uClientAttributesParcelSupport <= LOGONSRC_AND_CLIENTATTR {

		// Note: If the LogonSource string sent to the DBS is too long, the DBS will crash.
		// The client-specified portion is 98 characters maximum.
		// We will use only 97 characters at most, to provide a one-character fudge: 30 + 1 + 6 + 1 + 20 + 32 + 7 = 97

		sLogonSourceString := limitWidthUTF8 (con.m_params.M_sHostName, 30) + " " + itoa (getOSProcessID ()) + " " + limitWidthUTF8 (getOSUserName (), 20) + " " + limitWidthUTF8 (getOSProgramBaseName (con), 32) + " 01 LSS"

		aabyConnectRequestParcels = append (aabyConnectRequestParcels, makeParcel (PCLDATA, [] byte (sLogonSourceString)))
	}

	if con.m_config.M_uClientAttributesParcelSupport >= LOGONSRC_AND_CLIENTATTR {

		aabyConnectRequestParcels = append (aabyConnectRequestParcels, composeClientAttributesParcel (con))
	}

	abyConnectRequestMsg := con.makeRequestMessage (byConnectKind, NEW_CRYPTO_NONCE, uConnectRequestNumber, aabyConnectRequestParcels ...)

	abyConnectResponseMsg, _, err := con.sendAndReceive (&NetworkIO { ctx: ctxLogin, abyMsgToSend: abyConnectRequestMsg })
	if err != nil { return }

	err = con.processMessageSuccessOrError (abyConnectResponseMsg, /*bRequired*/ true)
	if err != nil { return }

	con.m_uLogonSequenceNumber = con.m_params.M_uLogonSequenceNumber
	aabyParcelHeaders = getParcelHeadersFromMessage (abyConnectResponseMsg)
	for _, abyParcelHeader := range aabyParcelHeaders {
		uFlavor := cookedParcelFlavor (abyParcelHeader)
		switch uFlavor {
			case PCLLSN:
				con.processLSNParcel (abyParcelHeader)
		}
	}

	con.m_bLoggedOn = true
	return

} // end method logonController

func (con *TeradataConnection) composeCredString () (sOutput string) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter composeCredString %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave composeCredString %v sOutput=%v", con, sOutput)) } ()
	}

	bGeneratedCredentials := con.m_pSecurity.GssGenCredFromLogon ()

	sUserName := con.m_params.M_sUserName
	sPassword := con.m_params.M_sPassword
	sAccount  := con.m_params.M_sAccount

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("composeCredString: m_bNoTransmitCredentials=%v bGeneratedCredentials=%v sUserName=%v sPassword=%v sAccount=%v",
			con.m_bNoTransmitCredentials,
			bGeneratedCredentials,
			sUserName,
			sPassword,
			sAccount))
	}

	// PCLLOGON must contain zero-length username and password strings if we're using generated credentials.
	// Use of generated credentials depends on whether external authentication is provided by the logon mechanism.
	// LDAP and KRB5 provide external authentication, but TD2 does not, so only LDAP and KRB5 can use generated credentials.
	// When using generated credentials (LDAP or KRB5), we don't pass the username and/or password, but must pass the account if it exists.
	// When using TD2, we must pass the username, password, and account if it exists.

	if con.m_bNoTransmitCredentials || bGeneratedCredentials {
		sUserName = ""
		sPassword = ""
	}

	// Return the empty string for no credentials.
	//
	// Return the following strings for one credential:
	//   username
	//   ,password
	//   ,,'account'
	//
	// Return the following strings for two credentials:
	//   username,password
	//   username,,'account'
	//   ,password,'account'
	//
	// Return the following string for all three credentials:
	//   username,password,'account'

	bHaveUsername := len (sUserName) > 0
	bHavePassword := len (sPassword) > 0
	bHaveAccount  := len (sAccount)  > 0

	if (bHaveUsername) {
		sOutput += doubleQuote (sUserName)
	}

	if (bHavePassword || bHaveAccount) {
		sOutput += ","
	}

	if (bHavePassword) {
		sOutput += doubleQuote (sPassword)
	}

	if (bHaveAccount) {
		sOutput += "," + singleQuote (sAccount)
	}

	return

} // end method composeCredString

func (con *TeradataConnection) doGssCall (abyInputToken [] byte) (bComplete bool, abyOutputToken [] byte, err error) {

	if con.hasDebugLog () {
		if abyInputToken == nil {
			debugLog ("doGssCall input token is nil")
		} else {
			debugLogHexDump ("doGssCall input token:", abyInputToken)
		}
	}

	var status goteragss.TdgssStatus
	status, abyOutputToken = con.m_pSecurity.GssCall(abyInputToken)

	bComplete = status.Major == goteragss.GSS_S_COMPLETE

	if status.Major == goteragss.GSS_S_COMPLETE {
		if con.hasDebugLog () {
			debugLog ("GssCall SUCCESS - Complete")
		}
	} else if status.Major == goteragss.GSS_S_CONTINUE_NEEDED {
		if con.hasDebugLog () {
			debugLog ("GssCall SUCCESS - Continue needed")
		}
	} else {
		err = con.makeChainedSecurityError (status)
		return
	}

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("doGssCall abyOutputToken length=%v", len (abyOutputToken)))
	}

	if con.hasDebugLog () {
		if len (abyOutputToken) == 0 {
			debugLog ("doGssCall output token is zero bytes")
		} else {
			debugLogHexDump ("doGssCall output token:", abyOutputToken)
		}
	}

	return

} // end method doGssCall

func (con *TeradataConnection) parseAssignResponse (abyAssignResponseMsg [] byte) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter parseAssignResponse %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave parseAssignResponse %v", con)) } ()
	}

	con.m_uSessionNumber = binary.BigEndian.Uint32 (abyAssignResponseMsg [20 : ])

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("Session Number = %v", con.m_uSessionNumber))
	}

	aabyParcelHeaders := getParcelHeadersFromMessage (abyAssignResponseMsg)
	if len (aabyParcelHeaders) == 0 || cookedParcelFlavor (aabyParcelHeaders [0]) != PCLASSIGNRSP {
		return
	}

	abyParcelBody := constrainedParcelBody (aabyParcelHeaders [0])

	con.m_uHostID = binary.BigEndian.Uint16 (abyParcelBody [92 : ])

} // end method parseAssignResponse
