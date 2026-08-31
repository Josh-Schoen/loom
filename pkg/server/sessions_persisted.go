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
	"sort"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/agent"
)

// persistedSessionState is the Session.state value for a session that exists in
// the store but is not loaded into any agent's memory. It is not "active" (no
// in-process session backs it) and not "idle" (idle implies loaded), so the
// listing says what is true: the row is persisted, nothing more. Clients render
// this string verbatim.
const persistedSessionState = "persisted"

// mergePersistedSessions returns the live in-process sessions plus the sessions
// that exist only in the store, ordered newest-updated first.
//
// Live entries win on ID collision: an in-process session carries richer state
// (live cost, live message count, "active") than the persisted row does.
//
// Requires the store to implement agent.SessionMetadataLister; when it does not
// (SessionStorage.ListSessions returns IDs alone, which cannot populate a
// Session message) the live listing is returned unchanged. Sessions are also
// returned unchanged when callerUserID is non-empty: the merge cannot attribute
// a persisted row to a tenant, so a multi-tenant caller must not see it.
func mergePersistedSessions(
	ctx context.Context,
	store agent.SessionStorage,
	live []*loomv1.Session,
	callerUserID string,
	logger *zap.Logger,
) []*loomv1.Session {
	merged := live
	if store != nil && callerUserID == "" {
		if lister, ok := store.(agent.SessionMetadataLister); ok {
			infos, err := lister.ListSessionInfos(ctx, 0)
			if err != nil {
				// A store read failure must not empty the live listing.
				if logger != nil {
					logger.Warn("failed to list persisted sessions", zap.Error(err))
				}
			} else {
				liveIDs := make(map[string]struct{}, len(live))
				for _, sess := range live {
					liveIDs[sess.GetId()] = struct{}{}
				}
				for _, info := range infos {
					if _, loaded := liveIDs[info.ID]; loaded {
						continue
					}
					merged = append(merged, convertPersistedSession(info))
				}
			}
		}
	}

	// Newest-updated first, ID as tiebreak. In-memory sessions arrive in Go map
	// order, so without this the listing shuffles between calls.
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].GetUpdatedAt() != merged[j].GetUpdatedAt() {
			return merged[i].GetUpdatedAt() > merged[j].GetUpdatedAt()
		}
		return merged[i].GetId() < merged[j].GetId()
	})

	return merged
}

// convertPersistedSession converts store metadata to a proto Session. Name is
// left empty when the session was never named — clients fall back to the ID
// rather than being handed a fabricated name.
func convertPersistedSession(info agent.SessionMetadata) *loomv1.Session {
	sess := &loomv1.Session{
		Id:                info.ID,
		Name:              info.Name,
		CreatedAt:         info.CreatedAt.Unix(),
		UpdatedAt:         info.UpdatedAt.Unix(),
		State:             persistedSessionState,
		TotalCostUsd:      info.TotalCostUSD,
		ConversationCount: int32(info.MessageCount), // #nosec G115 -- message counts are far below int32
	}
	// Metadata carries the fields proto Session has no home for. The desktop
	// listing includes workflow sub-agent sessions (they are real rows in the
	// store); these two keys are what a client needs to tell them apart.
	if info.AgentID != "" || info.ParentSessionID != "" {
		sess.Metadata = map[string]string{}
		if info.AgentID != "" {
			sess.Metadata["agent_id"] = info.AgentID
		}
		if info.ParentSessionID != "" {
			sess.Metadata["parent_session_id"] = info.ParentSessionID
		}
	}
	return sess
}

// persistedConversationHistory loads a session's messages straight from the
// store, for callers whose in-memory lookup missed.
//
// Read-only: the session is NOT inserted into agent memory, so viewing history
// does not adopt or resume the thread. (Memory.GetOrCreateSession already
// rehydrates a persisted session on the next Chat to that ID; this path
// deliberately does not trigger it.)
//
// Returns codes.NotFound when there is no store or the store has no such
// session. A persisted session with no messages returns an empty history, not
// NotFound.
func persistedConversationHistory(
	ctx context.Context,
	store agent.SessionStorage,
	sessionID string,
	logger *zap.Logger,
) (*loomv1.ConversationHistory, error) {
	if store == nil {
		return nil, status.Error(codes.NotFound, "session not found")
	}

	session, err := store.LoadSession(ctx, sessionID)
	if err != nil || session == nil {
		// Backends report "not found" as a plain error, indistinguishable from a
		// read failure, so both surface as NotFound — logged so the difference is
		// recoverable from the daemon log.
		if err != nil && logger != nil {
			logger.Debug("persisted session lookup failed",
				zap.String("session_id", sessionID),
				zap.Error(err))
		}
		return nil, status.Error(codes.NotFound, "session not found")
	}

	messages := session.GetMessages()
	protoMessages := make([]*loomv1.Message, len(messages))
	for i := range messages {
		protoMessages[i] = ConvertMessage(&messages[i])
	}

	return &loomv1.ConversationHistory{
		SessionId: sessionID,
		Messages:  protoMessages,
	}, nil
}
