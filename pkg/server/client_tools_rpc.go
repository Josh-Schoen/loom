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
package server

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/clienttools"
	"github.com/teradata-labs/loom/pkg/storage/postgres"
)

// SubmitClientToolResult delivers a caller's result for a client tool call to
// the StreamWeave turn waiting on it. A result is accepted once, and only from
// the user whose stream received the call; every other submission reads as
// "not pending" so it reveals nothing about calls the submitter does not own.
func (s *MultiAgentServer) SubmitClientToolResult(ctx context.Context, req *loomv1.SubmitClientToolResultRequest) (*loomv1.SubmitClientToolResultResponse, error) {
	if req.GetSessionId() == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}
	if req.GetToolCallId() == "" {
		return nil, status.Error(codes.InvalidArgument, "tool_call_id is required")
	}

	result := clienttools.Result{
		Success:      req.GetSuccess(),
		ErrorCode:    req.GetErrorCode(),
		ErrorMessage: req.GetErrorMessage(),
	}
	if req.GetSuccess() && req.GetOutput() != nil {
		result.Output = req.GetOutput().AsInterface()
	}

	accepted, reason := s.clientTools.Submit(req.GetSessionId(), req.GetToolCallId(), postgres.UserIDFromContext(ctx), result)
	if s.logger != nil {
		s.logger.Info("SubmitClientToolResult",
			zap.String("session_id", req.GetSessionId()),
			zap.String("tool_call_id", req.GetToolCallId()),
			zap.Bool("success", req.GetSuccess()),
			zap.Bool("accepted", accepted))
	}
	return &loomv1.SubmitClientToolResultResponse{Accepted: accepted, Error: reason}, nil
}
