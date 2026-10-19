// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAcceptsSamplingParams(t *testing.T) {
	for model, want := range map[string]bool{
		"claude-sonnet-4-5-20250929":          true,
		"claude-opus-4-6":                     true,
		"claude-haiku-4-5":                    true,
		"us.anthropic.claude-sonnet-4-5-v1:0": true,
		"gpt-4o":                              true,
		"claude-opus-4-7":                     false,
		"claude-opus-4-8":                     false,
		"claude-opus-5":                       false,
		"claude-opus-5-5":                     false,
		"claude-fable-5-1":                    false,
		"claude-mythos-5-1":                   false,
		"claude-sonnet-5":                     false,
		"claude-sonnet-5-5":                   false,
		"claude-haiku-5-5":                    false,
		"us.anthropic.claude-opus-5-v1:0":     false,
		"global.anthropic.claude-sonnet-5":    false,
		"coding-agent/claude-fable-5-1":       false,
		"Claude-Opus-5-5":                     false,
	} {
		assert.Equal(t, want, AcceptsSamplingParams(model), model)
	}
}
