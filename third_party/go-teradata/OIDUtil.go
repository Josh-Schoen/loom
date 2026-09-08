// Copyright 2022 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"encoding/asn1"
)

var g_mapOIDs = map [string] string {
	"1.3.6.1.4.1.11129.2.4.2"      : "CertificateExtension-SignedCertificateTimestampList",
	"1.3.6.1.4.1.11129.2.4.5"      : "OCSPExtension-SignedCertificateTimestampList",
	"1.3.6.1.5.5.7.1.1"            : "PKIX-AuthInfoAccess",
	"1.3.6.1.5.5.7.1.11"           : "PKIX-SubjectInfoAccess",
	"1.3.6.1.5.5.7.3.1"            : "KeyPurpose-ServerAuth",
	"1.3.6.1.5.5.7.3.2"            : "KeyPurpose-ClientAuth",
	"1.3.6.1.5.5.7.3.3"            : "KeyPurpose-CodeSigning",
	"1.3.6.1.5.5.7.3.4"            : "KeyPurpose-EmailProtection",
	"1.3.6.1.5.5.7.3.5"            : "KeyPurpose-IPsecEndSystem",
	"1.3.6.1.5.5.7.3.6"            : "KeyPurpose-IPsecTunnel",
	"1.3.6.1.5.5.7.3.7"            : "KeyPurpose-IPsecUser",
	"1.3.6.1.5.5.7.3.8"            : "KeyPurpose-TimeStamping",
	"1.3.6.1.5.5.7.3.9"            : "KeyPurpose-OCSPSigning",
	"1.3.6.1.5.5.7.48.1"           : "AccessDescriptor-OCSP",
	"1.3.6.1.5.5.7.48.1.1"         : "AccessDescriptor-OCSPBasicResponse",
	"1.3.6.1.5.5.7.48.1.2"         : "AccessDescriptor-OCSPNonceExt",
	"1.3.6.1.5.5.7.48.1.5"         : "AccessDescriptor-OCSPNoCheck",
	"1.3.6.1.5.5.7.48.2"           : "AccessDescriptor-CAIssuers",
	"1.3.6.1.5.5.7.48.3"           : "AccessDescriptor-TimeStamping",
	"1.3.6.1.5.5.7.48.5"           : "AccessDescriptor-CARepository",
	"2.5.4.3"                      : "X500-CommonName",
	"2.5.4.4"                      : "X500-Surname",
	"2.5.4.5"                      : "X500-SerialNumber",
	"2.5.4.6"                      : "X500-CountryName",
	"2.5.4.7"                      : "X500-LocalityName",
	"2.5.4.8"                      : "X500-StateName",
	"2.5.4.9"                      : "X500-StreetAddress",
	"2.5.4.10"                     : "X500-OrganizationName",
	"2.5.4.11"                     : "X500-OrganizationalUnitName",
	"2.5.4.12"                     : "X500-Title",
	"2.5.4.42"                     : "X500-GivenName",
	"2.5.4.43"                     : "X500-Initials",
	"2.5.4.44"                     : "X500-GenerationQualifier",
	"2.5.4.46"                     : "X500-DNQualifier",
	"2.5.29.9"                     : "CertificateExtension-SubjectDirectoryAttributes",
	"2.5.29.14"                    : "CertificateExtension-SubjectKeyID",
	"2.5.29.15"                    : "CertificateExtension-KeyUsage",
	"2.5.29.16"                    : "CertificateExtension-PrivateKeyUsage",
	"2.5.29.17"                    : "CertificateExtension-SubjectAlternativeName",
	"2.5.29.18"                    : "CertificateExtension-IssuerAlternativeName",
	"2.5.29.19"                    : "CertificateExtension-BasicConstraints",
	"2.5.29.20"                    : "CertificateExtension-CRLNumber",
	"2.5.29.21"                    : "CertificateExtension-ReasonCode",
	"2.5.29.23"                    : "CertificateExtension-HoldInstructionCode",
	"2.5.29.24"                    : "CertificateExtension-InvalidityDate",
	"2.5.29.27"                    : "CertificateExtension-DeltaCRLIndicator",
	"2.5.29.28"                    : "CertificateExtension-IssuingDistributionPoint",
	"2.5.29.29"                    : "CertificateExtension-CertificateIssuer",
	"2.5.29.30"                    : "CertificateExtension-NameConstraints",
	"2.5.29.31"                    : "CertificateExtension-CRLDistributionPoints",
	"2.5.29.32"                    : "CertificateExtension-CertificatePolicies",
	"2.5.29.32.0"                  : "CertificateExtension-AnyCertificatePolicy",
	"2.5.29.33"                    : "CertificateExtension-PolicyMappings",
	"2.5.29.35"                    : "CertificateExtension-AuthorityKeyID",
	"2.5.29.36"                    : "CertificateExtension-PolicyConstraints",
	"2.5.29.37"                    : "CertificateExtension-ExtendedKeyUsage",
	"2.5.29.37.0"                  : "CertificateExtension-AnyExtendedKeyUsage",
	"2.5.29.46"                    : "CertificateExtension-FreshestCRL",
	"2.5.29.54"                    : "CertificateExtension-InhibitAnyPolicy",
}

func formatOID (oid asn1.ObjectIdentifier) string {
	sDotted := oid.String ()
	sName, bFound := g_mapOIDs [sDotted]
	return ternary (bFound, sName + " ", "") + sDotted
}
