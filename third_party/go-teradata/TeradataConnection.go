// Copyright 2017-2022 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"github.com/Teradata-TIO/go-teradata/goteragss"
)

const NEW_CRYPTO_NONCE   uint64 = math.MaxUint64
const NEW_REQUEST_NUMBER uint32 = math.MaxUint32

var _ driver.Conn                 = &TeradataConnection {} // implements driver.Conn
var _ driver.ConnBeginTx          = &TeradataConnection {} // implements driver.ConnBeginTx
var _ driver.ConnPrepareContext   = &TeradataConnection {} // implements driver.ConnPrepareContext
var _ driver.Execer               = &TeradataConnection {} // implements driver.Execer
var _ driver.ExecerContext        = &TeradataConnection {} // implements driver.ExecerContext
var _ driver.NamedValueChecker    = &TeradataConnection {} // implements driver.NamedValueChecker
var _ driver.Pinger               = &TeradataConnection {} // implements driver.Pinger
var _ driver.Queryer              = &TeradataConnection {} // implements driver.Queryer
var _ driver.QueryerContext       = &TeradataConnection {} // implements driver.QueryerContext
var _ fmt.Stringer                = &TeradataConnection {} // implements fmt.Stringer

var g_uConnectionCount atomic.Uint64
var g_mutexGssInit     sync.Mutex

var g_mutexConnHandles sync.RWMutex // protects g_mapConnHandles
var g_mapConnHandles = make (map [uint64] *TeradataConnection)

type TeradataConnection struct {

	m_sConnectionID               string
	m_params                      *ConParams
	m_sLogMechForTDGSS            string
	m_sLogDataForTDGSS            string
	m_auPorts                     [] uint32
	m_bAutoCommit                 bool
	m_config                      *config
	m_gtwConfig                   *gtwConfig
	m_socket                      net.Conn
	m_nFrameBytesToRead           int
	m_sProxyUsed                  string
	m_sCertStatus                 string
	m_sConnectedHostName          string
	m_extra                       clientAttributesExtra
	m_pSecurity                   goteragss.Terasso
	m_uCryptoNonce                atomic.Uint64
	m_uSessionNumber              uint32
	m_uHostID                     uint16
	m_uCurrentRequestNumber       atomic.Uint32
	m_uLogonSequenceNumber        uint32
	m_bLoggedOn                   bool
	m_bInTransaction              bool
	m_bSessionSupportsRNP         bool
	m_bSessionSupportsRedrive     bool
	m_bSessionShouldUseRedrive    bool
	m_bRNPActive                  bool
	m_bRedriveActive              bool
	m_bReconnectInProgress        bool
	m_bSuppressReconnect          bool
	m_abyLastSentMsg              [] byte
	m_bReadInProgress             bool
	m_nRecoverableNetworkState    int // zero=sent, 1=ack, 2=complete
	m_bPromptMode                 bool
	m_mutexDeferredLobs           sync.Mutex // protects m_nDeferredLobToken and m_mapDeferredLobs
	m_nDeferredLobToken           int32
	m_mapDeferredLobs             map [int32] [] byte
	m_nConnectAttempts            int
	m_nConnectFailures            int
	m_nReconnectAttempts          int
	m_nHTTPSRetries               int
	m_durConnectTime              time.Duration
	m_durConnectTotalTime         time.Duration
	m_mapRequestToErrors          map [/*sNativeSQL*/ string] /*asErrors*/ [] string
	m_mapRequestToManager         map [/*sNativeSQL*/ string] /*Manager*/ any
	m_fastloadBindMetadata        [] bindMetadata // set for each fldManager.m_fastloadCon[] and thus safe for multiple simultaneous fastloads
	m_nFastLoadCurrentRow         int
	m_nFastExportReqMsgLen        int // set for each fxpManager.m_fastexportCon[] and thus safe for multiple simultaneous fastexports
	m_uSecurityRequired           byte
	m_uConfidentialityRequired    byte
	m_uSecurityLevel              uint32 // a.k.a. Quality of Protection
	m_nDHKE                       int // number of round trips for non-TLS DHKE
	m_bNoTransmitCredentials      bool
	m_setTokenFinders             Set [string]

} // end type TeradataConnection

// Note: This pointer-receiver method will not work with a value inside an interface, which we do not expect or intend.

func (con *TeradataConnection) String () string { // satisfies fmt.Stringer

	return fmt.Sprintf ("%v con=%p host=%v sess=%v", G_sVersionNumber, con, con.m_params.M_sHostName, con.m_uSessionNumber)

} // end method String

func newTeradataConnection (params *ConParams) (conOutput *TeradataConnection, err error) {

	con := &TeradataConnection {
		m_sConnectionID          : strconv.FormatUint (g_uConnectionCount.Add (1), 16), // hex
		m_params                 : params,
		m_bAutoCommit            : true,
		m_config                 : &config {},
		m_gtwConfig              : &gtwConfig {},
		m_mapDeferredLobs        : map [int32] [] byte {},
		m_mapRequestToErrors     : map [/*sNativeSQL*/ string] /*asErrors*/ [] string {},
		m_mapRequestToManager    : map [/*sNativeSQL*/ string] /*Manager*/ any {},
		m_uSecurityLevel         : goteragss.TDGSS_C_QOP_DEFAULT,
		m_nFrameBytesToRead      : -1,
		m_setTokenFinders        : &SynchronizedSet [string] { Target: &ListSet [string] {} },
	}

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter newTeradataConnection %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave newTeradataConnection %v err=%v", con, err)) } ()
	}

	defer func () { // catch and wrap any panic
		if ex := recover () ; ex != nil {
			err = con.makeDriverError (err, 0, formatRecover ("Cannot open database connection", ex)) // preserve existing err (if any) as cause
		}
	} ()

	con.m_params.M_sPassword, err = con.DecryptPass (con.m_params.M_sPassword, /*bMatchEntireString*/ true)
	if err != nil { return }

	con.m_params.M_sLogData, err = con.DecryptPass (con.m_params.M_sLogData, /*bMatchEntireString*/ false)
	if err != nil { return }

	con.m_params.M_sHTTPProxyPassword, err = con.DecryptPass (con.m_params.M_sHTTPProxyPassword, /*bMatchEntireString*/ true)
	if err != nil { return }

	con.m_params.M_sHTTPSProxyPassword, err = con.DecryptPass (con.m_params.M_sHTTPSProxyPassword, /*bMatchEntireString*/ true)
	if err != nil { return }

	con.m_pSecurity = goteragss.NewTerasso (con) // TeradataConnection implements goteragss.TdgssLogger

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("TeraGSS version %v %v %v %v", goteragss.GoTeraGSSMajorVersion, goteragss.GoTeraGSSMinorVersion, goteragss.GoTeraGSSMaintVersion, goteragss.GoTeraGSSEfixVersion))
		debugLog (fmt.Sprintf ("con.m_params=%+v", con.m_params))
	}

	ctxLogin := context.Background ()
	if con.m_params.M_uLogonTimeout > 0 {
		var cancelFunc context.CancelFunc
		ctxLogin, cancelFunc = context.WithTimeout (ctxLogin, time.Duration (con.m_params.M_uLogonTimeout) * time.Second)
		defer cancelFunc () // release the derived context if we finish before the timeout expires, leaving the parent context intact
	}

	err = con.connectToHost (ctxLogin, /*reconnect*/ nil)
	if err != nil { return }

	defer func () {
		if err != nil {
			con.closeSocket (GRACEFUL, IGNORE_ERROR)
		}
	} ()

	err = con.logonController (/*socket*/ nil, ctxLogin, /*reconnect*/ nil)
	if err != nil { return }

	conOutput = con

	if con.m_params.M_uConnHandle != 0 {
		g_mutexConnHandles.Lock () // write lock
		g_mapConnHandles [con.m_params.M_uConnHandle] = con
		g_mutexConnHandles.Unlock ()
	}

	if ! con.m_bLoggedOn { return } // logmech NONE

	if con.m_params.M_bRunStartup {

		err = con.executeSessionCommand (ctxLogin, "{fn teradata_send_run_startup_parcel}")
		if err != nil { return }
	}

	if con.m_params.M_sDatabase != "" {

		sRequestText := "DATABASE " + doubleQuote (con.m_params.M_sDatabase)
		if con.isAnsiMode () { sRequestText += " ; COMMIT" }

		err = con.executeSessionCommand (ctxLogin, sRequestText)
		if err != nil { return }
	}

	return

} // end func newTeradataConnection

func TeradataConnectionFromHandle (uLog uint64, uConnHandle uint64) (con *TeradataConnection) {

	bTraceLog := (uLog & 1) != 0

	if bTraceLog {
		traceLog (fmt.Sprintf ("> enter TeradataConnectionFromHandle uConnHandle=%v", uConnHandle))
		defer func () { traceLog (fmt.Sprintf ("< leave TeradataConnectionFromHandle uConnHandle=%v", uConnHandle)) } ()
	}

	g_mutexConnHandles.RLock () // read lock
	con = g_mapConnHandles [uConnHandle]
	g_mutexConnHandles.RUnlock ()
	return

} // end func TeradataConnectionFromHandle

func (con *TeradataConnection) GetConParams () *ConParams {
	return con.m_params
}

func (con *TeradataConnection) GetConfig () *config {
	return con.m_config
}

func (con *TeradataConnection) isAnsiMode () bool {

	return con.m_params.M_sTMode == "ANSI" ||
		con.m_params.M_sTMode == "DEFAULT" && con.m_config.M_sDefaultTransactionSemantics == "A" /*ANSI*/

} // end method isAnsiMode

func (con *TeradataConnection) registerDeferredLob (abyDeferredLob [] byte) (nDeferredLobToken int32) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter registerDeferredLob %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave registerDeferredLob %v token=%v", con, nDeferredLobToken)) } ()
	}

	con.m_mutexDeferredLobs.Lock ()

	con.m_nDeferredLobToken++
	nDeferredLobToken = con.m_nDeferredLobToken
	con.m_mapDeferredLobs [nDeferredLobToken] = abyDeferredLob

	con.m_mutexDeferredLobs.Unlock ()

	return

} // end method registerDeferredLob

func (con *TeradataConnection) unRegisterDeferredLob (nDeferredLobToken int32) (abyDeferredLob [] byte) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter unRegisterDeferredLob %v token=%v", con, nDeferredLobToken))
		defer func () { traceLog (fmt.Sprintf ("< leave unRegisterDeferredLob %v token=%v", con, nDeferredLobToken)) } ()
	}

	con.m_mutexDeferredLobs.Lock ()

	abyDeferredLob = con.m_mapDeferredLobs [nDeferredLobToken]
	delete (con.m_mapDeferredLobs, nDeferredLobToken)

	con.m_mutexDeferredLobs.Unlock ()

	return

} // end method unRegisterDeferredLob

func (con *TeradataConnection) makeRequestMessage (uMessageKind byte, uCryptoNonce uint64, uRequestNumber uint32, aabyParcels ... [] byte) (abyOutput [] byte) {

	return con.makeMessage (MESSAGE_CLASS_REQUEST, uMessageKind, uCryptoNonce, uRequestNumber, aabyParcels ...)

} // end method makeRequestMessage

func (con *TeradataConnection) makeResponseMessage (uMessageKind byte, uCryptoNonce uint64, uRequestNumber uint32, aabyParcels ... [] byte) (abyOutput [] byte) {

	return con.makeMessage (MESSAGE_CLASS_RESPONSE, uMessageKind, uCryptoNonce, uRequestNumber, aabyParcels ...)

} // end method makeResponseMessage

func (con *TeradataConnection) makeMessage (uMessageClass byte, uMessageKind byte, uCryptoNonce uint64, uRequestNumber uint32, aabyParcels ... [] byte) (abyOutput [] byte) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter makeMessage %v %v %v", con, MessageKind (uMessageKind), MessageClass (uMessageClass)))
		defer func () { traceLog (fmt.Sprintf ("< leave makeMessage %v req=%v %v %v len=%v", con, uRequestNumber, MessageKind (uMessageKind), MessageClass (uMessageClass), len (abyOutput))) } ()
	}

	if uCryptoNonce == NEW_CRYPTO_NONCE {
		uCryptoNonce = con.m_uCryptoNonce.Add (1)
	}

	if uRequestNumber == NEW_REQUEST_NUMBER {
		uRequestNumber = con.m_uCurrentRequestNumber.Add (1)
	}

	abyOutput = makeMessage (uMessageClass, uMessageKind, con.m_uSessionNumber, uCryptoNonce, uRequestNumber, CHARSET_CODE_UTF8, aabyParcels ...)
	return

} // end method makeMessage

func (con *TeradataConnection) serializeChar (s string) [] byte {

	return [] byte (s) // todo session charset encoding

} // end method serializeChar

func (con *TeradataConnection) serializeVarchar (s string) [] byte {

	aby := con.serializeChar (s)
	return append (twoBytes (uint16 (len (aby))), aby ...)

} // end method serializeVarchar

func (con *TeradataConnection) fixedWidthInSessionCharset (abyInput [] byte, uWidthInBytes uint64) [] byte {

	s := string (abyInput) // todo session charset decoding
	return fixedWidthBytes (s, uWidthInBytes, ' ') // todo session charset encoding

} // end method fixedWidthInSessionCharset

func (con *TeradataConnection) deserializeChar (uByteCount uint64, aby [] byte) [] byte {

	// This method converts from session character set encoded bytes to UTF8-encoded bytes.
	// Therefore, the UTF8 session character set is the most efficient, because no conversion is needed.

	return aby [ : uByteCount] // todo session charset decoding

} // end method deserializeChar

func (con *TeradataConnection) deserializeVarchar (aby [] byte) [] byte {

	return con.deserializeChar (uint64 (binary.BigEndian.Uint16 (aby)), aby [2 : ])

} // end method deserializeVarchar

func (con *TeradataConnection) consumeVarchar (paby * [] byte) (abyOutput [] byte) {

	abyOutput = con.deserializeVarchar (*paby)
	skipVarField (paby)
	return

} // end method consumeVarchar

func (con *TeradataConnection) retrieveErrorParcelDetails (abyParcelHeader [] byte) (uErrorCode uint16, sErrorMsg string, uStatementNumber uint16) {

	abyParcelBody   := constrainedParcelBody (abyParcelHeader)
	uStatementNumber = binary.BigEndian.Uint16 (abyParcelBody [0 : ])
	uErrorCode       = binary.BigEndian.Uint16 (abyParcelBody [4 : ])
	sErrorMsg        = string (con.deserializeVarchar (abyParcelBody [6 : ]))

	return

} // end retrieveErrorParcelDetails

func (con *TeradataConnection) processErrorParcel (errCause error, abyParcelHeader [] byte) (uWarningCode uint16, sRawWarningMsg string, sWarningMsg string, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter processErrorParcel %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave processErrorParcel %v", con)) } ()
	}

	abyParcelBody := constrainedParcelBody (abyParcelHeader)
	uErrorCode    := binary.BigEndian.Uint16 (abyParcelBody [4 : ])

	if errorIsReallyWarning (uErrorCode) {

		uWarningCode   = uErrorCode
		sRawWarningMsg = string (con.deserializeVarchar (abyParcelBody [6 : ]))

		if len (sRawWarningMsg) > 0 {
			sWarningMsg = con.formatDatabaseWarning (uWarningCode, sRawWarningMsg)
		}

		if con.hasDebugLog () { debugLog (fmt.Sprintf ("processErrorParcel %v errorIsReallyWarning %v %v", con, uWarningCode, sRawWarningMsg)) }

	} else {

		err = con.makeChainedDatabaseError (errCause, abyParcelHeader)
	}

	return

} // end method processErrorParcel

func (con *TeradataConnection) processSuccessParcel (abyParcelHeader [] byte) (uStatementNumber uint32, uActivityType uint16, uActivityCount uint64,
	uWarningCode uint16, sRawWarningMsg string, sWarningMsg string) {

	uParcelFlavor    := cookedParcelFlavor (abyParcelHeader)
	abyParcelBody    := constrainedParcelBody (abyParcelHeader)

	uStatementNumber  = uint32 (consumeShort (&abyParcelBody))
	uActivityCount    = uint64 (consumeInt (&abyParcelBody))
	uWarningCode      = consumeShort (&abyParcelBody)
	uFieldCount      := consumeShort (&abyParcelBody)
	uActivityType     = consumeShort (&abyParcelBody)
	sRawWarningMsg    = string (con.consumeVarchar (&abyParcelBody))

	if len (sRawWarningMsg) > 0 {
		sWarningMsg   = con.formatDatabaseWarning (uWarningCode, sRawWarningMsg)
	}

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("processSuccessParcel: %v %v uStatementNumber=%v uActivityCount=%v uWarningCode=%v uFieldCount=%v uActivityType=%v sWarningMsg=%v", con, parcelFlavor (uParcelFlavor), uStatementNumber, uActivityCount, uWarningCode, uFieldCount, uActivityType, sWarningMsg))
	}

	return

} // end method processSuccessParcel

func (con *TeradataConnection) processStatementStatusParcel (abyParcelHeader [] byte) (uStatementNumber uint32, uActivityType uint16, uActivityCount uint64,
	uLastWarningCode uint16, sRawWarningMsg string, sWarningMsg string, err error) {

	uParcelFlavor     := cookedParcelFlavor (abyParcelHeader)
	abyParcelBody     := constrainedParcelBody (abyParcelHeader)

	byStatementStatus := consumeByte  (&abyParcelBody)
	byResponseMode    := consumeByte  (&abyParcelBody)

	skipShort (&abyParcelBody) // reserved

	uStatementNumber   = consumeInt   (&abyParcelBody)
	uErrorCode        := consumeShort (&abyParcelBody) // Will be 0 unless this parcel is being used for Error & Failure

	uActivityType      = consumeShort (&abyParcelBody)
	uActivityCount     = consumeLong  (&abyParcelBody)
	uFieldCount       := consumeInt   (&abyParcelBody)

	skipInt (&abyParcelBody) // Reserved (4 bytes)

	if byStatementStatus != 0 {

		err = chainErrors (err, con.makeDriverError (nil, 549,
			fmt.Sprintf ("The Teradata Database reported non-successful status code %v for statement %v",
				byStatementStatus,
				uStatementNumber)))

		return
	}

	for len (abyParcelBody) - 6 >= 0 {

		uExtensionID     := consumeShort (&abyParcelBody)
		uExtensionLength := consumeInt   (&abyParcelBody)
		abyNextExtension := abyParcelBody [uExtensionLength : ]

		switch (uExtensionID) {

			case 1: // Extension Warning

				uLastWarningCode = consumeShort (&abyParcelBody)
				uComponentID    := consumeShort (&abyParcelBody)
				uWarningLength  := consumeInt   (&abyParcelBody)

				// The DBS can return more than one warning extension. However, if warning is a compile error (UDF/XSP/UDM/UDT/JAR),
				// then only one warning would be returned, so that would be handled in TeradataRows.
				if uWarningLength > 0 {

					sRawWarningMsg = string (consumeBytes (&abyParcelBody, uWarningLength))

					if len (sWarningMsg) > 0 {
						sWarningMsg += "\n"
					}

					sWarningMsg += con.formatDatabaseWarning (uLastWarningCode, sRawWarningMsg)

					if con.hasDebugLog () {
						debugLog (fmt.Sprintf ("processStatementStatusParcel: warning code=%v ComponentID=%v warning msg=%v", uLastWarningCode, uComponentID, sRawWarningMsg))
					}
				}
		} // end switch

		abyParcelBody = abyNextExtension

	} // end for

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("processStatementStatusParcel: %v %v uStatementNumber=%v uActivityCount=%v uLastWarningCode=%v uFieldCount=%v uActivityType=%v sWarningMsg=%v, byResponseMode=%v, uErrorCode=%v", con, parcelFlavor (uParcelFlavor), uStatementNumber, uActivityCount, uLastWarningCode, uFieldCount, uActivityType, sWarningMsg, byResponseMode, uErrorCode))
	}

	return

} // end method processStatementStatusParcel

func (con *TeradataConnection) processMessageSuccessOrError (abyMessage [] byte, bRequired bool) (err error) {

	sTitle := MessageTitle (abyMessage, false)

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter processMessageSuccessOrError %v %v", con, sTitle))
		defer func () { traceLog (fmt.Sprintf ("< leave processMessageSuccessOrError %v %v", con, sTitle)) } ()
	}

	abyParcelHeader := abyMessage [52 : ]
	uParcelFlavor   := cookedParcelFlavor (abyParcelHeader)

	switch uParcelFlavor {

		case PCLSUCCESS:

			con.processSuccessParcel (abyParcelHeader)

		case PCLSTMTSTATUS:

			con.processStatementStatusParcel (abyParcelHeader)

		case PCLFAILURE, PCLERROR:

			err = con.makeDatabaseError (abyParcelHeader)

		default:

			if bRequired {
				err = con.makeDriverError (nil, 0,
					fmt.Sprintf ("%v message contains unexpected parcel %v %v",
						sTitle,
						parcelFlavor (uParcelFlavor),
						uParcelFlavor))
			}
	} // end switch uParcelFlavor

	return

} // end method processMessageSuccessOrError

func (con *TeradataConnection) processLSNParcel (abyParcelHeader [] byte) {

	uParcelFlavor := cookedParcelFlavor (abyParcelHeader)
	abyParcelBody := constrainedParcelBody (abyParcelHeader)

	con.m_uLogonSequenceNumber = consumeInt (&abyParcelBody)

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("processLSNParcel: %v %v m_uLogonSequenceNumber=%v", con, parcelFlavor (uParcelFlavor), con.m_uLogonSequenceNumber))
	}
} // end method processLSNParcel

func (con *TeradataConnection) processSecurityPolicyParcel (abyParcelHeader [] byte) {

	uParcelFlavor := cookedParcelFlavor (abyParcelHeader)
	abyParcelBody := constrainedParcelBody (abyParcelHeader)

	con.m_uSecurityRequired        = consumeByte (&abyParcelBody)
	con.m_uConfidentialityRequired = consumeByte (&abyParcelBody)
	skipShort (&abyParcelBody) // pad
	con.m_uSecurityLevel           = consumeInt (&abyParcelBody) // a.k.a. Quality of Protection

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("processSecurityPolicyParcel: %v %v con.m_uSecurityRequired=%v con.m_uConfidentialityRequired=%v con.m_uSecurityLevel=%v",
			con, parcelFlavor (uParcelFlavor), con.m_uSecurityRequired, con.m_uConfidentialityRequired, con.m_uSecurityLevel))
	}
} // end method processSecurityPolicyParcel

func (con *TeradataConnection) Prepare (sRequestText string) (stmt driver.Stmt, err error) { // satisfies driver.Conn

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Conn.Prepare %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave Conn.Prepare %v", con)) } ()
	}

	if con.m_socket == nil {
		err = con.makeBadConnectionError (nil, 570, "Connection is closed")
		return
	}

	stmt, err = newTeradataStatement (con, nil, sRequestText)
	return

} // end method Prepare

func (con *TeradataConnection) freeGssMemory (perrPreserve *error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter freeGssMemory %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave freeGssMemory %v", con)) } ()
	}

	status := con.m_pSecurity.GssTerm ()

	if status.Major == goteragss.GSS_S_COMPLETE {

		if con.hasDebugLog () { debugLog ("GssTerm SUCCESS") }

	} else {

		err := status.Err

		if *perrPreserve == nil { *perrPreserve = err }
		if con.hasDebugLog () { debugLog (fmt.Sprintf ("Failure freeing GSS memory: %v", err)) }
	}
} // end method freeGssMemory

func (con *TeradataConnection) Close () (err error) { // satisfies driver.Conn

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Conn.Close %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave Conn.Close %v err=%v", con, err)) } ()
	}

	defer func () { // catch and wrap any panic
		if ex := recover () ; ex != nil {
			err = con.makeDriverError (err, 0, formatRecover ("Cannot close database connection", ex)) // preserve existing err (if any) as cause
		}
	} ()

	if con.m_socket == nil {
		if con.hasDebugLog () { debugLog (fmt.Sprintf ("Conn.Close: Socket is already closed %v", con)) }
		return
	}

	defer con.closeSocket (GRACEFUL, &err)

	if ! con.m_bLoggedOn {
		if con.hasDebugLog () { debugLog (fmt.Sprintf ("Conn.Close: Session is already logged off %v", con)) }
		return
	}

	con.m_bLoggedOn = false

	defer con.freeGssMemory (&err)

	var fldfxpErrors error
	defer func () {
		err = chainErrors (fldfxpErrors, err)
	} ()

	for k, manager := range con.m_mapRequestToManager {
		if con.hasDebugLog () {
			debugLog (fmt.Sprintf ("found in con.m_mapRequestToManager key=%v", k))
		}

		ctx := context.Background ()
		if con.m_params.M_uRequestTimeout > 0 {
			var cancelFunc context.CancelFunc
			ctx, cancelFunc = context.WithTimeout (ctx, time.Duration (con.m_params.M_uRequestTimeout) * time.Second)
			defer cancelFunc () // release the derived context if we finish before the timeout expires, leaving the parent context intact
		}

		switch typedval := manager.(type) {

			case fastLoadManager:

				fldManager := typedval.Value()
				if fldManager.fastloading () {
					fldfxpErrors = chainErrors(fldfxpErrors, fldManager.rollbackFastLoad(ctx))
				}

				fldfxpErrors = chainErrors(fldfxpErrors, fldManager.closeFastLoad())

			case fastExportManager:

				fxpManager := typedval.Value()
				fldfxpErrors = chainErrors(fldfxpErrors, fxpManager.closeFastExport(ctx))
		}

		if con.hasDebugLog () {
			debugLog (fmt.Sprintf ("delete from con.m_mapRequestToManager key=%v", k))
		}

		delete (con.m_mapRequestToManager, k)

	} // end for k

	// We ignore the possible race condition in which a different thread deletes the map entry,
	// because the built-in delete function is a no-op when the map does not contain the key.

	if con.m_params.M_uConnHandle != 0 {
		g_mutexConnHandles.Lock () // write lock
		delete (g_mapConnHandles, con.m_params.M_uConnHandle)
		g_mutexConnHandles.Unlock ()
	}

	con.m_bSuppressReconnect = true // do not reconnect during logoff

	abyLogoffRequestMsg := con.makeRequestMessage (MESSAGE_KIND_LOGOFF, NEW_CRYPTO_NONCE, NEW_REQUEST_NUMBER, makeParcel (PCLLOGOFF))

	abyLogoffResponseMsg, _, err := con.sendAndReceive (&NetworkIO { abyMsgToSend: abyLogoffRequestMsg })
	if err != nil { return }

	err = con.processMessageSuccessOrError (abyLogoffResponseMsg, /*bRequired*/ true)
	return

} // end method Close

func (con *TeradataConnection) Begin () (driver.Tx, error) { // satisfies driver.Conn

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Conn.Begin %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave Conn.Begin %v", con)) } ()
	}

	return nil, con.makeDriverError (nil, 0, "Deprecated method driver.Conn.Begin")

} // end method Begin

func (con *TeradataConnection) BeginTx (ctx context.Context, opts driver.TxOptions) (tx driver.Tx, err error) { // satisfies driver.ConnBeginTx

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter ConnBeginTx.BeginTx %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave ConnBeginTx.BeginTx %v", con)) } ()
	}

	if con.m_socket == nil {
		err = con.makeBadConnectionError (nil, 570, "Connection is closed")
		return
	}

	if opts.ReadOnly {
		err = con.makeDriverError (nil, 0, "The Teradata Database does not offer read-only transactions")
		return
	}

	if con.m_bInTransaction {
		err = con.makeDriverError (nil, 0, "Transaction already in progress")
		return
	}

	var sCommand string

	switch sql.IsolationLevel (opts.Isolation) { // cast driver.IsolationLevel to sql.IsolationLevel

		case sql.LevelReadUncommitted:
			sCommand = "set session characteristics as transaction isolation level ru"

		case sql.LevelReadCommitted:
			err = con.makeDriverError (nil, 0, "The Teradata Database does not offer read-committed transaction isolation")
			return

		case sql.LevelWriteCommitted:
			err = con.makeDriverError (nil, 0, "The Teradata Database does not offer write-committed transaction isolation")
			return

		case sql.LevelRepeatableRead:
			err = con.makeDriverError (nil, 0, "The Teradata Database does not offer repeatable-read transaction isolation")
			return

		case sql.LevelSnapshot:
			err = con.makeDriverError (nil, 0, "The Teradata Database does not offer snapshot transaction isolation")
			return

		case sql.LevelDefault,
			sql.LevelSerializable:
			sCommand = "set session characteristics as transaction isolation level sr"

		case sql.LevelLinearizable:
			err = con.makeDriverError (nil, 0, "The Teradata Database does not offer linearizable transaction isolation")
			return

		default:
			err = con.makeDriverError (nil, 0, fmt.Sprintf ("Invalid isolation level %d", opts.Isolation))
			return

	} // end switch

	err = con.executeSessionCommand (ctx, sCommand)
	if err != nil { return }

	tx, err = newTeradataTransaction (con)
	return

} // end method BeginTx

func (con *TeradataConnection) PrepareContext (ctx context.Context, sRequestText string) (stmt driver.Stmt, err error) { // satisfies driver.ConnPrepareContext

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter ConnPrepareContext.PrepareContext %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave ConnPrepareContext.PrepareContext %v", con)) } ()
	}

	if con.m_socket == nil {
		err = con.makeBadConnectionError (nil, 570, "Connection is closed")
		return
	}

	stmt, err = newTeradataStatement (con, ctx, sRequestText)
	return

} // end method PrepareContext

func (con *TeradataConnection) Exec (sRequestText string, aBindValues [] driver.Value) (driver.Result, error) { // satisfies driver.Execer

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Execer.Exec %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave Execer.Exec %v", con)) } ()
	}

	return nil, con.makeDriverError (nil, 0, "Deprecated method driver.Execer.Exec")

} // end method Exec

func (con *TeradataConnection) ExecContext (ctx context.Context, sRequestText string, aBindValues [] driver.NamedValue) (res driver.Result, err error) { // satisfies driver.ExecerContext

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter ExecerContext.ExecContext %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave ExecerContext.ExecContext %v", con)) } ()
	}

	if con.m_socket == nil {
		err = con.makeBadConnectionError (nil, 570, "Connection is closed")
		return
	}

	stmt, err := newTeradataStatement (con, ctx, sRequestText)
	if err != nil { return }

	res, err = stmt.ExecContext (ctx, aBindValues)
	return

} // end method ExecContext

func (con *TeradataConnection) CheckNamedValue (pBindValue *driver.NamedValue) (err error) { // satisfies driver.NamedValueChecker

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter NamedValueChecker.CheckNamedValue %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave NamedValueChecker.CheckNamedValue %v", con)) } ()
	}

	if con.m_socket == nil {
		err = con.makeBadConnectionError (nil, 570, "Connection is closed")
		return
	}

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("CheckNamedValue: Ordinal=%v TypeOf=%v", pBindValue.Ordinal, reflect.TypeOf (pBindValue.Value)))
	}

	// Return nil error to ensure that all bound data values are accepted.
	// Bind values will be validated later by the TeradataRows.validateBindValues method.

	return

} // end method CheckNamedValue

func (con *TeradataConnection) Ping (ctx context.Context) (err error) { // satisfies driver.Pinger

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Pinger.Ping %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave Pinger.Ping %v", con)) } ()
	}

	if con.m_socket == nil {
		err = con.makeBadConnectionError (nil, 570, "Connection is closed")
		return
	}

	err = con.executeSessionCommand (ctx, "{fn teradata_bypass_tx_commands}--ping")
	return

} // end method Ping

func (con *TeradataConnection) Query (sRequestText string, aBindValues [] driver.Value) (driver.Rows, error) { // satisfies driver.Queryer

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Queryer.Query %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave Queryer.Query %v", con)) } ()
	}

	return nil, con.makeDriverError (nil, 0, "Deprecated method driver.Queryer.Query")

} // end method Query

func (con *TeradataConnection) QueryContext (ctx context.Context, sRequestText string, aBindValues [] driver.NamedValue) (rows driver.Rows, err error) { // satisfies driver.QueryerContext

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter Conn.QueryContext %v sql=%v", con, sRequestText))
		defer func () { traceLog (fmt.Sprintf ("< leave Conn.QueryContext %v sql=%v ErrBadConn=%v err=%v", con, sRequestText, errors.Is (err, driver.ErrBadConn), err)) } ()
	}

	if con.m_socket == nil && con.m_params.M_sHostName != "-none" {
		err = con.makeBadConnectionError (nil, 570, "Connection is closed")
		return
	}

	stmt, err := newTeradataStatement (con, ctx, sRequestText)
	if err != nil { return }

	rows, err = stmt.QueryContext (ctx, aBindValues)
	return

} // end method QueryContext

func (con *TeradataConnection) executeSessionQueryRow (ctx context.Context, sRequestText string) (row [] driver.Value, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter executeSessionQueryRow %v sql=%v", con, sRequestText))
		defer func () { traceLog (fmt.Sprintf ("< leave executeSessionQueryRow %v sql=%v", con, sRequestText)) } ()
	}

	sRequestText = "{fn teradata_rpo(E)}{fn teradata_fake_result_sets_off}{fn teradata_values_off}" + sRequestText

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter executeSessionQueryRow %v sql=%v", con, sRequestText))
		defer func () { traceLog (fmt.Sprintf ("< leave executeSessionQueryRow %v sql=%v", con, sRequestText)) } ()
	}

	rows, err := con.QueryContext (ctx, sRequestText, nil)
	if err != nil { return }

	defer rows.Close ()

	row = make ([] driver.Value, len (rows.Columns ()))

	err = rows.Next (row)
	if err != nil { return }

	return

} // end method executeSessionQueryRow

func (con *TeradataConnection) queryDatabaseLSN (ctx context.Context) (uLogonSequenceNumber uint32, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter queryDatabaseLSN %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave queryDatabaseLSN %v uLogonSequenceNumber=%v", con, uLogonSequenceNumber)) } ()
	}

	row, err := con.executeSessionQueryRow (ctx, "select cast(from_bytes('00'xb || substring(LogonSequenceNo from 2 for 1) || substring(LogonSequenceNo from 1 for 1) || substring(LogonSequenceNo from 4 for 1) || substring(LogonSequenceNo from 3 for 1), 'base10') as integer) as LSN from DBC.SessionInfoV where SessionNo = session")
	if err != nil { return }

	uLogonSequenceNumber = uint32 (row [0].(int32))
	return

} // end method queryDatabaseLSN

func (con *TeradataConnection) executeSessionCommand (ctx context.Context, sRequestText string) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter executeSessionCommand %v sql=%v", con, sRequestText))
		defer func () { traceLog (fmt.Sprintf ("< leave executeSessionCommand %v sql=%v", con, sRequestText)) } ()
	}

	_, err = con.ExecContext (ctx, sRequestText, nil)
	return

} // end method executeSessionCommand

func (con *TeradataConnection) executeAsynchronousAbort (bUseUCAbort bool) (abyAsynchronousAbortResponseMsg [] byte, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter executeAsynchronousAbort %v bUseUCAbort=%v", con, bUseUCAbort))
		defer func () { traceLog (fmt.Sprintf ("< leave executeAsynchronousAbort %v", con)) } ()
	}

	uPclAbortType := func () uint16 { if bUseUCAbort { return PCLUCABORT } ; return PCLABORT} ()
	abyAsynchronousAbortMsg := con.makeRequestMessage (MESSAGE_KIND_ABORT, NEW_CRYPTO_NONCE, con.m_uCurrentRequestNumber.Load (), makeParcel (uPclAbortType))
	abyAsynchronousAbortResponseMsg, _, err = con.sendAndReceive (&NetworkIO { abyMsgToSend: abyAsynchronousAbortMsg })

	if err != nil { return }

	err = con.processMessageSuccessOrError (abyAsynchronousAbortResponseMsg, /*bRequired*/ false)
	return

}  // end executeAsynchronousAbort

func (con *TeradataConnection) databaseCapableOfRedriveLevel () (uOutput uint32) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter databaseCapableOfRedriveLevel %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave databaseCapableOfRedriveLevel %v returning %v", con, uOutput)) } ()
	}

	bConfigControlData := con.m_config.M_uControlDataSupport >= 1
	bConfigRNP         := con.m_config.M_uRNPSupport >= 1
	bConfigRedrive     := con.m_config.M_uRedriveSupport >= 1

	if ! con.m_gtwConfig.M_bControlData || ! bConfigControlData {
		uOutput = 0 // ControlData not supported

	} else if ! con.m_gtwConfig.M_bRecoverableProtocol || ! bConfigRNP {
		uOutput = 1 // ControlData supported, but RecoverableNP not supported

	} else if ! con.m_gtwConfig.M_bRedrive || ! bConfigRedrive {
		uOutput = 2 // ControlData and RecoverableNP supported, but Redrive not supported

	} else {
		uOutput = 3 // ControlData, RecoverableNP, and Redrive all supported
	}

	return

} // end method databaseCapableOfRedriveLevel

func (con *TeradataConnection) isRNPActive () bool {

	return con.m_bSessionSupportsRNP && con.m_params.M_uRedrive >= 2

} // end method isRNPActive

func (con *TeradataConnection) isRedriveActive () bool {

	return con.m_bSessionShouldUseRedrive && con.m_bSessionSupportsRedrive && con.m_params.M_uRedrive >= 3

} // end method isRedriveActive
