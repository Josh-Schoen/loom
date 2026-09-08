// Copyright 2017 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

func ternary       (b bool, s1 string, s2 string)        string { if b { return s1 } ; return s2 }
func coalesce      (s1 string, s2 string)                string { return ternary (s1 != "", s1, s2) }
func coalesce3     (s1 string, s2 string, s3 string)     string { return coalesce (s1, coalesce (s2, s3)) }
func yn            (b bool)                              string { return ternary (b, "Y", "N") }

func quoteString (sInput string, sQuote string) string {

	// Enclose in quotes and double any occurrences of the quote character
	return sQuote + strings.ReplaceAll (sInput, sQuote, sQuote + sQuote) + sQuote

} // end func quoteString

func singleQuote (sInput string) string {

	return quoteString (sInput, `'`)

} // end func singleQuote

func doubleQuote (sInput string) string {

	return quoteString (sInput, `"`)

} // end func doubleQuote

func indexOf (sInput string, sSubstring string, iStart int) (iOutput int) {

	iOutput = strings.Index (sInput [iStart : ], sSubstring)
	if iOutput >= 0 { iOutput += iStart }
	return

} // end indexOf

func limitWidthUTF8 (sInput string, uMaxByteCount uint64) string {

	nOutputByteCount := 0

	for i, nRuneByteCount := 0, 0 ; i < len (sInput) ; i += nRuneByteCount {

		_, nRuneByteCount = utf8.DecodeRuneInString (sInput [i : ])
		if (uint64 (i + nRuneByteCount) > uMaxByteCount) {
			break
		}

		nOutputByteCount = i + nRuneByteCount

	} // end for

	return sInput [ : nOutputByteCount]

} // end func limitWidthUTF8

func abbreviateString (s string, uMaxByteCount uint64) string {

	if uint64 (len (s)) > uMaxByteCount {
		s = limitWidthUTF8 (s, uMaxByteCount) + "..."
	}

	return s

} // end abbreviateString

func fixedWidthUTF8 (sInput string, uWidthInBytes uint64) string {

	return string (fixedWidthBytes (sInput, uWidthInBytes, ' '))

} // end func fixedWidthUTF8

func substringToRune (sInput string, iRunePastEnd int) string {

	iRune := 0

	for iFirstByteOfCodePoint := range sInput {

		if iRune >= iRunePastEnd { // return empty string when iRunePastEnd < 0
			return sInput [ : iFirstByteOfCodePoint]
		}

		iRune++

	} // end for

	return sInput // return entire string when iRunePastEnd >= len (sInput)

} // end func substringToRune

func substringFromRune (sInput string, iRuneStart int) string {

	iRune := 0

	for iFirstByteOfCodePoint := range sInput {

		if iRune >= iRuneStart { // return entire string when iRuneStart < 0
			return sInput [iFirstByteOfCodePoint : ]
		}

		iRune++

	} // end for

	return sInput [len (sInput) : ] // return empty string when iRuneStart >= len (sInput)

} // end func substringFromRune

func formatUint8    (u uint8 , nBase int) string { return strconv.FormatUint (uint64 (u), nBase) }
func formatUint16   (u uint16, nBase int) string { return strconv.FormatUint (uint64 (u), nBase) }
func formatUint32   (u uint32, nBase int) string { return strconv.FormatUint (uint64 (u), nBase) }
func formatUint64   (u uint64, nBase int) string { return strconv.FormatUint (        u , nBase) }

func format10Uint8  (u uint8            ) string { return formatUint8  (u, 10) }
func format10Uint16 (u uint16           ) string { return formatUint16 (u, 10) }
func format10Uint32 (u uint32           ) string { return formatUint32 (u, 10) }
func format10Uint64 (u uint64           ) string { return formatUint64 (u, 10) }

func parseUint8     (s string, nBase int) (uint8 , error) { u, err := strconv.ParseUint (s, nBase,  8) ; return uint8  (u), err }
func parseUint16    (s string, nBase int) (uint16, error) { u, err := strconv.ParseUint (s, nBase, 16) ; return uint16 (u), err }
func parseUint32    (s string, nBase int) (uint32, error) { u, err := strconv.ParseUint (s, nBase, 32) ; return uint32 (u), err }
func parseUint64    (s string, nBase int) (uint64, error) { u, err := strconv.ParseUint (s, nBase, 64) ; return         u , err }

func parse10Uint8   (s string           ) (uint8 , error) { return parseUint8  (s, 10) }
func parse10Uint16  (s string           ) (uint16, error) { return parseUint16 (s, 10) }
func parse10Uint32  (s string           ) (uint32, error) { return parseUint32 (s, 10) }
func parse10Uint64  (s string           ) (uint64, error) { return parseUint64 (s, 10) }

func atob (s string) (b bool) {
	b, _ = strconv.ParseBool (s) // ignore error
	return
}

func btoa (b bool  ) string { return strconv.FormatBool (b) }
func itoa (i int   ) string { return strconv.Itoa       (i) }
func utoa (u uint32) string { return format10Uint32     (u) }

var reNonDigits = regexp.MustCompile ("\\D+") // one or more non-digits

func parseAllUint32 (sInput string) (au [] uint32) {

	as := strings.SplitN (strings.TrimSpace (reNonDigits.ReplaceAllString (sInput, " ")), " ", -1)

	for _, s := range as {
		if s != "" {
			u, _ := parse10Uint32 (s)
			au = append (au, u)
		}
	}

	return

} // end parseAllUint32

func composeParseAllUint32 (sInput string, uDigitsPerNumber uint32, uNumberCount uint32) (uOutput uint32) {

	au     := parseAllUint32 (sInput)
	uLimit := minUint32 (uint32 (len (au)), uNumberCount)

	for i := uint32 (0) ; i < uLimit ; i++ {
		uOutput += au [i] * powUint32 (10, (uNumberCount - i - 1) * uDigitsPerNumber)
	}

	return

} // end func composeParseAllUint32

func formatByteSliceAsHexDigits (aby [] byte, sNilValue string) string {

	if aby == nil { return sNilValue }

	return hex.EncodeToString (aby)

} // end formatByteSliceAsHexDigits

func formatByteSliceAsHexDump (aby [] byte, sLinePrefix string, sNilValue string) string {

	if aby == nil { return sNilValue }

	return strings.ReplaceAll (strings.TrimSpace (hex.Dump (aby)), "\n", "\n" + sLinePrefix)

} // end formatByteSliceAsHexDump

func matchesPatternIgnoreCase (s string, sPattern string) bool {

	re, _ := regexp.Compile ("(?i)^" + sPattern + "$") // equivalent to Java's String.matches method which indicates whether the whole string matches the pattern
	return re != nil && re.MatchString (s)

} // end matchesPatternIgnoreCase

func omitEmptyStrings (asInput [] string) (asOutput [] string) {

	for _, s := range asInput {
		if s != "" {
			asOutput = append (asOutput, s)
		}
	}

	return

} // end omitEmptyStrings

func joinAllStrings (sSep string, asInput ... string) string {

	return strings.Join (omitEmptyStrings (asInput), sSep)

} // end joinAllStrings

func joinStrings (s1 string, sSep string, s2 string) string {

	return joinAllStrings (sSep, s1, s2)

} // end joinStrings

func makePath (s1 string, sSep string, s2 string) string {
	
	return joinStrings (strings.TrimRight (s1, sSep), sSep, strings.TrimLeft (s2, sSep))

} // end makePath

func smartDecodeBase64 (s string) (aby [] byte, err error) {

	if strings.ContainsAny (s, "+/") {
		aby, err = base64.StdEncoding.DecodeString (s)

	} else if strings.ContainsAny (s, "=") {
		aby, err = base64.URLEncoding.DecodeString (s)

	} else { // no padding
		aby, err = base64.RawURLEncoding.DecodeString (s)
	}

	return

} // end smartDecodeBase64
