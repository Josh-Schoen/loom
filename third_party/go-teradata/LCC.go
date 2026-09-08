// Copyright 2021 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

var g_mapConnectFailures sync.Map // map [/*sAddressAndPort*/ string] time.Time

func addSocketAddress (setSockAddrs [] socketAddress, sockAddrNew socketAddress) [] socketAddress {

	for _, sockAddr := range setSockAddrs {
		if sockAddr.m_sAddressAndPort == sockAddrNew.m_sAddressAndPort {
			return setSockAddrs
		}
	}

	return append (setSockAddrs, sockAddrNew)

} // end addSocketAddress

func removeThread (listThreads [] *ConnectThread, thread *ConnectThread) [] *ConnectThread {

	for i := 0 ; i < len (listThreads) ; i++ {
		if listThreads [i] == thread {
			return append (append ([] *ConnectThread {}, listThreads [ : i] ...), listThreads [i + 1 : ] ...)
		}
	}

	return listThreads

} // end removeThread

func formatSyncMap (m *sync.Map) string {

	s := ""
	m.Range (func (key any, value any) bool {
		s += fmt.Sprintf ("%v%v=%v", ternary (len (s) > 0, ", ", ""), key, value)
		return true // continue iteration
	})

	return "{" + s + "}"

} // end formatSyncMap

func (con *TeradataConnection) connectToHost (ctx context.Context, reconnect *Reconnect) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter connectToHost %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave connectToHost %v", con)) } ()
	}

	if con.m_params.M_sHostName == "-none" { return } // for token mode when no database is available

	aNameAddrs, err := discoverCops (con)
	if err != nil { return }

	if con.m_auPorts == nil {

		if con.m_params.m_nSSLModeLevel >= LEVEL_REQUIRE {
			con.m_auPorts = [] uint32 { con.m_params.M_uHTTPSPort }

		} else if con.m_params.m_nSSLModeLevel == LEVEL_ALLOW {
			con.m_auPorts = [] uint32 { con.m_params.M_uDbsPort, con.m_params.M_uHTTPSPort }

		} else if con.m_params.m_nSSLModeLevel == LEVEL_PREFER {
			con.m_auPorts = [] uint32 { con.m_params.M_uHTTPSPort, con.m_params.M_uDbsPort }

		} else {
			con.m_auPorts = [] uint32 { con.m_params.M_uDbsPort }
		}

		if con.hasDebugLog () {
			debugLog (fmt.Sprintf ("connectToHost: m_nSSLModeLevel=%v len(m_auPorts)=%v m_auPorts=%v", con.m_params.m_nSSLModeLevel, len (con.m_auPorts), con.m_auPorts))
		}
	}

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("Before connect loop: g_mapConnectFailures=%v", formatSyncMap (&g_mapConnectFailures)))
	}

	setSockAddrs := [] socketAddress {} // not an actual set, but used like a set

	bSingleNode := len (aNameAddrs) == 1
	nHTTPSRetry := func () int { if bSingleNode && con.m_params.m_nSSLModeLevel >= LEVEL_PREFER { return int (con.m_params.M_uHTTPSRetry) } ; return 0 } ()

	for iNameAddr := 0 ; iNameAddr < len (aNameAddrs) ; iNameAddr++ {

		for iPort := 0 ; iPort < len (con.m_auPorts) ; iPort++ {

			uPort := con.m_auPorts [iPort]

			sockAddr := con.makeDatabaseSocketAddress (aNameAddrs [iNameAddr], uPort)

			if oValue, bFound := g_mapConnectFailures.Load (sockAddr.m_sAddressAndPort) ; bFound {

				timeConnectFail := oValue.(time.Time)
				timeCurrent     := time.Now ()
				bIgnore         := timeConnectFail.After (timeCurrent) // ignore future failure times due to system clock change

				if ! bIgnore {

					timeUsable := timeConnectFail.Add (time.Duration (con.m_params.M_uConnectFailureTTL) * time.Second)
					bFailFast  := timeCurrent.Before (timeUsable)

					if bFailFast {
						if con.hasTimingLog () {
							timingLog (fmt.Sprintf ("Fail-fast connection attempt skipped due to failure at %v, %v will become usable at %v", formatTime (timeConnectFail), sockAddr.m_sAddressAndPort, formatTime (timeUsable)))
						}
						continue
					}
				} // end if ! bIgnore
			} // end if bFound

			setSockAddrs = addSocketAddress (setSockAddrs, sockAddr)

		} // end for iPort
	} // end for iNameAddr

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("setSockAddrs=%v", setSockAddrs))
	}

	con.m_nConnectAttempts = 0
	timeBeginConnect := time.Now ()

	var errLast error
	var threadToUse *ConnectThread

	listThreads := [] *ConnectThread {}

	wtl := newWorkerThreadList ()

	for iSockAddr := 0 ; threadToUse == nil && (iSockAddr < len (setSockAddrs) || len (listThreads) > 0) ; {

		if len (listThreads) < int (con.m_params.M_uLCCLimit) && iSockAddr < len (setSockAddrs) {

			sockAddr := setSockAddrs [iSockAddr]
			iSockAddr++

			con.m_nConnectAttempts++

			if con.hasTimingLog () {
				timingLog (fmt.Sprintf ("Attempting %v connection %v to %v", ternary (sockAddr.m_bUseHTTPS, "HTTPS", "Teradata"), con.m_nConnectAttempts, sockAddr.m_sAddressAndPort))
			}

			thread := &ConnectThread {}
			listThreads = append (listThreads, thread)
			thread.start (con, sockAddr, con.m_params.M_uConnectTimeoutMs, wtl)

		} // end if

		// default to zero (indefinite wait), relying on the socket connect timeout when all the slots are in use or there are no more addresses
		var durWait time.Duration

		// or use the LCC interval wait time when a slot is available and we have another address to try
		if len (listThreads) < int (con.m_params.M_uLCCLimit) && iSockAddr < len (setSockAddrs) {
			durWait = time.Duration (con.m_params.M_uLCCIntervalMs) * time.Millisecond
		}

		listCompletedThreads := wtl.waitForThreads (durWait)

		// if the list is empty, that means that the LCC interval elapsed with no thread having
		// completed - neither a connect nor a failure - we will skip the loop below
		// and repeat the while-loop to start another connect if a slot is open

		for iCompletedThread := 0 ; iCompletedThread < len (listCompletedThreads) ; iCompletedThread++ {

			threadCompleted := listCompletedThreads [iCompletedThread]
			listThreads = removeThread (listThreads, threadCompleted)

			timeBeginDeathWait := time.Now ()
			threadCompleted.m_death.Wait ()

			if con.hasTimingLog () {
				timingLog (fmt.Sprintf ("%v connection attempt to %v with timeout %v ms took %v and %v, waiting for thread took %v",
					ternary (threadCompleted.m_sockAddr.m_bUseHTTPS, "HTTPS", "Teradata"),
					threadCompleted.m_sockAddr,
					con.m_params.M_uConnectTimeoutMs,
					formatDuration (threadCompleted.m_durElapsedTime),
					ternary (threadCompleted.m_err != nil, "failed", ternary (threadToUse == nil, "succeeded", "succeeded but was discarded")),
					formatElapsed (timeBeginDeathWait)))
			}

			// use the first successful connection of multiple possible
			if threadToUse == nil && isUsableSocket (con, ctx, threadCompleted, reconnect) {
				threadToUse = threadCompleted
			}

			if threadCompleted.m_err != nil { // connect failure - a slot is now open

				con.m_nConnectFailures++

				// Save the time of the most recent failure.
				g_mapConnectFailures.Store (threadCompleted.m_sockAddr.m_sAddressAndPort, threadCompleted.m_timeFailure)

				// wrap low-level error with more context
				threadCompleted.m_err = con.makeDriverError (threadCompleted.m_err, 0,
					fmt.Sprintf ("%v connection attempt to %v with timeout %v ms took %v and failed",
						ternary (threadCompleted.m_sockAddr.m_bUseHTTPS, "HTTPS", "Teradata"),
						threadCompleted.m_sockAddr,
						con.m_params.M_uConnectTimeoutMs,
						formatDuration (threadCompleted.m_durElapsedTime)))

				// chain exceptions together to preserve all failure reasons
				threadCompleted.m_err = causedBy (threadCompleted.m_err, errLast)

				errLast = threadCompleted.m_err

				if threadCompleted.m_socket != nil {
					threadCompleted.m_socket.Close ()
				}

				if threadCompleted.m_bHTTPSFailure && nHTTPSRetry > 0 {

					if con.hasDebugLog () {
						debugLog (fmt.Sprintf ("nHTTPSRetry=%v m_sockAddr=%v", nHTTPSRetry, threadCompleted.m_sockAddr))
					}

					setSockAddrs = append (setSockAddrs, threadCompleted.m_sockAddr) // retry the same address
					con.m_nHTTPSRetries++
					nHTTPSRetry--
				}
			} // end if threadCompleted.m_err
		} // end for iCompletedThread
	} // end for iSockAddr

	// cancel all the socket connects except for the one to use (which may be nil)
	for _, thread := range listThreads {
		if thread != threadToUse && thread.m_cancelConnect != nil {
			thread.m_cancelConnect ()
		}
	}

	// close all the sockets except for the one to use (which may be nil)
	for _, thread := range listThreads {
		if thread != threadToUse && thread.m_socket != nil {
			thread.m_socket.Close ()
		}
	}

	// wait for all the threads that are still running
	for _, thread := range listThreads {

		timeBeginWait := time.Now ()
		thread.m_death.Wait ()

		if con.hasTimingLog () {
			timingLog (fmt.Sprintf ("Discarded connection attempt to %v, waiting for thread took %v", thread.m_sockAddr, formatElapsed (timeBeginWait)))
		}
	}

	con.m_durConnectTotalTime = time.Now ().Sub (timeBeginConnect)
	if con.hasTimingLog () {
		timingLog (fmt.Sprintf ("%v connection total time %v", con.m_params.M_sHostName, formatDuration (con.m_durConnectTotalTime)))
	}

	if threadToUse != nil {
		con.m_socket             = threadToUse.m_socket
		con.m_sProxyUsed         = threadToUse.m_sProxyUsed
		con.m_sCertStatus        = threadToUse.m_sCertStatus
		con.m_durConnectTime     = threadToUse.m_durElapsedTime // save the successful connect time
		con.m_sConnectedHostName = threadToUse.m_sockAddr.m_sResolvedHostName // empty string if we connected with an IP address
		return
	}

	if errLast == nil {

		err = con.makeDriverError (nil, 0,
			fmt.Sprintf ("Failed to connect to %v because all IP addresses for this database have had recent connection failures within the Connect Failure Time-to-Live of %v seconds",
				con.m_params.M_sHostName,
				con.m_params.M_uConnectFailureTTL))

		return
	}

	err = con.makeDriverError (errLast, 0,
		fmt.Sprintf ("Failed to connect to %v",
		con.m_params.M_sHostName))

	return

} // end method connectToHost

func isUsableSocket (con *TeradataConnection, ctx context.Context, thread *ConnectThread, reconnect *Reconnect) bool {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter isUsableSocket %v thread=%+v", con, thread))
		defer func () { traceLog (fmt.Sprintf ("< leave isUsableSocket %v thread=%+v", con, thread)) } ()
	}

	if thread.m_err != nil { return false }

	defer func () {
		if thread.m_err != nil {
			thread.m_timeFailure = time.Now ()
		}
	} ()

	bTLSSocket := isTLSSocket (thread.m_socket)
	if bTLSSocket {

		checkCertForThread (con, thread)
		if thread.m_err != nil { return false }

		// RFC 6455 The WebSocket Protocol
		// The request MUST include a header field with the name Sec-WebSocket-Key.
		// The value of this header field MUST be a nonce consisting of a randomly selected 16-byte value that has been base64-encoded

		sSecWebSocketKey := base64.StdEncoding.EncodeToString (randomBytes (16))

		// RFC 2616 section 14.23 Host
		// The Host request-header field specifies the Internet host and port number of the resource being requested

		sHTTPRequest := "GET /gateway HTTP/1.1\r\n" +
			"Host: " + thread.m_sockAddr.m_sAddressAndPort + "\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Key: " + sSecWebSocketKey + "\r\n" +
			"Sec-WebSocket-Version: 13\r\n" +
			"\r\n"

		abyHTTPRequest := [] byte (sHTTPRequest)
		if con.hasDumpLog () {
			dumpLogHexDump ("Transmit HTTP request to " + thread.m_sockAddr.m_sAddressAndPort, abyHTTPRequest)
		}

		timeStart := time.Now ()

		errWrite := socketWrite (thread.m_socket, abyHTTPRequest)
		if errWrite != nil {

			if con.hasTimingLog () { timingLog ("Transmit HTTP request to " + thread.m_sockAddr.m_sAddressAndPort + " took " + formatElapsed (timeStart) + " and failed") }

			thread.m_err = con.makeDriverError (errWrite, 0, "Failed to transmit HTTP request to " + thread.m_sockAddr.m_sAddressAndPort)
			return false
		}

		if con.hasTimingLog () { timingLog ("Transmit HTTP request to " + thread.m_sockAddr.m_sAddressAndPort + " took " + formatElapsed (timeStart)) }

		timeStart = time.Now ()

		abyRawHeaders, asSplitHeaders, _, errRead := readHttpHeaders (thread.m_socket)
		if errRead != nil {

			if con.hasTimingLog () { timingLog ("Read HTTP response from " + thread.m_sockAddr.m_sAddressAndPort + " took " + formatElapsed (timeStart) + " and failed") }

			thread.m_bHTTPSFailure = true
			thread.m_err = con.makeDriverError (errRead, 0, "Failed to read HTTP response from " + thread.m_sockAddr.m_sAddressAndPort)
			return false
		}

		if con.hasTimingLog () { timingLog ("Read HTTP response from " + thread.m_sockAddr.m_sAddressAndPort + " took " + formatElapsed (timeStart)) }

		if con.hasDumpLog () {
			dumpLogHexDump ("Received HTTP response from " + thread.m_sockAddr.m_sAddressAndPort, abyRawHeaders)
		}

		if len (asSplitHeaders) < 2 ||
		"HTTP/1.1 101 Switching Protocols" != asSplitHeaders [0] ||
		"Upgrade: websocket" != asSplitHeaders [1] {

			thread.m_bHTTPSFailure = ! bytes.HasPrefix (abyRawHeaders, [] byte ("HTTP"))
			thread.m_err = con.makeDriverError (nil, 0,
				fmt.Sprintf ("The database returned an unexpected %v-byte length HTTP response %v",
				len (abyRawHeaders),
				strings.Join (asSplitHeaders, "\n")))

			return false
		}
	} // end if bTLSSocket

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("Connection to %v m_nSSLModeLevel=%v bTLSSocket=%v", thread.m_sockAddr, con.m_params.m_nSSLModeLevel, bTLSSocket))
	}

	switch con.m_params.m_nSSLModeLevel { case LEVEL_ALLOW, LEVEL_PREFER:

		thread.m_err = con.logonController (thread.m_socket, ctx, reconnect)
		if thread.m_err != nil {
			thread.m_bHTTPSFailure = true
			return false
		}

		if con.hasDebugLog () {
			debugLog (fmt.Sprintf ("Connection to %v m_nSSLModeLevel=%v bTLSSocket=%v isLegacyPortAvailable=%v isHTTPSPortAvailable=%v",
				thread.m_sockAddr,
				con.m_params.m_nSSLModeLevel,
				bTLSSocket,
				con.m_gtwConfig.isLegacyPortAvailable (),
				con.m_gtwConfig.isHTTPSPortAvailable ()))
		}

		if con.m_params.m_nSSLModeLevel == LEVEL_ALLOW && bTLSSocket && con.m_gtwConfig.isLegacyPortAvailable () {

			thread.m_err = con.makeDriverError (nil, 0,
				fmt.Sprintf ("sslmode=ALLOW cannot use HTTPS port %v connection because the database has Teradata port connections enabled, but a connection cannot be made on Teradata port %v",
					con.m_params.M_uHTTPSPort,
					con.m_params.M_uDbsPort))

			return false
		}

		if con.m_params.m_nSSLModeLevel == LEVEL_PREFER && ! bTLSSocket && con.m_gtwConfig.isHTTPSPortAvailable () {

			thread.m_err = con.makeDriverError (nil, 0,
				fmt.Sprintf ("sslmode=PREFER cannot use Teradata port %v connection because the database has HTTPS connections enabled, but a connection cannot be made on HTTPS port %v",
					con.m_params.M_uDbsPort,
					con.m_params.M_uHTTPSPort))

			return false
		}
	} // end switch allow or prefer

	return true

} // end isUsableSocket

type ConnectThread struct {

	m_sockAddr          socketAddress
	m_socket            net.Conn // interface type can be nil
	m_sProxyUsed        string
	m_death             sync.WaitGroup
	m_cancelConnect     context.CancelFunc // function type can be nil
	m_durElapsedTime    time.Duration
	m_bHTTPSFailure     bool
	m_err               error // interface type can be nil
	m_timeFailure       time.Time // valid when m_err != nil
	m_sCertStatus       string

} // end type ConnectThread

func (thread *ConnectThread) start (con *TeradataConnection, sockAddr socketAddress, nTimeoutMs uint32, wtl *WorkerThreadList) {

	thread.m_death.Add (1) // must be called in main goroutine; cannot be called in goroutine function

	go thread.run (con, sockAddr, nTimeoutMs, wtl)

} // end method start

func (thread *ConnectThread) run (con *TeradataConnection, sockAddr socketAddress, nTimeoutMs uint32, wtl *WorkerThreadList) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter ConnectThread.run %v thread=%+v", con, thread))
		defer func () { traceLog (fmt.Sprintf ("< leave ConnectThread.run %v thread=%+v", con, thread)) } ()
	}

	defer thread.m_death.Done ()

	thread.m_sockAddr = sockAddr

	ctxConnect, cancelConnect := context.WithCancel (context.Background ())
	thread.m_cancelConnect = cancelConnect

	timeStart := time.Now ()

	thread.m_socket, thread.m_sProxyUsed, thread.m_err = connectSocket (con.m_params, sockAddr, nTimeoutMs, ctxConnect)

	thread.m_durElapsedTime = time.Since (timeStart)

	if thread.m_err != nil {
		thread.m_timeFailure = time.Now ()
	}

	wtl.reportCompletion (thread)

} // end method run

type WorkerThreadList struct {

	m_mon                     *Monitor
	m_listCompletedThreads    [] *ConnectThread

} // end type WorkerThreadList

func newWorkerThreadList () (*WorkerThreadList) {

	return &WorkerThreadList {
		m_mon                     : NewMonitor (),
		m_listCompletedThreads    : [] *ConnectThread {},
	}
} // newWorkerThreadList

func (wtl *WorkerThreadList) waitForThreads (durTimeout time.Duration) (listToReturn [] *ConnectThread) {

	wtl.m_mon.L.Lock () // synchronized method
	defer wtl.m_mon.L.Unlock ()

	timeStart := time.Now ()

	for len (wtl.m_listCompletedThreads) == 0 {

		var durWaitTime time.Duration

		if durTimeout == 0 { // indefinite
			durWaitTime = 0 // indefinite

		} else { // durTimeout != 0
			timeEnd := timeStart.Add (durTimeout)
			durWaitTime = timeEnd.Sub (time.Now ())
			if (durWaitTime <= 0) { // if we ran out of time
				break
			}
		}

		wtl.m_mon.Wait (durWaitTime)

	} // end for

	listToReturn = wtl.m_listCompletedThreads
	wtl.m_listCompletedThreads = [] *ConnectThread {}
	return

} // end method waitForThreads

func (wtl *WorkerThreadList) reportCompletion (thread *ConnectThread) {

	wtl.m_mon.L.Lock () // synchronized method
	defer wtl.m_mon.L.Unlock ()

	wtl.m_listCompletedThreads = append (wtl.m_listCompletedThreads, thread)
	wtl.m_mon.Signal ()

} // end method reportCompletion
