// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/mcp/apps"
	"github.com/teradata-labs/loom/pkg/storage/postgres"
	"github.com/teradata-labs/loom/pkg/types"
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
//
// Every method takes the owner: the user ID of the caller (see appOwner).
// Dynamic apps belong to the user who created them; an owner sees the
// shared embedded apps and their own dynamic apps only.
type AppProvider interface {
	ListAppInfo(owner string) []apps.AppInfo
	GetAppHTML(owner, name string) ([]byte, *apps.AppInfo, error)
	CreateApp(owner, name, displayName, description string, html []byte, overwrite bool) (*apps.AppInfo, bool, error)
	UpdateApp(owner, name, displayName, description string, html []byte) (*apps.AppInfo, error)
	DeleteApp(owner, name string) error
}

// AppSpecProvider is implemented by providers that also keep the spec an app
// was compiled from, so the app can be changed in place later (checklist
// ticks) and an agent can read it back before updating. apps.UIResourceRegistry
// implements it; providers that do not still work for everything else.
type AppSpecProvider interface {
	CreateAppWithSpec(owner, name, displayName, description string, html, spec []byte, overwrite bool) (*apps.AppInfo, bool, error)
	UpdateAppWithSpec(owner, name, displayName, description string, html, spec []byte) (*apps.AppInfo, error)
	AppSpec(owner, name string) ([]byte, error)
}

// AppSharingProvider is implemented by providers that keep shared apps:
// apps everyone on the server sees and can change (a household's grocery
// list). apps.UIResourceRegistry implements it.
type AppSharingProvider interface {
	CreateSharedAppWithSpec(sharer, name, displayName, description string, html, spec []byte, overwrite bool) (*apps.AppInfo, bool, error)
	SetAppSharing(owner, name string, shared bool) (*apps.AppInfo, error)
}

// errSharingUnsupported is returned when the app provider keeps no shared apps.
var errSharingUnsupported = errors.New("this server does not support shared apps")

// appOwner is the owner of the apps a request may see and change: the
// caller's user ID, set by the identity interceptors (and carried into agent
// turns, so tools see the user who sent the message). Without one it is
// types.DefaultUserID, the identity of requests when none is configured.
func appOwner(ctx context.Context) string {
	if id := postgres.UserIDFromContext(ctx); id != "" {
		return id
	}
	return types.DefaultUserID
}

// createAppFromSpec stores a compiled app, with its spec when the provider
// keeps specs; shared creates it as a shared app with owner as the sharer.
func createAppFromSpec(provider AppProvider, owner, name, displayName, description string, html []byte, spec *loomv1.UIAppSpec, overwrite, shared bool) (*apps.AppInfo, bool, error) {
	if shared {
		sp, ok := provider.(AppSharingProvider)
		if !ok {
			return nil, false, errSharingUnsupported
		}
		specJSON, err := protojson.Marshal(spec)
		if err != nil {
			return nil, false, fmt.Errorf("marshal spec: %w", err)
		}
		return sp.CreateSharedAppWithSpec(owner, name, displayName, description, html, specJSON, overwrite)
	}
	if sp, ok := provider.(AppSpecProvider); ok {
		specJSON, err := protojson.Marshal(spec)
		if err != nil {
			return nil, false, fmt.Errorf("marshal spec: %w", err)
		}
		return sp.CreateAppWithSpec(owner, name, displayName, description, html, specJSON, overwrite)
	}
	return provider.CreateApp(owner, name, displayName, description, html, overwrite)
}

// updateAppFromSpec is createAppFromSpec for updates.
func updateAppFromSpec(provider AppProvider, owner, name, displayName, description string, html []byte, spec *loomv1.UIAppSpec) (*apps.AppInfo, error) {
	if sp, ok := provider.(AppSpecProvider); ok {
		specJSON, err := protojson.Marshal(spec)
		if err != nil {
			return nil, fmt.Errorf("marshal spec: %w", err)
		}
		return sp.UpdateAppWithSpec(owner, name, displayName, description, html, specJSON)
	}
	return provider.UpdateApp(owner, name, displayName, description, html)
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

	infos := provider.ListAppInfo(appOwner(ctx))
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

	content, info, err := provider.GetAppHTML(appOwner(ctx), req.Name)
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
	info, overwritten, err := createAppFromSpec(provider, appOwner(ctx), req.Name, displayName, req.Description, html, req.Spec, req.Overwrite, req.Shared)
	if err != nil {
		errMsg := err.Error()
		switch {
		case errors.Is(err, apps.ErrNameInUse), errors.Is(err, errSharingUnsupported):
			return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
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
	info, err := updateAppFromSpec(provider, appOwner(ctx), req.Name, req.DisplayName, req.Description, html, req.Spec)
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

	specJSON, err := specProvider.AppSpec(appOwner(ctx), req.GetName())
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
	info, err := updateAppFromSpec(provider, appOwner(ctx), req.GetName(), "", "", html, spec)
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

	if err := provider.DeleteApp(appOwner(ctx), req.Name); err != nil {
		if errors.Is(err, apps.ErrNotSharer) {
			return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
		}
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

// SetUIAppSharing shares one of the caller's dynamic apps with everyone on
// the server, or stops sharing one the caller shared.
func (s *MultiAgentServer) SetUIAppSharing(ctx context.Context, req *loomv1.SetUIAppSharingRequest) (*loomv1.SetUIAppSharingResponse, error) {
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "app name is required")
	}
	s.mu.RLock()
	provider := s.appProvider
	s.mu.RUnlock()
	sp, ok := provider.(AppSharingProvider)
	if provider == nil || !ok {
		return nil, status.Error(codes.FailedPrecondition, errSharingUnsupported.Error())
	}
	info, err := sp.SetAppSharing(appOwner(ctx), req.Name, req.Shared)
	switch {
	case errors.Is(err, apps.ErrNameInUse), errors.Is(err, apps.ErrNotSharer):
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	case err != nil && strings.Contains(err.Error(), "not found"):
		return nil, status.Errorf(codes.NotFound, "%v", err)
	case err != nil:
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	}
	return &loomv1.SetUIAppSharingResponse{App: appInfoToProto(info)}, nil
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
		Shared:        info.Shared,
		SharedBy:      info.SharedBy,
	}
	if !info.UpdatedAt.IsZero() {
		app.UpdatedAt = timestamppb.New(info.UpdatedAt)
	}
	return app
}
