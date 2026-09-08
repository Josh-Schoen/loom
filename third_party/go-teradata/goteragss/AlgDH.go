// Copyright 2021 by Teradata Corporation. All Rights Reserved.
package goteragss

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
)

type algDHCtx struct {
	privateKey *big.Int
	publicKey  *big.Int
	P          *big.Int
	G          *big.Int
	secretKey  *big.Int
	pubKeyLen  int
	secKeyLen  int
	iVersion   int /* Version                     */
	iState     int /* State of structure          */
}

func (context *algDHCtx) generateKeys(logger TdgssLogger) (status TdgssStatus) {
	var err error
	status.Major = ALG_FAILURE

	if logger.HasTraceLog() {
		logger.TraceLog("> enter algDHGenerateKey")
		defer func() { logger.TraceLog("< leave algDHGenerateKey") }()
	}

	if context.P == nil {
		status.Minor = ALG_ERR_DH_P_KEY_NOT_SET
		if logger.HasDebugLog() {
			logger.DebugLog("generateKeys: P value is missing from DH context.")
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if context.G == nil {
		status.Minor = ALG_ERR_DH_G_KEY_NOT_SET
		if logger.HasDebugLog() {
			logger.DebugLog("generateKeys: G value is missing from DH context")
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if context.privateKey == nil {
		context.privateKey, err = rand.Int(rand.Reader, context.P)
		if err != nil {
			status.Minor = ALG_ERR_DH_CANNOT_COMPUTE_KEY
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("Private key not able to be generated: %d", err))
			}
			status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
			return
		}
	}

	context.publicKey = new(big.Int).Exp(context.G, context.privateKey, context.P)
	status.Major = ALG_SUCCESS
	status.Minor = ALG_S_SUCCESS
	return
}

func (context *algDHCtx) storeKeys(logger TdgssLogger, pValue []byte, gValue []byte) (status TdgssStatus) {

	status.Major = ALG_FAILURE

	if logger.HasTraceLog() {
		logger.TraceLog("> enter algDHCtx storeKeys")
		defer func() { logger.TraceLog("< leave algDHCtx storeKeys") }()
	}

	if len(pValue) == 0 || len(gValue) == 0 {
		status.Minor = ALG_ERR_DH_ERROR_INVALID_PARAMETER
		if logger.HasDebugLog() {
			logger.DebugLog("storeKeys: P or G value is missing")
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	context.P = new(big.Int).SetBytes(pValue)
	context.G = new(big.Int).SetBytes(gValue)

	context.pubKeyLen = len(pValue)
	context.secKeyLen = context.pubKeyLen

	status.Major = ALG_SUCCESS
	status.Minor = ALG_S_SUCCESS
	return
}

func (context *algDHCtx) generateSecret(logger TdgssLogger, peerPublicKey []byte) (status TdgssStatus) {
	status.Major = ALG_FAILURE

	if logger.HasTraceLog() {
		logger.TraceLog("> enter algDHCtx generateSecret")
		defer func() { logger.TraceLog("< leave algDHCtx generateSecret") }()
	}

	if context.P == nil {
		status.Minor = ALG_ERR_DH_P_KEY_NOT_SET
		if logger.HasDebugLog() {
			logger.DebugLog("generateKeys: P value is missing from DH context.")
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if context.G == nil {
		status.Minor = ALG_ERR_DH_G_KEY_NOT_SET
		if logger.HasDebugLog() {
			logger.DebugLog("generateKeys: G value is missing from DH context")
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if len(peerPublicKey) == 0 {
		return
	}

	peerPublicKeyInt := new(big.Int).SetBytes(peerPublicKey)
	context.secretKey = new(big.Int).Exp(peerPublicKeyInt, context.privateKey, context.P)
	status.Major = ALG_SUCCESS
	status.Minor = ALG_S_SUCCESS
	return
}

func (context *algDHCtx) getPublicKey(logger TdgssLogger) (status TdgssStatus, publicKey []byte) {
	status.Major = ALG_FAILURE

	if context.publicKey == nil {
		status.Minor = ALG_ERR_DH_PUB_KEY_NOT_GEN
		if logger.HasDebugLog() {
			logger.DebugLog("getPublicKey: publicKey is missing from DH context")
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	if logger.HasDebugLog() {
		logger.DebugLog(fmt.Sprintf("getPublicKey before len=%v\n%v", len(context.publicKey.Bytes()), hex.Dump(context.publicKey.Bytes())))
	}
	publicKey = make([]byte, context.pubKeyLen)
	copy(publicKey[context.pubKeyLen-len(context.publicKey.Bytes()):], context.publicKey.Bytes())
	if logger.HasDebugLog() {
		logger.DebugLog(fmt.Sprintf("getPublicKey len=%v\n%v", len(publicKey), hex.Dump(publicKey)))
	}
	status.Major = ALG_SUCCESS
	status.Minor = ALG_S_SUCCESS
	return
}

func (context *algDHCtx) getSecretKey(logger TdgssLogger) (status TdgssStatus, secretKey []byte) {
	status.Major = ALG_FAILURE

	if context.secretKey == nil {
		status.Minor = ALG_ERR_DH_CANNOT_GET_MP_SEC
		if logger.HasDebugLog() {
			logger.DebugLog("getSecretKey: secretKey is missing from DH context")
		}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	if logger.HasDebugLog() {
		logger.DebugLog(fmt.Sprintf("getSecretKey before len=%v\n%v", len(context.secretKey.Bytes()), hex.Dump(context.secretKey.Bytes())))
	}
	secretKey = make([]byte, context.secKeyLen)
	copy(secretKey, context.secretKey.Bytes())
	if logger.HasDebugLog() {
		logger.DebugLog(fmt.Sprintf("getSecretKey len=%v\n%v", len(secretKey), hex.Dump(secretKey)))
	}
	status.Major = ALG_SUCCESS
	status.Minor = ALG_S_SUCCESS
	return
}
