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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/agent"
	"github.com/teradata-labs/loom/pkg/observability"
	"github.com/teradata-labs/loom/pkg/server"
	"github.com/teradata-labs/loom/pkg/storage"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	// DefaultToolResultMemoryBytes bounds tool results held in memory when
	// MobileEngineConfig.tool_result_memory_bytes is zero. Larger results
	// overflow to disk under the data directory.
	DefaultToolResultMemoryBytes int64 = 32 << 20

	// toolResultCompressionThreshold and toolResultTTLSeconds match the
	// values the agent uses for the process-wide tool-result cache.
	toolResultCompressionThreshold int64 = 1 << 20
	toolResultTTLSeconds           int64 = 3600

	sessionDBName = "sessions.db"
	traceDBName   = "traces.db"

	// dataDirEnv is the variable Loom reads to locate its data directory.
	dataDirEnv = "LOOM_DATA_DIR"
)

// honouredAgentFields and honouredBehaviorFields list the AgentConfig and
// BehaviorConfig fields the engine applies. A config that sets any other
// field is rejected, so a field added to those messages later is refused
// until the engine supports it instead of being silently ignored.
var (
	honouredAgentFields = map[protoreflect.Name]bool{
		"name": true, "description": true, "system_prompt": true, "rom": true, "behavior": true,
	}
	honouredBehaviorFields = map[protoreflect.Name]bool{
		"max_turns": true, "max_tool_executions": true, "max_iterations": true,
	}
)

// errClosed is returned by every Engine method called after Close.
var errClosed = errors.New("engine is closed")

// Engine is one agent running inside the host application. It is safe for
// concurrent use.
type Engine struct {
	agent  *agent.Agent
	server *server.Server
	store  *agent.SessionStore
	tracer observability.Tracer

	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	closed bool
	turns  sync.WaitGroup
}

// NewEngine creates an engine. configJSON is a MobileEngineConfig as proto3
// JSON and model performs the engine's model calls.
func NewEngine(configJSON string, model ModelProvider) (*Engine, error) {
	if model == nil {
		return nil, errors.New("model provider is required")
	}
	cfg := &loomv1.MobileEngineConfig{}
	if err := strictUnmarshal.Unmarshal([]byte(configJSON), cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	dataDir := filepath.Clean(cfg.GetDataDir())
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	// Loom resolves its data directory from the environment and otherwise
	// falls back to ~/.loom, which a sandboxed application cannot write to.
	if os.Getenv(dataDirEnv) == "" {
		if err := os.Setenv(dataDirEnv, dataDir); err != nil {
			return nil, fmt.Errorf("set %s: %w", dataDirEnv, err)
		}
	}

	// The agent attaches to the process-wide tool-result cache when it is
	// constructed. Creating the cache first lets this engine set its bound.
	memoryBytes := cfg.GetToolResultMemoryBytes()
	if memoryBytes == 0 {
		memoryBytes = DefaultToolResultMemoryBytes
	}
	storage.GetGlobalSharedMemory(&storage.Config{
		MaxMemoryBytes:       memoryBytes,
		CompressionThreshold: toolResultCompressionThreshold,
		TTLSeconds:           toolResultTTLSeconds,
	})

	tracer, err := newTracer(cfg, dataDir)
	if err != nil {
		return nil, err
	}
	store, err := agent.NewSessionStore(filepath.Join(dataDir, sessionDBName), tracer)
	if err != nil {
		closeTracer(tracer)
		return nil, fmt.Errorf("open session store: %w", err)
	}

	ag := agent.NewAgent(hostBackend{}, &modelAdapter{host: model},
		agent.WithTracer(tracer),
		agent.WithMemory(agent.NewMemoryWithStore(store)),
		agent.WithConfig(agentConfig(cfg)),
	)

	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{
		agent:  ag,
		server: server.NewServer(ag, store),
		store:  store,
		tracer: tracer,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

// RegisterTool makes a host tool available to the agent.
func (e *Engine) RegisterTool(tool HostTool) error {
	adapter, err := newToolAdapter(tool)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errClosed
	}
	for _, existing := range e.agent.ListTools() {
		if existing == adapter.name {
			return fmt.Errorf("tool %q is already registered", adapter.name)
		}
	}
	e.agent.RegisterTool(adapter)
	return nil
}

// Weave runs one turn and returns when it finishes. requestJSON is a
// WeaveRequest and the return value is a WeaveResponse, both as proto3 JSON.
func (e *Engine) Weave(requestJSON string) (string, error) {
	req := &loomv1.WeaveRequest{}
	if err := strictUnmarshal.Unmarshal([]byte(requestJSON), req); err != nil {
		return "", fmt.Errorf("invalid request: %w", err)
	}
	if err := e.beginTurn(); err != nil {
		return "", err
	}
	defer e.turns.Done()

	resp, err := e.server.Weave(e.ctx, req)
	if err != nil {
		return "", hostError(err)
	}
	return marshalJSON(resp)
}

// StreamWeave runs one turn and reports progress to listener as it happens.
// requestJSON is a WeaveRequest as proto3 JSON. It returns when the turn
// finishes; the answer is carried by the final progress event.
func (e *Engine) StreamWeave(requestJSON string, listener ProgressListener) error {
	if listener == nil {
		return errors.New("progress listener is required")
	}
	req := &loomv1.WeaveRequest{}
	if err := strictUnmarshal.Unmarshal([]byte(requestJSON), req); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	if err := e.beginTurn(); err != nil {
		return err
	}
	defer e.turns.Done()

	return hostError(e.server.StreamWeave(req, &progressStream{ctx: e.ctx, listener: listener}))
}

// Close cancels turns in flight, waits for them to stop and releases the
// session database. A turn blocked inside a host callback stops when that
// callback returns. Close is idempotent.
func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	e.mu.Unlock()

	e.cancel()
	e.turns.Wait()
	err := e.store.Close()
	closeTracer(e.tracer)
	return err
}

// beginTurn registers a turn with the engine unless it has been closed.
// The caller must call e.turns.Done when the turn ends.
func (e *Engine) beginTurn() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errClosed
	}
	e.turns.Add(1)
	return nil
}

func validateConfig(cfg *loomv1.MobileEngineConfig) error {
	if cfg.GetDataDir() == "" {
		return errors.New("data_dir is required")
	}
	if !filepath.IsAbs(cfg.GetDataDir()) {
		return fmt.Errorf("data_dir must be an absolute path, got %q", cfg.GetDataDir())
	}
	if cfg.GetToolResultMemoryBytes() < 0 {
		return errors.New("tool_result_memory_bytes must not be negative")
	}
	ac := cfg.GetAgent()
	if ac == nil {
		return errors.New("agent is required")
	}
	if err := rejectUnsupported("agent", ac, honouredAgentFields); err != nil {
		return err
	}
	if ac.GetName() == "" {
		return errors.New("agent.name is required")
	}
	if strings.TrimSpace(ac.GetSystemPrompt()) == "" {
		return errors.New("agent.system_prompt is required")
	}
	if b := ac.GetBehavior(); b != nil {
		if err := rejectUnsupported("agent.behavior", b, honouredBehaviorFields); err != nil {
			return err
		}
		if b.GetMaxTurns() < 0 || b.GetMaxToolExecutions() < 0 || b.GetMaxIterations() < 0 {
			return errors.New("agent.behavior limits must not be negative")
		}
	}
	return nil
}

// rejectUnsupported returns an error naming every populated field of m that
// is not in honoured.
func rejectUnsupported(path string, m proto.Message, honoured map[protoreflect.Name]bool) error {
	var unsupported []string
	m.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if !honoured[fd.Name()] {
			unsupported = append(unsupported, path+"."+string(fd.Name()))
		}
		return true
	})
	if len(unsupported) == 0 {
		return nil
	}
	sort.Strings(unsupported)
	return fmt.Errorf("not supported by the embedded engine: %s", strings.Join(unsupported, ", "))
}

// agentConfig applies the honoured fields of a validated config on top of
// the agent defaults.
func agentConfig(cfg *loomv1.MobileEngineConfig) *agent.Config {
	ac := cfg.GetAgent()
	out := agent.DefaultConfig()
	out.Name = ac.GetName()
	out.Description = ac.GetDescription()
	out.SystemPrompt = ac.GetSystemPrompt()
	out.Rom = ac.GetRom()
	out.EnableTracing = cfg.GetEnableTracing()
	if b := ac.GetBehavior(); b != nil {
		if b.GetMaxTurns() > 0 {
			out.MaxTurns = int(b.GetMaxTurns())
		}
		if b.GetMaxToolExecutions() > 0 {
			out.MaxToolExecutions = int(b.GetMaxToolExecutions())
		}
		if b.GetMaxIterations() > 0 {
			out.MaxIterations = int(b.GetMaxIterations())
		}
	}
	return out
}

func newTracer(cfg *loomv1.MobileEngineConfig, dataDir string) (observability.Tracer, error) {
	if !cfg.GetEnableTracing() {
		return observability.NewNoOpTracer(), nil
	}
	tc := observability.DefaultEmbeddedConfig()
	tc.StorageType = "sqlite"
	tc.SQLitePath = filepath.Join(dataDir, traceDBName)
	tracer, err := observability.NewEmbeddedTracer(tc)
	if err != nil {
		return nil, fmt.Errorf("open trace store: %w", err)
	}
	return tracer, nil
}

func closeTracer(t observability.Tracer) {
	if c, ok := t.(interface{ Close() error }); ok {
		_ = c.Close()
	}
}

// hostError flattens a gRPC status error from the server layer into a plain
// error whose text carries the status code, since the host receives only
// the message.
func hostError(err error) error {
	if err == nil {
		return nil
	}
	if st, ok := status.FromError(err); ok {
		return fmt.Errorf("%s: %s", st.Code(), st.Message())
	}
	return err
}
