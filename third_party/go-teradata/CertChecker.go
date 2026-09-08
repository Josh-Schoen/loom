// Copyright 2022-2023 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"golang.org/x/crypto/ocsp"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"time"
)

var certpoolSystem = &x509.CertPool {} // argument that directs getServerCertProblems to use x509.VerifyOptions { Roots : nil } which uses the system roots or the platform verifier

type certRevStatus struct {
	bGood      bool
	bRevoked   bool
	errs       [] error
}

func (status *certRevStatus) setGood    ()      { status.bGood = true }
func (status *certRevStatus) setRevoked ()      { status.bRevoked = true }
func (status *certRevStatus) known      () bool { return status.bGood || status.bRevoked }

func checkCertForThread (con *TeradataConnection, thread *ConnectThread) { // sets thread.m_err in case of error

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter checkCertForThread %v thread=%+v", con, thread))
		defer func () { traceLog (fmt.Sprintf ("< leave checkCertForThread %v thread=%+v", con, thread)) } ()
	}

	thread.m_sCertStatus, thread.m_err = checkCert (con.m_params, con.m_params.m_nSSLModeLevel, thread.m_socket.(*tls.Conn), thread.m_sockAddr)

} // end checkCertForThread

func checkCert (params *ConParams, nSSLModeLevel int, sock *tls.Conn, addrDest socketAddress) (sCertStatus string, err error) {

	if params.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter checkCert nSSLModeLevel=%v addrDest=%v", nSSLModeLevel, addrDest))
		defer func () { traceLog (fmt.Sprintf ("< leave checkCert nSSLModeLevel=%v addrDest=%v", nSSLModeLevel, addrDest)) } ()
	}

	state := sock.ConnectionState ()

	if params.hasDebugLog () {
		debugLog (fmt.Sprintf ("ConnectionState.Version=0x%04x %v CipherSuite=0x%04x %v", state.Version, TLSVersionName (state.Version), state.CipherSuite, tls.CipherSuiteName (state.CipherSuite)))

		for iCert, cert := range state.PeerCertificates {
			debugLog ("state.PeerCertificates " + itoa (iCert + 1) + " of " + itoa (len (state.PeerCertificates)) + ternary (iCert == 0, " which is the peer certificate", "") +
				"\n  Certificate date range is " + formatDateRange (cert) +
				"\n  Certificate " + formatValidity (cert) +
				"\n  Certificate purpose is " + formatCertPurpose (cert) +
				"\n  Subject Distinguished Name (DN) is " + formatDN (cert) +
				"\n  Subject Common Name (CN) is " + formatCN (cert) +
				"\n  Subject Alternative Names (SANs) are " + formatSANs (cert) +
				"\n" + formatCert (cert))
		} // end for iCert
	} // end if hasDebugLog

	sAbbrevBase64 := abbreviateString (params.M_sSSLBase64, 50)
	sTitleBase64  := "sslbase64=" + sAbbrevBase64
	sTitlePEMFile := "sslca=" + params.M_sSSLCAFile
	sTitlePEMDir  := "sslcapath=" + params.M_sSSLCAPath
	sTitleSystem  := "System"

	certpoolBase64, err := createCertPoolFromBase64 (params, params.M_sSSLBase64, sTitleBase64)
	if err != nil { return }

	certpoolPEMFile, err := createCertPoolFromPEMFile (params, params.M_sSSLCAFile, sTitlePEMFile)
	if err != nil { return }

	certpoolPEMDir, err := createCertPoolFromPEMDirectory (params, params.M_sSSLCAPath, sTitlePEMDir)
	if err != nil { return }

	aValidityProblems := getValidityProblems (state.PeerCertificates)
	verifiedChainsBase64 , errBase64  := getServerCertProblems (sTitleBase64 , certpoolBase64 , state.PeerCertificates)
	verifiedChainsPEMFile, errPEMFile := getServerCertProblems (sTitlePEMFile, certpoolPEMFile, state.PeerCertificates)
	verifiedChainsPEMDir , errPEMDir  := getServerCertProblems (sTitlePEMDir , certpoolPEMDir , state.PeerCertificates)
	verifiedChainsSystem , errSystem  := getServerCertProblems (sTitleSystem , certpoolSystem , state.PeerCertificates)

	if params.hasDebugLog () {
		debugLog ("getValidityProblems " +
			ternary (len (aValidityProblems) == 0, "accepted certificate",
			fmt.Sprintf ("found %v certificate problem(s): %v", len (aValidityProblems), aValidityProblems)))

		debugLog ("createCertPoolFromBase64(" + sAbbrevBase64 + ") " +
			ternary (certpoolBase64 == nil, "is not available",
			ternary (errBase64 == nil, "accepted certificate",
			fmt.Sprintf ("found certificate problem: %v", errBase64))))

		debugLog ("createCertPoolFromPEMFile(" + params.M_sSSLCAFile + ") " +
			ternary (certpoolPEMFile == nil, "is not available",
			ternary (errPEMFile == nil, "accepted certificate",
			fmt.Sprintf ("found certificate problem: %v", errPEMFile))))

		debugLog ("createCertPoolFromPEMDirectory(" + params.M_sSSLCAPath + ") " +
			ternary (certpoolPEMDir == nil, "is not available",
			ternary (errPEMDir == nil, "accepted certificate",
			fmt.Sprintf ("found certificate problem: %v", errPEMDir))))

		debugLog ("System verifier " +
			ternary (errSystem == nil, "accepted certificate",
			fmt.Sprintf ("found certificate problem: %v", errSystem)))
	} // end if hasDebugLog

	bTrustedByBase64  := certpoolBase64  != nil && errBase64  == nil
	bTrustedByPEMFile := certpoolPEMFile != nil && errPEMFile == nil
	bTrustedByPEMDir  := certpoolPEMDir  != nil && errPEMDir  == nil
	bTrustedBySystem  :=                           errSystem  == nil // certpoolSystem is always available

	bAtLeastOneAccepted := bTrustedByBase64 || bTrustedByPEMFile || bTrustedByPEMDir || bTrustedBySystem

	// sslcrc=ALLOW   --> perform CRC for VERIFY-CA and VERIFY-FULL, soft fail for VERIFY-CA and VERIFY-FULL (avoids CRC performance hit with default sslmode and default sslcrc)
	// sslcrc=PREFER  --> perform CRC for all HTTPS, soft fail for VERIFY-CA and VERIFY-FULL (provides CRC telemetry without enforcing CRC for default sslmode)
	// sslcrc=REQUIRE --> perform CRC for all HTTPS, require for VERIFY-CA and VERIFY-FULL

	bPerformCRC := nSSLModeLevel >= LEVEL_VERIFY_CA ||
		params.M_sSSLCRC == "PREFER" ||
		params.M_sSSLCRC == "REQUIRE"

	bCRCSoftFail := params.M_sSSLCRC != "REQUIRE"

	if params.hasDebugLog () {
		debugLog (fmt.Sprintf ("checkCert: nSSLModeLevel=%v M_sSSLCRC=%v bPerformCRC=%v bCRCSoftFail=%v", nSSLModeLevel, params.M_sSSLCRC, bPerformCRC, bCRCSoftFail))
	}

	status := &certRevStatus {}
	if bPerformCRC {
		// To minimize performance impact, stop when the first CertPool says known to be good or known to be revoked.
		if bTrustedByBase64  && ! status.known () { checkRevocationStatus (params, status, sTitleBase64 , verifiedChainsBase64 ) }
		if bTrustedByPEMFile && ! status.known () { checkRevocationStatus (params, status, sTitlePEMFile, verifiedChainsPEMFile) }
		if bTrustedByPEMDir  && ! status.known () { checkRevocationStatus (params, status, sTitlePEMDir , verifiedChainsPEMDir) }
		if bTrustedBySystem  && ! status.known () { checkRevocationStatus (params, status, sTitleSystem , verifiedChainsSystem) }
	}

	bStatusOkay := func () bool { if bCRCSoftFail {
		return ! status.bRevoked } else { // soft fail checking passes when not positively revoked
		return status.bGood } } () // hard fail checking passes when positively good

	bVerifyCA := len (aValidityProblems) == 0 && bAtLeastOneAccepted && bStatusOkay
	if params.hasDebugLog () {
		debugLog ("VERIFY-CA would " + ternary (bVerifyCA, "accept", "reject") + " this connection")
	}

	var cert *x509.Certificate
	if len (state.PeerCertificates) > 0 { cert = state.PeerCertificates [0] }

	bMissingHostName := addrDest.m_bLiteralIP
	sVerifyHostName  := ternary (bMissingHostName, "", addrDest.m_sOriginalHostName)
	sVerifyResolved  := ternary (bMissingHostName, "", addrDest.m_sResolvedHostName)
	sVerifyIPAddress := hostFromAddr (sock.RemoteAddr ())

	aHostNameProblems  := getCertHostNameProblems (params, cert, "Hostname"  , sVerifyHostName)
	aResolvedProblems  := getCertHostNameProblems (params, cert, "Resolved"  , sVerifyResolved)
	aIPAddressProblems := getCertHostNameProblems (params, cert, "IP Address", sVerifyIPAddress)

	bMatchesHostName  := len (aHostNameProblems ) == 0
	bMatchesResolved  := len (aResolvedProblems ) == 0 // for thread.m_sCertStatus only because bVerifyFull does not consider bMatchesResolved
	bMatchesIPAddress := len (aIPAddressProblems) == 0

	bVerifyFull := bVerifyCA && (bMatchesHostName || bMatchesIPAddress)
	if params.hasDebugLog () {
		debugLog ("VERIFY-FULL would " + ternary (bVerifyFull, "accept", "reject") + " this connection")
	}

	sCertStatus = ternary (len (aValidityProblems) == 0, "V", "I") + // Valid or Invalid
		/* Base64      */ ",B" + ternary (certpoolBase64  == nil, "U" /*Unavailable*/, ternary (bTrustedByBase64 , "A", "R")) + // Accepted or Rejected
		/* PEM File    */ ",P" + ternary (certpoolPEMFile == nil, "U" /*Unavailable*/, ternary (bTrustedByPEMFile, "A", "R")) + // Accepted or Rejected
		/* PEM Dir     */ ",D" + ternary (certpoolPEMDir  == nil, "U" /*Unavailable*/, ternary (bTrustedByPEMDir , "A", "R")) + // Accepted or Rejected
		/* System      */ ",T" + ternary (bTrustedBySystem      , "A", "R") + // Accepted or Rejected
		/* Verify-CA   */ ",C" + ternary (bVerifyCA             , "Y", "N") +
		/* Hostname    */ ",H" + ternary (bMissingHostName      , "U" /*Unavailable*/, ternary (bMatchesHostName, "Y", "N")) +
		/* Resolved    */ ",R" + ternary (bMissingHostName      , "U" /*Unavailable*/, ternary (bMatchesResolved, "Y", "N")) + // informational only because bVerifyFull does not consider bMatchesResolved
		/* IP address  */ ",I" + ternary (bMatchesIPAddress     , "Y", "N") +
		/* Verify-Full */ ",F" + ternary (bVerifyFull           , "Y", "N") +
		/* Status      */ ",S" + ternary (status.bGood          , "G" /*Good*/, ternary (status.bRevoked, "R", "U")) // Revoked or Unavailable

	if params.hasDebugLog () {
		debugLog ("sCertStatus=" + sCertStatus)
	}

	bFailedVerifyCA   := nSSLModeLevel == LEVEL_VERIFY_CA   && ! bVerifyCA
	bFailedVerifyFull := nSSLModeLevel == LEVEL_VERIFY_FULL && ! bVerifyFull

	if bFailedVerifyCA || bFailedVerifyFull {

		errs := [] error {}

		if ! bAtLeastOneAccepted {
			if errBase64  != nil { errs = append (errs, errBase64) }
			if errPEMFile != nil { errs = append (errs, errPEMFile) }
			if errPEMDir  != nil { errs = append (errs, errPEMDir) }
			if errSystem  != nil { errs = append (errs, errSystem) }
		}

		if ! bStatusOkay {
			errs = append (errs, status.errs ...)
		}

		if bFailedVerifyFull {
			errs = append (errs, aHostNameProblems  ...)
			errs = append (errs, aIPAddressProblems ...)
		}

		err = formatError (enumerateErrors (errs), "TLS certificate verification failed for " + ternary (bFailedVerifyFull, "VERIFY-FULL", "VERIFY-CA"))
	}

	return

} // end checkCert

func getCertHostNameProblems (params *ConParams, cert *x509.Certificate, sTitle string, sHostName string) (errs [] error) {

	if (cert == nil) {
		errs = append (errs, errors.New ("Peer certificate is not available to match Subject CN or SANs"))
	}

	if (sHostName == "") {
		errs = append (errs, errors.New (sTitle + " is not available to match Subject CN or SANs"))
	}

	if cert != nil && sHostName != "" {

		sCN := cert.Subject.CommonName
		bMatchesCN := sCN != "" && matchesPatternIgnoreCase (sHostName, convertHostNamePatternToRegexPattern (sCN))

		if params.hasDebugLog () {
			debugLog (sTitle + " " + sHostName + " " + ternary (bMatchesCN, "matches", "does not match") + " Subject CN" + ternary (sCN != "", " " + sCN, ""))
		}

		asSANs        := extractSANsFromCert (cert)
		asSANsMatches := findMatchingPatterns (sHostName, convertHostNamePatternsToRegexPatterns (asSANs))

		if params.hasDebugLog () {
			debugLog (sTitle + " " + sHostName + " " + ternary (len (asSANsMatches) == 0, "does not match any SANs", fmt.Sprintf ("matches SANs %v", asSANsMatches)))
		}

		if ! bMatchesCN && len (asSANsMatches) == 0 {

			if ! bMatchesCN {
				errs = append (errs, errors.New (sTitle + " " + sHostName + ternary (sCN != "", " does not match Subject CN " + sCN, " cannnot match missing Subject CN")))
			}

			if len (asSANsMatches) == 0 {
				errs = append (errs, errors.New (sTitle + " " + sHostName + fmt.Sprintf (" does not match any SANs from certificate: %v", asSANs)))
			}
		}
	} // end if

	return

} // end getCertHostNameProblems

/*
	The following info is relevant to SSLMODE=VERIFY-FULL

	Excerpt from RFC 2818 ("HTTP Over TLS") Section 3.1 "Server Identity"
	https://www.ietf.org/rfc/rfc2818.txt

	If a subjectAltName extension of type dNSName is present, that MUST
	be used as the identity. Otherwise, the (most specific) Common Name
	field in the Subject field of the certificate MUST be used. Although
	the use of the Common Name is existing practice, it is deprecated and
	Certification Authorities are encouraged to use the dNSName instead.

	Matching is performed using the matching rules specified by
	[RFC2459].  If more than one identity of a given type is present in
	the certificate (e.g., more than one dNSName name, a match in any one
	of the set is considered acceptable.) Names may contain the wildcard
	character * which is considered to match any single domain name
	component or component fragment. E.g., *.a.com matches foo.a.com but
	not bar.foo.a.com. f*.com matches foo.com but not bar.com.

	In some cases, the URI is specified as an IP address rather than a
	hostname. In this case, the iPAddress subjectAltName must be present
	in the certificate and must exactly match the IP in the URI.

	Excerpt from RFC 2459 ("Internet X.509 Public Key Infrastructure")
	Section 4.2.1.7 "Subject Alternative Name" https://www.ietf.org/rfc/rfc2459.txt

	When the subjectAltName extension contains a domain name service
	label, the domain name MUST be stored in the dNSName (an IA5String).
	The name MUST be in the "preferred name syntax," as specified by RFC
	1034 [RFC 1034]. Note that while upper and lower case letters are
	allowed in domain names, no signifigance is attached to the case.
*/

var regexNonAlphaNumeric   = regexp.MustCompile (`[\W]`)
var regexBackslashAsterisk = regexp.MustCompile (`\\\*`)

func convertHostNamePatternToRegexPattern (s string) string {

	// Step 1 replaces every occurrence of a non-alphanumeric character with backslash followed by the matched character. In other words, step 1 prefixes every non-alphanumeric character with a backslash.
	// Step 1 changes foo.bar.com to foo\.bar\.com

	// Step 2 replaces every sequence of backslash asterisk with the pattern [^.]*
	// Step 2 changes \*\.bar\.com to [^.]*\.bar\.com

	return regexBackslashAsterisk.ReplaceAllString (regexNonAlphaNumeric.ReplaceAllString (s, `\$0`), "[^.]*")

} // convertHostNamePatternToRegexPattern

func convertHostNamePatternsToRegexPatterns (asInput [] string) (asOutput [] string) {

	for _, s := range asInput {
		asOutput = append (asOutput, convertHostNamePatternToRegexPattern (s))
	}

	return

} // convertHostNamePatternsToRegexPatterns

func findMatchingPatterns (sInput string, asPatterns [] string) (asOutput [] string) {

	for _, sPattern := range asPatterns {
		if matchesPatternIgnoreCase (sInput, sPattern) {
			asOutput = append (asOutput, sPattern)
		}
	}

	return

} // findMatchingPatterns

func loadCertificates (params *ConParams, sPathName string, abyContents [] byte, sTitle string) (certs [] *x509.Certificate, err error) {

	sDesc := ternary (sPathName != "",
		"file " + sPathName,
		itoa (len (abyContents)) + " decoded bytes")

	if params.hasDebugLog () {
		debugLog ("loadCertificates: " + sTitle + " " + sDesc)
	}

	certs = [] *x509.Certificate {}

	for aby := abyContents ; len (aby) > 0 ; {

		block, abyRest := pem.Decode (aby)
		if block == nil { break }

		aby = abyRest

		if block.Type == "CERTIFICATE" && len (block.Headers) == 0 {

			cert, errParse := x509.ParseCertificate (block.Bytes)
			if errParse != nil {
				err = formatError (errParse, sTitle + " Could not obtain certificate from " + sDesc)
				return
			}

			certs = append (certs, cert)
		}
	} // end for aby

	if params.hasDebugLog () {
		debugLog ("loadCertificates: " + sTitle + " loaded " + itoa (len (certs)) + " cert(s) from " + sDesc)
	}

	if len (certs) == 0 {
		err = formatError (nil, sTitle + " Found no certificate in " + sDesc)
		return
	}

	return

} // end loadCertificates

func loadCertificatesFromPEMFile (params *ConParams, sPathName string, sTitle string) ([] *x509.Certificate, error) {

	abyContents, err := os.ReadFile (sPathName)
	if err != nil {
		return nil, formatError (err, sTitle + " Unable to read PEM file " + sPathName)
	}

	return loadCertificates (params, sPathName, abyContents, sTitle)

} // end loadCertificatesFromPEMFile

func createCertPoolFromBase64 (params *ConParams, sBase64 string, sTitle string) (*x509.CertPool, error) {

	if sBase64 == "" {
		return nil, nil
	}

	certpool := x509.NewCertPool ()

	abyContents, err := base64.RawURLEncoding.DecodeString (sBase64)
	if err != nil {
		return nil, formatError (err, sTitle + " Could not decode base64url value")
	}

	certs, err := loadCertificates (params, /*sPathName*/ "", abyContents, sTitle)
	if err != nil {
		return nil, err
	}

	for _, cert := range certs {
		certpool.AddCert (cert)
	}

	return certpool, nil

} // end createCertPoolFromBase64

func createCertPoolFromPEMFiles (params *ConParams, asPathNames [] string, sTitle string) (*x509.CertPool, error) {

	if len (asPathNames) == 0 {
		return nil, nil
	}

	certpool := x509.NewCertPool ()

	for _, sPathName := range asPathNames {

		// Avoid using CertPool.AppendCertsFromPEM so we can count the certs in each file and obtain any parsing errors.

		certs, err := loadCertificatesFromPEMFile (params, sPathName, sTitle)
		if err != nil {
			return nil, err
		}

		for _, cert := range certs {
			certpool.AddCert (cert)
		}
	} // end for sPathName

	return certpool, nil

} // end createCertPoolFromPEMFiles

func createCertPoolFromPEMFile (params *ConParams, sPEMFile string, sTitle string) (*x509.CertPool, error) {

	if sPEMFile == "" {
		return nil, nil
	}

	return createCertPoolFromPEMFiles (params, [] string { sPEMFile }, sTitle)

} // end createCertPoolFromPEMFile

func createCertPoolFromPEMDirectory (params *ConParams, sPEMDirectory string, sTitle string) (*x509.CertPool, error) {

	if sPEMDirectory == "" {
		return nil, nil
	}

	info, err := os.Stat (sPEMDirectory)
	if err != nil {
		return nil, formatError (err, sTitle + " Unable to access directory " + sPEMDirectory)
	}

	if ! info.IsDir () {
		return nil, formatError (nil, sPEMDirectory + " is not a directory")
	}

	asPathNames, err := filepath.Glob (filepath.Join (sPEMDirectory, "*.pem")) // returns nil if there is no matching file
	if err != nil {
		return nil, formatError (err, sTitle + " Unable to read directory " + sPEMDirectory)
	}

	if asPathNames == nil {
		return nil, formatError (nil, sTitle + " No .pem files found in directory " + sPEMDirectory)
	}

	return createCertPoolFromPEMFiles (params, asPathNames, sTitle)

} // end createCertPoolFromPEMDirectory

func formatDateRange (cert *x509.Certificate) string {

	if cert == nil { return "not available" }

	return formatTime (cert.NotBefore) + " to " + formatTime (cert.NotAfter)

} // end formatDateRange

func getServerCertProblems (sTitle string, certpool *x509.CertPool, certs [] *x509.Certificate) (verifiedChains [][] *x509.Certificate, err error) {

	if certpool == nil { return } // skip verify when certpoolPEMFile or certpoolPEMDir are unavailable

	if certpool == certpoolSystem { certpool = nil } // x509.VerifyOptions { Roots : nil } uses the system roots or the platform verifier

	if len (certs) == 0 {
		err = formatError (nil, sTitle + " CertPool cannot check server certificate validity because server certificate is missing")
		return
	}

	firstCert   := certs [0]
	restOfCerts := certs [1 : ]

	// DNSName, if set, is checked against the leaf certificate with Certificate.VerifyHostname or the platform verifier.
	// Intermediates is an optional pool of certificates that are not trust anchors, but can be used to form a chain from the leaf certificate to a root certificate.
	// Roots is the set of trusted root certificates the leaf certificate needs to chain up to. If nil, the system roots or the platform verifier are used.
	// CurrentTime is used to check the validity of all certificates in the chain. If zero, the current time is used.
	// KeyUsages specifies which Extended Key Usage values are acceptable. A chain is accepted if it allows any of the listed values. An empty list means ExtKeyUsageServerAuth. To accept any key usage, include ExtKeyUsageAny.
	// MaxConstraintComparisions is the maximum number of comparisons to perform when checking a given certificate's name constraints. If zero, a sensible default is used.
	opts := x509.VerifyOptions {
		Intermediates : x509.NewCertPool (),
		Roots         : certpool,
		KeyUsages     : [] x509.ExtKeyUsage { x509.ExtKeyUsageAny }, // accept any key usage
	}

	// Assume the second and subsequent certs from the server are Intermediate
	for _, cert := range restOfCerts {
		opts.Intermediates.AddCert (cert)
	}

	verifiedChains, err = firstCert.Verify (opts)

	if err != nil {
		err = formatError (err, sTitle + " CertPool rejected certificate")
	}

	return

} // end getServerCertProblems

// Verifies that none of the certs have been revoked by a Certificate Revocation List (CRL) or Online Certificate Status Protocol (OCSP)
func checkRevocationStatus (params *ConParams, status *certRevStatus, sTitle string, verifiedChains [][] *x509.Certificate) {

	if params.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter checkRevocationStatus sTitle=%v", sTitle))
		defer func () { traceLog (fmt.Sprintf ("< leave checkRevocationStatus sTitle=%v status.bGood=%v status.bRevoked=%v", sTitle, status.bGood, status.bRevoked)) } ()
	}

	if params.hasDebugLog () {
		debugLog (fmt.Sprintf ("checkRevocationStatus: M_bSSLCRL=%v M_bSSLOCSP=%v", params.M_bSSLCRL, params.M_bSSLOCSP))
	}

	if params.M_bSSLOCSP {
		verifyOCSP (params, status, sTitle, verifiedChains) // check OCSP first because it is faster
	}

	if ! status.known () && params.M_bSSLCRL {
		verifyCRL (params, status, sTitle, verifiedChains) // check CRL second because it is slower
	}

	if params.hasDebugLog () {
		debugLog (fmt.Sprintf ("checkRevocationStatus: len(status.errs)=%v", len (status.errs)))
		for i, err := range status.errs {
			debugLog (fmt.Sprintf ("checkRevocationStatus: status.errs[%v]=%v", i, err))
		}
	}
} // end checkRevocationStatus

func getIssuer (i int, verifiedChain [] *x509.Certificate) *x509.Certificate {

	if i + 1 < len (verifiedChain) {
		return verifiedChain [i + 1]
	} else {
		return nil // self-signed or root certificate
	}
} // end getIssuer

func isSelfSigned (cert *x509.Certificate) bool {

	return cert.Issuer.String () == cert.Subject.String ()

} // end isSelfSigned

func getIssuedChain (verifiedChain [] *x509.Certificate) (issuedChain [] *x509.Certificate) {

	// Only issued certificates can be revoked. Self-signed certificates and root certificates cannot be revoked.
	// We do not check the revocation status of the last certificate in the chain if self-signed.

	issuedChain = verifiedChain

	if len (issuedChain) >= 2 && isSelfSigned (issuedChain [len (issuedChain) - 1]) {
		issuedChain = issuedChain [ : len (issuedChain) - 1] // omit self-signed certificate at end of chain
	}

	return

} // end getIssuedChain

func verifyCRL (params *ConParams, status *certRevStatus, sTitle string, verifiedChains [][] *x509.Certificate) {

	if params.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter verifyCRL sTitle=%v", sTitle))
		defer func () { traceLog (fmt.Sprintf ("< leave verifyCRL sTitle=%v status.bGood=%v status.bRevoked=%v", sTitle, status.bGood, status.bRevoked)) } ()
	}

	if params.hasDebugLog () {
		debugLog (fmt.Sprintf ("verifyCRL: len(verifiedChains)=%v", len (verifiedChains)))
	}

	for i, verifiedChain := range verifiedChains {

		if params.hasDebugLog () {
			debugLog (fmt.Sprintf ("verifyCRL: %v certificate chain %v of %v has %v certificate(s)",
				sTitle,
				i + 1,
				len (verifiedChains),
				len (verifiedChain)))
		}

		nGoodResponses := 0

		issuedChain := getIssuedChain (verifiedChain)

		if params.hasDebugLog () {
			debugLog (fmt.Sprintf ("verifyCRL: %v certificate chain %v of %v has %v issued certificate(s)",
				sTitle,
				i + 1,
				len (verifiedChains),
				len (issuedChain)))
		}

		for j, cert := range issuedChain {

			issuer := getIssuer (j, verifiedChain)

			if params.hasDebugLog () {
				debugLog (fmt.Sprintf ("verifyCRL: %v certificate chain %v of %v issued certificate %v of %v has %v CRL distribution point(s)",
					sTitle,
					i + 1,
					len (verifiedChains),
					j + 1,
					len (issuedChain),
					len (cert.CRLDistributionPoints)))
			}

			for k, distributionPoint := range cert.CRLDistributionPoints {

				bSkip := getUrlScheme (distributionPoint) != "http"

				if params.hasDebugLog () {
					debugLog (fmt.Sprintf ("verifyCRL: %v certificate chain %v of %v issued certificate %v of %v CRL distribution point %v of %v: %v --> %v",
						sTitle,
						i + 1,
						len (verifiedChains),
						j + 1,
						len (issuedChain),
						k + 1,
						len (cert.CRLDistributionPoints),
						distributionPoint,
						ternary (bSkip, "skip", "download")))
				}

				if bSkip { continue } // skip non-HTTP URLs such as LDAP

				revocationList, err := downloadCRL (params, distributionPoint)
				if err != nil {

					status.errs = append (status.errs, formatError (err,
						"verifyCRL: %v certificate chain %v of %v issued certificate %v of %v unable to download CRL from %v",
						sTitle,
						i + 1,
						len (verifiedChains),
						j + 1,
						len (issuedChain),
						distributionPoint))

				} // end if err

				if revocationList != nil {

					err = revocationList.CheckSignatureFrom (issuer)
					if err != nil {

						status.errs = append (status.errs, formatError (err,
							"verifyCRL: %v certificate chain %v of %v issued certificate %v of %v invalid CRL signature from %v",
							sTitle,
							i + 1,
							len (verifiedChains),
							j + 1,
							len (issuedChain),
							distributionPoint))

						revocationList = nil

					} // end if err
				} // end if revocationList

				if revocationList != nil && revocationList.NextUpdate.Before (time.Now ()) {

					status.errs = append (status.errs, formatError (nil,
						"verifyCRL: %v certificate chain %v of %v issued certificate %v of %v outdated CRL from %v",
						sTitle,
						i + 1,
						len (verifiedChains),
						j + 1,
						len (issuedChain),
						distributionPoint))

					revocationList = nil

				} // end if revocationList

				if revocationList != nil {

					if params.hasDebugLog () {
						debugLog (fmt.Sprintf ("verifyCRL: %v certificate chain %v of %v issued certificate %v of %v CRL distribution point %v of %v: %v has %v revoked certificate(s) --> stop",
							sTitle,
							i + 1,
							len (verifiedChains),
							j + 1,
							len (issuedChain),
							k + 1,
							len (cert.CRLDistributionPoints),
							distributionPoint,
							len (revocationList.RevokedCertificateEntries)))
					}

					for _, revokedCertificate := range revocationList.RevokedCertificateEntries {

						if revokedCertificate.SerialNumber.Cmp (cert.SerialNumber) == 0 {

							status.errs = append (status.errs, formatError (nil,
								"verifyCRL: %v certificate chain %v of %v issued certificate %v of %v revoked by CRL from %v for reason code %v",
								sTitle,
								i + 1,
								len (verifiedChains),
								j + 1,
								len (issuedChain),
								distributionPoint,
								revokedCertificate.ReasonCode))

							status.setRevoked ()
							return

						} // end if revokedCertificate
					} // end for revokedCertificate

					nGoodResponses++
					break // stop checking CRL distribution points after the first responsive distribution point

				} // end if revocationList
			} // end for k, distributionPoint
		} // end for j, cert

		if params.hasDebugLog () {
			debugLog (fmt.Sprintf ("verifyCRL: %v certificate chain %v of %v has %v issued certificate(s) and %v good CRL response(s)",
				sTitle,
				i + 1,
				len (verifiedChains),
				len (issuedChain),
				nGoodResponses))
		}

		// Set a good status if any one certificate chain has all good responses, including zero for a self-signed certificate.
		if nGoodResponses == len (issuedChain) {

			if params.hasDebugLog () {
				debugLog (fmt.Sprintf ("verifyCRL: %v certificate chain %v of %v has good status",
					sTitle,
					i + 1,
					len (verifiedChains)))
			}

			status.setGood ()
			return

		} // end if nGoodResponses
	} // end for i, verifiedChain

} // end verifyCRL

// Gets the revocationList at URL if it is stored in g_crlCache, otherwise downloads the revocation list and stores in the g_crlCache
func downloadCRL (params *ConParams, sURL string) (revocationList *x509.RevocationList, err error) {

	if params.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter downloadCRL sURL=%v", sURL))
		defer func () { traceLog (fmt.Sprintf ("< leave downloadCRL sURL=%v", sURL)) } ()
	}

	revocationList = g_crlCache.readCache (sURL)
	if revocationList != nil { // revocation list was in cache

		if params.hasDebugLog () {
			debugLog (fmt.Sprintf ("downloadCRL: cache hit for CRL from %v", sURL))
		}

		return
	}

	if params.hasDebugLog () {
		debugLog (fmt.Sprintf ("downloadCRL: cache miss, must download CRL from %v", sURL))
	}

	// URL was not found in cache so download it and add to cache
	resp, err := DoHttpRequest (params, "GET", sURL, /*aasHeaders*/ nil, /*abyRequestBody*/ nil)
	if err != nil {
		err = formatError (err, "Unable to download CRL from %v", sURL)
		return
	}

	if resp.M_nCode != 200 {
		err = formatError (nil, "Download CRL error %v %v from %v", resp.M_nCode, resp.M_sMessage, sURL)
		return
	}

	revocationList, err = x509.ParseRevocationList (resp.M_abyBody)
	if err != nil {
		err = formatError (err, "Unable to parse CRL from %v", sURL)
		return
	}

	g_crlCache.addToCache (sURL, revocationList, time.Minute) // one minute TTL
	return

} // end downloadCRL

func verifyOCSP (params *ConParams, status *certRevStatus, sTitle string, verifiedChains [][] *x509.Certificate) {

	if params.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter verifyOCSP sTitle=%v", sTitle))
		defer func () { traceLog (fmt.Sprintf ("< leave verifyOCSP sTitle=%v status.bGood=%v status.bRevoked=%v", sTitle, status.bGood, status.bRevoked)) } ()
	}

	if params.hasDebugLog () {
		debugLog (fmt.Sprintf ("verifyOCSP: len(verifiedChains)=%v", len (verifiedChains)))
	}

	for i, verifiedChain := range verifiedChains {

		if params.hasDebugLog () {
			debugLog (fmt.Sprintf ("verifyOCSP: %v certificate chain %v of %v has %v certificate(s)",
				sTitle,
				i + 1,
				len (verifiedChains),
				len (verifiedChain)))
		}

		nGoodResponses := 0

		issuedChain := getIssuedChain (verifiedChain)

		if params.hasDebugLog () {
			debugLog (fmt.Sprintf ("verifyOCSP: %v certificate chain %v of %v has %v issued certificate(s)",
				sTitle,
				i + 1,
				len (verifiedChains),
				len (issuedChain)))
		}

		for j, cert := range issuedChain {

			issuer := getIssuer (j, verifiedChain)

			if params.hasDebugLog () {
				debugLog (fmt.Sprintf ("verifyOCSP: %v certificate chain %v of %v issued certificate %v of %v has %v OCSP responder(s)",
					sTitle,
					i + 1,
					len (verifiedChains),
					j + 1,
					len (issuedChain),
					len (cert.OCSPServer)))
			}

			if len (cert.OCSPServer) == 0 {

				status.errs = append (status.errs, formatError (nil,
					"verifyOCSP: %v certificate chain %v of %v issued certificate %v of %v missing OCSP responder",
					sTitle,
					i + 1,
					len (verifiedChains),
					j + 1,
					len (issuedChain)))

			} else { // len (cert.OCSPServer) > 0

				abyOCSPRequest, err := ocsp.CreateRequest (cert, issuer, nil)
				if err != nil {

					status.errs = append (status.errs, formatError (err,
						"verifyOCSP: %v certificate chain %v of %v issued certificate %v of %v unable to create OCSP request",
						sTitle,
						i + 1,
						len (verifiedChains),
						j + 1,
						len (issuedChain)))

				} else { // created OCSP request

					for k, sOCSPServerURL := range cert.OCSPServer {

						if params.hasDebugLog () {
							debugLog (fmt.Sprintf ("verifyOCSP: %v certificate chain %v of %v issued certificate %v of %v OCSP responder %v of %v: %v",
								sTitle,
								i + 1,
								len (verifiedChains),
								j + 1,
								len (issuedChain),
								k + 1,
								len (cert.OCSPServer),
								sOCSPServerURL))
						}

						resp, err := DoHttpRequest (params, "POST", sOCSPServerURL, [][] string {
							{"Content-Type", "application/ocsp-request"},
							{"Accept", "application/ocsp-response"}},
							abyOCSPRequest)

						if err != nil {

							status.errs = append (status.errs, formatError (err,
								"verifyOCSP: %v certificate chain %v of %v issued certificate %v of %v OCSP request failure for %v",
								sTitle,
								i + 1,
								len (verifiedChains),
								j + 1,
								len (issuedChain),
								sOCSPServerURL))

						} else if resp.M_nCode != 200 {

							status.errs = append (status.errs, formatError (nil,
								"verifyOCSP: %v certificate chain %v of %v issued certificate %v of %v OCSP request error %v %v from %v",
								sTitle,
								i + 1,
								len (verifiedChains),
								j + 1,
								len (issuedChain),
								resp.M_nCode,
								resp.M_sMessage,
								sOCSPServerURL))

						} else { // successful OCSP response

							ocspResponse, err := ocsp.ParseResponse (resp.M_abyBody, issuer)
							if err != nil {

								status.errs = append (status.errs, formatError (err,
									"verifyOCSP: %v certificate chain %v of %v issued certificate %v of %v invalid OCSP response from %v",
									sTitle,
									i + 1,
									len (verifiedChains),
									j + 1,
									len (issuedChain),
									sOCSPServerURL))

							} else { // parsed OCSP response

								if params.hasDebugLog () {
									debugLog (fmt.Sprintf ("verifyOCSP: %v certificate chain %v of %v issued certificate %v of %v OCSP responder %v of %v: %v -> OCSP response: Status=%v ProducedAt=%v RevokedAt=%v RevocationReason=%v",
										sTitle,
										i + 1,
										len (verifiedChains),
										j + 1,
										len (issuedChain),
										k + 1,
										len (cert.OCSPServer),
										sOCSPServerURL,
										ocspResponse.Status,
										ocspResponse.ProducedAt,
										ocspResponse.RevokedAt,
										ocspResponse.RevocationReason))
								}

								// When ocspResponse.Status == ocsp.Unknown continue the loop to check next OCSP server.

								if ocspResponse.Status == ocsp.Revoked {

									status.errs = append (status.errs, formatError (nil,
										"verifyOCSP: %v certificate chain %v of %v issued certificate %v of %v revoked by OCSP response from %v",
										sTitle,
										i + 1,
										len (verifiedChains),
										j + 1,
										len (issuedChain),
										sOCSPServerURL))

									status.setRevoked ()
									return

								} else if ocspResponse.Status == ocsp.Good {

									if params.hasDebugLog () {
										debugLog (fmt.Sprintf ("verifyOCSP: %v certificate chain %v of %v issued certificate %v of %v good OCSP status from %v",
											sTitle,
											i + 1,
											len (verifiedChains),
											j + 1,
											len (issuedChain),
											sOCSPServerURL))
									}

									nGoodResponses++
									break // stop checking OCSP servers after the first good response

								} // end else ocsp.Good
							} // end else parsed OCSP response
						} // else successful OCSP response
					} // end for k, sOCSPServerURL
				} // end else created OCSP request
			}// end else len (cert.OCSPServer) > 0
		} // end for j, cert

		if params.hasDebugLog () {
			debugLog (fmt.Sprintf ("verifyOCSP: %v certificate chain %v of %v has %v issued certificate(s) and %v good OCSP response(s)",
				sTitle,
				i + 1,
				len (verifiedChains),
				len (issuedChain),
				nGoodResponses))
		}

		if len (issuedChain) == 0 { // self-signed certificate

			status.errs = append (status.errs, formatError (nil,
				"verifyOCSP: %v certificate chain %v of %v OCSP response not available for self-signed certificate",
				sTitle,
				i + 1,
				len (verifiedChains)))

		} else if nGoodResponses == len (issuedChain) { // set a good status if any one certificate chain has all good responses

			if params.hasDebugLog () {
				debugLog (fmt.Sprintf ("verifyOCSP: %v certificate chain %v of %v has good status",
					sTitle,
					i + 1,
					len (verifiedChains)))
			}

			status.setGood ()
			return

		} // end if nGoodResponses
	} // end for i, verifiedChain

} // end verifyOCSP

func checkValidity (cert *x509.Certificate) error {

	now := time.Now ()

	if now.Before (cert.NotBefore) {
		return x509.CertificateInvalidError {
			Cert   : cert,
			Reason : x509.Expired,
			Detail : fmt.Sprintf ("Certificate is not valid yet because the current time %v is before the certificate date range %v", formatTime (now), formatDateRange (cert)),
		}
	}

	if now.After (cert.NotAfter) {
		return x509.CertificateInvalidError {
			Cert   : cert,
			Reason : x509.Expired,
			Detail : fmt.Sprintf ("Certificate is expired because the current time %v is after the certificate date range %v", formatTime (now), formatDateRange (cert)),
		}
	}

	return nil

} // end checkValidity

func getValidityProblems (certs [] *x509.Certificate) (errs [] error) {

	for _, cert := range certs {

		err := checkValidity (cert)

		if err != nil { errs = append (errs, err) }
	}

	return

} // end getValidityProblems

func formatValidity (cert *x509.Certificate) string {

	if cert == nil { return "validity is not available" }

	err := checkValidity (cert)

	if err != nil { return fmt.Sprintf ("is invalid due to %v", err) }

	return "is valid"

} // end formatValidity

func formatCertPurpose (cert *x509.Certificate) string {

	if cert == nil { return "not available" }

	// RFC 5280 section 4.2.1.3 "Key Usage" says:
	// When the keyUsage extension appears in a certificate, at least one of the bits MUST be set to 1.

	if cert.KeyUsage == 0 { return "not present" }

	asNames := [] string {
		"DigitalSignature(0)",
		"ContentCommitment(1)",
		"KeyEncipherment(2)",
		"DataEncipherment(3)",
		"KeyAgreement(4)",
		"CertSign(5)",
		"CRLSign(6)",
		"EncipherOnly(7)",
		"DecipherOnly(8)",
	}

	sOutput := ""

	for i := 0 ; i < len (asNames) ; i++ {
		if (cert.KeyUsage & (1 << i)) != 0 {
			sOutput += ternary (len (sOutput) > 0, ", ", "") + asNames [i]
		}
	}

	return sOutput

} // end formatCertPurpose

func formatDN (cert *x509.Certificate) string {

	if cert == nil { return "not available" }

	return cert.Subject.String ()

} // end formatDN

func formatCN (cert *x509.Certificate) string {

	if cert == nil { return "not available" }

	return cert.Subject.CommonName

} // end formatCN

func extractSANsFromCert (cert *x509.Certificate) [] string {

	if cert == nil { return nil }

	asOutput := [] string (nil)

	if len (cert.DNSNames) > 0 { asOutput = append (asOutput, cert.DNSNames ...) }

	for _, addr := range cert.IPAddresses {
		asOutput = append (asOutput, fmt.Sprintf ("%v", addr))
	}

	return asOutput

} // end extractSANsFromCert

func formatSANs (cert *x509.Certificate) string {

	if cert == nil { return "not available" }

	asSANs := extractSANsFromCert (cert)

	if asSANs == nil { return "not present" }

	sOutput := ""

	for _, s := range asSANs {
		sOutput += ternary (len (sOutput) > 0, ", ", "") + s
	}

	return sOutput

} // end formatSANs

func formatIndentedList (sPrefix string, oInput any) string {

	v := reflect.ValueOf (oInput)

	if v.Kind () != reflect.Slice { return fmt.Sprintf ("%v %+v", reflect.TypeOf (oInput), oInput) }

	if v.Len () == 0 { return "not present" }

	sOutput := "[\n"

	for i := 0 ; i < v.Len () ; i++ {
		sOutput += sPrefix + fmt.Sprintf ("  %v\n", v.Index (i).Interface ())
	}

	return sOutput + sPrefix + "]"

} // end formatIndentedList

func formatExtensions (sPrefix string, exts [] pkix.Extension, unhandled [] asn1.ObjectIdentifier) string {

	if exts == nil { return "not present" }

	sOutput := "[\n"

	for _, ext := range exts {

		bUnhandled := false
		for _, oid := range unhandled {
			if ext.Id.Equal (oid) {
				bUnhandled = true
				break
			}
		}

		sOutput += sPrefix + fmt.Sprintf ("  %v%v%v [\n", formatOID (ext.Id), ternary (ext.Critical, " Critical", ""), ternary (bUnhandled, " Unhandled", ""))
		sOutput += sPrefix + fmt.Sprintf ("    %v\n", formatByteSliceAsHexDump (ext.Value, sPrefix + "    ", "not present"))
		sOutput += sPrefix + fmt.Sprintf ("  ]\n")
	}

	return sOutput + sPrefix + "]"

} // end formatExtensions

const certTimeFormat = "Mon Jan 02 15:04:05 MST 2006"

func formatCert (cert *x509.Certificate) string {

	if cert == nil { return "not available" }

	sHorzLine := "  ----------------------------------------------"
	sPrefix   := "  |  "

	sPublicKey := fmt.Sprintf ("%v %+v", reflect.TypeOf (cert.PublicKey), cert.PublicKey)

	if pk, bValid := cert.PublicKey.(*rsa.PublicKey) ; bValid {

		sPublicKey  = "[\n"
		sPublicKey += sPrefix + fmt.Sprintf ("  size: %v bits\n", pk.Size () * 8)
		sPublicKey += sPrefix + fmt.Sprintf ("  modulus: %v\n", pk.N)
		sPublicKey += sPrefix + fmt.Sprintf ("  public exponent: %v\n", pk.E)
		sPublicKey += sPrefix + "]"
	}

	sBasicConstraints := "not present"

	if cert.BasicConstraintsValid {

		sBasicConstraints  = "[\n"
		sBasicConstraints += sPrefix + fmt.Sprintf ("  IsCA: %v\n", cert.IsCA)
		sBasicConstraints += sPrefix + fmt.Sprintf ("  MaxPathLen: %v\n", cert.MaxPathLen)
		sBasicConstraints += sPrefix + fmt.Sprintf ("  MaxPathLenZero: %v\n", cert.MaxPathLenZero)
		sBasicConstraints += sPrefix + "]"
	}

	sOutput := sHorzLine + "\n"
	sOutput += sPrefix + fmt.Sprintf ("Version: V%v\n"                     , cert.Version)
	sOutput += sPrefix + fmt.Sprintf ("Subject: %v\n"                      , cert.Subject)
	sOutput += sPrefix + fmt.Sprintf ("PublicKeyAlgorithm: %v\n"           , cert.PublicKeyAlgorithm)
	sOutput += sPrefix + fmt.Sprintf ("PublicKey: %v\n"                    , sPublicKey)
	sOutput += sPrefix + fmt.Sprintf ("Validity: [\n")
	sOutput += sPrefix + fmt.Sprintf ("  From: %v\n"                       , cert.NotBefore.Format (certTimeFormat))
	sOutput += sPrefix + fmt.Sprintf ("  To:   %v\n"                       , cert.NotAfter.Format (certTimeFormat))
	sOutput += sPrefix + fmt.Sprintf ("]\n")
	sOutput += sPrefix + fmt.Sprintf ("Issuer: %v\n"                       , cert.Issuer)
	sOutput += sPrefix + fmt.Sprintf ("SerialNumber: %v\n"                 , formatByteSliceAsHexDigits (cert.SerialNumber.Bytes (), "not present"))
	sOutput += sPrefix + fmt.Sprintf ("SubjectKeyId:   %v\n"               , formatByteSliceAsHexDigits (cert.SubjectKeyId         , "not present"))
	sOutput += sPrefix + fmt.Sprintf ("AuthorityKeyId: %v\n"               , formatByteSliceAsHexDigits (cert.AuthorityKeyId       , "not present"))
	sOutput += sPrefix + fmt.Sprintf ("BasicConstraints: %v\n"             , sBasicConstraints)
	sOutput += sPrefix + fmt.Sprintf ("DNSNames: %v\n"                     , formatIndentedList (sPrefix, cert.DNSNames))
	sOutput += sPrefix + fmt.Sprintf ("EmailAddresses: %v\n"               , formatIndentedList (sPrefix, cert.EmailAddresses))
	sOutput += sPrefix + fmt.Sprintf ("IPAddresses: %v\n"                  , formatIndentedList (sPrefix, cert.IPAddresses))
	sOutput += sPrefix + fmt.Sprintf ("URIs: %v\n"                         , formatIndentedList (sPrefix, cert.URIs))
	sOutput += sPrefix + fmt.Sprintf ("OCSPServer: %v\n"                   , formatIndentedList (sPrefix, cert.OCSPServer))
	sOutput += sPrefix + fmt.Sprintf ("CRLDistributionPoints: %v\n"        , formatIndentedList (sPrefix, cert.CRLDistributionPoints))
	sOutput += sPrefix + fmt.Sprintf ("PolicyIdentifiers: %v\n"            , formatIndentedList (sPrefix, cert.PolicyIdentifiers))
	sOutput += sPrefix + fmt.Sprintf ("PermittedDNSDomainsCritical: %v\n"  , cert.PermittedDNSDomainsCritical)
	sOutput += sPrefix + fmt.Sprintf ("PermittedDNSDomains: %v\n"          , formatIndentedList (sPrefix, cert.PermittedDNSDomains))
	sOutput += sPrefix + fmt.Sprintf ("ExcludedDNSDomains: %v\n"           , formatIndentedList (sPrefix, cert.ExcludedDNSDomains))
	sOutput += sPrefix + fmt.Sprintf ("PermittedIPRanges: %v\n"            , formatIndentedList (sPrefix, cert.PermittedIPRanges))
	sOutput += sPrefix + fmt.Sprintf ("ExcludedIPRanges: %v\n"             , formatIndentedList (sPrefix, cert.ExcludedIPRanges))
	sOutput += sPrefix + fmt.Sprintf ("PermittedEmailAddresses: %v\n"      , formatIndentedList (sPrefix, cert.PermittedEmailAddresses))
	sOutput += sPrefix + fmt.Sprintf ("ExcludedEmailAddresses: %v\n"       , formatIndentedList (sPrefix, cert.ExcludedEmailAddresses))
	sOutput += sPrefix + fmt.Sprintf ("PermittedURIDomains: %v\n"          , formatIndentedList (sPrefix, cert.PermittedURIDomains))
	sOutput += sPrefix + fmt.Sprintf ("ExcludedURIDomains: %v\n"           , formatIndentedList (sPrefix, cert.ExcludedURIDomains))
	sOutput += sPrefix + fmt.Sprintf ("Extensions: %v\n"                   , formatExtensions (sPrefix, cert.Extensions, cert.UnhandledCriticalExtensions))
	sOutput += sPrefix + fmt.Sprintf ("SignatureAlgorithm: %v\n"           , cert.SignatureAlgorithm)
	sOutput += sPrefix + fmt.Sprintf ("Signature: [\n")
	sOutput += sPrefix + fmt.Sprintf ("  %v\n"                             , formatByteSliceAsHexDump (cert.Signature, sPrefix + "  ", "not present"))
	sOutput += sPrefix + fmt.Sprintf ("]\n")
	sOutput += sHorzLine
	return sOutput

} // end formatCert
