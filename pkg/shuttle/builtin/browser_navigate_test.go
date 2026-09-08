// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package builtin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrowserNavigateTool_Name(t *testing.T) {
	tool := NewBrowserNavigateTool()
	assert.Equal(t, "browser_navigate", tool.Name())
}

func TestBrowserNavigateTool_Schema(t *testing.T) {
	tool := NewBrowserNavigateTool()
	schema := tool.InputSchema()
	require.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "url")
	assert.Contains(t, schema.Properties, "extract_links")
	assert.Equal(t, []string{"url"}, schema.Required)
}

func TestBrowserNavigateTool_MissingURL(t *testing.T) {
	tool := NewBrowserNavigateTool()
	result, err := tool.Execute(context.Background(), map[string]interface{}{})
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Equal(t, "INVALID_PARAMS", result.Error.Code)
}

func TestBrowserNavigateTool_InvalidURL(t *testing.T) {
	tool := NewBrowserNavigateTool()
	// A URL with spaces cannot be parsed and must return INVALID_URL, not FETCH_FAILED.
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": "not a valid url",
	})
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Equal(t, "INVALID_URL", result.Error.Code)
}

func TestBrowserNavigateTool_FetchHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>Test Page</title></head>
<body>
  <h1>Hello World</h1>
  <p>This is a test paragraph.</p>
  <a href="https://example.com">Example Link</a>
</body>
</html>`))
	}))
	defer srv.Close()

	tool := NewBrowserNavigateTool()
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": srv.URL,
	})
	require.NoError(t, err)
	require.True(t, result.Success, "expected success, got error: %v", result.Error)

	data, ok := result.Data.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "Test Page", data["title"])
	assert.Contains(t, data["content"], "Hello World")
	assert.Contains(t, data["content"], "test paragraph")
	assert.Equal(t, 200, data["status_code"])
	// Links not extracted by default
	assert.Nil(t, data["links"])
}

func TestBrowserNavigateTool_ExtractLinks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body>
<a href="https://example.com">One</a>
<a href="https://other.org/page">Two</a>
<a href="/relative">Relative (skipped)</a>
</body></html>`))
	}))
	defer srv.Close()

	tool := NewBrowserNavigateTool()
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"url":           srv.URL,
		"extract_links": true,
	})
	require.NoError(t, err)
	require.True(t, result.Success)

	data := result.Data.(map[string]interface{})
	links, ok := data["links"].([]string)
	require.True(t, ok, "expected links slice")
	assert.Contains(t, links, "https://example.com")
	assert.Contains(t, links, "https://other.org/page")
	// Relative links are excluded (no http prefix)
	for _, l := range links {
		assert.True(t, strings.HasPrefix(l, "http"), "expected absolute link, got: %s", l)
	}
}

func TestBrowserNavigateTool_PlainTextResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("plain text content"))
	}))
	defer srv.Close()

	tool := NewBrowserNavigateTool()
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": srv.URL,
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	data := result.Data.(map[string]interface{})
	assert.Equal(t, "plain text content", data["content"])
}

func TestBrowserNavigateTool_AutoScheme(t *testing.T) {
	// A URL without scheme should get https:// prepended — this will fail to
	// connect since it's not a real host, but we just verify no INVALID_URL error.
	tool := NewBrowserNavigateTool()
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": "unreachable.invalid/page",
	})
	require.NoError(t, err)
	// Should fail with FETCH_FAILED (network), not INVALID_URL (parsing)
	assert.NotEqual(t, "INVALID_URL", result.Error.Code)
}

func TestBrowserExtractHTML_Basic(t *testing.T) {
	html := `<html><head><title>My Title</title></head>
<body><h1>Heading</h1><p>Paragraph text.</p></body></html>`

	title, text, links := browserExtractHTML(html, false)
	assert.Equal(t, "My Title", title)
	assert.Contains(t, text, "Heading")
	assert.Contains(t, text, "Paragraph text.")
	assert.Empty(t, links)
}

func TestBrowserExtractHTML_SkipsScriptAndStyle(t *testing.T) {
	html := `<html><body>
<script>var x = 1;</script>
<style>.foo { color: red; }</style>
<p>Visible</p>
</body></html>`

	_, text, _ := browserExtractHTML(html, false)
	assert.NotContains(t, text, "var x")
	assert.NotContains(t, text, ".foo")
	assert.Contains(t, text, "Visible")
}

func TestBrowserExtractHTML_DeduplicatesLinks(t *testing.T) {
	html := `<html><body>
<a href="https://a.com">A</a>
<a href="https://a.com">A again</a>
<a href="https://b.com">B</a>
</body></html>`

	_, _, links := browserExtractHTML(html, true)
	assert.Len(t, links, 2)
}

func TestBrowserNavigateTool_InRegistry(t *testing.T) {
	tool := ByName("browser_navigate")
	require.NotNil(t, tool)
	assert.Equal(t, "browser_navigate", tool.Name())

	names := Names()
	assert.Contains(t, names, "browser_navigate")
}
