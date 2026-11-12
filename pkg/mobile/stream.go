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
	"fmt"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"google.golang.org/grpc/metadata"
)

// ProgressListener is implemented by the host to receive progress during
// Engine.StreamWeave.
type ProgressListener interface {
	// OnProgress receives one WeaveProgress as proto3 JSON. Events arrive in
	// order on a single goroutine. The last event of a successful turn has
	// stage EXECUTION_STAGE_COMPLETED and carries the answer in
	// partial_content.
	OnProgress(progressJSON string)
}

// progressStream delivers the server's progress stream to a host listener
// in-process, standing in for the gRPC transport.
type progressStream struct {
	ctx      context.Context
	listener ProgressListener
}

func (s *progressStream) Send(p *loomv1.WeaveProgress) error {
	out, err := marshalJSON(p)
	if err != nil {
		return fmt.Errorf("encode progress: %w", err)
	}
	s.listener.OnProgress(out)
	return nil
}

func (s *progressStream) Context() context.Context { return s.ctx }

// Headers and trailers have no in-process recipient.
func (s *progressStream) SetHeader(metadata.MD) error  { return nil }
func (s *progressStream) SendHeader(metadata.MD) error { return nil }
func (s *progressStream) SetTrailer(metadata.MD)       {}

func (s *progressStream) SendMsg(m interface{}) error {
	p, ok := m.(*loomv1.WeaveProgress)
	if !ok {
		return fmt.Errorf("unexpected stream message type %T", m)
	}
	return s.Send(p)
}

func (s *progressStream) RecvMsg(interface{}) error {
	return errors.New("the progress stream is send-only")
}
