// Copyright 2022 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

type httpServer struct {

	m_con                      *TeradataConnection
	m_sExpectedPrefix          string
	m_nBrowserTimeoutInMs      int
	m_nBrowserTabTimeoutInMs   int
	m_listener                 net.Listener
	m_mapRequestParams         map [string] string
	m_done                     chan struct {}
	m_err                      error

} // end type httpServer

func newHttpServer (con *TeradataConnection, anListenPortRange [] int, sExpectedPrefix string, nBrowserTimeoutInMs int, nBrowserTabTimeoutInMs int) (http *httpServer, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter newHttpServer %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave newHttpServer %v", con)) } ()
	}

	nFirstListenPort := anListenPortRange [0]
	nLastListenPort  := anListenPortRange [1]

	for nListenPort := nFirstListenPort ; http == nil ; nListenPort++ {

		sListenAddr := fmt.Sprintf ("127.0.0.1:%v", nListenPort)
		listener, errListen := net.Listen ("tcp", sListenAddr) // port suffix :0 = use ephemeral port

		if errListen == nil {
			http = &httpServer {
				m_con                      : con,
				m_sExpectedPrefix          : sExpectedPrefix,
				m_nBrowserTimeoutInMs      : nBrowserTimeoutInMs,
				m_nBrowserTabTimeoutInMs   : nBrowserTabTimeoutInMs,
				m_listener                 : listener,
				m_mapRequestParams         : map [string] string {},
				m_done                     : make (chan struct {}),
			}

		} else if nListenPort == nLastListenPort {
			err = con.makeDriverError (errListen, 563, "Failed to create HTTP listener for Browser Authentication")
			return

		} else if con.hasDebugLog () {
			debugLog (fmt.Sprintf ("HttpServer could not bind to port %v due to %v", nListenPort, errListen))
		}
	} // end for nListenPort

	go http.run ()
	return

} // end newHttpServer

func (http *httpServer) getLocalPort () string {

	return portFromAddr (http.m_listener.Addr ())

} // end method getLocalPort

func (http *httpServer) waitForRequestParams () (mapRequestParams map [string] string, err error) {

	if http.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter waitForRequestParams %v", http.m_con))
		defer func () { traceLog (fmt.Sprintf ("< leave waitForRequestParams %v", http.m_con)) } ()
	}

	timeStart := time.Now ()
	<- http.m_done

	if http.m_con.hasTimingLog () { timingLog (fmt.Sprintf ("Waiting for httpServer took %v", formatElapsed (timeStart))) }

	if http.m_err != nil {
		err = http.m_err
		if http.m_con.hasDebugLog () { debugLog (fmt.Sprintf ("httpServer returned error: %v", err)) }
	} else {
		mapRequestParams = http.m_mapRequestParams
		if http.m_con.hasDebugLog () { debugLog (fmt.Sprintf ("httpServer returned: %v", mapRequestParams)) }
	}

	return

} // end waitForRequestParams

func (http *httpServer) run () {

	if http.m_con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter httpServer.run %v", http.m_con))
		defer func () { traceLog (fmt.Sprintf ("< leave httpServer.run %v", http.m_con)) } ()
	}

	defer close (http.m_done)
	defer http.m_listener.Close ()

	if http.m_con.hasDebugLog () { debugLog (fmt.Sprintf ("httpServer listening on port %v", http.getLocalPort ())) }

	if tcp, bValid := http.m_listener.(*net.TCPListener) ; bValid {
		errDeadline := tcp.SetDeadline (time.Now ().Add (time.Duration (http.m_nBrowserTimeoutInMs) * time.Millisecond))

		if errDeadline != nil {
			http.m_err = http.m_con.makeDriverError (errDeadline, 561, "Failed to set deadline for HTTP request from browser")
			return
		}
	}

	timeStart := time.Now ()
	socket, errAccept := http.m_listener.Accept () // blocks

	if http.m_con.hasTimingLog () { timingLog (fmt.Sprintf ("Waiting for httpServer.m_listener.Accept took %v", formatElapsed (timeStart))) }

	if errAccept != nil {
		http.m_err = http.m_con.makeDriverError (errAccept, 559, "Failed to accept HTTP request from browser")
		return
	}

	defer socket.Close ()

	if http.m_con.hasDebugLog () { debugLog (fmt.Sprintf ("httpServer.m_listener.Accept returned: %v", socket)) }

	timeStart = time.Now ()
	abyRawHeaders, asSplitHeaders, _, errRead := readHttpHeaders (socket)

	if http.m_con.hasTimingLog () { timingLog ("Waiting to read HTTP headers took " + formatElapsed (timeStart)) }

	if errRead != nil {
		http.m_err = http.m_con.makeDriverError (errRead, 560, "Failed to read HTTP headers from browser")
		return
	}

	if http.m_con.hasDumpLog () { dumpLogHexDump ("Received HTTP response", abyRawHeaders) }

	sRequestLine := ""
	if len (asSplitHeaders) > 0 { sRequestLine = asSplitHeaders [0] }
	asRequestTokens := strings.Split (sRequestLine, " ")

	bValidRequest := len (asRequestTokens) >= 3 &&
		asRequestTokens [0] == "GET" &&
		strings.HasPrefix (asRequestTokens [1], http.m_sExpectedPrefix) &&
		strings.HasPrefix (asRequestTokens [2], "HTTP/")

	if bValidRequest {
		sRequestParams := asRequestTokens [1][len (http.m_sExpectedPrefix) : ]
		asRequestParams := strings.Split (sRequestParams, "&")

		for _, sParam := range asRequestParams {
			asKeyValue := strings.SplitN (sParam, "=", 2)
			if len (asKeyValue) == 2 {
				http.m_mapRequestParams [asKeyValue [0]] = asKeyValue [1]
			}
		}
	}

	if http.m_con.hasDebugLog () {
		debugLog (fmt.Sprintf ("HttpServer bValidRequest=%v m_mapRequestParams=%v", bValidRequest, http.m_mapRequestParams))
	}

	// IETF RFC 6749 OAuth 2.0 Authorization Framework
	//
	// Section 4.1.2 Authorization Response https://datatracker.ietf.org/doc/html/rfc6749#section-4.1.2
	//  says "The client MUST ignore unrecognized response parameters."
	//
	// Section 4.1.2.1 Error Response https://datatracker.ietf.org/doc/html/rfc6749#section-4.1.2.1
	//  says "If the request fails ... the authorization server ... MUST NOT automatically redirect the user-agent to the invalid redirection URI."
	//
	// But Microsoft Entra does redirect the browser to the redirection URI, with error and error_description URL parameters.

	sError, _     := http.m_mapRequestParams ["error"]
	sError, _      = url.QueryUnescape (sError)
	sErrorDesc, _ := http.m_mapRequestParams ["error_description"]
	sErrorDesc, _  = url.QueryUnescape (sErrorDesc)
	bError        := sError != "" || sErrorDesc != ""

	if http.m_con.hasDebugLog () {
		debugLog (fmt.Sprintf ("HttpServer bError=%v sError=%v sErrorDesc=%v", bError, sError, sErrorDesc))
	}

	sHttpResponse := "HTTP/1.1 " + ternary (bValidRequest, "200 OK", "404 Not Found") + "\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"Cache-Control: no-cache\r\n" +
		"Pragma: no-cache\r\n" +
		"Connection: close\r\n" +
		"\r\n"

	if bValidRequest {
		sHttpResponse += "<!DOCTYPE HTML>\r\n" +
			"<html>\r\n" +
			"<head>\r\n" +
			"<title>Teradata Browser Authentication</title>\r\n" +
			"</head>\r\n" +
			"<body style=\"background-color: DimGray;\"" + ternary (http.m_nBrowserTabTimeoutInMs < 0, "", fmt.Sprintf (" onload=\"setTimeout(window.close,%v);\"", http.m_nBrowserTabTimeoutInMs)) + ">\r\n" +
			"<p style=\"" +
				"border-radius: 10pt;" + // rounded corners
				"padding: 22pt;" +
				"background: DarkOrange;" +
				"color: white;" + // text color
				"text-align: center;" +
				"font-family: sans-serif;" +
				"font-weight: bolder;" +
				"font-size: 24pt;" +
			"\">TERADATA BROWSER AUTHENTICATION " + ternary (bError, "FAILED", "COMPLETED") + "</p>\r\n" +
			"</body>\r\n" +
			"</html>\r\n"
	}

	_, errWrite := socket.Write ([] byte (sHttpResponse))

	if errWrite != nil {
		http.m_err = http.m_con.makeDriverError (errWrite, 562, "Failed to transmit HTTP response to browser")

	} else if bError {
		http.m_err = http.m_con.makeDriverError (nil, 0, "HttpServer received " +
			coalesce (sError, "unspecified") +
			" error " +
			coalesce (sErrorDesc, "with no description"))
	}
} // end method run
