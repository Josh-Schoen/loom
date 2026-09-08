// Copyright 2021 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var g_aCiphers = append (tls.CipherSuites (), tls.InsecureCipherSuites () ...)

var g_aCurveIDs = [] tls.CurveID {

	tls.CurveP256,
	tls.CurveP384,
	tls.CurveP521,
	tls.X25519,
	tls.X25519MLKEM768,

} // end g_aCurveIDs

var g_auTLSVersions = func () (auOutput [] uint16) {

	set := ListSet [uint16] {}

	for _, cipher := range g_aCiphers {
		for _, uID := range cipher.SupportedVersions {
			set.Add (uID)
		}
	}

	auOutput = [] uint16 {}

	for _, uID := range set.ToSlice () {
		auOutput = append (auOutput, uID)
	}

	return

} () // end g_auTLSVersions

// g_mapCipherIDToName is not needed because tls.CipherSuiteName(uint16) provides that functionality.

var g_mapCipherNameToID = func () (m map [string] uint16) {

	m = map [string] uint16 {}
	for _, cipher := range g_aCiphers { m [cipher.Name] = cipher.ID }
	return

} () // end g_mapCipherNameToID

// g_mapCurveIDToNamedGroup is not needed because tls.CurveID.String() provides that functionality.

var g_mapNamedGroupToCurveID = func () (m map [string] tls.CurveID) {

	m = map [string] tls.CurveID {}
	for _, uCurveID := range g_aCurveIDs { m [uCurveID.String ()] = uCurveID }
	return

} () // end g_mapNamedGroupToCurveID

func TLSVersionName (uID uint16) string {

	return strings.ReplaceAll (tls.VersionName (uID), "TLS ", "TLSv")

} // end TLSVersionName

var g_mapTLSVersionNameToID = func () (m map [string] uint16) {

	m = map [string] uint16 {}

	for _, uID := range g_auTLSVersions {
		sName := TLSVersionName (uID)
		m [sName] = uID
	}

	return

} () // end g_mapTLSVersionNameToID

func hostFromAddr (a net.Addr) (sHost string) {

	if a == nil {
		sHost = "none"
	} else {
		sHost, _, _ = net.SplitHostPort (a.String ())
	}

	return

} // end hostFromAddr

func portFromAddr (a net.Addr) (sPort string) {

	if a == nil {
		sPort = "none"
	} else {
		_, sPort, _ = net.SplitHostPort (a.String ())
	}

	return

} // end portFromAddr

func safeLocalAddr (socket net.Conn) net.Addr {

	if socket == nil { return nil }

	return socket.LocalAddr ()
	
} // end safeLocalAddr

func safeRemoteAddr (socket net.Conn) net.Addr {

	if socket == nil { return nil }

	return socket.RemoteAddr ()

} // end safeRemoteAddr

func isTLSSocket (socket net.Conn) (bTLS bool) {

	_, bTLS = socket.(*tls.Conn)
	return

} // end isTLSSocket

func asTCPConn (socket net.Conn) *net.TCPConn {

	if isTLSSocket (socket) { socket = socket.(*tls.Conn).NetConn () }
	return socket.(*net.TCPConn)

} // end asTCPConn

func getTLSProtocol (socket net.Conn) (sProtocol string, sCipher string) {

	if isTLSSocket (socket) {

		state := socket.(*tls.Conn).ConnectionState ()

		sProtocol = TLSVersionName (state.Version)
		sCipher = tls.CipherSuiteName (state.CipherSuite)

	} else {
		sProtocol = "plain"
	}

	return

} // end getTLSProtocol

func socketWrite (socket net.Conn, aby [] byte) (err error) {

	// io.Writer.Write documentation:
	// Write writes len(p) bytes from p to the underlying data stream.
	// It returns the number of bytes written from p (0 <= n <= len(p)) and any error encountered that caused the write to stop early.
	// Write must return a non-nil error if it returns n < len(p).

	_, err = socket.Write (aby)
	return

} // end socketWrite

func socketRead (socket net.Conn, aby [] byte) (nTotalBytesRead int, err error) {

	// io.Reader.Read documentation:
	// If some data is available but not len(p) bytes, Read conventionally returns what is available instead of waiting for more.
	// When Read encounters an error or end-of-file condition after successfully reading n > 0 bytes, it returns the number of bytes read.
	// It may return the (non-nil) error from the same call or return the error (and n == 0) from a subsequent call.
	// An instance of this general case is that a Reader returning a non-zero number of bytes at the end of the input stream may return either err == EOF or err == nil.
	// The next Read should return 0, EOF.
	// Callers should always process the n > 0 bytes returned before considering the error err.

	for nTotalBytesRead < len (aby) {

		var nBytesRead int
		nBytesRead, err = socket.Read (aby [nTotalBytesRead : ])
		if err != nil { return }

		nTotalBytesRead += nBytesRead
	}

	return

} // end socketRead

func getUrlScheme (sURL string) string {

	parsedURL, err := url.Parse (sURL)
	if err != nil { return "" }
	return parsedURL.Scheme

} // end getUrlScheme

func readHttpHeaders (socket net.Conn) (abyRawHeaders [] byte, asSplitHeaders [] string, r *bufio.Reader, err error) {

	abyRawHeaders  = [] byte   {}
	asSplitHeaders = [] string {}

	r = bufio.NewReader (socket)
	for {
		var abyLine [] byte
		abyLine, err = r.ReadBytes ('\n') // returns a string containing the data up to and including the delimiter

		if err != nil { return }

		abyRawHeaders = append (abyRawHeaders, abyLine ...)

		sLine := string (abyLine)
		if sLine == "\r\n" { return }

		asSplitHeaders = append (asSplitHeaders, strings.TrimRight (sLine, "\r\n"))
	}
} // end readHttpHeaders

var regexHttpHeader = regexp.MustCompile ("^HTTP/[0-9.]+ ([0-9]+) (.*)$")

func parseHttpHeader (sHeader string) (nRespCode int, sRespMessage string, bValid bool) {

	asGroup := regexHttpHeader.FindStringSubmatch (sHeader)
	bValid = asGroup != nil
	if bValid {
		nRespCode, _ = strconv.Atoi (asGroup [1])
		sRespMessage = strings.TrimSpace (asGroup [2])
	}

	return

} // end parseHttpHeader

type HttpResponse struct {

	M_nCode int
	M_sMessage string
	M_abyBody [] byte
	M_sProxyUsed string
	M_sProtocol string
	M_sCipher string
	M_sCertStatus string

} // end type HttpResponse

func DoHttpRequest (params *ConParams, sMethod string, sURL string, aasHeaders [][] string, abyRequestBody [] byte) (resp *HttpResponse, err error) {

	resp = &HttpResponse {}

	if params.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter DoHttpRequest sMethod=%v sURL=%v", sMethod, sURL))
		defer func () { traceLog (fmt.Sprintf ("< leave DoHttpRequest sMethod=%v sURL=%v", sMethod, sURL)) } ()
	}

	parsedURL, err := url.Parse (sURL)
	if err != nil { return }

	// net.URL fields:
	// Scheme      string
	// Opaque      string    // encoded opaque data
	// User        *Userinfo // username and password information
	// Host        string    // host or host:port
	// Path        string    // path (relative paths may omit leading slash)
	// RawPath     string    // encoded path hint (see EscapedPath method)
	// OmitHost    bool      // do not emit empty host (authority)
	// ForceQuery  bool      // append a query ('?') even if RawQuery is empty
	// RawQuery    string    // encoded query values, without '?'
	// Fragment    string    // fragment for references, without '#'
	// RawFragment string    // encoded fragment hint (see EscapedFragment method)
	//
	// net.URL methods:
	// Hostname()  string    // returns Host, stripping any valid port number if present. Removes square brackets from literal IPv6 addresses.
	// Port()      string    // returns the port part of Host, without the leading colon. Returns an empty string if Host doesn't contain a valid numeric port.

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		err = formatError (nil, "Invalid scheme %v specified in URL %v", parsedURL.Scheme, sURL)
		return
	}

	bUseHTTPS := "https" == parsedURL.Scheme

	sPort := parsedURL.Port ()
	var uPort uint32
	if sPort != "" {
		uPort, _ = parse10Uint32 (sPort)
	} else {
		if bUseHTTPS { uPort = 443 } else { uPort = 80 }
	}

	sPath := parsedURL.Path
	if sPath == "" { sPath = "/" } // default to /

	sQuery := parsedURL.RawQuery
	if sQuery != "" { sQuery = "?" + sQuery }

	bUsingDefaultPort := bUseHTTPS && uPort == 443 || ! bUseHTTPS && uPort == 80
	sPortSuffix := ternary (bUsingDefaultPort, "", ":" + format10Uint32 (uPort))

	sRequest := sMethod + " " + sPath + sQuery + " HTTP/1.0\r\n" +
		"Host: " + parsedURL.Hostname () + sPortSuffix + "\r\n" +
		"User-Agent: teradatasql/" + G_sVersionNumber + "\r\n" // required by some services, 403 Forbidden if missing

	bHasAccept := false
	for _, asHeader := range aasHeaders {
		sRequest += fmt.Sprintf ("%v: %v\r\n", asHeader [0], asHeader [1])
		if strings.EqualFold (asHeader [0], "Accept") { bHasAccept = true }
	}

	if ! bHasAccept {
		sRequest += "Accept: */*\r\n"
	}

	if abyRequestBody != nil {
		sRequest += fmt.Sprintf ("Content-Length: %v\r\n", len (abyRequestBody))
	}

	sRequest += "\r\n" ;

	abyRequest := append ([] byte (sRequest), abyRequestBody ...)

	addrDest := parseSocketAddress (parsedURL.Scheme, parsedURL.Hostname (), uPort)

	var socket net.Conn
	socket, resp.M_sProxyUsed, err = connectSocket (
		params,
		addrDest,
		/*nTimeoutMs*/ 15000, // 15 seconds
		/*ctxConnect*/ context.Background ())
	if err != nil { return }

	defer socket.Close ()

	resp.M_sProtocol, resp.M_sCipher = getTLSProtocol (socket)

	if bUseHTTPS {
		resp.M_sCertStatus, err = checkCert (params, params.m_nOIDCSSLModeLevel, socket.(*tls.Conn), addrDest)
		if err != nil { return }
	}

	if params.hasDumpLog () {
		dumpLogHexDump ("Transmit HTTP request " + sURL, abyRequest)
	}

	timeStart := time.Now ()

	err = socketWrite (socket, abyRequest)

	if params.hasTimingLog () {
		timingLog ("Transmit HTTP request " + sURL + " took " + formatElapsed (timeStart) + " and " + ternary (err != nil, "failed", "succeeded"))
	}

	if err != nil { return }

	timeStart = time.Now ()

	abyRawHeaders, asSplitHeaders, r, err := readHttpHeaders (socket)

	if params.hasTimingLog () {
		timingLog ("Read HTTP response headers " + sURL + " took " + formatElapsed (timeStart) + " and " + ternary (err != nil, "failed", "succeeded"))
	}

	if err != nil { return }

	if params.hasDumpLog () {
		dumpLogHexDump ("Received HTTP response headers " + sURL, abyRawHeaders)
	}

	timeStart = time.Now ()

	resp.M_abyBody, err = io.ReadAll (r)

	if params.hasTimingLog () {
		timingLog ("Read HTTP response body " + sURL + " took " + formatElapsed (timeStart) + " and " + ternary (err != nil, "failed", "succeeded"))
	}

	if err != nil { return }

	if params.hasDumpLog () {
		dumpLogHexDump ("Received HTTP response body " + sURL, resp.M_abyBody)
	}

	resp.M_nCode, resp.M_sMessage, _ = parseHttpHeader (asSplitHeaders [0])
	return

} // end DoHttpRequest

func (con *TeradataConnection) doHttpRequest (sMethod string, sURL string, aasHeaders [][] string, abyRequestBody [] byte, anRequiredRespCodes [] int) (resp *HttpResponse, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter doHttpRequest %v sMethod=%v sURL=%v", con, sMethod, sURL))
		defer func () { traceLog (fmt.Sprintf ("< leave doHttpRequest %v sMethod=%v sURL=%v", con, sMethod, sURL)) } ()
	}

	resp, err = DoHttpRequest (con.m_params, sMethod, sURL, aasHeaders, abyRequestBody)
	if err != nil {
		err = con.makeDriverError (err, 0, fmt.Sprintf ("HTTP request failed for %v", sURL))
		return
	}

	if anRequiredRespCodes != nil {

		bFound := false
		for _, nRequiredCode := range anRequiredRespCodes {
			if resp.M_nCode == nRequiredCode {
				bFound = true
				break
			}
		} // end for

		if ! bFound {
			_, err = con.parseHttpErrorResponse (sURL, resp.M_nCode, resp.M_sMessage, string (resp.M_abyBody))
			return
		}
	} // end if

	return

} // end doHttpRequest

func (con *TeradataConnection) parseHttpErrorResponse (sURL string, nRespCode int, sRespMessage string, sJSON string) (sError string, err error) {

	var e struct {
		M_sError     string `json:"error"`
		M_sErrorDesc string `json:"error_description"`
	}

	if nRespCode == 400 /*Bad Request*/ || nRespCode == 401 /*Unauthorized*/ {
		shredJSON (sJSON, &e) // ignore error
	}

	if con.hasDebugLog () {
		debugLog ("M_sError="     + e.M_sError)
		debugLog ("M_sErrorDesc=" + e.M_sErrorDesc)
	}

	sErrorCodeAndMsg := itoa (nRespCode) +
		ternary (e.M_sErrorDesc != "", " " + e.M_sErrorDesc,
		ternary (e.M_sError     != "", " " + e.M_sError,
		ternary (sRespMessage   != "", " " + sRespMessage, "")))

	sError = e.M_sError
	err = con.makeDriverError (nil, 558, fmt.Sprintf ("Received error response code %v from URL %v", sErrorCodeAndMsg, sURL))
	return

} // end parseHttpErrorResponse

func decodeURLParameter (sInput string) (sOutput string) {

	sOutput, err := url.QueryUnescape (sInput)

	if err != nil { sOutput = sInput } // return original if decoding fails
	return

} // end decodeURLParameter

func parseFormData (sFormData string) (mapOutput map [string] string) {

	mapOutput = map [string] string {}

	if sFormData != "" {

		asPairs := strings.Split (sFormData, "&")
		for _, sPair := range asPairs {

			asNameValue := strings.SplitN (sPair, "=", 2)
			if len (asNameValue) == 2 {

				sName  := decodeURLParameter (asNameValue [0])
				sValue := decodeURLParameter (asNameValue [1])
				mapOutput [sName] = sValue
			}
		}
	}

	return

} // end parseFormData
