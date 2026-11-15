// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package anthropic

import (
	"testing"

	"github.com/stretchr/testify/assert"

	llmtypes "github.com/teradata-labs/loom/pkg/llm/types"
)

// recordToolInput records tool-input progress reports, checking that each
// call's deltas join to its Bytes, and keeps them without the delta so the
// identity and size checks read as before.
func recordToolInput(t *testing.T, got *[]llmtypes.ToolInputProgress) func(llmtypes.ToolInputProgress) {
	joined := map[int]string{}
	return func(p llmtypes.ToolInputProgress) {
		joined[p.Index] += p.Delta
		assert.Equal(t, p.Bytes, len(joined[p.Index]), "a call's deltas join to its argument JSON, Bytes long")
		p.Delta = ""
		*got = append(*got, p)
	}
}
