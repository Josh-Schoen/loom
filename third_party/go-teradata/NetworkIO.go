// Copyright 2021 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
	"github.com/Teradata-TIO/go-teradata/goteragss"
)

type NetworkIO struct {

	socket              net.Conn // nil socket means use the connection's socket
	ctx                 context.Context // nil ctx means no timeout or cancel
	abyMsgToSend        [] byte
	bAbortable          bool
	bProcessingElicit   bool
	bSkipFrame          bool
	ptimeStartSend      *time.Time // set by sendMessage, used by receiveMessageBody
	rows                *TeradataRows

} // end type NetworkIO

func (con *TeradataConnection) socket (nio *NetworkIO) net.Conn {

	if nio.socket != nil { return nio.socket }

	return con.m_socket

} // end method socket

func (con *TeradataConnection) dumpMessage (sTitle string, aby [] byte) {

	if con.hasDumpLog () {
		dumpLogHexDump (fmt.Sprintf ("%v message dump = %v bytes", sTitle, len (aby)), aby)
	}
} // end method dumpMessage

func (con *TeradataConnection) socketSetReadDeadline (nio *NetworkIO, deadline time.Time) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter socketSetReadDeadline %v timeout=%v", con, formatTimeout (deadline)))
		defer func () { traceLog (fmt.Sprintf ("< leave socketSetReadDeadline %v err=%v", con, err)) } ()
	}

	socket := con.socket (nio)
	if socket == nil {
		err = con.makeBadConnectionError (nil, 570, "Connection is closed")
		return
	}

	err = socket.SetReadDeadline (deadline)
	return

} // end socketSetReadDeadline

func (con *TeradataConnection) sendAndReceive (nio *NetworkIO) (abyResponseMsg [] byte, bAbortSent bool, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter sendAndReceive %v len=%v rows=%v", con, len (nio.abyMsgToSend), nio.rows))
		defer func () { traceLog (fmt.Sprintf ("< leave sendAndReceive %v err=%v", con, err)) } ()
	}

	if nio.ctx != nil && wasInterrupted (nio.ctx) {

		err = con.makeInterruptError (nio.ctx.Err (), false /*abort sent*/)

		if con.hasDebugLog () {
			debugLog (fmt.Sprintf ("Message was interrupted before being sent, context err = %v", nio.ctx.Err ()))
		}

		return
	}

	err = con.sendMessage (nio)

	bOrigReadState := con.m_bReadInProgress
	con.m_bReadInProgress = true
	defer func () {
		con.m_bReadInProgress = bOrigReadState
		if ! con.m_bReadInProgress {
			con.m_nRecoverableNetworkState = 2 // complete
		}
	} ()

	abyResponseMsg, bAbortSent, err = con.receiveMessage (nio, err)
	return

} // end method sendAndReceive

func (con *TeradataConnection) receiveMessage (nio *NetworkIO, errSend error) (abyResponseMsg [] byte, bAbortSent bool, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter receiveMessage %v bAbortable=%v bProcessingElicit=%v bSkipFrame=%v rows=%v errSend=%v", con, nio.bAbortable, nio.bProcessingElicit, nio.bSkipFrame, nio.rows, errSend))
		defer func () { traceLog (fmt.Sprintf ("< leave receiveMessage %v bAbortSent=%v err=%v", con, bAbortSent, err)) } ()
	}

	defer func () {
		if err != nil {
			abyResponseMsg, err = con.processReconnect (nio, err)
		}
	} ()

	if errSend != nil {
		err = errSend
		return
	}

	if nio.ctx != nil {
		if deadline, ok := nio.ctx.Deadline () ; ok {
			err = con.socketSetReadDeadline (nio, deadline)
			if err != nil { return }
		}
	} // end if ctx

	var chReadComplete chan bool
	var chFinished     chan bool

	if nio.ctx != nil {
		// Create a thread to listen for a context timeout or cancel. The thread will end
		// when either a timeout/cancel occurs or the read of the message header completes.
		chReadComplete = make (chan bool, 1)
		chFinished     = make (chan bool)
		go func () {
			defer func () { chFinished <- true } ()

			for {
				select {
					case <- nio.ctx.Done () :
						// stop the read
						if nio.ctx.Err () == context.Canceled {
							con.socketSetReadDeadline (nio, time.Now ())
						}
						if con.hasDebugLog () {
							debugLog (fmt.Sprintf ("ctx.Done occurred: ctx.Err=%v", nio.ctx.Err ()))
						}
						return

					case <- chReadComplete :
						return

					default :
						time.Sleep (20 * time.Millisecond)
				}
			} // end for
		} ()
	} // end if ctx

	abyResponseMsg, err = con.receiveMessageHeader (nio)

	if nio.ctx != nil {
		// It is possible the deadline for the socket interrupted the read, receiveMessageHeader completed and chReadComplete
		// was set while the thread was still sleeping. The thread woke because chReadComplete was true. This is a problem if the
		// ctx.Err () is not set until after it is checked below which means no abort is sent and no interrupt error is generated.
		if (err == nil || ! strings.Contains (fmt.Sprintf ("%s", err), "i/o timeout")) && nio.ctx.Err () == nil {
			chReadComplete <- true
		}

		<- chFinished

		// Documentation for net.Conn.SetReadDeadline says "A zero value for t means Read will not time out."
		timeZeroValue := time.Time {}

		// Reset the socket timeout so it won't time out while sending the abort.
		// This must be done after the thread completes since the thread can change the timeout.
		con.socketSetReadDeadline (nio, timeZeroValue) // the socket can be nil here if the session was aborted

	} // end if ctx

	// There could be a context error after the request completed successfully so no
	// point in sending an abort.
	if err != nil {

		if nio.bAbortable && nio.ctx != nil && (nio.ctx.Err () == context.DeadlineExceeded || nio.ctx.Err () == context.Canceled) {

			var errAbort error
			abyResponseMsg, errAbort = con.executeAsynchronousAbort (/*bUseUCAbort*/ nio.bProcessingElicit)
			bAbortSent = true

			// If the abort was too late, then clear the err and let the request finish
			if errAbort == nil {
				err = nil
			} else {
				err = causedBy (errAbort, con.makeInterruptError (nio.ctx.Err (), true /*abort sent*/))
			}

			if con.hasDebugLog () {
				debugLog (fmt.Sprintf ("Request was interrupted errAbort is %v", errAbort))
			}
		}

		// Even if the abort did not take, the response body was received so return
		return
	}

	abyResponseMsg, err = con.receiveMessageBody (nio, abyResponseMsg)
	return

} // end method receiveMessage

func (con *TeradataConnection) sendMessage (nio *NetworkIO) (err error) {

	uRequestNumber := binary.BigEndian.Uint32 (nio.abyMsgToSend [32 : ])
	sTitle := MessageTitle (nio.abyMsgToSend, false)

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter sendMessage %v req=%v %v len=%v", con, uRequestNumber, sTitle, len (nio.abyMsgToSend)))
		defer func () { traceLog (fmt.Sprintf ("< leave sendMessage %v req=%v", con, uRequestNumber)) } ()
	}

	byMsgKind := nio.abyMsgToSend [2]

	if con.isRNPActive () &&
	! con.m_bReadInProgress &&
	(byMsgKind == MESSAGE_KIND_START || byMsgKind == MESSAGE_KIND_CONTINUE || byMsgKind == MESSAGE_KIND_ELICIT) {

		// Save a copy of the message before encryption
		con.m_abyLastSentMsg = nio.abyMsgToSend [ : ] // copy
		con.m_nRecoverableNetworkState = 0 // sent
	}

	bAppRequestedDataEncryption := con.m_params.M_bEncryptData
	bWrapDataMessages           := con.m_uSecurityRequired >= 1
	bCentralAdminDataEncryption := con.m_uConfidentialityRequired >= 1

	bUsingTLS := isTLSSocket (con.socket (nio))

	var bWrap    bool
	var bPrivacy bool

	switch byMsgKind {

		case MESSAGE_KIND_CONFIG, MESSAGE_KIND_ASSIGN, MESSAGE_KIND_REASSIGN, MESSAGE_KIND_AUTHMETHODS, MESSAGE_KIND_SSO:
			bWrap    = false
			bPrivacy = false

		case MESSAGE_KIND_CONNECT, MESSAGE_KIND_RECONNECT:
			bWrap    = ! bUsingTLS
			bPrivacy = true

		default:
			bWrap    = ! bUsingTLS && (bAppRequestedDataEncryption || bWrapDataMessages)
			bPrivacy = bWrap && (bAppRequestedDataEncryption || bCentralAdminDataEncryption)
	}

	if bWrap {

		con.dumpMessage (sTitle, nio.abyMsgToSend)

		abyPortionToEncrypt := nio.abyMsgToSend [24 : ]

		nConfidentialityDesired := func () int { if bPrivacy { return 1 } ; return 0 } ()
		uQOPDesired             := con.m_uSecurityLevel

		status, nConfidentialityApplied, abyEncryptedPortion := con.m_pSecurity.GssWrap (nConfidentialityDesired, uQOPDesired, abyPortionToEncrypt)

		if status.Major == goteragss.GSS_S_COMPLETE {
			if con.hasDebugLog () {
				debugLog (fmt.Sprintf ("GssWrap SUCCESS nConfidentialityDesired=%v nConfidentialityApplied=%v uQOPDesired=%v", nConfidentialityDesired, nConfidentialityApplied, uQOPDesired))
			}
		} else {
			err = con.makeChainedSecurityError (status)
			return
		}

		nio.abyMsgToSend = remakeEncryptedRequestMessage (nio.abyMsgToSend, abyEncryptedPortion)

	} // end if bWrap

	var abyFrame [] byte
	if bUsingTLS {

		// The first byte of WebSocket frame contains the byte value 0x82.
		//   0x80 is the FIN bit to indicate the final fragment of the message and 0x02 is the opcode to indicate a binary data frame.
		// The second byte of the WebSocket frame describes the payload length.
		//   If the second byte has value 0 through 125, that is the payload length.
		//   If the second byte has value 126, the following 2 bytes are a uint16 payload length in network byte order (big-endian).
		//   If the second byte has value 127, the following 8 bytes are a uint64 payload length in network byte order (big-endian).

		length := len (nio.abyMsgToSend)

		nFrameHeaderByteCount := func () int { switch {
			case length <=    125 : return  2
			case length <= 0xFFFF : return  4
			default               : return 10
		} } ()

		// Prepend the WebSocket frame header to the Teradata message for better performance.
		abyFrame = make ([] byte, nFrameHeaderByteCount + length)
		copy (abyFrame [nFrameHeaderByteCount : ], nio.abyMsgToSend)

		abyFrame [0] = 0x82

		if (length <= 125) {
			abyFrame [1] = byte (length)

		} else if (length <= 0xFFFF) {
			abyFrame [1] = 126
			abyFrame [2] = byte ((length >> 8) & 0xFF) // big-endian payload length
			abyFrame [3] = byte ( length       & 0xFF)

		} else {
			abyFrame [1] = 127
			// abyFrame [2] == 0 // leave as zero the upper 32-bits of WebSocket payload length
			// abyFrame [3] == 0
			// abyFrame [4] == 0
			// abyFrame [5] == 0
			abyFrame [6] = byte ((length >> 24) & 0xFF) // big-endian payload length
			abyFrame [7] = byte ((length >> 16) & 0xFF)
			abyFrame [8] = byte ((length >>  8) & 0xFF)
			abyFrame [9] = byte ( length        & 0xFF)
		}

		if con.hasDumpLog () {
			dumpLogHexDump ("Transmit WebSocket frame", abyFrame [ : nFrameHeaderByteCount])
		}
	} // end if isTLSSocket

	con.dumpMessage (sTitle, nio.abyMsgToSend)

	if abyFrame != nil { nio.abyMsgToSend = abyFrame }

	timeNow := time.Now ()
	nio.ptimeStartSend = &timeNow

	err = con.writeToSocket (nio)
	if err != nil {
		if con.hasTimingLog () { timingLog (fmt.Sprintf ("Send message took %v and failed", formatElapsed (*nio.ptimeStartSend))) }
		err = con.makeDriverError (err, 0, fmt.Sprintf ("Failure sending %v message", sTitle))
		return
	}

	return

} // end method sendMessage

func (con *TeradataConnection) receiveMessageHeader (nio *NetworkIO) (abyHeader [] byte, err error) {

	// Initialize with dummy values for deferred trace logging.
	uRequestNumber     := uint32 (0)
	sTitle             := "unknown"
	nMessageBodyLength := -1

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter receiveMessageHeader %v con.m_nFrameBytesToRead=%v", con, con.m_nFrameBytesToRead))
		defer func () { traceLog (fmt.Sprintf ("< leave receiveMessageHeader %v req=%v sTitle=%v nMessageBodyLength=%v con.m_nFrameBytesToRead=%v", con, uRequestNumber, sTitle, nMessageBodyLength, con.m_nFrameBytesToRead)) } ()
	}

	bUsingTLS := isTLSSocket (con.socket (nio))
	timeStart := time.Now ()

	if ! nio.bSkipFrame && bUsingTLS && con.m_nFrameBytesToRead != 0 {

		nFrameLength := 0
		abyFrame     := make ([] byte, 10)

		// If m_nFrameBytesToRead is -1, then driver needs to read the entire WebSocket frame
		if con.m_nFrameBytesToRead == -1 {

			nFrameLength  = 2
			err = con.readFromSocket (nio, abyFrame [ : nFrameLength])

			if err == nil {
				switch abyFrame [1] & 0x7F {
					case 126: nFrameLength += 2
					case 127: nFrameLength += 8
				}
			} else {
				if con.hasTimingLog () { timingLog (fmt.Sprintf ("Receive WebSocket frame took %v and failed", formatElapsed (timeStart))) }
				err = con.makeDriverError (err, 0, "Failure receiving WebSocket frame")
				return
			}
		}

		// if it is zero than we already read the first two bytes of the frame before socket timed out
		if nFrameLength == 0 {
			nFrameLength = con.m_nFrameBytesToRead + 2
		}

		if nFrameLength > 2 {
			err = con.readFromSocket (nio, abyFrame [2 : nFrameLength])
			if err != nil {
				if con.hasTimingLog () { timingLog (fmt.Sprintf ("Receive WebSocket frame took %v and failed", formatElapsed (timeStart))) }

				if strings.Contains (fmt.Sprintf ("%s", err), "timeout") {
					// Timed out after reading the first two bytes of the web socket frame, but the driver we still need to finish reading the frame
					con.m_nFrameBytesToRead = nFrameLength - 2
				}

				err = con.makeDriverError (err, 0, "Failure receiving WebSocket frame")
				return
			}
		}

		if con.hasDumpLog () {
			dumpLogHexDump ("Received WebSocket frame", abyFrame [ : nFrameLength])
		}
	} // end if

	abyHeader = make ([] byte, 52)
	err = con.readFromSocket (nio, abyHeader)
	if err != nil {
		if con.hasTimingLog () { timingLog (fmt.Sprintf ("Receive message header took %v and failed", formatElapsed (timeStart))) }

		if strings.Contains (fmt.Sprintf ("%s", err), "timeout") {
			// The WebSocket frame has been read but was interrupted before reading the message header. Set to 0 so it is not read twice.
			con.m_nFrameBytesToRead = 0
		}

		err = con.makeDriverError (err, 0, "Failure receiving message header")
		return
	}

	// No interrupts and the full message header was read including the WebSocket frame.
	con.m_nFrameBytesToRead = -1
	uRequestNumber = binary.BigEndian.Uint32 (abyHeader [32 : ])
	sTitle = MessageTitle (abyHeader, false)
	nMessageBodyLength = MessageBodyLength (abyHeader)

	if con.hasTimingLog () {
		timingLog (fmt.Sprintf ("Receive header %v message took %v", sTitle, formatElapsed (timeStart)))
	}

	return

} // end method receiveMessageHeader

func (con *TeradataConnection) receiveMessageBody (nio *NetworkIO, abyHeader [] byte) (abyResponseMsg [] byte, err error) {

	// Initialize values here for deferred trace logging.
	uRequestNumber       := binary.BigEndian.Uint32 (abyHeader [32 : ])
	sTitle               := MessageTitle (abyHeader, false)
	nMessageBodyLength   := MessageBodyLength (abyHeader)
	nActualMsgBodyLength := -1

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter receiveMessageBody %v req=%v title=%v nMessageBodyLength=%v rows=%v", con, uRequestNumber, sTitle, nMessageBodyLength, nio.rows))
		defer func () { traceLog (fmt.Sprintf ("< leave receiveMessageBody %v req=%v title=%v nMessageBodyLength=%v nActualMsgBodyLength=%v", con, uRequestNumber, sTitle, nMessageBodyLength, nActualMsgBodyLength)) } ()
	}

	abyResponseMsg = make ([] byte, 52 + nMessageBodyLength)
	copy (abyResponseMsg, abyHeader)

	if con.m_bLoggedOn {
		con.m_bInTransaction = inTransaction (abyResponseMsg)
	}

	timeStart := time.Now ()

	err = con.readFromSocket (nio, abyResponseMsg [52 : ])
	if err != nil {
		if con.hasTimingLog () { timingLog (fmt.Sprintf ("Receive %v message took %v and failed", sTitle, formatElapsed (timeStart))) }
		err = con.makeDriverError (err, 0, fmt.Sprintf ("Failure receiving %v message body", sTitle))
		return
	}

	if con.hasTimingLog () {
		s := fmt.Sprintf ("Receive %v message body took %v", sTitle, formatElapsed (timeStart))
		if nio.ptimeStartSend != nil { s += fmt.Sprintf (", send and receive took %v", formatElapsed (*nio.ptimeStartSend)) }
		timingLog (s)
	}

	bUnwrap := (abyResponseMsg [1] & 0x80) != 0
	if bUnwrap {

		con.dumpMessage (sTitle, abyResponseMsg)

		abyPortionToDecrypt := abyResponseMsg [24 : ]

		status, nConfidentialityApplied, nQOPApplied, abyDecryptedPortion := con.m_pSecurity.GssUnwrap(abyPortionToDecrypt)

		if status.Major == goteragss.GSS_S_COMPLETE {
			if con.hasDebugLog () {
				debugLog (fmt.Sprintf ("GssUnwrap SUCCESS nConfidentialityApplied=%v nQOPApplied=%v", nConfidentialityApplied, nQOPApplied))
			}
		} else {
			err = con.makeChainedSecurityError (status)
			return
		}

		abyResponseMsg = remakeDecryptedResponseMessage (abyResponseMsg, abyDecryptedPortion)

	} // end if bUnwrap

	nActualMsgBodyLength = len (abyResponseMsg) // used by deferred trace logging

	con.dumpMessage (sTitle, abyResponseMsg)

	abyResponseMsg, err = con.processControlData (nio, abyResponseMsg)
	return

} // end method receiveMessageBody

func (con *TeradataConnection) socketErrorEOF (err error) (bOutput bool) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter socketErrorEOF %v err=%v", con, err))
		defer func () { traceLog (fmt.Sprintf ("< leave socketErrorEOF %v bOutput=%v", con, bOutput)) } ()
	}

	if err == nil { return }

	if errors.Is (err, io.EOF) {
		bOutput = true
		return
	}

	asMessages := [] string {
		"connection was forcibly closed",
		"connection was aborted",
		"broken pipe",
		"connection reset",
		"connection closed",
	}

	sError := err.Error ()
	for _, sMessage := range asMessages {
		if strings.Contains (sError, sMessage) {
			bOutput = true
			return
		}
	}

	return

} // end method socketErrorEOF

func (con *TeradataConnection) writeToSocket (nio *NetworkIO) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter writeToSocket %v len=%v", con, len (nio.abyMsgToSend)))
		defer func () { traceLog (fmt.Sprintf ("< leave writeToSocket %v len=%v", con, len (nio.abyMsgToSend))) } ()
	}

	socket := con.socket (nio)
	if socket == nil {
		err = con.makeBadConnectionError (nil, 570, "Connection is closed")
		return
	}

	err = socketWrite (socket, nio.abyMsgToSend)
	return

} // end method writeToSocket

func (con *TeradataConnection) readFromSocket (nio *NetworkIO, aby [] byte) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter readFromSocket %v len=%v", con, len (aby)))
		defer func () { traceLog (fmt.Sprintf ("< leave readFromSocket %v len=%v", con, len (aby))) } ()
	}

	socket := con.socket (nio)
	if socket == nil {
		err = con.makeBadConnectionError (nil, 570, "Connection is closed")
		return
	}

	_, err = socketRead (socket, aby)
	return

} // end method readFromSocket

const ABORTIVE = true  // intended for bAbortive argument
const GRACEFUL = false // intended for bAbortive argument
var IGNORE_ERROR *error // intended for perrPreserve argument

func (con *TeradataConnection) closeSocket (bAbortive bool, perrPreserve *error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter closeSocket bAbortive=%v %v", bAbortive, con))
		defer func () { traceLog (fmt.Sprintf ("< leave closeSocket bAbortive=%v %v", bAbortive, con)) } ()
	}

	if con.m_socket == nil {
		if con.hasDebugLog () { debugLog (fmt.Sprintf ("closeSocket: Socket is already closed %v", con)) }
		return
	}

	if bAbortive {
		asTCPConn (con.m_socket).SetLinger (0)
	}

	err := con.m_socket.Close ()

	if err != nil {
		if perrPreserve != nil && *perrPreserve == nil {
			*perrPreserve = err
		}

		if con.hasDebugLog () { debugLog (fmt.Sprintf ("Failure closing socket: %v", err)) }
	}

	con.m_socket = nil

} // end method closeSocket

func (con *TeradataConnection) processReconnect (nio *NetworkIO, errOriginal error) (abyResponseMsg [] byte, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter processReconnect %v errOriginal=%v", con, errOriginal))
		defer func () { traceLog (fmt.Sprintf ("< leave processReconnect %v len(abyResponseMsg)=%v err=%v", con, len (abyResponseMsg), err)) } ()
	}

	if ! con.socketErrorEOF (errOriginal) {
		err = errOriginal
		return
	}

	bReconnectEnabled := con.isRNPActive () || con.m_params.M_uReconnectCount != 0 || con.m_params.M_uReconnectInterval != 0

	if ! con.m_bLoggedOn || ! bReconnectEnabled || con.m_bSuppressReconnect || con.m_bReconnectInProgress {

		con.closeSocket (GRACEFUL, IGNORE_ERROR)
		err = con.makeBadConnectionError (errOriginal, 503, "Lost connection to the Teradata Database")
		return
	}

	con.m_bReconnectInProgress = true // prevent recursion
	func () { // try
		defer func () { con.m_bReconnectInProgress = false } () // finally

		nReconnectCount := int (con.m_params.M_uReconnectCount)
		if nReconnectCount == 0 { nReconnectCount = 11 } // default if omitted is 11 attempts

		nReconnectInterval := int (con.m_params.M_uReconnectInterval)
		if nReconnectInterval == 0 { nReconnectInterval = 30 } // default if omitted is 30 seconds

		// The maximum possible elapsed time for reconnect attempts is: (M_uReconnectCount - 1) * M_uReconnectInterval
		// Using the default values, the maximum possible elapsed time for reconnect attempts is: (11 - 1) * 30 sec = 10 * 30 sec = 5 minutes

		for nAttempt := 1 ; ; nAttempt++ {

			con.closeSocket (ABORTIVE, IGNORE_ERROR)

			con.m_nReconnectAttempts++

			if nAttempt > 1 {
				if (con.hasTimingLog ()) {
					timingLog (fmt.Sprintf ("Reconnect attempt %v of %v waiting %v seconds for connection %v", nAttempt, nReconnectCount, nReconnectInterval, con))
				}

				threadSleep (con.hasTimingLog (), time.Duration (nReconnectInterval) * time.Second)
			}

			if (con.hasTimingLog ()) {
				timingLog (fmt.Sprintf ("Reconnect attempt %v of %v for connection %v", nAttempt, nReconnectCount, con))
			}

			reconnect := &Reconnect { uCryptoNonce: con.m_uCryptoNonce.Load (), uRequestNumber: con.m_uCurrentRequestNumber.Load () }

			err = con.connectToHost (context.Background (), reconnect)
			if err != nil {

				// socket is already closed in case of failure
				if nAttempt == nReconnectCount {
					err = causedBy (con.makeBadConnectionError (err, 503, "Unable to reconnect to the Teradata Database"), errOriginal)
					return
				}

				continue // try again to connect
			}

			// at this point, the socket is successfully open

			err = con.logonController (/*socket*/ nil, /*ctxLogin*/ nil, reconnect) // recursive call
			if err == nil { break } // break out of the loop after successfully logging on

			// a failure occurred during the reconnect logon handshake - must close the socket
			con.closeSocket (GRACEFUL, IGNORE_ERROR)

			// try again if the logon failed due to a late-restarting MPP node, or bail out if the logon failed for some other reason
			if nAttempt == nReconnectCount || ! errors.Is (err, driver.ErrBadConn) {
				err = causedBy (con.makeBadConnectionError (err, 503, "Unable to reconnect to the Teradata Database"), errOriginal)
				return
			}
		} // end for nAttempt

		// the Start Response message is supposed to be available immediately to indicate the status of the last response
		// The concatenated Start Response message does not have a WebSocket frame, so we must skip reading it.
		abyResponseMsg, _, err = con.receiveMessage (&NetworkIO { bSkipFrame: true }, /*errSend*/ nil) // recursive call
		if err != nil {

			// unable to get the Start Response message, perhaps due to timeout - must close the socket
			con.closeSocket (GRACEFUL, IGNORE_ERROR)
			err = causedBy (con.makeBadConnectionError (err, 503, "Unable to reconnect to the Teradata Database"), errOriginal)
			return
		}
	} () // end try m_bReconnectInProgress

	// If the session was aborted by an administrator, the Gateway will close the socket after the reconnect logon, and it will appear to be a zero-length response message.
	if len (abyResponseMsg) == 0 {
		con.closeSocket (GRACEFUL, IGNORE_ERROR)
		err = con.makeBadConnectionError (errOriginal, 503, "Unable to reconnect to the Teradata Database")
		return
	}

	sMsgTitle := MessageTitle (abyResponseMsg, false)
	aabyParcelHeaders := getParcelHeadersFromMessage (abyResponseMsg)

	var errReconnect error
	var byRedriveOrNewRequestOK byte
	var mapKnownRequests map [uint32] byte

	for i, abyParcelHeader := range aabyParcelHeaders {

		uFlavor       := cookedParcelFlavor    (abyParcelHeader)
		abyParcelBody := constrainedParcelBody (abyParcelHeader)

		if con.hasDebugLog () {
			debugLog (fmt.Sprintf ("Parcel %v of %v %v %v", i + 1, len (aabyParcelHeaders), parcelFlavor (uFlavor), uFlavor))
		}

		switch uFlavor {

			case PCLENDREQUEST:

				// indicates that the last request was successfully completed

			case PCLFAILURE, PCLERROR:

				errReconnect = con.makeDatabaseError (abyParcelHeader)

			case PCLSESSIONSTATUSRESP:

				byRedriveOrNewRequestOK, mapKnownRequests = con.processSessionStatusResponseParcel (abyParcelBody)

			default:

				con.closeSocket (GRACEFUL, IGNORE_ERROR)
				err = con.makeBadConnectionError (errOriginal, 503, fmt.Sprintf ("%v message contains unexpected parcel %v %v after reconnect to the Teradata Database",
					sMsgTitle,
					parcelFlavor (uFlavor),
					uFlavor))
				return

		} // end switch
	} // end for

	if errReconnect != nil {
		errReconnect = causedBy (errReconnect, errOriginal)
	} else {
		errReconnect = con.makeDriverError (errOriginal, 0, "Reconnected to the Teradata Database after a communication failure")
	}

	bAttemptRecoverableNP := mapKnownRequests != nil && con.m_abyLastSentMsg != nil

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("processReconnect: errReconnect=%v", errReconnect))
		debugLog (fmt.Sprintf ("processReconnect: byRedriveOrNewRequestOK=%v mapKnownRequests=%v bAttemptRecoverableNP=%v", byRedriveOrNewRequestOK, mapKnownRequests, bAttemptRecoverableNP))
	}

	if bAttemptRecoverableNP {

		byLastSentMsgKind      := con.m_abyLastSentMsg [2]
		uLastSentRequestNumber := binary.BigEndian.Uint32 (con.m_abyLastSentMsg [32 : ])
		_, bIsKnownRequest     := mapKnownRequests [uLastSentRequestNumber]

		// From the Redrive SIIS 541-0008587A03 section 3.20.2:
		// RedriveOrNewRequestOK is binary 1 if the client can submit a new request or redrive an outstanding request without informing the application of the database or network failure.
		// If the client has an outstanding request on the server and the request can't be redriven, then RedriveOrNewRequestOK is set to 0.
		// If the request was the killer request, then it won't be redriven and RedriveOrNewRequestOK is set to 0.

		bMultiTSRContinuation := false
		if byRedriveOrNewRequestOK == 0 && byLastSentMsgKind == MESSAGE_KIND_START {

			aabyParcelHeaders := getParcelHeadersFromMessage (con.m_abyLastSentMsg)

			if peekCookedParcelFlavor (0, aabyParcelHeaders) == PCLMULTITSR {

				abyParcelBody   := constrainedParcelBody (aabyParcelHeaders [0])
				uSequenceNumber := binary.BigEndian.Uint16 (abyParcelBody)

				bMultiTSRContinuation = uSequenceNumber > 1
			}
		}

		if con.hasDebugLog () {
			debugLog (fmt.Sprintf ("processReconnect: uLastSentRequestNumber=%v byLastSentMsgKind=%v (%v) m_nRecoverableNetworkState=%v bIsKnownRequest=%v bMultiTSRContinuation=%v",
				uLastSentRequestNumber,
				byLastSentMsgKind,
				MessageKind (byLastSentMsgKind),
				con.m_nRecoverableNetworkState,
				bIsKnownRequest,
				bMultiTSRContinuation))
		}

		if byLastSentMsgKind == MESSAGE_KIND_START && bIsKnownRequest && byRedriveOrNewRequestOK == 1 {

			// We may be in the sent state or the ack state here. Either way, we want to send ElicitStartResp Request.
			// 1. If we are in the sent state and the DBS knows about the request, then that means that a socket
			//    failure occurred during the transmission of the Start Response from the DBS to us.
			//    We must obtain the response by sending the ElicitStartResp Request message.
			// 2. If we are in the ack state and the DBS knows about the request, then that means that a socket
			//    failure occurred when we tried to send the MessageReceived Response and the DBS didn't get it.
			//    The ElicitStartResp Response message will contain an AckRequested parcel, and we will try again.

			if con.hasDebugLog () { debugLog ("processReconnect: condition 1") }

			err = con.sendElicitStartResp (uLastSentRequestNumber)

			abyResponseMsg, _, err = con.receiveMessage (nio, err) // recursive call

			if err != nil { err = causedBy (err, errReconnect) }
			return

		} else if byLastSentMsgKind == MESSAGE_KIND_ELICIT && bIsKnownRequest && byRedriveOrNewRequestOK == 1 {

			// We had already sent at least one Elicit Response message to the DBS when the disconnect occurred.
			// After we reconnect, we send an ElicitStartResp Request to the DBS and the DBS replies with either
			//  1. an ElicitStartResp Response containing PCLSUCCESS, if all LOB data was successfully transferred, or
			//  2. an ElicitStartResp Response containing PCLERROR/PCLFAILURE, if a network disconnect occurred during LOB data transfer, or
			//  3. an Elicit Request, if a DBS restart occurred during LOB data transfer.
			// For situation 1, we return the response message to the caller.
			// For situation 2, the DBS aborts the request itself, and we return the response message to the caller.
			// For situation 3, we cannot rewind the LOB data InputStream or Reader, so we must send a synchronous (UC) Abort Request to the DBS.

			if con.hasDebugLog () { debugLog ("processReconnect: condition 2") }

			err = con.sendElicitStartResp (uLastSentRequestNumber)

			abyResponseMsg, _, err = con.receiveMessage (nio, err) // recursive call

			if err != nil {
				err = causedBy (err, errReconnect)
				return
			}

			bGotElicitRequest := abyResponseMsg [2] == MESSAGE_KIND_ELICIT
			if ! bGotElicitRequest { return } // situations 1 and 2

			err = con.sendUCAbort (uLastSentRequestNumber) // situation 3

			_, _, err = con.receiveMessage (nio, err) // recursive call

			if err != nil {
				err = causedBy (err, errReconnect)
				return
			}

			// ignore result of receiveMessage, and fall through to return error below

		} else if con.m_nRecoverableNetworkState == 0 /*sent*/ && (byLastSentMsgKind == MESSAGE_KIND_START || byLastSentMsgKind == MESSAGE_KIND_CONTINUE) && ! bMultiTSRContinuation {

			// If we most recently sent a Start or Continue request messsage, and a disconnect occurred, then we resend the message in the following scenarios.
			// byLastSentMsgKind  bIsKnownRequest  byRedriveOrNewRequestOK
			//  Start              false            0   (occurs for DBS restart after a CALL sproc finished execution)
			//  Start              false            1
			//  Continue           false            1
			//  Continue           true             1

			if con.hasDebugLog () { debugLog ("processReconnect: condition 3") }

			nioResend := *nio // copy
			nioResend.abyMsgToSend = con.m_abyLastSentMsg [ : ] // copy
			binary.BigEndian.PutUint64 (nioResend.abyMsgToSend [24 : ], con.m_uCryptoNonce.Add (1))

			err = con.sendMessage (&nioResend) // resend last message

			abyResponseMsg, _, err = con.receiveMessage (nio, err) // recursive call

			if err != nil { err = causedBy (err, errReconnect) }
			return

		} else if con.m_nRecoverableNetworkState == 1 /*ack*/ && ! bIsKnownRequest && byRedriveOrNewRequestOK == 1 {

			// If we are in the ack state, and the DBS doesn't know about the request, and it's OK to send a new request,
			// then that means that the DBS got the MessageReceived Response, but a socket failure occurred during
			// the subsequent Continue Request or Continue Response. No action is needed from us in this situation.

			if con.hasDebugLog () { debugLog ("processReconnect: condition 4") }

			return // return value is ignored during ack processing
		}

		errReconnect = con.makeDriverError (errReconnect, 0, fmt.Sprintf ("Automatic recovery after socket communication failure was not possible for %v message", MessageTitle (con.m_abyLastSentMsg, false)))

	} // end if bAttemptRecoverableNP

	err = errReconnect
	return

} // end method processReconnect

func (con *TeradataConnection) processSessionStatusResponseParcel (abyParcelBody [] byte) (byRedriveOrNewRequestOK byte, mapKnownRequests map [uint32] byte) {

	mapKnownRequests = map [uint32] byte {}

	for len (abyParcelBody) - 4 >= 0 {

		uFeatureKind   := binary.BigEndian.Uint16 (abyParcelBody)
		uFeatureLength := binary.BigEndian.Uint16 (abyParcelBody [2 : ])
		abyFeature     := abyParcelBody [4 : uFeatureLength]

		if con.hasDebugLog () {
			debugLog (fmt.Sprintf ("processSessionStatusResponseParcel: self-defining feature %v has length %v", uFeatureKind, uFeatureLength))
		}

		if uFeatureKind == 7 { // PCLSESSTATUSRESPOPENRESPEXT Open Response Extension

			byRedriveOrNewRequestOK   = consumeByte (&abyFeature)
			uEntryListBytesRemaining := consumeShort (&abyFeature)

			for ; uEntryListBytesRemaining >= 5 ; uEntryListBytesRemaining -= 5 {

				uRequestNumber := consumeInt (&abyFeature)
				byRequestState := consumeByte (&abyFeature)
				mapKnownRequests [uRequestNumber] = byRequestState
			}
		} // end if

		abyParcelBody = abyParcelBody [uFeatureLength : ]

	} // end for

	return

} // end method processSessionStatusResponseParcel

func (con *TeradataConnection) processControlData (nio *NetworkIO, abyInput [] byte) (abyOutput [] byte, err error) {

	nControlDataLength := int (binary.BigEndian.Uint32 (abyInput [40 : ]))
	iControlDataStart  := len (abyInput) - nControlDataLength
	abyControlData     := abyInput [iControlDataStart : ]
	abyOutput           = abyInput [ : iControlDataStart]

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter processControlData %v len(abyInput)=%v len(abyControlData)=%v len(abyOutput)=%v", con, len (abyInput), len (abyControlData), len (abyOutput)))
		defer func () { traceLog (fmt.Sprintf ("< leave processControlData %v len(abyInput)=%v len(abyControlData)=%v len(abyOutput)=%v err=%v", con, len (abyInput), len (abyControlData), len (abyOutput), err)) } ()
	}

	if nControlDataLength == 0 { return }

	uRequestNumber := binary.BigEndian.Uint32 (abyInput [32 : ])

	aabyParcelHeaders := getParcelHeadersFromCurrent (abyControlData)
	for i, abyParcelHeader := range aabyParcelHeaders {

		uFlavor       := cookedParcelFlavor    (abyParcelHeader)
		abyParcelBody := constrainedParcelBody (abyParcelHeader)

		if con.hasDebugLog () {
			debugLog (fmt.Sprintf ("processControlData: parcel %v of %v %v %v", (i + 1), len (aabyParcelHeaders), parcelFlavor (uFlavor), uFlavor))
		}

		switch uFlavor {

			case PCLCONTROLDATASTART:

				nControlDataStartValue := int (binary.BigEndian.Uint32 (abyParcelBody [0 : ]))
				if con.hasDebugLog () && nControlDataStartValue != nControlDataLength {
					debugLog (fmt.Sprintf ("processControlData: mismatch nControlDataStartValue=%v nControlDataLength=%v", nControlDataStartValue, nControlDataLength))
				}

			case PCLCONTROLDATAEND:

				nControlDataEndValue := int (binary.BigEndian.Uint32 (abyParcelBody [0 : ]))
				if con.hasDebugLog () && nControlDataEndValue != nControlDataLength {
					debugLog (fmt.Sprintf ("processControlData: mismatch nControlDataEndValue=%v nControlDataLength=%v", nControlDataEndValue, nControlDataLength))
				}

			case PCLCONTINUECONTEXT:

				if nio.rows != nil {
					nio.rows.m_abyContinueContextBody = abyParcelBody
				}

			case PCLREDRIVE:

				con.m_bSessionShouldUseRedrive = true

			case PCLREDRIVESUPPORTED:

				con.m_bSessionSupportsRedrive = true

			case PCLRECOVERABLENPSUPPORTED:

				con.m_bSessionSupportsRNP = true

			case PCLACKREQUESTED:

				con.m_nRecoverableNetworkState = 1 // ack
				err = con.sendAck (uRequestNumber)
				if err != nil { return }

			case PCLSECURITYPOLICY:

				con.processSecurityPolicyParcel (abyParcelHeader)
		}
	} // end for

	return

} // end method processControlData

func (con *TeradataConnection) sendAck (uRequestNumber uint32) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter sendAck %v uRequestNumber=%v", con, uRequestNumber))
		defer func () { traceLog (fmt.Sprintf ("< leave sendAck %v uRequestNumber=%v err=%v", con, uRequestNumber, err)) } ()
	}

	err = con.sendMessage (&NetworkIO { abyMsgToSend: con.makeResponseMessage (MESSAGE_KIND_MESSAGERECEIVED, NEW_CRYPTO_NONCE, uRequestNumber) })

	if err == nil {
		err = con.sendMessage (&NetworkIO { abyMsgToSend: con.makeRequestMessage (MESSAGE_KIND_CONTINUE, NEW_CRYPTO_NONCE, uRequestNumber, makeParcel (PCLCANCEL)) })
	}

	// We expect to get the following message in reply:
	//  Continue Response with error 3119 Continue request submitted but no response to return

	_, _, err = con.receiveMessage (&NetworkIO {}, err) // recursive call
	return

} // end method sendAck

func (con *TeradataConnection) sendElicitStartResp (uLastSentRequestNumber uint32) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter sendElicitStartResp %v uLastSentRequestNumber=%v", con, uLastSentRequestNumber))
		defer func () { traceLog (fmt.Sprintf ("< leave sendElicitStartResp %v uLastSentRequestNumber=%v err=%v", con, uLastSentRequestNumber, err)) } ()
	}

	aabyControlDataParcels := [][] byte {}

	abyStart := makeParcel (PCLCONTROLDATASTART, fourBytes (0)) // set actual length below
	aabyControlDataParcels = append (aabyControlDataParcels, abyStart)

	aabyControlDataParcels = append (aabyControlDataParcels, makeParcel (PCLRECOVERABLEPROTOCOL))

	abyEnd := makeParcel (PCLCONTROLDATAEND, fourBytes (0)) // set actual length below
	aabyControlDataParcels = append (aabyControlDataParcels, abyEnd)

	uControlDataLength := uint32 (combinedLength (aabyControlDataParcels))
	binary.BigEndian.PutUint32 (constrainedParcelBody (abyStart), uControlDataLength)
	binary.BigEndian.PutUint32 (constrainedParcelBody (abyEnd  ), uControlDataLength)

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("sendElicitStartResp: uControlDataLength=%v", uControlDataLength))
	}

	abyRequestMsg := con.makeRequestMessage (MESSAGE_KIND_ELICITSTARTRESP, NEW_CRYPTO_NONCE, uLastSentRequestNumber, aabyControlDataParcels ...)
	binary.BigEndian.PutUint32 (abyRequestMsg [40 : ], uControlDataLength)

	err = con.sendMessage (&NetworkIO { abyMsgToSend: abyRequestMsg })
	return

} // end method sendElicitStartResp

func (con *TeradataConnection) sendUCAbort (uRequestNumber uint32) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter sendUCAbort %v uRequestNumber=%v", con, uRequestNumber))
		defer func () { traceLog (fmt.Sprintf ("< leave sendUCAbort %v uRequestNumber=%v err=%v", con, uRequestNumber, err)) } ()
	}

	err = con.sendMessage (&NetworkIO { abyMsgToSend: con.makeRequestMessage (MESSAGE_KIND_ABORT, NEW_CRYPTO_NONCE, uRequestNumber, makeParcel (PCLUCABORT)) })
	return

} // end method sendUCAbort
