// Copyright 2021 by Teradata Corporation. All Rights Reserved.
package goteragss

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"fmt"
)

type algAESCtx struct {
	cipher   cipher.Block
	version  int
	key      []byte
	keylen   uint
	qopMode  uint
	iv       []byte
	auth_tag []byte
}

const AES uint = 2
const AES_VERSION int = 0x7890ABCD

/* Mode Constants */
const (
	MODE_MINIMUM_VALUE = 0
	CBC                = 1
	CFB                = 2
	ECB                = 3
	OFB                = 4
	GCM                = 5
	CCM                = 6
	CTR                = 7
	AEADGCM            = 8
	GCM96              = 9
	MODE_MAXIMUM_VALUE = 10
)

func (context *algAESCtx) aesInitializeCipher(logger TdgssLogger, qop algQOP, key []byte) (status TdgssStatus) {
	status.Major = ALG_SUCCESS
	var err error

	if logger.HasTraceLog() {
		logger.TraceLog("> enter algAESCtxAesInitializeCipher")
		defer func() { logger.TraceLog("< leave algAESCtxAesInitializeCipher") }()
	}

	if context.version != AES_VERSION {
		status.Minor = ALG_ERR_AES_CTX_NOT_VALID
		status.Major = ALG_FAILURE
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("aesInitializeCipher: algAESCtx %d version does not match expected %d.", context.version, AES_VERSION))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if qop.cipher != AES {
		status.Minor = ALG_ERR_AES_INVALID_QOP
		status.Major = ALG_FAILURE
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("aesInitializeCipher: QOP cipher is not AES: %d", qop.cipher))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	switch qop.keySize {
	case AES_128:
		context.keylen = 16
	case AES_192:
		context.keylen = 24
	case AES_256:
		context.keylen = 32
	default:
		status.Minor = ALG_ERR_AES_INVALID_KEY_LENGTH
		status.Major = ALG_FAILURE
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("aesInitializeCipher: QOP keySize (%d) not a legal value", qop.keySize))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if uint(len(key)) < context.keylen {
		status.Minor = ALG_ERR_AES_INSUFFCIENT_KEY
		status.Major = ALG_FAILURE
		if logger.HasDebugLog() {
			logger.DebugLog("aesInitializeCipher: length of key is less than context keylength")
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	keyBytes := make([]byte, context.keylen)
	copy(keyBytes, key[0:context.keylen])

	context.cipher, err = aes.NewCipher(keyBytes)
	if err != nil {
		status.Minor = ALG_ERR_AES_ERROR_ENCRYPT_KEY
		status.Major = ALG_FAILURE
		if logger.HasDebugLog() {
			logger.DebugLog("aesInitializeCipher: error when generating encryption and decryption keys")
		}
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}

	context.qopMode = qop.mode

	return
}

func (context *algAESCtx) aesEncrypt(logger TdgssLogger, source []byte, iv []byte) (status TdgssStatus, encrypted []byte, authTag []byte) {
	var err error
	var padded []byte
	status.Major = ALG_SUCCESS

	if logger.HasTraceLog() {
		logger.TraceLog("> enter algAESCtxAesEncrypt")
		defer func() { logger.TraceLog("< leave algAESCtxAesEncrypt") }()
	}

	if len(source) == 0 {
		status.Minor = ALG_ERR_AES_INVALID_PARAMETER
		status.Major = ALG_FAILURE
		if logger.HasDebugLog() {
			logger.DebugLog("aesEncrypt: Input is missing")
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if context.qopMode != ECB && len(iv) == 0 {
		if logger.HasDebugLog() {
			logger.DebugLog("aesEncrypt: IV is missing")
		}
		status.Minor = ALG_ERR_AES_INVALID_PARAMETER
		status.Major = ALG_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	/*For Legacy Pre 14.0 QOPs(cfb)*/
	if context.qopMode == CFB {
		if logger.HasDebugLog() {
			logger.DebugLog("CFB legacy mode is no longer supported")
		}
		status.Minor = ALG_ERR_AES_ERROR_INVALID_MODE
		status.Major = ALG_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if context.qopMode <= MODE_MINIMUM_VALUE || context.qopMode >= MODE_MAXIMUM_VALUE {
		status.Minor = ALG_ERR_AES_ERROR_INVALID_MODE
		status.Major = ALG_FAILURE
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("aesEncrypt: QOP mode value (%d) is out of bounds", context.qopMode))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if context.qopMode == CBC || context.qopMode == ECB || context.qopMode == OFB {
		status, padded = aesPKCS5Padding(logger, source, context.cipher.BlockSize())
	}

	switch context.qopMode {
	case OFB:
		/* LEGACY QOP for LDAP */
		ofb := cipher.NewOFB(context.cipher, iv)
		encryptedTemp := make([]byte, len(padded))
		ofb.XORKeyStream(encryptedTemp, padded)
		encrypted = encryptedTemp
	case GCM96:
		if len(iv) != 12 {
			status.Minor = ALG_ERR_AES_INVALID_PARAMETER
			status.Major = ALG_FAILURE
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("aesEncrypt: Length of iv (%d) doesn't conform to 96 byte requirement", len(iv)))
			}
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}
		fallthrough
	case AEADGCM:
		fallthrough
	case GCM:
		var aesgcm cipher.AEAD
		aesgcm, err = cipher.NewGCMWithNonceSize(context.cipher, len(iv))
		if err != nil {
			status.Minor = ALG_ERR_AES_INVALID_PARAMETER
			status.Major = ALG_FAILURE
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("aesEncrypt: Could not create new cipher wrapped with GCM, returned error %d", err))
			}
			status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
			return
		}

		temp := aesgcm.Seal(nil, iv, source, nil)
		encrypted = temp[:len(temp)-16]

		context.auth_tag = temp[len(temp)-16:]
		authTag = context.auth_tag
	case CBC:
		cbcEncrypter := cipher.NewCBCEncrypter(context.cipher, iv)
		encryptedTemp := make([]byte, len(padded))
		cbcEncrypter.CryptBlocks(encryptedTemp, padded)
		encrypted = encryptedTemp
	case CTR:
		encryptedTemp := make([]byte, len(source))
		stream := cipher.NewCTR(context.cipher, iv)
		stream.XORKeyStream(encryptedTemp, source)
		encrypted = encryptedTemp
	case ECB:
		bs := context.cipher.BlockSize()
		if len(padded)%bs != 0 {
			if logger.HasDebugLog() {
				logger.DebugLog("aesEncrypt: ECB input length required to be multiple of the blocksize")
			}
			status.Minor = ALG_ERR_AES_INVALID_DATA_LENGTH
			status.Major = ALG_FAILURE
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}
		encryptedTemp := make([]byte, len(padded))
		for i := 0; i < len(padded); i = i + bs {
			context.cipher.Encrypt(encryptedTemp[i:i+bs], padded[i:i+bs])
		}
		encrypted = encryptedTemp
	case CCM:
		status.Minor = ALG_ERR_AES_ERROR_INVALID_MODE
		status.Major = ALG_FAILURE
		if logger.HasDebugLog() {
			logger.DebugLog("Not currently supported")
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	return
}

func (context *algAESCtx) aesDecrypt(logger TdgssLogger, encrypted []byte, iv []byte) (status TdgssStatus, decrypted []byte) {
	var err error
	status.Major = ALG_SUCCESS

	if logger.HasTraceLog() {
		logger.TraceLog("> enter algAESCtxAesDecrypt")
		defer func() { logger.TraceLog("< leave algAESCtxAesDecrypt") }()
	}

	if len(encrypted) == 0 {
		status.Minor = ALG_ERR_AES_INVALID_PARAMETER
		status.Major = ALG_FAILURE
		if logger.HasDebugLog() {
			logger.DebugLog("aesDecrypt: Encrypted input is missing")
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if context.qopMode != ECB && len(iv) == 0 {
		if logger.HasDebugLog() {
			logger.DebugLog("aesEncrypt: IV is missing")
		}
		status.Minor = ALG_ERR_AES_INVALID_PARAMETER
		status.Major = ALG_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	/*For Legacy Pre 14.0 QOPs(cfb)*/
	if context.qopMode == CFB {
		if logger.HasDebugLog() {
			logger.DebugLog("CFB legacy mode is no longer supported")
		}
		status.Major = ALG_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if context.qopMode <= MODE_MINIMUM_VALUE || context.qopMode >= MODE_MAXIMUM_VALUE {
		status.Minor = ALG_ERR_AES_ERROR_INVALID_MODE
		status.Major = ALG_FAILURE
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("aesDecrypt: QOP mode value (%d) is out of bounds", context.qopMode))
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	switch context.qopMode {
	case OFB:
		/* LEGACY QOP for LDAP */
		ofb := cipher.NewOFB(context.cipher, iv)
		decryptedTemp := make([]byte, len(encrypted))
		ofb.XORKeyStream(decryptedTemp, encrypted)
		decrypted = decryptedTemp
	case GCM96:
		if len(iv) != 12 {
			status.Minor = ALG_ERR_AES_INVALID_PARAMETER
			status.Major = ALG_FAILURE
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("aesDecrypt: Length of iv (%d) doesn't conform to 96 byte requirement", len(iv)))
			}
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}
		fallthrough
	case AEADGCM:
		fallthrough
	case GCM:
		var aesgcm cipher.AEAD
		aesgcm, err = cipher.NewGCMWithNonceSize(context.cipher, len(iv))
		if err != nil {
			status.Minor = ALG_ERR_AES_INVALID_PARAMETER
			status.Major = ALG_FAILURE
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("aesDecrypt: Could not create GCM context, returned error %v", err))
			}
			status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
			return
		}

		decrypted, err = aesgcm.Open(nil, iv, encrypted, nil)
		if err != nil {
			status.Minor = ALG_ERR_SHA_INVALID_DIGEST
			status.Major = ALG_FAILURE
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("aesDecrypt: Could not decrypt encrypted bytes, returned error %v", err))
			}
			status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
			return
		}
	case CBC:
		cbcDecrypter := cipher.NewCBCDecrypter(context.cipher, iv)
		decryptedTemp := make([]byte, len(encrypted))
		cbcDecrypter.CryptBlocks(decryptedTemp, encrypted)
		decrypted = decryptedTemp
	case CTR:
		decryptedTemp := make([]byte, len(encrypted))
		stream := cipher.NewCTR(context.cipher, iv)
		stream.XORKeyStream(decryptedTemp, encrypted)
		decrypted = decryptedTemp
	case ECB:
		bs := context.cipher.BlockSize()
		if len(encrypted)%bs != 0 {
			if logger.HasDebugLog() {
				logger.DebugLog("aesEncrypt: ECB encrypted length must be multiple of the blocksize")
			}
			status.Minor = ALG_ERR_AES_INVALID_DATA_LENGTH
			status.Major = ALG_FAILURE
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}
		decryptedTemp := make([]byte, len(encrypted))
		for i := 0; i < len(encrypted); i = i + bs {
			context.cipher.Decrypt(decryptedTemp[i:i+bs], encrypted[i:i+bs])
		}
		decrypted = decryptedTemp
	case CCM:
		status.Minor = ALG_ERR_AES_ERROR_INVALID_MODE
		status.Major = ALG_FAILURE
		if logger.HasDebugLog() {
			logger.DebugLog("Not currently supported")
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if context.qopMode == CBC || context.qopMode == ECB || context.qopMode == OFB {
		/* Trim PKCS5Padding */
		status, decrypted = aesPKCS5Trimming(logger, decrypted)
	}

	return
}

func (context *algAESCtx) aesReturnLength(logger TdgssLogger, plainDataLength uint) (status TdgssStatus, encryptedLength uint) {

	switch context.qopMode {
	case CBC:
		fallthrough
	case OFB:
		fallthrough
	case ECB:
		pad := plainDataLength % uint(context.cipher.BlockSize())
		encryptedLength = plainDataLength + uint(context.cipher.BlockSize()) - pad
		status.Major = ALG_SUCCESS
		return
	case CTR:
		fallthrough
	case GCM96:
		fallthrough
	case AEADGCM:
		fallthrough
	case GCM:
		encryptedLength = plainDataLength
		status.Major = ALG_SUCCESS
		return
	case CCM:
		status.Minor = ALG_ERR_AES_ERROR_INVALID_MODE
		status.Major = ALG_FAILURE
		if logger.HasDebugLog() {
			logger.DebugLog("Not currently supported")
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	default:
		status.Minor = ALG_ERR_AES_ERROR_INVALID_MODE
		status.Major = ALG_FAILURE
		if logger.HasDebugLog() {
			logger.DebugLog("Block mode not supported")
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
}

func aesPKCS5Padding(logger TdgssLogger, unpadded []byte, blockSize int) (status TdgssStatus, padded []byte) {
	padding := blockSize - len(unpadded)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	padded = append(unpadded, padtext...)
	status.Major = ALG_SUCCESS
	return
}

func aesPKCS5Trimming(logger TdgssLogger, padded []byte) (status TdgssStatus, unpadded []byte) {
	padding := padded[len(padded)-1]
	unpadded = padded[:len(padded)-int(padding)]
	status.Major = ALG_SUCCESS
	return
}
