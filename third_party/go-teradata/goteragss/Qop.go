// Copyright 2021 by Teradata Corporation. All Rights Reserved.
package goteragss

/* Constants for QOP */
const (
	/* Priority */
	TDGSS_C_QOP_DEFAULT    = 0
	TDGSS_C_QOP_LOW        = 1
	TDGSS_C_QOP_MEDIUM     = 2
	TDGSS_C_QOP_HIGH       = 3
	TDGSS_C_QOP_NO_SUPPORT = 999
	/* Algorithm Name */
	TDGSS_ALG_NAME_NONE           = 0
	TDGSS_ALG_NAME_Blowfish       = 1
	TDGSS_ALG_NAME_AES            = 2
	TDGSS_ALG_NAME_MD5            = 3
	TDGSS_ALG_NAME_SHA1           = 4
	TDGSS_ALG_NAME_DIFFIE_HELLMAN = 5
	TDGSS_ALG_NAME_DH             = 5
	TDGSS_ALG_NAME_SHA256         = 6
	TDGSS_ALG_NAME_SHA512         = 7
	/* Digest Algorithm Constants */
	SHA256 = TDGSS_ALG_NAME_SHA256
	/* Key Length */
	TDGSS_KEY_LEN_K0    = 0
	TDGSS_KEY_LEN_K128  = 128
	TDGSS_KEY_LEN_K192  = 192
	TDGSS_KEY_LEN_K256  = 256
	TDGSS_KEY_LEN_K416  = 416
	TDGSS_KEY_LEN_K448  = 448
	TDGSS_KEY_LEN_K512  = 512
	TDGSS_KEY_LEN_K1024 = 1024
	TDGSS_KEY_LEN_K2048 = 2048
	/* Key Size constants */
	AES_128 = TDGSS_KEY_LEN_K128
	AES_192 = TDGSS_KEY_LEN_K192
	AES_256 = TDGSS_KEY_LEN_K256
	/* Key Length P */
	TDGSS_KEY_LEN_P_K2048 = 2048
	/* Mode */
	TDGSS_MODE_NONE    = 0
	TDGSS_MODE_CBC     = 1
	TDGSS_MODE_CFB     = 2
	TDGSS_MODE_ECB     = 3
	TDGSS_MODE_OFB     = 4
	TDGSS_MODE_GCM     = 5
	TDGSS_MODE_CCM     = 6
	TDGSS_MODE_CTR     = 7
	TDGSS_MODE_AEADGCM = 8
	TDGSS_MODE_GCM96   = 9
	/* Padding */
	TDGSS_PADDING_NoPadding                   = 0
	TDGSS_PADDING_OAEPWithDIGESTAndMGFPadding = 1
	TDGSS_PADDING_PKCS1Padding                = 3
	TDGSS_PADDING_PKCS5Padding                = 4
	TDGSS_PADDING_SSL3Padding                 = 5
)

type algQOP struct {
	version   uint /* Version information           */
	cipher    uint /* Cipher algorithm              */
	digest    uint /* Digest Algorithm              */
	keySize   uint /* Key size                      */
	mode      uint /* Mode for encryption           */
	padding   uint /* Type of padding               */
	reserved  uint /* Reserved for NCR internal use */
	custom    uint /* Reserver for custom methods   */
	cipherCtx algCipherFn
	digestCtx algDigestFn
}

/* Extended Alg QOP Structure */
type algQOPEx struct {
	legacyQOP  algQOP /* legacy algQOP          */
	mechQOP    uint   /* Mechanism QOP          */
	keyExchAlg uint   /* Key Exchange Algorithm */
	pKeySize   uint   /* DH P key Size          */
	hashKey    []byte
}

type qopInfo struct {
	mechQop int
	algQop  *algQOP
}

type newQopData struct {
	mechQopElementId int
	mechQopValue     string
	globalQop        string
	globalQopNVL     map[string]string
}

/* Legacy QOP */
var gLegacyQop0 = algQOP{
	version: 1,                       /* Version information: usually 1      */
	cipher:  TDGSS_ALG_NAME_Blowfish, /* GlobalQOP -> ConfidentialityAlgorithm */
	digest:  TDGSS_ALG_NAME_NONE,     /* GlobalQOP -> IntegrityAlgorithm */
	keySize: TDGSS_KEY_LEN_K128,      /* GlobalQOP -> KeyLength */
	mode:    TDGSS_MODE_ECB,          /* GlobalQOP -> Mode */
	padding: TDGSS_PADDING_NoPadding, /* GlobalQOP -> Padding */
}
var gLegacyQop1 = algQOP{
	version: 1,                          /* Version information: usually 1      */
	cipher:  TDGSS_ALG_NAME_AES,         /* GlobalQOP -> ConfidentialityAlgorithm */
	digest:  TDGSS_ALG_NAME_SHA1,        /* GlobalQOP -> IntegrityAlgorithm */
	keySize: TDGSS_KEY_LEN_K128,         /* GlobalQOP -> KeyLength */
	mode:    TDGSS_MODE_OFB,             /* GlobalQOP -> Mode */
	padding: TDGSS_PADDING_PKCS5Padding, /* GlobalQOP -> Padding */
}

/* Enhanced QOPs */
/* AES-K128_GCM_PKCS5Padding_SHA2_DH-K2048 */
var defaultQop1 = algQOP{
	version: 1,                          /* Version information: usually 1      */
	cipher:  TDGSS_ALG_NAME_AES,         /* GlobalQOP -> ConfidentialityAlgorithm */
	digest:  TDGSS_ALG_NAME_SHA256,      /* GlobalQOP -> IntegrityAlgorithm */
	keySize: TDGSS_KEY_LEN_K128,         /* GlobalQOP -> KeyLength */
	mode:    TDGSS_MODE_GCM,             /* GlobalQOP -> Mode */
	padding: TDGSS_PADDING_PKCS5Padding, /* GlobalQOP -> Padding */
}
var defaultQopEx1 = algQOPEx{
	legacyQOP:  defaultQop1,
	mechQOP:    TDGSS_C_QOP_DEFAULT,
	keyExchAlg: TDGSS_ALG_NAME_DH,     /* GlobalQOP -> KeyExchangeAlgorithm */
	pKeySize:   TDGSS_KEY_LEN_P_K2048, /* GlobalQOP -> KeyLengthP */
}

/* AES-K128_CBC_PKCS5Padding_SHA1_DH-K2048 */
var defaultQop2 = algQOP{
	version: 1,                          /* Version information: usually 1      */
	cipher:  TDGSS_ALG_NAME_AES,         /* GlobalQOP -> ConfidentialityAlgorithm */
	digest:  TDGSS_ALG_NAME_SHA1,        /* GlobalQOP -> IntegrityAlgorithm */
	keySize: TDGSS_KEY_LEN_K128,         /* GlobalQOP -> KeyLength */
	mode:    TDGSS_MODE_CBC,             /* GlobalQOP -> Mode */
	padding: TDGSS_PADDING_PKCS5Padding, /* GlobalQOP -> Padding */
}
var defaultQopEx2 = algQOPEx{
	legacyQOP:  defaultQop2,
	mechQOP:    TDGSS_C_QOP_DEFAULT,
	keyExchAlg: TDGSS_ALG_NAME_DH,     /* GlobalQOP -> KeyExchangeAlgorithm */
	pKeySize:   TDGSS_KEY_LEN_P_K2048, /* GlobalQOP -> KeyLengthP */
}

/* AES-K192_GCM_PKCS5Padding_SHA2_DH-K2048 */
var defaultQop3 = algQOP{
	version: 1,                          /* Version information: usually 1      */
	cipher:  TDGSS_ALG_NAME_AES,         /* GlobalQOP -> ConfidentialityAlgorithm */
	digest:  TDGSS_ALG_NAME_SHA256,      /* GlobalQOP -> IntegrityAlgorithm */
	keySize: TDGSS_KEY_LEN_K192,         /* GlobalQOP -> KeyLength */
	mode:    TDGSS_MODE_GCM,             /* GlobalQOP -> Mode */
	padding: TDGSS_PADDING_PKCS5Padding, /* GlobalQOP -> Padding */
}
var defaultQopEx3 = algQOPEx{
	legacyQOP:  defaultQop3,
	mechQOP:    TDGSS_C_QOP_DEFAULT,
	keyExchAlg: TDGSS_ALG_NAME_DH,     /* GlobalQOP -> KeyExchangeAlgorithm */
	pKeySize:   TDGSS_KEY_LEN_P_K2048, /* GlobalQOP -> KeyLengthP */
}

/* AES-K192_CBC_PKCS5Padding_SHA1_DH-K2048 */
var defaultQop4 = algQOP{
	version: 1,                          /* Version information: usually 1      */
	cipher:  TDGSS_ALG_NAME_AES,         /* GlobalQOP -> ConfidentialityAlgorithm */
	digest:  TDGSS_ALG_NAME_SHA1,        /* GlobalQOP -> IntegrityAlgorithm */
	keySize: TDGSS_KEY_LEN_K192,         /* GlobalQOP -> KeyLength */
	mode:    TDGSS_MODE_CBC,             /* GlobalQOP -> Mode */
	padding: TDGSS_PADDING_PKCS5Padding, /* GlobalQOP -> Padding */
}
var defaultQopEx4 = algQOPEx{
	legacyQOP:  defaultQop4,
	mechQOP:    TDGSS_C_QOP_DEFAULT,
	keyExchAlg: TDGSS_ALG_NAME_DH,     /* GlobalQOP -> KeyExchangeAlgorithm */
	pKeySize:   TDGSS_KEY_LEN_P_K2048, /* GlobalQOP -> KeyLengthP */
}

/* AES-K256_GCM_PKCS5Padding_SHA2_DH-K2048 */
var defaultQop5 = algQOP{
	version: 1,                          /* Version information: usually 1      */
	cipher:  TDGSS_ALG_NAME_AES,         /* GlobalQOP -> ConfidentialityAlgorithm */
	digest:  TDGSS_ALG_NAME_SHA256,      /* GlobalQOP -> IntegrityAlgorithm */
	keySize: TDGSS_KEY_LEN_K256,         /* GlobalQOP -> KeyLength */
	mode:    TDGSS_MODE_GCM,             /* GlobalQOP -> Mode */
	padding: TDGSS_PADDING_PKCS5Padding, /* GlobalQOP -> Padding */
}
var defaultQopEx5 = algQOPEx{
	legacyQOP:  defaultQop5,
	mechQOP:    TDGSS_C_QOP_DEFAULT,
	keyExchAlg: TDGSS_ALG_NAME_DH,     /* GlobalQOP -> KeyExchangeAlgorithm */
	pKeySize:   TDGSS_KEY_LEN_P_K2048, /* GlobalQOP -> KeyLengthP */
}

/* AES-K256_CBC_PKCS5Padding_SHA1_DH-K2048 */
var defaultQop6 = algQOP{
	version: 1,                          /* Version information: usually 1      */
	cipher:  TDGSS_ALG_NAME_AES,         /* GlobalQOP -> ConfidentialityAlgorithm */
	digest:  TDGSS_ALG_NAME_SHA1,        /* GlobalQOP -> IntegrityAlgorithm */
	keySize: TDGSS_KEY_LEN_K256,         /* GlobalQOP -> KeyLength */
	mode:    TDGSS_MODE_CBC,             /* GlobalQOP -> Mode */
	padding: TDGSS_PADDING_PKCS5Padding, /* GlobalQOP -> Padding */
}
var defaultQopEx6 = algQOPEx{
	legacyQOP:  defaultQop6,
	mechQOP:    TDGSS_C_QOP_DEFAULT,
	keyExchAlg: TDGSS_ALG_NAME_DH,     /* GlobalQOP -> KeyExchangeAlgorithm */
	pKeySize:   TDGSS_KEY_LEN_P_K2048, /* GlobalQOP -> KeyLengthP */
}

/* AES-K128_AEADGCM_PKCS5Padding_SHA2_DH-K2048 */
var defaultQop7 = algQOP{
	version: 1,                          /* Version information: usually 1      */
	cipher:  TDGSS_ALG_NAME_AES,         /* GlobalQOP -> ConfidentialityAlgorithm */
	digest:  TDGSS_ALG_NAME_SHA256,      /* GlobalQOP -> IntegrityAlgorithm */
	keySize: TDGSS_KEY_LEN_K128,         /* GlobalQOP -> KeyLength */
	mode:    TDGSS_MODE_AEADGCM,         /* GlobalQOP -> Mode */
	padding: TDGSS_PADDING_PKCS5Padding, /* GlobalQOP -> Padding */
}
var defaultQopEx7 = algQOPEx{
	legacyQOP:  defaultQop7,
	mechQOP:    TDGSS_C_QOP_DEFAULT,
	keyExchAlg: TDGSS_ALG_NAME_DH,     /* GlobalQOP -> KeyExchangeAlgorithm */
	pKeySize:   TDGSS_KEY_LEN_P_K2048, /* GlobalQOP -> KeyLengthP */
}

/* AES-K192_AEADGCM_PKCS5Padding_SHA2_DH-K2048 */
var defaultQop8 = algQOP{
	version: 1,                          /* Version information: usually 1      */
	cipher:  TDGSS_ALG_NAME_AES,         /* GlobalQOP -> ConfidentialityAlgorithm */
	digest:  TDGSS_ALG_NAME_SHA256,      /* GlobalQOP -> IntegrityAlgorithm */
	keySize: TDGSS_KEY_LEN_K192,         /* GlobalQOP -> KeyLength */
	mode:    TDGSS_MODE_AEADGCM,         /* GlobalQOP -> Mode */
	padding: TDGSS_PADDING_PKCS5Padding, /* GlobalQOP -> Padding */
}
var defaultQopEx8 = algQOPEx{
	legacyQOP:  defaultQop8,
	mechQOP:    TDGSS_C_QOP_DEFAULT,
	keyExchAlg: TDGSS_ALG_NAME_DH,     /* GlobalQOP -> KeyExchangeAlgorithm */
	pKeySize:   TDGSS_KEY_LEN_P_K2048, /* GlobalQOP -> KeyLengthP */
}

/* AES-K256_AEADGCM_PKCS5Padding_SHA2_DH-K2048 */
var defaultQop9 = algQOP{
	version: 1,                          /* Version information: usually 1      */
	cipher:  TDGSS_ALG_NAME_AES,         /* GlobalQOP -> ConfidentialityAlgorithm */
	digest:  TDGSS_ALG_NAME_SHA256,      /* GlobalQOP -> IntegrityAlgorithm */
	keySize: TDGSS_KEY_LEN_K256,         /* GlobalQOP -> KeyLength */
	mode:    TDGSS_MODE_AEADGCM,         /* GlobalQOP -> Mode */
	padding: TDGSS_PADDING_PKCS5Padding, /* GlobalQOP -> Padding */
}
var defaultQopEx9 = algQOPEx{
	legacyQOP:  defaultQop9,
	mechQOP:    TDGSS_C_QOP_DEFAULT,
	keyExchAlg: TDGSS_ALG_NAME_DH,     /* GlobalQOP -> KeyExchangeAlgorithm */
	pKeySize:   TDGSS_KEY_LEN_P_K2048, /* GlobalQOP -> KeyLengthP */
}

/* AES-K128_GCM96_PKCS5Padding_SHA2_DH-K2048 */
var defaultQop10 = algQOP{
	version: 1,                          /* Version information: usually 1      */
	cipher:  TDGSS_ALG_NAME_AES,         /* GlobalQOP -> ConfidentialityAlgorithm */
	digest:  TDGSS_ALG_NAME_SHA256,      /* GlobalQOP -> IntegrityAlgorithm */
	keySize: TDGSS_KEY_LEN_K128,         /* GlobalQOP -> KeyLength */
	mode:    TDGSS_MODE_GCM96,           /* GlobalQOP -> Mode */
	padding: TDGSS_PADDING_PKCS5Padding, /* GlobalQOP -> Padding */
}
var defaultQopEx10 = algQOPEx{
	legacyQOP:  defaultQop10,
	mechQOP:    TDGSS_C_QOP_DEFAULT,
	keyExchAlg: TDGSS_ALG_NAME_DH,     /* GlobalQOP -> KeyExchangeAlgorithm */
	pKeySize:   TDGSS_KEY_LEN_P_K2048, /* GlobalQOP -> KeyLengthP */
}

/* AES-K192_GCM96_PKCS5Padding_SHA2_DH-K2048 */
var defaultQop11 = algQOP{
	version: 1,                          /* Version information: usually 1      */
	cipher:  TDGSS_ALG_NAME_AES,         /* GlobalQOP -> ConfidentialityAlgorithm */
	digest:  TDGSS_ALG_NAME_SHA256,      /* GlobalQOP -> IntegrityAlgorithm */
	keySize: TDGSS_KEY_LEN_K192,         /* GlobalQOP -> KeyLength */
	mode:    TDGSS_MODE_GCM96,           /* GlobalQOP -> Mode */
	padding: TDGSS_PADDING_PKCS5Padding, /* GlobalQOP -> Padding */
}
var defaultQopEx11 = algQOPEx{
	legacyQOP:  defaultQop11,
	mechQOP:    TDGSS_C_QOP_DEFAULT,
	keyExchAlg: TDGSS_ALG_NAME_DH,     /* GlobalQOP -> KeyExchangeAlgorithm */
	pKeySize:   TDGSS_KEY_LEN_P_K2048, /* GlobalQOP -> KeyLengthP */
}

/* AES-K256_GCM96_PKCS5Padding_SHA2_DH-K2048 */
var defaultQop12 = algQOP{
	version: 1,                          /* Version information: usually 1      */
	cipher:  TDGSS_ALG_NAME_AES,         /* GlobalQOP -> ConfidentialityAlgorithm */
	digest:  TDGSS_ALG_NAME_SHA256,      /* GlobalQOP -> IntegrityAlgorithm */
	keySize: TDGSS_KEY_LEN_K256,         /* GlobalQOP -> KeyLength */
	mode:    TDGSS_MODE_GCM96,           /* GlobalQOP -> Mode */
	padding: TDGSS_PADDING_PKCS5Padding, /* GlobalQOP -> Padding */
}
var defaultQopEx12 = algQOPEx{
	legacyQOP:  defaultQop12,
	mechQOP:    TDGSS_C_QOP_DEFAULT,
	keyExchAlg: TDGSS_ALG_NAME_DH,     /* GlobalQOP -> KeyExchangeAlgorithm */
	pKeySize:   TDGSS_KEY_LEN_P_K2048, /* GlobalQOP -> KeyLengthP */
}

/* AES-K128_CCM_PKCS5Padding_SHA2_DH-K2048 */

/* AES-K192_CCM_PKCS5Padding_SHA2_DH-K2048 */

/* AES-K256_CCM_PKCS5Padding_SHA2_DH-K2048 */

/* AES-K128_CTR_PKCS5Padding_SHA2_DH-K2048 */

/* AES-K192_CTR_PKCS5Padding_SHA2_DH-K2048 */

/* AES-K256_CTR_PKCS5Padding_SHA2_DH-K2048 */

/* Conversion table from string to algQOPEx */
var gExQops = map[string]*algQOPEx{
	"AES-K128_GCM_PKCS5Padding_SHA2_DH-K2048":     &defaultQopEx1,
	"AES-K128_CBC_PKCS5Padding_SHA1_DH-K2048":     &defaultQopEx2,
	"AES-K192_GCM_PKCS5Padding_SHA2_DH-K2048":     &defaultQopEx3,
	"AES-K192_CBC_PKCS5Padding_SHA1_DH-K2048":     &defaultQopEx4,
	"AES-K256_GCM_PKCS5Padding_SHA2_DH-K2048":     &defaultQopEx5,
	"AES-K256_CBC_PKCS5Padding_SHA1_DH-K2048":     &defaultQopEx6,
	"AES-K128_AEADGCM_PKCS5Padding_SHA2_DH-K2048": &defaultQopEx7,
	"AES-K192_AEADGCM_PKCS5Padding_SHA2_DH-K2048": &defaultQopEx8,
	"AES-K256_AEADGCM_PKCS5Padding_SHA2_DH-K2048": &defaultQopEx9,
	"AES-K128_GCM96_PKCS5Padding_SHA2_DH-K2048":   &defaultQopEx10,
	"AES-K192_GCM96_PKCS5Padding_SHA2_DH-K2048":   &defaultQopEx11,
	"AES-K256_GCM96_PKCS5Padding_SHA2_DH-K2048":   &defaultQopEx12,
}

var gTd2DefaultQopsList = []string{
	"AES-K128_GCM_PKCS5Padding_SHA2_DH-K2048",
	"AES-K128_CBC_PKCS5Padding_SHA1_DH-K2048",
	"AES-K192_GCM_PKCS5Padding_SHA2_DH-K2048",
	"AES-K192_CBC_PKCS5Padding_SHA1_DH-K2048",
	"AES-K256_GCM_PKCS5Padding_SHA2_DH-K2048",
	"AES-K256_CBC_PKCS5Padding_SHA1_DH-K2048",
	"AES-K128_GCM96_PKCS5Padding_SHA2_DH-K2048",
	"AES-K192_GCM96_PKCS5Padding_SHA2_DH-K2048",
	"AES-K256_GCM96_PKCS5Padding_SHA2_DH-K2048",
}

var gLdapDefaultQopsList = []string{
	"AES-K128_AEADGCM_PKCS5Padding_SHA2_DH-K2048",
	"AES-K192_AEADGCM_PKCS5Padding_SHA2_DH-K2048",
	"AES-K256_AEADGCM_PKCS5Padding_SHA2_DH-K2048",
	"AES-K128_GCM_PKCS5Padding_SHA2_DH-K2048",
	"AES-K128_CBC_PKCS5Padding_SHA1_DH-K2048",
	"AES-K192_GCM_PKCS5Padding_SHA2_DH-K2048",
	"AES-K192_CBC_PKCS5Padding_SHA1_DH-K2048",
	"AES-K256_GCM_PKCS5Padding_SHA2_DH-K2048",
	"AES-K256_CBC_PKCS5Padding_SHA1_DH-K2048",
	"AES-K128_GCM96_PKCS5Padding_SHA2_DH-K2048",
	"AES-K192_GCM96_PKCS5Padding_SHA2_DH-K2048",
	"AES-K256_GCM96_PKCS5Padding_SHA2_DH-K2048",
}

var gJwtDefaultQopsList = []string{
	"AES-K128_GCM96_PKCS5Padding_SHA2_DH-K2048",
	"AES-K192_GCM96_PKCS5Padding_SHA2_DH-K2048",
	"AES-K256_GCM96_PKCS5Padding_SHA2_DH-K2048",
	"AES-K128_AEADGCM_PKCS5Padding_SHA2_DH-K2048",
	"AES-K192_AEADGCM_PKCS5Padding_SHA2_DH-K2048",
	"AES-K256_AEADGCM_PKCS5Padding_SHA2_DH-K2048",
	"AES-K128_GCM_PKCS5Padding_SHA2_DH-K2048",
	"AES-K192_GCM_PKCS5Padding_SHA2_DH-K2048",
	"AES-K256_GCM_PKCS5Padding_SHA2_DH-K2048",
	"AES-K128_CBC_PKCS5Padding_SHA1_DH-K2048",
	"AES-K192_CBC_PKCS5Padding_SHA1_DH-K2048",
	"AES-K256_CBC_PKCS5Padding_SHA1_DH-K2048",
}

func GetQops(qopSearch []string) (qops []*algQOPEx) {

	for _, qopItem := range qopSearch {
		if qopMatch, match := gExQops[qopItem]; match {
			qops = append(qops, qopMatch)
			continue
		}
	}

	return
}

func GetTd2LegacyQop() (qop qopInfo) {
	qop.algQop = &gLegacyQop1
	qop.mechQop = 0
	return
}

func GetLdapLegacyQop() (qop qopInfo) {
	qop.algQop = &gLegacyQop0
	qop.mechQop = 0
	return
}
