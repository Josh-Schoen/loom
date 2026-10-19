// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package llm

import "strings"

// noSamplingMarkers name the Claude model families that reject sampling
// parameters (temperature, top_p, top_k). Claude Opus 4.7 and later Opus,
// Fable and Mythos reject any value with a 400; Sonnet 5.x and Haiku 5.x
// accept only the default value, so a configured temperature fails there
// too. Clients omit the field for all of them, which takes the API default.
// Matching is on a substring of the lowered model id, like
// adaptiveThinkingMarkers, so it holds for bare names, dated suffixes,
// Bedrock profile ids and gateway aliases alike.
var noSamplingMarkers = []string{
	"opus-4-7", "opus-4-8", "opus-5", "fable-", "mythos-", "sonnet-5", "haiku-5",
}

// AcceptsSamplingParams reports whether model accepts a temperature. When it
// does not, a client must leave temperature out of the request.
func AcceptsSamplingParams(model string) bool {
	m := strings.ToLower(model)
	for _, marker := range noSamplingMarkers {
		if strings.Contains(m, marker) {
			return false
		}
	}
	return true
}
