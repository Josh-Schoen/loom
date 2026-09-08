// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	"github.com/teradata-labs/loom/pkg/session"
	"github.com/teradata-labs/loom/pkg/shuttle"
)

const (
	// browserNavigateMaxBody caps the response body read at 2 MB.
	browserNavigateMaxBody = 2 << 20
	// browserNavigateMaxContent caps the text returned to the LLM.
	browserNavigateMaxContent = 32_000
	// browserNavigateTimeout is the per-request deadline.
	browserNavigateTimeout = 20 * time.Second
	// browserNavigateMaxRedirects limits redirect chains.
	browserNavigateMaxRedirects = 5
)

// BrowserNavigateTool fetches a URL, converts the HTML to readable text, and
// returns the page title and content. It handles charset detection and
// redirect following automatically.
//
// Agents declare it in their YAML as:
//
//	tools:
//	  builtin:
//	    - browser_navigate
type BrowserNavigateTool struct {
	client *http.Client
}

// NewBrowserNavigateTool creates a BrowserNavigateTool with sensible defaults.
func NewBrowserNavigateTool() *BrowserNavigateTool {
	return &BrowserNavigateTool{
		client: &http.Client{
			Timeout: browserNavigateTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= browserNavigateMaxRedirects {
					return http.ErrUseLastResponse
				}
				return nil
			},
			Transport: &http.Transport{
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: 5,
				IdleConnTimeout:     60 * time.Second,
			},
		},
	}
}

func (t *BrowserNavigateTool) Name() string { return "browser_navigate" }

// Description returns a fallback description used when the prompt registry is
// unavailable. The canonical description lives in prompts/tools/browser_navigate.yaml.
func (t *BrowserNavigateTool) Description() string {
	return `Navigate to a URL and read the page content as plain text.
Fetches the page, extracts visible text (stripping scripts, styles, and HTML tags),
and returns the page title plus readable content. Handles redirects and charset
detection automatically. Use this to read documentation, articles, or any web page.`
}

func (t *BrowserNavigateTool) InputSchema() *shuttle.JSONSchema {
	return shuttle.NewObjectSchema(
		"Parameters for browser navigation",
		map[string]*shuttle.JSONSchema{
			"url": shuttle.NewStringSchema(
				"The URL to navigate to (required). https:// is assumed when no scheme is provided.",
			).WithFormat("uri"),
			"extract_links": shuttle.NewBooleanSchema(
				"When true, include hyperlinks found on the page in the output (default: false).",
			).WithDefault(false),
		},
		[]string{"url"},
	)
}

func (t *BrowserNavigateTool) Execute(ctx context.Context, params map[string]interface{}) (*shuttle.Result, error) {
	start := time.Now()

	rawURL, ok := params["url"].(string)
	if !ok || strings.TrimSpace(rawURL) == "" {
		return &shuttle.Result{
			Success: false,
			Error: &shuttle.Error{
				Code:       "INVALID_PARAMS",
				Message:    "url is required",
				Suggestion: "Provide a valid URL, e.g. \"https://docs.example.com/guide\"",
			},
			ExecutionTimeMs: time.Since(start).Milliseconds(),
		}, nil
	}

	rawURL = strings.TrimSpace(rawURL)
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}

	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return &shuttle.Result{
			Success: false,
			Error: &shuttle.Error{
				Code:       "INVALID_URL",
				Message:    fmt.Sprintf("invalid URL: %v", err),
				Suggestion: "Check the URL format. Example: https://example.com/page",
			},
			ExecutionTimeMs: time.Since(start).Milliseconds(),
		}, nil
	}

	extractLinks := false
	if v, ok := params["extract_links"].(bool); ok {
		extractLinks = v
	}

	// When the loom-ide gateway is reachable, ask the extension to navigate
	// the real browser tab and return the fully-rendered page content.
	// This handles JavaScript-heavy SPAs that plain HTTP fetch can't read.
	// Falls back to HTTP fetch automatically if the gateway isn't running or
	// the extension doesn't respond in time.
	if extPage, ok := t.fetchViaExtension(ctx, rawURL); ok {
		content := extPage.Content
		if len(content) > browserNavigateMaxContent {
			content = content[:browserNavigateMaxContent] + "\n…(content truncated)"
		}
		return &shuttle.Result{
			Success: true,
			Data: map[string]interface{}{
				"url":         extPage.URL,
				"title":       extPage.Title,
				"content":     content,
				"status_code": 200,
				"source":      "browser_extension",
			},
			Metadata: map[string]interface{}{
				"url":    extPage.URL,
				"title":  extPage.Title,
				"source": "browser_extension",
			},
			ExecutionTimeMs: time.Since(start).Milliseconds(),
		}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return &shuttle.Result{
			Success: false,
			Error: &shuttle.Error{
				Code:      "REQUEST_BUILD_FAILED",
				Message:   fmt.Sprintf("failed to build request: %v", err),
				Retryable: false,
			},
			ExecutionTimeMs: time.Since(start).Milliseconds(),
		}, nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; LoomAgent/1.0)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := t.client.Do(req)
	if err != nil {
		return &shuttle.Result{
			Success: false,
			Error: &shuttle.Error{
				Code:       "FETCH_FAILED",
				Message:    fmt.Sprintf("failed to fetch URL: %v", err),
				Retryable:  true,
				Suggestion: "Check network connectivity and that the URL is reachable.",
			},
			ExecutionTimeMs: time.Since(start).Milliseconds(),
		}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	// Charset-aware reader so non-UTF8 pages decode correctly.
	reader, err := charset.NewReader(resp.Body, resp.Header.Get("Content-Type"))
	if err != nil {
		reader = resp.Body
	}

	body, err := io.ReadAll(io.LimitReader(reader, browserNavigateMaxBody))
	if err != nil {
		return &shuttle.Result{
			Success: false,
			Error: &shuttle.Error{
				Code:      "READ_FAILED",
				Message:   fmt.Sprintf("failed to read response body: %v", err),
				Retryable: true,
			},
			ExecutionTimeMs: time.Since(start).Milliseconds(),
		}, nil
	}

	ct := resp.Header.Get("Content-Type")
	var title, content string
	var links []string
	if strings.Contains(ct, "text/html") || ct == "" {
		title, content, links = browserExtractHTML(string(body), extractLinks)
	} else {
		// Non-HTML: return raw text, capped.
		content = string(body)
		title = parsed.Host
	}

	if len(content) > browserNavigateMaxContent {
		content = content[:browserNavigateMaxContent] + "\n…(content truncated)"
	}

	data := map[string]interface{}{
		"url":         resp.Request.URL.String(), // final URL after any redirects
		"title":       title,
		"content":     content,
		"status_code": resp.StatusCode,
	}
	if extractLinks && len(links) > 0 {
		data["links"] = links
	}

	return &shuttle.Result{
		Success: true,
		Data:    data,
		Metadata: map[string]interface{}{
			"url":         resp.Request.URL.String(),
			"status_code": resp.StatusCode,
			"title":       title,
		},
		ExecutionTimeMs: time.Since(start).Milliseconds(),
	}, nil
}

func (t *BrowserNavigateTool) Backend() string { return "" }

// extensionPage holds page content delivered by the Chrome extension.
type extensionPage struct {
	URL     string
	Title   string
	Content string
}

// fetchViaExtension asks the loom-ide gateway to deliver page content
// extracted by the Chrome extension's content script. This gives the agent
// fully-rendered HTML (post-JavaScript) rather than the raw HTTP response,
// which is critical for SPA sites like visiticeland.com.
//
// Returns (page, true) on success, (zero, false) on any error so callers can
// fall back to plain HTTP fetch.
func (t *BrowserNavigateTool) fetchViaExtension(ctx context.Context, rawURL string) (extensionPage, bool) {
	// Honour an explicit override; fall back to the loom-ide default port.
	gatewayBase := os.Getenv("LOOM_IDE_GATEWAY_URL")
	if gatewayBase == "" {
		gatewayBase = "http://localhost:8080"
	}

	sessionID := session.SessionIDFromContext(ctx)
	if sessionID == "" {
		return extensionPage{}, false
	}

	awaitURL := fmt.Sprintf("%s/browser/await?session=%s&url=%s",
		gatewayBase,
		url.QueryEscape(sessionID),
		url.QueryEscape(rawURL),
	)

	// Use a no-timeout client so ctx deadline governs the wait.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, awaitURL, nil)
	if err != nil {
		return extensionPage{}, false
	}

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		// Gateway not running — silent fallback.
		return extensionPage{}, false
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return extensionPage{}, false
	}

	var raw struct {
		URL     string `json:"url"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return extensionPage{}, false
	}

	// Require at least some meaningful content before accepting the result.
	if len(strings.TrimSpace(raw.Content)) < 100 {
		return extensionPage{}, false
	}

	return extensionPage{URL: raw.URL, Title: raw.Title, Content: raw.Content}, true
}

// browserExtractHTML parses raw HTML and returns the page title, visible text,
// and (optionally) a de-duplicated list of absolute hyperlink URLs.
func browserExtractHTML(rawHTML string, collectLinks bool) (title, text string, links []string) {
	doc, err := xhtml.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return "", rawHTML, nil
	}

	var sb strings.Builder
	seen := make(map[string]bool)

	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode {
			switch n.Data {
			// Always skip content-less or invisible elements entirely.
			// Note: "head" is NOT skipped — we walk into it to find <title>.
			case "script", "style", "noscript", "meta", "link",
				"svg", "canvas", "iframe", "object", "embed":
				return
			case "title":
				if n.FirstChild != nil {
					title = strings.TrimSpace(n.FirstChild.Data)
				}
				return
			case "br", "hr":
				sb.WriteString("\n")
			case "p", "div", "section", "article", "main",
				"h1", "h2", "h3", "h4", "h5", "h6":
				sb.WriteString("\n")
			case "li":
				sb.WriteString("\n• ")
			case "td", "th":
				sb.WriteString("\t")
			case "tr":
				sb.WriteString("\n")
			case "a":
				if collectLinks {
					for _, attr := range n.Attr {
						if attr.Key == "href" && strings.HasPrefix(attr.Val, "http") && !seen[attr.Val] {
							seen[attr.Val] = true
							links = append(links, attr.Val)
						}
					}
				}
			}
		}

		if n.Type == xhtml.TextNode {
			t := strings.TrimFunc(n.Data, unicode.IsControl)
			if strings.TrimSpace(t) != "" {
				sb.WriteString(t)
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	// Collapse consecutive blank lines.
	lines := strings.Split(sb.String(), "\n")
	out := make([]string, 0, len(lines))
	prev := ""
	for _, l := range lines {
		trimmed := strings.TrimRightFunc(l, unicode.IsSpace)
		if trimmed == "" && prev == "" {
			continue
		}
		out = append(out, trimmed)
		prev = trimmed
	}

	return title, strings.Join(out, "\n"), links
}
