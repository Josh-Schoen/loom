// Copyright © 2026 Teradata Corporation - All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"

	"go.uber.org/zap"
)

// Extension routes let a service next to loom serve HTTP routes on loom's
// gateway, behind loom's authentication: loom authenticates each request as
// it does its own (device tokens, JWTs, the user ID interceptor), then
// forwards it to the service on this machine with the caller's user ID. The
// caller's credentials are not forwarded. Clients keep one address, one
// certificate and one credential.

// Headers loom sets on forwarded requests. Values clients send under these
// names are removed first.
const (
	// ExtensionUserIDHeader carries the authenticated caller's user ID.
	ExtensionUserIDHeader = "X-Loom-User-Id"
	// ExtensionSecretHeader carries the route's shared secret, so the
	// service can tell requests forwarded by loom from anything else that
	// reaches its port.
	ExtensionSecretHeader = "X-Loom-Extension-Secret"
)

// extensionMethodPrefix names extension routes to the interceptors, like a
// gRPC method: "/loom.extension/<name>".
const extensionMethodPrefix = "/loom.extension/"

// ExtensionMethod is the method name interceptors see for an extension
// route's requests.
func ExtensionMethod(name string) string { return extensionMethodPrefix + name }

// ExtensionRoute forwards requests under PathPrefix to Upstream.
type ExtensionRoute struct {
	// Name identifies the route in logs and in its method name.
	Name string
	// PathPrefix is the path the route serves, ending in "/" (e.g.
	// "/v1/household/"). The full path is forwarded unchanged.
	PathPrefix string
	// Upstream is the service's base URL; it must be on this machine
	// (localhost or a loopback address).
	Upstream *url.URL
	// Secret is sent as ExtensionSecretHeader; empty sends none.
	Secret string
	// AllowDevices lets paired devices call the route (DeviceServer
	// AllowMethod with ExtensionMethod(Name)).
	AllowDevices bool
}

var extensionNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// reservedExtensionPrefixes are loom's own HTTP routes; an extension route
// may not serve them or anything above them.
var reservedExtensionPrefixes = []string{"/health", "/swagger-ui", "/openapi.json", "/apps", "/v1/weave", "/v1/pairing"}

// ParseExtensionRoute validates one route's settings.
func ParseExtensionRoute(name, pathPrefix, upstream, secret string, allowDevices bool) (ExtensionRoute, error) {
	if !extensionNamePattern.MatchString(name) {
		return ExtensionRoute{}, fmt.Errorf("extension name %q: lowercase letters, digits and hyphens, at most 32", name)
	}
	if !strings.HasPrefix(pathPrefix, "/") || !strings.HasSuffix(pathPrefix, "/") || strings.Count(pathPrefix, "/") < 3 ||
		strings.Contains(pathPrefix, "..") || strings.ContainsAny(pathPrefix, ":?#") {
		return ExtensionRoute{}, fmt.Errorf("extension %s: path_prefix %q must look like /v1/<name>/ (two segments or more, ending in /)", name, pathPrefix)
	}
	for _, reserved := range reservedExtensionPrefixes {
		if strings.HasPrefix(pathPrefix, reserved) || strings.HasPrefix(reserved, pathPrefix) {
			return ExtensionRoute{}, fmt.Errorf("extension %s: path_prefix %q overlaps loom's own route %s", name, pathPrefix, reserved)
		}
	}
	u, err := url.Parse(upstream)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ExtensionRoute{}, fmt.Errorf("extension %s: upstream %q must be an http(s) URL", name, upstream)
	}
	if !isLoopbackHost(u.Hostname()) {
		return ExtensionRoute{}, fmt.Errorf("extension %s: upstream %q must be on this machine (localhost or a loopback address)", name, upstream)
	}
	return ExtensionRoute{Name: name, PathPrefix: pathPrefix, Upstream: u, Secret: secret, AllowDevices: allowDevices}, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// AddExtensionRoute serves route on this gateway. Must be called before
// Start(); not safe for concurrent use. Overlapping prefixes are an error.
func (h *HTTPServer) AddExtensionRoute(route ExtensionRoute) error {
	if route.Upstream == nil {
		return errors.New("extension route needs an upstream")
	}
	for _, existing := range h.extensions {
		if strings.HasPrefix(route.PathPrefix, existing.PathPrefix) || strings.HasPrefix(existing.PathPrefix, route.PathPrefix) {
			return fmt.Errorf("extension %s: path_prefix %s overlaps extension %s (%s)", route.Name, route.PathPrefix, existing.Name, existing.PathPrefix)
		}
	}
	h.extensions = append(h.extensions, route)
	return nil
}

// extensionHandler authenticates each request and forwards it to the route's
// upstream with the caller's user ID.
func (h *HTTPServer) extensionHandler(route ExtensionRoute) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// The upstream's path, if any, is joined before the request's.
			pr.SetURL(route.Upstream)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			h.logger.Warn("Extension route upstream failed", zap.String("extension", route.Name), zap.Error(err))
			http.Error(w, "this service is not available right now", http.StatusBadGateway)
		},
	}
	method := ExtensionMethod(route.Name)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := h.authorizeHTTP(w, r, method)
		if !ok {
			return
		}
		out := r.Clone(ctx)
		// Credentials are for loom; the service learns only who is calling.
		out.Header.Del("Authorization")
		out.Header.Del("Cookie")
		for name := range out.Header {
			if strings.HasPrefix(strings.ToLower(name), "x-loom-") || strings.HasPrefix(strings.ToLower(name), "grpc-metadata-") ||
				strings.EqualFold(name, "x-user-id") {
				out.Header.Del(name)
			}
		}
		out.Header.Set(ExtensionUserIDHeader, appOwner(ctx))
		if route.Secret != "" {
			out.Header.Set(ExtensionSecretHeader, route.Secret)
		}
		proxy.ServeHTTP(w, out)
	})
}

// AllowMethod lets paired devices call fullMethod in addition to
// deviceAllowedMethods, e.g. an extension route (ExtensionMethod). Must be
// called before serving; not safe for concurrent use.
func (s *DeviceServer) AllowMethod(fullMethod string) {
	if s.extraAllowed == nil {
		s.extraAllowed = map[string]bool{}
	}
	s.extraAllowed[fullMethod] = true
}
