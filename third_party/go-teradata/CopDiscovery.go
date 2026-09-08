// Copyright 2019 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"fmt"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"
)

var g_mutexHostNameRotations sync.Mutex // protects g_mapHostNameRotations
var g_mapHostNameRotations = map [string] int {}

type nameLookup struct {

	m_con           *TeradataConnection
	m_sHostName     string
	m_bLookupDone   bool
	m_aAddresses    [] string
	m_err           error

} // end type nameLookup

func newLookup (con *TeradataConnection, sHostName string) *nameLookup {

	return &nameLookup {
		m_con         : con,
		m_sHostName   : sHostName,
	}
} // end func newLookup

type lookupResult struct {

	m_sHostName     string
	m_asAddresses   [] string

} // end type lookupResult

func newLookupResult (sHostName string, asAddresses [] string) lookupResult {

	return lookupResult {
		m_sHostName     : sHostName,
		m_asAddresses   : asAddresses,
	}
} // end func newLookupResult

type nameAddress struct {

	m_sHostName   string
	m_sAddress    string

} // end type nameAddress

func newNameAddress (sHostName string, sAddress string) nameAddress {

	return nameAddress {
		m_sHostName   : sHostName,
		m_sAddress    : sAddress,
	}
} // end func newNameAddress

func (lookup *nameLookup) isLiteralIpAddress () bool {

	return net.ParseIP (lookup.m_sHostName) != nil

} // end method isLiteralIpAddress

func (lookup *nameLookup) getAddresses () (lookupResult, error) {

	if ! lookup.m_bLookupDone {

		lookup.m_bLookupDone = true

		timeStart := time.Now ()

		lookup.m_aAddresses, lookup.m_err = net.LookupHost (lookup.m_sHostName)

		if lookup.m_con.hasTimingLog () {
			timingLog (fmt.Sprintf ("Hostname lookup for %v took %v and returned %v", lookup.m_sHostName, formatElapsed (timeStart),
				func () string {
					if lookup.m_err != nil {
						return fmt.Sprintf ("error %v", lookup.m_err)
					} else {
						return fmt.Sprintf ("%v address(es) %v", len (lookup.m_aAddresses), lookup.m_aAddresses)
					}
				} ()))
		} // end if

		if lookup.m_err != nil {
			lookup.m_err = lookup.m_con.makeDriverError (lookup.m_err, 0, fmt.Sprintf ("Hostname lookup failed for %v", lookup.m_sHostName))
		}
	} // end if

	return newLookupResult (lookup.m_sHostName, lookup.m_aAddresses), lookup.m_err

} // end method getAddresses

func containsAddress (aNameAddrs [] nameAddress, sAddress string) bool {

	for _, nameAddr := range aNameAddrs {
		if sAddress == nameAddr.m_sAddress { return true }
	}

	return false

} // end func containsAddress

func discoverCops (con *TeradataConnection) (aNameAddrs [] nameAddress, err error) {

	if con.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter discoverCops %v", con))
		defer func () { traceLog (fmt.Sprintf ("< leave discoverCops %v", con)) } ()
	}

	lookupWithoutCop := newLookup (con, con.m_params.M_sHostName)
	resultNoCop      := lookupResult {}
	results2D        := [] lookupResult {}

	if lookupWithoutCop.isLiteralIpAddress () {

		resultNoCop, err = lookupWithoutCop.getAddresses ()
		if err != nil { return }

		results2D = append (results2D, resultNoCop)

	} else if con.m_params.M_bCop {

		iDot      := strings.Index (con.m_params.M_sHostName, ".")
		sBaseName := ""
		sDomain   := ""

		if iDot < 0 {
			sBaseName = con.m_params.M_sHostName
		} else {
			sBaseName = con.m_params.M_sHostName [ : iDot]
			sDomain   = con.m_params.M_sHostName [iDot : ]
		}

		resultCopLast := lookupResult {}

		if con.m_params.M_bCopLast {

			sCopLastHostName := sBaseName + "coplast" + sDomain

			resultCopLast, err = newLookup (con, sCopLastHostName).getAddresses ()
			if err != nil { return }
		}

		for nSuffix := 1 ; ; nSuffix++ {

			sCopHostName := sBaseName + "cop" + itoa (nSuffix) + sDomain

			resultCop, errIgnore := newLookup (con, sCopHostName).getAddresses ()
			if errIgnore != nil { break } // no more HostNameCOPnnn names

			results2D = append (results2D, resultCop)

			if len (resultCopLast.m_asAddresses) != 0 && resultCop.m_asAddresses [0] == resultCopLast.m_asAddresses [0] {

				if con.hasTimingLog () {
					timingLog (fmt.Sprintf ("discoverCops: Reached last cop %v", resultCop.m_asAddresses [0]))
				}

				break
			}
		} // end for
	} // end if COP Discovery

	// Use the DBS hostname itself if no HostNameCOPnnn names are defined in DNS.
	if len (results2D) == 0 {

		resultNoCop, err = lookupWithoutCop.getAddresses ()
		if err != nil { return }

		results2D = append (results2D, resultNoCop)
	}

	if len (results2D) > 1 {

		nRotation := rand.Intn (len (results2D))

		g_mutexHostNameRotations.Lock ()

		if nPrev, bFound := g_mapHostNameRotations [con.m_params.M_sHostName] ; bFound {
			nRotation = (nPrev + 1) % len (results2D)
		}

		g_mapHostNameRotations [con.m_params.M_sHostName] = nRotation

		g_mutexHostNameRotations.Unlock ()

		if con.hasDebugLog () {
			debugLog (fmt.Sprintf ("discoverCops: original %v", results2D))
		}

		results2D = append (results2D [nRotation : ], results2D [ : nRotation] ...)

		if con.hasDebugLog () {
			debugLog (fmt.Sprintf ("discoverCops: rotated %v %v", nRotation, results2D))
		}
	} // end if

	// Flatten the 2-D address list down to 1-D and omit any duplicates.
	for _, result := range results2D {
		for _, sAddress := range result.m_asAddresses {
			if ! containsAddress (aNameAddrs, sAddress) {
				aNameAddrs = append (aNameAddrs, newNameAddress (result.m_sHostName, sAddress))
			}
		}
	}

	return

} // end func discoverCops
