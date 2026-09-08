// Copyright 2021 by Teradata Corporation. All Rights Reserved.
package goteragss

import (
	"fmt"
	"runtime"
)

// Severity Codes used in MinorStatus
const (
	// Severity Code Text
	TDGSS_ERR_SEV_SUCCESS_STR     = "SUCCESS"
	TDGSS_ERR_SEV_WARNING_STR     = "WARNING"
	TDGSS_ERR_SEV_ERROR_STR       = "ERROR"
)

/**************************************************************
* formatMinorStatus:
*   Returns a string containing the error location, error number
*   and error text
***************************************************************/
func formatMinorStatus(status TdgssStatus) (msg string) {

	// First get filename, line number
	_, fn, line, _ := runtime.Caller(1)
	msg1 := fmt.Sprintf("%s:%d ", fn, line)

	// Then get error details
	msg2 := formatMinorStatusErrOnly(status)
	msg = msg1 + msg2
	return
}

func GetMinorStatusErrorText(status TdgssStatus) (errorText string, severityText string) {

	errorText = gMinorStatusCodes[status.Minor]
	severityText = TDGSS_ERR_SEV_ERROR_STR

	if len(errorText) == 0 {
		errorText = "UNKNOWN MINORSTATUS CODE"

	} else if status.Minor < 0x40000000 {
		severityText = TDGSS_ERR_SEV_SUCCESS_STR

	} else if (status.Minor & 0xf0000000) == 0xa0000000 {
		severityText = TDGSS_ERR_SEV_WARNING_STR
	}

	return
}

/***************************************************************
* formatMinorStatusErrOnly:
*   Returns a string containing the error number and error text only
***************************************************************/
func formatMinorStatusErrOnly(status TdgssStatus) (msg string) {

	errorText, severityText := GetMinorStatusErrorText(status)
	msg = fmt.Sprintf("minorStatus=%d (0x%X) %s: %s", status.Minor,
		status.Minor, severityText, errorText)
	return
}
