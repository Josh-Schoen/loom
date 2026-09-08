// Copyright 2021 by Teradata Corporation. All Rights Reserved.
package goteragss

import (
	"bytes"
	"encoding/asn1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

const CONFIDENTIALITY = 0x4
const DELEGATION = 0x10

/* WrapUnwrapToken Tag */
const WRAPUNWRAPTOKEN_TAG = 0x0

/* WrapUnwrapToken  */
const WRAPUNWRAPTOKEN_MESSAGE_TAG = 0x0
const WRAPUNWRAPTOKEN_TOKENHEADER_TAG = 0x1
const WRAPUNWRAPTOKEN_COMPUTEDMAC_TAG = 0x2
const WRAPUNWRAPTOKEN_AUTH_TAG = 0x3

/* TokenHeader */
const TOKENHEADER_VERSION_TAG = 0x0
const TOKENHEADER_MSGTYPE_TAG = 0x1
const TOKENHEADER_FLAGS_TAG = 0x2
const TOKENHEADER_FQOP_TAG = 0x3
const TOKENHEADER_ULMSGLENGTH_TAG = 0x4
const TOKENHEADER_SEQNUMBER_TAG = 0x5

/*
	For confidentiality and integrity, tdatGenericNewWrap encrypts the message and computes HMAC for the encrypted message.
	For integrity only, tdatGenericNewWrap computes the HMAC for the message.
	Required: logger, tokenHdr, qop, and inMsg
	Return:   status, and outMsg, outMac, authTag
*/
func tdatGenericNewWrap(logger TdgssLogger, tokenHdr []byte, qop *algQOP, inMsg []byte) (status TdgssStatus, outMsg []byte, outMac []byte, authTag []byte) {
	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter tdatGenericNewWrap"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave tdatGenericNewWrap")) }()
	}

	cipherFn := qop.cipherCtx
	digestFn := qop.digestCtx

	// Check the flag to see if confidentiality is required.
	confidentialityFlag := false
	var flags byte
	status, flags = tdatIntGetFlags(tokenHdr)
	if status.Major != GSS_S_COMPLETE {
		status.Major = GSS_S_FAILURE
		status.Minor = TDAT_ERR_INT_FAILURE
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatGenericNewWrap: tdatIntGetFlags failed"))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	if (flags & CONFIDENTIALITY) != 0 {
		confidentialityFlag = true
	}

	finalMsgLength := len(inMsg) + len(tokenHdr)

	if digestFn == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = ALG_ERR_FUNC_NOT_INTITALIZED
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatGenericNewWrap: digestFn is null %v", qop))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if !confidentialityFlag {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatGenericNewWrap: integrity only"))
		}

		/* ONLY INTEGRITY */

		/****************************************************************************
		* ASN1 with Only Integrity:
		* -----------------------------
		*
		* final_message = input_message +  token_header
		* computed_hmac = hmac(final_message)
		*
		* output_message = input_message
		* mac_buffer = computed_hmac
		* token_header.ulMsgLength = length(input_message + token_header)
		*
		****************************************************************************/

		// set message header length to length of input message + token header length
		tdatIntSetMsgLength(uint32(finalMsgLength + len(tokenHdr)), tokenHdr)

		// Final message is input message + token header
		finalMsg := concat(inMsg, tokenHdr)

		// Generate HMAC for final message
		statusAlg, abyDigest := digestFn.generateHMAC(logger, finalMsg)
		if statusAlg.Major != ALG_SUCCESS {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatGenericNewWrap: generateHMAC failed: %d", statusAlg.Major))
			}
			status = statusAlg
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
			return
		}

		outMsg = inMsg
		outMac = abyDigest

	} else {

		/* CONFIDENTIALITY AND INTEGRITY */

		/****************************************************************************
		*  Confidentiality & Integrity:
		* ------------------------------------------
		*
		* final_message = input_message +  token_header
		* iv = token_header
		* encrypted_message = encrypt(final_message, iv, secret_key)
		*
		* output_message_buffer = encyrpted_message
		* output_hmac_buffer = HMAC(encrypted_message)
		* auth_tag = authTAG(generated only for AE modes)
		* token_header.ulMsgLength = length(encyrpted_message + token_header)
		*
		****************************************************************************/

		// Set the token header length
		if cipherFn == nil {
			status.Minor = ALG_ERR_FUNC_NOT_INTITALIZED
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatGenericNewWrap: cipherFN is null"))
			}
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		// Get the maximum encrypted length
		statusAlg, encryptedLength := cipherFn.aesReturnLength(logger, uint(finalMsgLength))
		if statusAlg.Major != ALG_SUCCESS {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatGenericNewWrap: aesReturnLength failed: %d", statusAlg.Major))
			}
			status = statusAlg
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
			return
		}

		// set message header length to length of encrypted message + token header length
		tdatIntSetMsgLength(uint32(int(encryptedLength) + len(tokenHdr)), tokenHdr)

		// final message = input message + token header
		finalMsg := concat(inMsg, tokenHdr)

		// IV is the token header
		var iv []byte
		if qop.mode == GCM96 {
			iv = make([]byte, 12)
			copy(iv, tokenHdr[len(tokenHdr)-12:])
		} else {
			iv = make([]byte, len(tokenHdr))
			copy(iv, tokenHdr)
		}

		// Encrypt the final message
		statusAlg, outMsg, authTag = cipherFn.aesEncrypt(logger, finalMsg, iv)
		if statusAlg.Major != ALG_SUCCESS {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatGenericNewWrap: aesEncrypt failed: %d", statusAlg.Major))
			}
			status = statusAlg
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
			return
		}
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatGenericNewWrap: aesEncrypt encrypted message length: %d authTag length %d", len(outMsg), len(authTag)))
		}

		// Calculate HMAC (For AEADGCM mode HMAC will not be calculated)
		if qop.mode != AEADGCM && qop.mode != GCM96 {
			encryptedMsg := make([]byte, len(outMsg))
			copy(encryptedMsg, outMsg)

			// Generate HMAC for final message
			statusAlg, abyDigest := digestFn.generateHMAC(logger, encryptedMsg)
			if statusAlg.Major != ALG_SUCCESS {
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("tdatGenericNewWrap: generateHMAC failed: %d", statusAlg.Major))
				}
				status = statusAlg
				status.Major = GSS_S_FAILURE
				status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
				return
			}
			outMac = abyDigest
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatGenericNewWrap hmac:\n%v", hex.Dump(outMac)))
			}
		}
	}

	status.Major = GSS_S_COMPLETE
	status.Minor = TDAT_S_COMPLETE
	return
} // End tdatGenericNewWrap

func tdatGenericNewUnwrap(logger TdgssLogger, tokenHdr []byte, qop *algQOP, inMsg []byte, inMac []byte, authTag []byte) (status TdgssStatus, outMsg []byte) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter tdatGenericNewUnwrap"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave tdatGenericNewUnwrap")) }()
	}

	cipherFn := qop.cipherCtx
	digestFn := qop.digestCtx

	// Check the flag to see if confidentiality is required.
	confidentialityFlag := false
	var flags byte
	status, flags = tdatIntGetFlags(tokenHdr)
	if status.Major != GSS_S_COMPLETE {
		status.Minor = TDAT_ERR_INT_FAILURE
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatGenericNewUnwrap: tdatIntGetFlags failed"))
		}
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	if (flags & CONFIDENTIALITY) != 0 {
		confidentialityFlag = true
	}

	/*both TDGSS and TeraGSS useses 12 byte of IV for AES-GCM mode*/
	/*if( (pTdatSecCtx->fCapabilities & TDGSSGCMIV12) && (pTdatSecCtx->fCapabilities & TERAGSSGCMIV12))
	{
		pTdatSecCtx->UnwrapCipherCtx.use12byteIVforGCM = 1;
	}*/

	if tokenHdr[0] != VERSION3 || tokenHdr[1] != ENCRYPTMSG2 {
		status.Minor = TDAT_ERR_INVALID_TOKEN
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatGenericNewUnwrap: Invalid token header."))
		}
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if digestFn == nil {
		status.Minor = ALG_ERR_FUNC_NOT_INTITALIZED
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatGenericNewUnwrap: digestFn is null %v", qop))
		}
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	/* input message comes with the header */
	if !confidentialityFlag {
		/* ONLY INTEGRITY */

		/****************************************************************************
		* Version3 with Only Integrity:
		* -----------------------------
		* VerifyHMAC(input_message_buffer + TH)
		*
		* output = plain_data (which is input_message_buffer)
		*
		****************************************************************************/

		/* HMAC is required here */
		if len(inMac) == 0 {
			status.Minor = TDAT_ERR_INVALID_TOKEN
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatGenericNewUnwrap: Missing HMAC - HMAC is required."))
			}
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		finalMsg := concat(inMsg, tokenHdr)

		/* Call verifyHMAC */
		statusAlg := digestFn.verifyHMAC(logger, finalMsg, inMac)
		if statusAlg.Major != ALG_SUCCESS {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatGenericNewUnwrap: verifyHMAC failed: %d", statusAlg.Major))
			}
			status = statusAlg
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
			return
		}

		/* Set the output to the input */
		outMsg = inMsg

		status.Minor = TDAT_S_COMPLETE
		status.Major = GSS_S_COMPLETE
		return
	} else {
		/* CONFIDENTIALITY AND INTEGRITY */

		/****************************************************************************
		* Version3 with Confidentiality & Integrity:
		* ------------------------------------------
		* final_message=  input_message_buffer + TH
		* VerifyHMAC(final_message) except for the mode AEADGCM and GCM96
		* decrypted_message = decrypt(input_message_buffer, iv, secret_key)
		* plain_datalenth = decrypted_message.length -
		*                  token_header.length
		*
		* output = plain_data
		*
		****************************************************************************/

		if cipherFn == nil {
			status.Minor = ALG_ERR_FUNC_NOT_INTITALIZED
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatGenericNewUnwrap: cipherFN is null"))
			}
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		/* For AEADGCM mode HMAC is not calculated. */
		if qop.mode != AEADGCM && qop.mode != GCM96 {

			if len(inMac) == 0 {
				status.Minor = TDAT_ERR_INVALID_TOKEN
				status.Major = GSS_S_FAILURE
				status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
				return
			}

			/* Call Verify HMAC */
			statusAlg := digestFn.verifyHMAC(logger, inMsg, inMac)
			if statusAlg.Major != ALG_SUCCESS {
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("tdatGenericNewUnwrap: verifyHMAC failed: %d", statusAlg.Major))
				}
				status = statusAlg
				status.Major = GSS_S_FAILURE
				status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
				return
			}
		}

		/* Initialize IV */
		var iv []byte
		if qop.mode == GCM96 {
			iv = make([]byte, 12)
			copy(iv, tokenHdr[len(tokenHdr)-12:])
		} else {
			iv = make([]byte, len(tokenHdr))
			copy(iv, tokenHdr)
		}

		/* Decrypt the message. decrypted_message = plain_data + token_header */
		msgToDecrypt := []byte{}
		if len(authTag) > 0 {
			msgToDecrypt = concat(inMsg, authTag)
		} else {
			msgToDecrypt = inMsg
		}
		statusAlg, out := cipherFn.aesDecrypt(logger, msgToDecrypt, iv)
		if statusAlg.Major != ALG_SUCCESS {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatGenericNewUnwrap: aesDecrypt failed: %d", statusAlg.Major))
			}
			status = statusAlg
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
			return
		}

		if len(out) < TDAT_TOKEN_HEADER_LENGTH {
			status.Minor = TDAT_ERR_INT_FAILURE
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatGenericNewUnwrap: decrypted message must contain the token header."))
			}
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}
		outMsg = out[0 : len(out)-TDAT_TOKEN_HEADER_LENGTH]
		status.Minor = TDAT_S_COMPLETE
		status.Major = GSS_S_COMPLETE
		return
	}
}

func tdatGenericOldWrap(logger TdgssLogger, tokenHdr []byte, qop *algQOP, inMsg []byte, hashKey []byte, peerCapabilities uint32) (status TdgssStatus, outMsg []byte) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter tdatGenericOldWrap"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave tdatGenericOldWrap")) }()
	}

	cipherFn := qop.cipherCtx
	digestFn := qop.digestCtx

	// Check the flag to see if confidentiality is required.
	confidentialityFlag := false
	var flags byte
	status, flags = tdatIntGetFlags(tokenHdr)
	if status.Major != GSS_S_COMPLETE {
		status.Minor = TDAT_ERR_INT_FAILURE
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatGenericOldWrap: tdatIntGetFlags failed"))
		}
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	if digestFn == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = ALG_ERR_FUNC_NOT_INTITALIZED
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatGenericNewWrap: digestFn is null %v", qop))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if (flags & CONFIDENTIALITY) != 0 {
		confidentialityFlag = true
	}

	if (peerCapabilities & UNIQUEIVKEYEDMIC) == 0 {

		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatGenericOldWrap: Require UNIQUEIVKEYEDMIC support"))
		}
		status.Major = GSS_S_FAILURE
		status.Minor = TDAT_ERR_INT_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return

	} else { // UNIQUEIVKEYEDMIC

		finalMsgLength := len(inMsg) + len(hashKey) + len(tokenHdr)

		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatGenericOldWrap: len(inMsg)=%d len(hashKey)=%d len(tokenHdr)=%d", len(inMsg), len(hashKey), len(tokenHdr)))
			logger.DebugLog(fmt.Sprintf("tdatGenericOldWrap: finalMsgLength=%d", finalMsgLength))
			logger.DebugLog(fmt.Sprintf("tdatGenericOldWrap: hashKey:\n%v", hex.Dump(hashKey)))
		}

		/* VERSION3 */
		if !confidentialityFlag {
			// ONLY INTEGRITY

			/****************************************************************************
				* Version3 with Only Integrity: 
				* -----------------------------
				* token_header.ulMsgLength = length(input_message + digest + token_header)
				* final_message = input_message + hash_secret_key + token_header
				* computed_mic = hash(final_message)
				* output_token = input_message + computed_mic + token_header 
				*
				****************************************************************************/

			/* final_message = input_message + hash_secret_key + token_header */

			/* Set the token header length */
			tdatIntSetMsgLength(uint32(finalMsgLength), tokenHdr)

			finalMsg := concat(inMsg, hashKey, tokenHdr)

			// Hash the final message.
			statusAlg, abyDigest := digestFn.generateDigest(logger, finalMsg)
			if statusAlg.Major != ALG_SUCCESS {
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("tdatGenericOldWrap: generateDigest failed: Major=0x%x  Minor=0x%x ", statusAlg.Major, statusAlg.Minor))
				}
				status = statusAlg
				status.Major = GSS_S_FAILURE
				status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
				return
			}
	
			outMsg = concat(inMsg, abyDigest, tokenHdr)

			status.Major = GSS_S_COMPLETE
			status.Minor = TDAT_S_COMPLETE
			return

		} else {

			// CONFIDENTIALITY AND INTEGRITY

			/****************************************************************************
				* Version3 with Confidentiality & Integrity: 
				* ------------------------------------------
				* final_message_length = length(input_message + hash_secret_key + token_header)
				* encrypted_message_length = ReturnEncryptedLength(final_message_length)
				* token_header.ulMsgLength = encrypted_message_length
				*
				* final_message = input_message + hash_secret_key + token_header
				* computed_mic = hash(final_message)
				*
				* final_message =  input_message + computed_mic + token_header
				* iv = token_header
				* encrypted_message = encrypt(final_message, iv, secret_key)
				*
				* output_token = encyrpted_message + token_header
				*
				****************************************************************************/

			if cipherFn == nil {
				status.Minor = ALG_ERR_FUNC_NOT_INTITALIZED
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("tdatGenericOldWrap: cipherFN is null"))
				}
				status.Major = GSS_S_FAILURE
				status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
				return
			}

			// Get the maximum encrypted length
			statusAlg, encryptedLength := cipherFn.aesReturnLength(logger, uint(finalMsgLength))
			if statusAlg.Major != ALG_SUCCESS {
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("tdatGenericOldWrap: aesReturnLength failed: %d", statusAlg.Major))
				}
				status = statusAlg
				status.Major = GSS_S_FAILURE
				status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
				return
			}
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatGenericOldWrap: encryptedLength=%d", encryptedLength))
			}
	

			// set message header length to length of encrypted message
			tdatIntSetMsgLength(uint32(encryptedLength), tokenHdr)

			// final message = input message + hashKey + token header
			finalMsg := concat(inMsg, hashKey, tokenHdr)

			// Hash the final message.
			statusAlg, abyDigest := digestFn.generateDigest(logger, finalMsg)
			if statusAlg.Major != ALG_SUCCESS {
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("tdatGenericOldWrap: generateDigest failed: Major=0x%x  Minor=0x%x ", statusAlg.Major, statusAlg.Minor))
				}
				status = statusAlg
				status.Major = GSS_S_FAILURE
				status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
				return
			}

			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatGenericOldWrap: len(abyDigest)=%d", len(abyDigest)))
				logger.DebugLog(fmt.Sprintf("tdatGenericOldWrap: abyDigest:\n%v", hex.Dump(abyDigest)))
			}

			finalMsgToEncrypt := concat(inMsg, abyDigest, tokenHdr)

			// IV is the token header
			var iv []byte = make([]byte, len(tokenHdr))
			copy(iv, tokenHdr)

			// Encrypt the final message
			statusAlg, encryptedMsg, _ := cipherFn.aesEncrypt(logger, finalMsgToEncrypt, iv)
			if statusAlg.Major != ALG_SUCCESS {
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("tdatGenericOldWrap: aesEncrypt failed: %d", statusAlg.Major))
				}
				status = statusAlg
				status.Major = GSS_S_FAILURE
				status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
				return
			}
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatGenericOldWrap: aesEncrypt encrypted message length: %d", len(encryptedMsg)))
			}

			outMsg = concat(encryptedMsg, tokenHdr)

			status.Major = GSS_S_COMPLETE
			status.Minor = TDAT_S_COMPLETE
			return
		}
	}

	status.Major = GSS_S_COMPLETE
	status.Minor = TDAT_S_COMPLETE
	return
}

func tdatGenericOldUnwrap(logger TdgssLogger, qop *algQOP, inMsg []byte, hashKey []byte, peerCapabilities uint32) (status TdgssStatus, outMsg []byte) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter tdatGenericOldUnwrap"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave tdatGenericOldUnwrap")) }()
	}

	cipherFn := qop.cipherCtx
	digestFn := qop.digestCtx

	inMsgLength := len(inMsg)

	if inMsgLength < TDAT_TOKEN_HEADER_LENGTH {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatGenericOldUnwrap: invalid input message length (%d)", inMsgLength))
		}
		status.Major = GSS_S_FAILURE
		status.Minor = TDAT_ERR_INVALID_INPUT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	tokenHdr := inMsg[inMsgLength - TDAT_TOKEN_HEADER_LENGTH:]
	tokenHdrLen := len(tokenHdr)

	// Check the flag to see if confidentiality is required.
	confidentialityFlag := false
	var flags byte
	status, flags = tdatIntGetFlags(tokenHdr)
	if status.Major != GSS_S_COMPLETE {
		status.Minor = TDAT_ERR_INT_FAILURE
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatGenericOldUnwrap: tdatIntGetFlags failed"))
		}
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	if digestFn == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = ALG_ERR_FUNC_NOT_INTITALIZED
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatGenericOldUnwrap: digestFn is null %v", qop))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if (flags & CONFIDENTIALITY) != 0 {
		confidentialityFlag = true
	}

	if (peerCapabilities & UNIQUEIVKEYEDMIC) == 0 {

		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatGenericOldUnwrap: Require UNIQUEIVKEYEDMIC support"))
		}
		status.Major = GSS_S_FAILURE
		status.Minor = TDAT_ERR_INT_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return

	} else {  // UNIQUEIVKEYEDMIC

		var version uint32
		var msgType uint32

		status, version = tdatIntGetVersion(tokenHdr)
		if status.Major != GSS_S_COMPLETE {
			status.Minor = TDAT_ERR_INT_FAILURE
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatGenericOldUnwrap: tdatIntGetVersion failed"))
			}
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		status, msgType = tdatIntGetMsgType(tokenHdr)
		if status.Major != GSS_S_COMPLETE {
			status.Minor = TDAT_ERR_INT_FAILURE
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatGenericOldUnwrap: tdatIntGetMsgType failed"))
			}
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		if version != VERSION3 || msgType != ENCRYPTMSG2 {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatGenericOldUnwrap: wrong version (%d) or msg type (%d)", version, msgType))
			}
			status.Major = GSS_S_FAILURE
			status.Minor = TDAT_ERR_INVALID_INPUT
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		// Get the length of the digest stored in this context.
		statusAlg, digestLen := digestFn.getDigestLength(logger)
		if statusAlg.Major != ALG_SUCCESS {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("tdatGenericOldUnwrap: getDigestLength failed: %v", status))
			}
			status = statusAlg
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		/* input message comes with the header */
		if !confidentialityFlag {
			/* ONLY INTEGRITY */

			/****************************************************************************
			* Version3 with Only Integrity: 
			* -----------------------------
			* recv_message = plain_data + mic + token_header
			* orig_message = plain_data + hash_secret_key + token_header
			* verify(mic, orig_message)
			* output = plain_data (which is, recv_message - mic - token_header)
			*
			****************************************************************************/

			if inMsgLength < tokenHdrLen + int(digestLen) {
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("tdatGenericOldUnwrap: invalid input message length (%d)", inMsgLength))
				}
				status.Major = GSS_S_FAILURE
				status.Minor = TDAT_ERR_INVALID_INPUT
				status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
				return
			}

			messageLength := inMsgLength - int(digestLen) - tokenHdrLen
			plainData := inMsg[0:messageLength]
			mic := inMsg[messageLength:messageLength + int(digestLen)]

			originalMsg := concat(plainData, hashKey, tokenHdr)

			statusAlg := digestFn.verifyDigest(logger, originalMsg, mic)
			if statusAlg.Major != ALG_SUCCESS {
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("tdatGenericOldUnwrap: verifyDigest failed: Major=0x%x  Minor=0x%x ", statusAlg.Major, statusAlg.Minor))
				}
				status = statusAlg
				status.Major = GSS_S_FAILURE
				status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
				return
			}

			/* Set the output, message_length has the correct plain data length */
			outMsg = plainData

			status.Major = GSS_S_COMPLETE
			status.Minor = TDAT_S_COMPLETE
			return

		} else {
			/* CONFIDENTIALITY AND INTEGRITY */

			/****************************************************************************
			* Version3 with Confidentiality & Integrity: 
			* ------------------------------------------
			* recv_message = encrypted_message + token_header
			* iv = token_header
			* decrypted_message = decrypt(encrypted_message, iv, secret_key) => plain_data + mic + token_header1
			* compare(token_header, token_header1)
			* final_message = plain_data + hash_secret_key + token_header
			* verify(mic, final_message)
			* output = plain_data (which is, decrypted_message - mic - token_header)
			*
			****************************************************************************/

			if cipherFn == nil {
				status.Minor = ALG_ERR_FUNC_NOT_INTITALIZED
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("tdatGenericOldUnwrap: cipherFN is null"))
				}
				status.Major = GSS_S_FAILURE
				status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
				return
			}

			encryptedMsgLength := inMsgLength - tokenHdrLen
			encryptedMsg := inMsg[0:encryptedMsgLength]

			// IV is the token header
			var iv []byte = make([]byte, tokenHdrLen)
			copy(iv, tokenHdr)

			statusAlg, decryptedMsg := cipherFn.aesDecrypt(logger, encryptedMsg, iv)
			if statusAlg.Major != ALG_SUCCESS {
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("tdatGenericOldUnwrap: aesDecrypt failed: %d", statusAlg.Major))
				}
				status = statusAlg
				status.Major = GSS_S_FAILURE
				status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
				return
			}

			decryptedMsgLength := len(decryptedMsg)
			if decryptedMsgLength < tokenHdrLen + int(digestLen) {
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("tdatGenericOldUnwrap: invalid decrypted message length (%d)", decryptedMsgLength))
				}
				status.Major = GSS_S_FAILURE
				status.Minor = TDAT_ERR_INVALID_INPUT
				status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
				return
			}

			tokenHdr1 := decryptedMsg[decryptedMsgLength - tokenHdrLen:]
			plainDataLength := decryptedMsgLength - int(digestLen) - len(tokenHdr1)
			plainData := decryptedMsg[0:plainDataLength]
			mic := decryptedMsg[plainDataLength:plainDataLength + int(digestLen)]

			/* Compare the decrypted header and received header for integrity */
			result := bytes.Equal(tokenHdr, tokenHdr1)
			if !result {
				status.Major = ALG_FAILURE
				status.Minor = TDAT_ERR_INVALID_INPUT
				status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
				return
			}

			originalMsg := concat(plainData, hashKey, tokenHdr)

			statusAlg = digestFn.verifyDigest(logger, originalMsg, mic)
			if statusAlg.Major != ALG_SUCCESS {
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("tdatGenericOldUnwrap: verifyDigest failed: Major=0x%x  Minor=0x%x ", statusAlg.Major, statusAlg.Minor))
				}
				status = statusAlg
				status.Major = GSS_S_FAILURE
				status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
				return
			}
			outMsg = plainData

			status.Major = GSS_S_COMPLETE
			status.Minor = TDAT_S_COMPLETE
			return
		}
	}
	status.Major = GSS_S_COMPLETE
	status.Minor = TDAT_S_COMPLETE
	return
}

/*
	tdatCryptoASN1Encode encodes the encrypted message, token header, hmac, authTag into one byte slice, wrappedMsg
	Required: message, tokenHdr, and hmac
	Optional: authTag
	Return:   status, and wrappedMsg
*/
func tdatCryptoASN1Encode(message []byte, tokenHdr []byte, hmac []byte, authTag []byte) (status TdgssStatus, wrappedMsg []byte) {
	/*
		The message, tokenHdr, hmac, and authTag are encoded in the following format:

		first byte is the tag type followed by one or more bytes containing the length of the data that follows
		If the length of the data fits in 7 bits that is <= 127, then we only need one byte for the length.
		If the length of the data cannot fit in 7 bits that is > 127, then we need 2 or more bytes for the
		length of the data. The high order bit of the first byte is set and then the other 7 bits will contain
		the number of bytes required for the length of the data.

		For example

		[0x81 0xff] means that the length of the data is 255 (0xff) which is > 127. 0x81 has the high order bit
		set and the other 7 bits says that it requires one byte to hold the length, 255.

		[0x82 0xff 0xff] means that the length of the data is 65535 (0xff 0xff) which is > 127. 0x82 has the high order bit
		set and the other 7 bits says that it requires 2 byte to hold the length, 65535.
		======================================================================================================


		e0 81 ff   GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | WRAPUNWRAPTOKEN_MESSAGE_TAG

			c0 81 b3   GSSP_TAG_PRIVATE | WRAPUNWRAPTOKEN_MESSAGE_TAG

			00000010                                             3a 03  |..............:.|
			00000020  89 28 01 3a 63 de e2 30  13 24 99 6a ec c0 cb e7  |.(.:c..0.$.j....|
			00000030  27 ed cc 32 a1 c7 e9 a6  4f 99 ea dc ae bb b4 d4  |'..2....O.......|
			00000040  12 43 ed 16 df ee 67 07  28 d2 43 79 f7 c9 fb 20  |.C....g.(.Cy... |
			00000050  64 1b 0f c9 ff ff b5 2b  4a ec 29 cc 8c 17 b6 98  |d......+J.).....|
			00000060  53 7d 24 c7 0b ed 4d 44  d9 5e f2 1e 92 e2 54 03  |S}$...MD.^....T.|
			00000070  ec 3d 19 68 61 14 87 f4  ad 59 f9 a1 ee 2c 80 48  |.=.ha....Y...,.H|
			00000080  2a 0c 69 e1 e2 81 a5 b0  b5 a7 ba d7 a0 b3 e4 93  |*.i.............|
			00000090  88 77 e3 ae a9 0f 28 a9  83 da ba 23 3e 49 b0 b6  |.w....(....#>I..|
			000000a0  69 7b d0 39 89 90 57 77  d7 06 22 8e 76 65 88 0c  |i{.9..Ww..".ve..|
			000000b0  cb 30 dd 6d 22 90 53 06  0c d3 e5 26 50 83 0f 53  |.0.m".S....&P..S|
			000000c0  a9 70 30 d0 f1 da 40 53  1b cb bd c3 ce 70 5f 79  |.p0...@S.....p_y|
			000000d0  0e



			e1 13   GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | WRAPUNWRAPTOKEN_TOKENHEADER_TAG

			000000d0           c0 01 03 c1 01  07 c2 01 04 c3 01 00 c4  |................|
			000000e0  02 00 c3 c5 01 01


			c2 20   GSSP_TAG_PRIVATE | WRAPUNWRAPTOKEN_COMPUTEDMAC_TAG

			000000e0                           92 8f 6a 6c 24 f5 e8 c6  |....... ..jl$...|
			000000f0  3d 84 92 4e 7d 77 d6 da  69 f5 21 82 65 56 26 dc  |=..N}w..i.!.eV&.|
			00000100  e2 ad 82 28 78 50 53 36



			c3 10   GSSP_TAG_PRIVATE | WRAPUNWRAPTOKEN_AUTH_TAG

			00000100                                 13 e1 6c a9 7c 81  |...(xPS6....l.|.|
			00000110  0d ac 04 0c 14 f9 e4 63  11 c7                    |.......c..|
	*/

	if len(message) == 0 || len(tokenHdr) < TDAT_TOKEN_HEADER_LENGTH {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_INVALID_ARGUMENT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	// Encode encrypted message
	encodedMsg := makeASN1Value(GSSP_TAG_PRIVATE|WRAPUNWRAPTOKEN_MESSAGE_TAG, message...)

	// Encode token header
	status, encodedTokenHdr := tdatASN1EncodeHdr(tokenHdr)
	if status.Major != GSS_S_COMPLETE {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	// Encode HMAC
	encodedHMAC := []byte{}
	if len(hmac) > 0 {
		encodedHMAC = makeASN1Value(GSSP_TAG_PRIVATE|WRAPUNWRAPTOKEN_COMPUTEDMAC_TAG, hmac...)
	}

	// Encode AuthTag
	encodedAuthTag := []byte{}
	if len(authTag) > 0 {
		encodedAuthTag = makeASN1Value(GSSP_TAG_PRIVATE|WRAPUNWRAPTOKEN_AUTH_TAG, authTag...)
	}

	wrappedMsg = makeASN1Value(GSSP_TAG_PRIVATE|GSSP_TAG_COMPOUND|WRAPUNWRAPTOKEN_MESSAGE_TAG,
		concat(encodedMsg, encodedTokenHdr, encodedHMAC, encodedAuthTag)...)

	return
} // End tdatCryptoASN1Encode

func tdatCryptoASN1Decode(wrappedMsg []byte) (status TdgssStatus, message []byte, tokenHdr []byte, hmac []byte, authTag []byte) {

	if len(wrappedMsg) == 0 {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_INVALID_ARGUMENT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Translate the input into an undecoded
	// ASN.1 format for subsequent processing.
	asn1Msg, err := readASN1Values(wrappedMsg)
	if err != nil {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_ASN_TAG_INVALID
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}

	// The tag identifiers must be:
	//  Tag class:  GSSP_TAG_PRIVATE
	//  Octet type: constructed/compound/set (0, 1, or more elements)
	//  Tag number: WRAPUNWRAPTOKEN_MESSAGE_TAG
	if asn1Msg[0].parent.Class != asn1.ClassPrivate || !asn1Msg[0].parent.IsCompound || asn1Msg[0].parent.Tag != WRAPUNWRAPTOKEN_MESSAGE_TAG {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_ASN_TAG_INVALID
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// The wrapped token message should have at least 3 children and at most 4 children.
	tokenParts := asn1Msg[0].children

	for i := 0; i < len(tokenParts); i++ {
		if tokenParts[i].parent.Class != asn1.ClassPrivate {
			status.Major = GSS_S_FAILURE
			status.Minor = TDGSS_ERR_ASN_TAG_INVALID
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		switch tokenParts[i].parent.Tag {
		case WRAPUNWRAPTOKEN_MESSAGE_TAG:
			// Message class = GSSP_TAG_PRIVATE Tag = WRAPUNWRAPTOKEN_MESSAGE_TAG
			message = tokenParts[i].parent.Bytes
		case WRAPUNWRAPTOKEN_TOKENHEADER_TAG:
			if !tokenParts[i].parent.IsCompound {
				status.Major = GSS_S_FAILURE
				status.Minor = TDGSS_ERR_ASN_TAG_INVALID
				status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
				return
			}
			status, tokenHdr = tdatASN1DecodeHdr(tokenParts[i])
			if status.Major != GSS_S_COMPLETE {
				status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
				return
			}
		case WRAPUNWRAPTOKEN_COMPUTEDMAC_TAG:
			hmac = tokenParts[i].parent.Bytes
		case WRAPUNWRAPTOKEN_AUTH_TAG:
			authTag = tokenParts[i].parent.Bytes
		default:
			status.Major = GSS_S_FAILURE
			status.Minor = TDGSS_ERR_ASN_TAG_INVALID
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}
	}

	if len(message) == 0 || len(tokenHdr) == 0 {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_INVALID_ARGUMENT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = GSS_S_COMPLETE
	return
} // End tdatCryptoASN1Decode

/*
	tdatASN1EncodeHdr encodes the token header into one byte slice, wrappedHdr
	Required: tokenHdr
	Return:   status, wrappedHdr
*/
func tdatASN1EncodeHdr(tokenHdr []byte) (status TdgssStatus, wrappedHdr []byte) {
	/*
		e1 13   -GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | WRAPUNWRAPTOKEN_TOKENHEADER_TAG

		000000d0           c0 01 03 c1 01  07 c2 01 04 c3 01 00 c4  |................|
		000000e0  02 00 c3 c5 01 01

		breakdown for the header
			c0 01 03       GSSP_TAG_PRIVATE | TOKENHEADER_VERSION_TAG
			c1 01 07       GSSP_TAG_PRIVATE | TOKENHEADER_MSGTYPE_TAG
			c2 01 04       GSSP_TAG_PRIVATE | TOKENHEADER_FLAGS_TAG
			c3 01 00       GSSP_TAG_PRIVATE | TOKENHEADER_FQOP_TAG
			c4 02 00 c3    GSSP_TAG_PRIVATE | TOKENHEADER_ULMSGLENGTH_TAG
			c5 01 01       GSSP_TAG_PRIVATE | TOKENHEADER_SEQNUMBER_TAG

	*/

	if len(tokenHdr) < TDAT_TOKEN_HEADER_LENGTH {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_INVALID_ARGUMENT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	version := makeASN1Value(GSSP_TAG_PRIVATE|TOKENHEADER_VERSION_TAG, tokenHdr[0])
	msgType := makeASN1Value(GSSP_TAG_PRIVATE|TOKENHEADER_MSGTYPE_TAG, tokenHdr[1])
	flags := makeASN1Value(GSSP_TAG_PRIVATE|TOKENHEADER_FLAGS_TAG, tokenHdr[2])
	fQop := makeASN1Value(GSSP_TAG_PRIVATE|TOKENHEADER_FQOP_TAG, tokenHdr[3])

	nMsgLength := int64(uint64(binary.BigEndian.Uint32(tokenHdr[4:8])))
	abyMsgLength := makeASN1Value(GSSP_TAG_PRIVATE|TOKENHEADER_ULMSGLENGTH_TAG, makeASN1Int64Payload(nMsgLength)...)

	nSeqNum := int64(binary.BigEndian.Uint64(tokenHdr[8:]))
	abySeqNum := makeASN1Value(GSSP_TAG_PRIVATE|TOKENHEADER_SEQNUMBER_TAG, makeASN1Int64Payload(nSeqNum)...)

	wrappedHdr = makeASN1Value(GSSP_TAG_PRIVATE|GSSP_TAG_COMPOUND|WRAPUNWRAPTOKEN_TOKENHEADER_TAG,
		concat(version, msgType, flags, fQop, abyMsgLength, abySeqNum)...)

	status.Major = GSS_S_COMPLETE
	return
} // End tdatASN1EncodeHdr

func tdatASN1DecodeHdr(tokenHdrParcel asn1Value) (status TdgssStatus, tokenHdr []byte) {

	/*
		e1 13   -GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | WRAPUNWRAPTOKEN_TOKENHEADER_TAG

		000000d0           c0 01 03 c1 01  07 c2 01 04 c3 01 00 c4  |................|
		000000e0  02 00 c3 c5 01 01

		breakdown for the header
			c0 01 03       GSSP_TAG_PRIVATE | TOKENHEADER_VERSION_TAG
			c1 01 07       GSSP_TAG_PRIVATE | TOKENHEADER_MSGTYPE_TAG
			c2 01 04       GSSP_TAG_PRIVATE | TOKENHEADER_FLAGS_TAG
			c3 01 00       GSSP_TAG_PRIVATE | TOKENHEADER_FQOP_TAG
			c4 02 00 c3    GSSP_TAG_PRIVATE | TOKENHEADER_ULMSGLENGTH_TAG
			c5 01 01       GSSP_TAG_PRIVATE | TOKENHEADER_SEQNUMBER_TAG

	*/

	var v3TokenHdr tdatV3TokenHeader

	// The wrapped token header should have 6 children.
	tokenParts := tokenHdrParcel.children
	if len(tokenParts) != 6 {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_INVALID_ARGUMENT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	for i := 0; i < len(tokenParts); i++ {
		if tokenParts[i].parent.Class != asn1.ClassPrivate || len(tokenParts[i].parent.Bytes) == 0 {
			status.Major = GSS_S_FAILURE
			status.Minor = TDGSS_ERR_ASN_TAG_INVALID
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		var uint64Temp uint64 = 0
		if len(tokenParts[i].parent.Bytes) == 1 {
			uint64Temp = uint64(tokenParts[i].parent.Bytes[0])
		} else {
			status, uint64Temp = readASN1Uint64Payload(tokenParts[i].parent.Bytes)
			if status.Major != GSS_S_COMPLETE {
				status.Major = GSS_S_FAILURE
				status.Minor = TDGSS_ERR_INVALID_ARGUMENT
				status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
				return
			}
		}

		switch tokenParts[i].parent.Tag {
		case TOKENHEADER_VERSION_TAG:
			// Version
			v3TokenHdr.Version = byte(uint64Temp)
		case TOKENHEADER_MSGTYPE_TAG:
			// MsgType
			v3TokenHdr.MsgType = byte(uint64Temp)
		case TOKENHEADER_FLAGS_TAG:
			// Flags
			v3TokenHdr.Flags = byte(uint64Temp)
		case TOKENHEADER_FQOP_TAG:
			// FQop
			v3TokenHdr.FQop = byte(uint64Temp)
		case TOKENHEADER_ULMSGLENGTH_TAG:
			// UlMsgLength
			v3TokenHdr.UlMsgLength = uint32(uint64Temp)
		case TOKENHEADER_SEQNUMBER_TAG:
			// SeqNum
			v3TokenHdr.SeqNum = uint64Temp
		}
	}

	var buf bytes.Buffer
	err := binary.Write(&buf, binary.BigEndian, v3TokenHdr)
	if err != nil {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_ASN_TAG_INVALID
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}
	tokenHdr = buf.Bytes()

	status.Major = GSS_S_COMPLETE
	return
}

func tdatComputeKeys(logger TdgssLogger, algDH *algDHCtx, pValue []byte, gValue []byte, peerPublicKey []byte) (status TdgssStatus, abyPublicKey []byte) {

	// Store the P, G, and Server's public key in the algDH context
	statusAlg := algDH.storeKeys(logger, pValue, gValue)
	if statusAlg.Major != ALG_SUCCESS {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatComputeKeys Unable to store P, G, and Server public key!"))
		}
		status = statusAlg
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
		return
	}

	// Generate the client's private and public keys
	statusAlg = algDH.generateKeys(logger)
	if statusAlg.Major != ALG_SUCCESS {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatComputeKeys Unable to generate private and public keys!"))
		}
		status = statusAlg
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
		return
	}

	// Compute shared secret key.
	statusAlg = algDH.generateSecret(logger, peerPublicKey)
	if statusAlg.Major != ALG_SUCCESS {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatComputeKeys Unable to compute shared secret key!"))
		}
		status = statusAlg
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
		return
	}

	statusAlg, abyPublicKey = algDH.getPublicKey(logger)
	if statusAlg.Major != ALG_SUCCESS {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("tdatComputeKeys Unable to retrieve public key!"))
		}
		status = statusAlg
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(statusAlg.Err, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = GSS_S_COMPLETE
	return
}
