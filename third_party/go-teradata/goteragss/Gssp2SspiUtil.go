// Copyright 2022 by Teradata Corporation. All Rights Reserved.

//go:build windows

package goteragss

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"syscall"
	"fmt"
	"unsafe"
)

const (
	SECPKG_CRED_INBOUND  = 1
	SECPKG_CRED_OUTBOUND = 2
	SECPKG_CRED_BOTH     = (SECPKG_CRED_OUTBOUND | SECPKG_CRED_INBOUND)

	SEC_WINNT_AUTH_IDENTITY_UNICODE = 0x2
	SEC_WINNT_AUTH_IDENTITY_VERSION = 0x200
)

const KERBEROS_NAME = "Kerberos"
const SEC_E_OK                    = syscall.Errno(0)
const SEC_I_COMPLETE_AND_CONTINUE = syscall.Errno(590612)
const SEC_I_COMPLETE_NEEDED       = syscall.Errno(590611)
const SEC_I_CONTINUE_NEEDED       = syscall.Errno(590610)

const (
	SECURITY_NATIVE_DREP = 16

	SECBUFFER_DATA           = 1
	SECBUFFER_TOKEN          = 2
	SECBUFFER_PKG_PARAMS     = 3
	SECBUFFER_MISSING        = 4
	SECBUFFER_EXTRA          = 5
	SECBUFFER_STREAM_TRAILER = 6
	SECBUFFER_STREAM_HEADER  = 7
	SECBUFFER_PADDING        = 9
	SECBUFFER_STREAM         = 10
	SECBUFFER_READONLY       = 0x80000000
	SECBUFFER_ATTRMASK       = 0xf0000000
	SECBUFFER_VERSION        = 0
	SECBUFFER_EMPTY          = 0

	ISC_REQ_DELEGATE               = 1
	ISC_REQ_MUTUAL_AUTH            = 2
	ISC_REQ_REPLAY_DETECT          = 4
	ISC_REQ_SEQUENCE_DETECT        = 8
	ISC_REQ_CONFIDENTIALITY        = 16
	ISC_REQ_USE_SESSION_KEY        = 32
	ISC_REQ_PROMPT_FOR_CREDS       = 64
	ISC_REQ_USE_SUPPLIED_CREDS     = 128
	ISC_REQ_ALLOCATE_MEMORY        = 256
	ISC_REQ_USE_DCE_STYLE          = 512
	ISC_REQ_DATAGRAM               = 1024
	ISC_REQ_CONNECTION             = 2048
	ISC_REQ_EXTENDED_ERROR         = 16384
	ISC_REQ_STREAM                 = 32768
	ISC_REQ_INTEGRITY              = 65536
	ISC_REQ_MANUAL_CRED_VALIDATION = 524288
	ISC_REQ_HTTP                   = 268435456

	ASC_REQ_DELEGATE        = 1
	ASC_REQ_MUTUAL_AUTH     = 2
	ASC_REQ_REPLAY_DETECT   = 4
	ASC_REQ_SEQUENCE_DETECT = 8
	ASC_REQ_CONFIDENTIALITY = 16
	ASC_REQ_USE_SESSION_KEY = 32
	ASC_REQ_ALLOCATE_MEMORY = 256
	ASC_REQ_USE_DCE_STYLE   = 512
	ASC_REQ_DATAGRAM        = 1024
	ASC_REQ_CONNECTION      = 2048
	ASC_REQ_EXTENDED_ERROR  = 32768
	ASC_REQ_STREAM          = 65536
	ASC_REQ_INTEGRITY       = 131072
)

const (
	UNKNOWNENDIEN   = 0
	LITTLE_ENDIEN   = 1
	BIG_ENDIEN      = 2

	TDGSS_5_1_0     = 1
	TDGSS_6_0_0     = 2
)

const BIT0 byte = 0x1
const BIT1 byte = 0x2
const BIT2 byte = 0x4
const BIT3 byte = 0x8

const (
	SECPKG_ATTR_SIZES            = 0
	SECPKG_ATTR_NAMES            = 1
	SECPKG_ATTR_LIFESPAN         = 2
	SECPKG_ATTR_DCE_INFO         = 3
	SECPKG_ATTR_STREAM_SIZES     = 4
	SECPKG_ATTR_KEY_INFO         = 5
	SECPKG_ATTR_AUTHORITY        = 6
	SECPKG_ATTR_PROTO_INFO       = 7
	SECPKG_ATTR_PASSWORD_EXPIRY  = 8
	SECPKG_ATTR_SESSION_KEY      = 9
	SECPKG_ATTR_PACKAGE_INFO     = 10
	SECPKG_ATTR_USER_FLAGS       = 11
	SECPKG_ATTR_NEGOTIATION_INFO = 12
	SECPKG_ATTR_NATIVE_NAMES     = 13
	SECPKG_ATTR_FLAGS            = 14
)

type _SecPkgContext_Sizes struct {
	MaxToken             uint32
	MaxSignature         uint32
	BlockSize            uint32
	SecurityTrailer      uint32
}

type _SecPkgContext_NativeNames struct {
	ClientName *uint16
	ServerName *uint16
}

const SECQOP_WRAP_NO_ENCRYPT uint32 = 0x80000001
const SSPI_TOKEN_HEADER_LENGTH = 16

type sspiV2TokenHeader struct {
	Version         byte
	MsgType         byte
	ByteVar         byte
	ClientOrServer  byte
	MsgLength       uint32
	Flags           byte
	Capabilities    uint32
	ResForExp       [3]byte
}

type SecPkgInfo struct {
	Capabilities uint32
	Version      uint16
	RPCID        uint16
	MaxToken     uint32
	Name         *uint16
	Comment      *uint16
}

type sspiSecPkgInfo struct {
	Capabilities uint32
	Version      uint16
	RPCID        uint16
	MaxToken     uint32
	Name         string
	Comment      string
}

type SecBuffer struct {
	BufferSize uint32
	BufferType uint32
	Buffer     *byte
}

type SecBufferDesc struct {
	Version      uint32
	BuffersCount uint32
	Buffers      *SecBuffer
}

// The Win32 struct SEC_WINNT_AUTH_IDENTITY_EX is unpacked and has size = 72 bytes
// The SEC_WINNT_AUTH_IDENTITY_EX pointer fields are aligned on 8-byte boundaries as expected by Go.
// This was verified by printing the sizeof SEC_WINNT_AUTH_IDENTITY_EX in C TeraGSS on Windows.
type SEC_WINNT_AUTH_IDENTITY_EX struct {
	Version           uint32
	Length            uint32
	User              *uint16
	UserLength        uint32
	Domain            *uint16
	DomainLength      uint32
	Password          *uint16
	PasswordLength    uint32
	Flags             uint32
	PackageList       *uint16
	PackageListLength uint32
}

var TRAILING_BYTES = []byte {0, 0, 0, 0, 0, 0, 0, 0}
var TRAILING_BYTES_LENGTH uint32 = uint32(len(TRAILING_BYTES))

func gsspCredUsage(gsspUsage int) (sspiUsage int) {
	if gsspUsage == GSS_C_INITIATE {
		sspiUsage = SECPKG_CRED_OUTBOUND
	} else if gsspUsage == GSS_C_ACCEPT {
		sspiUsage = SECPKG_CRED_INBOUND
	} else {
		sspiUsage = SECPKG_CRED_BOTH
	}
	return
}

func setSecBuffer(sb *SecBuffer, buftype uint32, data []byte) {
	sb.BufferType = buftype
	if len(data) > 0 {
		sb.Buffer = &data[0]
		sb.BufferSize = uint32(len(data))
	} else {
		sb.Buffer = nil
		sb.BufferSize = 0
	}
}

func getBytes(sb *SecBuffer) (output []byte) {
	if sb.Buffer == nil || sb.BufferSize <= 0 {
		return nil
	}
	output = (*[0x7FFFFFFF]byte)(unsafe.Pointer(sb.Buffer))[:sb.BufferSize]
	return
}


func sspiIntBuildTokenHdr(logger TdgssLogger, msgType byte, msgSeq byte, confReqFlag int, fDelegation uint32, tokenLength uint32, fCapabilities uint32) (status TdgssStatus, tokenHdr []byte) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter sspiIntBuildTokenHdr"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave sspiIntBuildTokenHdr")) }()
	}

	var reqFlags byte = 0

	/* Set up the first bit for SSPI(0) or GSS(1) */
	reqFlags = reqFlags & (^BIT0)

	/* Set up the second bit for BIG_ENDIEN(1) */
	reqFlags = reqFlags | BIT1

	/* Used for TD2 style wrap and unwrap */
	if confReqFlag != NOTAPPLICABLE && confReqFlag != 0 {
		/* Both confidentiality and integrity */
		reqFlags |= BIT2
	}

	/* Set the delegation bit, the forth bit. */
	if fDelegation != 0 {
		reqFlags |= BIT3
	}

	sspiTokenHdr := sspiV2TokenHeader{
		Version:          VERSION2,
		MsgType:          msgType,
		ByteVar:          msgSeq,
		ClientOrServer:   CLIENT,
		MsgLength:        tokenLength,
		Flags:            reqFlags,
		Capabilities:     fCapabilities,
		//ResForExp:      //initialized to zero be default
	}

	var buf bytes.Buffer
	err := binary.Write(&buf, binary.BigEndian, sspiTokenHdr)
	if err != nil {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("sspiIntBuildTokenHdr: binary.Write failed: %v", err))
		}
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}
	tokenHdr = buf.Bytes()

	status.Major = GSS_S_COMPLETE	
	return
}

func sspiIntRetrieveHeader(in []byte) (status TdgssStatus, tokenHdr sspiV2TokenHeader) {

	status.Major = GSS_S_FAILURE
	if len(in) == 0 {
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// De-serialize the V2 token header
	r := bytes.NewReader(in)
	err := binary.Read(r, binary.BigEndian, &tokenHdr)
	if err != nil {
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}

	if tokenHdr.Version != VERSION2 {
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = GSS_S_COMPLETE
	return
}

func sspiIntGetAdditionalCredentials(username string, domain string, password string) (status TdgssStatus, pAuthData *SEC_WINNT_AUTH_IDENTITY_EX) {
	
	var err error

	var userPtr *uint16
	var userLength uint32
	var utf16User []uint16
	if len(username) > 0 {
		utf16User, err = syscall.UTF16FromString(username)
			if err != nil {
				status.Major = GSS_S_FAILURE
				status.Minor = SSPI_ERR_INVALID_INPUT
				status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
				return
			}
		userPtr = &utf16User[0]
		userLength = uint32(len(utf16User) - 1) // do not count terminating 0
	}

	var domainPtr *uint16
	var domainLength uint32
	var utf16Domain []uint16
	if len(domain) > 0 {
		utf16Domain, err = syscall.UTF16FromString(domain)
		if err != nil {
			status.Major = GSS_S_FAILURE
			status.Minor = SSPI_ERR_INVALID_INPUT
			status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
			return
		}
		domainPtr = &utf16Domain[0]
		domainLength = uint32(len(utf16Domain) - 1) // do not count terminating 0
	}
	
	var passwordPtr *uint16
	var passwordLength uint32
	var utf16Password []uint16
	if len(password) > 0 {
		utf16Password, err = syscall.UTF16FromString(password)
		if err != nil {
			status.Major = GSS_S_FAILURE
			status.Minor = SSPI_ERR_INVALID_INPUT
			status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
			return
		}
		passwordPtr = &utf16Password[0]
		passwordLength = uint32(len(utf16Password) - 1) // do not count terminating 0
	}

	utf16PackageList, err := syscall.UTF16FromString("KERBEROS,NEGOTIATE")
	if err != nil {
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_INVALID_INPUT
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}

	var authData SEC_WINNT_AUTH_IDENTITY_EX
	authData = SEC_WINNT_AUTH_IDENTITY_EX{
		Version:           SEC_WINNT_AUTH_IDENTITY_VERSION,
		Length:            uint32(unsafe.Sizeof(authData)),
		User:              userPtr,
		UserLength:        userLength,
		Domain:            domainPtr,
		DomainLength:      domainLength, 
		Password:          passwordPtr,
		PasswordLength:    passwordLength,
		Flags:             SEC_WINNT_AUTH_IDENTITY_UNICODE,
		PackageList:       &utf16PackageList[0],
		PackageListLength: uint32(len(utf16PackageList) - 1), // do not count terminating 0
	}

	pAuthData = &authData

	status.Major = GSS_S_COMPLETE
	return
}

func sspIntAcquireCredentialsHandle(logger TdgssLogger, desiredName string, credUsage int, handle *sspiCredHandle, expiry *syscall.Filetime) (status TdgssStatus, extraLogdata string) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter sspIntAcquireCredentialsHandle"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave sspIntAcquireCredentialsHandle")) }()
	}

	name, err := syscall.UTF16PtrFromString(KERBEROS_NAME)
	if err != nil {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("SSPI sspIntAcquireCredentialsHandle: UTF16PtrFromString failed"))
		}
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_INVALID_INPUT
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}

	var authData *SEC_WINNT_AUTH_IDENTITY_EX = nil

	if len(desiredName) > 0 {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("SSPI sspIntAcquireCredentialsHandle: desiredName %v", desiredName))
		}

		var user string
		var domain string
		var password string
		var mapExtra map[string]string
		status, user, domain, password, mapExtra = tdatParseLogData(logger, desiredName, []string {"user", "profile"})

		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("SSPI sspIntAcquireCredentialsHandle: user: %s domain: %s password: %s mapExtra %v", user, domain, password, mapExtra))
		}
		if status.Major != GSS_S_COMPLETE {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("SSPI sspIntAcquireCredentialsHandle: tdatParseLogData failed"))
			}
			status.Major = GSS_S_FAILURE
			status.Minor = SSPI_ERR_INVALID_INPUT
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
			return
		}
		if len(user) > 0 || len(domain) > 0 || len(password) > 0 {
			status, authData = sspiIntGetAdditionalCredentials(user, domain, password)
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("SSPI sspIntAcquireCredentialsHandle: authData.Length %d", authData.Length))
				logger.DebugLog(fmt.Sprintf("SSPI sspIntAcquireCredentialsHandle: authData.UserLength %d", authData.UserLength))
				logger.DebugLog(fmt.Sprintf("SSPI sspIntAcquireCredentialsHandle: authData.DomainLength %d", authData.DomainLength))
				logger.DebugLog(fmt.Sprintf("SSPI sspIntAcquireCredentialsHandle: authData.PasswordLength %d", authData.PasswordLength))
				logger.DebugLog(fmt.Sprintf("SSPI sspIntAcquireCredentialsHandle: authData.PackageListLength %d", authData.PackageListLength))
			}
			if status.Major != GSS_S_COMPLETE {
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("SSPI sspIntAcquireCredentialsHandle: sspiIntGetAdditionalCredentials failed"))
				}
				status.Major = GSS_S_FAILURE
				status.Minor = SSPI_ERR_CANT_INQUIRE_CRED
				status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
				return
			}
		}

		for key, value := range mapExtra {
			extraLogdata = extraLogdata + key + "=" + value + " "
		}
	}

	ret := AcquireCredentialsHandle(nil, /* use the default principal */
								name, /* Kerberos */
								uint32(gsspCredUsage(credUsage)),
								nil, /* logonID */
								(*byte)(unsafe.Pointer(authData)), /* AuthData NULL for SSO, non-NULL for the third party sign-on */
								0, /* GetKeyFn */
								0, /* GetKeyArgument */
								handle,
								expiry)

	if ret != SEC_E_OK {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("SSPI sspIntAcquireCredentialsHandle: AcquireCredentialsHandle failed ret:<%v>", ret))
		}
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_CANT_INQUIRE_CRED
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	if logger.HasDebugLog() {
		logger.DebugLog(fmt.Sprintf("SSPI sspIntAcquireCredentialsHandle: AcquireCredentialsHandle handle %+v", *handle))
	}

	status.Major = GSS_S_COMPLETE
	return
}

func sspIntFreeCredentialsHandle(logger TdgssLogger, sspiCred *sspiCredHandle) (status TdgssStatus) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter sspIntFreeCredentialsHandle"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave sspIntFreeCredentialsHandle")) }()
	}

	if sspiCred == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_INVALID_INPUT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if logger.HasDebugLog() {
		logger.DebugLog(fmt.Sprintf("SSPI sspIntFreeCredentialsHandle for handle %+v", sspiCred))
	}

	if sspiCred.Lower != 0 || sspiCred.Upper != 0 {
		ret := FreeCredentialsHandle(sspiCred)
		sspiCred.Lower = 0
		sspiCred.Upper = 0
		if ret != SEC_E_OK {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("SSPI sspIntFreeCredentialsHandle: FreeCredentialsHandle failed %v", ret))
			}
			status.Major = GSS_S_FAILURE
			status.Minor = SSPI_ERR_INVALID_HANDLE
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}
	}

	status.Major = GSS_S_COMPLETE
	return
}

func sspiIntInitializeSecurityContext(logger TdgssLogger,
	credential *sspiCredHandle,
	context *sspiCtxtHandle,
	targetName string,
	contextreq uint32,
	input []byte,
	maxTokenSize uint32,
	newcontext *sspiCtxtHandle,
	expiry *syscall.Filetime) (status TdgssStatus, output []byte, contextattr uint32, ret syscall.Errno) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter sspiIntInitializeSecurityContext"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave sspiIntInitializeSecurityContext")) }()
	}
	
	targname, err := syscall.UTF16PtrFromString(targetName)
	if err != nil {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("SSPI sspiIntInitializeSecurityContext: UTF16PtrFromString failed"))
		}
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_INVALID_INPUT
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}

	output = make([]byte, maxTokenSize)

	var inputBuf, outputBuf [1]SecBuffer

	setSecBuffer(&inputBuf[0], SECBUFFER_TOKEN, input)
	setSecBuffer(&outputBuf[0], SECBUFFER_TOKEN, output)

	inputBuffers := &SecBufferDesc{
		Version: SECBUFFER_VERSION,
		BuffersCount: 1,
		Buffers: &inputBuf[0],
	}

	outputBuffers := &SecBufferDesc{
		Version: SECBUFFER_VERSION,
		BuffersCount: 1,
		Buffers: &outputBuf[0],
	}

	ret = InitializeSecurityContext(
		credential, // passed in as credential
		context, // passed in as context
		targname, // passed in as targetName as string type, must translate to *uint16
		contextreq, // passed in as contextreq 
		0,  // must be zero
		SECURITY_NATIVE_DREP,  // SECURITY_NATIVE_DREP
		inputBuffers, // passed in as input, must convert to SecBufferDesc. 
		0, // must be zero
		newcontext,  // passed in as newcontext
		outputBuffers, // must create a SecBufferDesc and convert to []byte and return as output
		&contextattr,  // return as contextattr
		expiry) // passed in as expiry


	switch ret {
		case SEC_E_OK:
			status.Major = GSS_S_COMPLETE

		case SEC_I_CONTINUE_NEEDED:
			status.Major = GSS_S_CONTINUE_NEEDED
			
		case SEC_I_COMPLETE_NEEDED, SEC_I_COMPLETE_AND_CONTINUE:
			completeAuthRet := CompleteAuthToken(newcontext, outputBuffers);
			
			if completeAuthRet == SEC_E_OK {
				if ret == SEC_I_COMPLETE_NEEDED {
					ret = SEC_E_OK
					status.Major = GSS_S_COMPLETE
				} else {
					status.Major = GSS_S_CONTINUE_NEEDED
				}

			} else {
				ret = completeAuthRet	
				if logger.HasDebugLog() {
					logger.DebugLog(fmt.Sprintf("SSPI sspiIntInitializeSecurityContext: InitializeSecurityContext failed %+v %d", ret, ret))
				}
				status.Major = GSS_S_FAILURE
				status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
				return
			}

		default:
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("SSPI sspiIntInitializeSecurityContext: InitializeSecurityContext failed %+v %d", ret, ret))
			}
			status.Major = GSS_S_FAILURE
			status.Minor = SSPI_ERR_INVALID_HANDLE
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
	}

	output = getBytes(&outputBuf[0])

	if logger.HasDebugLog() {
		if len(output) > 0 {
			logger.DebugLog(fmt.Sprintf("gsspInitSecContext Initial token:\n%v", hex.Dump(output)))
		}
		logger.DebugLog(fmt.Sprintf("gsspInitSecContext Initial token length: %d and outputBuffer: %+v", len(output), outputBuffers))
		logger.DebugLog(fmt.Sprintf("gsspInitSecContext Initial outputBuf[0].BufferSize: %d", outputBuf[0].BufferSize))
	}

	return
}


func sspIntDeleteSecurityContext(logger TdgssLogger, sspiCtx *sspiCtxtHandle) (status TdgssStatus) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter sspIntDeleteSecurityContext"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave sspIntDeleteSecurityContext")) }()
	}

	if sspiCtx == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_INVALID_INPUT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if logger.HasDebugLog() {
		logger.DebugLog(fmt.Sprintf("SSPI sspiIntDeleteSecurityContext: for handle %+v", sspiCtx))
	}

	if sspiCtx.Lower != 0 || sspiCtx.Upper != 0 {
		ret := DeleteSecurityContext(sspiCtx)
		sspiCtx.Lower = 0
		sspiCtx.Upper = 0
		if ret != SEC_E_OK {
			if logger.HasDebugLog() {
				logger.DebugLog(fmt.Sprintf("SSPI sspiIntDeleteSecurityContext: DeleteSecurityContext failed %+v", ret))
			}
			status.Major = GSS_S_FAILURE
			status.Minor = SSPI_ERR_INVALID_HANDLE
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}
	}

	status.Major = GSS_S_COMPLETE
	return

}

func sspiIntQuerySecurityPackageInfo(logger TdgssLogger, packageName string) (status TdgssStatus, packageInfo *sspiSecPkgInfo) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter sspiIntQuerySecurityPackageInfo"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave sspiIntQuerySecurityPackageInfo")) }()
	}

	// Translate packagename to UTF16 using syscall.UTF16PtrFromString(string)
	utf16Name, err := syscall.UTF16PtrFromString(packageName)
	if err != nil {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("SSPI sspiIntQuerySecurityPackageInfo: UTF16PtrFromString failed"))
		}
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_INVALID_INPUT
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}


	// Declare a pointer to SecPkgInfo which you will pass the address of the pointer to Windows which is the pointer to pointer (**SecPkgInfo).
	var pSecPkgInfo *SecPkgInfo
	ret := QuerySecurityPackageInfo(utf16Name, &pSecPkgInfo)

	if ret != SEC_E_OK {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("SSPI sspiIntQuerySecurityPackageInfo: QuerySecurityPackageInfo failed ret:<%v>", ret))
		}
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_CANT_INQUIRE_CONTEXT
		status.Err = tdgssError(nil, fmt.Sprintf("SSPI sspiIntQuerySecurityPackageInfo: QuerySecurityPackageInfo failed ret:<%v>", ret))
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}


	// Free the memory using defer which will defer until the function returns at any time.
	defer FreeContextBuffer((*byte)(unsafe.Pointer(pSecPkgInfo)))

	// Return packageInfo which is the address of sspiPackageInfo. Go knows about this memory used for packageInfo, so the garbage collector will take care of it when it is no longer in use.
	packageInfo = &sspiSecPkgInfo {
		Capabilities:   pSecPkgInfo.Capabilities,
		Version:        pSecPkgInfo.Version,
		RPCID:          pSecPkgInfo.RPCID,
		MaxToken:       pSecPkgInfo.MaxToken,
		Name:           syscall.UTF16ToString((*[1000000]uint16)(unsafe.Pointer(pSecPkgInfo.Name))[:]),
		Comment:        syscall.UTF16ToString((*[1000000]uint16)(unsafe.Pointer(pSecPkgInfo.Comment))[:]),
	}

	if logger.HasDebugLog() {
		logger.DebugLog(fmt.Sprintf("SSPI QuerySecurityPackageInfo: Capabilities [%d]", packageInfo.Capabilities))
		logger.DebugLog(fmt.Sprintf("SSPI QuerySecurityPackageInfo: Version [%d]", packageInfo.Version))
		logger.DebugLog(fmt.Sprintf("SSPI QuerySecurityPackageInfo: RPCID [%d]", packageInfo.RPCID))
		logger.DebugLog(fmt.Sprintf("SSPI QuerySecurityPackageInfo: MaxToken [%d]", packageInfo.MaxToken))
		logger.DebugLog(fmt.Sprintf("SSPI QuerySecurityPackageInfo: Name [%s]", packageInfo.Name))
		logger.DebugLog(fmt.Sprintf("SSPI QuerySecurityPackageInfo: Comment [%s]", packageInfo.Comment))
	}

	status.Major = GSS_S_COMPLETE
	return
}

func sspiIntQueryContextNativeNames(logger TdgssLogger, context *sspiCtxtHandle) (status TdgssStatus, clientName string, serverName string) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter sspiIntQueryContextNativeNames"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave sspiIntQueryContextNativeNames")) }()
	}

	if context == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_INVALID_INPUT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	var contextNativeNames _SecPkgContext_NativeNames
	ret := QueryContextAttributes(context, SECPKG_ATTR_NATIVE_NAMES, (*byte)(unsafe.Pointer(&contextNativeNames)))
	if ret != SEC_E_OK {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	defer FreeContextBuffer((*byte)(unsafe.Pointer(contextNativeNames.ClientName)))
	defer FreeContextBuffer((*byte)(unsafe.Pointer(contextNativeNames.ServerName)))
	clientName = syscall.UTF16ToString((*[1000000]uint16)(unsafe.Pointer(contextNativeNames.ClientName))[:])
	serverName = syscall.UTF16ToString((*[1000000]uint16)(unsafe.Pointer(contextNativeNames.ServerName))[:])

	if logger.HasDebugLog() {
		logger.DebugLog(fmt.Sprintf("SSPI sspiIntQueryContextUserName: clientName [%s]", clientName))
		logger.DebugLog(fmt.Sprintf("SSPI sspiIntQueryContextUserName: serverName [%s]", serverName))
	}

	status.Major = GSS_S_COMPLETE
	return
}

func sspiIntQueryContextSizes(logger TdgssLogger, context *sspiCtxtHandle) (status TdgssStatus, contextSizes *_SecPkgContext_Sizes) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter sspiIntQueryContextSizes"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave sspiIntQueryContextSizes")) }()
	}

	if context == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_INVALID_INPUT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	contextSizes = &_SecPkgContext_Sizes{}
	ret := QueryContextAttributes(context, SECPKG_ATTR_SIZES, (*byte)(unsafe.Pointer(contextSizes)))
	if ret != SEC_E_OK {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("SSPI sspiIntQueryContextSizes: QueryContextAttributes failed %+v", ret))
		}
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_INVALID_HANDLE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if logger.HasDebugLog() {
		logger.DebugLog(fmt.Sprintf("SSPI sspiIntQueryContextSizes: MaxToken [%d]", contextSizes.MaxToken))
		logger.DebugLog(fmt.Sprintf("SSPI sspiIntQueryContextSizes: MaxSignature [%d]", contextSizes.MaxSignature))
		logger.DebugLog(fmt.Sprintf("SSPI sspiIntQueryContextSizes: BlockSize [%d]", contextSizes.BlockSize))
		logger.DebugLog(fmt.Sprintf("SSPI sspiIntQueryContextSizes: SecurityTrailer [%d]", contextSizes.SecurityTrailer))
	}

	status.Major = GSS_S_COMPLETE
	return
}

func sspiIntBuildSecCtx(bNewConversation bool, fDelegation uint32, secCtx *sspiSecContext, tokenHdr sspiV2TokenHeader) {

	if secCtx.completionStatus == SSPI_CTX_SETUP_COMPLETE {
		return
	}

	if secCtx.completionStatus == SSPI_CTX_NEW {
		secCtx.hostMachineType = BIG_ENDIEN
		secCtx.hostType = SSPISTYLE
		secCtx.clientOrServer = CLIENT
		secCtx.version = VERSION2
		if fDelegation != 0 {
			secCtx.fDelegation = 1
		} else {
			secCtx.fDelegation = 0
		}
	}

	secCtx.peerLibraryType = TDGSS_6_0_0

	if bNewConversation {
		secCtx.peerType = UNKNOWNSTYLE
		secCtx.peerMachineType = UNKNOWNENDIEN

	} else if tokenHdr.ByteVar == INITMSG {
		if (tokenHdr.Flags & BIT0) != 0 {
			secCtx.peerType = GSSSTYLE
		} else {
			secCtx.peerType = SSPISTYLE
		}

		if (tokenHdr.Flags & BIT1) != 0 {
			secCtx.peerMachineType = BIG_ENDIEN
		} else {
			secCtx.peerMachineType = LITTLE_ENDIEN
		}
	}
	secCtx.completionStatus = SSPI_CTX_SETUP_PARTIAL

	return
}


func sspiIntEncryptMessage(logger TdgssLogger, context *sspiCtxtHandle, inputMsg []byte, qop uint32, seqno uint32) (status TdgssStatus, outputMsg []byte) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter sspiIntEncryptMessage"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave sspiIntEncryptMessage")) }()
	}

	if context == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_INVALID_INPUT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status1, contextSizes := sspiIntQueryContextSizes(logger, context)

	if status1.Major != GSS_S_COMPLETE {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	var secBuffers [3]SecBuffer

	bySecurityTrailer := make([]byte, contextSizes.SecurityTrailer)
	byPadding := make([]byte, contextSizes.BlockSize)

	setSecBuffer(&secBuffers[0], SECBUFFER_TOKEN, bySecurityTrailer)
	setSecBuffer(&secBuffers[1], SECBUFFER_DATA, inputMsg)
	setSecBuffer(&secBuffers[2], SECBUFFER_PADDING, byPadding)

	secBuffDesc := &SecBufferDesc {
		Version: SECBUFFER_VERSION,
		BuffersCount: uint32(len(secBuffers)),
		Buffers: &secBuffers[0],
	}

	ret := EncryptMessage(context, qop, secBuffDesc, seqno)
	if logger.HasDebugLog() {
		logger.DebugLog(fmt.Sprintf("SSPI sspiIntEncryptMessage: EncryptMessage return code %+v", ret))
	}
	if ret != SEC_E_OK {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("SSPI sspiIntEncryptMessage: EncryptMessage failed %+v", ret))
		}
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_CANT_WRAP
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if logger.HasDebugLog() {
		logger.DebugLog(fmt.Sprintf("SSPI sspiIntEncryptMessage Security Trailer: %+v", secBuffers[0]))
		logger.DebugLog(fmt.Sprintf("SSPI sspiIntEncryptMessage Message: %+v", secBuffers[1]))
		logger.DebugLog(fmt.Sprintf("SSPI sspiIntEncryptMessage Padding: %+v", secBuffers[2]))
	}

	r0 := getBytes(&secBuffers[0])
	r1 := getBytes(&secBuffers[1])
	r2 := getBytes(&secBuffers[2])

	if r0 == nil || r1 == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_CANT_WRAP
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	if logger.HasDebugLog() {
		logger.DebugLog(fmt.Sprintf("SSPI sspiIntEncryptMessage Security Trailer:\n%v", hex.Dump(r0)))
		logger.DebugLog(fmt.Sprintf("SSPI sspiIntEncryptMessage Message:\n%v", hex.Dump(r1)))
		logger.DebugLog(fmt.Sprintf("SSPI sspiIntEncryptMessage Padding:\n%v", hex.Dump(r2)))
	}

	 /* 8 extra bytes for holding lengths security trailer and padding */
	outputMsg = make([]byte, 0, secBuffers[0].BufferSize + secBuffers[1].BufferSize + secBuffers[2].BufferSize + TRAILING_BYTES_LENGTH)

	outputMsg = append(outputMsg, r0...)
	outputMsg = append(outputMsg, r1...)
	if len(r2) > 0 {
		outputMsg = append(outputMsg, r2...)
	}
	outputMsg = append(outputMsg, TRAILING_BYTES...)

	status.Major = GSS_S_COMPLETE
	return
}

func sspiIntDecryptMessage(logger TdgssLogger, context *sspiCtxtHandle, inputMsg []byte, seqno uint32) (status TdgssStatus, qop uint32, outputMsg []byte) {

	if logger.HasTraceLog() {
		logger.TraceLog(fmt.Sprintf("> enter sspiIntDecryptMessage"))
		defer func() { logger.TraceLog(fmt.Sprintf("< leave sspiIntDecryptMessage")) }()
	}

	if context == nil  || inputMsg == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_INVALID_INPUT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	var secBuffers [2]SecBuffer

	setSecBuffer(&secBuffers[0], SECBUFFER_STREAM, inputMsg)
	
	// We do not need to allocate memory for the data buffer because according to documentation,
	// the encrypted message in the stream buffer will be decrypted in place and the databuffer
	// will point into the stream buffer. The C code also passed in a null buffer and only
	// frees the stream buffer at the end of unwrap.
	setSecBuffer(&secBuffers[1], SECBUFFER_DATA, []byte{})

	secBuffDesc := &SecBufferDesc {
		Version: SECBUFFER_VERSION,
		BuffersCount: uint32(len(secBuffers)),
		Buffers: &secBuffers[0],
	}

	ret := DecryptMessage(context, secBuffDesc, seqno, &qop)
	if ret != SEC_E_OK {
		if logger.HasDebugLog() {
			logger.DebugLog(fmt.Sprintf("SSPI sspiIntDecryptMessage: DecryptMessage failed %+v", ret))
		}
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_CANT_UNWRAP
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	outputMsg = getBytes(&secBuffers[1])
	if outputMsg == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = SSPI_ERR_CANT_UNWRAP
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = GSS_S_COMPLETE
	return
}

const MaxGsspFlags = 6
const MaxSspiFlags = 6

var fGsspList = [MaxGsspFlags]uint32 {
	GSS_C_DELEG_FLAG,
	GSS_C_MUTUAL_FLAG,
	GSS_C_REPLAY_FLAG,
	GSS_C_SEQUENCE_FLAG,
	GSS_C_CONF_FLAG,
	GSS_C_INTEG_FLAG,
}

var fSspiInitList = [MaxSspiFlags]uint32 {
	ISC_REQ_DELEGATE,
	ISC_REQ_MUTUAL_AUTH,
	ISC_REQ_REPLAY_DETECT,
	ISC_REQ_SEQUENCE_DETECT,
	ISC_REQ_CONFIDENTIALITY,
	ISC_REQ_INTEGRITY,
}

func SSPIIntConvertFlags(reqFlags uint32) (contextreq uint32) {
	// no conversion needed
	if reqFlags == 0 {
		contextreq = 0
		return
	}

	for i := 0; i < len(fGsspList) ; i++ {
		if (reqFlags & fSspiInitList[i]) != 0 {
			contextreq |= fGsspList[i]
		}
	}

	return
}
