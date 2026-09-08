// Copyright 2025 by Teradata Corporation. All rights reserved.
// TERADATA CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"fmt"
	"sync"
)

type OIDCMetadataCacheEntry struct {

	m_resp              *HttpResponse
	m_nCreateTimeSecs   int

} // end type OIDCMetadataCacheEntry

var g_mutexOIDCMetadataCache sync.Mutex // protects g_mapOIDCMetadataCache
var g_mapOIDCMetadataCache = map [/*sIdProURL*/ string] *OIDCMetadataCacheEntry {}

func (md *OIDCMetadataCacheEntry) isValid (con *TeradataConnection) bool {

	nLifetimeSecs := int (con.m_params.M_uOIDCMetadataCache)
	nDeadlineSecs := md.m_nCreateTimeSecs + nLifetimeSecs
	return currentTimeSeconds () < nDeadlineSecs

} // end method isValid

func getOIDCMetadataResponse (con *TeradataConnection, sIdProURL string) (resp *HttpResponse, err error) {

	if con.m_params.hasTraceLog () {
		traceLog (fmt.Sprintf ("> enter getOIDCMetadataResponse sIdProURL=%v", sIdProURL))
		defer func () { traceLog (fmt.Sprintf ("< leave getOIDCMetadataResponse sIdProURL=%v", sIdProURL)) } ()
	}

	g_mutexOIDCMetadataCache.Lock () // synchronized (g_mapOIDCMetadataCache)

	md, bFound := g_mapOIDCMetadataCache [sIdProURL]

	bValid := bFound && md.isValid (con)
	if bValid {
		resp = md.m_resp
	}

	g_mutexOIDCMetadataCache.Unlock () // end synchronized (g_mapOIDCMetadataCache)

	if resp == nil {
		resp, err = con.doHttpRequest ("GET", sIdProURL, /*aasHeaders*/ nil, /*abyRequestBody*/ nil, [] int {200})
		if err != nil {
			err = con.makeDriverError (err, 566, "Failed to obtain metadata from Identity Provider")
			return
		}

		g_mutexOIDCMetadataCache.Lock () // synchronized (g_mapOIDCMetadataCache)
		g_mapOIDCMetadataCache [sIdProURL] = &OIDCMetadataCacheEntry { m_resp : resp, m_nCreateTimeSecs : currentTimeSeconds () }
		g_mutexOIDCMetadataCache.Unlock () // end synchronized (g_mapOIDCMetadataCache)
	}

	if con.hasDebugLog () {
		debugLog ("getOIDCMetadataResponse: OIDC metadata cache " + ternary (bValid, "hit", ternary (bFound, "refresh", "miss")) + " for " + sIdProURL)
	}

	con.m_extra.m_sIdpProxyUsed  = resp.M_sProxyUsed
	con.m_extra.m_sIdpProtocol   = resp.M_sProtocol
	con.m_extra.m_sIdpCipher     = resp.M_sCipher
	con.m_extra.m_sIdpCertStatus = resp.M_sCertStatus
	return

} // end getOIDCMetadataResponse
