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
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	loomv1 "github.com/teradata-labs/loom/gen/go/loom/v1"
	"github.com/teradata-labs/loom/pkg/devices"
	"github.com/teradata-labs/loom/pkg/storage/postgres"
)

const testWeaveMethod = "/loom.v1.LoomService/Weave"

func newTestDeviceServer(t *testing.T, required bool) *DeviceServer {
	t.Helper()
	dir := t.TempDir()
	m, err := devices.Open(filepath.Join(dir, "devices.json"))
	require.NoError(t, err)
	admin, err := devices.LoadOrCreateAdminToken(filepath.Join(dir, "device-admin.token"))
	require.NoError(t, err)
	s, err := NewDeviceServer(m, DevicePairingConfig{
		Required:   required,
		PublicURL:  "http://192.168.1.20:5006/",
		CodeTTL:    10 * time.Minute,
		AdminToken: admin,
	})
	require.NoError(t, err)
	return s
}

func withBearer(token string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
}

func pair(t *testing.T, s *DeviceServer) *loomv1.RedeemPairingCodeResponse {
	t.Helper()
	created, err := s.CreatePairingCode(context.Background(), &loomv1.CreatePairingCodeRequest{})
	require.NoError(t, err)
	redeemed, err := s.RedeemPairingCode(context.Background(), &loomv1.RedeemPairingCodeRequest{
		Code: created.GetCode(), DeviceName: "Phone", Platform: "ios",
	})
	require.NoError(t, err)
	return redeemed
}

func TestNewDeviceServerValidation(t *testing.T) {
	m, err := devices.Open(filepath.Join(t.TempDir(), "d.json"))
	require.NoError(t, err)
	_, err = NewDeviceServer(nil, DevicePairingConfig{AdminToken: devices.AdminTokenPrefix + "x", CodeTTL: time.Minute})
	assert.Error(t, err)
	_, err = NewDeviceServer(m, DevicePairingConfig{AdminToken: "nope", CodeTTL: time.Minute})
	assert.Error(t, err)
	_, err = NewDeviceServer(m, DevicePairingConfig{AdminToken: devices.AdminTokenPrefix + "x"})
	assert.Error(t, err)
}

func TestCreatePairingCodeBuildsLink(t *testing.T) {
	s := newTestDeviceServer(t, false)
	resp, err := s.CreatePairingCode(context.Background(), &loomv1.CreatePairingCodeRequest{TtlSeconds: 60})
	require.NoError(t, err)
	assert.Equal(t, "http://192.168.1.20:5006", resp.GetServerUrl())
	assert.WithinDuration(t, time.Now().Add(time.Minute), resp.GetExpiresAt().AsTime(), 5*time.Second)

	link, err := url.Parse(resp.GetPairingLink())
	require.NoError(t, err)
	assert.Equal(t, "loompair", link.Scheme)
	assert.Equal(t, "pair", link.Host)
	assert.Equal(t, resp.GetServerUrl(), link.Query().Get("server"))
	assert.Equal(t, resp.GetCode(), link.Query().Get("code"))

	// Requests beyond the configured TTL are capped.
	long, err := s.CreatePairingCode(context.Background(), &loomv1.CreatePairingCodeRequest{TtlSeconds: 86400})
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(10*time.Minute), long.GetExpiresAt().AsTime(), 5*time.Second)

	_, err = s.CreatePairingCode(context.Background(), &loomv1.CreatePairingCodeRequest{TtlSeconds: -1})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestRedeemPairingCode(t *testing.T) {
	s := newTestDeviceServer(t, false)
	created, err := s.CreatePairingCode(context.Background(), &loomv1.CreatePairingCodeRequest{UserId: "alice"})
	require.NoError(t, err)

	resp, err := s.RedeemPairingCode(context.Background(), &loomv1.RedeemPairingCodeRequest{Code: created.GetCode()})
	require.NoError(t, err)
	assert.Equal(t, "alice", resp.GetUserId())
	assert.NotEmpty(t, resp.GetDeviceToken())

	_, err = s.RedeemPairingCode(context.Background(), &loomv1.RedeemPairingCodeRequest{Code: created.GetCode()})
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	_, err = s.RedeemPairingCode(context.Background(), &loomv1.RedeemPairingCodeRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestListAndRevokeDevices(t *testing.T) {
	s := newTestDeviceServer(t, false)
	paired := pair(t, s)

	list, err := s.ListDevices(context.Background(), &loomv1.ListDevicesRequest{})
	require.NoError(t, err)
	require.Len(t, list.GetDevices(), 1)
	assert.Equal(t, "Phone", list.GetDevices()[0].GetName())

	revoked, err := s.RevokeDevice(context.Background(), &loomv1.RevokeDeviceRequest{DeviceId: paired.GetDeviceId()})
	require.NoError(t, err)
	assert.NotNil(t, revoked.GetDevice().GetRevokedAt())

	_, err = s.authenticate(withBearer(paired.GetDeviceToken()), testWeaveMethod)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	_, err = s.RevokeDevice(context.Background(), &loomv1.RevokeDeviceRequest{DeviceId: "dev_nope"})
	assert.Equal(t, codes.NotFound, status.Code(err))
	_, err = s.RevokeDevice(context.Background(), &loomv1.RevokeDeviceRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestDeviceAuthenticateRules(t *testing.T) {
	s := newTestDeviceServer(t, false)
	token := pair(t, s).GetDeviceToken()

	tests := []struct {
		name     string
		ctx      context.Context
		method   string
		wantCode codes.Code
		wantUser string
		wantAuth bool
	}{
		{"redeem needs no credential", context.Background(), deviceServiceRedeemMethod, codes.OK, "", true},
		{"admin RPC with admin token", withBearer(s.cfg.AdminToken), "/loom.v1.DeviceService/ListDevices", codes.OK, "", true},
		{"admin RPC without token", context.Background(), "/loom.v1.DeviceService/CreatePairingCode", codes.PermissionDenied, "", false},
		{"admin RPC with device token", withBearer(token), "/loom.v1.DeviceService/RevokeDevice", codes.PermissionDenied, "", false},
		{"device token sets user", withBearer(token), testWeaveMethod, codes.OK, "default-user", true},
		{"unknown device token", withBearer(devices.TokenPrefix + "forged"), testWeaveMethod, codes.Unauthenticated, "", false},
		{"other bearer passes through", withBearer("eyJ.some.jwt"), testWeaveMethod, codes.OK, "", false},
		{"no bearer passes when optional", context.Background(), testWeaveMethod, codes.OK, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, err := s.authenticate(tt.ctx, tt.method)
			require.Equal(t, tt.wantCode, status.Code(err), "err: %v", err)
			if err != nil {
				return
			}
			assert.Equal(t, tt.wantUser, postgres.UserIDFromContext(ctx))
			assert.Equal(t, tt.wantAuth, isAuthenticated(ctx))
		})
	}
}

func TestDeviceAuthenticateRequired(t *testing.T) {
	s := newTestDeviceServer(t, true)
	_, err := s.authenticate(context.Background(), testWeaveMethod)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	// Pairing itself still works without a credential.
	_, err = s.authenticate(context.Background(), deviceServiceRedeemMethod)
	assert.NoError(t, err)
}

func TestDeviceInterceptorsChainWithSupabaseAndUserID(t *testing.T) {
	s := newTestDeviceServer(t, false)
	token := pair(t, s).GetDeviceToken()
	secret := []byte("supabase-test-secret-at-least-32-bytes!!")
	authr := testHS256Authenticator(t, secret)
	userID := UserIDUnaryInterceptor(UserIDConfig{RequireUserID: true})

	chain := []grpc.UnaryServerInterceptor{s.UnaryServerInterceptor(), authr.UnaryServerInterceptor(), userID}
	run := func(ctx context.Context, method string) (string, error) {
		var seen string
		handler := func(ctx context.Context, _ any) (any, error) {
			seen = postgres.UserIDFromContext(ctx)
			return nil, nil
		}
		next := grpc.UnaryHandler(handler)
		for i := len(chain) - 1; i >= 0; i-- {
			ic, inner := chain[i], next
			next = func(ctx context.Context, req any) (any, error) {
				return ic(ctx, req, &grpc.UnaryServerInfo{FullMethod: method}, inner)
			}
		}
		_, err := next(ctx, nil)
		return seen, err
	}

	// The device token is not re-validated as a JWT downstream.
	user, err := run(withBearer(token), testWeaveMethod)
	require.NoError(t, err)
	assert.Equal(t, "default-user", user)

	// A Supabase JWT still works alongside device pairing.
	claims := validClaims()
	claims["sub"] = "jwt-user"
	jwtToken := mintHS256(t, secret, claims)
	user, err = run(withBearer(jwtToken), testWeaveMethod)
	require.NoError(t, err)
	assert.Equal(t, "jwt-user", user)

	// Redeem passes even though require_user_id is on and no user is known.
	_, err = run(context.Background(), deviceServiceRedeemMethod)
	assert.NoError(t, err)
}

func TestFirstPrivateIPv4(t *testing.T) {
	mk := func(cidr string) net.Addr {
		ip, n, err := net.ParseCIDR(cidr)
		require.NoError(t, err)
		n.IP = ip
		return n
	}
	addrs := []net.Addr{mk("127.0.0.1/8"), mk("fe80::1/64"), mk("8.8.8.8/32"), mk("10.0.0.7/24"), mk("192.168.1.2/24")}
	assert.Equal(t, "10.0.0.7", firstPrivateIPv4(addrs))
	assert.Equal(t, "", firstPrivateIPv4(addrs[:3]))
}

func TestAppsPagesRequireCredentialsWhenPairingIsRequired(t *testing.T) {
	s := newTestDeviceServer(t, true)
	token := pair(t, s).GetDeviceToken()

	h := NewHTTPServer(nil, "127.0.0.1:0", "127.0.0.1:0", nil)
	h.SetAppHTMLProvider(newTestProvider())
	h.SetStreamInterceptors(s.StreamServerInterceptor(), UserIDStreamInterceptor(UserIDConfig{}))

	for _, path := range []string{"/apps/", "/apps/data-chart"} {
		rr := httptest.NewRecorder()
		h.handleApps(rr, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusUnauthorized, rr.Code, "%s without a token", path)

		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr = httptest.NewRecorder()
		h.handleApps(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code, "%s with a device token", path)
	}
}

func TestAppsPagesOpenWithoutInterceptors(t *testing.T) {
	h := &HTTPServer{appHTMLProvider: newTestProvider()}
	rr := httptest.NewRecorder()
	h.handleApps(rr, httptest.NewRequest(http.MethodGet, "/apps/data-chart", nil))
	assert.Equal(t, http.StatusOK, rr.Code)
}
