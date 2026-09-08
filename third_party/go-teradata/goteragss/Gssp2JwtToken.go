// Copyright 2021 by Teradata Corporation. All Rights Reserved.
package goteragss

import (
	"encoding/asn1"
	"encoding/binary"
)

const (
	DIFFIE_HELLMAN                          = 0

	CLIENT_INFO_TOKEN_TAG                   = 0x0
	SERVER_INFO_TOKEN_TAG                   = 0x1
	ENCRYPTED_JWT_TAG                       = 0x2

	/* Client Info Tags */
	CLIENT_INFO_TOKEN_TOKENHEADER_TAG       = 0x0
	CLIENT_INFO_TOKEN_CIPHERSUITE_TAG       = 0x1
	CLIENT_INFO_TOKEN_JWTTOKEN_TAG          = 0x2

	/* ServerInfo Tags */
	SERVER_INFO_TOKEN_TOKENHEADER_TAG       = 0x0
	SERVER_INFO_KEYEXCHANGE_PARAMS_TAG      = 0x1
	SERVER_INFO_QOPPARCEL_TAG               = 0x2

	/* Encrypted JWT tags */
	ENCRYPTED_JWT_TOKENHEADER_TAG           = 0x0
	ENCRYPTED_JWT_KEYEXCHANGE_PARAMS_TAG    = 0x1
	ENCRYPTED_PAYLOAD_TAG                   = 0x2

	/* Token Header Tags */
	JWT_TOKENHEADER_VERSION_TAG             = 0x0
	JWT_TOKENHEADER_MSGTYPE_TAG             = 0x1
	JWT_TOKENHEADER_CLIENTORSERVER_TAG      = 0x2
	JWT_TOKENHEADER_CAPABILITY_TAG          = 0x3
	JWT_TOKENHEADER_REQUESTFLAG_TAG         = 0x4
	JWT_TOKENHEADER_NEWCAPABILITY_TAG       = 0x5

	/* Key Exchange Param tags */
	KEYEXCHANGE_PARAMS_TYPE_TAG             = 0x0
	DH_KEYEXCHANGE_PARAMS_TAG               = 0x1

	/* DH Param Tags */
	DH_KEYEXCHANGE_PARAMS_P_TAG             = 0x0
	DH_KEYEXCHANGE_PARAMS_G_TAG             = 0x1
	DH_KEYEXCHANGE_PARAMS_PEERPUBLICKEY_TAG = 0x2

	/* JWT Encrypted Payload tags */
	JWT_ENCRYPTEDPAYLOAD_TAG                = 0x0

	/* Encrypted Payload Param Tag*/
	ENCRYPTEDPAYLOAD_PARAMS_JWTTOKEN_TAG    = 0x0
	ENCRYPTEDPAYLOAD_PARAMS_EXTLOGDATA_TAG  = 0x1
)

type jwtTokenHeader struct {
	version         byte
	msgType         byte
	clientOrServer  byte
	capabilities    uint32
	requestFlags    byte
	newCapabilities uint32
}


func jwtEncodeHdr(jwtTokenHdr jwtTokenHeader) (status TdgssStatus, wrappedHdr []byte) {
	/*
		ASN.1 encode the JWT Token header which is different from the TD2 Token header.
	
		e0 0f   GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | ENCRYPTED_JWT_TOKENHEADER_TAG

			c0 01 03          GSSP_TAG_PRIVATE | JWT_TOKENHEADER_VERSION_TAG
			c1 01 01          GSSP_TAG_PRIVATE | JWT_TOKENHEADER_MSGTYPE_TAG
			c2 01 00          GSSP_TAG_PRIVATE | JWT_TOKENHEADER_CLIENTORSERVER_TAG
			c3 01 01          GSSP_TAG_PRIVATE | JWT_TOKENHEADER_CAPABILITY_TAG
			c4 01 3e          GSSP_TAG_PRIVATE | JWT_TOKENHEADER_REQUESTFLAG_TAG

	*/

	version := makeASN1Value(GSSP_TAG_PRIVATE | JWT_TOKENHEADER_VERSION_TAG, jwtTokenHdr.version)
	msgType := makeASN1Value(GSSP_TAG_PRIVATE | JWT_TOKENHEADER_MSGTYPE_TAG, jwtTokenHdr.msgType)
	clientServer := makeASN1Value(GSSP_TAG_PRIVATE | JWT_TOKENHEADER_CLIENTORSERVER_TAG, jwtTokenHdr.clientOrServer)

	capabilities := makeASN1Value(GSSP_TAG_PRIVATE | JWT_TOKENHEADER_CAPABILITY_TAG, makeASN1Uint32Payload(jwtTokenHdr.capabilities)...)
	newCapabilities := makeASN1Value(GSSP_TAG_PRIVATE | JWT_TOKENHEADER_NEWCAPABILITY_TAG, makeASN1Uint64Payload(uint64 (jwtTokenHdr.newCapabilities))...)
	
	reqFlags := makeASN1Value(GSSP_TAG_PRIVATE | JWT_TOKENHEADER_REQUESTFLAG_TAG, jwtTokenHdr.requestFlags)

	wrappedHdr = makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND|ENCRYPTED_JWT_TOKENHEADER_TAG,
		concat(version, msgType, clientServer, capabilities, reqFlags, newCapabilities)...)

	status.Major = GSS_S_COMPLETE

	return
} // End jwtEncodeHdr

func jwtEncodeKeyExchange(publicKey []byte) (status TdgssStatus, wrappedKeyExchange []byte) {
	/*
		ASN.1 encode the key exchange parameters. For the client, we only need to send the 
		public key to the server using key exchange type DIFFIE_HELLMAN

		e1 82 01 0b         GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | ENCRYPTED_JWT_KEYEXCHANGE_PARAMS_TAG

			c0 01 00        GSSP_TAG_PRIVATE | KEYEXCHANGE_PARAMS_TYPE_TAG

			e1 82 01 04     GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | DH_KEYEXCHANGE_PARAMS_TAG
				c2 82 01 00 GSSP_TAG_PRIVATE | DH_KEYEXCHANGE_PARAMS_PEERPUBLICKEY_TAG
									 64  2e e3 fd c1 b9 fb fc 4b
				08 34 36 d6 53 bc 32 df  f5 16 ae 6b c4 93 ec 63
				7d 25 86 2b e7 3e 4f 7e  d1 fe 84 98 f1 01 0c e9
				ec 5a bd 12 17 7e d6 25  3a 47 f4 7f fb 93 f9 0f
				da ba 74 39 eb e3 4b f0  d4 e4 49 78 70 cb df 16
				93 2e d5 85 b7 60 0f 4f  3d 45 60 c1 47 2d 24 f4
				fb 14 6c 5c 56 bb e3 a9  20 6d 2c 90 11 f8 52 4d
				55 09 14 fb da e4 e3 e1  63 bf e9 5b 1c 5b 61 48
				b9 88 36 41 9e 2c 18 70  f3 6a 4b 73 09 66 d3 18
				51 d5 40 f4 97 a6 94 24  12 ef 6d 27 ad ce 7c c2
				a0 46 72 de 8c 90 b5 9e  ee 43 1b ce d9 6d c2 19
				89 78 b2 5b 37 d9 59 97  69 34 15 bc 4c ed 27 2d
				01 7d 4f 83 14 b4 45 69  35 41 0b 22 f3 ed 91 1e
				29 7b f9 d2 10 15 29 e3  68 20 dc 6f 56 af b4 24
				82 e5 c4 cc a3 71 7e d6  f9 9d 09 4e c5 1f 5d 06
				98 cc 0a 99 d8 ca fb c9  da cb 8d d1 4a 95 36 cc
				59 59 99 79 87 e8 cd 


	*/

	if len(publicKey) == 0 {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_INVALID_ARGUMENT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	encodedExchangeType := makeASN1Value(GSSP_TAG_PRIVATE | KEYEXCHANGE_PARAMS_TYPE_TAG, DIFFIE_HELLMAN)

	encodedPublicKey := makeASN1Value(GSSP_TAG_PRIVATE | DH_KEYEXCHANGE_PARAMS_PEERPUBLICKEY_TAG, publicKey...)
	encodedKeyExchange := makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | DH_KEYEXCHANGE_PARAMS_TAG, encodedPublicKey...)

	wrappedKeyExchange = makeASN1Value(GSSP_TAG_PRIVATE|GSSP_TAG_COMPOUND|ENCRYPTED_JWT_KEYEXCHANGE_PARAMS_TAG,
		concat(encodedExchangeType, encodedKeyExchange)...)

	status.Major = GSS_S_COMPLETE
	return

} // End jwtEncodeKeyExchange

func jwtEncodeJWT(abyJwt []byte) (status TdgssStatus, encodedJWT []byte) {
	/*
		ASN.1 encode JWT before we wrap.

		e0 82 03 0d       GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND |JWT_ENCRYPTEDPAYLOAD_TAG
			c0 82 01 00   GSSP_TAG_PRIVATE | ENCRYPTEDPAYLOAD_PARAMS_JWTTOKEN_TAG

				JWT
	*/

	if len(abyJwt) == 0 {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_INVALID_ARGUMENT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	encodedJWT = makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | JWT_ENCRYPTEDPAYLOAD_TAG,
	makeASN1Value(GSSP_TAG_PRIVATE | ENCRYPTEDPAYLOAD_PARAMS_JWTTOKEN_TAG, abyJwt...)...)

	status.Major = GSS_S_COMPLETE
	return

} // end jwtEncodeJWT

func jwtEncodeEncryptedJWT(jwtTokenHdr jwtTokenHeader, publicKey []byte, payload []byte) (status TdgssStatus, wrappedEncryptedJWT []byte) {
	/*
		ASN.1 encode the encrypted (wrapped) JWT. The token includes
		1) JWT token header
		2) Key exchange parameters, for the client this includes the client's public key
		3) Wrapped JWT (payload)

		e2 82 04 99               GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | ENCRYPTED_JWT_TAG
			e0 0f   GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | ENCRYPTED_JWT_TOKENHEADER_TAG

				c0 01 03          GSSP_TAG_PRIVATE | JWT_TOKENHEADER_VERSION_TAG
				c1 01 01          GSSP_TAG_PRIVATE | JWT_TOKENHEADER_MSGTYPE_TAG
				c2 01 00          GSSP_TAG_PRIVATE | JWT_TOKENHEADER_CLIENTORSERVER_TAG
				c3 01 01          GSSP_TAG_PRIVATE | JWT_TOKENHEADER_CAPABILITY_TAG
				c4 01 3e          GSSP_TAG_PRIVATE | JWT_TOKENHEADER_REQUESTFLAG_TAG

			e1 82 01 0b           GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | ENCRYPTED_JWT_KEYEXCHANGE_PARAMS_TAG

				c0 01 00          GSSP_TAG_PRIVATE | KEYEXCHANGE_PARAMS_TYPE_TAG

				e1 82 01 04       GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND |DH_KEYEXCHANGE_PARAMS_TAG
					c2 82 01 00   GSSP_TAG_PRIVATE | DH_KEYEXCHANGE_PARAMS_PEERPUBLICKEY_TAG
										64  2e e3 fd c1 b9 fb fc 4b
					08 34 36 d6 53 bc 32 df  f5 16 ae 6b c4 93 ec 63
					7d 25 86 2b e7 3e 4f 7e  d1 fe 84 98 f1 01 0c e9
					ec 5a bd 12 17 7e d6 25  3a 47 f4 7f fb 93 f9 0f
					da ba 74 39 eb e3 4b f0  d4 e4 49 78 70 cb df 16
					93 2e d5 85 b7 60 0f 4f  3d 45 60 c1 47 2d 24 f4
					fb 14 6c 5c 56 bb e3 a9  20 6d 2c 90 11 f8 52 4d
					55 09 14 fb da e4 e3 e1  63 bf e9 5b 1c 5b 61 48
					b9 88 36 41 9e 2c 18 70  f3 6a 4b 73 09 66 d3 18
					51 d5 40 f4 97 a6 94 24  12 ef 6d 27 ad ce 7c c2
					a0 46 72 de 8c 90 b5 9e  ee 43 1b ce d9 6d c2 19
					89 78 b2 5b 37 d9 59 97  69 34 15 bc 4c ed 27 2d
					01 7d 4f 83 14 b4 45 69  35 41 0b 22 f3 ed 91 1e
					29 7b f9 d2 10 15 29 e3  68 20 dc 6f 56 af b4 24
					82 e5 c4 cc a3 71 7e d6  f9 9d 09 4e c5 1f 5d 06
					98 cc 0a 99 d8 ca fb c9  da cb 8d d1 4a 95 36 cc
					59 59 99 79 87 e8 cd 

			c2 82 03 72           GSSP_TAG_PRIVATE | ENCRYPTED_PAYLOAD_TAG
					payload is the wrapped JWT.


	*/

	if len(publicKey) == 0  || len(payload) == 0 {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_INVALID_ARGUMENT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	_, encodedHdr := jwtEncodeHdr(jwtTokenHdr)
	var encodedPublicKey []byte
	status, encodedPublicKey = jwtEncodeKeyExchange(publicKey)
	if status.Major != GSS_S_COMPLETE {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	encodedPayload := makeASN1Value(GSSP_TAG_PRIVATE | ENCRYPTED_PAYLOAD_TAG, payload...)

	wrappedEncryptedJWT = makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | ENCRYPTED_JWT_TAG,
		concat(encodedHdr, encodedPublicKey, encodedPayload)...)

	status.Major = GSS_S_COMPLETE
	return

} // End jwtEncodeEncryptedJWT

func jwtEncodeClientInfo(jwtTokenHdr jwtTokenHeader, cipherSuite []byte) (status TdgssStatus, wrappedClientInfo []byte) {
	/*
		ASN.1 encode the first token in the key exchange for JWT.
		This includes the JWT token header and the cipher suites.
		
		e0 59  GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | CLIENT_INFO_TOKEN_TAG
			e0 12  GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND |SERVER_INFO_TOKEN_TOKENHEADER_TAG
				c0 01 01 
				c1 01 01 
				c2 01 00 
				c3 04 15 00 00 00
				c4 01 3e
			c1 43 GSSP_TAG_PRIVATE | CLIENT_INFO_TOKEN_CIPHERSUITE_TAG
				ASN1 encoded Cipher suite
	*/

	if len(cipherSuite) == 0 {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_INVALID_ARGUMENT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	_, encodedHdr := jwtEncodeHdr(jwtTokenHdr)
	encodedCiphers := makeASN1Value(GSSP_TAG_PRIVATE | CLIENT_INFO_TOKEN_CIPHERSUITE_TAG, cipherSuite...)

	wrappedClientInfo = makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | CLIENT_INFO_TOKEN_TAG,
		concat(encodedHdr, encodedCiphers)...)

	status.Major = GSS_S_COMPLETE
	return

} // End jwtEncodeClientInfo

func jwtEncodeClientInfoTls (jwtTokenHdr jwtTokenHeader, abyToken [] byte) (status TdgssStatus, wrappedClientInfo []byte) {
	/*
		ASN.1 encode the first token in the key exchange for JWT.

		e0 82 03 c2  GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | CLIENT_INFO_TOKEN_TAG
			e0 15    GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | CLIENT_INFO_TOKEN_TOKENHEADER_TAG
				c0 01 01                GSSP_TAG_PRIVATE | JWT_TOKENHEADER_VERSION_TAG
				c1 01 01                GSSP_TAG_PRIVATE | JWT_TOKENHEADER_MSGTYPE_TAG
				c2 01 00                GSSP_TAG_PRIVATE | JWT_TOKENHEADER_CLIENTORSERVER_TAG
				c3 01 00                GSSP_TAG_PRIVATE | JWT_TOKENHEADER_CAPABILITY_TAG
				c4 01 3c                GSSP_TAG_PRIVATE | JWT_TOKENHEADER_REQUESTFLAG_TAG
				c5 04 80 00 00 00       GSSP_TAG_PRIVATE | JWT_TOKENHEADER_NEWCAPABILITY_TAG
			c2 02 GSSP_TAG_PRIVATE | CLIENT_INFO_TOKEN_JWTTOKEN_TAG
	*/

	if len (abyToken) == 0 {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_INVALID_ARGUMENT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	_, encodedHdr := jwtEncodeHdr(jwtTokenHdr)
	encodedToken := makeASN1Value(GSSP_TAG_PRIVATE | CLIENT_INFO_TOKEN_JWTTOKEN_TAG, abyToken ...)

	wrappedClientInfo = makeASN1Value(GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | CLIENT_INFO_TOKEN_TAG,  concat (encodedHdr, encodedToken) ...)

	status.Major = GSS_S_COMPLETE
	return

} // End jwtEncodeClientInfoTls


// jwtDecodeHdr returns a jwtTokenHeader struct populated with the decoded information
// from the input JWT token header.
func jwtDecodeHdr(encodedHdr asn1Value) (status TdgssStatus, decodedHdr jwtTokenHeader) {

	// The token header should have five
	// children -- one child for each member
	// of the jwtTokenHeader struct.
	headerParts := encodedHdr.children
	if len(headerParts) < 5 {
		status.Major = GSS_S_FAILURE
		status.Minor = JWT_ERR_INVALID_ARGUMENT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Iterate over the headerParts in order to extract the version, msgType, clientOrServer,
	// capabilities, and requestFlag at each respective index.
	for i := 0; i < len(headerParts); i++ {
		if headerParts[i].parent.Class != asn1.ClassPrivate || len(headerParts[i].parent.Bytes) == 0 {
			status.Major = GSS_S_FAILURE
			status.Minor = TDGSS_ERR_ASN_TAG_INVALID
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		var uint64Temp uint64 = 0
		if len(headerParts[i].parent.Bytes) == 1 {
			uint64Temp = uint64(headerParts[i].parent.Bytes[0])
		} else if len(headerParts[i].parent.Bytes) == 4 {
			multiBytes := headerParts[i].parent.Bytes
			if headerParts[i].parent.Tag == JWT_TOKENHEADER_CAPABILITY_TAG {
				// Okay, things get a "bit" funky here.
				//
				// The value of the JWT_TOKENHEADER_CAPABILITY_TAG that we get from the
				// encoded header comes in as little-endian in some cases, but we need
				// it to be big-endian. To do this, we need to swap the byte order if
				// and only if the first byte & 0xFF is not zero.
				//
				// Note that the bytes that come in are _perceived_ to be big-endian, so
				// we use binary.LittleEndian to swap the order, even though that kind of
				// seems counterintuitive.
				//
				// Example:
				//		c3 04 15 00 00 00              <======= The JWT_TOKENHEADER_CAPABILITY_TAG received from the server. The data is little-endian.
				//		[]byte{0x15, 0x00, 0x00, 0x00} <======= The corresponding headerParts[i].parent.Bytes value.
				//
				//		0x00000015                     <======= After we apply binary.LittleEndian.Uint32(headerParts[i].parent.Bytes).
				//		[]byte{0x0, 0x0, 0x0, 0x15}    <======= After we apply binary.BigEndian.PutUint32() to the resulting value from the line above.
				if (multiBytes[0] & 0xFF) != 0 {
					swappedByteOrder := binary.LittleEndian.Uint32(multiBytes)

					// Then we'll stick the now-big-endian capabilities value into a
					// byte slice. This byte slice will contain the bytes in big-endian
					// order for subsequent use.
					multiBytes = nil
					multiBytes = make([] byte, 4)
					binary.BigEndian.PutUint32(multiBytes, swappedByteOrder)
				}

				// By the time we get here the multiBytes byte slice
				// should be in the correct byte order.
				status, uint64Temp = readASN1Uint64Payload(multiBytes)
				if status.Major != GSS_S_COMPLETE {
					status.Major = GSS_S_FAILURE
					status.Minor = JWT_ERR_INVALID_INPUT
					status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
					return
				}

			} else if headerParts[i].parent.Tag == JWT_TOKENHEADER_NEWCAPABILITY_TAG {
				// The JWT_TOKENHEADER_NEWCAPABILITY_TAG value from the encoded header may be in little-endian
				// format, but we require it in big-endian. To achieve this, we swap the byte order only if the
				// fourth byte & 0xFF is not zero.
				//
				// Note: The incoming bytes are perceived as big-endian, so we use binary.LittleEndian to swap the order,
				// even though it might seem counterintuitive.
				// Example:
				//		c5 04 00 00 00 80              <======= The JWT_TOKENHEADER_NEWCAPABILITY_TAG received from the server. The data is little-endian.
				//		[]byte{0x00, 0x00, 0x00, 0x80} <======= The corresponding headerParts[i].parent.Bytes value.
				//
				//		0x80000000                     <======= After we apply binary.LittleEndian.Uint32(headerParts[i].parent.Bytes).
				//		[]byte{0x80, 0x00, 0x00, 0x00} <======= After we apply binary.BigEndian.PutUint32() to the resulting value from the line above.
				// Determine if the new capabilities are in little-endian format by examining the fourth byte; 
				// if it is non-zero, then it is in little-endian format (0x00, 0x00, 0x00, 0x80).
				if (multiBytes [len (multiBytes) - 1] & 0xFF) != 0 {
					// This will flip the byte order of the multiBytes slice.
					uint64Temp = uint64 (binary.LittleEndian.Uint32 (multiBytes))

				} else {
					// No swapping is necessary
					uint64Temp = uint64 (binary.BigEndian.Uint32 (multiBytes))
				}
			}

		} else {
			// ERROR: The headerParts[i].parent.Bytes length is not 1 or 4.
			status.Major = GSS_S_FAILURE
			status.Minor = JWT_ERR_INVALID_INPUT
			status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))

			// Clear data that was already decoded
			decodedHdr = jwtTokenHeader{}
			return
		}

		// Now we'll populate the jwtTokenHeader struct.
		switch headerParts[i].parent.Tag {
		case JWT_TOKENHEADER_VERSION_TAG:
			decodedHdr.version = byte(uint64Temp)
		case JWT_TOKENHEADER_MSGTYPE_TAG:
			decodedHdr.msgType = byte(uint64Temp)
		case JWT_TOKENHEADER_CLIENTORSERVER_TAG:
			decodedHdr.clientOrServer = byte(uint64Temp)
		case JWT_TOKENHEADER_CAPABILITY_TAG:
			decodedHdr.capabilities = uint32(uint64Temp)
		case JWT_TOKENHEADER_NEWCAPABILITY_TAG:
			decodedHdr.newCapabilities = uint32(uint64Temp)
		case JWT_TOKENHEADER_REQUESTFLAG_TAG:
			decodedHdr.requestFlags = byte(uint64Temp)
		default:
			// ignore new values added to the header
		}
	}

	status.Major = GSS_S_COMPLETE
	return
} // End jwtDecodeHdr


// jwtDecodeKeyExchange returns the decoded key exchange information (i.e. the P value, the G value, and the peer public key) given the encoded
// key exchange data.
func jwtDecodeKeyExchange(encodedKeyExchange asn1Value) (status TdgssStatus, decodedPValue []byte, decodedGValue []byte, decodedPeerPublic []byte) {

	// Ensure that we have key exchange data to decode.
	keyExchangeParts := encodedKeyExchange.children
	if len(keyExchangeParts) != 2 {
		status.Major = GSS_S_FAILURE
		status.Minor = JWT_ERR_KEYEXCHANGEPARAMS_EMPTY
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// This child must indicate a KEYEXCHANGE_PARAMS_TYPE_TAG.
	if keyExchangeParts[0].parent.Class != asn1.ClassPrivate || keyExchangeParts[0].parent.IsCompound || keyExchangeParts[0].parent.Tag != KEYEXCHANGE_PARAMS_TYPE_TAG {
		status.Major = GSS_S_FAILURE
		status.Minor = JWT_ERR_INVALID_INPUT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Currently, the key exchange type should always be Diffie-Hellman.
	keyExchangeType := uint(keyExchangeParts[0].parent.Bytes[0])
	if keyExchangeType == DIFFIE_HELLMAN {
		// This child must indicate a DH_KEYEXCHANGE_PARAMS_TAG.
		if keyExchangeParts[1].parent.Class != asn1.ClassPrivate || !keyExchangeParts[1].parent.IsCompound || keyExchangeParts[1].parent.Tag != DH_KEYEXCHANGE_PARAMS_TAG {
			status.Major = GSS_S_FAILURE
			status.Minor = TDGSS_ERR_ASN_TAG_INVALID
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		// The children of this keyExchangePart
		// should contain the P value, G value,
		// and peer public key.
		DhParts := keyExchangeParts[1].children
		if len(DhParts) == 0 {
			status.Major = GSS_S_FAILURE
			status.Minor = JWT_ERR_DHPARAMS_EMPTY
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		// Iterate over the DhParts in order to extract the 
		// P value, G value, and peer public key at each
		// respective index.
		for i := 0; i < len(DhParts); i++ {
			if DhParts[i].parent.Class != asn1.ClassPrivate || len(DhParts[i].parent.Bytes) == 0 {
				status.Major = GSS_S_FAILURE
				status.Minor = TDGSS_ERR_ASN_TAG_INVALID
				status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
				return
			}

			// Now we'll populate the return values.
			switch (DhParts[i].parent.Tag) {
			case DH_KEYEXCHANGE_PARAMS_P_TAG:
				decodedPValue = DhParts[i].parent.Bytes
			case DH_KEYEXCHANGE_PARAMS_G_TAG:
				decodedGValue = DhParts[i].parent.Bytes
			case DH_KEYEXCHANGE_PARAMS_PEERPUBLICKEY_TAG:
				decodedPeerPublic = DhParts[i].parent.Bytes
			default:
				status.Major = GSS_S_FAILURE
				status.Minor = TDGSS_ERR_ASN_TAG_INVALID
				status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
				return
			}
		}
	} else {
		status.Major = GSS_S_FAILURE
		status.Minor = JWT_ERR_INVALID_KEY_EXCHANGE
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = GSS_S_COMPLETE
	return
} // End jwtDecodeKeyExchange


// jwtDecodeServerInfo returns the decoded JWT token header, the key exchange values, and the QoP parcel given an encoded server info token.
func jwtDecodeServerInfo(serverInfo []byte) (status TdgssStatus, jwtTokenHdr jwtTokenHeader, pValue []byte, gValue []byte, peerPublic []byte, qopParcel []byte) {

	// Ensure that we have valid input for decoding.
	if len(serverInfo) == 0 || serverInfo == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = JWT_ERR_INVALID_ARGUMENT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Translate the serverInfo token (which is a big
	// ol' slice of bytes) into an unmarshaled ASN.1
	// format for subsequent processing.
	asn1ServerInfo, err := readASN1Values(serverInfo)
	if err != nil {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_ASN_TAG_INVALID
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}

	// Validate the asn1ServerInfo token. The identifiers must be:
	//		Tag class: GSSP_TAG_PRIVATE
	//		Octet type: constructed/compound/set (i.e. 0, 1, or more elements)
	//		Tag number: SERVER_INFO_TOKEN_TAG
	if asn1ServerInfo[0].parent.Class != asn1.ClassPrivate || !asn1ServerInfo[0].parent.IsCompound || asn1ServerInfo[0].parent.Tag != SERVER_INFO_TOKEN_TAG {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_ASN_TAG_INVALID
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Valid token confirmed. Time to process its children,
	// which must contain: the token header, the key
	// exchange data, and the QoPs parcel.
	asn1ServerInfoParts := asn1ServerInfo[0].children
	if len(asn1ServerInfoParts) != 3 {
		status.Major = GSS_S_FAILURE
		status.Minor = JWT_ERR_SERVERINFOTOKEN_EMPTY
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	
	// Iterate over the asn1ServerInfoParts in order to extract
	// the token header, the key exchange data, and the QoP
	// parcel at each respective index.
	for i := 0; i < len(asn1ServerInfoParts); i++ {
		if asn1ServerInfoParts[i].parent.Class != asn1.ClassPrivate {
			status.Major = GSS_S_FAILURE
			status.Minor = TDGSS_ERR_ASN_TAG_INVALID
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}

		switch asn1ServerInfoParts[i].parent.Tag {
		// The JWT header struct has version,
		// msgType, clientOrServer, capabilities,
		// and requestFlags members that we'll harvest.
		case SERVER_INFO_TOKEN_TOKENHEADER_TAG:
			if !asn1ServerInfoParts[i].parent.IsCompound {
				status.Major = GSS_S_FAILURE
				status.Minor = TDGSS_ERR_ASN_TAG_INVALID
				status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
				return
			}
			status, jwtTokenHdr = jwtDecodeHdr(asn1ServerInfoParts[i])
			if status.Major != GSS_S_COMPLETE {
				status.Major = GSS_S_FAILURE
				status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
				return
			}
		// The key exchange data has a P value,
		// a G value, and a peer public key that
		// we'll harvest.
		case SERVER_INFO_KEYEXCHANGE_PARAMS_TAG:
			if !asn1ServerInfoParts[i].parent.IsCompound {
				status.Major = GSS_S_FAILURE
				status.Minor = TDGSS_ERR_ASN_TAG_INVALID
				status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
				return
			}
			status, pValue, gValue, peerPublic = jwtDecodeKeyExchange(asn1ServerInfoParts[i])
			if status.Major != GSS_S_COMPLETE {
				status.Major = GSS_S_FAILURE
				status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
				return
			}
		// Retrieve the serverInfo QoP
		// parcel. No need to decode it here.
		case SERVER_INFO_QOPPARCEL_TAG:
			qopParcel = asn1ServerInfoParts[i].parent.Bytes
		default:
			status.Major = GSS_S_FAILURE
			status.Minor = TDGSS_ERR_ASN_TAG_INVALID
			status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
			return
		}
	}

	status.Major = GSS_S_COMPLETE
	return

} // End jwtDecodeServerInfo

// jwtDecodeServerInfoTls returns the decoded JWT token header. Called only when DH/CI Bypass is enabled.
//
// Expected format of server info token:
// GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | SERVER_INFO_TOKEN_TAG
//     GSSP_TAG_PRIVATE | GSSP_TAG_COMPOUND | SERVER_INFO_TOKEN_TOKENHEADER_TAG
//         version
//         messageType
//         client/server
//         capabilities
//         requestFlags
//         newCapabilities

func jwtDecodeServerInfoTls(abyServerInfo []byte) (status TdgssStatus, jwtTokenHdr jwtTokenHeader) {

	// Ensure that we have valid input for decoding.
	if len(abyServerInfo) == 0 || abyServerInfo == nil {
		status.Major = GSS_S_FAILURE
		status.Minor = JWT_ERR_INVALID_ARGUMENT
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Translate the serverInfo token into an unmarshaled ASN.1
	// format for subsequent processing.
	asn1ServerInfo, err := readASN1Values(abyServerInfo)
	if err != nil {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_ASN_TAG_INVALID
		status.Err = tdgssError(err, formatTdgssStatusErrOnly(status))
		return
	}

	// Validate the asn1ServerInfo token. The identifiers must be:
	//		Tag class: GSSP_TAG_PRIVATE
	//		Octet type: constructed/compound/set (i.e. 0, 1, or more elements)
	//		Tag number: SERVER_INFO_TOKEN_TAG
	if asn1ServerInfo[0].parent.Class != asn1.ClassPrivate || !asn1ServerInfo[0].parent.IsCompound || asn1ServerInfo[0].parent.Tag != SERVER_INFO_TOKEN_TAG {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_ASN_TAG_INVALID
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	// Valid token confirmed. Process its child, which must only contain the token header.
	asn1ServerInfoParts := asn1ServerInfo[0].children
	if len(asn1ServerInfoParts) != 1 {
		status.Major = GSS_S_FAILURE
		status.Minor = JWT_ERR_SERVERINFOTOKEN_EMPTY
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}
	
	// Validate the asn1ServerInfoParts token. The identifiers must be:
	//		Tag class: GSSP_TAG_PRIVATE
	//		Octet type: constructed/compound/set (i.e. 0, 1, or more elements)
	//		Tag number: SERVER_INFO_TOKEN_TOKENHEADER_TAG
	if asn1ServerInfoParts[0].parent.Class != asn1.ClassPrivate || !asn1ServerInfoParts[0].parent.IsCompound || asn1ServerInfoParts[0].parent.Tag != SERVER_INFO_TOKEN_TOKENHEADER_TAG {
		status.Major = GSS_S_FAILURE
		status.Minor = TDGSS_ERR_ASN_TAG_INVALID
		status.Err = tdgssError(nil, formatTdgssStatusErrOnly(status))
		return
	}

	status, jwtTokenHdr = jwtDecodeHdr(asn1ServerInfoParts[0])
	if status.Major != GSS_S_COMPLETE {
		status.Major = GSS_S_FAILURE
		status.Err = tdgssError(status.Err, formatTdgssStatusErrOnly(status))
		return
	}

	status.Major = GSS_S_COMPLETE
	return

} // End jwtDecodeServerInfoTls
