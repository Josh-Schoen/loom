// Copyright 2018 Teradata. All rights reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/hex"
	"fmt"
	"hash"
	"os"
	"strings"
)

const PREFIX = "ENCRYPTED_PASSWORD("
const ARGSEP = ","
const SUFFIX = ")"
const FILEPREFIX = "file:"

func (con *TeradataConnection) DecryptPass (sInput string, bMatchEntireString bool) (sOutput string, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter DecryptPass %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave DecryptPass %v", con)) } ()
	}

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("DecryptPass: bMatchEntireString=%v sInput=\"%v\" length=%v", bMatchEntireString, sInput, len (sInput)))
	}

	iPrefix := strings.Index (sInput, PREFIX)
	if iPrefix < 0 {
		if con.hasDebugLog () {
			debugLog ("DecryptPass: input string does not contain encrypted password prefix " + PREFIX)
		}

		sOutput = sInput
		return
	}

	iSuffix := indexOf (sInput, SUFFIX, iPrefix + len (PREFIX))
	if iSuffix == -1 {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Encrypted password string contains prefix %v but is missing suffix %v", PREFIX, SUFFIX))
		return
	}

	// Error if the input string must be matched completely but does not
	iPastSuffix := iSuffix + len (SUFFIX)
	nExtraTrailerCharCount := len (sInput) - iPastSuffix
	bGotExtraStuff := iPrefix != 0 || nExtraTrailerCharCount != 0
	if bMatchEntireString && bGotExtraStuff {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("The password must consist solely of an encrypted password string, but it contains %v leading character(s) and %v trailing character(s) outside the encrypted password string", iPrefix, nExtraTrailerCharCount))
		return
	}

	sArgs := sInput [iPrefix + len (PREFIX) : iSuffix]

	asResourceNames := strings.Split (sArgs, ARGSEP)
	if len (asResourceNames) != 2 {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("The encrypted password string contains %v resource name(s), but must contain 2 resource names", len (asResourceNames)))
		return
	}

	sPasswordEncryptionKeyResourceName := asResourceNames [0]
	sEncryptedPasswordResourceName     := asResourceNames [1]

	if strings.TrimSpace (sPasswordEncryptionKeyResourceName) != sPasswordEncryptionKeyResourceName {
		err = con.makeDriverError (nil, 0, "The first argument of the encrypted password string (the password encryption key resource name) contains non-permitted whitespace")
		return
	}

	if strings.TrimSpace (sEncryptedPasswordResourceName) != sEncryptedPasswordResourceName {
		err = con.makeDriverError (nil, 0, "The second argument of the encrypted password string (the encrypted password resource name) contains non-permitted whitespace")
		return
	}

	if sPasswordEncryptionKeyResourceName == "" {
		err = con.makeDriverError (nil, 0, "The first argument of the encrypted password string (the password encryption key resource name) is missing")
		return
	}

	if sEncryptedPasswordResourceName == "" {
		err = con.makeDriverError (nil, 0, "The second argument of the encrypted password string (the encrypted password resource name) is missing")
		return
	}

	if con.hasDebugLog () {
		debugLog ("DecryptPass: sPasswordEncryptionKeyResourceName=" + sPasswordEncryptionKeyResourceName + " sEncryptedPasswordResourceName=" + sEncryptedPasswordResourceName)
	}

	if ! strings.HasPrefix (sPasswordEncryptionKeyResourceName, FILEPREFIX) {
		err = con.makeDriverError (nil, 0, "The first argument of the encrypted password string (the password encryption key resource name) does not start with the required prefix " + FILEPREFIX)
		return
	}

	if ! strings.HasPrefix (sEncryptedPasswordResourceName, FILEPREFIX) {
		err = con.makeDriverError (nil, 0, "The second argument of the encrypted password string (the encrypted password resource name) does not start with the required prefix " + FILEPREFIX)
		return
	}

	sPasswordEncryptionKeyFileName := strings.TrimPrefix (sPasswordEncryptionKeyResourceName, FILEPREFIX)
	sEncryptedPasswordFileName     := strings.TrimPrefix (sEncryptedPasswordResourceName    , FILEPREFIX)

	if con.hasDebugLog () {
		debugLog ("DecryptPass: sPasswordEncryptionKeyFileName=" + sPasswordEncryptionKeyFileName + " sEncryptedPasswordFileName=" + sEncryptedPasswordFileName)
	}

	mapPassKey, err := con.loadPropertiesFile (sPasswordEncryptionKeyFileName)
	if err != nil { return }

	mapEncPass, err := con.loadPropertiesFile (sEncryptedPasswordFileName)
	if err != nil { return }

	sPassKey_Version         := mapPassKey ["version"]
	sPassKey_Transformation  := mapPassKey ["transformation"]
	sPassKey_Algorithm       := mapPassKey ["algorithm"]
	sPassKey_Match           := mapPassKey ["match"]
	sPassKey_KeyHexDigits    := mapPassKey ["key"]
	sPassKey_Mac             := mapPassKey ["mac"]
	sPassKey_MacKeyHexDigits := mapPassKey ["mackey"]

	sEncPass_Version           := mapEncPass ["version"]
	sEncPass_Match             := mapEncPass ["match"]
	sEncPass_PasswordHexDigits := mapEncPass ["password"]
	sEncPass_ParamsHexDigits   := mapEncPass ["params"]
	sEncPass_HashHexDigits     := mapEncPass ["hash"]

	if sPassKey_Version == "" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Password encryption key file %v is missing a version value", sPasswordEncryptionKeyFileName))
		return
	}

	if sEncPass_Version == "" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Encrypted password file %v is missing a version value", sEncryptedPasswordFileName))
		return
	}

	if sPassKey_Version != "1" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Password encryption key file %v contains unexpected version %v", sPasswordEncryptionKeyFileName, sPassKey_Version))
		return
	}

	if sEncPass_Version != "1" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Encrypted password file %v contains unexpected version %v", sEncryptedPasswordFileName, sEncPass_Version))
		return
	}

	if sPassKey_Match == "" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Password encryption key file %v is missing a match value", sPasswordEncryptionKeyFileName))
		return
	}

	if sEncPass_Match == "" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Encrypted password file %v is missing a match value", sEncryptedPasswordFileName))
		return
	}

	if sPassKey_Match != sEncPass_Match {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Password encryption key file %v match value %v differs from encrypted password file %v match value %v", sPasswordEncryptionKeyFileName, sPassKey_Match, sEncryptedPasswordFileName, sEncPass_Match))
		return
	}

	if sPassKey_Algorithm == "" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Password encryption key file %v is missing an algorithm", sPasswordEncryptionKeyFileName))
		return
	}

	if sPassKey_Algorithm != "AES" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Password encryption key file %v contains invalid algorithm %v", sPasswordEncryptionKeyFileName, sPassKey_Algorithm))
		return
	}

	if sPassKey_Transformation == "" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Password encryption key file %v is missing a transformation", sPasswordEncryptionKeyFileName))
		return
	}

	sTransParts := strings.Split (sPassKey_Transformation, "/")
	if len (sTransParts) != 3 {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Password encryption key file %v contains invalid transformation %v", sPasswordEncryptionKeyFileName, sPassKey_Transformation))
		return
	}

	sAlgorithm := sTransParts [0]
	sMode      := sTransParts [1]
	sPadding   := sTransParts [2]

	if sPassKey_Algorithm != sAlgorithm {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Password encryption key file %v algorithm %v differs from transformation algorithm %v", sPasswordEncryptionKeyFileName, sPassKey_Algorithm, sAlgorithm))
		return
	}

	switch sMode {

		case "CBC", "CFB", "OFB": // do nothing

		default:
			err = con.makeDriverError (nil, 0, fmt.Sprintf ("Password encryption key file %v contains invalid mode %v", sPasswordEncryptionKeyFileName, sMode))
			return
	}

	switch sPadding {

		case "PKCS5Padding", "NoPadding": // do nothing

		default:
			err = con.makeDriverError (nil, 0, fmt.Sprintf ("Password encryption key file %v contains invalid padding %v", sPasswordEncryptionKeyFileName, sPadding))
			return
	}

	if sPassKey_KeyHexDigits == "" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Password encryption key file %v is missing a key", sPasswordEncryptionKeyFileName))
		return
	}

	abyKey, err := hex.DecodeString (sPassKey_KeyHexDigits)
	if err != nil {
		err = con.makeDriverError (err, 0, fmt.Sprintf ("Password encryption key file %v contains an invalid key: %v", sPasswordEncryptionKeyFileName, err))
		return
	}

	if sPassKey_KeyHexDigits == "" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Password encryption key file %v is missing a mackey", sPasswordEncryptionKeyFileName))
		return
	}

	abyMacKey, err := hex.DecodeString (sPassKey_MacKeyHexDigits)
	if err != nil {
		err = con.makeDriverError (err, 0, fmt.Sprintf ("Password encryption key file %v contains an invalid mackey: %v", sPasswordEncryptionKeyFileName, err))
		return
	}

	if sPassKey_Mac == "" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Password encryption key file %v is missing a mac", sPasswordEncryptionKeyFileName))
		return
	}

	var hasher hash.Hash
	switch sPassKey_Mac {

		case "HmacSHA1"   : hasher = hmac.New (sha1.New  , abyMacKey)
		case "HmacSHA256" : hasher = hmac.New (sha256.New, abyMacKey)

		default:
			err = con.makeDriverError (nil, 0, fmt.Sprintf ("Password encryption key file %v contains invalid mac %v", sPasswordEncryptionKeyFileName, sPassKey_Mac))
			return
	}

	if sEncPass_PasswordHexDigits == "" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Encrypted password file %v is missing a password", sEncryptedPasswordFileName))
		return
	}

	abyCipherText, err := hex.DecodeString (sEncPass_PasswordHexDigits)
	if err != nil {
		err = con.makeDriverError (err, 0, fmt.Sprintf ("Encrypted password file %v contains an invalid password: %v", sEncryptedPasswordFileName, err))
		return
	}

	if sEncPass_ParamsHexDigits == "" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Encrypted password file %v is missing params", sEncryptedPasswordFileName))
		return
	}

	abyASN1EncodedIV, err := hex.DecodeString (sEncPass_ParamsHexDigits)
	if err != nil {
		err = con.makeDriverError (err, 0, fmt.Sprintf ("Encrypted password file %v contains invalid hex-encoded params: %v", sEncryptedPasswordFileName, err))
		return
	}

	var abyIV [] byte
	_, err = asn1.Unmarshal (abyASN1EncodedIV, &abyIV)
	if err != nil {
		err = con.makeDriverError (err, 0, fmt.Sprintf ("Encrypted password file %v contains invalid ASN1-encoded params: %v", sEncryptedPasswordFileName, err))
		return
	}

	if sEncPass_HashHexDigits == "" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Encrypted password file %v is missing hash", sEncryptedPasswordFileName))
		return
	}

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("DecryptPass: sPassKey_Transformation=%v sPassKey_Algorithm=%v len(abyKey)=%v sPassKey_Mac=%v len(abyMacKey)=%v len(abyCipherText)=%v len(abyIV)=%v len(sEncPass_HashHexDigits)=%v",
			sPassKey_Transformation, sPassKey_Algorithm, len (abyKey), sPassKey_Mac, len (abyMacKey), len (abyCipherText), len (abyIV), len (sEncPass_HashHexDigits)))
	}

	hasher.Write (abyCipherText)
	hasher.Write ([] byte (sPassKey_Transformation))
	hasher.Write (abyASN1EncodedIV)

	sHashHexDigits := hex.EncodeToString (hasher.Sum (nil))

	if sHashHexDigits != sEncPass_HashHexDigits {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Hash mismatch indicates possible tampering with file %s and/or file %s", sPasswordEncryptionKeyFileName, sEncryptedPasswordFileName))
		return
	}

	ciph, err := aes.NewCipher (abyKey)

	if err != nil {
		err = con.makeDriverError (err, 0, fmt.Sprintf ("Unable to decrypt password file %v", sEncryptedPasswordFileName))
		return
	}

	switch sMode {
		case "CBC" : cipher.NewCBCDecrypter (ciph, abyIV).CryptBlocks  (abyCipherText, abyCipherText)
		case "CFB" : cipher.NewCFBDecrypter (ciph, abyIV).XORKeyStream (abyCipherText, abyCipherText)
		case "OFB" : cipher.NewOFB          (ciph, abyIV).XORKeyStream (abyCipherText, abyCipherText)
	}

	iZeroByte := bytes.IndexByte (abyCipherText, 0)
	if iZeroByte >= 0 {
		abyCipherText = abyCipherText [ : iZeroByte] // no need to worry about PKCS5Padding because it trails the zero-byte padding
	}

	sDecryptedPassword := string (abyCipherText)

	sOutput = sInput [ : iPrefix] + sDecryptedPassword + sInput [iPastSuffix : ]

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("DecryptPass: sDecryptedPassword=%v (len=%v) sOutput=%v (len=%v)", sDecryptedPassword, len (sDecryptedPassword), sOutput, len (sOutput)))
	}

	return

} // end DecryptPass

func (con *TeradataConnection) loadPropertiesFile (sFileName string) (mapOutput map [string] string, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter loadPropertiesFile sFileName=%v %v", sFileName, con))
		defer func () { traceLog (fmt.Sprintf ("< leave loadPropertiesFile sFileName=%v %v", sFileName, con)) } ()
	}

	abyContents, err := os.ReadFile (sFileName)
	if err != nil {
		err = con.makeDriverError (err, 0, fmt.Sprintf ("Unable to load properties file %v", sFileName))
		return
	}

	sFileContents := string (abyContents) // assume the ISO-8859-1 contents are compatible with UTF-8

	mapOutput = map [string] string {}

	for _, sLine := range strings.Split (sFileContents, "\n") {

		sLine = strings.TrimSpace (sLine)

		if len (sLine) > 0 && ! strings.HasPrefix (sLine, "#") {

			asTokens := strings.SplitN (sLine, "=", 2)

			sKey := strings.TrimSpace (asTokens [0])

			sValue := ""
			if len (asTokens) == 2 { sValue = strings.TrimSpace (asTokens [1]) }

			mapOutput [sKey] = sValue

		} // end if
	} // end for

	return

} // end loadPropertiesFile
