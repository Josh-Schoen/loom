// Copyright 2026 Teradata
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package mobile

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// marshalOptions writes the field names used in the .proto files so a host
// can read the contract straight from them.
var marshalOptions = protojson.MarshalOptions{UseProtoNames: true}

// strictUnmarshal rejects unknown fields. It is used for input the host
// authors by hand (configuration and requests), where an unknown field is
// almost always a misspelling.
var strictUnmarshal = protojson.UnmarshalOptions{}

// lenientUnmarshal ignores unknown fields. It is used for model responses so
// a host built against a newer contract keeps working.
var lenientUnmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}

func marshalJSON(m proto.Message) (string, error) {
	b, err := marshalOptions.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// toValue converts any JSON-encodable Go value to a protobuf Value.
func toValue(v interface{}) (*structpb.Value, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := &structpb.Value{}
	if err := protojson.Unmarshal(b, out); err != nil {
		return nil, err
	}
	return out, nil
}

// toStruct converts a JSON-encodable Go value that encodes to a JSON object
// into a protobuf Struct.
func toStruct(v interface{}) (*structpb.Struct, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := &structpb.Struct{}
	if err := protojson.Unmarshal(b, out); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	return out, nil
}
