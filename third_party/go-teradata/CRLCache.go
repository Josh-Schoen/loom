// Copyright 2023 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"crypto/x509"
	"sync"
	"time"
)

type crlCacheEntry struct {
	m_timeExpire       time.Time
	m_revocationList   *x509.RevocationList
}

type crlCache struct {
	m_mutex     sync.RWMutex
	m_entries   map [string] crlCacheEntry
}

var g_crlCache = &crlCache {
	m_entries : make (map [string] crlCacheEntry),
}

func (cache *crlCache) deleteExpiredEntries () {

	cache.m_mutex.Lock ()
	defer cache.m_mutex.Unlock ()

	timeNow := time.Now ()

	for sURL, entry := range cache.m_entries {
		if timeNow.After (entry.m_timeExpire) {
			delete (cache.m_entries, sURL)
		}
	}
} // end deleteExpiredEntries

// Add revocationList to the cache, mapped to the specified URL, with the specified Time-To-Live.
func (cache *crlCache) addToCache (sURL string, revocationList *x509.RevocationList, durTTL time.Duration) {

	cache.deleteExpiredEntries ()

	cache.m_mutex.Lock ()
	defer cache.m_mutex.Unlock ()

	cache.m_entries [sURL] = crlCacheEntry {
		m_timeExpire     : time.Now ().Add (durTTL),
		m_revocationList : revocationList,
	}
} // end addToCache

// Return the revocation list mapped to the specified URL if it exists, return nil otherwise.
func (cache *crlCache) readCache (sURL string) *x509.RevocationList {

	cache.deleteExpiredEntries ()

	cache.m_mutex.RLock ()
	defer cache.m_mutex.RUnlock ()

	if entry, bFound := cache.m_entries [sURL] ; bFound {
		return entry.m_revocationList
	}

	return nil

} // end readCache
