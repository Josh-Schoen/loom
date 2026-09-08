// Copyright 2024 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"time"
)

func (con *TeradataConnection) validateBearerFlow () (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter validateBearerFlow %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave validateBearerFlow %v", con)) } ()
	}

	// user and password must be omitted when this logmech is specified
	if con.m_params.M_sUserName != "" {
		err = con.makeDriverError (nil, 552, fmt.Sprintf ("Do not specify user with logmech %v", con.m_params.M_sLogMech))
		return
	}

	if con.m_params.M_sPassword != "" {
		err = con.makeDriverError (nil, 553, fmt.Sprintf ("Do not specify password with logmech %v", con.m_params.M_sLogMech))
		return
	}

	if con.m_params.M_sOAuthScopes != "" {
		err = con.makeDriverError (nil, 554, fmt.Sprintf ("Do not specify oauth_scopes with logmech %v", con.m_params.M_sLogMech))
		return
	}

	return

} // end validateBearerFlow

func (con *TeradataConnection) doBearerFlow (sUserName string, sPassword string, sIdProURL string, sClientID string, sOIDCScope string, bOAuthFlow bool, cc *CacheContext) (err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter doBearerFlow %v bOAuthFlow=%v sOIDCScope=%v", con, bOAuthFlow, sOIDCScope))
		defer func () { traceLog (fmt.Sprintf ("< leave doBearerFlow %v bOAuthFlow=%v sOIDCScope=%v", con, bOAuthFlow, sOIDCScope)) } ()
	}

	// https://datatracker.ietf.org/doc/html/rfc7518#section-3.1

	// 3.1.  "alg" (Algorithm) Header Parameter Values for JWS

	// +--------------+-------------------------------------------------+-----------------+
	// | "alg" Param  | Digital Signature or MAC Algorithm              | Implementation  |
	// | Value        |                                                 | Requirements    |
	// +--------------+-------------------------------------------------+-----------------+
	// | HS256        | HMAC using SHA-256                              | Required        |
	// | HS384        | HMAC using SHA-384                              | Optional        |
	// | HS512        | HMAC using SHA-512                              | Optional        |
	// | RS256        | RSASSA-PKCS1-v1_5 using SHA-256                 | Recommended     |  <-- default
	// | RS384        | RSASSA-PKCS1-v1_5 using SHA-384                 | Optional        |  <-- supported
	// | RS512        | RSASSA-PKCS1-v1_5 using SHA-512                 | Optional        |  <-- supported
	// | ES256        | ECDSA using P-256 and SHA-256                   | Recommended+    |
	// | ES384        | ECDSA using P-384 and SHA-384                   | Optional        |
	// | ES512        | ECDSA using P-521 and SHA-512                   | Optional        |
	// | PS256        | RSASSA-PSS using SHA-256 and MGF1 with SHA-256  | Optional        |
	// | PS384        | RSASSA-PSS using SHA-384 and MGF1 with SHA-384  | Optional        |
	// | PS512        | RSASSA-PSS using SHA-512 and MGF1 with SHA-512  | Optional        |
	// | none         | No digital signature or MAC performed           | Optional        |
	// +--------------+-------------------------------------------------+-----------------+

	switch con.m_params.M_sJWSAlgorithm {

		case "RS256", "RS384", "RS512": // valid values

		default:
			err = con.makeDriverError (nil, 0, fmt.Sprintf ("Invalid argument %v was specified for the jws_algorithm connection parameter", con.m_params.M_sJWSAlgorithm))
			return
	}

	con.m_extra.m_sJWSAlgorithm = con.m_params.M_sJWSAlgorithm

	// The following value would differ if we add support for more algorithms.

	sKeyFactory := "RSA"

	if con.hasDebugLog () {
		debugLog ("doBearerFlow: M_sJWSAlgorithm=" + con.m_params.M_sJWSAlgorithm)
		debugLog ("doBearerFlow: sKeyFactory=" + sKeyFactory)
	}

	// If we add support for HS algorithms, logdata would be required to specify the client secret.

	// logdata must be omitted when this logmech is specified.
	if con.m_params.M_sLogData != "" {
		err = con.makeDriverError (nil, 554, fmt.Sprintf ("Do not specify logdata with logmech %v", con.m_params.M_sLogMech))
		return
	}

	// If we add support for HS algorithms, jws_private_key would not be permitted with an HS algorithm.

	// jws_private_key must be specified when this logmech is specified.
	if con.m_params.M_sJWSPrivateKey == "" {
		err = con.makeDriverError (nil, 0, fmt.Sprintf ("Connection parameter jws_private_key is required with logmech %v", con.m_params.M_sLogMech))
		return
	}

	privkey, sKeyID, err := con.loadPrivateKeyFromFile (sKeyFactory, con.m_params.M_sJWSPrivateKey)
	if err != nil { return }

	// jws_cert is optional when this logmech is specified.
	sX509Thumbprint := ""
	if con.m_params.M_sJWSCert != "" {
		sX509Thumbprint, err = con.loadX509Thumbprint (con.m_params.M_sJWSCert)
		if err != nil { return }
	}

	resp, err := getOIDCMetadataResponse (con, sIdProURL)
	if err != nil { return }

	sJSON := string (resp.M_abyBody)

	if con.hasDebugLog () {
		debugLog ("doBearerFlow: sJSON=" + sJSON)
	}

	sTokenURL, err := con.getStringFromJSON ("token_endpoint", sJSON, 0, "Failed to parse metadata JSON from Identity Provider")
	if err != nil { return }

	if con.hasDebugLog () {
		debugLog ("doBearerFlow: sTokenURL=" + sTokenURL)
	}

	// https://datatracker.ietf.org/doc/html/rfc7515#section-4.1.1

	// 4.1.1. "alg" (Algorithm) Header Parameter

	// The "alg" (algorithm) Header Parameter identifies the cryptographic algorithm used to secure the JWS.
	// ... This Header Parameter MUST be present and MUST be understood and processed by implementations.

	// 4.1.4. "kid" (Key ID) Header Parameter

	// The "kid" (key ID) Header Parameter is a hint indicating which key was used to secure the JWS. This parameter allows originators to explicitly signal a change of key to recipients.
	// The structure of the "kid" value is unspecified. Its value MUST be a case-sensitive string. Use of this Header Parameter is OPTIONAL.
	// When used with a JWK, the "kid" value is used to match a JWK "kid" parameter value.

	// https://datatracker.ietf.org/doc/html/rfc7519#section-5.1

	// 5.1. "typ" (Type) Header Parameter

	// The "typ" (type) Header Parameter defined by [JWS] and [JWE] is used by JWT applications to declare the media type [IANA.MediaTypes] of this complete JWT.
	// ... If present, it is RECOMMENDED that its value be "JWT" to indicate that this object is a JWT.

	// https://datatracker.ietf.org/doc/html/rfc7515#section-4.1.7

	// 4.1.7. "x5t" (X.509 Certificate SHA-1 Thumbprint) Header Parameter

	// The "x5t" (X.509 certificate SHA-1 thumbprint) Header Parameter is a base64url-encoded SHA-1 thumbprint (a.k.a. digest) of the DER
	// encoding of the X.509 certificate [RFC5280] corresponding to the key used to digitally sign the JWS.  Note that certificate thumbprints
	// are also sometimes known as certificate fingerprints.  Use of this Header Parameter is OPTIONAL.

	sJWTHeader := "{" +
		`"alg": "` + con.m_params.M_sJWSAlgorithm + `"` +
		ternary (sKeyID != "",
		`, "kid": "` + sKeyID + `"`, "") +
		`, "typ": "JWT"` +
		ternary (sX509Thumbprint != "",
		`, "x5t": "` + sX509Thumbprint + `"`, "") +
		"}"

	con.m_extra.m_sJWTHeaderParams = joinStrings ( // list of JWT header parameters included to help IdP select JWT signature key
		ternary (sKeyID          != "", "kid", ""),
		",",
		ternary (sX509Thumbprint != "", "x5t", ""))

	// https://datatracker.ietf.org/doc/html/rfc7523#section-3

	// 1. The JWT MUST contain an "iss" (issuer) claim that contains a unique identifier for the entity that issued the JWT.

	sIssuer := sClientID

	// 2. The JWT MUST contain a "sub" (subject) claim that identifies the principal that is the subject of the JWT.
	// ... For client authentication, the subject MUST be the "client_id" of the OAuth client.

	sSubject := sClientID

	// 3. The JWT MUST contain an "aud" (audience) claim containing a value that identifies the authorization server as an intended audience.
	// The token endpoint URL of the authorization server MAY be used as a value for an "aud" element to identify the authorization server as an intended audience of the JWT.

	sAudience := sTokenURL

	// 4. The JWT MUST contain an "exp" (expiration time) claim that limits the time window during which the JWT can be used.

	sExpiration := itoa (int (time.Now ().Add (time.Second * 60).Unix ())) // 60 seconds from now

	// 7. The JWT MAY contain a "jti" (JWT ID) claim that provides a unique identifier for the token.

	sUniqueID := base64.RawURLEncoding.EncodeToString (randomBytes (16))

	sPayload := "{" +
		  `"iss": "` + sIssuer     + `"` +
		`, "sub": "` + sSubject    + `"` +
		`, "aud": "` + sAudience   + `"` +
		`, "exp": `  + sExpiration +
		`, "jti": "` + sUniqueID   + `"` +
		"}"

	if con.hasDebugLog () {
		debugLog ("doBearerFlow: sIssuer=" + sIssuer)
		debugLog ("doBearerFlow: sSubject=" + sSubject)
		debugLog ("doBearerFlow: sAudience=" + sAudience)
		debugLog ("doBearerFlow: sExpiration=" + sExpiration)
		debugLog ("doBearerFlow: sUniqueID=" + sUniqueID)
		debugLog ("doBearerFlow: sJWTHeader=" + sJWTHeader)
		debugLog ("doBearerFlow: sPayload=" + sPayload)
	}

	// https://datatracker.ietf.org/doc/html/rfc7521#section-4.2

	// When using assertions as client credentials, the client includes the assertion and related information using the following HTTP request parameters:
	// client_assertion_type  REQUIRED  The format of the assertion as defined by the authorization server.  The value will be an absolute URI.
	// client_assertion       REQUIRED  The assertion being used to authenticate the client. Specific serialization of the assertion is defined by profile documents.
	// client_id              OPTIONAL  ... The "client_id" is unnecessary for client assertion authentication because the client is identified by the subject of the assertion.

	// https://datatracker.ietf.org/doc/html/rfc7523#section-2.2

	// To use a JWT Bearer Token for client authentication, the client uses the following parameter values and encodings.
	// The value of the "client_assertion_type" is "urn:ietf:params:oauth:client-assertion-type:jwt-bearer".
	// The value of the "client_assertion" parameter contains a single JWT. It MUST NOT contain more than one JWT.

	sSignedJWT, err := con.makeJWT (sJWTHeader, sPayload, con.m_params.M_sJWSAlgorithm, privkey)
	if err != nil {
		err = con.makeDriverError (err, 0, fmt.Sprintf ("Failed to construct JWT Bearer Token for logmech %v", con.m_params.M_sLogMech))
		return
	}

	sFormData := "grant_type=client_credentials" +
		"&scope=" + url.QueryEscape (sOIDCScope) +
		"&client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer" +
		"&client_assertion=" + sSignedJWT

	if con.hasDebugLog () {
		debugLog ("doBearerFlow: sSignedJWT=" + sSignedJWT)
		debugLog ("doBearerFlow: sFormData=" + sFormData)
	}

	resp, err = con.doHttpRequest ("POST", sTokenURL, [][] string {{ "Content-Type", "application/x-www-form-urlencoded" }}, [] byte (sFormData), /*anRequiredRespCodes*/ [] int {200})
	if err != nil {
		err = con.makeDriverError (err, 567, "Failed to obtain token from Identity Provider")
		return
	}

	sJSON = string (resp.M_abyBody)

	err = con.putCacheEntry (sUserName, sPassword, sIdProURL, sClientID, sOIDCScope, bOAuthFlow, sTokenURL, sJSON, cc)
	return

} // end doBearerFlow
