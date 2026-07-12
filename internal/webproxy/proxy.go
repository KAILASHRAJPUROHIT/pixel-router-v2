// Package webproxy fans a single public tunnel endpoint out to several
// backends by path prefix - pixel-router-v2's own dashboard at "/", plus
// AKD and local-router (both PC-hosted, reached over the phone's own LAN
// since it's the WiFi hotspot/AP for those machines) at "/akd" and
// "/local-router". This exists so all three projects can share the one
// free static domain the phone's always-on tunnel service holds, instead
// of each needing its own tunnel and its own domain.
package webproxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
)

// Route maps a URL path prefix to the backend base URL requests under
// that prefix should be reverse-proxied to, with the prefix stripped
// first (so "/akd/foo" reaches the AKD server as "/foo").
type Route struct {
	Prefix string
	Target string
}

// blockedPaths are never proxied, full stop, regardless of route -
// both are gated phone-only (requireLocalhost) on the webserver side,
// meant only for the on-device AOD app talking to webserver directly on
// its own port, never through this proxy. Confirmed live 2026-07-12 as
// a real vulnerability: forwarding them here made both PUBLICLY
// reachable with no auth at all, since every proxied request reaches
// webserver via 127.0.0.1 - indistinguishable, from webserver's own
// loopback-IP check, from a genuinely local request. The fix has to
// live here, at the outermost layer, since webserver structurally can't
// tell "proxied from the internet" apart from "actually local" once the
// request already arrives over loopback.
var blockedPaths = []string{"/healthz", "/internal/aod-stats"}

// NewHandler builds an http.Handler that dispatches by longest-matching
// route prefix, falling back to defaultTarget (pixel-router-v2's own
// dashboard) for anything that matches no route - this is what lets the
// existing dashboard keep working unmodified at "/" while the other two
// projects fan out from their own prefixes.
func NewHandler(routes []Route, defaultTarget string) (http.Handler, error) {
	type compiled struct {
		prefix string
		proxy  *httputil.ReverseProxy
	}

	compiledRoutes := make([]compiled, 0, len(routes))
	for _, rt := range routes {
		proxy, err := newProxy(rt.Prefix, rt.Target)
		if err != nil {
			return nil, fmt.Errorf("route %s -> %s: %w", rt.Prefix, rt.Target, err)
		}
		compiledRoutes = append(compiledRoutes, compiled{prefix: rt.Prefix, proxy: proxy})
	}
	// Longest prefix first, so a route like "/akd/admin" (if ever added)
	// would be checked before the more general "/akd".
	sort.Slice(compiledRoutes, func(i, j int) bool {
		return len(compiledRoutes[i].prefix) > len(compiledRoutes[j].prefix)
	})

	defaultProxy, err := newProxy("", defaultTarget)
	if err != nil {
		return nil, fmt.Errorf("default target %s: %w", defaultTarget, err)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, blocked := range blockedPaths {
			if r.URL.Path == blocked {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		for _, rt := range compiledRoutes {
			if r.URL.Path == rt.prefix || strings.HasPrefix(r.URL.Path, rt.prefix+"/") {
				rt.proxy.ServeHTTP(w, r)
				return
			}
		}
		defaultProxy.ServeHTTP(w, r)
	}), nil
}

func newProxy(prefix, target string) (*httputil.ReverseProxy, error) {
	targetURL, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(targetURL)
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, prefix)
			if !strings.HasPrefix(pr.Out.URL.Path, "/") {
				pr.Out.URL.Path = "/" + pr.Out.URL.Path
			}
			pr.SetXForwarded()
		},
	}, nil
}
