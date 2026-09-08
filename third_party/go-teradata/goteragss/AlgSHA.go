// Copyright 2021 by Teradata Corporation. All Rights Reserved.

package goteragss

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/hmac"
	"hash"
	"fmt"
	"bytes"
)

const (
	SHA1_VERSION         = 0xABCDEF90
	SHA256_VERSION       = 0xABCDEF96
	SHA512_VERSION       = 0xABCDEF96
)

// TODO: Temporary error returns; remove/replace once a
// more permanent solution has been implemented.
const (
	// Major statuses
	ALG_SUCCESS = 1
	ALG_FAILURE = 0
)

type algSHACtx struct {
	md               []byte
	version          uint
	key              []byte
	keyLen           uint
	digest           uint
}

// validateAlgSHACtx checks that this algSHACtx's version and digest are valid.
func (ctx *algSHACtx) validateAlgSHACtx(logger TdgssLogger) (status TdgssStatus) {
	if ctx.version != SHA1_VERSION && ctx.version != SHA256_VERSION && ctx.version != SHA512_VERSION {
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_SHA_CTX_NOT_VALID
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if ctx.digest != TDGSS_ALG_NAME_SHA1 && ctx.digest != TDGSS_ALG_NAME_SHA256 && ctx.digest != TDGSS_ALG_NAME_SHA512 {
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_SHA_INVALID_DIGEST
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = ALG_SUCCESS
	status.Minor = ALG_S_SUCCESS
	return
} // End validateAlgSHACtx

// setKeyInDigest takes the key passed in as input and sets it in the algSHACtx ctx.
func (ctx *algSHACtx) setKeyInDigestCtx(logger TdgssLogger, algQop algQOP, key []byte) (status TdgssStatus, keyLen uint32) {
	// Ensure that our algSHACtx is valid.
	status = ctx.validateAlgSHACtx(logger)
	if status.Major != ALG_SUCCESS {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("setKeyInDigestCtx: validateAlgSHACtx failed: %v", status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	// The key we receive from the caller must be at least 16 bytes.
	if len(key) < 16 {
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_DIGEST_INSUFFCIENT_MIN_KEY
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Set the keyLen for this algSHACtx.
	switch algQop.keySize {
	// 16 in byte format
	case AES_128:
		ctx.keyLen = 128/8
	// 24 in byte format
	case AES_192:
		ctx.keyLen = 192/8
	// 32 in byte format
	case AES_256:
		ctx.keyLen = 256/8
	default:
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_DIGEST_INVALID_KEY_LENGTH
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Verify that the size of the input key is at least
	// the size required by ctx.keyLen.
	if uint(len(key)) < ctx.keyLen {
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_DIGEST_INSUFFCIENT_KEY
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Set the key into this algSHACtx.
	// The built-in copy() function copies min(len(dst), len(src)) elements,
	// ensuring that the ctx.key will contain a ctx.keyLen length of bytes.
	ctx.key = make([]byte, ctx.keyLen)
	numCopied := copy(ctx.key, key)
	if uint(numCopied) != ctx.keyLen {
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_DIGEST_INSUFFCIENT_KEY
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	keyLen = uint32(ctx.keyLen)
	status.Major = ALG_SUCCESS
	status.Minor = ALG_S_SUCCESS
	return
} // End setKeyInDigest

// getHashType returns the hash.Hash type associated with this context's digest.
func (ctx *algSHACtx) getHashType(logger TdgssLogger) (status TdgssStatus, hashType func() hash.Hash) {
	switch ctx.digest {
	case TDGSS_ALG_NAME_SHA1:
		hashType = sha1.New
	case TDGSS_ALG_NAME_SHA256:
		hashType = sha256.New
	case TDGSS_ALG_NAME_SHA512:
		hashType = sha512.New
	default:
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_SHA_INVALID_DIGEST
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = ALG_SUCCESS
	status.Minor = ALG_S_SUCCESS
	return
} // End getDigestType

// getDigestLength returns the size (in bytes) of the context's digest.
func (ctx *algSHACtx) getDigestLength(logger TdgssLogger) (status TdgssStatus, digestLength uint32) {
	switch ctx.digest {
	case TDGSS_ALG_NAME_SHA1:
		// sha1.Size == 20 bytes.
		digestLength = sha1.Size
	case TDGSS_ALG_NAME_SHA256:
		// sha256.Size == 32 bytes.
		digestLength = sha256.Size
	case TDGSS_ALG_NAME_SHA512:
		// sha512.Size == 64 bytes.
		digestLength = sha512.Size
	default:
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_SHA_INVALID_DIGEST
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = ALG_SUCCESS
	status.Minor = ALG_S_SUCCESS
	return
} // End getDigestLength

// generateHMAC computes an HMAC for input abyData using the key stored in this algSHACtx. The resulting digest is stored in
// the output abyDigest.
func (ctx *algSHACtx) generateHMAC(logger TdgssLogger, abyData []byte) (status TdgssStatus, abyDigest []byte) {
	// Ensure that our algSHACtx is valid.
	status = ctx.validateAlgSHACtx(logger)
	if status.Major != ALG_SUCCESS {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("generateHMAC: validateAlgSHACtx failed: %v", status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	// We can't continue if we don't have any data.
	if len(abyData) == 0 {
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_SHA_INVALID_PARAMETER
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// This context must have a valid key.
	if ctx.keyLen <= 0 {
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_DIGEST_KEY_NOT_INTIALIZED
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Get the hash type (SHA-1, SHA-256, or SHA-512) associated with the digest
	// stored in this context. We'll then use the hash type as an argument
	// to hmac.New() further down.
	status, hashType := ctx.getHashType(logger)
	if status.Major != ALG_SUCCESS {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("generateHMAC: getHashType failed: %v", status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	// Get the length of the digest stored in this context.
	status, digestLen := ctx.getDigestLength(logger)
	if status.Major != ALG_SUCCESS {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("generateHMAC: getDigestLength failed: %v", status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	// Generate a new HMAC using the
	// hash function associated with
	// this context and this context's
	// key.
	h := hmac.New(hashType, ctx.key)

	// Write the data to the HMAC.
	h.Write(abyData)

	// Get the unencoded digest.
	ctx.md = h.Sum(nil)

	// Copy the unencoded digest into
	// output abyDigest.
	abyDigest = make([]byte, digestLen)
	numCopied := copy(abyDigest, ctx.md)

	// Ensure that we've copied digestLen bytes.
	if uint32(numCopied) != digestLen {
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_HMAC_GENERATE_FAILED
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = ALG_SUCCESS
	status.Minor = ALG_S_SUCCESS
	return
} // End generateHMAC

// verifyHMAC confirms that the hash stored in the abyDigest is valid by recomputing the hash
// using the same key. We'll use the crypto/hmac package's Equal function in order to safely
// compare the stored hash with the recomputed hash.
func (ctx *algSHACtx) verifyHMAC(logger TdgssLogger, abyData []byte, abyDigest []byte) (status TdgssStatus) {
	// Ensure that our algSHACtx is valid.
	status = ctx.validateAlgSHACtx(logger)
	if status.Major != ALG_SUCCESS {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("verifyHMAC: validateAlgSHACtx failed: %v", status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	// This context must have a valid key.
	if ctx.keyLen <= 0 {
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_DIGEST_KEY_NOT_INTIALIZED
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// We can't continue if we don't have any data.
	if len(abyData) == 0 || len(abyDigest) == 0 {
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_SHA_INVALID_PARAMETER
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Get the hash type (SHA-1, SHA-256, or SHA-512) associated with
	// the digest stored in this context. We'll then use the hash type
	// as an argument to hmac.New() further down.
	status, hashType := ctx.getHashType(logger)
	if status.Major != ALG_SUCCESS {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("verifyHMAC: getHashType failed: %v", status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	// Verify the hash by recomputing it using the same key.
	h := hmac.New(hashType, ctx.key)
	h.Write(abyData)
	expected := h.Sum(nil)
	result := hmac.Equal(abyDigest, expected)

	// It's an error if we don't have the expected hash.
	if !result {
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_SHA_INVALID_DIGEST
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = ALG_SUCCESS
	status.Minor = ALG_S_SUCCESS
	return
} // End verifyHMAC

// generateDigest computes a digest for input abyData. The resulting digest is stored in the output abyDigest.
func (ctx *algSHACtx) generateDigest(logger TdgssLogger, abyData []byte) (status TdgssStatus, abyDigest []byte) {
	// Ensure that our algSHACtx is valid.
	status = ctx.validateAlgSHACtx(logger)
	if status.Major != ALG_SUCCESS {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("generateDigest: validateAlgSHACtx failed: %v", status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	// We can't continue if we don't have any data.
	if len(abyData) == 0 {
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_SHA_INVALID_PARAMETER
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Get the hash function (SHA-1, SHA-256, or SHA-512) associated with the digest
	// stored in this context.
	status, hashConstructor := ctx.getHashType(logger)
	if status.Major != ALG_SUCCESS {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("generateDigest: getHashType failed: %v", status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	// Get the length of the digest stored in this context.
	status, digestLen := ctx.getDigestLength(logger)
	if status.Major != ALG_SUCCESS {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("generateDigest: getDigestLength failed: %v", status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	// Generate a new digest using the
	// hash function associated with
	// this context and this context's
	// key.
	// Write the data.
	h := hashConstructor()
	h.Write(abyData)

	// Get the unencoded digest.
	ctx.md = h.Sum(nil)

	// Copy the unencoded digest into
	// output abyDigest.
	abyDigest = make([]byte, digestLen)
	numCopied := copy(abyDigest, ctx.md)

	// Ensure that we've copied digestLen bytes.
	if uint32(numCopied) != digestLen {
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_HMAC_GENERATE_FAILED
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = ALG_SUCCESS
	status.Minor = ALG_S_SUCCESS
	return
} // End generateDigest

// verifyDigest confirms that the hash stored in the abyDigest is valid by recomputing the hash
// and compare the stored hash with the recomputed hash.
func (ctx *algSHACtx) verifyDigest(logger TdgssLogger, abyData []byte, abyDigest []byte) (status TdgssStatus) {
	// Ensure that our algSHACtx is valid.
	status = ctx.validateAlgSHACtx(logger)
	if status.Major != ALG_SUCCESS {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("verifyDigest: validateAlgSHACtx failed: %v", status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	// We can't continue if we don't have any data.
	if len(abyData) == 0 || len(abyDigest) == 0 {
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_SHA_INVALID_PARAMETER
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Get the hash function (SHA-1, SHA-256, or SHA-512) associated with
	// the digest stored in this context.
	status, hashConstructor := ctx.getHashType(logger)
	if status.Major != ALG_SUCCESS {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("verifyDigest: getHashType failed: %v", status))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	// Verify the hash by recomputing it.
	h := hashConstructor()
	h.Write(abyData)
	abyExpected := h.Sum(nil)
	result := bytes.Equal(abyDigest, abyExpected)

	// It's an error if we don't have the expected hash.
	if !result {
		status.Major = ALG_FAILURE
		status.Minor = ALG_ERR_SHA_INVALID_DIGEST
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = ALG_SUCCESS
	status.Minor = ALG_S_SUCCESS
	return
} // End verifyDigest
