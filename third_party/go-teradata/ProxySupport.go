// Copyright 2024 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"
)

type socketAddress struct {

	m_sScheme           string // "http" or "https" or "socket"
	m_bUseHTTPS         bool
	m_sAddress          string
	m_uPort             uint32
	m_sAddressAndPort   string
	m_bLiteralIP        bool // true if m_sAddress is a literal IP address
	m_sOriginalHostName string // empty string if m_sAddress is a literal IP address
	m_sResolvedHostName string // empty string if m_sAddress is a literal IP address

} // end type socketAddress

var _ fmt.Stringer = &socketAddress {} // implements fmt.Stringer

func (sockAddr socketAddress) String () string { // satisfies fmt.Stringer

	return sockAddr.m_sScheme + ":" + ternary (sockAddr.m_sResolvedHostName != "", sockAddr.m_sResolvedHostName + "/" + sockAddr.m_sAddressAndPort, sockAddr.m_sAddressAndPort)

} // end method String

func (sockAddr socketAddress) getHostString () string { // equivalent to Java InetSocketAddress.getHostString

	return ternary (sockAddr.m_sResolvedHostName != "", sockAddr.m_sResolvedHostName, sockAddr.m_sAddress)

} // end method getHostString

func newSocketAddress (sScheme string, sAddress string, uPort uint32, bLiteralIP bool, sOriginalHostName string, sResolvedHostName string) socketAddress {

	return socketAddress {
		m_sScheme           : sScheme,
		m_bUseHTTPS         : sScheme == "https",
		m_sAddress          : sAddress,
		m_uPort             : uPort,
		m_sAddressAndPort   : net.JoinHostPort (sAddress, format10Uint32 (uPort)), // adds brackets if needed for IPv6 address
		m_bLiteralIP        : bLiteralIP, // true if m_sAddress is a literal IP address
		m_sOriginalHostName : sOriginalHostName, // empty string if m_sAddress is a literal IP address
		m_sResolvedHostName : sResolvedHostName, // empty string if m_sAddress is a literal IP address
	}
} // end newSocketAddress

func parseSocketAddress (sScheme string, sAddress string, uPort uint32) socketAddress {

	bLiteralIP := net.ParseIP (sAddress) != nil

	sOriginalHostName := ternary (bLiteralIP, "", sAddress)
	sResolvedHostName := ternary (bLiteralIP, "", sAddress)

	return newSocketAddress (sScheme, sAddress, uPort, bLiteralIP, sOriginalHostName, sResolvedHostName)

} // end parseSocketAddress

func (con *TeradataConnection) makeDatabaseSocketAddress (nameAddr nameAddress, uPort uint32) socketAddress {

	sScheme := ternary (uPort == con.m_params.M_uHTTPSPort, "https", "socket")

	bLiteralIP := nameAddr.m_sHostName == nameAddr.m_sAddress

	sOriginalHostName := ternary (bLiteralIP, "", con.m_params.M_sHostName)
	sResolvedHostName := ternary (bLiteralIP, "", nameAddr.m_sHostName)

	return newSocketAddress (sScheme, nameAddr.m_sAddress, uPort, bLiteralIP, sOriginalHostName, sResolvedHostName)

} // end method makeDatabaseSocketAddress

func convertProxyBypassHostsToRegex (s string) string {

	return strings.ReplaceAll (strings.ReplaceAll (strings.ReplaceAll (strings.ReplaceAll (s,
		`.`, `\.`),
		`[`, `\[`),
		`]`, `\]`),
		`*`, `.+`)

} // end convertProxyBypassHostsToRegex

func connectSocket (params *ConParams, addrDest socketAddress, nTimeoutMs uint32, ctxConnect context.Context) (sockToReturn net.Conn, sProxyUsed string, err error) {

	if params.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter connectSocket addrDest=%v nTimeoutMs=%v", addrDest, nTimeoutMs))
		defer func () { traceLog (fmt.Sprintf ("< leave connectSocket addrDest=%v nTimeoutMs=%v", addrDest, nTimeoutMs)) } ()
	}

	// Brackets are needed around an IPv6 literal address for matching a bypass hosts regular expression that contains literal brackets.
	sDestHostWithBrackets := addrDest.getHostString ()
	if strings.Contains (sDestHostWithBrackets, ":") {
		sDestHostWithBrackets = "[" + sDestHostWithBrackets + "]"
	}

	sProxyBypassHosts := ternary (params.M_sProxyBypassHosts != "", params.M_sProxyBypassHosts, "localhost|127.*|[::1]")

	sProxyBypassPattern := convertProxyBypassHostsToRegex (sProxyBypassHosts)

	bBypassProxy := matchesPatternIgnoreCase (sDestHostWithBrackets, sProxyBypassPattern) ;

	if params.hasDebugLog () {
		debugLog (fmt.Sprintf ("connectSocket: addrDest=%v nTimeoutMs=%v M_sProxyBypassHosts=%v sProxyBypassHosts=%v sProxyBypassPattern=%v sDestHostWithBrackets=%v bBypassProxy=%v m_urlHTTPProxy=%v m_urlHTTPSProxy=%v",
			addrDest,
			nTimeoutMs,
			params.M_sProxyBypassHosts,
			sProxyBypassHosts,
			sProxyBypassPattern,
			sDestHostWithBrackets,
			bBypassProxy,
			params.m_urlHTTPProxy,
			params.m_urlHTTPSProxy))
	}

	addrConnect    := addrDest
	bConnectMethod := false
	sCredentials   := ""
	if ! bBypassProxy {
		if addrDest.m_sScheme == "http" && params.m_urlHTTPProxy != nil {

			uProxyPort, _ := parse10Uint32 (params.m_urlHTTPProxy.Port ()) // guaranteed to be valid because it was validated by ConParams
			addrConnect = parseSocketAddress ("socket", params.m_urlHTTPProxy.Hostname (), uProxyPort)
			bConnectMethod = true

			if params.M_sHTTPProxyUser != "" && params.M_sHTTPProxyPassword != "" {
				sCredentials = params.M_sHTTPProxyUser + ":" + params.M_sHTTPProxyPassword
			}
		} else if addrDest.m_sScheme == "https" && params.m_urlHTTPSProxy != nil {

			uProxyPort, _ := parse10Uint32 (params.m_urlHTTPSProxy.Port ()) // guaranteed to be valid because it was validated by ConParams
			addrConnect = parseSocketAddress ("socket", params.m_urlHTTPSProxy.Hostname (), uProxyPort)
			bConnectMethod = true

			if params.M_sHTTPSProxyUser != "" && params.M_sHTTPSProxyPassword != "" {
				sCredentials = params.M_sHTTPSProxyUser + ":" + params.M_sHTTPSProxyPassword
			}
		}
	}

	if params.hasDebugLog () {
		debugLog (fmt.Sprintf ("connectSocket: addrConnect=%v bConnectMethod=%v sCredentials=%v",
		addrConnect,
		bConnectMethod,
		sCredentials))
	}

	if bConnectMethod {
		sProxyUsed = addrConnect.m_sAddressAndPort
	}

	sTitle := ternary (bConnectMethod,
		fmt.Sprintf ("proxy %v to %v", addrConnect, addrDest),
		fmt.Sprintf ("direct to %v", addrConnect))

	dialer := &net.Dialer { Timeout : time.Duration (nTimeoutMs) * time.Millisecond }

	timeStart := time.Now ()

	sockPlain, err := dialer.DialContext (ctxConnect, "tcp", addrConnect.m_sAddressAndPort)

	if params.hasTimingLog () {
		timingLog ("Socket connect " + sTitle + " with timeout " + format10Uint32 (nTimeoutMs) + " ms took " + formatElapsed (timeStart) + " and " + ternary (err != nil, "failed", "succeeded"))
	}

	if err != nil { return }

	if bConnectMethod {
		sCredentialsBase64 := ternary (sCredentials != "",
			base64.StdEncoding.EncodeToString ([] byte (sCredentials)), // equivalent to Java Base64.getEncoder ().encodeToString (...)
			"")

		sProxyAuthorization := ternary (sCredentialsBase64 != "",
			"Proxy-Authorization: Basic " + sCredentialsBase64 + "\r\n",
			"")

		// Cal's wiki page says the driver should specify a COP hostname in the CONNECT method:
		// "
		//  Client performs COP-Discovery when this feature is not explicitly disabled
		//   * picks a COP or
		//   * selects TDPID/DBCName when DNS does not have COP entries or disabled
		//  Client establishes a socket connection to the proxy server
		//  Client sends a HTTP Connect method to the proxy server
		//   * Proxy server resolves the COP-Name or TDPID and connects to the Gateway
		// "

		sConnectMethodRequest :=
			"CONNECT " + sDestHostWithBrackets + ":" + format10Uint32 (addrDest.m_uPort) + " HTTP/1.0\r\n" +
			sProxyAuthorization +
			"\r\n"

		abyConnectMethodRequest := [] byte (sConnectMethodRequest)

		if params.hasDumpLog () {
			dumpLogHexDump (fmt.Sprintf ("Transmit HTTP request to proxy %v", addrConnect), abyConnectMethodRequest)
		}

		timeStart = time.Now ()

		err = socketWrite (sockPlain, abyConnectMethodRequest)

		if params.hasTimingLog () {
			timingLog ("HTTPS CONNECT send took " + formatElapsed (timeStart) + " and " + ternary (err != nil, "failed", "succeeded"))
		}

		if err != nil { return }

		timeStart = time.Now ()

		abyRawHeaders, asSplitHeaders, _, errRead := readHttpHeaders (sockPlain)
		err = errRead

		if params.hasTimingLog () {
			timingLog ("HTTPS CONNECT receive took " + formatElapsed (timeStart) + " and " + ternary (err != nil, "failed", "succeeded"))
		}

		if err != nil { return }

		if params.hasDumpLog () {
			dumpLogHexDump (fmt.Sprintf ("Received HTTP response from proxy %v", addrConnect), abyRawHeaders)
		}

		nRespCode, _, _ := parseHttpHeader (asSplitHeaders [0])
		if nRespCode != 200 {
			err = formatError (nil, "Unexpected response from proxy %v\n%v", addrConnect, hex.Dump (abyRawHeaders))
			return
		}
	} // end if bConnectMethod

	if ! addrDest.m_bUseHTTPS {

		sockToReturn = sockPlain

	} else {

		// addrDest.getHostString has the COP-suffixed hostname, which goes into the Server Name Indication (SNI) field in the TLS client hello.
		//  https://en.wikipedia.org/wiki/Server_Name_Indication
		// This allows firewall rules to be based on the intended destination hostname.

		cfg := &tls.Config {
			ServerName         : addrDest.getHostString (),
			InsecureSkipVerify : true,
			CipherSuites       : params.m_auTLSCiphers,
			MinVersion         : params.m_uTLSVersion,
			CurvePreferences   : params.m_auTLSCurveIDs,
		}

		if params.hasDebugLog () {
			debugLog (fmt.Sprintf ("connectSocket: tls.Config=%+v", cfg))
		}

		sockTLS := tls.Client (sockPlain, cfg)

		err = sockTLS.HandshakeContext (ctxConnect)
		if err != nil {
			if params.hasDebugLog () {
				debugLog (fmt.Sprintf ("connectSocket: sockTLS.HandshakeContext err=%v", err))
			}

			sockTLS.Close () // closes the underlying socket
			return
		}

		sockToReturn = sockTLS
	}

	return

} // end connectSocket
