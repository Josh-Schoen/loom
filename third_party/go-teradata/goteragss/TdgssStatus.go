// Copyright 2021 by Teradata Corporation. All Rights Reserved.
package goteragss

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
)

type TdgssStatus struct {
	Major uint32
	Minor uint32
	Err   error
}

/**************************************************************
* formatTdgssStatus:
*   Returns a string containing the error location, error number
*   and error text for both the major and minor status
***************************************************************/
func formatTdgssStatus(status TdgssStatus) string {

	// First get filename, line number
	_, fn, line, _ := runtime.Caller(1)
	return fmt.Sprintf("%s:%d %v", fn, line, formatTdgssStatusErrOnly(status))
}

/***************************************************************
* formatTdgssStatusErrOnly:
*   Returns a string containing the error number and error text only
*   for both major and minor status.
***************************************************************/
func formatTdgssStatusErrOnly(status TdgssStatus) (msg string) {

	msg1 := formatMajorStatusErrOnly(status)
	msg2 := formatMinorStatusErrOnly(status)
	msg = msg1 + " " + msg2
	return
}

func tdgssError(errCause error, sMessage string) error {

	sCause := ""
	if errCause != nil {
		sCause = "\n" + "Caused by " + errCause.Error()
	}

	ap := make([]uintptr, 64)       // maximum number of stack frames
	count := runtime.Callers(2, ap) // 2 specifies the caller's frame
	frames := runtime.CallersFrames(ap[:count])

	sBackTrace := ""
	for bMore := true; bMore; {
		var frame runtime.Frame
		frame, bMore = frames.Next()
		sBackTrace += fmt.Sprintf("\n at %v %v:%v", frame.Function, filepath.Base(frame.File), frame.Line)
	}

	return errors.New(sMessage + sBackTrace + sCause)
}
