// Copyright 2019 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

func sliceContainsStringIndex (as [] string, sToFind string) int {

	for i, s := range as {
		if s == sToFind { return i }
	}

	return -1 // indicates not found

} // end func sliceContainsStringIndex

func sliceContainsString (as [] string, sToFind string) bool {

	return sliceContainsStringIndex (as, sToFind) >= 0

} // end func sliceContainsString
