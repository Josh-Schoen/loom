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

package server

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/devices"
	"github.com/teradata-labs/loom/pkg/storage/postgres"
)

// Full method names of DeviceService RPCs, used by the device authenticator.
const (
	deviceServiceRedeemMethod = "/loom.v1.DeviceService/RedeemPairingCode"
	deviceServicePrefix       = "/loom.v1.DeviceService/"
)

// DevicePairingConfig configures device pairing (see docs/architecture/device-pairing.md).
type DevicePairingConfig struct {
	// Required rejects requests that carry no valid credential. When false, a
	// request without a bearer token passes through to the next interceptor
	// (Supabase JWT or the legacy x-user-id path); a present but invalid
	// device token is always rejected.
	Required bool
	// PublicURL is the HTTP gateway address put in pairing links. Empty
	// derives one from the first private IPv4 address and HTTPPort.
	PublicURL string
	// HTTPPort is the gateway port used when PublicURL is empty.
	HTTPPort int
	// LinkScheme is the URL scheme of pairing links (default "loompair").
	// Ignored when LinkBase is set.
	LinkScheme string
	// LinkBase, when set, is an https URL (e.g. "https://setup.example.com/pair")
	// that pairing links are built on instead of LinkScheme: the server and
	// code go in the fragment ("#server=...&code=..."), which browsers do not
	// send to the web host. Opened on a phone with the app, an associated
	// domain hands the link to the app; without the app, the page there can
	// offer the download.
	LinkBase string
	// CodeTTL is the default and maximum pairing code lifetime.
	CodeTTL time.Duration
	// DefaultUserID is the user a device acts as when the code names none.
	DefaultUserID string
	// CertPin, when set, is the pin of the certificate the HTTP gateway
	// serves itself (devices.LoadOrCreatePinnedCert): pairing links carry
	// it, and the derived address is https. A PublicURL must then reach
	// this gateway's TLS unchanged (for example Tailscale forwarding TCP),
	// since devices trust only the pinned certificate.
	CertPin string
	// AdminToken authorizes CreatePairingCode, ListDevices, and RevokeDevice,
	// and every RPC a device token may not call (see deviceAllowedMethods).
	AdminToken string
	// OtherBearersAllowed lets a bearer that is neither a device token nor
	// the admin token pass to the next interceptor when Required is set. Set
	// it only when an authenticator that verifies such bearers (Supabase JWT)
	// runs next; otherwise a junk bearer would reach the x-user-id path.
	OtherBearersAllowed bool
	Logger              *zap.Logger
}

// DeviceServer implements loomv1.DeviceService and authenticates device
// tokens on every other RPC.
type DeviceServer struct {
	loomv1.UnimplementedDeviceServiceServer

	manager *devices.Manager
	cfg     DevicePairingConfig
	// interfaceAddrs lists this machine's addresses (net.InterfaceAddrs;
	// replaced in tests).
	interfaceAddrs func() ([]net.Addr, error)
	logger         *zap.Logger
}

// NewDeviceServer builds a DeviceServer over manager.
func NewDeviceServer(manager *devices.Manager, cfg DevicePairingConfig) (*DeviceServer, error) {
	if manager == nil {
		return nil, errors.New("device manager is required")
	}
	if !strings.HasPrefix(cfg.AdminToken, devices.AdminTokenPrefix) {
		return nil, errors.New("admin token is required")
	}
	if cfg.CodeTTL <= 0 {
		return nil, errors.New("code TTL must be positive")
	}
	if cfg.LinkScheme == "" {
		cfg.LinkScheme = "loompair"
	}
	if cfg.DefaultUserID == "" {
		cfg.DefaultUserID = "default-user"
	}
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	return &DeviceServer{manager: manager, cfg: cfg, logger: logger, interfaceAddrs: net.InterfaceAddrs}, nil
}

// CreatePairingCode issues a pairing code. The admin token is checked by the
// interceptor.
func (s *DeviceServer) CreatePairingCode(_ context.Context, req *loomv1.CreatePairingCodeRequest) (*loomv1.CreatePairingCodeResponse, error) {
	userID := req.GetUserId()
	if userID == "" {
		userID = s.cfg.DefaultUserID
	}
	if err := validateUserID(userID); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid user_id: %v", err)
	}
	if req.GetTtlSeconds() < 0 {
		return nil, status.Error(codes.InvalidArgument, "ttl_seconds must not be negative")
	}
	ttl := s.cfg.CodeTTL
	if requested := time.Duration(req.GetTtlSeconds()) * time.Second; requested > 0 && requested < ttl {
		ttl = requested
	}

	serverURL, pin, err := s.publicURL()
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "no address to put in the pairing link: %v (set server.device_pairing.public_url)", err)
	}
	code, expires, err := s.manager.CreateCode(userID, ttl)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create pairing code: %v", err)
	}
	values := url.Values{"server": {serverURL}, "code": {code}}
	if pin != "" {
		values.Set("pin", pin)
	}
	params := values.Encode()
	link := url.URL{Scheme: s.cfg.LinkScheme, Host: "pair", RawQuery: params}
	if s.cfg.LinkBase != "" {
		base, err := url.Parse(s.cfg.LinkBase)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "invalid link base: %v", err)
		}
		link = *base
		link.RawFragment = params
		link.Fragment, _ = url.PathUnescape(params)
	}
	s.logger.Info("pairing code issued", zap.String("user_id", userID), zap.Time("expires_at", expires))
	return &loomv1.CreatePairingCodeResponse{
		Code:        code,
		ExpiresAt:   timestamppb.New(expires),
		ServerUrl:   serverURL,
		PairingLink: link.String(),
		CertPin:     pin,
	}, nil
}

// RedeemPairingCode exchanges a pairing code for a device token.
func (s *DeviceServer) RedeemPairingCode(_ context.Context, req *loomv1.RedeemPairingCodeRequest) (*loomv1.RedeemPairingCodeResponse, error) {
	if req.GetCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "code is required")
	}
	d, token, err := s.manager.Redeem(req.GetCode(), req.GetDeviceName(), req.GetPlatform())
	if errors.Is(err, devices.ErrInvalidCode) {
		return nil, status.Error(codes.Unauthenticated, "invalid or expired pairing code")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "pair device: %v", err)
	}
	s.logger.Info("device paired", zap.String("device_id", d.ID), zap.String("platform", d.Platform))
	return &loomv1.RedeemPairingCodeResponse{DeviceId: d.ID, DeviceToken: token, UserId: d.UserID}, nil
}

// ListDevices lists paired devices.
func (s *DeviceServer) ListDevices(context.Context, *loomv1.ListDevicesRequest) (*loomv1.ListDevicesResponse, error) {
	list := s.manager.List()
	out := make([]*loomv1.Device, 0, len(list))
	for i := range list {
		out = append(out, deviceProto(&list[i]))
	}
	return &loomv1.ListDevicesResponse{Devices: out}, nil
}

// RevokeDevice revokes a device token.
func (s *DeviceServer) RevokeDevice(_ context.Context, req *loomv1.RevokeDeviceRequest) (*loomv1.RevokeDeviceResponse, error) {
	if req.GetDeviceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "device_id is required")
	}
	d, err := s.manager.Revoke(req.GetDeviceId())
	if errors.Is(err, devices.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "device %s not found", req.GetDeviceId())
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "revoke device: %v", err)
	}
	s.logger.Info("device revoked", zap.String("device_id", d.ID))
	return &loomv1.RevokeDeviceResponse{Device: deviceProto(d)}, nil
}

// deviceAllowedMethods are the RPCs a paired device may call: the
// conversation, the person's own pages and memories, and reading which
// assistants exist. Everything else (managing agents, MCP servers, tools,
// storage, other services) changes the server for everyone or runs programs
// on the host, so it needs the admin token.
var deviceAllowedMethods = map[string]bool{
	"/loom.v1.LoomService/Weave":                       true,
	"/loom.v1.LoomService/StreamWeave":                 true,
	"/loom.v1.LoomService/SubmitClientToolResult":      true,
	"/loom.v1.LoomService/AnswerClarificationQuestion": true,
	"/loom.v1.LoomService/CreateSession":               true,
	"/loom.v1.LoomService/GetSession":                  true,
	"/loom.v1.LoomService/ListSessions":                true,
	"/loom.v1.LoomService/DeleteSession":               true,
	"/loom.v1.LoomService/SubscribeToSession":          true,
	"/loom.v1.LoomService/GetConversationHistory":      true,
	"/loom.v1.LoomService/ListAgents":                  true,
	"/loom.v1.LoomService/GetAgent":                    true,
	"/loom.v1.LoomService/GetHealth":                   true,
	"/loom.v1.LoomService/ListUIApps":                  true,
	"/loom.v1.LoomService/GetUIApp":                    true,
	"/loom.v1.LoomService/CreateUIApp":                 true,
	"/loom.v1.LoomService/UpdateUIApp":                 true,
	"/loom.v1.LoomService/DeleteUIApp":                 true,
	"/loom.v1.LoomService/SetUIAppChecklistItem":       true,
	"/loom.v1.LoomService/SetUIAppSharing":             true,
	"/loom.v1.LoomService/ListComponentTypes":          true,
	"/loom.v1.LoomService/ListUserMemories":            true,
	"/loom.v1.LoomService/AddUserMemory":               true,
	"/loom.v1.LoomService/CorrectUserMemory":           true,
	"/loom.v1.LoomService/ForgetUserMemory":            true,
	"/loom.v1.LoomService/ForgetAllUserMemories":       true,
}

// DeviceMayCall reports whether a device token may call fullMethod.
func DeviceMayCall(fullMethod string) bool {
	return deviceAllowedMethods[fullMethod]
}

// authenticate applies the device-pairing rules for one RPC:
//   - RedeemPairingCode needs no credential (the code is the credential).
//   - Other DeviceService RPCs need the admin token.
//   - A device token establishes the device's user identity, and only for
//     the RPCs in deviceAllowedMethods.
//   - The admin token passes to the next interceptor (the server owner acts
//     as any user, as without pairing).
//   - Any other bearer, or none, is rejected when pairing is required; any
//     other bearer passes only when OtherBearersAllowed (a JWT authenticator
//     follows), and no bearer passes only when pairing is not required.
func (s *DeviceServer) authenticate(ctx context.Context, fullMethod string) (context.Context, error) {
	raw := bearerFromContext(ctx)
	if fullMethod == deviceServiceRedeemMethod {
		return contextWithAuthenticated(ctx), nil
	}
	isAdmin := raw != "" && devices.TokensEqual(raw, s.cfg.AdminToken)
	if strings.HasPrefix(fullMethod, deviceServicePrefix) {
		if !isAdmin {
			return nil, status.Error(codes.PermissionDenied, "this operation needs the server admin token")
		}
		return contextWithAuthenticated(ctx), nil
	}
	if strings.HasPrefix(raw, devices.TokenPrefix) {
		d, ok := s.manager.Authenticate(raw)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "unknown or revoked device token")
		}
		if !deviceAllowedMethods[fullMethod] {
			return nil, status.Error(codes.PermissionDenied, "paired devices cannot call this operation; it needs the server admin token")
		}
		ctx = postgres.ContextWithUserID(ctx, d.UserID)
		return contextWithAuthenticated(ctx), nil
	}
	if isAdmin {
		return ctx, nil
	}
	if !s.cfg.Required {
		return ctx, nil
	}
	if raw == "" {
		return nil, status.Error(codes.Unauthenticated, "a device token is required; pair this device with `looms pair`")
	}
	if !s.cfg.OtherBearersAllowed {
		return nil, status.Error(codes.Unauthenticated, "unknown token; pair this device with `looms pair`")
	}
	return ctx, nil
}

// UnaryServerInterceptor authenticates unary RPCs.
func (s *DeviceServer) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, err := s.authenticate(ctx, info.FullMethod)
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor authenticates streaming RPCs.
func (s *DeviceServer) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, err := s.authenticate(ss.Context(), info.FullMethod)
		if err != nil {
			return err
		}
		return handler(srv, &wrappedServerStream{ServerStream: ss, ctx: ctx})
	}
}

// publicURL returns the configured gateway URL, or one built from the first
// private IPv4 address of this machine (https with the certificate pin when
// the gateway serves pinned TLS).
func (s *DeviceServer) publicURL() (serverURL, pin string, err error) {
	if s.cfg.PublicURL != "" {
		return strings.TrimRight(s.cfg.PublicURL, "/"), s.cfg.CertPin, nil
	}
	if s.cfg.HTTPPort <= 0 {
		return "", "", errors.New("the HTTP gateway is disabled")
	}
	addrs, err := s.interfaceAddrs()
	if err != nil {
		return "", "", err
	}
	ip := firstPrivateIPv4(addrs)
	if ip == "" {
		return "", "", errors.New("no private IPv4 address found")
	}
	scheme := "http"
	if s.cfg.CertPin != "" {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(ip, strconv.Itoa(s.cfg.HTTPPort)), s.cfg.CertPin, nil
}

func firstPrivateIPv4(addrs []net.Addr) string {
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip4 := ipNet.IP.To4(); ip4 != nil && ip4.IsPrivate() {
			return ip4.String()
		}
	}
	return ""
}

func deviceProto(d *devices.Device) *loomv1.Device {
	p := &loomv1.Device{
		Id:         d.ID,
		Name:       d.Name,
		Platform:   d.Platform,
		UserId:     d.UserID,
		CreatedAt:  timestamppb.New(d.CreatedAt),
		LastSeenAt: timestamppb.New(d.LastSeenAt),
	}
	if d.RevokedAt != nil {
		p.RevokedAt = timestamppb.New(*d.RevokedAt)
	}
	return p
}

// authenticatedKey marks a context whose caller an upstream authenticator has
// already verified, so later authenticators do not re-validate its bearer.
type authenticatedKey struct{}

func contextWithAuthenticated(ctx context.Context) context.Context {
	return context.WithValue(ctx, authenticatedKey{}, true)
}

func isAuthenticated(ctx context.Context) bool {
	v, _ := ctx.Value(authenticatedKey{}).(bool)
	return v
}
