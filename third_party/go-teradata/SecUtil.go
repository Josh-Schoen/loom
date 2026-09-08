// Copyright 2021 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"strings"
)

func randomBytes (nByteCount int) (aby [] byte) {

	aby = make ([] byte, nByteCount)
	rand.Read (aby) // never returns an error and always entirely fills the specified slice with cryptographically secure random bytes
	return

} // end randomBytes

func hashBytesSHA1 (abyInput [] byte) [] byte {

	abyOutput := sha1.Sum (abyInput) // byte array must be "addressible" in order to be converted to slice
	return abyOutput [ : ] // convert byte array to byte slice

} // end hashBytesSHA1

func hashBytesSHA256 (abyInput [] byte) [] byte {

	abyOutput := sha256.Sum256 (abyInput) // byte array must be "addressible" in order to be converted to slice
	return abyOutput [ : ] // convert byte array to byte slice

} // end hashBytesSHA256

func hashBytesSHA384 (abyInput [] byte) [] byte {

	abyOutput := sha512.Sum384 (abyInput) // byte array must be "addressible" in order to be converted to slice
	return abyOutput [ : ] // convert byte array to byte slice

} // end hashBytesSHA384

func hashBytesSHA512 (abyInput [] byte) [] byte {

	abyOutput := sha512.Sum512 (abyInput) // byte array must be "addressible" in order to be converted to slice
	return abyOutput [ : ] // convert byte array to byte slice

} // end hashBytesSHA512

func (con *TeradataConnection) signBytes (sAlgorithm string, privkey *rsa.PrivateKey, abyInput [] byte) (abyOutput [] byte, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter signBytes %v sAlgorithm=%v", con, sAlgorithm))
		defer func () { traceLog (fmt.Sprintf ("< leave signBytes %v sAlgorithm=%v", con, sAlgorithm)) } ()
	}

	switch sAlgorithm {
		case "RS256": abyOutput, err = rsa.SignPKCS1v15 (nil, privkey, crypto.SHA256, hashBytesSHA256 (abyInput))
		case "RS384": abyOutput, err = rsa.SignPKCS1v15 (nil, privkey, crypto.SHA384, hashBytesSHA384 (abyInput))
		case "RS512": abyOutput, err = rsa.SignPKCS1v15 (nil, privkey, crypto.SHA512, hashBytesSHA512 (abyInput))
	}

	return

} // end method signBytes

func (con *TeradataConnection) makeJWT (sHeader string, sPayload string, sSignatureAlgorithm string, privkey *rsa.PrivateKey) (sOutput string, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter makeJWT %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave makeJWT %v", con)) } ()
	}

	sEncodedHeader    := base64.RawURLEncoding.EncodeToString ([] byte (sHeader))
	sEncodedPayload   := base64.RawURLEncoding.EncodeToString ([] byte (sPayload))
	sUnsignedJWT      := sEncodedHeader + "." + sEncodedPayload

	abySignature, err := con.signBytes (sSignatureAlgorithm, privkey, [] byte (sUnsignedJWT))
	if err != nil { return }

	sEncodedSignature := base64.RawURLEncoding.EncodeToString (abySignature)
	sOutput            = sUnsignedJWT + "." + sEncodedSignature

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("makeJWT: sHeader=%v", sHeader))
		debugLog (fmt.Sprintf ("makeJWT: sPayload=%v", sPayload))
		debugLog (fmt.Sprintf ("makeJWT: sSignatureAlgorithm=%v", sSignatureAlgorithm))
		debugLog (fmt.Sprintf ("makeJWT: privkey=%+v", privkey))
		debugLog (fmt.Sprintf ("makeJWT: sEncodedHeader=%v", sEncodedHeader))
		debugLog (fmt.Sprintf ("makeJWT: sEncodedPayload=%v", sEncodedPayload))
		debugLog (fmt.Sprintf ("makeJWT: sUnsignedJWT=%v", sUnsignedJWT))
		debugLog (fmt.Sprintf ("makeJWT: len(abySignature)=%v bits=%v", len (abySignature), len (abySignature) * 8)) // signature length will match private key length
		debugLog (fmt.Sprintf ("makeJWT: sEncodedSignature=%v", sEncodedSignature))
		debugLog (fmt.Sprintf ("makeJWT: sOutput=%v", sOutput))
	}

	return

} // end method makeJWT

func (con *TeradataConnection) loadPrivateKeyFromPEMFile (sKeyFactory string, sFileName string) (privkey *rsa.PrivateKey, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter loadPrivateKeyFromPEMFile %v sKeyFactory=%v sFileName=%v", con, sKeyFactory, sFileName))
		defer func () { traceLog (fmt.Sprintf ("< leave loadPrivateKeyFromPEMFile %v sKeyFactory=%v sFileName=%v", con, sKeyFactory, sFileName)) } ()
	}

	abyContents, err := os.ReadFile (sFileName)
	if err != nil {
		err = con.makeDriverError (err, 0, fmt.Sprintf ("Unable to read private key from PEM file %v", sFileName))
		return
	}

	if con.hasDebugLog () {
		debugLogHexDump ("loadPrivateKeyFromPEMFile: abyContents=", abyContents)
	}

	block, _ := pem.Decode (abyContents)

	anyprivkey, err := x509.ParsePKCS8PrivateKey (block.Bytes) // PKCS #8 format begins with -----BEGIN PRIVATE KEY-----
	if err != nil {
		err = con.makeDriverError (err, 0, fmt.Sprintf ("Unable to parse private key from PEM file %v", sFileName))
		return
	}

	if sKeyFactory == "RSA" {
		privkey = anyprivkey.(*rsa.PrivateKey) // other currently unsupported possibilities are ecdsa.PrivateKey or ed25519.PrivateKey
	}

	if privkey == nil {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Cannot obtain %v private key from PEM file %v", sKeyFactory, sFileName))
		return
	}

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("loadPrivateKeyFromPEMFile: output privkey=%+v", privkey))
	}

	return

} // end method loadPrivateKeyFromPEMFile

func (con *TeradataConnection) getBytesBase64FromJSON (mapTokens map [string] any, sKey string, sFileName string) (abyOutput [] byte, err error) {

	oValue, bPresent := mapTokens [sKey]
	if ! bPresent {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("File %v does not contain required %v value", sFileName, sKey))
		return
	}

	sValue, bIsString := oValue.(string)
	if ! bIsString {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("File %v contains non-string %v value %v", sFileName, sKey, oValue))
		return
	}

	abyOutput, err = base64.RawURLEncoding.DecodeString (sValue)
	if err != nil {
		err = con.makeDriverError (err, 0, fmt.Sprintf ("File %v contains invalid %v value %v", sFileName, sKey, sValue))
		return
	}

	return

} // end method getBytesBase64FromJSON

func (con *TeradataConnection) getBigIntBase64FromJSON (mapTokens map [string] any, sKey string, sFileName string) (nOutput *big.Int, err error) {

	aby, err := con.getBytesBase64FromJSON (mapTokens, sKey, sFileName)
	if err != nil { return }

	n := convertByteSliceToUnsignedBigInt (aby)
	nOutput = &n
	return

} // end method getBigIntBase64FromJSON

func (con *TeradataConnection) getIntBase64FromJSON (mapTokens map [string] any, sKey string, sFileName string) (nOutput int, err error) {

	n, err := con.getBigIntBase64FromJSON (mapTokens, sKey, sFileName)
	if err != nil { return }

	if ! n.IsInt64 () {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("File %v contains invalid %v value %v", sFileName, sKey, n))
		return
	}

	nOutput = int (n.Int64 ())
	return

} // end method getIntBase64FromJSON

func (con *TeradataConnection) loadPrivateKeyFromJWKFile (sKeyFactory string, sFileName string) (privkey *rsa.PrivateKey, sKeyID string, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter loadPrivateKeyFromJWKFile %v sKeyFactory=%v sFileName=%v", con, sKeyFactory, sFileName))
		defer func () { traceLog (fmt.Sprintf ("< leave loadPrivateKeyFromJWKFile %v sKeyFactory=%v sFileName=%v", con, sKeyFactory, sFileName)) } ()
	}

	abyJSON, err := os.ReadFile (sFileName)
	if err != nil {
		err = con.makeDriverError (err, 0, fmt.Sprintf ("Unable to read private key from JWK file %v", sFileName))
		return
	}

	sJSON := string (abyJSON)

	if con.hasDebugLog () {
		debugLogHexDump ("loadPrivateKeyFromJWKFile: abyJSON=", abyJSON)
		debugLog ("loadPrivateKeyFromJWKFile: sJSON=" + sJSON)
	}

	var jwk struct {
		M_sKeyType string `json:"kty"`
		M_sKeyID   string `json:"kid"`
	}

	err = shredJSON (sJSON, &jwk)
	if err != nil {
		err = con.makeDriverError (err, 0, fmt.Sprintf ("Unable to parse JWK file %v", sFileName))
		return
	}

	mapTokens, err := makeMapFromJSON (sJSON)
	if err != nil {
		err = con.makeDriverError (err, 0, fmt.Sprintf ("Unable to parse JWK file %v", sFileName))
		return
	}

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("loadPrivateKeyFromJWKFile: jwk=%+v", jwk))
		debugLog (fmt.Sprintf ("loadPrivateKeyFromJWKFile: mapTokens=%v", mapTokens))
	}

	nModulus        , err := con.getBigIntBase64FromJSON (mapTokens, "n" , sFileName) ; if err != nil { return }
	nPublicExponent , err := con.getIntBase64FromJSON    (mapTokens, "e" , sFileName) ; if err != nil { return }
	nPrivateExponent, err := con.getBigIntBase64FromJSON (mapTokens, "d" , sFileName) ; if err != nil { return }
	nPrimeP         , err := con.getBigIntBase64FromJSON (mapTokens, "p" , sFileName) ; if err != nil { return }
	nPrimeQ         , err := con.getBigIntBase64FromJSON (mapTokens, "q" , sFileName) ; if err != nil { return }
	nPrimeExponentP , err := con.getBigIntBase64FromJSON (mapTokens, "dp", sFileName) ; if err != nil { return }
	nPrimeExponentQ , err := con.getBigIntBase64FromJSON (mapTokens, "dq", sFileName) ; if err != nil { return }
	nCrtCoefficient , err := con.getBigIntBase64FromJSON (mapTokens, "qi", sFileName) ; if err != nil { return }

	if sKeyFactory != jwk.M_sKeyType {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Private key file %v has unsupported key type %v", sFileName, jwk.M_sKeyType))
		return
	}

	// https://datatracker.ietf.org/doc/html/rfc7515#section-4.1.4

	// 4.1.4. "kid" (Key ID) Header Parameter

	// The "kid" (key ID) Header Parameter is a hint indicating which key was used to secure the JWS. This parameter allows originators to explicitly signal a change of key to recipients.
	// The structure of the "kid" value is unspecified. Its value MUST be a case-sensitive string. Use of this Header Parameter is OPTIONAL.
	// When used with a JWK, the "kid" value is used to match a JWK "kid" parameter value.

	sKeyID = jwk.M_sKeyID

	// https://datatracker.ietf.org/doc/html/rfc7517#section-9.3

	// 9.3. RSA Private Key Representations and Blinding

	// The RSA Key blinding operation [Kocher], which is a defense against some timing attacks, requires all of the RSA key values "n", "e", and "d".
	// However, some RSA private key representations do not include the public exponent "e", but only include the modulus "n" and the private exponent "d".
	// This is true, for instance, of the Java RSAPrivateKeySpec API, which does not include the public exponent "e" as a parameter.
	// So as to enable RSA key blinding, such representations should be avoided. For Java, the RSAPrivateCrtKeySpec API can be used instead.

	privkey = &rsa.PrivateKey {

		PublicKey: rsa.PublicKey {
			N: nModulus,
			E: nPublicExponent,
		},

		D: nPrivateExponent,

		Primes: [] *big.Int {
			nPrimeP,
			nPrimeQ,
		},

		Precomputed: rsa.PrecomputedValues {
			Dp:   nPrimeExponentP,
			Dq:   nPrimeExponentQ,
			Qinv: nCrtCoefficient,
		},
	}

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("loadPrivateKeyFromJWKFile: output sKeyID=%v", sKeyID))
		debugLog (fmt.Sprintf ("loadPrivateKeyFromJWKFile: output privkey=%+v", privkey))
	}

	return

} // end method loadPrivateKeyFromJWKFile

func (con *TeradataConnection) loadPrivateKeyFromFile (sKeyFactory string, sFileName string) (privkey *rsa.PrivateKey, sKeyID string, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter loadPrivateKeyFromFile %v sKeyFactory=%v sFileName=%v", con, sKeyFactory, sFileName))
		defer func () { traceLog (fmt.Sprintf ("< leave loadPrivateKeyFromFile %v sKeyFactory=%v sFileName=%v", con, sKeyFactory, sFileName)) } ()
	}

	if strings.HasSuffix (sFileName, ".pem") {

		privkey, err = con.loadPrivateKeyFromPEMFile (sKeyFactory, sFileName)

	} else if strings.HasSuffix (sFileName, ".jwk") {

		privkey, sKeyID, err = con.loadPrivateKeyFromJWKFile (sKeyFactory, sFileName)

	} else {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Private key file %v is missing a supported file extension .pem or .jwk", sFileName))
	}

	return

} // end method loadPrivateKeyFromFile

func (con *TeradataConnection) loadX509Thumbprint (sFileName string) (sOutput string, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter loadX509Thumbprint %v sFileName=%v", con, sFileName))
		defer func () { traceLog (fmt.Sprintf ("< leave loadX509Thumbprint %v sFileName=%v", con, sFileName)) } ()
	}

	abyContents, err := os.ReadFile (sFileName)
	if err != nil {
		err = con.makeDriverError (err, 0, fmt.Sprintf ("Unable to read X.509 certificate from file %v", sFileName))
		return
	}

	if con.hasDebugLog () {
		debugLogHexDump ("loadX509Thumbprint: abyContents=", abyContents)
	}

	block, _ := pem.Decode (abyContents)

	cert, err := x509.ParseCertificate (block.Bytes)
	if err != nil {
		err = con.makeDriverError (err, 0, fmt.Sprintf ("Unable to parse X.509 certificate from file %v", sFileName))
		return
	}

	if con.hasDebugLog () {
		debugLog (fmt.Sprintf ("loadX509Thumbprint: cert=%+v", cert))
	}

	// https://datatracker.ietf.org/doc/html/rfc7515#section-4.1.7

	// 4.1.7. "x5t" (X.509 Certificate SHA-1 Thumbprint) Header Parameter

	// The "x5t" (X.509 certificate SHA-1 thumbprint) Header Parameter is a base64url-encoded SHA-1 thumbprint (a.k.a. digest) of the DER
	// encoding of the X.509 certificate [RFC5280] corresponding to the key used to digitally sign the JWS.  Note that certificate thumbprints
	// are also sometimes known as certificate fingerprints.  Use of this Header Parameter is OPTIONAL.

	abyThumbprint := hashBytesSHA1 (cert.Raw)

	sOutput = base64.RawURLEncoding.EncodeToString (abyThumbprint)

	if con.hasDebugLog () {
		debugLog ("loadX509Thumbprint: abyThumbprint=" + formatByteSliceAsHexDigits (abyThumbprint, /*sNilValue*/ ""))
		debugLog ("loadX509Thumbprint: sOutput=" + sOutput)
	}

	return

} // end method loadX509Thumbprint
