package webproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewHandler_BlocksHealthzAndAodStats_EvenThoughDefaultWouldServeThem(t *testing.T) {
	// Regression test for a real vulnerability confirmed live
	// 2026-07-12: these two paths must never reach the default
	// (or any) backend through this proxy, since webserver's own
	// requireLocalhost guard can't distinguish a proxied request from
	// a genuinely local one once it arrives over loopback.
	def := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should never see this"))
	}))
	defer def.Close()

	handler, err := NewHandler(nil, def.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	for _, path := range []string{"/healthz", "/internal/aod-stats"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: expected 403, got %d (body: %s)", path, resp.StatusCode, body)
		}
	}
}

func TestNewHandler_RoutesByPrefixAndStripsIt(t *testing.T) {
	akd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("akd saw path: " + r.URL.Path))
	}))
	defer akd.Close()

	def := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("default saw path: " + r.URL.Path))
	}))
	defer def.Close()

	handler, err := NewHandler([]Route{{Prefix: "/akd", Target: akd.URL}}, def.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/akd/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if got := string(body); got != "akd saw path: /dashboard" {
		t.Fatalf("got %q", got)
	}
}

func TestNewHandler_FallsBackToDefaultTarget(t *testing.T) {
	def := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("default saw path: " + r.URL.Path))
	}))
	defer def.Close()

	handler, err := NewHandler([]Route{{Prefix: "/akd", Target: "http://127.0.0.1:1"}}, def.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/dashboard.js")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if got := string(body); got != "default saw path: /dashboard.js" {
		t.Fatalf("got %q", got)
	}
}

func TestNewHandler_DoesNotMatchUnrelatedPrefix(t *testing.T) {
	// "/akdextra" must NOT be routed to the "/akd" route - only an exact
	// match or a "/"-bounded sub-path should count as a match.
	akd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("akd"))
	}))
	defer akd.Close()
	def := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("default"))
	}))
	defer def.Close()

	handler, err := NewHandler([]Route{{Prefix: "/akd", Target: akd.URL}}, def.URL)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/akdextra/thing")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if got := string(body); got != "default" {
		t.Fatalf("expected /akdextra to fall through to default, got %q", got)
	}
}
