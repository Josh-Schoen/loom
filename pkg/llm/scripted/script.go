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

// Package scripted is an LLM provider that answers from a YAML script instead
// of a model. It lets `looms serve` run end to end — gateway, SSE, sessions,
// tool execution, client tools — with no API key and deterministic replies,
// for client development and integration tests. It is not a model and makes
// no attempt to understand language beyond the script's regular expressions.
package scripted

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"
)

// Script is a parsed, validated script file.
//
// On every provider call the rules are tried in order and the first match
// answers. A rule without after_tool matches only when the newest message is
// from the user; a rule with after_tool matches only when the newest message
// is that tool's result. This keeps a tool-calling rule from firing again on
// its own result.
type Script struct {
	// Rules are tried in order.
	Rules []Rule `yaml:"rules"`

	// DefaultReply answers when no rule matches.
	DefaultReply string `yaml:"default_reply"`

	// Timing shapes the simulated stream.
	Timing Timing `yaml:"timing"`

	defaultReply *template.Template
}

// Rule is one scripted behavior.
type Rule struct {
	// Name identifies the rule in errors.
	Name string `yaml:"name"`

	// When decides whether the rule applies.
	When Match `yaml:"when"`

	// CallTool makes the reply a tool call. Exactly one of CallTool and
	// Reply is set.
	CallTool *ToolCallSpec `yaml:"call_tool"`

	// Reply makes the reply text.
	Reply string `yaml:"reply"`

	userMessage *regexp.Regexp
	reply       *template.Template
}

// Match holds a rule's conditions; all set conditions must hold.
type Match struct {
	// UserMessage is a regular expression matched against the newest user
	// message. Its capture groups are available to templates as .Group n.
	UserMessage string `yaml:"user_message"`

	// ToolOffered requires a tool with this name to be advertised on the call
	// (for example a client tool the request declared).
	ToolOffered string `yaml:"tool_offered"`

	// AfterTool requires the newest message to be this tool's result.
	AfterTool string `yaml:"after_tool"`
}

// ToolCallSpec is a scripted tool call. Arguments may be any YAML value
// (strings, numbers, booleans, lists, maps); every string inside, at any
// depth, is a template. Other scalars are sent unchanged.
type ToolCallSpec struct {
	Name      string         `yaml:"name"`
	Arguments map[string]any `yaml:"arguments"`

	arguments map[string]argNode
}

// argNode is a compiled argument value: a template, a list, a map, or a
// literal scalar.
type argNode struct {
	tmpl    *template.Template
	list    []argNode
	fields  map[string]argNode
	literal any
	kind    argKind
}

type argKind int

const (
	argLiteral argKind = iota
	argTemplate
	argList
	argMap
)

// compileArg parses every string in v as a template.
func compileArg(name string, v any) (argNode, error) {
	switch val := v.(type) {
	case string:
		t, err := parseTemplate(name, val)
		if err != nil {
			return argNode{}, err
		}
		return argNode{kind: argTemplate, tmpl: t}, nil
	case []any:
		node := argNode{kind: argList, list: make([]argNode, 0, len(val))}
		for i, item := range val {
			child, err := compileArg(fmt.Sprintf("%s[%d]", name, i), item)
			if err != nil {
				return argNode{}, err
			}
			node.list = append(node.list, child)
		}
		return node, nil
	case map[string]any:
		node := argNode{kind: argMap, fields: make(map[string]argNode, len(val))}
		for key, item := range val {
			child, err := compileArg(name+"."+key, item)
			if err != nil {
				return argNode{}, err
			}
			node.fields[key] = child
		}
		return node, nil
	case map[any]any:
		converted := make(map[string]any, len(val))
		for key, item := range val {
			converted[fmt.Sprint(key)] = item
		}
		return compileArg(name, converted)
	default:
		return argNode{kind: argLiteral, literal: val}, nil
	}
}

// Timing shapes the simulated token stream.
type Timing struct {
	// FirstTokenMs is the delay before the first chunk (default 300).
	FirstTokenMs int `yaml:"first_token_ms"`
	// ChunkChars is the characters per streamed chunk (default 8).
	ChunkChars int `yaml:"chunk_chars"`
	// ChunkDelayMs is the delay between chunks (default 20).
	ChunkDelayMs int `yaml:"chunk_delay_ms"`
}

const (
	defaultFirstTokenMs = 300
	defaultChunkChars   = 8
	defaultChunkDelayMs = 20
)

// templateFuncs are available to every template.
var templateFuncs = template.FuncMap{
	// dateAt returns the RFC 3339 UTC timestamp days from today at hour:00.
	"dateAt": func(days, hour int) string {
		now := time.Now().UTC()
		day := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
		return day.AddDate(0, 0, days).Format(time.RFC3339)
	},
	// find returns the first capture group (or the whole match, without
	// groups) of pattern in text, or "" when it does not match or pattern
	// is invalid: picking an id out of an earlier tool result.
	"find": func(pattern, text string) string {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return ""
		}
		m := re.FindStringSubmatch(text)
		switch {
		case m == nil:
			return ""
		case len(m) > 1:
			return m[1]
		default:
			return m[0]
		}
	},
	// quote escapes text for use inside a find pattern.
	"quote": regexp.QuoteMeta,
}

// LoadFile reads and validates a script file.
func LoadFile(path string) (*Script, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-configured script path
	if err != nil {
		return nil, fmt.Errorf("scripted: read %s: %w", path, err)
	}
	return Parse(raw)
}

// Parse decodes and validates a script.
func Parse(raw []byte) (*Script, error) {
	var s Script
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("scripted: parse script: %w", err)
	}
	if err := s.compile(); err != nil {
		return nil, err
	}
	return &s, nil
}

func (s *Script) compile() error {
	if len(s.Rules) == 0 && s.DefaultReply == "" {
		return fmt.Errorf("scripted: script needs at least one rule or a default_reply")
	}
	var err error
	if s.defaultReply, err = parseTemplate("default_reply", s.DefaultReply); err != nil {
		return err
	}
	for i := range s.Rules {
		r := &s.Rules[i]
		label := r.Name
		if label == "" {
			label = fmt.Sprintf("rules[%d]", i)
		}
		if (r.CallTool == nil) == (r.Reply == "") {
			return fmt.Errorf("scripted: %s: set exactly one of call_tool and reply", label)
		}
		if r.When.UserMessage != "" {
			if r.userMessage, err = regexp.Compile(r.When.UserMessage); err != nil {
				return fmt.Errorf("scripted: %s: user_message: %w", label, err)
			}
		}
		if r.Reply != "" {
			if r.reply, err = parseTemplate(label+".reply", r.Reply); err != nil {
				return err
			}
			continue
		}
		if r.CallTool.Name == "" {
			return fmt.Errorf("scripted: %s: call_tool.name is required", label)
		}
		r.CallTool.arguments = make(map[string]argNode, len(r.CallTool.Arguments))
		for key, value := range r.CallTool.Arguments {
			if r.CallTool.arguments[key], err = compileArg(label+".arguments."+key, value); err != nil {
				return err
			}
		}
	}
	if s.Timing.FirstTokenMs <= 0 {
		s.Timing.FirstTokenMs = defaultFirstTokenMs
	}
	if s.Timing.ChunkChars <= 0 {
		s.Timing.ChunkChars = defaultChunkChars
	}
	if s.Timing.ChunkDelayMs <= 0 {
		s.Timing.ChunkDelayMs = defaultChunkDelayMs
	}
	return nil
}

func parseTemplate(name, text string) (*template.Template, error) {
	t, err := template.New(name).Funcs(templateFuncs).Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, fmt.Errorf("scripted: template %s: %w", name, err)
	}
	return t, nil
}
