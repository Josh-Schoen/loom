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

// Package mobile runs a Loom agent inside a host application, such as an iOS
// app, instead of behind a server. It is written to be bound with gomobile,
// so every exported signature uses only strings, errors and interfaces.
//
// The host supplies two things through callbacks: the model (ModelProvider)
// and the tools (HostTool). The engine supplies the agent loop, session
// persistence in SQLite and, optionally, tracing. Turns use the same
// WeaveRequest, WeaveResponse and WeaveProgress messages as the gRPC API;
// the remaining messages are defined in proto/loom/v1/mobile.proto.
//
// Every message crosses the boundary as proto3 JSON with the field names
// written in the .proto files (snake_case). The engine also accepts
// lowerCamelCase names on input.
//
// Host callbacks may be invoked from several goroutines at once and must be
// safe for concurrent use.
package mobile
