// Copyright 2019 by Teradata Corporation. All Rights Reserved.
// TERADATA CORPORATION CONFIDENTIAL AND TRADE SECRET

package teradata

import (
	"encoding/json"
)

func mapStringStringAddAll (mapInput1 map [string] string, mapInput2 map [string] string) (mapOutput map [string] string) {

	mapOutput = map [string] string {}
	for key, value := range mapInput1 { mapOutput [key] = value }
	for key, value := range mapInput2 { mapOutput [key] = value }
	return

} // end mapStringStringAddAll

func mapStringStringAsJSON (mapInput map [string] string) (sOutput string, err error) {

	aby, err := json.Marshal (mapInput)
	if err != nil { return }

	sOutput = string (aby)
	return

} // end mapStringStringAsJSON

func mapStringStringToStruct (mapInput map [string] string, pOutput any) (err error) {

	sJSON, err := mapStringStringAsJSON (mapInput)
	if err != nil { return }

	err = shredJSON (sJSON, pOutput)
	return

} // end mapStringStringToStruct

type MapEntry [K comparable, V any] struct {
	Key   K
	Value V
}

func entrySet [K comparable, V any] (m map [K] V) (ame [] MapEntry [K, V]) {

	for k, v := range m {
		ame = append (ame, MapEntry [K, V] { Key: k, Value: v })
	}

	return

} // end entrySet
