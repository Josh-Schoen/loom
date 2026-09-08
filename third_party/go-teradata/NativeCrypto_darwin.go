// Copyright 2025 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"fmt"
)

func getNativeCryptoLib (params *ConParams) (sNativeCryptoLib string) {

	if params.hasTraceLog () {
		traceLog ("> enter getNativeCryptoLib")
		defer func () { traceLog (fmt.Sprintf ("< leave getNativeCryptoLib sNativeCryptoLib=%v", sNativeCryptoLib)) } ()
	}

	if G_sGoDistribution == "M" { // Microsoft Go
		sNativeCryptoLib = "macOS CryptoKit"
	}

	return

} // end getNativeCryptoLib
