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

// Package apps provides the MCP Apps UI resource registry and embedded HTML apps.
// It manages registration and retrieval of interactive HTML resources that
// MCP clients (like Claude Desktop) can render alongside tool results.
package apps

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/teradata-labs/loom/pkg/mcp/protocol"
)

const (
	// DefaultMaxDynamic is the maximum number of dynamic (agent-created) apps
	// per owner.
	DefaultMaxDynamic = 100

	// DefaultMaxTotalBytes is the maximum total HTML bytes of one owner's
	// dynamic apps (50 MB).
	DefaultMaxTotalBytes int64 = 50 * 1024 * 1024

	// DefaultOwner owns dynamic apps on servers without per-user identity,
	// apps created through the owner-less methods, and apps stored before
	// apps had owners. It equals types.DefaultUserID, the user ID requests
	// get when no identity is configured.
	DefaultOwner = "default-user"
)

// UIResource represents an HTML app that can be rendered by an MCP client.
type UIResource struct {
	URI         string                   // ui:// URI identifying this resource
	Name        string                   // Human-readable name
	Description string                   // Description of what this UI shows
	MIMEType    string                   // MIME type (typically protocol.ResourceMIME)
	HTML        []byte                   // Raw HTML content
	Meta        *protocol.UIResourceMeta // Security and display metadata
	Dynamic     bool                     // true = agent-created, false = embedded
	Owner       string                   // User who owns a dynamic app (DefaultOwner when empty); unused for embedded
	UpdatedAt   time.Time                // Last create/update (dynamic apps only)
	Spec        []byte                   // protojson UIAppSpec the HTML was compiled from (dynamic apps; may be nil)
}

// UIResourceRegistry manages UI resources that the MCP server exposes.
// Embedded apps are shared by everyone. Dynamic (agent-created) apps belong
// to the user who created them: two owners can each have an app with the
// same name, and the owner-scoped methods (ListAppInfo, GetAppHTML,
// CreateApp, ...) only see the given owner's apps plus the embedded ones.
// Thread-safe for concurrent access.
type UIResourceRegistry struct {
	resources     map[string]*UIResource            // embedded apps, by URI
	dynamic       map[string]map[string]*UIResource // dynamic apps, by owner then name
	mu            sync.RWMutex
	onChange      func()           // Called outside lock after mutations
	maxDynamic    int              // Max dynamic apps per owner (default 100)
	maxTotalBytes int64            // Max total HTML bytes of one owner's dynamic apps (default 50MB)
	store         *fileStore       // Persists dynamic apps when non-nil
	now           func() time.Time // Clock for UpdatedAt
}

// NewUIResourceRegistry creates a new empty registry.
func NewUIResourceRegistry() *UIResourceRegistry {
	return &UIResourceRegistry{
		resources:     make(map[string]*UIResource),
		dynamic:       make(map[string]map[string]*UIResource),
		maxDynamic:    DefaultMaxDynamic,
		maxTotalBytes: DefaultMaxTotalBytes,
		now:           time.Now,
	}
}

// normalizeOwner maps an empty owner to DefaultOwner.
func normalizeOwner(owner string) string {
	if owner == "" {
		return DefaultOwner
	}
	return owner
}

func appURI(name string) string { return "ui://loom/" + name }

// EnablePersistence stores dynamic apps in dir and loads the apps already
// there, so agent-created apps survive restarts. Call once, after
// registering embedded apps and before serving. Files that cannot be read,
// or that would exceed the registry limits, are skipped and returned by
// path relative to dir; they stay on disk.
func (r *UIResourceRegistry) EnablePersistence(dir string) (loaded int, skipped []string, err error) {
	store, err := newFileStore(dir)
	if err != nil {
		return 0, nil, err
	}
	stored, skipped, err := store.loadAll()
	if err != nil {
		return 0, nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, app := range stored {
		res := &UIResource{
			URI:         appURI(app.Name),
			Name:        app.DisplayName,
			Description: app.Description,
			MIMEType:    protocol.ResourceMIME,
			HTML:        []byte(app.HTML),
			Meta:        &protocol.UIResourceMeta{PrefersBorder: boolPtrTrue()},
			Dynamic:     true,
			Owner:       normalizeOwner(app.Owner),
			UpdatedAt:   app.UpdatedAt,
			Spec:        specBytes(app.Spec),
		}
		if _, err := r.upsertLocked(res); err != nil {
			skipped = append(skipped, app.file)
			continue
		}
		loaded++
	}
	r.store = store
	return loaded, skipped, nil
}

// SetOnChange registers a callback that is invoked after any mutation
// (Upsert, Delete). The callback is called outside the lock to prevent
// deadlocks. Only one callback can be registered; a new call replaces
// the previous one.
func (r *UIResourceRegistry) SetOnChange(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onChange = fn
}

// Register adds an embedded UI resource to the registry.
// Returns an error if a resource with the same URI is already registered.
func (r *UIResourceRegistry) Register(res *UIResource) error {
	if res == nil {
		return fmt.Errorf("resource cannot be nil")
	}
	if res.URI == "" {
		return fmt.Errorf("resource URI cannot be empty")
	}
	if res.Dynamic {
		return fmt.Errorf("dynamic resources are added with Upsert or CreateApp")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.resources[res.URI]; exists {
		return fmt.Errorf("resource already registered: %s", res.URI)
	}

	r.resources[res.URI] = res
	return nil
}

// Upsert creates or replaces a dynamic UI resource of res.Owner (DefaultOwner
// when empty). It rejects resources whose name is taken by an embedded app.
// Returns true if a new resource was created, false if an existing dynamic
// resource was replaced.
func (r *UIResourceRegistry) Upsert(res *UIResource) (created bool, err error) {
	if res == nil {
		return false, fmt.Errorf("resource cannot be nil")
	}
	if res.URI == "" {
		return false, fmt.Errorf("resource URI cannot be empty")
	}
	if !res.Dynamic {
		return false, fmt.Errorf("only dynamic resources can be upserted")
	}

	var notify func()

	r.mu.Lock()
	created, err = r.upsertLocked(res)
	notify = r.onChange
	r.mu.Unlock()

	if err != nil {
		return false, err
	}
	if notify != nil {
		notify()
	}
	return created, nil
}

// upsertLocked inserts or replaces a dynamic resource of res.Owner.
// The caller MUST hold r.mu in write mode.
func (r *UIResourceRegistry) upsertLocked(res *UIResource) (created bool, err error) {
	name := extractAppName(res.URI)
	if _, embedded := r.embeddedLocked(name); embedded {
		return false, fmt.Errorf("cannot overwrite embedded resource: %s", res.URI)
	}
	res.Owner = normalizeOwner(res.Owner)
	apps := r.dynamic[res.Owner]
	existing, exists := apps[name]

	// Capacity checks for new resources
	if !exists {
		dynCount, dynBytes := r.dynamicStatsLocked(res.Owner)
		if dynCount >= r.maxDynamic {
			return false, fmt.Errorf("dynamic app limit reached (%d)", r.maxDynamic)
		}
		if dynBytes+int64(len(res.HTML)) > r.maxTotalBytes {
			return false, fmt.Errorf("dynamic app total size limit reached (%d bytes)", r.maxTotalBytes)
		}
	}

	if res.UpdatedAt.IsZero() {
		res.UpdatedAt = r.now()
	}
	if apps == nil {
		apps = make(map[string]*UIResource)
		r.dynamic[res.Owner] = apps
	}
	apps[name] = res
	if r.store != nil {
		if err := r.store.save(res); err != nil {
			// Keep memory and disk in agreement: undo the change.
			if exists {
				apps[name] = existing
			} else {
				delete(apps, name)
			}
			return false, err
		}
	}
	return !exists, nil
}

// Delete removes a DefaultOwner dynamic UI resource by URI. Rejects deletion
// of embedded resources. Use DeleteApp for other owners.
func (r *UIResourceRegistry) Delete(uri string) error {
	r.mu.RLock()
	_, embedded := r.resources[uri]
	r.mu.RUnlock()
	if embedded {
		return fmt.Errorf("cannot delete embedded resource: %s", uri)
	}
	return r.DeleteApp(DefaultOwner, extractAppName(uri))
}

// dynamicStatsLocked returns the count and total HTML bytes of owner's
// dynamic resources. Must be called with r.mu held.
func (r *UIResourceRegistry) dynamicStatsLocked(owner string) (count int, totalBytes int64) {
	for _, res := range r.dynamic[owner] {
		count++
		totalBytes += int64(len(res.HTML))
	}
	return count, totalBytes
}

// visibleLocked returns the embedded resources and owner's dynamic ones.
// Must be called with r.mu held.
func (r *UIResourceRegistry) visibleLocked(owner string) []*UIResource {
	owned := r.dynamic[normalizeOwner(owner)]
	out := make([]*UIResource, 0, len(r.resources)+len(owned))
	for _, res := range r.resources {
		out = append(out, res)
	}
	for _, res := range owned {
		out = append(out, res)
	}
	return out
}

// embeddedLocked finds an embedded app by short name. Must be called with
// r.mu held.
func (r *UIResourceRegistry) embeddedLocked(name string) (*UIResource, bool) {
	if res, ok := r.resources[appURI(name)]; ok {
		return res, true
	}
	for uri, res := range r.resources {
		if extractAppName(uri) == name {
			return res, true
		}
	}
	return nil, false
}

// lookupLocked finds an app by short name: an embedded app, else owner's
// dynamic app. Must be called with r.mu held.
func (r *UIResourceRegistry) lookupLocked(owner, name string) (*UIResource, bool) {
	if res, ok := r.embeddedLocked(name); ok {
		return res, true
	}
	res, ok := r.dynamic[normalizeOwner(owner)][name]
	return res, ok
}

// byURILocked finds an embedded resource, or a DefaultOwner dynamic one, by
// URI. Must be called with r.mu held.
func (r *UIResourceRegistry) byURILocked(uri string) (*UIResource, bool) {
	if res, ok := r.resources[uri]; ok {
		return res, true
	}
	res, ok := r.dynamic[DefaultOwner][extractAppName(uri)]
	if !ok || res.URI != uri {
		return nil, false
	}
	return res, true
}

// List returns the embedded resources and DefaultOwner's dynamic ones as MCP
// Resource objects, sorted by URI. This is the view of an MCP client, which
// has no per-user identity.
func (r *UIResourceRegistry) List() []protocol.Resource {
	r.mu.RLock()
	defer r.mu.RUnlock()

	visible := r.visibleLocked(DefaultOwner)
	resources := make([]protocol.Resource, 0, len(visible))
	for _, res := range visible {
		resources = append(resources, protocol.Resource{
			URI:         res.URI,
			Name:        res.Name,
			Description: res.Description,
			MimeType:    res.MIMEType,
		})
	}
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].URI < resources[j].URI
	})
	return resources
}

// Read returns the contents of an embedded or DefaultOwner resource by URI.
// Returns an error if the resource is not found.
func (r *UIResourceRegistry) Read(uri string) (*protocol.ReadResourceResult, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res, ok := r.byURILocked(uri)
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", uri)
	}

	contents := protocol.ResourceContents{
		URI:      res.URI,
		MimeType: res.MIMEType,
		Text:     string(res.HTML),
	}

	// Add UI-specific metadata if present
	if res.Meta != nil {
		metaMap := make(map[string]interface{})
		if res.Meta.CSP != nil {
			metaMap["csp"] = res.Meta.CSP
		}
		if res.Meta.Permissions != nil {
			metaMap["permissions"] = res.Meta.Permissions
		}
		if res.Meta.Domain != "" {
			metaMap["domain"] = res.Meta.Domain
		}
		if res.Meta.PrefersBorder != nil {
			metaMap["prefersBorder"] = *res.Meta.PrefersBorder
		}
		if len(metaMap) > 0 {
			contents.Meta = metaMap
		}
	}

	return &protocol.ReadResourceResult{
		Contents: []protocol.ResourceContents{contents},
	}, nil
}

// Count returns the number of registered resources: embedded apps plus every
// owner's dynamic apps.
func (r *UIResourceRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := len(r.resources)
	for _, owned := range r.dynamic {
		n += len(owned)
	}
	return n
}

// Get returns an embedded or DefaultOwner UIResource by its URI, or an error
// if not found.
func (r *UIResourceRegistry) Get(uri string) (*UIResource, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res, ok := r.byURILocked(uri)
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", uri)
	}
	copy := *res
	return &copy, nil
}

// AppNames returns the sorted short names of the apps owner can see (the
// embedded ones and owner's own). For example, "ui://loom/data-chart" yields
// "data-chart".
func (r *UIResourceRegistry) AppNames(owner string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	visible := r.visibleLocked(owner)
	names := make([]string, 0, len(visible))
	for _, res := range visible {
		names = append(names, extractAppName(res.URI))
	}
	sort.Strings(names)
	return names
}

// AppHTML returns the raw HTML of an app owner can see, by short name.
// Returns an error if there is none.
func (r *UIResourceRegistry) AppHTML(owner, name string) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if res, ok := r.lookupLocked(owner, name); ok {
		return append([]byte(nil), res.HTML...), nil
	}
	return nil, fmt.Errorf("app not found: %s", name)
}

// AppInfo holds metadata about a UI app for use by the gRPC server.
// This avoids the server importing the apps package directly.
type AppInfo struct {
	Name          string
	URI           string
	DisplayName   string
	Description   string
	MimeType      string
	PrefersBorder bool
	Dynamic       bool
	UpdatedAt     time.Time // Zero for embedded apps
}

func appInfo(res *UIResource) AppInfo {
	info := AppInfo{
		Name:        extractAppName(res.URI),
		URI:         res.URI,
		DisplayName: res.Name,
		Description: res.Description,
		MimeType:    res.MIMEType,
		Dynamic:     res.Dynamic,
		UpdatedAt:   res.UpdatedAt,
	}
	if res.Meta != nil && res.Meta.PrefersBorder != nil {
		info.PrefersBorder = *res.Meta.PrefersBorder
	}
	return info
}

// ListAppInfo returns metadata for the apps owner can see (embedded apps and
// owner's dynamic apps), sorted by name.
func (r *UIResourceRegistry) ListAppInfo(owner string) []AppInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	visible := r.visibleLocked(owner)
	infos := make([]AppInfo, 0, len(visible))
	for _, res := range visible {
		infos = append(infos, appInfo(res))
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Name < infos[j].Name
	})
	return infos
}

// GetAppHTML returns the HTML content and metadata of an app owner can see,
// by short name. Returns an error if there is none.
func (r *UIResourceRegistry) GetAppHTML(owner, name string) ([]byte, *AppInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res, ok := r.lookupLocked(owner, name)
	if !ok {
		return nil, nil, fmt.Errorf("app not found: %s", name)
	}
	info := appInfo(res)
	return append([]byte(nil), res.HTML...), &info, nil
}

// CreateApp creates a dynamic app of owner from compiled HTML. It builds the
// UIResource, checks existence atomically, and returns the app info. This
// implements the AppProvider interface for the gRPC server.
func (r *UIResourceRegistry) CreateApp(owner, name, displayName, description string, html []byte, overwrite bool) (*AppInfo, bool, error) {
	return r.CreateAppWithSpec(owner, name, displayName, description, html, nil, overwrite)
}

// CreateAppWithSpec is CreateApp that also keeps spec (protojson UIAppSpec),
// so the app can later be changed in place (e.g. a checklist tick).
func (r *UIResourceRegistry) CreateAppWithSpec(owner, name, displayName, description string, html, spec []byte, overwrite bool) (*AppInfo, bool, error) {
	owner = normalizeOwner(owner)
	res := &UIResource{
		URI:         appURI(name),
		Name:        displayName,
		Description: description,
		MIMEType:    protocol.ResourceMIME,
		HTML:        html,
		Meta: &protocol.UIResourceMeta{
			PrefersBorder: boolPtrTrue(),
		},
		Dynamic: true,
		Owner:   owner,
		Spec:    spec,
	}

	var notify func()

	// Single write lock: check-and-create atomically to prevent TOCTOU races.
	r.mu.Lock()
	existing, exists := r.lookupLocked(owner, name)
	if exists && (!overwrite || !existing.Dynamic) {
		r.mu.Unlock()
		if existing.Dynamic {
			return nil, false, fmt.Errorf("app already exists: %s (use overwrite=true to replace)", name)
		}
		return nil, false, fmt.Errorf("cannot overwrite embedded app: %s", name)
	}

	created, err := r.upsertLocked(res)
	notify = r.onChange
	r.mu.Unlock()

	if err != nil {
		return nil, false, err
	}
	if notify != nil {
		notify()
	}

	info := appInfo(res)
	return &info, !created, nil
}

// UpdateApp updates an existing dynamic app of owner with new HTML. Returns
// an error if owner has no such app or it is embedded. The stored spec is
// dropped, since it no longer matches the HTML.
func (r *UIResourceRegistry) UpdateApp(owner, name, displayName, description string, html []byte) (*AppInfo, error) {
	return r.UpdateAppWithSpec(owner, name, displayName, description, html, nil)
}

// UpdateAppWithSpec is UpdateApp that also replaces the stored spec.
func (r *UIResourceRegistry) UpdateAppWithSpec(owner, name, displayName, description string, html, spec []byte) (*AppInfo, error) {
	owner = normalizeOwner(owner)

	var notify func()

	// Single write lock: check-and-update atomically to prevent TOCTOU races.
	r.mu.Lock()
	existing, exists := r.lookupLocked(owner, name)
	if !exists {
		r.mu.Unlock()
		return nil, fmt.Errorf("app not found: %s", name)
	}
	if !existing.Dynamic {
		r.mu.Unlock()
		return nil, fmt.Errorf("cannot update embedded app: %s", name)
	}

	// Use existing values for empty fields
	if displayName == "" {
		displayName = existing.Name
	}
	if description == "" {
		description = existing.Description
	}

	res := &UIResource{
		URI:         appURI(name),
		Name:        displayName,
		Description: description,
		MIMEType:    protocol.ResourceMIME,
		HTML:        html,
		Meta: &protocol.UIResourceMeta{
			PrefersBorder: boolPtrTrue(),
		},
		Dynamic: true,
		Owner:   owner,
		Spec:    spec,
	}

	_, err := r.upsertLocked(res)
	notify = r.onChange
	r.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if notify != nil {
		notify()
	}

	info := appInfo(res)
	return &info, nil
}

// AppSpec returns the stored spec (protojson UIAppSpec) of one of owner's
// dynamic apps, or an error when owner has no such app or it has no stored
// spec (apps created before specs were kept, or through CreateApp/UpdateApp).
func (r *UIResourceRegistry) AppSpec(owner, name string) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	res, ok := r.dynamic[normalizeOwner(owner)][name]
	if !ok {
		return nil, fmt.Errorf("app not found: %s", name)
	}
	if len(res.Spec) == 0 {
		return nil, fmt.Errorf("app %s has no stored spec", name)
	}
	return append([]byte(nil), res.Spec...), nil
}

// DeleteApp deletes one of owner's dynamic apps by short name. Rejects
// deletion of embedded apps.
func (r *UIResourceRegistry) DeleteApp(owner, name string) error {
	owner = normalizeOwner(owner)
	var notify func()

	r.mu.Lock()
	if res, embedded := r.embeddedLocked(name); embedded {
		r.mu.Unlock()
		return fmt.Errorf("cannot delete embedded resource: %s", res.URI)
	}
	owned := r.dynamic[owner]
	existing, exists := owned[name]
	if !exists {
		r.mu.Unlock()
		return fmt.Errorf("resource not found: %s", appURI(name))
	}

	delete(owned, name)
	if r.store != nil {
		if err := r.store.delete(owner, name); err != nil {
			owned[name] = existing
			r.mu.Unlock()
			return err
		}
	}
	if len(owned) == 0 {
		delete(r.dynamic, owner)
	}
	notify = r.onChange
	r.mu.Unlock()

	if notify != nil {
		notify()
	}
	return nil
}

func boolPtrTrue() *bool {
	b := true
	return &b
}

// ExtractAppName extracts the short app name from a ui:// URI.
// For example, "ui://loom/data-chart" returns "data-chart".
func ExtractAppName(uri string) string {
	// Find last "/" and return everything after it
	if idx := strings.LastIndex(uri, "/"); idx >= 0 {
		return uri[idx+1:]
	}
	return uri
}

// extractAppName is an unexported alias for internal callers.
func extractAppName(uri string) string {
	return ExtractAppName(uri)
}
