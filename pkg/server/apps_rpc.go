// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/mcp/apps"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// appNameRegex validates app names: lowercase alphanumeric, hyphens, 1-63 chars.
var appNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// reservedAppNames are names that cannot be used for apps because they
// collide with HTTP gateway routes (e.g. GET /v1/apps/component-types).
var reservedAppNames = map[string]bool{
	"component-types": true,
}

// AppProvider provides UI app information to the gRPC server.
// Implemented by UIResourceRegistry to avoid tight coupling between
// the server package and the apps package internals.
type AppProvider interface {
	ListAppInfo() []apps.AppInfo
	GetAppHTML(name string) ([]byte, *apps.AppInfo, error)
	CreateApp(name, displayName, description string, html []byte, overwrite bool) (*apps.AppInfo, bool, error)
	UpdateApp(name, displayName, description string, html []byte) (*apps.AppInfo, error)
	DeleteApp(name string) error
}

// AppSpecProvider is implemented by providers that also keep the spec an app
// was compiled from, so the app can be changed in place later (checklist
// ticks) and an agent can read it back before updating. apps.UIResourceRegistry
// implements it; providers that do not still work for everything else.
type AppSpecProvider interface {
	CreateAppWithSpec(name, displayName, description string, html, spec []byte, overwrite bool) (*apps.AppInfo, bool, error)
	UpdateAppWithSpec(name, displayName, description string, html, spec []byte) (*apps.AppInfo, error)
	AppSpec(name string) ([]byte, error)
}

// createAppFromSpec stores a compiled app, with its spec when the provider
// keeps specs.
func createAppFromSpec(provider AppProvider, name, displayName, description string, html []byte, spec *loomv1.UIAppSpec, overwrite bool) (*apps.AppInfo, bool, error) {
	if sp, ok := provider.(AppSpecProvider); ok {
		specJSON, err := protojson.Marshal(spec)
		if err != nil {
			return nil, false, fmt.Errorf("marshal spec: %w", err)
		}
		return sp.CreateAppWithSpec(name, displayName, description, html, specJSON, overwrite)
	}
	return provider.CreateApp(name, displayName, description, html, overwrite)
}

// updateAppFromSpec is createAppFromSpec for updates.
func updateAppFromSpec(provider AppProvider, name, displayName, description string, html []byte, spec *loomv1.UIAppSpec) (*apps.AppInfo, error) {
	if sp, ok := provider.(AppSpecProvider); ok {
		specJSON, err := protojson.Marshal(spec)
		if err != nil {
			return nil, fmt.Errorf("marshal spec: %w", err)
		}
		return sp.UpdateAppWithSpec(name, displayName, description, html, specJSON)
	}
	return provider.UpdateApp(name, displayName, description, html)
}

// AppCompiler compiles UIAppSpec to HTML. Separated from AppProvider to keep
// the registry focused on storage and the compiler focused on validation/rendering.
type AppCompiler interface {
	Compile(spec *loomv1.UIAppSpec) ([]byte, error)
	Validate(spec *loomv1.UIAppSpec) error
	ListComponentTypes() []*loomv1.ComponentType
}

// SetAppProvider sets the app provider for ListUIApps/GetUIApp RPCs.
func (s *MultiAgentServer) SetAppProvider(p AppProvider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appProvider = p
}

// SetAppCompiler sets the compiler for CreateUIApp/UpdateUIApp RPCs.
func (s *MultiAgentServer) SetAppCompiler(c AppCompiler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appCompiler = c
}

// ListUIApps lists all available UI apps registered with the app provider.
func (s *MultiAgentServer) ListUIApps(ctx context.Context, req *loomv1.ListUIAppsRequest) (*loomv1.ListUIAppsResponse, error) {
	s.mu.RLock()
	provider := s.appProvider
	s.mu.RUnlock()

	if provider == nil {
		return &loomv1.ListUIAppsResponse{}, nil
	}

	infos := provider.ListAppInfo()
	protoApps := make([]*loomv1.UIApp, 0, len(infos))
	for _, info := range infos {
		protoApps = append(protoApps, appInfoToProto(&info))
	}

	if s.logger != nil {
		s.logger.Info("ListUIApps completed", zap.Int("total_count", len(protoApps)))
	}

	return &loomv1.ListUIAppsResponse{
		Apps:       protoApps,
		TotalCount: safeIntToInt32(len(protoApps)),
	}, nil
}

// GetUIApp retrieves a specific UI app by short name, returning its metadata and HTML content.
func (s *MultiAgentServer) GetUIApp(ctx context.Context, req *loomv1.GetUIAppRequest) (*loomv1.GetUIAppResponse, error) {
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "app name is required")
	}

	s.mu.RLock()
	provider := s.appProvider
	s.mu.RUnlock()

	if provider == nil {
		return nil, status.Error(codes.NotFound, "no app provider configured")
	}

	content, info, err := provider.GetAppHTML(req.Name)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "app not found: %s", req.Name)
	}

	if s.logger != nil {
		s.logger.Info("GetUIApp completed", zap.String("name", req.Name))
	}

	return &loomv1.GetUIAppResponse{
		App:     appInfoToProto(info),
		Content: content,
	}, nil
}

// CreateUIApp creates a dynamic UI app from a declarative spec.
func (s *MultiAgentServer) CreateUIApp(ctx context.Context, req *loomv1.CreateUIAppRequest) (*loomv1.CreateUIAppResponse, error) {
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "app name is required")
	}
	if !appNameRegex.MatchString(req.Name) {
		return nil, status.Errorf(codes.InvalidArgument,
			"invalid app name %q: must match ^[a-z0-9][a-z0-9-]{0,62}$", req.Name)
	}
	if reservedAppNames[req.Name] {
		return nil, status.Errorf(codes.InvalidArgument,
			"app name %q is reserved (collides with HTTP route)", req.Name)
	}
	if req.Spec == nil {
		return nil, status.Error(codes.InvalidArgument, "spec is required")
	}

	s.mu.RLock()
	provider := s.appProvider
	compiler := s.appCompiler
	s.mu.RUnlock()

	if provider == nil {
		return nil, status.Error(codes.FailedPrecondition, "no app provider configured")
	}
	if compiler == nil {
		return nil, status.Error(codes.FailedPrecondition, "no app compiler configured")
	}

	// Compile spec to HTML
	html, err := compiler.Compile(req.Spec)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "compile spec: %v", err)
	}

	// Use spec title as display name if not provided
	displayName := req.DisplayName
	if displayName == "" {
		displayName = req.Spec.Title
	}

	// Create app in registry
	info, overwritten, err := createAppFromSpec(provider, req.Name, displayName, req.Description, html, req.Spec, req.Overwrite)
	if err != nil {
		errMsg := err.Error()
		switch {
		case strings.Contains(errMsg, "already exists"):
			return nil, status.Errorf(codes.AlreadyExists, "%v", err)
		case strings.Contains(errMsg, "cannot overwrite embedded"):
			return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
		case strings.Contains(errMsg, "limit reached"):
			return nil, status.Errorf(codes.ResourceExhausted, "%v", err)
		default:
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
	}

	if s.logger != nil {
		s.logger.Info("CreateUIApp completed",
			zap.String("name", req.Name),
			zap.Bool("overwritten", overwritten),
			zap.Int("html_bytes", len(html)),
		)
	}

	return &loomv1.CreateUIAppResponse{
		App:         appInfoToProto(info),
		Content:     html,
		Overwritten: overwritten,
	}, nil
}

// UpdateUIApp updates an existing dynamic app's spec.
func (s *MultiAgentServer) UpdateUIApp(ctx context.Context, req *loomv1.UpdateUIAppRequest) (*loomv1.UpdateUIAppResponse, error) {
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "app name is required")
	}
	if req.Spec == nil {
		return nil, status.Error(codes.InvalidArgument, "spec is required")
	}

	s.mu.RLock()
	provider := s.appProvider
	compiler := s.appCompiler
	s.mu.RUnlock()

	if provider == nil {
		return nil, status.Error(codes.FailedPrecondition, "no app provider configured")
	}
	if compiler == nil {
		return nil, status.Error(codes.FailedPrecondition, "no app compiler configured")
	}

	// Compile spec to HTML
	html, err := compiler.Compile(req.Spec)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "compile spec: %v", err)
	}

	// Update in registry
	info, err := updateAppFromSpec(provider, req.Name, req.DisplayName, req.Description, html, req.Spec)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "%v", err)
	}

	if s.logger != nil {
		s.logger.Info("UpdateUIApp completed",
			zap.String("name", req.Name),
			zap.Int("html_bytes", len(html)),
		)
	}

	return &loomv1.UpdateUIAppResponse{
		App:     appInfoToProto(info),
		Content: html,
	}, nil
}

// SetUIAppChecklistItem ticks or unticks one checklist item and saves the app.
func (s *MultiAgentServer) SetUIAppChecklistItem(ctx context.Context, req *loomv1.SetUIAppChecklistItemRequest) (*loomv1.SetUIAppChecklistItemResponse, error) {
	if !appNameRegex.MatchString(req.GetName()) {
		return nil, status.Errorf(codes.InvalidArgument, "invalid app name %q", req.GetName())
	}
	if req.GetComponentId() == "" || req.GetItemId() == "" {
		return nil, status.Error(codes.InvalidArgument, "component_id and item_id are required")
	}

	s.mu.RLock()
	provider := s.appProvider
	compiler := s.appCompiler
	s.mu.RUnlock()
	specProvider, ok := provider.(AppSpecProvider)
	if !ok || compiler == nil {
		return nil, status.Error(codes.FailedPrecondition, "this server cannot change apps in place")
	}

	// Read-modify-write of one app's spec; serialize so concurrent ticks on
	// the same page do not overwrite each other.
	s.checklistMu.Lock()
	defer s.checklistMu.Unlock()

	specJSON, err := specProvider.AppSpec(req.GetName())
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, status.Errorf(codes.NotFound, "%v", err)
		}
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	}
	spec := &loomv1.UIAppSpec{}
	if err := protojson.Unmarshal(specJSON, spec); err != nil {
		return nil, status.Errorf(codes.Internal, "stored spec for %s is unreadable: %v", req.GetName(), err)
	}
	if err := setChecklistItem(spec.GetComponents(), req.GetComponentId(), req.GetItemId(), req.GetChecked()); err != nil {
		return nil, err
	}

	html, err := compiler.Compile(spec)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "recompile %s: %v", req.GetName(), err)
	}
	info, err := updateAppFromSpec(provider, req.GetName(), "", "", html, spec)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "save %s: %v", req.GetName(), err)
	}
	return &loomv1.SetUIAppChecklistItemResponse{App: appInfoToProto(info)}, nil
}

// setChecklistItem finds the checklist component componentID (at any depth)
// and sets the checked prop of its item itemID.
func setChecklistItem(components []*loomv1.UIComponent, componentID, itemID string, checked bool) error {
	comp := findComponent(components, componentID)
	if comp == nil || comp.GetType() != "checklist" {
		return status.Errorf(codes.NotFound, "checklist %q not found", componentID)
	}
	for _, item := range apps.ChecklistItems(comp) {
		if apps.ChecklistItemID(item) == itemID {
			item.Fields["checked"] = structpb.NewBoolValue(checked)
			return nil
		}
	}
	return status.Errorf(codes.NotFound, "item %q not found in checklist %q", itemID, componentID)
}

func findComponent(components []*loomv1.UIComponent, id string) *loomv1.UIComponent {
	for _, comp := range components {
		if comp.GetId() == id {
			return comp
		}
		if found := findComponent(comp.GetChildren(), id); found != nil {
			return found
		}
	}
	return nil
}

// DeleteUIApp deletes a dynamic UI app.
func (s *MultiAgentServer) DeleteUIApp(ctx context.Context, req *loomv1.DeleteUIAppRequest) (*loomv1.DeleteUIAppResponse, error) {
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "app name is required")
	}

	s.mu.RLock()
	provider := s.appProvider
	s.mu.RUnlock()

	if provider == nil {
		return nil, status.Error(codes.FailedPrecondition, "no app provider configured")
	}

	if err := provider.DeleteApp(req.Name); err != nil {
		return nil, status.Errorf(codes.NotFound, "%v", err)
	}

	if s.logger != nil {
		s.logger.Info("DeleteUIApp completed", zap.String("name", req.Name))
	}

	return &loomv1.DeleteUIAppResponse{
		Deleted: true,
	}, nil
}

// ListComponentTypes returns the catalog of available component types for building dynamic apps.
func (s *MultiAgentServer) ListComponentTypes(ctx context.Context, req *loomv1.ListComponentTypesRequest) (*loomv1.ListComponentTypesResponse, error) {
	s.mu.RLock()
	compiler := s.appCompiler
	s.mu.RUnlock()

	if compiler == nil {
		return &loomv1.ListComponentTypesResponse{}, nil
	}

	return &loomv1.ListComponentTypesResponse{
		Types: compiler.ListComponentTypes(),
	}, nil
}

// appInfoToProto converts an apps.AppInfo to a proto UIApp message.
func appInfoToProto(info *apps.AppInfo) *loomv1.UIApp {
	app := &loomv1.UIApp{
		Name:          info.Name,
		Uri:           info.URI,
		DisplayName:   info.DisplayName,
		Description:   info.Description,
		MimeType:      info.MimeType,
		PrefersBorder: info.PrefersBorder,
		Dynamic:       info.Dynamic,
	}
	if !info.UpdatedAt.IsZero() {
		app.UpdatedAt = timestamppb.New(info.UpdatedAt)
	}
	return app
}
