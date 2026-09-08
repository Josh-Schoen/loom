// Copyright 2023 by Teradata Corporation. All Rights Reserved.
package goteragss

import (
	"encoding/asn1"
	"sync"
)

type negMechHashTable struct { 
	m_negMechHashTable    sync.Map  // key: MechOid, value: NegMech
	m_nCount              int       // Sync.map does not have a size
}

func newNegMechHashTable () *negMechHashTable {
	return &negMechHashTable {}
}

func (mechTable *negMechHashTable) get (mechOid asn1.ObjectIdentifier) (*negMech) {
	oid, ok := mechTable.m_negMechHashTable.Load (mechOid.String ())
	if ok { return oid.(*negMech) }
	return nil
}

func (mechTable *negMechHashTable) put (mechOid asn1.ObjectIdentifier, temp *negMech) {
	mechTable.m_negMechHashTable.Store (mechOid.String (), temp)
	mechTable.m_nCount++
}

func (mechTable *negMechHashTable) size () (int) {
	return mechTable.m_nCount
}

func (mechTable *negMechHashTable) encodeNegMechList () (abyOutMechTokens [] byte) {
	if (mechTable.m_nCount > 0 ) {
		mechTable.m_negMechHashTable.Range (func (key, value any) bool {
			negMech := value.(*negMech)
			abyOutMechTokens = append (abyOutMechTokens, negMech.encodeNegMech ()...)
			return true
		})
		return
	}
	return
} // end encodeNegMechList

func (mechTable *negMechHashTable) decodeNegMechList (asn1MechList asn1Value) (status TdgssStatus) {
	for _, ans1NegMech := range asn1MechList.children {

		negMech := newNegMech ()
		status = negMech.decodeNegMech (ans1NegMech)

		if status.Major != GSS_S_COMPLETE { return }
		mechTable.put (negMech.getMechType (), negMech)
	}

	status.Major = GSS_S_COMPLETE
	status.Minor = TDNEGO_S_COMPLETE
	return
} // end decodeNegMechList
