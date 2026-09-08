// Copyright 2021 by Teradata Corporation. All Rights Reserved.

package goteragss

import (
	"hash"
)

type algDigest struct {
	data      []byte
	abyDigest []byte
	digest    uint
}

type algCipherFn interface {
	aesInitializeCipher(logger TdgssLogger, qop algQOP, key []byte) (status TdgssStatus)
	aesEncrypt(logger TdgssLogger, source []byte, iv []byte) (status TdgssStatus, encrypted []byte, authTag []byte)
	aesDecrypt(logger TdgssLogger, encrypted []byte, iv []byte) (status TdgssStatus, decrypted []byte)
	aesReturnLength(logger TdgssLogger, plainDataLength uint) (status TdgssStatus, encryptedLength uint)
}

type algDigestFn interface {
	setKeyInDigestCtx(logger TdgssLogger, algQop algQOP, key []byte) (status TdgssStatus, keyLen uint32)
	getHashType(logger TdgssLogger) (status TdgssStatus, hashType func() hash.Hash)
	getDigestLength(logger TdgssLogger) (status TdgssStatus, digestLength uint32)
	generateHMAC(logger TdgssLogger, abyData []byte) (status TdgssStatus, abyDigest []byte)
	verifyHMAC(logger TdgssLogger, abyData []byte, abyDigest []byte) (status TdgssStatus)
	generateDigest(logger TdgssLogger, abyData []byte) (status TdgssStatus, abyDigest []byte)
	verifyDigest(logger TdgssLogger, abyData []byte, abyDigest []byte) (status TdgssStatus)
}

var _ algCipherFn = &algAESCtx{} // implements algCipherFn

var _ algDigestFn = &algSHACtx{} // implements algDigestFn

// New Cipher functions
func newCipher(algQop *algQOP) (status TdgssStatus) {

	if algQop == nil {
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_AES_INVALID_PARAMETER
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	switch algQop.cipher {
	case TDGSS_ALG_NAME_AES:
		algQop.cipherCtx = &algAESCtx{version: AES_VERSION}
	default:
		algQop.cipherCtx = nil
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_AES_INVALID_QOP
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = ALG_SUCCESS
	status.Minor = ALG_S_SUCCESS
	return
}

// New Digest functions
func newDigest(algQop *algQOP) (status TdgssStatus) {

	status.Major = ALG_FAILURE
	status.Minor = ALG_ERR_AES_CTX_NOT_VALID

	if algQop == nil {
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_AES_INVALID_PARAMETER
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	switch algQop.digest {
	case TDGSS_ALG_NAME_SHA1:
		algQop.digestCtx = &algSHACtx{
			version: SHA1_VERSION,
			digest:  algQop.digest,
		}
	case TDGSS_ALG_NAME_SHA256:
		algQop.digestCtx = &algSHACtx{
			version: SHA256_VERSION,
			digest:  algQop.digest,
		}
	case TDGSS_ALG_NAME_SHA512:
		algQop.digestCtx = &algSHACtx{
			version: SHA512_VERSION,
			digest:  algQop.digest,
		}
	default:
		algQop.digestCtx = nil
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_AES_INVALID_QOP
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = ALG_SUCCESS
	status.Minor = ALG_S_SUCCESS
	return
}
