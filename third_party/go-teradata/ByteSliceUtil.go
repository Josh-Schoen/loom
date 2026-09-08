// Copyright 2017 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"bytes"
	"encoding/binary"
)

func combinedLength (aaby [][] byte) (nLength int) {

	for _, aby := range aaby {
		nLength += len (aby)
	}

	return

} // end func combinedLength

func copyByteSlice (aby [] byte) [] byte {

	return append ([] byte (nil), aby ...)

} // end func copyByteSlice

func copyByteSlicesInto (abyOutput [] byte, aabyInputs [][] byte) {

	i := 0
	for _, abyInput := range aabyInputs {
		i += copy (abyOutput [i : ], abyInput)
	}
} // end func copyByteSlicesInto

func padByteSlice (abyInput [] byte, uMinByteCount uint64, byPad byte) [] byte {

	uInputByteCount := uint64 (len (abyInput))
	if (uInputByteCount < uMinByteCount) {

		nDelta := int (uMinByteCount - uInputByteCount)

		// Use a full slice expression with slice capacity third element, to prevent append from reusing the backing store.
		return append (abyInput [ : uInputByteCount : uInputByteCount], bytes.Repeat ([] byte {byPad}, nDelta) ...)
	}

	return abyInput

} // end func padByteSlice

func fixedWidthBytes (sInput string, uWidthInBytes uint64, byPad byte) [] byte {

	return padByteSlice ([] byte (limitWidthUTF8 (sInput, uWidthInBytes)), uWidthInBytes, byPad)

} // end func fixedWidthBytes

func twoBytes (u uint16) [] byte {

	abyOutput := make ([] byte, 2)
	binary.BigEndian.PutUint16 (abyOutput, u)
	return abyOutput

} // end func twoBytes

func fourBytes (u uint32) [] byte {

	abyOutput := make ([] byte, 4)
	binary.BigEndian.PutUint32 (abyOutput, u)
	return abyOutput

} // end func fourBytes

func eightBytes (u uint64) [] byte {

	abyOutput := make ([] byte, 8)
	binary.BigEndian.PutUint64 (abyOutput, u)
	return abyOutput

} // end func eightBytes
