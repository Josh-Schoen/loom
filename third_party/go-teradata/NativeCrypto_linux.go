// Copyright 2025 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

func getNativeCryptoLib (params *ConParams) (sNativeCryptoLib string) {

	if params.hasTraceLog () {
		traceLog ("> enter getNativeCryptoLib")
		defer func () { traceLog (fmt.Sprintf ("< leave getNativeCryptoLib sNativeCryptoLib=%v", sNativeCryptoLib)) } ()
	}

	if G_sGoDistribution == "M" { // Microsoft Go

		sMapFilesDir := "/proc/self/map_files"

		setPathNames := HashSet [string] {}
		asCryptoNames := [] string {}

		entries, _ := os.ReadDir (sMapFilesDir) // ignore error
		for _, entry := range entries {

			// Each entry is a symbolic link to a mapped file
			sPathName, _ := os.Readlink (sMapFilesDir + "/" + entry.Name ()) // ignore error

			if setPathNames.Contains (sPathName) {
				continue // skip duplicate map ranges for the same file
			} else {
				setPathNames.Add (sPathName)
			}

			bMatch := strings.Contains (sPathName, "/libcrypto.so")
			if bMatch {
				asCryptoNames = append (asCryptoNames, sPathName)
			}

			if params.hasDebugLog () {
				debugLog (fmt.Sprintf ("getNativeCryptoLib: %v mapped file %v", yn (bMatch), sPathName))
			}
		} // end for

		slices.Sort (asCryptoNames) // sort for reproducible results

		sNativeCryptoLib = joinAllStrings (" ", asCryptoNames ...)

	} // end if Microsoft Go

	return

} // end getNativeCryptoLib
