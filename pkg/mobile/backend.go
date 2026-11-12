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
	"context"
	"errors"

	"github.com/teradata-labs/loom/pkg/fabric"
)

// errNoBackend is returned by every data operation of hostBackend.
var errNoBackend = errors.New("the embedded engine has no execution backend; expose data to the agent through host tools")

// hostBackend satisfies the agent's ExecutionBackend requirement for an
// engine whose data access is provided entirely by host tools.
type hostBackend struct{}

func (hostBackend) Name() string { return "host" }

func (hostBackend) ExecuteQuery(context.Context, string) (*fabric.QueryResult, error) {
	return nil, errNoBackend
}

func (hostBackend) GetSchema(context.Context, string) (*fabric.Schema, error) {
	return nil, errNoBackend
}

func (hostBackend) ListResources(context.Context, map[string]string) ([]fabric.Resource, error) {
	return nil, nil
}

func (hostBackend) GetMetadata(context.Context, string) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}

func (hostBackend) Ping(context.Context) error { return nil }

func (hostBackend) Capabilities() *fabric.Capabilities { return fabric.NewCapabilities() }

func (hostBackend) ExecuteCustomOperation(context.Context, string, map[string]interface{}) (interface{}, error) {
	return nil, errNoBackend
}

func (hostBackend) Close() error { return nil }
