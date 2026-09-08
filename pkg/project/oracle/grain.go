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
	"strings"
	"time"
)

// maxGrainIdentifierPartLen bounds each dot-separated part of a grain
// identifier; warehouse identifiers do not exceed this.
const maxGrainIdentifierPartLen = 128

// GrainCheck compares a result's row count against its distinct count on the
// declared grain. Pure — the caller supplies the counts.
func GrainCheck(declaredGrain string, totalRows, distinctGrainRows int64) VerificationRecord {
	start := time.Now()

	grain := strings.TrimSpace(declaredGrain)
	if grain == "" {
		return newRecord(RungGrain, VerdictSkip, "grain check skipped: no declared grain", start)
	}
	if totalRows < 0 || distinctGrainRows < 0 {
		return newRecord(RungGrain, VerdictSkip,
			fmt.Sprintf("grain %s check skipped: row counts unavailable (%d rows, %d distinct)",
				grain, totalRows, distinctGrainRows), start)
	}
	if distinctGrainRows > totalRows {
		return newRecord(RungGrain, VerdictSkip,
			fmt.Sprintf("grain %s check skipped: %d distinct exceeds %d rows — counts disagree",
				grain, distinctGrainRows, totalRows), start)
	}
	if distinctGrainRows == totalRows {
		return newRecord(RungGrain, VerdictPass,
			fmt.Sprintf("grain %s holds: %d rows, %d distinct", grain, totalRows, distinctGrainRows), start)
	}
	return newRecord(RungGrain, VerdictFail,
		fmt.Sprintf("grain %s violated: %d rows, %d distinct — %d duplicates (silent fan-out?)",
			grain, totalRows, distinctGrainRows, totalRows-distinctGrainRows), start)
}

// GrainCountSQL builds the count query for a grain check. Returns "" when the
// grain is not a bare (optionally qualified) identifier or the inner SQL is
// empty — this string reaches a warehouse, so nothing else may pass.
func GrainCountSQL(declaredGrain, innerSQL string) string {
	grain := strings.TrimSpace(declaredGrain)
	inner := strings.TrimSpace(innerSQL)
	if inner == "" || !validGrainIdentifier(grain) {
		return ""
	}
	inner = strings.TrimRight(inner, "; \t\r\n")
	if inner == "" {
		return ""
	}
	return fmt.Sprintf(
		"SELECT COUNT(*) AS total_rows, COUNT(DISTINCT %s) AS distinct_rows FROM (%s) loom_grain_check",
		grain, inner)
}

// validGrainIdentifier accepts letters, digits and underscores per part, with
// dots separating parts of a qualified name. Every part must start with a
// letter or underscore. Everything else — whitespace, quotes, comments,
// operators, statement separators — is refused.
func validGrainIdentifier(grain string) bool {
	if grain == "" {
		return false
	}
	parts := strings.Split(grain, ".")
	for _, part := range parts {
		if part == "" || len(part) > maxGrainIdentifierPartLen {
			return false
		}
		for i := 0; i < len(part); i++ {
			c := part[i]
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
			case c >= '0' && c <= '9':
				if i == 0 {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}
