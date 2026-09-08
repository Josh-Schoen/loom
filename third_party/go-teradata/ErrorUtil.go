// Copyright 2017 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"github.com/Teradata-TIO/go-teradata/goteragss"
)

func simpleGoStack (sSep string) string {

	ap := make ([] uintptr, 64) // maximum number of stack frames
	nCount := runtime.Callers (2, ap) // 2 specifies the caller's frame
	frames := runtime.CallersFrames (ap [ : nCount])

	asGoStack := [] string {}
	for bMore := true ; bMore ; {
		var frame runtime.Frame
		frame, bMore = frames.Next ()

		if strings.HasPrefix (frame.Function, "runtime." ) { continue }
		if strings.HasPrefix (frame.Function, "main._cgo") { continue }

		iDot := strings.Index (frame.Function, ".")
		if iDot < 0 { continue }

		sPackage := frame.Function [ : iDot]
		if sPackage == "main" { sPackage = filepath.Base (frame.File) }

		if strings.Contains (sPackage, "/") {
			asParts := strings.Split (sPackage, "/")
			sPackage = strings.Join (asParts [len (asParts) - 2 : ], "/") // take the last 2 parts only
		}

		if sPackage != "" && ! sliceContainsString (asGoStack, sPackage) {
			asGoStack = append ([] string { sPackage }, asGoStack ...) // produce order caller -> callee
		}
	} // end for

	return strings.Join (asGoStack, sSep)

} // end simpleGoStack

func formatRecover (sPrefix string, ex any) (sOutput string) {

	sOutput = fmt.Sprintf ("%v: %v", sPrefix, ex)

	ap := make ([] uintptr, 64) // maximum number of stack frames
	nCount := runtime.Callers (2, ap) // 2 specifies the caller's frame
	frames := runtime.CallersFrames (ap [ : nCount])
	for bMore := true ; bMore ; {
		var frame runtime.Frame
		frame, bMore = frames.Next ()
		sOutput += fmt.Sprintf ("\n at %v %v:%v", frame.Function, filepath.Base (frame.File), frame.Line)
	}

	return

} // end formatRecover

func formatStackTrace (sCause string, sMessage string) string {

	if sCause != "" {
		sCause = "\n" + "Caused by " + sCause
	}

	ap := make ([] uintptr, 64) // maximum number of stack frames
	nCount := runtime.Callers (2, ap) // 2 specifies the caller's frame
	frames := runtime.CallersFrames (ap [ : nCount])

	sStackTrace := ""
	for bMore := true ; bMore ; {
		var frame runtime.Frame
		frame, bMore = frames.Next ()
		sStackTrace += fmt.Sprintf ("\n at %v %v:%v", frame.Function, filepath.Base (frame.File), frame.Line)
	}

	return sMessage + sStackTrace + sCause

} // end formatStackTrace

func MakeError (errCause error, sMessage string) error {

	sCause := ""

	if errCause != nil {
		sCause = errCause.Error ()
	}

	return preserveSpecialError (errors.New (formatStackTrace (sCause, sMessage)), errCause)

} // end MakeError

func formatError (errCause error, sFormat string, a ... any) error {

	return MakeError (errCause, fmt.Sprintf (sFormat, a ...))

} // end formatError

func concatErrors (errFirst error, sTransition string, errSecond error) error {

	if errFirst == nil {
		return errSecond

	} else if errSecond == nil {
		return errFirst

	} else { // both are non-nil
		return preserveSpecialError (errors.New (errFirst.Error () + "\n" + sTransition + errSecond.Error ()), errFirst, errSecond)
	}
} // end concatErrors

func causedBy (errFirst error, errSecond error) error {

	return concatErrors (errFirst, "Caused by ", errSecond)

} // end causedBy

func chainErrors (errFirst error, errSecond error) error {

	return concatErrors (errFirst, "Followed by ", errSecond)

} // end chainErrors

func enumerateErrors (errs [] error) error {

	sMessage := ""

	for i, err := range errs {
		sMessage += ternary (i > 0, "\n", "") +
			ternary (len (errs) > 1, fmt.Sprintf ("Error %v of %v: ", i + 1, len (errs)), "") +
			fmt.Sprintf ("%v", err)
	}

	errs = append ([] error { errors.New (sMessage) }, errs ...) // prepend

	return preserveSpecialError (errs ...)

} // end enumerateErrors

var regexErrorCode = regexp.MustCompile ("\\[Error (\\d+)\\]")

func GetErrorCode (errInput error) int {

	s := fmt.Sprintf ("%v", errInput)
	as := regexErrorCode.FindStringSubmatch (s)
	if as != nil && len (as) == 2 {
		if n, err := strconv.Atoi (as [1]) ; err == nil {
			return n
		}
	}

	return 0

} // end GetErrorCode

func (con *TeradataConnection) makeDriverError (errCause error, nErrorCode int, sMessage string) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter makeDriverError nErrorCode=%v %v", nErrorCode, con))
		defer func () { traceLog (fmt.Sprintf ("< leave makeDriverError nErrorCode=%v %v", nErrorCode, con)) } ()
	}

	if nErrorCode != 0 {
		err = formatError (errCause, "[Version %v] [Session %v] [Teradata SQL Driver] [Error %v] %v", G_sVersionNumber, con.m_uSessionNumber, nErrorCode, sMessage)
	} else {
		err = formatError (errCause, "[Version %v] [Session %v] [Teradata SQL Driver] %v", G_sVersionNumber, con.m_uSessionNumber, sMessage)
	}

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("makeDriverError: %v %v", con, err))
	}

	return

} // end method makeDriverError

func (con *TeradataConnection) makeDriverWarning (uWarningCode uint16, sMessage string) string {

	sMessage = fmt.Sprintf ("[Version %v] [Session %v] [Teradata SQL Driver] [Warning %v] %v", G_sVersionNumber, con.m_uSessionNumber, uWarningCode, sMessage)
	return formatStackTrace ("", sMessage)

} // end method makeDriverWarning

func (con *TeradataConnection) makeBadConnectionError (errCause error, nErrorCode int, sMessage string) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter makeBadConnectionError %v nErrorCode=%v", con, nErrorCode))
		defer func () { traceLog (fmt.Sprintf ("< leave makeBadConnectionError %v err=%v", con, err)) } ()
	}

	err = preserveSpecialError (con.makeDriverError (errCause, nErrorCode, sMessage), driver.ErrBadConn) // tell the connection pool to discard this connection
	return

} // end method makeBadConnectionError

func (con *TeradataConnection) formatDatabaseWarning (uWarningCode uint16, sWarningMsg string) string {

	return fmt.Sprintf ("[Teradata Database] [Warning %v] %v", uWarningCode, sWarningMsg)

} // end method formatDatabaseWarning

func (con *TeradataConnection) formatDatabaseError (errCause error, uErrorCode uint16, sErrorMsg string) error {

	return formatError (errCause, "[Version %v] [Session %v] [Teradata Database] [Error %v] %v", G_sVersionNumber, con.m_uSessionNumber, uErrorCode, sErrorMsg)

} // end method formatDatabaseError

func (con *TeradataConnection) makeChainedDatabaseError (errCause error, abyParcelHeader [] byte) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter makeChainedDatabaseError %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave makeChainedDatabaseError %v", con)) } ()
	}

	uParcelFlavor := cookedParcelFlavor (abyParcelHeader)
	abyParcelBody := constrainedParcelBody (abyParcelHeader)
	uErrorCode    := binary.BigEndian.Uint16 (abyParcelBody [4 : ])
	sErrorMsg     := string (con.deserializeVarchar (abyParcelBody [6 : ]))

	err = con.formatDatabaseError (errCause, uErrorCode, sErrorMsg)

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("makeChainedDatabaseError: %v %v %v", con, parcelFlavor (uParcelFlavor), err))
	}

	return

} // end method makeChainedDatabaseError

func (con *TeradataConnection) makeDatabaseError (abyParcelHeader [] byte) error {

	return con.makeChainedDatabaseError (nil, abyParcelHeader)

} // end method makeDatabaseError

func (con *TeradataConnection) makeInterruptError (errContext error, bAbortSent bool) error {

	if errContext == nil { return nil }

	switch errContext {

		case context.DeadlineExceeded:
			if bAbortSent {
				return con.makeDriverError (errContext, 531, "The context timeout expired while the SQL request was being executed, so an Abort Request was sent to the Teradata Database.")
			} else {
				return con.makeDriverError (errContext, 531, "The context timeout expired while the SQL request was being executed.")
			}

		case context.Canceled:
			return con.makeDriverError (errContext, 530,  "The context associated with this SQL request has been canceled by another thread." )
	}

	return nil

} // end makeInterruptError

func wasInterrupted (ctx context.Context) bool {

	if ctx == nil { return false }

	select {
		case <- ctx.Done () :
			return true
		default:
			return false
	}
} // end wasInterrupted

func (con *TeradataConnection) makeChainedSecurityError (status goteragss.TdgssStatus) error {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter makeChainedSecurityError %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave makeChainedSecurityError %v", con)) } ()
	}

	sErrorMsg, _ := goteragss.GetMinorStatusErrorText (status)

	return formatError (status.Err, "[Version %v] [Session %v] [Teradata Security] [Error 0x%X/0x%X] %v", G_sVersionNumber, con.m_uSessionNumber, status.Major, status.Minor, sErrorMsg)

} // end makeChainedSecurityError
