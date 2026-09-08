// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package builtin

// run_project's 'preview' action (backlog 41, the scale half): the agent
// rides the SAME fast lane the desktop's Rerun does — pkg/project/fastlane,
// one session per connection held for the daemon's life — so "check this
// cell" costs the agent seconds instead of a dbt build. Unverified by
// definition; 'run' remains the verdict lane.
//
// The preview is also written into the project's build tree (the same file
// the verified lane writes), so the desktop notebook shows the agent's fast
// runs when its turn ends — one preview pipeline, whoever drives.

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/teradata-labs/loom/pkg/config"
	"github.com/teradata-labs/loom/pkg/project"
	"github.com/teradata-labs/loom/pkg/project/fastlane"
	"github.com/teradata-labs/loom/pkg/shuttle"
)

// previewDataRows caps the rows returned to the AGENT. Its context is a
// budget; the full standard sample (fastlane.DefaultPreviewRows) still lands
// in the preview file the desktop reads.
const previewDataRows = 20

var (
	previewMu   sync.Mutex
	previewSess *fastlane.Session
	previewID   string
)

// runProjectPreview runs one cell on the fast lane and shapes the result for
// the agent.
func runProjectPreview(ctx context.Context, start time.Time, doc *project.Document, docPath, cellID string) *shuttle.Result {
	if cellID == "" {
		return runProjectFailure(start, "INVALID_PARAMS", "action 'preview' needs a 'cell'",
			"Name the cell to run, e.g. cell: 'customer_totals'.")
	}

	profilesDir := project.ProfilesDir(config.GetLoomDataDir())
	conn, err := fastlane.ResolveConnection(profilesDir, project.DefaultProfileName)
	if err != nil {
		return runProjectFailure(start, "NO_CONNECTION", err.Error(),
			"The fast lane reads the dbt profile's active target; fix profiles.yml or its environment variables.")
	}

	previewMu.Lock()
	defer previewMu.Unlock()

	if previewSess == nil || previewID != conn.ID {
		if previewSess != nil {
			previewSess.Close()
			previewSess = nil
		}
		sess, err := fastlane.Open(ctx, conn, &fastlane.Options{
			VenvPython: fastlane.VenvPython(config.GetLoomDataDir()),
		})
		if err != nil {
			return runProjectFailure(start, "NO_CONNECTION", err.Error(),
				"For engines without a direct driver, use action 'run' (the dbt lane) instead.")
		}
		previewSess = sess
		previewID = conn.ID
	}

	// docPath scopes this project's state inside the shared session — the
	// daemon serves every project, and same-named cells must not collide.
	res, err := previewSess.RunCell(ctx, docPath, doc, cellID, fastlane.DefaultPreviewRows)
	if err != nil {
		// The warehouse's (or python's) refusal is the answer. If the session
		// died underneath, drop it so the next preview reconnects.
		if pingErr := previewSess.Ping(ctx); pingErr != nil {
			previewSess.Close()
			previewSess = nil
			log.Printf("run_project: fast-lane session lost (%v); next preview reconnects", pingErr)
		}
		return runProjectFailure(start, "PREVIEW_FAILED", err.Error(),
			"The message is the engine's own diagnosis; fix the named cell and preview again.")
	}

	// The desktop reads previews out of the build tree; give it this one.
	if err := fastlane.WritePreview(projectBuildDir(docPath), cellID, res.Preview); err != nil {
		log.Printf("run_project: preview for %s ran but did not persist: %v", cellID, err)
	}

	rows := res.Preview.Rows
	truncatedForAgent := false
	if len(rows) > previewDataRows {
		rows = rows[:previewDataRows]
		truncatedForAgent = true
	}
	data := map[string]interface{}{
		"action":      runProjectActionPreview,
		"cell":        cellID,
		"columns":     res.Preview.Columns,
		"rows":        rows,
		"row_note":    fmt.Sprintf("up to %d of the sample shown here; the notebook shows %d", previewDataRows, fastlane.DefaultPreviewRows),
		"truncated":   res.Preview.Truncated || truncatedForAgent,
		"stdout":      res.Preview.Stdout,
		"note":        res.Preview.Note,
		"rebuilt":     res.Rebuilt,
		"duration_ms": res.Duration.Milliseconds(),
		"verified":    false,
	}
	return &shuttle.Result{
		Success:         true,
		Data:            data,
		ExecutionTimeMs: time.Since(start).Milliseconds(),
	}
}
