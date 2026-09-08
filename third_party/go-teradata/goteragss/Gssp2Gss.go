// Copyright 2022 by Teradata Corporation. All Rights Reserved.

//go:build !windows && !wasm && krb5

package goteragss

// #cgo LDFLAGS: -ldl
// #include <stdlib.h>
// #include <dlfcn.h>
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"
	"sync"
	"strings"
	"encoding/hex"
)

const (
	GSS_CTX_NEW = iota
	GSS_CTX_SETUP_PARTIAL
	GSS_CTX_SETUP_COMPLETE_AND_CONTINUE_FOR_SERVER_RESPONSE
	GSS_CTX_SETUP_COMPLETE_AND_CONTINUE_FOR_CLIENT_LOGDATA
	GSS_CTX_SETUP_COMPLETE
)

const CAP_LOGDATA = 0x2

type gssSecContext struct {
	mechInfo          *mechanismInfo
	logger            TdgssLogger
	cI                confInteg /* See TdatUtil.go */
	hostType          byte      /* GSSSTYLE or SSPISTYLE */
	peerType          byte      /* GSSSTYLE or SSPISTYLE */
	peerCapabilities  uint32
	completionStatus  uint8
	wrappedSecretKey  []byte
	userProperties    []byte
	haveLogdata       bool
	extraLogdata      string
	gssCred           *tdgssCredId
	gssCtx            *tdgssCtxId
}

var _ gssp = &gssSecContext{} // implements gssp

var configInfoStatus string
var g_bConfigured bool
var g_mutexGssConfigure sync.Mutex

func newKrb5Mech(loggerArg TdgssLogger, mi *mechanismInfo) gssp {

	return &gssSecContext{
		logger:           loggerArg,
		mechInfo:         mi,
		completionStatus: GSS_CTX_NEW,
		cI: confInteg{
			algDH:    algDHCtx{},
			conf:     mi.mechProperties.ConfidentialityDesired,
			integ:    mi.mechProperties.IntegrityDesired,
			replay:   mi.mechProperties.ReplayDetection,
			sequence: mi.mechProperties.OutOfSequenceDetection,
		},
	}
}

// String interface
func (secCtx *gssSecContext) String() string { return fmt.Sprintf("gssSecContext: %p", secCtx) }

// gssp interface
func (secCtx *gssSecContext) gsspConfigure() (status TdgssStatus) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspConfigure %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspConfigure %v", secCtx)) }()
	}
	status.Major = GSS_S_COMPLETE
	status.Minor = GSSP2GSS_S_COMPLETE

	g_mutexGssConfigure.Lock () // enter critical section

	if !g_bConfigured {

		/* Check via the following priority for paths to the MIT Kerberos Library:
			1) Environment variable
			2) Configuration file
			3) Canned name
		Use NULL if the above fail
		*/

		dlopenFlag := (C.RTLD_LOCAL | C.RTLD_NOW)
		var pLibHandle unsafe.Pointer

		/* Check for a valid environment variable */
		krb5libpath := os.Getenv("TDGSS_KRB5_KRB5LIB")
		if krb5libpath != "" {
			krb5libpathC := C.CString(krb5libpath)
			pLibHandle = C.dlopen(krb5libpathC, C.int(dlopenFlag))
			C.free(unsafe.Pointer(krb5libpathC))
		}

		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("gsspConfigure %v environment variable TDGSS_KRB5_KRB5LIB=%v -> pLibHandle=%v krb5libpath=%v", secCtx, krb5libpath, pLibHandle, krb5libpath))
		}

		// Choose proper krb5 Library Path based on OS
		krb5LibDefaultPaths := [] string {}
		if runtime.GOOS == "darwin" {
			krb5LibDefaultPaths = [] string { "/usr/lib/libgssapi_krb5.dylib" }
		} else if runtime.GOOS == "linux" {
			krb5LibDefaultPaths = [] string { "/usr/lib64/libgssapi_krb5.so", "/usr/lib64/libgssapi_krb5.so.2" }
		}

		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("gsspConfigure %v krb5LibDefaultPaths len=%v", secCtx, len(krb5LibDefaultPaths)))
		}

		/* No valid path, now check canned name */
		for i := 0 ; pLibHandle == nil && i < len(krb5LibDefaultPaths) ; i++ {
			krb5libpathC := C.CString(krb5LibDefaultPaths[i])
			pLibHandle = C.dlopen(krb5libpathC, C.int(dlopenFlag))
			C.free(unsafe.Pointer(krb5libpathC))
			if pLibHandle != nil {
				krb5libpath = krb5LibDefaultPaths[i]
			}

			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspConfigure %v krb5LibDefaultPaths[%v]=%v -> pLibHandle=%v krb5libpath=%v", secCtx, i, krb5LibDefaultPaths[i], pLibHandle, krb5libpath))
			}
		}

		/* Fall-through case if the above path checks fail; we dlopen() NULL to access the global symbol table*/
		if pLibHandle == nil {
			pLibHandle = C.dlopen(nil, C.int(dlopenFlag))

			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gsspConfigure %v global symbol table -> pLibHandle=%v", secCtx, pLibHandle))
			}
		} else {
			status.Err = tdgssError(status.Err, "ConfigureWarning: gssp2gss: Loaded Kerberos provider library")
		}

		/* It's an error if we couldn't open one Library file */
		if pLibHandle == nil {
			pszDlerrorMsg := C.dlerror()
			if pszDlerrorMsg != nil {
				status.Err = tdgssError(status.Err, fmt.Sprintf("ConfigureWarning: gssp2gss: %s", C.GoString(pszDlerrorMsg)))
			} else {
				status.Err = tdgssError(status.Err, "ConfigureWarning: gssp2gss: MIT KRB5 Library dlopen failed")
			}
			status.Major = GSS_S_FAILURE
			status.Minor = TDGSS_ERR_DISABLE_MECH
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		/***************************************************************
		*  Now get the GSS KRB5 function pointers.
		***************************************************************/
		initialize_function_pointers(secCtx.logger, pLibHandle)

		g_bConfigured = true
	}

	g_mutexGssConfigure.Unlock () // exit critical section

	if (secCtx.mechInfo.mechProperties.LdapSystemFQDN == "") && !secCtx.mechInfo.mechProperties.UseLdapConfig && secCtx.mechInfo.mechProperties.AuthorizationSupported {
		if len(configInfoStatus) == 0 {
			status.Major = GSS_S_FAILURE
			status.Minor = TDGSS_ERR_DISABLE_MECH
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}
		status.Err = tdgssError(status.Err, "ConfigureWarning: gssp2gss: KRB5 Mechanism Disabled:(TDGSS config file: System FQDN is missing.)")
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_DISABLE_MECH
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}
	if !secCtx.mechInfo.mechProperties.LdapNeedcanon && !secCtx.mechInfo.mechProperties.UseLdapConfig && secCtx.mechInfo.mechProperties.AuthorizationSupported {
		if len(configInfoStatus) == 0 {
			status.Major = GSS_S_FAILURE
			status.Minor = TDGSS_ERR_DISABLE_MECH
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}
		minorStatusString := gMinorStatusCodes[GSSP2GSS_LDAP_ERR_CFG_NO_CANON]
		status.Err = tdgssError(status.Err, "ConfigureWarning: gssp2gss: KRB5 Mechanism Disabled:(" + minorStatusString + ")")
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_DISABLE_MECH
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	return
}

func (secCtx *gssSecContext) gsspInitSecContext(targetName string, in []byte) (status TdgssStatus, retFlags uint32, timeRec uint32, needTrailer bool, out []byte) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspInitSecContext %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspInitSecContext %v", secCtx)) }()
	}

	if secCtx.logger.HasDebugLog() {
		secCtx.logger.DebugLog(fmt.Sprintf("Target Name in gsspInitSecContext %s", targetName))
	}

	if secCtx.completionStatus == GSS_CTX_SETUP_COMPLETE  && len(in) == 0 {
		status.Major = GSS_S_COMPLETE
		status.Minor = GSSP2GSS_S_COMPLETE
		out = nil
		return
	}

	bNewConversation := false
	if secCtx.gssCtx == nil {

		bNewConversation = true
		needTrailer = true

		if len(in) !=0 {
			status.Major = GSS_S_DEFECTIVE_TOKEN
			status.Minor = GSSP2GSS_ERR_INVALID_TOKEN
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		// Allocate the memory for the new security context on first call
		secCtx.gssCtx = NewTdgssCtxId()

		secCtx.completionStatus = GSS_CTX_NEW
	}

	bHaveLogdata := len(secCtx.extraLogdata) > 0
	reqFlags := secCtx.mechInfo.desiredServices
	reqFlags |= GSS_C_MUTUAL_FLAG

	var inputToken []byte

	var peerTokenHdr gssV2TokenHeader
	if !bNewConversation {
		if len(in) == 0 {
			status.Major = GSS_S_DEFECTIVE_TOKEN
			status.Minor = GSSP2GSS_ERR_INVALID_TOKEN
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		// Retrieve token header.
		status, peerTokenHdr = gssIntRetrieveHeader(secCtx.logger, in)
		if status.Major != GSS_S_COMPLETE {
			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}

		if peerTokenHdr.ByteVar == CONFMSG && secCtx.completionStatus == GSS_CTX_SETUP_PARTIAL {
			secCtx.completionStatus = GSS_CTX_SETUP_COMPLETE
			status.Major = GSS_S_COMPLETE
			status.Minor = GSSP2GSS_S_COMPLETE
			out = nil
			return
		}

		inputToken = in[GSS_TOKEN_HEADER_LENGTH:]
		secCtx.completionStatus = GSS_CTX_SETUP_PARTIAL
	}

	var outputToken []byte
	status, outputToken = gssIntInitSecContext(secCtx.logger, secCtx.gssCred, secCtx.gssCtx, targetName, reqFlags, 0, inputToken)

	if status.Major != GSS_S_COMPLETE  && status.Major != GSS_S_CONTINUE_NEEDED {
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("gssIntInitSecContext returned an error: Major=0x%x Minor=0x%x", status.Major, status.Minor))
		}
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	var tokenHdr []byte
	var msgSeq byte
	var fCapabilities uint32 = NOTAPPLICABLE

	if bNewConversation {
		msgSeq = INITMSG
	} else {
		msgSeq = CONTMSG
	}

	if bNewConversation {
		fCapabilities = CAP_LOGDATA
	}

	secCtx.completionStatus = GSS_CTX_SETUP_PARTIAL

	if len(outputToken) > 0 {

		var hdrStatus TdgssStatus
		hdrStatus, tokenHdr = gssIntBuildTokenHdr(secCtx.logger, INITCTXMSG, msgSeq, NOTAPPLICABLE, uint32(len(outputToken)), fCapabilities)
		if hdrStatus.Major != GSS_S_COMPLETE {
			if secCtx.logger.HasDebugLog() {
				secCtx.logger.DebugLog(fmt.Sprintf("gssIntBuildTokenHdr failed"))
			}

			if bNewConversation {
				secCtx.gssCtx.Release()
			}

			status.Major = GSS_S_FAILURE
			status.Err = tdgssError(hdrStatus.Err, formatTdgssStatusErrOnly(status))
			return
		}
		out = concat(tokenHdr, outputToken)
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext initial output token:\n%v", hex.Dump(out)))
		}
	}

		/*
		* Encrypt and send the Extra Logdata to the server if the
		* server has logdata handling capability.
		* If the server has logdata handling capability, but the client does
		* not have any extra logdata information to send, encrypt and send an
		* empty string as the extra logdata.
		* If the server does not have logdata handling capability and the client
		* has logdata to send, then return a descriptive error.
		* This is done as below after the context establishment is complete.
		*/

	if !bNewConversation {
		if peerTokenHdr.ByteVar == INITMSG {

			if  peerTokenHdr.Capabilities == CAP_LOGDATA {

				if len(outputToken) > 0 {
					// This is a sanity check. According to the C code, if this is not the first call and
					// there is a valid non zero length output token from init_sec_context then
					// this is an error.
					if secCtx.logger.HasDebugLog() {
						secCtx.logger.DebugLog(fmt.Sprintf("gssIntInitSecContext output token should be zero length!"))
					}
					status.Major = GSS_S_FAILURE
					status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
					return
				}

				extraLogdata := ""
				if bHaveLogdata {
					extraLogdata = secCtx.extraLogdata
				}

				// Encrypt the logdata and send to server
				wrapStatus, _, abyEncryptedLogdata := secCtx.gsspWrap(1, 0, []byte(extraLogdata))

				if wrapStatus.Major != GSS_S_COMPLETE {
					status = wrapStatus
					status.Err = tdgssError(wrapStatus.Err, formatTdgssStatusErrOnly(status))
					return
				}

				hdrStatus, logdataTokenHdr := gssIntBuildTokenHdr(secCtx.logger,
								INITCTXMSG, LOGDATAMSG, NOTAPPLICABLE, uint32(len(abyEncryptedLogdata)), NOTAPPLICABLE)

				if hdrStatus.Major != GSS_S_COMPLETE {
					status = hdrStatus
					status.Err = tdgssError(hdrStatus.Err, formatTdgssStatusErrOnly(status))
					return
				}

				out = concat(logdataTokenHdr, abyEncryptedLogdata)
				if secCtx.logger.HasDebugLog() {
					secCtx.logger.DebugLog(fmt.Sprintf("gsspInitSecContext logdata output token:\n%v", hex.Dump(out)))
				}

				secCtx.completionStatus = GSS_CTX_SETUP_COMPLETE
				status.Major = GSS_S_COMPLETE
				status.Minor = GSSP2GSS_S_COMPLETE
				return

			} else if bHaveLogdata && peerTokenHdr.Capabilities != CAP_LOGDATA {
				/* If the client has extra logdata to send, but the server does
					not have the handling capability, return an error.
					This is in accordance with how SSPI handles today.
					In the future, we can propose to handle this in a different
					way, by still completing successfully, that is allowing the
					user to authenticate but sending back a meaningful message
					saying that the authorization failed due to capability being
					unavailable on the server side.
					This may require some changes to the way error handling
					works today to have a status bit to indicate that even
					though the routine completed successfully, there is a
					message stored in it's minor status.
					*/

				status.Major = GSS_S_FAILURE
				status.Minor = GSSP2GSS_ERR_NO_LOGDATA_CAPABILITIES
				return
			}

			if status.Major == GSS_S_COMPLETE {
				secCtx.completionStatus = GSS_CTX_SETUP_COMPLETE
			}
		}
	}

	return
}

func (secCtx *gssSecContext) gsspWrap(confReqFlag int, qopReq uint32, in []byte) (status TdgssStatus, confState int, out []byte) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspWrap GSS %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspWrap GSS %v", secCtx)) }()
	}

	if secCtx.gssCtx == nil {
		status.Major = GSS_S_NO_CONTEXT | GSS_S_CALL_INACCESSIBLE_READ
		status.Minor = GSSP2GSS_ERR_INVALID_HANDLE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	var outBuffer []byte
	status, outBuffer, confState = gssIntWrap(secCtx.logger, secCtx.gssCtx, confReqFlag, in)
	if status.Major != GSS_S_COMPLETE {
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("KRB5 gssIntWrap failed"))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	var tokenHdr []byte
	status, tokenHdr = gssIntBuildTokenHdr(secCtx.logger, ENCRYPTMSG, NOTAPPLICABLE, NOTAPPLICABLE, uint32(len(outBuffer)), NOTAPPLICABLE)
	if status.Major != GSS_S_COMPLETE {
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("KRB5 gssIntWrap failed"))
		}
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	out = concat(tokenHdr, outBuffer)
	status.Major = GSS_S_COMPLETE
	return
}

func (secCtx *gssSecContext) gsspUnwrap(in []byte) (status TdgssStatus, confState int, qopState uint32, out []byte) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspUnwrap GSS %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspUnwrap GSS %v", secCtx)) }()
	}

	if secCtx.gssCtx == nil {
		status.Major = GSS_S_NO_CONTEXT | GSS_S_CALL_INACCESSIBLE_READ
		status.Minor = GSSP2GSS_ERR_INVALID_HANDLE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if in == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = GSSP2GSS_ERR_INVALID_BUFFER
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Retrieve token header.
	var peerTokenHdr gssV2TokenHeader
	status, peerTokenHdr = gssIntRetrieveHeader(secCtx.logger, in)
	if status.Major != GSS_S_COMPLETE {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}


	tokenLength := peerTokenHdr.MsgLength

	if len(in) < int(GSS_TOKEN_HEADER_LENGTH + tokenLength) {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	encryptedMsg := in[GSS_TOKEN_HEADER_LENGTH:]

	status, out, confState = gssIntUnwrap(secCtx.logger, secCtx.gssCtx, encryptedMsg)
	if status.Major != GSS_S_COMPLETE {
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	qopState = 0
	return
}

func (secCtx *gssSecContext) gsspAcquireCred(userLgnMechName string, credUsage int, desiredName string) (status TdgssStatus, timeRec uint32) {

	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspAcquireCred for GSS %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspAcquireCred for GSS %v", secCtx)) }()
	}

	/* KRB5 does not support authcid style logon properties in logdata so if any one of the
	*  properties exist in logdata, then return an unknown property message.
	*/
	if strings.Contains(desiredName, "authcid=") || strings.Contains(desiredName, "password=") {
		status.Major = GSS_S_FAILURE
		status.Minor = TDAT_LDAP_ERR_UNKNOWN_PROPERTY
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status, timeRec, secCtx.gssCred, secCtx.extraLogdata = gssIntAcquireCred(secCtx.logger, desiredName, secCtx.mechInfo.mechProperties.DesiredCredentialTime)

	if status.Major != GSS_S_COMPLETE {
		if secCtx.logger.HasDebugLog() {
			secCtx.logger.DebugLog(fmt.Sprintf("GSS gsspAcquireCred: gssIntAcquireCred failed"))
		}
		status.Major = GSS_S_FAILURE
		status.Minor = GSSP2GSS_ERR_CANT_INQUIRE_CRED
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = GSS_S_COMPLETE
	return
}

func (secCtx *gssSecContext) gsspReleaseCred() (status TdgssStatus) {
	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspReleaseCred %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspReleaseCred %v", secCtx)) }()
	}

	gssIntReleaseCred(secCtx.logger, secCtx.gssCred)

	status.Major = GSS_S_COMPLETE
	return
}

func (secCtx *gssSecContext) gsspDeleteContext() (status TdgssStatus) {
	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspDeleteContext %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspDeleteContext %v", secCtx)) }()
	}

	gssIntDeleteContext(secCtx.logger, secCtx.gssCtx)

	status.Major = GSS_S_COMPLETE
	return
}

func (secCtx *gssSecContext) gsspGetMechInfo() (mechInfo *mechanismInfo) {
	if secCtx.logger.HasTraceLog() {
		secCtx.logger.TraceLog(fmt.Sprintf("> enter gsspGetMechInfo for GSS %v", secCtx))
		defer func() { secCtx.logger.TraceLog(fmt.Sprintf("< leave gsspGetMechInfo for GSS %v", secCtx)) }()
	}
	return secCtx.mechInfo
}

func (secCtx *gssSecContext) gsspIsEstablished() bool {
	return secCtx.completionStatus == GSS_CTX_SETUP_COMPLETE
}
