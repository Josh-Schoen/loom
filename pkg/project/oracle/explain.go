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

package oracle

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Prediction is what the optimizer claimed before the query ran.
type Prediction struct {
	EstimatedRows int64
	Confidence    string
	Found         bool
}

// Teradata EXPLAIN text is the only dialect this parser handles.
var (
	teradataEstimateRe   = regexp.MustCompile(`(?i)estimat`)
	teradataRowCountRe   = regexp.MustCompile(`(?i)([0-9][0-9,]*)\s+rows?\b`)
	teradataConfidenceRe = regexp.MustCompile(`(?i)(high|low|no|index join)\s+confidence`)
)

// ParseTeradataExplain extracts the last row estimate from Teradata EXPLAIN
// text — the result spool — plus the confidence stated alongside it.
func ParseTeradataExplain(planText string) Prediction {
	var (
		p              Prediction
		lastConfidence string
	)
	for _, segment := range splitExplainSegments(planText) {
		if !teradataEstimateRe.MatchString(segment) {
			continue
		}
		if c := teradataConfidenceRe.FindStringSubmatch(segment); c != nil {
			lastConfidence = strings.ToLower(c[1]) + " confidence"
		}
		matches := teradataRowCountRe.FindAllStringSubmatch(segment, -1)
		if len(matches) == 0 {
			continue
		}
		raw := strings.ReplaceAll(matches[len(matches)-1][1], ",", "")
		rows, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			continue
		}
		p = Prediction{EstimatedRows: rows, Confidence: lastConfidence, Found: true}
	}
	return p
}

// splitExplainSegments breaks plan text into sentence-sized units so a row
// estimate and its confidence phrase stay together.
func splitExplainSegments(planText string) []string {
	return strings.FieldsFunc(planText, func(r rune) bool {
		return r == '.' || r == '\n' || r == '\r'
	})
}

// PredictionCheck compares the optimizer's estimate against the rows actually
// returned. Verdicts are order-of-magnitude: estimates are estimates.
func PredictionCheck(p Prediction, actualRows int64) VerificationRecord {
	start := time.Now()

	if !p.Found {
		return newRecord(RungExplainPrediction, VerdictSkip,
			"explain prediction skipped: no row estimate found in plan text", start)
	}
	if actualRows < 0 {
		return newRecord(RungExplainPrediction, VerdictSkip,
			fmt.Sprintf("explain predicted %d rows (%s); actual row count unavailable from result",
				p.EstimatedRows, confidenceText(p)), start)
	}

	verdict, comparison := predictionVerdict(p.EstimatedRows, actualRows)
	return newRecord(RungExplainPrediction, verdict,
		fmt.Sprintf("explain predicted %d rows (%s); actual %d rows — %s",
			p.EstimatedRows, confidenceText(p), actualRows, comparison), start)
}

// predictionVerdict returns the verdict and the human comparison phrase.
func predictionVerdict(estimated, actual int64) (string, string) {
	switch {
	case estimated >= 1000 && actual == 0:
		return VerdictFail, "predicted a large result, got none"
	case estimated <= 1 && actual >= 10000:
		return VerdictFail, "predicted at most one row, got a large result"
	}

	if estimated == actual {
		return VerdictPass, "exact match"
	}

	hi, lo := estimated, actual
	if hi < lo {
		hi, lo = lo, hi
	}
	if lo <= 0 {
		return VerdictFail, fmt.Sprintf("off by more than %dx", hi)
	}

	ratio := float64(hi) / float64(lo)
	switch {
	case ratio <= 10:
		return VerdictPass, fmt.Sprintf("within %.1fx", ratio)
	case ratio <= 1000:
		return VerdictWarn, fmt.Sprintf("off by %.0fx", ratio)
	default:
		return VerdictFail, fmt.Sprintf("off by %.0fx", ratio)
	}
}

func confidenceText(p Prediction) string {
	if p.Confidence == "" {
		return "confidence unstated"
	}
	return p.Confidence
}
