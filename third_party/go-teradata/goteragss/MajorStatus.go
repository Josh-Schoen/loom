// Copyright 2021 by Teradata Corporation. All Rights Reserved.
package goteragss

import (
	"fmt"
	"runtime"
)

// MajorStatus error masks, codes and text
const (
	//
	// Error Masks
	//
	CALLING_ERROR_MASK      = 0xff000000
	ROUTINE_ERROR_MASK      = 0x00ff0000
	SUPPLEMENTAL_BITS_MASK  = 0x0000ffff
	GSS_ERROR_MASK          = CALLING_ERROR_MASK | ROUTINE_ERROR_MASK

	//
	// Calling Error Codes
	//
	GSS_S_CALL_INACCESSIBLE_READ  = 0x1000000
	GSS_S_CALL_INACCESSIBLE_WRITE = 0x2000000
	GSS_S_CALL_BAD_STRUCTURE      = 0x3000000

	//
	// Calling Error Messages
	//
	GSS_S_CALL_INACCESSIBLE_READ_STR  = "Inaccessible read"
	GSS_S_CALL_INACCESSIBLE_WRITE_STR = "Inaccessible write"
	GSS_S_CALL_BAD_STRUCTURE_STR      = "Bad structure"

	//
	// Routine Error Codes
	//
	GSS_S_COMPLETE             = 0x00000
	GSS_S_BAD_MECH             = 0x10000
	GSS_S_BAD_NAME             = 0x20000
	GSS_S_BAD_NAMETYPE         = 0x30000
	GSS_S_BAD_BINDINGS         = 0x40000
	GSS_S_BAD_STATUS           = 0x50000
	GSS_S_BAD_MIC              = 0x60000
	GSS_S_BAD_SIG              = GSS_S_BAD_MIC  // Deprecated
	GSS_S_NO_CRED              = 0x70000
	GSS_S_NO_CONTEXT           = 0x80000
	GSS_S_DEFECTIVE_TOKEN      = 0x90000
	GSS_S_DEFECTIVE_CREDENTIAL = 0xa0000
	GSS_S_CREDENTIALS_EXPIRED  = 0xb0000
	GSS_S_CONTEXT_EXPIRED      = 0xc0000
	GSS_S_FAILURE              = 0xd0000
	GSS_S_BAD_QOP              = 0xe0000
	GSS_S_UNAUTHORIZED         = 0xf0000
	GSS_S_UNAVAILABLE          = 0x100000
	GSS_S_DUPLICATE_ELEMENT    = 0x110000
	GSS_S_NAME_NOT_MN          = 0x120000
	GSS_S_CONTINUE             = 0x130000  // Deprecated

	//
	// Routine Error Messages
	//
	GSS_S_COMPLETE_STR             = "Successful"
	GSS_S_BAD_MECH_STR             = "Bad mechanism"
	GSS_S_BAD_NAME_STR             = "Bad name"
	GSS_S_BAD_NAMETYPE_STR         = "Bad name type"
	GSS_S_BAD_BINDINGS_STR         = "Bad bindings"
	GSS_S_BAD_STATUS_STR           = "Bad status"
	GSS_S_BAD_MIC_STR              = "Bad MIC"
	GSS_S_NO_CRED_STR              = "No credential"
	GSS_S_NO_CONTEXT_STR           = "No context"
	GSS_S_DEFECTIVE_TOKEN_STR      = "Defective token"
	GSS_S_DEFECTIVE_CREDENTIAL_STR = "Defective credential"
	GSS_S_CREDENTIALS_EXPIRED_STR  = "Credentials expired"
	GSS_S_CONTEXT_EXPIRED_STR      = "Context expired"
	GSS_S_FAILURE_STR              = "Failure"
	GSS_S_BAD_QOP_STR              = "Bad QoP"
	GSS_S_UNAUTHORIZED_STR         = "Unauthorized"
	GSS_S_UNAVAILABLE_STR          = "Unavailable"
	GSS_S_DUPLICATE_ELEMENT_STR    = "Duplicate element"
	GSS_S_NAME_NOT_MN_STR          = "Name not mechanism name"
	GSS_S_CONTINUE_STR             = "Continue"

	//
	// Supplemental Status Bit Codes
	//
	GSS_S_CONTINUE_NEEDED    = 0x00000001
	GSS_S_DUPLICATE_TOKEN    = 0x00000002
	GSS_S_OLD_TOKEN          = 0x00000004
	GSS_S_UNSEQ_TOKEN        = 0x00000008
	GSS_S_GAP_TOKEN          = 0x00000010
	GSS_S_SUPPL_UNUSED_BIT5  = 0x00000020
	GSS_S_SUPPL_UNUSED_BIT6  = 0x00000040
	GSS_S_SUPPL_UNUSED_BIT7  = 0x00000080
	GSS_S_SUPPL_UNUSED_BIT8  = 0x00000100
	GSS_S_SUPPL_UNUSED_BIT9  = 0x00000200
	GSS_S_SUPPL_UNUSED_BIT10 = 0x00000400
	GSS_S_SUPPL_UNUSED_BIT11 = 0x00000800
	GSS_S_SUPPL_UNUSED_BIT12 = 0x00001000
	GSS_S_SUPPL_UNUSED_BIT13 = 0x00002000
	GSS_S_SUPPL_UNUSED_BIT14 = 0x00004000
	GSS_S_SUPPL_UNUSED_BIT15 = 0x00008000

	//
	// Supplemental Status Bit Messages
	//
	GSS_S_CONTINUE_NEEDED_STR    = "Continue needed"
	GSS_S_DUPLICATE_TOKEN_STR    = "Duplicate token"
	GSS_S_OLD_TOKEN_STR          = "Old token"
	GSS_S_UNSEQ_TOKEN_STR        = "Unsequenced token"
	GSS_S_GAP_TOKEN_STR          = "Gap token"
	GSS_S_SUPPL_UNUSED_BIT5_STR  = "UNUSED SUPPLEMENTAL BIT 5"
	GSS_S_SUPPL_UNUSED_BIT6_STR  = "UNUSED SUPPLEMENTAL BIT 6"
	GSS_S_SUPPL_UNUSED_BIT7_STR  = "UNUSED SUPPLEMENTAL BIT 7"
	GSS_S_SUPPL_UNUSED_BIT8_STR  = "UNUSED SUPPLEMENTAL BIT 8"
	GSS_S_SUPPL_UNUSED_BIT9_STR  = "UNUSED SUPPLEMENTAL BIT 9"
	GSS_S_SUPPL_UNUSED_BIT10_STR = "UNUSED SUPPLEMENTAL BIT 10"
	GSS_S_SUPPL_UNUSED_BIT11_STR = "UNUSED SUPPLEMENTAL BIT 11"
	GSS_S_SUPPL_UNUSED_BIT12_STR = "UNUSED SUPPLEMENTAL BIT 12"
	GSS_S_SUPPL_UNUSED_BIT13_STR = "UNUSED SUPPLEMENTAL BIT 13"
	GSS_S_SUPPL_UNUSED_BIT14_STR = "UNUSED SUPPLEMENTAL BIT 14"
	GSS_S_SUPPL_UNUSED_BIT15_STR = "UNUSED SUPPLEMENTAL BIT 15"
)

// Map MinorStatus codes to text
var gMajorStatusCodes = map[uint32]string {
	//
	// Routine Errors
	//
	GSS_S_COMPLETE:             GSS_S_COMPLETE_STR,
	GSS_S_BAD_MECH:             GSS_S_BAD_MECH_STR,
	GSS_S_BAD_NAME:             GSS_S_BAD_NAME_STR,
	GSS_S_BAD_NAMETYPE:         GSS_S_BAD_NAMETYPE_STR,
	GSS_S_BAD_BINDINGS:         GSS_S_BAD_BINDINGS_STR,
	GSS_S_BAD_STATUS:           GSS_S_BAD_STATUS_STR,
	GSS_S_BAD_MIC:              GSS_S_BAD_MIC_STR,
	GSS_S_NO_CRED:              GSS_S_NO_CRED_STR,
	GSS_S_NO_CONTEXT:           GSS_S_NO_CONTEXT_STR,
	GSS_S_DEFECTIVE_TOKEN:      GSS_S_DEFECTIVE_TOKEN_STR,
	GSS_S_DEFECTIVE_CREDENTIAL: GSS_S_DEFECTIVE_CREDENTIAL_STR,
	GSS_S_CREDENTIALS_EXPIRED:  GSS_S_CREDENTIALS_EXPIRED_STR,
	GSS_S_CONTEXT_EXPIRED:      GSS_S_CONTEXT_EXPIRED_STR,
	GSS_S_FAILURE:              GSS_S_FAILURE_STR,
	GSS_S_BAD_QOP:              GSS_S_BAD_QOP_STR,
	GSS_S_UNAUTHORIZED:         GSS_S_UNAUTHORIZED_STR,
	GSS_S_UNAVAILABLE:          GSS_S_UNAVAILABLE_STR,
	GSS_S_DUPLICATE_ELEMENT:    GSS_S_DUPLICATE_ELEMENT_STR,
	GSS_S_NAME_NOT_MN:          GSS_S_NAME_NOT_MN_STR,
	GSS_S_CONTINUE:             GSS_S_CONTINUE_STR,

	//
	// Calling Errors
	//
	GSS_S_CALL_INACCESSIBLE_READ:  GSS_S_CALL_INACCESSIBLE_READ_STR,
	GSS_S_CALL_INACCESSIBLE_WRITE: GSS_S_CALL_INACCESSIBLE_WRITE_STR,
	GSS_S_CALL_BAD_STRUCTURE:      GSS_S_CALL_BAD_STRUCTURE_STR,

	//
	// Supplemental Status Bits
	//
	GSS_S_CONTINUE_NEEDED:    GSS_S_CONTINUE_NEEDED_STR,
	GSS_S_DUPLICATE_TOKEN:    GSS_S_DUPLICATE_TOKEN_STR,
	GSS_S_OLD_TOKEN:          GSS_S_OLD_TOKEN_STR,
	GSS_S_UNSEQ_TOKEN:        GSS_S_UNSEQ_TOKEN_STR,
	GSS_S_GAP_TOKEN:          GSS_S_GAP_TOKEN_STR,
	GSS_S_SUPPL_UNUSED_BIT5:  GSS_S_SUPPL_UNUSED_BIT5_STR,
	GSS_S_SUPPL_UNUSED_BIT6:  GSS_S_SUPPL_UNUSED_BIT6_STR,
	GSS_S_SUPPL_UNUSED_BIT7:  GSS_S_SUPPL_UNUSED_BIT7_STR,
	GSS_S_SUPPL_UNUSED_BIT8:  GSS_S_SUPPL_UNUSED_BIT8_STR,
	GSS_S_SUPPL_UNUSED_BIT9:  GSS_S_SUPPL_UNUSED_BIT9_STR,
	GSS_S_SUPPL_UNUSED_BIT10: GSS_S_SUPPL_UNUSED_BIT10_STR,
	GSS_S_SUPPL_UNUSED_BIT11: GSS_S_SUPPL_UNUSED_BIT11_STR,
	GSS_S_SUPPL_UNUSED_BIT12: GSS_S_SUPPL_UNUSED_BIT12_STR,
	GSS_S_SUPPL_UNUSED_BIT13: GSS_S_SUPPL_UNUSED_BIT13_STR,
	GSS_S_SUPPL_UNUSED_BIT14: GSS_S_SUPPL_UNUSED_BIT14_STR,
	GSS_S_SUPPL_UNUSED_BIT15: GSS_S_SUPPL_UNUSED_BIT15_STR,
}

/***************************************************************
* formatMajorStatus:
*   Returns a string containing the error location, error number
*   and error text
***************************************************************/
func formatMajorStatus(status TdgssStatus) (msg string) {

	// First get filename, line number
	_, fn, line, _ := runtime.Caller(1)
	msg1 := fmt.Sprintf("%s:%d ", fn, line)

	// Then get error details
	msg2 := formatMajorStatusErrOnly(status)
	msg = msg1 + msg2
	return
}

/***************************************************************
* formatMajorStatusErrOnly:
*   Returns a string containing the error number and error text only
***************************************************************/
func formatMajorStatusErrOnly(status TdgssStatus) (msg string) {
	var errorText, callingText, routineText, supplementalText string

	errorText = fmt.Sprintf("majorStatus=%d (0x%X) ", status.Major, status.Major)

	// Get the Calling Error, if any
	callingError := status.Major & CALLING_ERROR_MASK
	if callingError != 0 {
		callingText = "[" + gMajorStatusCodes[callingError] + "]"
		if callingText == "[]" {
			callingText = "[UNKNOWN CALLING ERROR]"
		}
	}

	// Get the Routine Error
	routineText = "[" + gMajorStatusCodes[status.Major & ROUTINE_ERROR_MASK] + "]"
	if routineText == "[]" {
		routineText = "[UNKNOWN MAJORSTATUS ERROR]"
	}

	// Finally if any Supplemental status bits are set, get them one by one
	var bit uint32
	supplementalBits := status.Major & SUPPLEMENTAL_BITS_MASK
	if supplementalBits != 0 {
		for i := 0; i < 16; i++ {
			bit = uint32(1<<i)
			if supplementalBits & bit != 0 {
				supplementalText += "[" + gMajorStatusCodes[bit] + "]"
			}
		}
	}
	msg = errorText + callingText + routineText + supplementalText
	return
}
