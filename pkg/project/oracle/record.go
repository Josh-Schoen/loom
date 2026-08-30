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

// Package oracle implements the verification ladder: machine-checked records
// attached to tool results. Checks are advisory only — a check never fails,
// delays, or alters the result it describes.
package oracle

import "time"

// Rung identifiers.
const (
	RungExplainPrediction = "explain_prediction"
	RungGrain             = "grain"
)

// Verdicts. A check that could not run yields VerdictSkip, never an error.
const (
	VerdictPass = "pass"
	VerdictWarn = "warn"
	VerdictFail = "fail"
	VerdictSkip = "skip"
)

// MetadataKey is the shuttle.Result.Metadata key holding []VerificationRecord.
const MetadataKey = "verification"

// VerificationRecord is one check's outcome. JSON tags are the desktop's
// wire shape; proto lands when records persist.
type VerificationRecord struct {
	Rung     string `json:"rung"`
	Verdict  string `json:"verdict"`
	Evidence string `json:"evidence"`
	CostMs   int64  `json:"costMs"`
	At       string `json:"at"`
}

// newRecord stamps At in RFC3339 and CostMs from the check's own elapsed time.
func newRecord(rung, verdict, evidence string, start time.Time) VerificationRecord {
	return VerificationRecord{
		Rung:     rung,
		Verdict:  verdict,
		Evidence: evidence,
		CostMs:   time.Since(start).Milliseconds(),
		At:       time.Now().UTC().Format(time.RFC3339),
	}
}
