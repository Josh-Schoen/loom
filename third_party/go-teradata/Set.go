// Copyright 2025 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"slices"
	"sync"
)

type Set [T comparable] interface {

	Add (added T)
	Len () int
	Contains (elem T) bool
	ToSlice () [] T

} // end interface Set

// ------------- HashSet implementation -------------

type presence struct {} // empty struct whose presence indicates that the element is present in the HashSet

type HashSet [T comparable] struct {

	elements map [T] presence

} // end type HashSet

var _ Set [int] = &HashSet [int] {} // HashSet implements Set

func (set *HashSet [T]) Add (added T) {

	if set.elements == nil {
		set.elements = map [T] presence {}
	}

	set.elements [added] = presence {}

} // end method Add

func (set *HashSet [T]) Len () int {

	return len (set.elements) // okay for set.elements to be nil

} // end method Len

func (set *HashSet [T]) Contains (elem T) (b bool) {

	if set.elements != nil {
		_, b = set.elements [elem]
	}

	return

} // end method Contains

func (set *HashSet [T]) ToSlice () (output [] T) {

	for elem := range set.elements { // okay for set.elements to be nil
		output = append (output, elem)
	}

	return

} // end method ToSlice

// ------------- ListSet implementation -------------

type ListSet [T comparable] struct {

	elements [] T

} // end type ListSet

var _ Set [int] = &ListSet [int] {} // ListSet implements Set

func (set *ListSet [T]) Add (added T) {

	for _, elem := range set.elements { // okay for set.elements to be nil
		if elem == added {
			return // already present
		}
	}

	set.elements = append (set.elements, added)

} // end method Add

func (set *ListSet [T]) Len () int {

	return len (set.elements) // okay for set.elements to be nil

} // end method Len

func (set *ListSet [T]) Contains (elem T) bool {

	return slices.Contains (set.elements, elem)

} // end method Contains

func (set *ListSet [T]) ToSlice () [] T {

	return append ([] T (nil), set.elements ...) // return a copy of the slice, okay for set.elements to be nil

} // end method ToSlice

// ------------- SynchronizedSet decorator -------------

type SynchronizedSet [T comparable] struct {

	Target   Set [T]
	mutex    sync.Mutex

} // end type SynchronizedSet

var _ Set [int] = &SynchronizedSet [int] {} // SynchronizedSet implements Set

func (set *SynchronizedSet [T]) Add (added T) {

	set.mutex.Lock ()
	defer set.mutex.Unlock ()

	set.Target.Add (added)

} // end method Add

func (set *SynchronizedSet [T]) Len () int {

	set.mutex.Lock ()
	defer set.mutex.Unlock ()

	return set.Target.Len ()

} // end method Len

func (set *SynchronizedSet [T]) Contains (elem T) bool {

	set.mutex.Lock ()
	defer set.mutex.Unlock ()

	return set.Target.Contains (elem)

} // end method Contains

func (set *SynchronizedSet [T]) ToSlice () [] T {

	set.mutex.Lock ()
	defer set.mutex.Unlock ()

	return set.Target.ToSlice ()

} // end method ToSlice
