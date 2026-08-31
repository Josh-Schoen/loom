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

package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// selectorDoc is a three-level chain plus an unrelated branch: rerunning
// `mid` must reach `top` and nothing else.
const selectorDoc = `apiVersion: loom/v1
kind: Project
metadata:
  name: chain
cells:
  - id: base
    lang: sql
    source: |
      SELECT 1 AS n
  - id: mid
    lang: sql
    inputs: [base]
    source: |
      SELECT n FROM {{ ref('base') }}
  - id: top
    lang: sql
    inputs: [mid]
    source: |
      SELECT n FROM {{ ref('mid') }}
  - id: sibling
    lang: sql
    inputs: [base]
    source: |
      SELECT n FROM {{ ref('base') }}
  - id: notes
    lang: prose
    source: |
      Not a model.
`

func TestSelectDownstream(t *testing.T) {
	doc, err := Parse([]byte(selectorDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	sel, err := doc.SelectDownstream("mid")
	if err != nil {
		t.Fatalf("SelectDownstream: %v", err)
	}
	if sel.Selector != "mid+" {
		t.Errorf("Selector = %q, want %q — dbt's downstream operator", sel.Selector, "mid+")
	}
	if got := strings.Join(sel.Cells, ","); got != "mid,top" {
		t.Errorf("Cells = %q, want %q (dependency order, sibling and base excluded)", got, "mid,top")
	}

	// The base reaches everything that consumes it, transitively.
	sel, err = doc.SelectDownstream("base")
	if err != nil {
		t.Fatalf("SelectDownstream(base): %v", err)
	}
	if got := strings.Join(sel.Cells, ","); got != "base,mid,sibling,top" {
		t.Errorf("Cells = %q, want the whole cone", got)
	}

	if _, err := doc.SelectDownstream("nope"); err == nil {
		t.Error("an unknown cell id was selected")
	}
}

// selectorRunResults is a dbt build artifact for a selected run: only the
// selected nodes appear, which is what dbt writes.
const selectorRunResults = `{
  "metadata": {"generated_at": "2026-08-30T12:00:00.000000Z"},
  "results": [
    {"unique_id": "model.chain.mid", "status": "success", "execution_time": 0.5,
     "adapter_response": {"rows_affected": 1}},
    {"unique_id": "model.chain.top", "status": "success", "execution_time": 0.5,
     "adapter_response": {"rows_affected": 1}}
  ]
}
`

// argvCapturingDBT installs a dbt stand-in that appends every invocation's
// arguments to a log file, answers `show` with a preview payload, and copies a
// canned run_results.json for anything else. Returns the binary and the log.
func argvCapturingDBT(t *testing.T) (bin, argvLog string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the dbt stand-in is a POSIX shell script")
	}
	dir := t.TempDir()
	argvLog = filepath.Join(dir, "argv.log")
	fixture := filepath.Join(dir, "run_results.fixture.json")
	if err := os.WriteFile(fixture, []byte(selectorRunResults), 0o600); err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	fmt.Fprintf(&b, "echo \"$*\" >> %q\n", argvLog)
	b.WriteString("if [ \"$1\" = \"show\" ]; then\n")
	b.WriteString("  echo \"12:00:06  Previewing node '$3':\"\n")
	b.WriteString("  echo \"12:00:06  {\"\n")
	b.WriteString("  echo \"  \\\"$3\\\": [\"\n")
	b.WriteString("  echo '    {\"n\": 1}'\n")
	b.WriteString("  echo \"  ]\"\n")
	b.WriteString("  echo \"}\"\n")
	b.WriteString("  exit 0\n")
	b.WriteString("fi\n")
	b.WriteString("mkdir -p target\n")
	fmt.Fprintf(&b, "cp %q target/run_results.json\n", fixture)
	b.WriteString("exit 0\n")

	bin = filepath.Join(dir, "dbt")
	if err := os.WriteFile(bin, []byte(b.String()), 0o700); err != nil { // #nosec G302 -- must be executable
		t.Fatal(err)
	}
	return bin, argvLog
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path) // #nosec G304 -- test-owned temp path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// A selection reaches dbt as `--select <selector>` on the BUILD, and scopes
// the preview pass to the selected cells: refreshing a preview for a model dbt
// was not asked to rebuild would show a stale sample as freshly produced.
func TestRunPassesSelectorAndScopesPreviews(t *testing.T) {
	doc, err := Parse([]byte(selectorDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	buildDir := filepath.Join(t.TempDir(), "build")
	if _, err := Compile(doc, buildDir); err != nil {
		t.Fatalf("compile: %v", err)
	}
	bin, argvLog := argvCapturingDBT(t)

	sel, err := doc.SelectDownstream("mid")
	if err != nil {
		t.Fatalf("SelectDownstream: %v", err)
	}
	outcome := Run(context.Background(), doc, buildDir, RunOptions{
		DBTBin:      bin,
		ProfilesDir: filepath.Join(t.TempDir(), "profiles"),
		Selection:   &sel,
	})
	if outcome.Err != nil {
		t.Fatalf("Run: %v (output %q)", outcome.Err, outcome.Output)
	}

	lines := readLines(t, argvLog)
	var build string
	var shown []string
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "build "):
			build = l
		case strings.HasPrefix(l, "show "):
			fields := strings.Fields(l)
			// show --select <cell> ...
			if len(fields) >= 3 {
				shown = append(shown, fields[2])
			}
		}
	}
	if build == "" {
		t.Fatalf("no build invocation in %v", lines)
	}
	if !strings.Contains(build, "--select mid+") {
		t.Errorf("build args = %q, want them to carry --select mid+", build)
	}
	if got := strings.Join(shown, ","); got != "mid,top" {
		t.Errorf("previewed %q, want %q — only the selected models", got, "mid,top")
	}
	if outcome.Previews != 2 {
		t.Errorf("Previews = %d, want 2", outcome.Previews)
	}
	if len(outcome.Records) != 2 {
		t.Errorf("Records covers %d cells, want 2 (the artifact only names the selected nodes)", len(outcome.Records))
	}
	for _, id := range []string{"mid", "top"} {
		if _, err := os.Stat(filepath.Join(buildDir, "target", PreviewsSubdir, id+".json")); err != nil {
			t.Errorf("no preview for %s: %v", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(buildDir, "target", PreviewsSubdir, "sibling.json")); err == nil {
		t.Error("an unselected cell's preview was refreshed")
	}
}

// No selection builds the whole project and previews every model cell — the
// run_project tool's behaviour, unchanged by the extraction.
func TestRunWithoutSelectionBuildsEverything(t *testing.T) {
	doc, err := Parse([]byte(selectorDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	buildDir := filepath.Join(t.TempDir(), "build")
	if _, err := Compile(doc, buildDir); err != nil {
		t.Fatalf("compile: %v", err)
	}
	bin, argvLog := argvCapturingDBT(t)

	outcome := Run(context.Background(), doc, buildDir, RunOptions{
		DBTBin:      bin,
		ProfilesDir: filepath.Join(t.TempDir(), "profiles"),
	})
	if outcome.Err != nil {
		t.Fatalf("Run: %v (output %q)", outcome.Err, outcome.Output)
	}
	for _, l := range readLines(t, argvLog) {
		if strings.HasPrefix(l, "build ") && strings.Contains(l, "--select") {
			t.Errorf("build args = %q, want no --select without a selection", l)
		}
	}
	// Four sql cells emit models; the prose cell does not.
	if outcome.Previews != 4 {
		t.Errorf("Previews = %d, want 4", outcome.Previews)
	}
}

// A dbt that never wrote an artifact is the one failure Run reports: nothing
// was verified, so the caller has to say so rather than show empty verdicts.
func TestRunReportsMissingArtifact(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the dbt stand-in is a POSIX shell script")
	}
	doc, err := Parse([]byte(selectorDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	buildDir := filepath.Join(t.TempDir(), "build")
	if _, err := Compile(doc, buildDir); err != nil {
		t.Fatalf("compile: %v", err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "dbt")
	script := "#!/bin/sh\necho \"Encountered an error: invalid credentials\" >&2\nexit 2\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil { // #nosec G302 -- must be executable
		t.Fatal(err)
	}

	outcome := Run(context.Background(), doc, buildDir, RunOptions{
		DBTBin:      bin,
		ProfilesDir: filepath.Join(dir, "profiles"),
	})
	if outcome.Err == nil {
		t.Fatal("a run with no artifact reported success")
	}
	if !strings.Contains(outcome.Err.Error(), "no run_results.json") {
		t.Errorf("Err = %q, want it to name the missing artifact", outcome.Err)
	}
	if outcome.ExitCode != 2 {
		t.Errorf("ExitCode = %d, want 2", outcome.ExitCode)
	}
	if !strings.Contains(outcome.Output, "invalid credentials") {
		t.Errorf("Output = %q, want dbt's own diagnosis", outcome.Output)
	}
}

// BuildDir is the ONE derivation of a document's build tree; the desktop and
// the tool both call it, and a byte of drift makes runs invisible. The literal
// digest is pinned for the same reason cmd/loom-desktop pins it.
func TestBuildDirIsStable(t *testing.T) {
	got := BuildDir("/data", "/tmp/loom-desktop-test/project.yaml")
	want := filepath.Join("/data", BuildCacheDir, "f755927cca804b99", BuildSubdir)
	if got != want {
		t.Errorf("BuildDir = %q, want %q", got, want)
	}
	// Clean-then-Abs, not symlink-resolved: an uncleaned path hashes the same.
	if other := BuildDir("/data", "/tmp/loom-desktop-test/./project.yaml"); other != want {
		t.Errorf("BuildDir(uncleaned) = %q, want %q", other, want)
	}
}
