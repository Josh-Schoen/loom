// Copyright 2021 by Teradata Corporation. All Rights Reserved.

package goteragss

import (
	"encoding/binary"
	"encoding/asn1"
)


// Cipher suite constants: DER-type private tags
const (
	GSSP_TAG_PRIVATE         = 0xC0
	GSSP_TAG_APPLICATION     = 0x40
	GSSP_TAG_COMPOUND        = 0x20
	GSSP_TAG_CIPHER_SUITES   = 1
	GSSP_TAG_CIPHER_SUITE    = 2
	GSSP_TAG_QOP_DEFINITIONS = 3
	GSSP_TAG_SET_DEFAULT_QOP = 4
	GSSP_TAG_SET_LOW_QOP     = 5
	GSSP_TAG_SET_MEDIUM_QOP  = 6
	GSSP_TAG_SET_HIGH_QOP    = 7
	GSSP_TAG_CONF_ALG        = 16
	GSSP_TAG_INTEG_ALG       = 17
	GSSP_TAG_KEY_EXCH_ALG    = 18
	GSSP_TAG_MODE            = 19
	GSSP_TAG_PADDING         = 20
	GSSP_TAG_KEY_LEN         = 21
	GSSP_TAG_KEY_LEN_P       = 22
)


// asn1Value contains an undecoded ASN.1 object and its children.
type asn1Value struct {
	parent   asn1.RawValue
	children []asn1Value
}


/*
	Returns a byte slice containing a tag class, the length (in bytes) of the
	data to follow, and the data itself.

	If the length of the data fits in 7 bits that is <= 127, then we only need one byte for the length.
	If the length of the data cannot fit in 7 bits that is > 127, then we need 2 or more bytes for the
	length of the data. The high order bit of the first byte is set and then the other 7 bits will contain
	the number of bytes required for the length of the data.

	For example

	[0x81 0xff] means that the length of the data is 255 (0xff) which is > 127. 0x81 has the high order bit
	set and the other 7 bits says that it requires one byte to hold the length, 255.

	[0x82 0xff 0xff] means that the length of the data is 65535 (0xff 0xff) which is > 127. 0x82 has the high order bit
	set and the other 7 bits says that it requires 2 byte to hold the length, 65535.)

*/
func makeASN1Value(byTag byte, abyBody ... byte) (abyOutput []byte) {
	if len(abyBody) <= 127 {
		// Length of the data is <= 127 so it will only require one byte for the length.
		abyOutput = make([]byte, 0, 2 + len(abyBody))
		abyOutput = append(abyOutput, byTag, byte (len(abyBody)))
		abyOutput = append(abyOutput, abyBody ...)
	} else {
		// Length of the data is > 127 so it will require 2 or more bytes for the length.
		// We need to figure out the minimum number of bytes required for the length.
		// [0x00 0x00 0x00 0x00 0x00 0x00 0xff 0xff]  ==> [0xff 0xff]

		seqLength := uint64(len(abyBody))

		// buf will hold the minimum number of bytes required for the length
		buf := []byte{}
		// while we still have bytes that are non-zero.
		for seqLength != 0	{
			// Prepend the least significant byte to buf
			buf = append([]byte{byte(seqLength & 0xFF)}, buf...)
			// Remove the least significant byte
			seqLength >>= 8
		}
		// get the minimum number of bytes for the length
		numBytes := byte(len(buf))

		abyOutput = make([]byte, 0, 2 + len(buf) + len(abyBody))

		// append the tag and number of bytes with high order bit set
		abyOutput = append(abyOutput, byTag, numBytes | 0x80)
		abyOutput = append(abyOutput, buf...)
		abyOutput = append(abyOutput, abyBody ...)
	}
	return
} // end makeASN1Value

/*
	Given an 64bit signed integer, return the minimum number of bytes needed to hold the signed integer value.

	The most significant bit (MSB) of the first byte dictates whether the entire number is considered non-negative or negative.
	When the MSB of the first byte is clear, then the entire number is non-negative.
	When the MSB of the first byte is set, meaning bit mask 0x80, then the entire number is negative.

	This function trims the unneeded leading bytes.
	A leading byte is unneeded when it meets the following 3 conditions:

	Condition 1.
	The byte must be a leading byte, meaning that there must be more than 1 byte in the slice.

	Condition 2.
	For a non-negative number, unneeded leading bytes will be zero.
	For a negative number, unneeded leading bytes will be 0xFF.

	Condition 3.
	The next byte after the leading byte has an MSB that is correct for the integer's sign.
	For a non-negative number, the next byte must have an MSB that is clear.
	For a negative number, the next byte must have an MSB that is set, meaning bit mask 0x80.

	Example (non-negative):
	n = 255
	minimum number of bytes to represent 255: [0x00, 0xFF]

	Example (negative)
	n = -256
	minimum number of bytes to represent -256: [0xFF, 0x00]

*/
func makeASN1Int64Payload(n int64) (abyOutput []byte) {

	abyOutput = make([]byte, 8)
	binary.BigEndian.PutUint64(abyOutput, uint64(n))

	var byTrim byte = 0
	if n < 0 {
		byTrim = 0xFF
	}

	// while there is at least 2 bytes and the most significant byte == the byTrim
	// and the next byte's high order bit is the same as byTrim's high order bit
	for len(abyOutput) > 1 && abyOutput[0] == byTrim && (abyOutput[1] & 0x80) == (byTrim & 0x80) {
		// remove the most significant byte.
		abyOutput = abyOutput[1:]
	}
	return
} // end makeASN1Int64Payload

// makeASN1Uint64Payload converts a uint64 to a byte slice with the minimum number of bytes needed to hold the integer value.
func makeASN1Uint64Payload (n uint64) (abyOutput [] byte) {

	abyOutput = make ([] byte, 8)
	binary.BigEndian.PutUint64 (abyOutput, uint64(n))

	// Find the index of the first non-zero byte
    nStart := 0
    for nStart < len (abyOutput) - 1 && abyOutput [nStart] == 0 {
        nStart++
    }

	abyOutput = abyOutput [nStart:]

	return
} // end makeASN1Uint64Payload

func makeASN1Uint32Payload(n uint32) []byte {
	return makeASN1Int64Payload(int64(uint64(n)))

} // end makeASN1Uint32Payload

func readASN1Uint64Payload(aby []byte) (status TdgssStatus, n uint64) {

	if len(aby) == 0 || (aby[0] & 0x80) != 0 {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	abyOutput := make([]byte, 8 - len(aby))
	abyOutput = append(abyOutput, aby...)
	n = binary.BigEndian.Uint64(abyOutput)
	status.Major = GSS_S_COMPLETE
	return
}

func readASN1Uint32(aby []byte) (status TdgssStatus, n uint32) {

	if len(aby) == 0  {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	abyOutput := aby
	if len(abyOutput) < 4 {
		abyOutput = make([]byte, 4 - len(aby))
		abyOutput = append(abyOutput, aby...)
	}

	n = binary.BigEndian.Uint32(abyOutput)
	status.Major = GSS_S_COMPLETE
	return

} // end readASN1Uint32

func readASN1ObjectIdentifier (aby []byte) (status TdgssStatus, oid asn1.ObjectIdentifier) {

	var mechOid []byte
	mechOid = append (mechOid, asn1.TagOID, byte (len (aby)))
	mechOid = append (mechOid, aby...)

	_, err := asn1.Unmarshal (mechOid, &oid)
	if (err != nil) {

		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	return

} // end readASN1ObjectIdentifier

// Utility function to encode a uint16 into an output byte slice
func twoBytes(u uint16) (abyOutput []byte) {
	abyOutput = make([]byte, 2)
	binary.BigEndian.PutUint16(abyOutput, u)
	return
} // end twoBytes


// Utility function to concatenate a variable number of byte slices into a
// single byte slice
func concat(aabyInputs ... []byte) (abyOutput []byte) {
	nByteCount := 0
	for _, aby := range aabyInputs {
		nByteCount += len(aby)
	}
	abyOutput = make([]byte, 0, nByteCount)
	for _, aby := range aabyInputs {
		abyOutput = append(abyOutput, aby ...)
	}
	return
} // end concat


// Returns a byte slice containing the hard-coded ASN.1 encoded cipher suites
func composeCipherSuites() []byte {
	return makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | GSSP_TAG_CIPHER_SUITES, concat(
		makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | GSSP_TAG_CIPHER_SUITE, concat(
			makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_CONF_ALG  , TDGSS_ALG_NAME_AES),
			makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_MODE     , TDGSS_MODE_CBC),
			makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_MODE     , TDGSS_MODE_ECB),
			makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_MODE     , TDGSS_MODE_OFB),
			makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_MODE     , TDGSS_MODE_CFB),
			makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_MODE     , TDGSS_MODE_GCM),
			makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_MODE     , TDGSS_MODE_CTR),
			makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_MODE     , TDGSS_MODE_AEADGCM),
			makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_MODE     , TDGSS_MODE_GCM96),
			makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_PADDING  , TDGSS_PADDING_PKCS5Padding),
			makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_KEY_LEN, twoBytes(TDGSS_KEY_LEN_K128) ...),
			makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_KEY_LEN, twoBytes(TDGSS_KEY_LEN_K192) ...),
			makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_KEY_LEN, twoBytes(TDGSS_KEY_LEN_K256) ...)) ...),
		makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | GSSP_TAG_CIPHER_SUITE,
			makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_INTEG_ALG, TDGSS_ALG_NAME_SHA1) ...),
		makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | GSSP_TAG_CIPHER_SUITE,
			makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_INTEG_ALG, TDGSS_ALG_NAME_SHA256) ...),
		makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | GSSP_TAG_CIPHER_SUITE,
			makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_INTEG_ALG, TDGSS_ALG_NAME_SHA512) ...),
		makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | GSSP_TAG_CIPHER_SUITE, concat(
			makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_KEY_EXCH_ALG, TDGSS_ALG_NAME_DIFFIE_HELLMAN),
			makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_KEY_LEN_P, twoBytes(TDGSS_KEY_LEN_P_K2048) ...)) ...)) ...)
} // end composeCipherSuites


// Retrieves the ASN.1 encoded cipher suites constructed by composeCipherSuites
func gsspGetCipherSuites() (status TdgssStatus, cipherSuitesParcel []byte) {
	cipherSuitesParcel = composeCipherSuites()
	return
} // End gsspGetCipherSuites


// readASN1Values parses the ASN.1 DER-encoded abyInput into the aOutput struct.
func readASN1Values(abyInput []byte) (aOutput []asn1Value, err error) {
	for len (abyInput) > 0 {
		var v asn1Value
		abyInput, err = asn1.Unmarshal(abyInput, &v.parent)
		if err != nil {
			return
		}
		if v.parent.IsCompound {
			v.children, err = readASN1Values(v.parent.Bytes)
			if err != nil {
				return
			}
		}
		aOutput = append(aOutput, v)
	}
	return
} // End readASN1Values


// twoBytesToUint16 converts two bytes to a uint.
func twoBytesToUint16(b []byte) (u uint16) {
	u = binary.BigEndian.Uint16(b)
	return
} // End twoBytesToUint


// asn1ToAlgQopEx fills in the algQOPEx struct given a single QoP (of strength
// default, low, medium, or high) and its associated properties.
func asn1ToAlgQopEx(qop asn1Value) (status TdgssStatus, algQopEx algQOPEx) {
	// Validate the given Qop; the identifiers must be:
	//		Tag class:  GSSP_TAG_PRIVATE
	//		Octet type: constructed/compound/set (0, 1, or more elements)
		if qop.parent.Class != asn1.ClassPrivate || !qop.parent.IsCompound {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_ERROR_DECODING_QOP_PARCEL
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// This QoP's strength should be one of: default, low, medium, or high.
	switch qop.parent.Tag {
	case GSSP_TAG_SET_DEFAULT_QOP:
		algQopEx.mechQOP = TDGSS_C_QOP_DEFAULT
	case GSSP_TAG_SET_LOW_QOP:
		algQopEx.mechQOP = TDGSS_C_QOP_LOW
	case GSSP_TAG_SET_MEDIUM_QOP:
		algQopEx.mechQOP = TDGSS_C_QOP_MEDIUM
	case GSSP_TAG_SET_HIGH_QOP:
		algQopEx.mechQOP = TDGSS_C_QOP_HIGH
	default:
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_ERROR_DECODING_QOP_PARCEL
		algQopEx = algQOPEx{}
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// For each of this QoP's properties...
	for i := 0; i < len(qop.children); i++ {
		// Validate this QoP's property data; the identifiers must be:
		//		Tag class:  GSSP_TAG_PRIVATE
		//		Octet type: primitive (the octets encode one element)
		if qop.children[i].parent.Class != asn1.ClassPrivate || qop.children[i].parent.IsCompound {
			status.Major = GSS_S_FAILURE
			status.Minor = TDGSS_ERR_ERROR_DECODING_QOP_PARCEL
			algQopEx = algQOPEx{}
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		// Get the size of the data in order to index into the
		// property's byte slice.
		szData      := uint(qop.children[i].parent.FullBytes[1])-1
		qopProperty := qop.children[i].parent

		// Fill the algQOPEx element with this QoP's associated data.
		switch qopProperty.Tag {
		case GSSP_TAG_CONF_ALG:
			algQopEx.legacyQOP.cipher  = uint(qopProperty.Bytes[szData])
		case GSSP_TAG_INTEG_ALG:
			algQopEx.legacyQOP.digest  = uint(qopProperty.Bytes[szData])
		case GSSP_TAG_KEY_EXCH_ALG:
			algQopEx.keyExchAlg        = uint(qopProperty.Bytes[szData])
		case GSSP_TAG_MODE:
			algQopEx.legacyQOP.mode    = uint(qopProperty.Bytes[szData])
		case GSSP_TAG_PADDING:
			algQopEx.legacyQOP.padding = uint(qopProperty.Bytes[szData])
		case GSSP_TAG_KEY_LEN:
			algQopEx.legacyQOP.keySize = uint(twoBytesToUint16(qopProperty.Bytes))
		case GSSP_TAG_KEY_LEN_P:
			algQopEx.pKeySize          = uint(twoBytesToUint16(qopProperty.Bytes))
		default:
			status.Major = GSS_S_FAILURE
			status.Minor = TDGSS_ERR_ERROR_DECODING_QOP_PARCEL
			algQopEx = algQOPEx{}
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}
	}
	algQopEx.legacyQOP.version = 1
	return
} // End asn1ToAlgQopEx


// gsspGetSelectedQops returns an array of algQOPEx elements given a parcel of negotiated QoPs from the
// Server. Upon return, the array will contain four algQOPEx elements in default, low, medium, and high order.
func gsspGetSelectedQops(qopsParcel []byte) (status TdgssStatus, selectedQops []algQOPEx) {
	// We must have a valid qopsParcel to proceed.
	if len(qopsParcel) == 0 {
		status.Major = GSS_S_CALL_INACCESSIBLE_READ
		status.Minor = TDGSS_ERR_INVALID_ARGUMENT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Translate the input into an undecoded
	// ASN.1 format for subsequent processing.
	asn1QopParcel, err := readASN1Values(qopsParcel)
	if err != nil {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_ERROR_DECODING_QOP_PARCEL
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}

	// Validate the QoP parcel; the identifiers must be:
	//		Tag class:  GSSP_TAG_PRIVATE
	//		Octet type: constructed/compound/set (0, 1, or more elements)
	//		Tag number: GSSP_TAG_QOP_DEFINITIONS
	if asn1QopParcel[0].parent.Class != asn1.ClassPrivate || !asn1QopParcel[0].parent.IsCompound || asn1QopParcel[0].parent.Tag != GSSP_TAG_QOP_DEFINITIONS {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_ERROR_DECODING_QOP_PARCEL
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// We've confirmed the set of QoP
	// definitions, now let's process
	// its four children: default, low
	// medium, and high.
	qops := asn1QopParcel[0].children

	// For each default, low, medium, and high QoP, process its data into the
	// output array of algQOPEx structs. Note that there will only be one QoP
	// for each of the default, low, medium, and high strengths.
	selectedQops = make([]algQOPEx, len(qops))
	for i := 0 ; i < len(qops) ; i++ {
			status, selectedQops[i] = asn1ToAlgQopEx(qops[i])
			if status.Major != GSS_S_COMPLETE {
				selectedQops = nil
				status.Major = GSS_S_FAILURE
				status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
				return
			}
	}

	status.Major = GSS_S_COMPLETE
	status.Minor = TDGSS_S_COMPLETE
	return

} // End gsspGetSelectedQops
