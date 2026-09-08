// Copyright 2017 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"math/big"
)

func minInt (a, b int) int {

	if a < b { return a } ; return b

} // end func minInt

func minUint8 (a, b uint8) uint8 {

	if a < b { return a } ; return b

} // end func minUint8

func minUint16 (a, b uint16) uint16 {

	if a < b { return a } ; return b

} // end func minUint16

func minUint32 (a, b uint32) uint32 {

	if a < b { return a } ; return b

} // end func minUint32

func minUint64 (a, b uint64) uint64 {

	if a < b { return a } ; return b

} // end func minUint64

func maxInt (a, b int) int {

	if a > b { return a } ; return b

} // end func maxInt

func maxUint8 (a, b uint8) uint8 {

	if a > b { return a } ; return b

} // end func maxUint8

func maxUint16 (a, b uint16) uint16 {

	if a > b { return a } ; return b

} // end func maxUint16

func maxUint32 (a, b uint32) uint32 {

	if a > b { return a } ; return b

} // end func maxUint32

func maxUint64 (a, b uint64) uint64 {

	if a > b { return a } ; return b

} // end func maxUint64

func powInt (a, b int) (p int) {

	p = 1

	for b > 0 {
		if b & 1 != 0 { p *= a }
		b >>= 1
		a *= a
	}

	return

} // end func powInt

func powUint32 (a, b uint32) (p uint32) {

	p = 1

	for b > 0 {
		if b & 1 != 0 { p *= a }
		b >>= 1
		a *= a
	}

	return

} // end func powUint32

func powUint64 (a, b uint64) (p uint64) {

	p = 1

	for b > 0 {
		if b & 1 != 0 { p *= a }
		b >>= 1
		a *= a
	}

	return

} // end func powUint64

func bigIntDigitCount (pnInput *big.Int) int {

	var nAbs big.Int
	nAbs.Abs (pnInput)
	return len (nAbs.String ())

} // end func bigIntDigitCount
