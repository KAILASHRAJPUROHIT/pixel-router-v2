// Package webserver replaces V1's busybox httpd + 11 separately
// bolted-on CGI 2FA checks with one server that has session/auth/
// rate-limiting as a built-in default, not an afterthought. Every
// protected route goes through the same requireSession middleware -
// there is no way to add a new action route that forgets the 2FA
// check, unlike V1 where each of 11 scripts needed the check
// copy-pasted in individually.
package webserver

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/antriva/pixel-router-v2/internal/aodcontrol"
	"github.com/antriva/pixel-router-v2/internal/aodstats"
	"github.com/antriva/pixel-router-v2/internal/buildinfo"
	"github.com/antriva/pixel-router-v2/internal/managerchat"
	"github.com/antriva/pixel-router-v2/internal/smsviewer"
	"github.com/antriva/pixel-router-v2/internal/speedtest"
	"github.com/antriva/pixel-router-v2/internal/store"
	"github.com/antriva/pixel-router-v2/internal/totp"
)

const sessionCookieName = "router_session"
const sessionTTL = 12 * time.Hour

type Config struct {
	Username     string
	PasswordHash string // bcrypt hash
	TotpSecret   string // base32

	// Health-check callbacks, injected rather than imported directly -
	// keeps webserver decoupled from hotspot/usbtether's system-call
	// dependencies (and makes handleHealth testable with fakes). Nil
	// callbacks are reported as "unknown" rather than false, so a
	// caller that didn't wire one up doesn't show as falsely down.
	HotspotUp            func() bool
	USBTetherUp          func() bool
	SupervisorStatusPath string

	// AodSources feeds /internal/aod-stats, the on-device Android AOD
	// app's replacement for scraping V1's rendered HTML. Zero-value is
	// fine (Collect() reports "n/a"/safe defaults for anything not
	// wired up) - this lets the route exist and be exercised even
	// before every real collector is plugged in.
	AodSources aodstats.Sources

	// HotspotForceRestartPath, if set, is where handleHotspotRestart
	// writes a trigger file the hotspot service polls for on its own
	// check-interval - the same flat-file cross-process signaling
	// pattern already used elsewhere (e.g. failover_internet_type.txt),
	// since the webserver and hotspot are separate processes. Added
	// 2026-07-12 after a recurring incident where a connected laptop
	// kept losing internet every 15-90 minutes (root cause: the hotspot
	// process getting killed by Android's low-memory-killer, since
	// nothing protected it - see internal/supervisor's
	// protectFromLowMemoryKiller fix for the real underlying fix). This
	// button is a manual, on-demand escalation for whenever a client
	// still seems stuck despite that fix - restarting the AP radio
	// deauthenticates and reconnects every currently-connected client,
	// not just one, since there's no reliable per-client kick mechanism
	// on this device (no hostapd_cli binary present, and root's access
	// to hostapd's wifi-group-owned control socket wasn't confirmed
	// SELinux-safe to attempt).
	HotspotForceRestartPath string

	// FailoverInternetTypePath, if set, is read to report which uplink
	// (own cellular vs a borrowed WiFi network) is currently active -
	// the same state file the failover/vpntether services already
	// write/read (see internal/vpntether's doc comment).
	FailoverInternetTypePath string

	// TunnelStatusPath, if set, is read to report the WireGuard tunnel's
	// alive/dead/not_present status (written by cmd/vpntether). Added
	// 2026-07-12 porting V1's dashboard, which surfaced this after a
	// real incident where the tunnel silently died and nobody noticed
	// until a manual check caught it days later.
	TunnelStatusPath string

	// BlockDevice/UnblockDevice apply or remove the actual iptables
	// enforcement (see internal/blocklist) - injected as plain functions
	// rather than the concrete controller type, same pattern as
	// HotspotUp/USBTetherUp, so this package doesn't need to import
	// os/exec-touching code directly and stays unit-testable with fakes.
	BlockDevice   func(mac string) error
	UnblockDevice func(mac string) error

	// BorrowedSSIDPath, if set, is read alongside FailoverInternetTypePath
	// to name WHICH borrowed network is in use (e.g. "Dad's iPhone")
	// rather than just the generic "borrowed" - resolved via
	// ResolveBorrowedName against phone1/phone2's configured name+ssid.
	BorrowedSSIDPath string
	Phone1Name       string
	Phone1SSID       string
	Phone2Name       string
	Phone2SSID       string

	// RestartService signals the named supervised service's current
	// process to exit - the supervisor's own restart:always policy
	// relaunches it fresh, same mechanism as any organic crash-restart,
	// just triggered on demand instead of waiting for one.
	RestartService func(name string) error
	// Reboot restarts the whole device.
	Reboot func() error

	// FetchSMS reads the last N received SMS - injected so this
	// package doesn't need to import os/exec-touching code directly.
	FetchSMS func(limit int) ([]smsviewer.Message, error)

	// HotspotConfigPath is the same JSON file cmd/hotspot's own
	// loadConfig reads - this package can't import cmd/hotspot (it's a
	// main package), so the schema is duplicated in hotspotFileConfig
	// below; keep the two in sync if either changes.
	HotspotConfigPath string

	// AodPrefs applies a layout/theme index change to the already-
	// installed AOD app and forces a refresh - see internal/aodcontrol.
	AodPrefs aodcontrol.Controller

	// PCCommandPath is where a queued PC remote-control command
	// ({"command":...,"ts":...}) is written - there's no direct
	// phone->PC connectivity path (the PC's Tailscale IP approach V1
	// considered doesn't work here since WireGuard already occupies
	// this phone's one VpnService slot for the Airtel-bypass tunnel),
	// so instead a script on the PC POLLS this same file via
	// /pc-command.json, exactly mirroring V1's pc_command_watcher.py.
	PCCommandPath string
	// PCCommandKey gates the polling endpoint - the PC-side poller
	// can't complete an interactive TOTP login, so this is a static
	// shared secret (presented as ?key=...) rather than a session,
	// scoped to this one narrow read-only command channel.
	PCCommandKey string

	// RunSpeedtest performs a real on-demand throughput/latency
	// measurement - injected (see internal/speedtest) so this package
	// doesn't need to import the network-calling code directly.
	RunSpeedtest       func() (speedtest.Result, error)
	SpeedtestCachePath string

	// CustomMessagePath, if set, is where a timed custom message for
	// the AOD screen is stored (expiry epoch + text) - ports V1's
	// "Show on AOD" quick action.
	CustomMessagePath string
}

// ResolveBorrowedName names the currently-borrowed network after
// whichever configured phone's SSID matches, falling back to the raw
// SSID (or a generic "Borrowed" if that's empty too) - ports V1's
// dashboard behavior of naming the actual source ("Dad's iPhone")
// rather than a generic label. Pure function, split out for testing
// without needing real config files.
func ResolveBorrowedName(borrowedSSID, phone1Name, phone1SSID, phone2Name, phone2SSID string) string {
	switch {
	case borrowedSSID != "" && borrowedSSID == phone1SSID && phone1Name != "":
		return phone1Name
	case borrowedSSID != "" && borrowedSSID == phone2SSID && phone2Name != "":
		return phone2Name
	case borrowedSSID != "":
		return borrowedSSID
	default:
		return "Borrowed"
	}
}

type Server struct {
	cfg   Config
	store *store.Store
	mux   *http.ServeMux
}

func New(cfg Config, st *store.Store) *Server {
	s := &Server{cfg: cfg, store: st, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	s.mux.HandleFunc("/login", s.handleLogin)
	s.mux.HandleFunc("/api/status", s.requireSession(s.handleStatus))
	s.mux.HandleFunc("/api/devices", s.requireSession(s.handleDevices))
	s.mux.HandleFunc("/api/health", s.requireSession(s.handleHealth))
	s.mux.HandleFunc("/api/hotspot/restart", s.requireSession(s.handleHotspotRestart))
	s.mux.HandleFunc("/api/devices/rename", s.requireSession(s.handleDeviceRename))
	s.mux.HandleFunc("/api/devices/block", s.requireSession(s.handleDeviceBlock))
	s.mux.HandleFunc("/api/billing-day", s.requireSession(s.handleSetBillingDay))
	s.mux.HandleFunc("/api/service/restart", s.requireSession(s.handleServiceRestart))
	s.mux.HandleFunc("/api/system/reboot", s.requireSession(s.handleReboot))
	s.mux.HandleFunc("/api/sms", s.requireSession(s.handleSMS))
	s.mux.HandleFunc("/api/pc-command", s.requireSession(s.handleQueuePCCommand))
	s.mux.HandleFunc("/pc-command.json", s.handlePollPCCommand)
	s.mux.HandleFunc("/api/speedtest", s.requireSession(s.handleSpeedtest))
	s.mux.HandleFunc("/api/custom-message", s.requireSession(s.handleSetCustomMessage))
	s.mux.HandleFunc("/api/chat", s.requireSession(s.handleChat))
	s.mux.HandleFunc("/api/hotspot/band", s.requireSession(s.handleSetBand))
	s.mux.HandleFunc("/api/aod/layout", s.requireSession(s.handleSetAodLayout))
	s.mux.HandleFunc("/api/aod/theme", s.requireSession(s.handleSetAodTheme))
	s.mux.HandleFunc("/api/aod/options", s.requireSession(s.handleAodOptions))
	s.mux.HandleFunc("/internal/aod-stats", s.requireLocalhost(s.handleAodStats))
	s.mux.HandleFunc("/healthz", s.requireLocalhost(s.handleHealthz))
	s.mux.HandleFunc("/", s.requireSession(s.handleDashboard))
}

// requireLocalhost gates a route by the request genuinely originating
// from 127.0.0.1/::1, rather than the session/2FA middleware -
// appropriate specifically for the on-device AOD app, which polls
// automatically every few seconds and has no way to interactively
// enter a live Authy code. This route is never reachable through the
// Cloudflare tunnel or from any other device on the network, only from
// a process running on the phone itself.
func (s *Server) requireLocalhost(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func clientKey(r *http.Request) string {
	// RemoteAddr, not X-Forwarded-For - this server isn't meant to sit
	// behind an untrusted proxy that could spoof a rate-limit bypass.
	return r.RemoteAddr
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		renderLoginForm(w, "")
		return
	}

	ck := clientKey(r)
	lockedOut, err := s.store.IsLockedOut(ck)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if lockedOut {
		log.Printf("login: locked-out attempt from %s", ck)
		renderLoginForm(w, "Too many failed attempts. Try again in 15 minutes.")
		return
	}

	if err := r.ParseForm(); err != nil {
		renderLoginForm(w, "Bad request.")
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")
	code := r.FormValue("code")

	validCreds := username == s.cfg.Username &&
		bcrypt.CompareHashAndPassword([]byte(s.cfg.PasswordHash), []byte(password)) == nil

	// Accept either a live TOTP code or a one-time recovery code - the
	// latter exists specifically for the case Authy itself is
	// unavailable (device lost/reset), which had no recovery path at
	// all before this. Recovery codes are single-use and consumed on
	// successful login.
	validCode := false
	usedRecoveryCode := false
	if validCreds {
		if totp.Verify(s.cfg.TotpSecret, code, time.Now().Unix()) {
			validCode = true
		} else if ok, _ := s.store.CheckAndConsumeRecoveryCode(code); ok {
			validCode = true
			usedRecoveryCode = true
		}
	}

	if !validCreds || !validCode {
		count, _ := s.store.RecordFailedAttempt(ck)
		log.Printf("login: failed attempt from %s (count=%d)", ck, count)
		renderLoginForm(w, "Incorrect username, password, or code.")
		return
	}

	if usedRecoveryCode {
		remaining, _ := s.store.RemainingRecoveryCodes()
		log.Printf("login: successful login from %s using a RECOVERY CODE (%d remaining)", ck, remaining)
	}

	s.store.ClearFailedAttempts(ck)
	token, err := randomToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.store.CreateSession(token, sessionTTL); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	log.Printf("login: successful login from %s", ck)

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		// Secure: the dashboard is only ever reached over HTTPS (both
		// the ngrok and Cloudflare tunnels terminate TLS at their edge)
		// - marking this Secure costs nothing and stops the browser
		// from ever sending it over a downgraded/plain-HTTP connection.
		// SameSite=Strict: this dashboard has several real
		// state-changing POST endpoints (reboot, restart-service,
		// block-device, run-speedtest, PC-command) with no separate
		// CSRF token - the browser's own SameSite enforcement is the
		// actual defense against a malicious external page silently
		// submitting one of those requests using an already-logged-in
		// session. There's no legitimate cross-site linking use case
		// for an admin dashboard, so Strict (not just Lax) is safe.
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || !s.store.IsSessionValid(cookie.Value) {
			http.Error(w, "authentication required - go to /login", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

type triState string

const (
	triUp      triState = "up"
	triDown    triState = "down"
	triUnknown triState = "unknown"
)

func fromBoolFn(fn func() bool) triState {
	if fn == nil {
		return triUnknown
	}
	if fn() {
		return triUp
	}
	return triDown
}

type healthJSON struct {
	HotspotUp         triState                   `json:"hotspot_up"`
	USBTetherUp       triState                   `json:"usb_tether_up"`
	DeviceCount       int                        `json:"device_count"`
	QuotaCutoffActive bool                       `json:"quota_cutoff_active"`
	QuotaUsedGB       float64                    `json:"quota_used_gb"`
	QuotaBillingDay   int                        `json:"quota_billing_day"`
	Services          map[string]json.RawMessage `json:"services,omitempty"`
	// DetailStats reuses the exact same 10-stat contract already
	// collected for the on-device AOD app (Down/Up Mbps, Battery,
	// Thermal, Skin Temp, SMS Fwd, Internet, Signal) - added 2026-07-12
	// after noticing all of this was already being gathered server-side
	// but never surfaced to the human web dashboard, only the phone's
	// lock-screen display.
	DetailStats []aodstats.Stat `json:"detail_stats,omitempty"`
	// InternetType is "self" (own cellular) or "borrowed" (someone
	// else's WiFi) - the same state failover/vpntether already track,
	// surfaced here since it's directly relevant to what a user sees
	// when diagnosing a connectivity issue.
	InternetType string `json:"internet_type,omitempty"`
	// TunnelStatus is "alive", "dead", "not_present", or "" if the
	// status file hasn't been read/written yet.
	TunnelStatus string `json:"tunnel_status,omitempty"`
	Version      string `json:"version"`
	// SpeedtestCache is the raw last-cached speed test JSON (already in
	// the shape the dashboard wants), or empty if none has run yet.
	SpeedtestCache json.RawMessage `json:"speedtest_cache,omitempty"`
	// CustomMessage is only populated if a message is currently active
	// (not expired) - see Config.CustomMessagePath.
	CustomMessage string `json:"custom_message,omitempty"`
	// Band2GHzEnabled reflects the hotspot config's current band mode -
	// see hotspotFileConfig's doc comment.
	Band2GHzEnabled bool `json:"band_2ghz_enabled"`
	// AodLayoutIndex/AodThemeIndex reflect the AOD app's currently
	// active selection, -1 if unreadable (app never run / prefs missing).
	AodLayoutIndex int `json:"aod_layout_index"`
	AodThemeIndex  int `json:"aod_theme_index"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	h := healthJSON{
		HotspotUp:      fromBoolFn(s.cfg.HotspotUp),
		USBTetherUp:    fromBoolFn(s.cfg.USBTetherUp),
		Version:        buildinfo.Version,
		AodLayoutIndex: -1,
		AodThemeIndex:  -1,
	}

	if s.cfg.AodPrefs != nil {
		if xml, err := s.cfg.AodPrefs.ReadPrefs(); err == nil {
			if idx, ok := aodcontrol.ParseLayoutIndex(xml); ok {
				h.AodLayoutIndex = idx
			}
			if idx, ok := aodcontrol.ParseThemeIndex(xml); ok {
				h.AodThemeIndex = idx
			}
		}
	}

	if devices, err := s.store.ListDevices(); err == nil {
		now := time.Now()
		for _, d := range devices {
			if now.Sub(d.LastSeen) < OnlineThreshold {
				h.DeviceCount++
			}
		}
	}

	if bc, err := s.store.GetBillingCycle(); err == nil {
		h.QuotaCutoffActive = bc.CutoffActive
		h.QuotaUsedGB = float64(bc.CycleBytes) / 1_000_000_000
		h.QuotaBillingDay = bc.BillingDay
	}

	if s.cfg.SupervisorStatusPath != "" {
		if data, err := os.ReadFile(s.cfg.SupervisorStatusPath); err == nil {
			var services map[string]json.RawMessage
			if json.Unmarshal(data, &services) == nil {
				h.Services = services
			}
		}
	}

	h.DetailStats = aodstats.Collect(s.cfg.AodSources)

	if s.cfg.FailoverInternetTypePath != "" {
		if data, err := os.ReadFile(s.cfg.FailoverInternetTypePath); err == nil {
			h.InternetType = strings.TrimSpace(string(data))
		}
	}
	if h.InternetType == "borrowed" && s.cfg.BorrowedSSIDPath != "" {
		ssid := ""
		if data, err := os.ReadFile(s.cfg.BorrowedSSIDPath); err == nil {
			ssid = strings.TrimSpace(string(data))
		}
		h.InternetType = ResolveBorrowedName(ssid, s.cfg.Phone1Name, s.cfg.Phone1SSID, s.cfg.Phone2Name, s.cfg.Phone2SSID)
	}

	if s.cfg.TunnelStatusPath != "" {
		if data, err := os.ReadFile(s.cfg.TunnelStatusPath); err == nil {
			h.TunnelStatus = strings.TrimSpace(string(data))
		}
	}

	if s.cfg.HotspotConfigPath != "" {
		if data, err := os.ReadFile(s.cfg.HotspotConfigPath); err == nil {
			var fc hotspotFileConfig
			if json.Unmarshal(data, &fc) == nil {
				h.Band2GHzEnabled = fc.Band2GHzEnabled
			}
		}
	}

	if s.cfg.SpeedtestCachePath != "" {
		if data, err := os.ReadFile(s.cfg.SpeedtestCachePath); err == nil {
			h.SpeedtestCache = json.RawMessage(data)
		}
	}

	if s.cfg.CustomMessagePath != "" {
		if data, err := os.ReadFile(s.cfg.CustomMessagePath); err == nil {
			lines := strings.SplitN(string(data), "\n", 2)
			if len(lines) == 2 {
				if expiry, err := strconv.ParseInt(lines[0], 10, 64); err == nil && time.Now().Unix() < expiry {
					h.CustomMessage = lines[1]
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h)
}

type healthzServiceJSON struct {
	State        string `json:"state"`
	RestartCount int    `json:"restart_count"`
}

type healthzJSON struct {
	OK       bool                          `json:"ok"`
	Version  string                        `json:"version"`
	Services map[string]healthzServiceJSON `json:"services"`
}

// handleHealthz is a plain machine-readable liveness/readiness probe -
// distinct from /api/health (which is session-gated and feeds the
// human dashboard's cards). "ok" is true only if every service the
// supervisor knows about currently reports "running"; a service
// missing from the status file entirely (supervisor hasn't started it
// yet, or the file couldn't be read) also counts as not-ok, since
// either case means something isn't confirmed healthy.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	resp := healthzJSON{Version: buildinfo.Version, Services: map[string]healthzServiceJSON{}}

	data, err := os.ReadFile(s.cfg.SupervisorStatusPath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(resp)
		return
	}

	var raw map[string]struct {
		State        string `json:"state"`
		RestartCount int    `json:"restart_count"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || len(raw) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(resp)
		return
	}

	resp.OK = true
	for name, svc := range raw {
		resp.Services[name] = healthzServiceJSON{State: svc.State, RestartCount: svc.RestartCount}
		if svc.State != "running" {
			resp.OK = false
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if !resp.OK {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(resp)
}

type deviceJSON struct {
	MAC             string `json:"mac"`
	Name            string `json:"name"`
	Band            string `json:"band"`
	CumulativeBytes int64  `json:"cumulative_bytes"`
	FirstSeen       string `json:"first_seen"`
	LastSeen        string `json:"last_seen"`
	// Online is computed here, not stored - devicescan only ever
	// updates LastSeen for currently-live clients (see its own doc
	// comment), it never marks a row "offline". Confirmed live
	// 2026-07-12 as a genuine point of confusion: the device list kept
	// every device ever seen (pruned only after 7 days) with no way to
	// tell "connected right now" from "connected hours ago", making a
	// normal historical record look like a room full of unexpected
	// devices. onlineThreshold gives a few missed scan cycles of buffer
	// (devicescan's own check-interval is 30s) before flipping to false.
	Online  bool `json:"online"`
	Blocked bool `json:"blocked"`
}

// OnlineThreshold is exported so callers building the shared
// DeviceCount source (used by both this dashboard and the on-device
// AOD screen, via cmd/webserver/main.go) can count only currently-
// online devices with the same definition of "online" used here.
const OnlineThreshold = 90 * time.Second

func (s *Server) handleAodStats(w http.ResponseWriter, r *http.Request) {
	stats := aodstats.Collect(s.cfg.AodSources)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// handleHotspotRestart lets an authenticated dashboard user force an
// immediate hotspot AP restart on demand, rather than waiting on the
// service's own automatic down-detection. Writes a trigger file rather
// than restarting anything directly - the webserver and hotspot are
// separate supervised processes, so this is the same flat-file
// cross-process signal already used elsewhere in this codebase.
func (s *Server) handleHotspotRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.HotspotForceRestartPath == "" {
		http.Error(w, "not configured", http.StatusServiceUnavailable)
		return
	}
	if err := os.WriteFile(s.cfg.HotspotForceRestartPath, []byte(time.Now().UTC().Format(time.RFC3339)), 0644); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	log.Printf("hotspot force-restart requested via dashboard by %s", clientKey(r))
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true,"detail":"restart requested - every connected device will briefly reconnect"}`))
}

// handleDeviceRename lets an authenticated user give a device a
// friendly name - store.SetDeviceName already existed (built alongside
// the rest of the per-device tracking) but nothing ever called it until
// this endpoint, confirmed live 2026-07-12 as a real point of confusion
// (every device showing as "Unknown device").
func (s *Server) handleDeviceRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	mac := strings.ToLower(strings.TrimSpace(r.FormValue("mac")))
	name := strings.TrimSpace(r.FormValue("name"))
	if mac == "" {
		http.Error(w, "mac is required", http.StatusBadRequest)
		return
	}
	if len(name) > 60 {
		name = name[:60]
	}
	if err := s.store.SetDeviceName(mac, name); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// handleDeviceBlock toggles a device's block state - persists the
// intent via store.SetDeviceBlocked (so it survives a reboot) and
// applies/removes the actual iptables enforcement immediately via the
// injected BlockDevice/UnblockDevice functions.
func (s *Server) handleDeviceBlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	mac := strings.ToLower(strings.TrimSpace(r.FormValue("mac")))
	action := r.FormValue("action")
	if mac == "" || (action != "block" && action != "unblock") {
		http.Error(w, "mac and action=block|unblock are required", http.StatusBadRequest)
		return
	}
	blocked := action == "block"

	if err := s.store.SetDeviceBlocked(mac, blocked); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var applyErr error
	if blocked && s.cfg.BlockDevice != nil {
		applyErr = s.cfg.BlockDevice(mac)
	} else if !blocked && s.cfg.UnblockDevice != nil {
		applyErr = s.cfg.UnblockDevice(mac)
	}
	if applyErr != nil {
		http.Error(w, "blocked flag saved but enforcement failed: "+applyErr.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("device %s %s via dashboard by %s", mac, action, clientKey(r))
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// handleSetBillingDay changes which day-of-month the cellular quota
// cycle resets on - store.SetBillingDay already existed (the quota
// service reads it every check), just never had a UI to change it from.
func (s *Server) handleSetBillingDay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	day, err := strconv.Atoi(r.FormValue("day"))
	if err != nil || day < 1 || day > 28 {
		http.Error(w, "day must be an integer 1-28", http.StatusBadRequest)
		return
	}
	if err := s.store.SetBillingDay(day); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	log.Printf("billing day set to %d via dashboard by %s", day, clientKey(r))
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// handleServiceRestart signals the named service to exit on demand -
// ports V1's per-service "Restart X" buttons. Only services the
// supervisor itself currently knows about (i.e. already appear in its
// status file) can be targeted - that file is the natural whitelist,
// since RestartService looks the name up there for its PID.
func (s *Server) handleServiceRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("service"))
	if name == "" {
		http.Error(w, "service is required", http.StatusBadRequest)
		return
	}
	if s.cfg.RestartService == nil {
		http.Error(w, "not configured", http.StatusServiceUnavailable)
		return
	}
	if err := s.cfg.RestartService(name); err != nil {
		http.Error(w, "restart failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("service %s restart requested via dashboard by %s", name, clientKey(r))
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// handleReboot restarts the whole device - the most disruptive action
// on the dashboard, hence POST-only and requiring the same session/2FA
// gate as everything else here (no separate confirmation step
// server-side; the dashboard's own confirm() dialog is the guard).
func (s *Server) handleReboot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.Reboot == nil {
		http.Error(w, "not configured", http.StatusServiceUnavailable)
		return
	}
	log.Printf("device reboot requested via dashboard by %s", clientKey(r))
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true,"detail":"rebooting now - the dashboard will be unreachable for 1-2 minutes"}`))
	go s.cfg.Reboot()
}

type smsJSON struct {
	Address string `json:"address"`
	Body    string `json:"body"`
	DateMs  int64  `json:"date_ms"`
}

// handleSMS returns the last 15 received messages - the dashboard's own
// JS groups them by sender and applies the category filter tabs
// (V1 did this same grouping/filtering server-side in awk; doing it in
// the browser instead means this endpoint stays a plain data source).
func (s *Server) handleSMS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.cfg.FetchSMS == nil {
		json.NewEncoder(w).Encode([]smsJSON{})
		return
	}
	msgs, err := s.cfg.FetchSMS(15)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]smsJSON, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, smsJSON{Address: m.Address, Body: m.Body, DateMs: m.DateMs})
	}
	json.NewEncoder(w).Encode(out)
}

// handleQueuePCCommand lets an authenticated dashboard user queue a
// remote-control command for the PC-side poller to pick up.
func (s *Server) handleQueuePCCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	command := r.FormValue("command")
	if command != "restart_rustdesk" && command != "restart_pc" {
		http.Error(w, "unknown command", http.StatusBadRequest)
		return
	}
	if s.cfg.PCCommandPath == "" {
		http.Error(w, "not configured", http.StatusServiceUnavailable)
		return
	}
	payload := fmt.Sprintf(`{"command":%q,"ts":%d}`, command, time.Now().Unix())
	if err := os.WriteFile(s.cfg.PCCommandPath, []byte(payload), 0644); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	log.Printf("PC command %q queued via dashboard by %s", command, clientKey(r))
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// handlePollPCCommand is what the PC-side poller actually fetches -
// gated by a static shared key (see Config.PCCommandKey's doc comment
// for why this can't be a normal TOTP-backed session) rather than
// requireSession. Uses a constant-time comparison - found during a
// security audit alongside the same issue in TOTP verification (see
// internal/totp's Verify) - a plain `!=` on a secret invites a timing
// attack.
func (s *Server) handlePollPCCommand(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if s.cfg.PCCommandKey == "" || subtle.ConstantTimeCompare([]byte(key), []byte(s.cfg.PCCommandKey)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	data, err := os.ReadFile(s.cfg.PCCommandPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// handleSpeedtest runs a real throughput/latency test on demand (costs
// real cellular data, hence on-demand-only, never scheduled) and caches
// the result so a page reload shows the last known reading immediately
// rather than blank until clicked again.
func (s *Server) handleSpeedtest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.RunSpeedtest == nil {
		http.Error(w, "not configured", http.StatusServiceUnavailable)
		return
	}
	res, err := s.cfg.RunSpeedtest()
	if err != nil {
		http.Error(w, "speed test failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if s.cfg.SpeedtestCachePath != "" {
		payload := fmt.Sprintf(`{"down":%.2f,"up":%.2f,"ping":%.0f,"ts":%d}`,
			res.DownMbps, res.UpMbps, res.PingMs, time.Now().Unix())
		os.WriteFile(s.cfg.SpeedtestCachePath, []byte(payload), 0644)
	}
	log.Printf("speed test run via dashboard by %s: down=%.1f up=%.1f ping=%.0f", clientKey(r), res.DownMbps, res.UpMbps, res.PingMs)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]float64{"down": res.DownMbps, "up": res.UpMbps, "ping": res.PingMs})
}

// handleSetCustomMessage ports V1's "Show on AOD" quick action - posts
// a timed message that expires on its own rather than needing to be
// manually cleared.
func (s *Server) handleSetCustomMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	message := strings.TrimSpace(r.FormValue("message"))
	durationMin, err := strconv.Atoi(r.FormValue("duration_min"))
	if err != nil || durationMin < 1 || durationMin > 1440 {
		http.Error(w, "duration_min must be an integer 1-1440", http.StatusBadRequest)
		return
	}
	if s.cfg.CustomMessagePath == "" {
		http.Error(w, "not configured", http.StatusServiceUnavailable)
		return
	}
	expiry := time.Now().Add(time.Duration(durationMin) * time.Minute).Unix()
	payload := fmt.Sprintf("%d\n%s", expiry, message)
	if err := os.WriteFile(s.cfg.CustomMessagePath, []byte(payload), 0644); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	log.Printf("custom AOD message set via dashboard by %s (expires in %dm)", clientKey(r), durationMin)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// handleChat ports V1's "Chat with the Manager" - explicitly NOT a
// language model (see internal/managerchat's own doc comment); this
// matches keywords against the router's real, already-collected state.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	message := r.FormValue("message")
	reply := managerchat.Answer(message, s.buildManagerChatContext())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"reply": reply})
}

// buildManagerChatContext assembles managerchat.Context from the same
// live sources handleHealth already reads - the "ok" helper treats a
// stat's absence or "n/a" reading as fine (nothing to flag) rather than
// as a problem, since a not-yet-wired sensor shouldn't itself count as
// a health issue.
func (s *Server) buildManagerChatContext() managerchat.Context {
	stats := aodstats.Collect(s.cfg.AodSources)
	byLabel := make(map[string]aodstats.Stat, len(stats))
	for _, st := range stats {
		byLabel[st.Label] = st
	}
	ok := func(label string) bool {
		st, found := byLabel[label]
		if !found || st.Value == "n/a" {
			return true
		}
		return st.Color == "" || st.Color == "#3ecf8e"
	}

	onlineCount := 0
	if devices, err := s.store.ListDevices(); err == nil {
		now := time.Now()
		for _, d := range devices {
			if now.Sub(d.LastSeen) < OnlineThreshold {
				onlineCount++
			}
		}
	}

	bc, _ := s.store.GetBillingCycle()

	tunnelStatus := ""
	if s.cfg.TunnelStatusPath != "" {
		if data, err := os.ReadFile(s.cfg.TunnelStatusPath); err == nil {
			tunnelStatus = strings.TrimSpace(string(data))
		}
	}

	internetType := ""
	if s.cfg.FailoverInternetTypePath != "" {
		if data, err := os.ReadFile(s.cfg.FailoverInternetTypePath); err == nil {
			internetType = strings.TrimSpace(string(data))
		}
	}

	services := map[string]managerchat.ServiceStatus{}
	if s.cfg.SupervisorStatusPath != "" {
		if data, err := os.ReadFile(s.cfg.SupervisorStatusPath); err == nil {
			var raw map[string]struct {
				State        string `json:"state"`
				RestartCount int    `json:"restart_count"`
			}
			if json.Unmarshal(data, &raw) == nil {
				for name, svc := range raw {
					services[name] = managerchat.ServiceStatus{State: svc.State, RestartCount: svc.RestartCount}
				}
			}
		}
	}

	return managerchat.Context{
		HotspotUp:         s.cfg.HotspotUp != nil && s.cfg.HotspotUp(),
		InternetUp:        ok("Internet"),
		SMSForwarderOK:    ok("SMS Fwd"),
		ThermalOK:         ok("Thermal"),
		SignalOK:          ok("Signal"),
		InternetType:      internetType,
		TunnelStatus:      tunnelStatus,
		QuotaUsedGB:       float64(bc.CycleBytes) / 1_000_000_000,
		QuotaCutoffActive: bc.CutoffActive,
		OnlineDeviceCount: onlineCount,
		Services:          services,
	}
}

// hotspotFileConfig mirrors cmd/hotspot's own fileConfig schema - see
// that package's doc comment for why 2.4GHz is a full band-mode toggle
// (5GHz-only day-to-day vs bridged dual-band on demand) rather than two
// simultaneously-named networks, which this exact hardware can't do.
type hotspotFileConfig struct {
	SSID5GhzOnly    string   `json:"ssid_5ghz_only"`
	SSIDDualBand    string   `json:"ssid_dual_band"`
	Password        string   `json:"password"`
	Band2GHzEnabled bool     `json:"band_2ghz_enabled"`
	Deny2GhzMACs    []string `json:"deny_2ghz_macs"`
}

// handleSetBand toggles whether the 2.4GHz radio is enabled (bridged
// dual-band) or off (5GHz-only) - applying this REQUIRES a hotspot
// service restart, which briefly deauthenticates every connected
// client (same disruptive side effect as the "Force reconnect all
// devices" button), so the dashboard's own confirm() dialog is the
// only gate before this fires.
func (s *Server) handleSetBand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	action := r.FormValue("action")
	if action != "enable_2ghz" && action != "disable_2ghz" {
		http.Error(w, "action must be enable_2ghz or disable_2ghz", http.StatusBadRequest)
		return
	}
	if s.cfg.HotspotConfigPath == "" {
		http.Error(w, "not configured", http.StatusServiceUnavailable)
		return
	}

	data, err := os.ReadFile(s.cfg.HotspotConfigPath)
	if err != nil {
		http.Error(w, "internal error reading hotspot config", http.StatusInternalServerError)
		return
	}
	var fc hotspotFileConfig
	if err := json.Unmarshal(data, &fc); err != nil {
		http.Error(w, "internal error parsing hotspot config", http.StatusInternalServerError)
		return
	}
	fc.Band2GHzEnabled = action == "enable_2ghz"

	newData, err := json.MarshalIndent(fc, "", "  ")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// 0600, not 0644 - this file contains the real WiFi password in
	// plaintext (confirmed live 2026-07-12 as a real gap: an earlier
	// manual migration left it world-readable, inconsistent with every
	// other secret file in this directory, which are all 0600).
	if err := os.WriteFile(s.cfg.HotspotConfigPath, newData, 0600); err != nil {
		http.Error(w, "internal error writing hotspot config", http.StatusInternalServerError)
		return
	}

	if s.cfg.RestartService == nil {
		http.Error(w, "config saved but restart not configured", http.StatusInternalServerError)
		return
	}
	if err := s.cfg.RestartService("hotspot"); err != nil {
		http.Error(w, "config saved but restart failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("hotspot band mode set to %s via dashboard by %s", action, clientKey(r))
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true,"detail":"hotspot restarting with the new band - every connected device will briefly reconnect"}`))
}

// handleSetAodLayout/handleSetAodTheme port V1's set_aod_layout.cgi/
// set_aod_theme.cgi - edit the already-installed AOD app's own
// shared_prefs XML directly and force a fresh render, no app rebuild
// involved (see internal/aodcontrol's doc comment).
func (s *Server) handleSetAodLayout(w http.ResponseWriter, r *http.Request) {
	s.handleSetAodIndex(w, r, "layout", len(aodcontrol.LayoutNames), aodcontrol.PatchLayoutIndex)
}

func (s *Server) handleSetAodTheme(w http.ResponseWriter, r *http.Request) {
	s.handleSetAodIndex(w, r, "theme", len(aodcontrol.ThemeNames), aodcontrol.PatchThemeIndex)
}

func (s *Server) handleSetAodIndex(w http.ResponseWriter, r *http.Request, formField string, count int, patch func(string, int) string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	idx, err := strconv.Atoi(r.FormValue(formField))
	if err != nil || idx < 0 || idx >= count {
		http.Error(w, fmt.Sprintf("%s must be an integer 0-%d", formField, count-1), http.StatusBadRequest)
		return
	}
	if s.cfg.AodPrefs == nil {
		http.Error(w, "not configured", http.StatusServiceUnavailable)
		return
	}
	xml, err := s.cfg.AodPrefs.ReadPrefs()
	if err != nil {
		http.Error(w, "AOD app prefs not found - is it installed and has it run at least once?", http.StatusInternalServerError)
		return
	}
	if err := s.cfg.AodPrefs.WritePrefs(patch(xml, idx)); err != nil {
		http.Error(w, "internal error writing prefs", http.StatusInternalServerError)
		return
	}
	if err := s.cfg.AodPrefs.Refresh(); err != nil {
		http.Error(w, "prefs saved but refresh failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("AOD %s set to %d via dashboard by %s", formField, idx, clientKey(r))
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (s *Server) handleAodOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string][]string{
		"layouts": aodcontrol.LayoutNames,
		"themes":  aodcontrol.ThemeNames,
	})
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := s.store.ListDevices()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	now := time.Now()
	out := make([]deviceJSON, 0, len(devices))
	for _, d := range devices {
		out = append(out, deviceJSON{
			MAC: d.MAC, Name: d.Name, Band: d.Band, CumulativeBytes: d.CumulativeBytes,
			FirstSeen: d.FirstSeen.UTC().Format(time.RFC3339),
			LastSeen:  d.LastSeen.UTC().Format(time.RFC3339),
			Online:    now.Sub(d.LastSeen) < OnlineThreshold,
			Blocked:   d.Blocked,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online // online devices first
		}
		return out[i].LastSeen > out[j].LastSeen // most-recently-seen next
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(dashboardHTML))
}

const dashboardHTML = `<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Router Dashboard</title>
<style>
:root{
  --bg:#0b0f14; --panel:#11161d; --panel-2:#151b24; --border:#232b36;
  --text:#e6edf5; --text-dim:#8b98a8; --text-faint:#5b6779;
  --accent:#4fc3d9;
  --good:#3ecf8e; --good-bg:#122a20; --good-border:#215c40;
  --warn:#f5a623; --warn-bg:#2a2114; --warn-border:#5c4a25;
  --crit:#ff5468; --crit-bg:#2a1418; --crit-border:#5c2530;
  --mono: ui-monospace, "SF Mono", Consolas, monospace;
}
*{box-sizing:border-box}
body{background:var(--bg);color:var(--text);font-family:-apple-system,Segoe UI,Roboto,sans-serif;
     margin:0;padding:24px 16px 60px;max-width:820px;margin-left:auto;margin-right:auto}
h1{font-size:1.3rem;margin:0 0 4px}
.subtitle{color:var(--text-dim);font-size:0.85rem;margin-bottom:20px}
.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(160px,1fr));gap:10px;margin-bottom:24px}
.card{background:var(--panel);border:1px solid var(--border);border-radius:10px;padding:14px 16px}
.card .label{font-size:0.7rem;text-transform:uppercase;letter-spacing:0.05em;color:var(--text-faint);font-weight:600}
.card .value{font-size:1.3rem;font-weight:700;margin-top:4px}
.pill{display:inline-block;font-size:0.7rem;font-weight:700;padding:2px 8px;border-radius:999px;text-transform:uppercase}
.pill.up{background:var(--good-bg);color:var(--good);border:1px solid var(--good-border)}
.pill.down{background:var(--crit-bg);color:var(--crit);border:1px solid var(--crit-border)}
.pill.unknown{background:var(--warn-bg);color:var(--warn);border:1px solid var(--warn-border)}
.pill.offline{background:var(--panel-2);color:var(--text-faint);border:1px solid var(--border)}
.devices-summary{color:var(--text-dim);font-size:0.8rem;margin-bottom:10px}
.rename-input{background:var(--panel-2);border:1px solid var(--border);border-radius:6px;color:var(--text);
     padding:4px 8px;font-size:0.85rem;width:130px;font-family:inherit}
.rename-btn{padding:4px 10px;font-size:0.78rem}
.quota-bar-track{background:var(--panel-2);border-radius:999px;height:6px;margin-top:8px;overflow:hidden}
.quota-bar-fill{height:100%;border-radius:999px;background:var(--accent)}
.billing-row{display:flex;align-items:center;gap:8px;font-size:0.85rem;color:var(--text-dim);margin-bottom:20px}
.billing-row select{background:var(--panel-2);color:var(--text);border:1px solid var(--border);border-radius:6px;padding:5px 8px}
.sms-tabs{display:flex;gap:6px;flex-wrap:wrap;margin-bottom:12px}
.sms-tab{background:var(--panel-2);border:1px solid var(--border);border-radius:999px;padding:5px 13px;font-size:0.78rem;font-weight:700;cursor:pointer;color:var(--text-faint)}
.sms-tab.active{background:var(--accent);color:#04222b;border-color:var(--accent)}
.sms-item{background:var(--panel-2);border-radius:10px;padding:10px 14px;margin-bottom:6px}
.sms-item .meta{display:flex;justify-content:space-between;color:var(--text-faint);font-size:0.78rem}
.sms-item .body{margin-top:4px;font-size:0.88rem;word-break:break-word}
.sender-title{margin:14px 0 6px;font-size:0.9rem;font-weight:700}
.chat-window{background:var(--bg);border:1px solid var(--border);border-radius:10px;padding:14px;max-height:280px;overflow-y:auto;margin-bottom:10px}
.chat-msg{max-width:78%;padding:9px 13px;border-radius:12px;margin-bottom:8px;font-size:0.86rem;line-height:1.4}
.chat-user{background:var(--accent);color:#04222b;margin-left:auto;border-bottom-right-radius:3px}
.chat-manager{background:var(--panel-2);color:var(--text);margin-right:auto;border-bottom-left-radius:3px}
.chat-input-row{display:flex;gap:8px}
.chat-input-row input{flex:1;background:var(--panel-2);border:1px solid var(--border);border-radius:8px;color:var(--text);padding:9px 12px}
.aod-row{display:grid;grid-template-columns:220px 1fr;gap:16px;margin-bottom:24px}
@media (max-width:600px){.aod-row{grid-template-columns:1fr}}
.phone-frame{background:#06070c;border:1px solid var(--border);border-radius:20px;padding:18px 16px;color:#e6edf5}
.phone-time{font-size:1.5rem;font-weight:700;text-align:center}
.phone-date{text-align:center;color:#8b98a8;font-size:0.7rem;margin-top:2px}
.phone-rows{display:grid;grid-template-columns:1fr 1fr;gap:10px 14px;border-top:1px solid #232b36;padding-top:12px;margin-top:12px}
.phone-rows .lbl{font-size:0.6rem;color:#8b98a8;letter-spacing:0.04em;text-transform:uppercase}
.phone-rows .val{font-size:0.95rem;font-weight:700;margin-top:2px}
.aod-controls{display:flex;flex-direction:column;gap:6px}
.aod-controls label{font-size:0.72rem;color:var(--text-faint);text-transform:uppercase;letter-spacing:0.04em}
.aod-controls select{background:var(--panel-2);border:1px solid var(--border);border-radius:8px;color:var(--text);padding:8px 10px}
h2{font-size:0.95rem;margin:24px 0 10px;color:var(--text-dim);text-transform:uppercase;letter-spacing:0.05em}
table{width:100%;border-collapse:collapse;font-size:0.88rem}
th{text-align:left;color:var(--text-faint);font-size:0.72rem;text-transform:uppercase;padding:6px 10px;border-bottom:1px solid var(--border)}
td{padding:8px 10px;border-bottom:1px solid var(--border);font-family:var(--mono);font-size:0.85rem}
.loading{color:var(--text-faint);font-size:0.85rem}
.actions{margin-bottom:24px}
button.action{background:var(--panel-2);color:var(--text);border:1px solid var(--border);border-radius:8px;
     padding:10px 16px;font-size:0.85rem;font-weight:600;cursor:pointer}
button.action:hover{border-color:var(--accent)}
button.action:disabled{opacity:0.5;cursor:default}
.action-msg{font-size:0.8rem;margin-top:8px;color:var(--text-dim)}
</style></head><body>
<div style="display:flex;align-items:baseline;justify-content:space-between;flex-wrap:wrap;gap:8px">
  <h1>Pixel Router V2</h1>
  <div id="clock" style="font-size:1.3rem;font-weight:700;font-family:var(--mono)"></div>
</div>
<div class="subtitle" id="subtitle">Live status</div>
<div id="cards" class="grid"><div class="loading">Loading...</div></div>
<h2>AOD preview &amp; customization</h2>
<div class="aod-row">
  <div class="phone-frame" id="aod-preview">
    <div class="loading">Loading...</div>
  </div>
  <div class="aod-controls">
    <label>Layout</label>
    <select id="aod-layout-select"></select>
    <button class="action" onclick="setAodLayout()">Apply layout</button>
    <label style="margin-top:14px">Theme</label>
    <select id="aod-theme-select"></select>
    <button class="action" onclick="setAodTheme()">Apply theme</button>
    <div class="action-msg" id="aod-msg"></div>
  </div>
</div>
<div id="quota-bar-wrap" style="margin-bottom:20px"></div>
<div class="billing-row" id="billing-row"></div>
<h2>Signal &amp; performance</h2>
<div id="detail-cards" class="grid"><div class="loading">Loading...</div></div>
<div class="actions">
  <button class="action" id="restart-hotspot-btn" onclick="restartHotspot()">Force reconnect all devices</button>
  <button class="action" onclick="restartService('dreamwatchdog')">Restart AOD</button>
  <button class="action" onclick="restartService('failover')">Restart Failover</button>
  <button class="action" onclick="restartService('webserver')">Restart Dashboard</button>
  <button class="action" style="color:var(--crit);border-color:var(--crit-border)" onclick="rebootDevice()">Reboot Router</button>
  <button class="action" onclick="queuePCCommand('restart_rustdesk')">Restart RustDesk (PC)</button>
  <button class="action" style="color:var(--crit);border-color:var(--crit-border)" onclick="queuePCCommand('restart_pc')">Restart PC</button>
  <div class="action-msg" id="restart-hotspot-msg"></div>
</div>
<div class="actions" style="align-items:center">
  <span style="font-size:0.85rem;color:var(--text-dim)">2.4GHz radio:</span>
  <button class="action" id="band-toggle-btn" onclick="toggleBand()">Loading...</button>
  <span style="font-size:0.78rem;color:var(--text-faint)">Restarts the hotspot to apply - every device briefly reconnects</span>
</div>
<h2>Connected devices</h2>
<div class="devices-summary" id="devices-summary"></div>
<div id="devices"><div class="loading">Loading...</div></div>
<h2>Speed test &amp; AOD message</h2>
<div class="actions">
  <button class="action" id="speedtest-btn" onclick="runSpeedtest()">Run speed test</button>
  <span class="action-msg" id="speedtest-result"></span>
</div>
<div class="actions" style="align-items:center">
  <input type="text" id="custom-message-input" placeholder="Send a message home..." maxlength="200" style="flex:1;min-width:200px;background:var(--panel-2);border:1px solid var(--border);border-radius:8px;color:var(--text);padding:9px 12px">
  <input type="number" id="custom-message-duration" value="5" min="1" max="1440" style="width:70px;background:var(--panel-2);border:1px solid var(--border);border-radius:8px;color:var(--text);padding:9px 8px">
  <span style="color:var(--text-dim);font-size:0.8rem">minutes</span>
  <button class="action" onclick="setCustomMessage()">Show on AOD</button>
  <span class="action-msg" id="custom-message-status"></span>
</div>
<h2>Recent SMS &amp; alerts</h2>
<div class="sms-tabs" id="sms-tabs">
  <span class="sms-tab active" data-cat="all">All</span>
  <span class="sms-tab" data-cat="jio">Jio</span>
  <span class="sms-tab" data-cat="alerts">Alerts</span>
  <span class="sms-tab" data-cat="otp">OTP</span>
  <span class="sms-tab" data-cat="data">Data Usage</span>
</div>
<div id="sms-list"><div class="loading">Loading...</div></div>
<h2>Chat with the Manager</h2>
<div class="chat-window" id="chat-window"></div>
<div class="chat-input-row">
  <input type="text" id="chat-input" placeholder="Ask about status, alerts, flapping services, quota, tunnel, devices...">
  <button class="action" id="chat-send">Send</button>
</div>
<div style="color:var(--text-faint);font-size:0.72rem;margin-top:8px">Rule-based, not a language model - it answers from the router's real state, not general conversation.</div>
<h2>Services</h2>
<div id="services"><div class="loading">Loading...</div></div>

<script>
function pill(state) {
  return '<span class="pill ' + state + '">' + state + '</span>';
}
function pillWithLabel(state, label) {
  return '<span class="pill ' + state + '">' + label + '</span>';
}
function card(label, valueHTML) {
  return '<div class="card"><div class="label">' + label + '</div><div class="value">' + valueHTML + '</div></div>';
}
function statValue(stat) {
  return stat.color ? '<span style="color:' + stat.color + '">' + stat.value + '</span>' : stat.value;
}
function updateClock() {
  const now = new Date();
  document.getElementById('clock').textContent =
    now.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) + ' · ' +
    now.toLocaleDateString([], { weekday: 'short', day: '2-digit', month: 'short' });
}
setInterval(updateClock, 1000);
updateClock();
// One glanceable Good/Warning/Issue signal combining internet, SMS
// forwarder, thermal, and signal quality - so a user doesn't have to
// read four separate stats to know "is everything actually fine".
function computeHealthPill(detailStats) {
  const byLabel = {};
  (detailStats || []).forEach(s => { byLabel[s.label] = s; });
  const internetDown = byLabel.Internet && byLabel.Internet.value !== 'Up';
  const smsDown = byLabel['SMS Fwd'] && byLabel['SMS Fwd'].value !== 'OK';
  if (internetDown || smsDown) return { state: 'down', label: 'Issue' };
  const warnColors = ['#f5a623', '#ff5468'];
  const thermalWarn = byLabel.Thermal && warnColors.includes(byLabel.Thermal.color);
  const signalWarn = byLabel.Signal && (warnColors.includes(byLabel.Signal.color) || byLabel.Signal.value === 'n/a');
  if (thermalWarn || signalWarn) return { state: 'unknown', label: 'Warning' };
  return { state: 'up', label: 'Good' };
}
async function loadHealth() {
  const res = await fetch('/api/health');
  const h = await res.json();

  const health = computeHealthPill(h.detail_stats);
  document.getElementById('subtitle').innerHTML =
    'Live status' + (h.version ? ' · v' + h.version : '') +
    (h.internet_type ? ' · uplink: ' + h.internet_type : '') +
    ' ' + pillWithLabel(health.state, health.label);

  const tunnelPillState = { alive: 'up', dead: 'down', not_present: 'unknown' }[h.tunnel_status] || 'unknown';
  const tunnelLabel = { alive: 'Alive', dead: 'DEAD - reconnect needed', not_present: 'Not present' }[h.tunnel_status] || 'Unknown';
  const cards = [
    card('Hotspot', pill(h.hotspot_up)),
    card('USB Tether', pill(h.usb_tether_up)),
    card('Devices', h.device_count),
    card('Cellular this cycle', h.quota_used_gb.toFixed(1) + ' GB'),
    card('Quota cutoff', h.quota_cutoff_active ? pill('down') : pill('up')),
    card('WG Tunnel', pillWithLabel(tunnelPillState, tunnelLabel)),
  ];
  document.getElementById('cards').innerHTML = cards.join('');

  const quotaCapGB = 300;
  const quotaPct = Math.min(100, (h.quota_used_gb / quotaCapGB) * 100).toFixed(1);
  document.getElementById('quota-bar-wrap').innerHTML =
    '<div style="font-size:0.8rem;color:var(--text-dim)">' + h.quota_used_gb.toFixed(1) + ' / ' + quotaCapGB + ' GB this cycle (' + quotaPct + '%)</div>' +
    '<div class="quota-bar-track"><div class="quota-bar-fill" style="width:' + quotaPct + '%"></div></div>';

  if (!document.getElementById('billing-day-select')) {
    let options = '';
    for (let i = 1; i <= 28; i++) options += '<option value="' + i + '"' + (i === h.quota_billing_day ? ' selected' : '') + '>' + i + '</option>';
    document.getElementById('billing-row').innerHTML =
      'Billing resets on day <select id="billing-day-select">' + options + '</select> of the month ' +
      '<button class="action" style="padding:5px 10px;font-size:0.8rem" onclick="setBillingDay()">Set</button>' +
      '<span id="billing-day-msg"></span>';
  }

  renderAodPreview(h);
  if (aodOptionsLoaded && h.aod_layout_index >= 0) document.getElementById('aod-layout-select').value = h.aod_layout_index;
  if (aodOptionsLoaded && h.aod_theme_index >= 0) document.getElementById('aod-theme-select').value = h.aod_theme_index;

  currentBand2GHzEnabled = h.band_2ghz_enabled;
  const bandBtn = document.getElementById('band-toggle-btn');
  bandBtn.textContent = h.band_2ghz_enabled ? '2.4GHz ON (dual-band) - tap to disable' : '2.4GHz OFF (5GHz only) - tap to enable';

  if (!document.getElementById('speedtest-btn').dataset.rendered) {
    renderSpeedtestCache(h.speedtest_cache);
    document.getElementById('speedtest-btn').dataset.rendered = '1';
  }

  const detail = (h.detail_stats || []).map(s => card(s.label, statValue(s)));
  document.getElementById('detail-cards').innerHTML = detail.length
    ? detail.join('')
    : '<div class="loading">No detail stats available.</div>';

  const services = h.services || {};
  const names = Object.keys(services).sort();
  if (!names.length) {
    document.getElementById('services').innerHTML = '<div class="loading">No service status available.</div>';
  } else {
    let rows = names.map(name => {
      const svc = services[name];
      const state = svc.state || 'unknown';
      const pillClass = state === 'running' ? 'up' : (state === 'backoff' ? 'down' : 'unknown');
      return '<tr><td>' + name + '</td><td>' + pill(pillClass) + ' ' + state + '</td><td>' +
             (svc.restart_count || 0) + '</td></tr>';
    }).join('');
    document.getElementById('services').innerHTML =
      '<table><tr><th>Service</th><th>State</th><th>Restarts</th></tr>' + rows + '</table>';
  }
}
async function loadDevices() {
  const res = await fetch('/api/devices');
  const devices = await res.json();
  if (!devices.length) {
    document.getElementById('devices').innerHTML = '<div class="loading">No devices tracked yet.</div>';
    return;
  }
  const onlineCount = devices.filter(d => d.online).length;
  document.getElementById('devices-summary').textContent =
    onlineCount + ' online now · ' + devices.length + ' seen in the last 7 days';
  let rows = devices.map(d => {
    const gb = (d.cumulative_bytes / 1e9).toFixed(2);
    const macId = 'name-' + d.mac.replace(/:/g, '');
    const statusPill = d.blocked ? pillWithLabel('down', 'Blocked') : pill(d.online ? 'up' : 'offline');
    const blockBtn = d.blocked
      ? '<button class="action rename-btn" onclick="setBlocked(\'' + d.mac + '\', false)">Allow</button>'
      : '<button class="action rename-btn" onclick="setBlocked(\'' + d.mac + '\', true)">Block</button>';
    return '<tr><td>' + statusPill + '</td><td>' +
           '<input class="rename-input" id="' + macId + '" value="' + (d.name || '') + '" placeholder="Unknown device" maxlength="60">' +
           '</td><td>' + d.mac + '</td><td>' +
           (d.band || '-') + '</td><td>' + gb + ' GB</td><td>' + d.last_seen + '</td>' +
           '<td><button class="action rename-btn" onclick="renameDevice(\'' + d.mac + '\', \'' + macId + '\')">Save</button> ' + blockBtn + '</td></tr>';
  }).join('');
  document.getElementById('devices').innerHTML =
    '<table><tr><th>Status</th><th>Name</th><th>MAC</th><th>Band</th><th>Total</th><th>Last seen</th><th></th></tr>' + rows + '</table>';
}
async function renameDevice(mac, inputId) {
  const name = document.getElementById(inputId).value;
  await fetch('/api/devices/rename', {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: 'mac=' + encodeURIComponent(mac) + '&name=' + encodeURIComponent(name)
  });
}
async function setBillingDay() {
  const day = document.getElementById('billing-day-select').value;
  const msg = document.getElementById('billing-day-msg');
  const res = await fetch('/api/billing-day', {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: 'day=' + encodeURIComponent(day)
  });
  msg.textContent = res.ok ? ' saved' : ' failed';
}
async function setBlocked(mac, blocked) {
  await fetch('/api/devices/block', {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: 'mac=' + encodeURIComponent(mac) + '&action=' + (blocked ? 'block' : 'unblock')
  });
  loadDevices();
}
async function restartHotspot() {
  const btn = document.getElementById('restart-hotspot-btn');
  const msg = document.getElementById('restart-hotspot-msg');
  btn.disabled = true;
  msg.textContent = 'Requesting restart...';
  try {
    const res = await fetch('/api/hotspot/restart', { method: 'POST' });
    const body = await res.json();
    msg.textContent = res.ok ? body.detail : ('Failed: ' + (body.detail || res.status));
  } catch (e) {
    msg.textContent = 'Failed: ' + e;
  }
  setTimeout(() => { btn.disabled = false; }, 20000);
}
async function restartService(name) {
  const msg = document.getElementById('restart-hotspot-msg');
  msg.textContent = 'Restarting ' + name + '...';
  try {
    const res = await fetch('/api/service/restart', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'service=' + encodeURIComponent(name)
    });
    msg.textContent = res.ok ? name + ' restarted' : 'Failed to restart ' + name;
  } catch (e) {
    msg.textContent = 'Failed: ' + e;
  }
}
async function rebootDevice() {
  if (!confirm('Reboot the router now? This will drop the hotspot for 1-2 minutes.')) return;
  const msg = document.getElementById('restart-hotspot-msg');
  msg.textContent = 'Rebooting...';
  try {
    await fetch('/api/system/reboot', { method: 'POST' });
  } catch (e) {}
}
function categorizeSMS(addr, body) {
  const s = (addr + ' ' + body).toLowerCase();
  if (s.includes('jio')) return 'jio';
  if (s.includes('otp')) return 'otp';
  if (/data\s*(quota|used|balance)/.test(s)) return 'data';
  return 'alerts';
}
let allSMS = [];
let activeSMSCat = 'all';
function renderSMS() {
  const filtered = activeSMSCat === 'all' ? allSMS : allSMS.filter(m => m.cat === activeSMSCat);
  if (!filtered.length) {
    document.getElementById('sms-list').innerHTML = '<div class="loading">No messages.</div>';
    return;
  }
  // Built via DOM API, not string-concatenated HTML - sender address
  // and message body are both untrusted content (real SMS text), so
  // neither can be allowed to be interpreted as markup.
  const container = document.getElementById('sms-list');
  container.innerHTML = '';
  let lastSender = null;
  filtered.forEach(m => {
    if (m.address !== lastSender) {
      const title = document.createElement('h4');
      title.className = 'sender-title';
      title.textContent = m.address;
      container.appendChild(title);
      lastSender = m.address;
    }
    const when = new Date(m.date_ms).toLocaleString([], { hour: '2-digit', minute: '2-digit', day: '2-digit', month: 'short' });
    const item = document.createElement('div');
    item.className = 'sms-item';
    const meta = document.createElement('div');
    meta.className = 'meta';
    const metaSpan = document.createElement('span');
    metaSpan.textContent = when;
    meta.appendChild(metaSpan);
    const body = document.createElement('div');
    body.className = 'body';
    body.textContent = m.body;
    item.appendChild(meta);
    item.appendChild(body);
    container.appendChild(item);
  });
}
async function loadSMS() {
  const res = await fetch('/api/sms');
  const msgs = await res.json();
  allSMS = msgs.map(m => ({ ...m, cat: categorizeSMS(m.address, m.body) }));
  renderSMS();
}
document.querySelectorAll('.sms-tab').forEach(tab => {
  tab.addEventListener('click', () => {
    document.querySelectorAll('.sms-tab').forEach(t => t.classList.remove('active'));
    tab.classList.add('active');
    activeSMSCat = tab.dataset.cat;
    renderSMS();
  });
});
function renderSpeedtestCache(cache) {
  const el = document.getElementById('speedtest-result');
  if (!cache) { el.textContent = 'Not run yet'; return; }
  const ageMin = Math.round((Date.now() / 1000 - cache.ts) / 60);
  el.textContent = 'Down: ' + cache.down.toFixed(1) + ' Mbps / Up: ' + cache.up.toFixed(1) + ' Mbps / Ping: ' + cache.ping.toFixed(0) + ' ms (tested ' + ageMin + 'm ago)';
}
async function runSpeedtest() {
  const btn = document.getElementById('speedtest-btn');
  const el = document.getElementById('speedtest-result');
  btn.disabled = true;
  el.textContent = 'Running speed test (uses real data)...';
  try {
    const res = await fetch('/api/speedtest', { method: 'POST' });
    const body = await res.json();
    if (res.ok) {
      el.textContent = 'Down: ' + body.down.toFixed(1) + ' Mbps / Up: ' + body.up.toFixed(1) + ' Mbps / Ping: ' + body.ping.toFixed(0) + ' ms';
    } else {
      el.textContent = 'Speed test failed';
    }
  } catch (e) {
    el.textContent = 'Failed: ' + e;
  }
  btn.disabled = false;
}
async function setCustomMessage() {
  const message = document.getElementById('custom-message-input').value;
  const duration = document.getElementById('custom-message-duration').value;
  const status = document.getElementById('custom-message-status');
  if (!message.trim()) return;
  const res = await fetch('/api/custom-message', {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: 'message=' + encodeURIComponent(message) + '&duration_min=' + encodeURIComponent(duration)
  });
  status.textContent = res.ok ? 'Shown on AOD' : 'Failed';
}
let currentBand2GHzEnabled = false;
async function toggleBand() {
  const enabling = !currentBand2GHzEnabled;
  const action = enabling ? 'enable_2ghz' : 'disable_2ghz';
  const msg = enabling
    ? 'Enable 2.4GHz (switches to bridged dual-band, shared SSID "PILLYBOO")? The hotspot will restart and every device will briefly reconnect.'
    : 'Disable 2.4GHz (switches to 5GHz-only, SSID "PILLYBOO 5GHZ")? The hotspot will restart and every device will briefly reconnect.';
  if (!confirm(msg)) return;
  const btn = document.getElementById('band-toggle-btn');
  btn.disabled = true;
  try {
    const res = await fetch('/api/hotspot/band', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'action=' + action
    });
    const body = await res.json();
    document.getElementById('restart-hotspot-msg').textContent = res.ok ? body.detail : ('Failed: ' + (body.detail || res.status));
  } catch (e) {
    document.getElementById('restart-hotspot-msg').textContent = 'Failed: ' + e;
  }
  btn.disabled = false;
}
let aodOptionsLoaded = false;
let cachedThemeNames = [];
async function loadAodOptions() {
  const res = await fetch('/api/aod/options');
  const body = await res.json();
  cachedThemeNames = body.themes || [];
  const layoutSel = document.getElementById('aod-layout-select');
  const themeSel = document.getElementById('aod-theme-select');
  layoutSel.innerHTML = (body.layouts || []).map((name, i) => '<option value="' + i + '">' + name + '</option>').join('');
  themeSel.innerHTML = cachedThemeNames.map((name, i) => '<option value="' + i + '">' + name + '</option>').join('');
  aodOptionsLoaded = true;
}
let lastHealthForPreview = null;
function renderAodPreview(h) {
  lastHealthForPreview = h;
  const now = new Date();
  const time = now.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  const date = now.toLocaleDateString([], { weekday: 'short', day: '2-digit', month: 'short' });
  const byLabel = {};
  (h.detail_stats || []).forEach(s => { byLabel[s.label] = s; });
  const row = (label) => byLabel[label] ? statValue(byLabel[label]) : '--';
  const themeName = cachedThemeNames[h.aod_theme_index] || '--';
  document.getElementById('aod-preview').innerHTML =
    '<div class="phone-time">' + time + '</div><div class="phone-date">' + date + '</div>' +
    '<div class="phone-rows">' +
    '<div><div class="lbl">Signal</div><div class="val">' + row('Signal') + '</div></div>' +
    '<div><div class="lbl">Internet</div><div class="val">' + row('Internet') + '</div></div>' +
    '<div><div class="lbl">Battery</div><div class="val">' + row('Battery') + '</div></div>' +
    '<div><div class="lbl">Skin Temp</div><div class="val">' + row('Skin Temp') + '</div></div>' +
    '<div><div class="lbl">Devices</div><div class="val">' + (h.device_count != null ? h.device_count : '--') + '</div></div>' +
    '<div><div class="lbl">SMS Fwd</div><div class="val">' + row('SMS Fwd') + '</div></div>' +
    '</div>' +
    '<div style="text-align:center;margin-top:12px;padding-top:12px;border-top:1px solid #232b36">' +
    '<div class="lbl" style="color:#8b98a8;font-size:0.6rem;text-transform:uppercase">Theme</div>' +
    '<div style="color:var(--accent);font-weight:700;margin-top:2px">' + themeName + '</div></div>';
}
// Ticks just the clock/date text between the ~15s full-data refreshes,
// reusing whatever stats were last fetched rather than re-rendering
// with empty data.
setInterval(function() {
  if (lastHealthForPreview) renderAodPreview(lastHealthForPreview);
}, 1000);
async function setAodLayout() {
  const idx = document.getElementById('aod-layout-select').value;
  const msg = document.getElementById('aod-msg');
  msg.textContent = 'Applying layout...';
  const res = await fetch('/api/aod/layout', {
    method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: 'layout=' + encodeURIComponent(idx)
  });
  msg.textContent = res.ok ? 'Layout applied' : 'Failed to apply layout';
}
async function setAodTheme() {
  const idx = document.getElementById('aod-theme-select').value;
  const msg = document.getElementById('aod-msg');
  msg.textContent = 'Applying theme...';
  const res = await fetch('/api/aod/theme', {
    method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: 'theme=' + encodeURIComponent(idx)
  });
  msg.textContent = res.ok ? 'Theme applied' : 'Failed to apply theme';
}
loadAodOptions();
async function queuePCCommand(command) {
  const confirmMsg = command === 'restart_pc'
    ? 'Restart the PC? It queues a reboot with a 60s cancelable grace period.'
    : 'Restart RustDesk on the PC?';
  if (!confirm(confirmMsg)) return;
  const msg = document.getElementById('restart-hotspot-msg');
  msg.textContent = 'Queuing ' + command + ' (picked up within ~60s)...';
  try {
    const res = await fetch('/api/pc-command', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'command=' + encodeURIComponent(command)
    });
    msg.textContent = res.ok ? command + ' queued' : 'Failed to queue ' + command;
  } catch (e) {
    msg.textContent = 'Failed: ' + e;
  }
}
function appendChatMsg(role, text) {
  const win = document.getElementById('chat-window');
  const div = document.createElement('div');
  div.className = 'chat-msg chat-' + role;
  div.textContent = text;
  win.appendChild(div);
  win.scrollTop = win.scrollHeight;
}
async function sendChat() {
  const input = document.getElementById('chat-input');
  const msg = input.value.trim();
  if (!msg) return;
  appendChatMsg('user', msg);
  input.value = '';
  try {
    const res = await fetch('/api/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'message=' + encodeURIComponent(msg)
    });
    const body = await res.json();
    appendChatMsg('manager', body.reply || '(no reply)');
  } catch (e) {
    appendChatMsg('manager', 'Request failed: ' + e);
  }
}
document.getElementById('chat-send').addEventListener('click', sendChat);
document.getElementById('chat-input').addEventListener('keydown', function(e) {
  if (e.key === 'Enter') sendChat();
});
function refresh() { loadHealth(); loadDevices(); loadSMS(); }
refresh();
setInterval(refresh, 15000);
</script>
</body></html>`

func randomToken() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func renderLoginForm(w http.ResponseWriter, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	errHTML := ""
	if errMsg != "" {
		errHTML = `<div class="err">` + errMsg + `</div>`
	}
	w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Router Login</title>
<style>
body{background:#0b0f14;color:#e6edf5;font-family:-apple-system,Segoe UI,Roboto,sans-serif;
     display:flex;align-items:center;justify-content:center;height:100vh;margin:0}
.box{background:#11161d;border:1px solid #232b36;border-radius:10px;padding:28px;width:280px}
h1{font-size:1.1rem;margin:0 0 14px}
input{width:100%;padding:10px;font-size:1rem;background:#151b24;border:1px solid #232b36;
      border-radius:6px;color:#e6edf5;margin-bottom:10px;box-sizing:border-box}
input[name=code]{font-size:1.4rem;letter-spacing:0.2em;text-align:center}
button{width:100%;padding:10px;background:#4fc3d9;border:none;border-radius:6px;font-weight:600;cursor:pointer}
.err{color:#ff5468;font-size:0.85rem;margin-bottom:10px}
</style></head><body>
<form class="box" method="POST" action="/login">
<h1>Router Login</h1>` + errHTML + `
<input name="username" placeholder="Username" autocomplete="username">
<input name="password" type="password" placeholder="Password" autocomplete="current-password">
<input name="code" placeholder="Authy code" inputmode="numeric" maxlength="6" autocomplete="one-time-code">
<button type="submit">Sign in</button>
</form></body></html>`))
}
