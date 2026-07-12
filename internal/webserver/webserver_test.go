package webserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/antriva/pixel-router-v2/internal/smsviewer"
	"github.com/antriva/pixel-router-v2/internal/speedtest"
	"github.com/antriva/pixel-router-v2/internal/store"
	"github.com/antriva/pixel-router-v2/internal/totp"
)

const testSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	fh, err := os.CreateTemp("", "webserver_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	fh.Close()
	path := fh.Name()
	t.Cleanup(func() { os.Remove(path) })

	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	hash, err := bcrypt.GenerateFromPassword([]byte("testpass123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}

	srv := New(Config{
		Username:     "TestUser",
		PasswordHash: string(hash),
		TotpSecret:   testSecret,
	}, st)
	return srv, st
}

func TestProtectedRoute_NoSession_Returns401(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestLogin_WrongPassword_Rejected(t *testing.T) {
	srv, _ := newTestServer(t)
	code, _ := totp.At(testSecret, time.Now().Unix())

	form := url.Values{"username": {"TestUser"}, "password": {"wrongpass"}, "code": {code}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected the login form re-rendered with 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Incorrect") {
		t.Fatal("expected an error message in the response body")
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "router_session" {
			t.Fatal("must not issue a session cookie on failed login")
		}
	}
}

func TestLogin_WrongCode_Rejected(t *testing.T) {
	srv, _ := newTestServer(t)
	form := url.Values{"username": {"TestUser"}, "password": {"testpass123"}, "code": {"000000"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if strings.Contains(strings.ToLower(w.Body.String()), "see other") {
		t.Fatal("should not redirect on wrong code")
	}
}

func TestLogin_CorrectCredentials_IssuesSessionAndUnlocksProtectedRoute(t *testing.T) {
	srv, _ := newTestServer(t)
	code, _ := totp.At(testSecret, time.Now().Unix())

	form := url.Values{"username": {"TestUser"}, "password": {"testpass123"}, "code": {code}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect on successful login, got %d", w.Code)
	}

	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "router_session" {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected a router_session cookie to be set")
	}

	// Now use that session against a protected route.
	req2 := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req2.AddCookie(sessionCookie)
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 with a valid session, got %d", w2.Code)
	}
}

func TestLogin_RecoveryCode_WorksOnceThenRejected(t *testing.T) {
	srv, st := newTestServer(t)
	codes, err := st.GenerateRecoveryCodes(3)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{"username": {"TestUser"}, "password": {"testpass123"}, "code": {codes[0]}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected successful login via recovery code, got %d: %s", w.Code, w.Body.String())
	}

	// Same recovery code again must be rejected (single-use).
	req2 := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, req2)
	if w2.Code == http.StatusSeeOther {
		t.Fatal("a used recovery code must not work a second time")
	}
}

func TestLockout_AfterFiveFailures(t *testing.T) {
	srv, _ := newTestServer(t)
	form := url.Values{"username": {"TestUser"}, "password": {"wrong"}, "code": {"000000"}}

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "9.9.9.9:1234"
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
	}

	// 6th attempt, even with the CORRECT credentials, should be locked out.
	code, _ := totp.At(testSecret, time.Now().Unix())
	form2 := url.Values{"username": {"TestUser"}, "password": {"testpass123"}, "code": {code}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form2.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "9.9.9.9:1234"
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusSeeOther {
		t.Fatal("expected lockout to block even correct credentials after 5 failures")
	}
	if !strings.Contains(w.Body.String(), "Too many") {
		t.Fatal("expected a lockout message")
	}
}

func TestHandleHealth_AggregatesCorrectly(t *testing.T) {
	fh, _ := os.CreateTemp("", "webserver_health_test_*.db")
	fh.Close()
	path := fh.Name()
	defer os.Remove(path)

	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	st.UpsertDeviceSeen("aa:bb:cc:dd:ee:ff", "5GHz", 1000)
	st.SetBillingDay(15)

	hash, _ := bcrypt.GenerateFromPassword([]byte("x"), bcrypt.DefaultCost)
	srv := New(Config{
		Username: "u", PasswordHash: string(hash), TotpSecret: testSecret,
		HotspotUp:   func() bool { return true },
		USBTetherUp: func() bool { return false },
	}, st)

	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.AddCookie(&http.Cookie{Name: "router_session", Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"hotspot_up":"up"`) {
		t.Fatalf("expected hotspot_up=up in response, got %s", body)
	}
	if !strings.Contains(body, `"usb_tether_up":"down"`) {
		t.Fatalf("expected usb_tether_up=down in response, got %s", body)
	}
	if !strings.Contains(body, `"device_count":1`) {
		t.Fatalf("expected device_count=1, got %s", body)
	}
}

func issueTestSession(st *store.Store) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	return token, st.CreateSession(token, time.Hour)
}

func TestHotspotRestart_NoSession_Returns401(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/hotspot/restart", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHotspotRestart_GetMethod_Rejected(t *testing.T) {
	srv, st := newTestServer(t)
	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/hotspot/restart", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for a GET request, got %d", w.Code)
	}
}

func TestHotspotRestart_ValidSession_WritesTriggerFile(t *testing.T) {
	fh, err := os.CreateTemp("", "hotspot_force_restart_*")
	if err != nil {
		t.Fatal(err)
	}
	path := fh.Name()
	fh.Close()
	os.Remove(path) // the handler should create it fresh, not require it pre-exist
	t.Cleanup(func() { os.Remove(path) })

	fh2, err := os.CreateTemp("", "webserver_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	fh2.Close()
	dbPath := fh2.Name()
	t.Cleanup(func() { os.Remove(dbPath) })
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, err := bcrypt.GenerateFromPassword([]byte("testpass123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Config{
		Username:                "TestUser",
		PasswordHash:            string(hash),
		TotpSecret:              testSecret,
		HotspotForceRestartPath: path,
	}, st)

	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/hotspot/restart", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected trigger file to be written, got error: %v", err)
	}
}

func TestHotspotRestart_NotConfigured_ReturnsServiceUnavailable(t *testing.T) {
	srv, st := newTestServer(t) // HotspotForceRestartPath left empty
	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/hotspot/restart", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when unconfigured, got %d", w.Code)
	}
}

func TestDeviceRename_ValidSession_UpdatesName(t *testing.T) {
	srv, st := newTestServer(t)
	st.UpsertDeviceSeen("aa:bb:cc:dd:ee:ff", "5GHz", 0)

	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/devices/rename", strings.NewReader("mac=aa:bb:cc:dd:ee:ff&name=Living+Room+TV"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	devices, err := st.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Name != "Living Room TV" {
		t.Fatalf("expected renamed device, got %+v", devices)
	}
}

func TestDeviceRename_NoSession_Returns401(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/devices/rename", strings.NewReader("mac=aa:bb:cc:dd:ee:ff&name=x"))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

type fakeAodPrefs struct {
	xml           string
	readErr       error
	refreshCalled bool
}

func (f *fakeAodPrefs) ReadPrefs() (string, error)  { return f.xml, f.readErr }
func (f *fakeAodPrefs) WritePrefs(xml string) error { f.xml = xml; return nil }
func (f *fakeAodPrefs) Refresh() error              { f.refreshCalled = true; return nil }

func TestSetAodLayout_ValidSession_PatchesAndRefreshes(t *testing.T) {
	srv, st := newTestServer(t)
	fake := &fakeAodPrefs{xml: `<map><int name="layout_index" value="0" /><int name="theme_index" value="0" /></map>`}
	srv.cfg.AodPrefs = fake

	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/aod/layout", strings.NewReader("layout=12"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(fake.xml, `layout_index" value="12"`) {
		t.Fatalf("expected patched layout index, got %s", fake.xml)
	}
	if !fake.refreshCalled {
		t.Fatal("expected Refresh() to be called")
	}
}

func TestSetAodLayout_OutOfRange_Rejected(t *testing.T) {
	srv, st := newTestServer(t)
	srv.cfg.AodPrefs = &fakeAodPrefs{xml: `<map><int name="layout_index" value="0" /></map>`}
	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/aod/layout", strings.NewReader("layout=999"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAodOptions_ReturnsNameLists(t *testing.T) {
	srv, st := newTestServer(t)
	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/aod/options", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Corner stack") || !strings.Contains(w.Body.String(), "Neon cyberpunk") {
		t.Fatalf("expected layout/theme names in response, got %s", w.Body.String())
	}
}

func TestSetBand_EnableAndDisable_PersistsAndRestartsHotspot(t *testing.T) {
	fh, err := os.CreateTemp("", "hotspot_config_*.json")
	if err != nil {
		t.Fatal(err)
	}
	path := fh.Name()
	initial := hotspotFileConfig{
		SSID5GhzOnly: "PILLYBOO 5GHZ", SSIDDualBand: "PILLYBOO",
		Password: "x", Band2GHzEnabled: false,
		Deny2GhzMACs: []string{"aa:bb:cc:dd:ee:ff"},
	}
	data, _ := json.Marshal(initial)
	fh.Write(data)
	fh.Close()
	t.Cleanup(func() { os.Remove(path) })

	srv, st := newTestServer(t)
	srv.cfg.HotspotConfigPath = path
	restartCalls := []string{}
	srv.cfg.RestartService = func(name string) error { restartCalls = append(restartCalls, name); return nil }

	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/hotspot/band", strings.NewReader("action=enable_2ghz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(restartCalls) != 1 || restartCalls[0] != "hotspot" {
		t.Fatalf("expected hotspot restart triggered, got %v", restartCalls)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fc hotspotFileConfig
	if err := json.Unmarshal(saved, &fc); err != nil {
		t.Fatal(err)
	}
	if !fc.Band2GHzEnabled {
		t.Fatal("expected band_2ghz_enabled to be persisted as true")
	}
	if fc.SSID5GhzOnly != "PILLYBOO 5GHZ" || len(fc.Deny2GhzMACs) != 1 {
		t.Fatalf("expected other fields preserved, got %+v", fc)
	}
}

func TestSetBand_InvalidAction_Rejected(t *testing.T) {
	srv, st := newTestServer(t)
	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/hotspot/band", strings.NewReader("action=nonsense"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestChat_ValidSession_ReturnsRuleBasedReply(t *testing.T) {
	srv, st := newTestServer(t)
	srv.cfg.HotspotUp = func() bool { return true }

	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader("message=status"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"reply"`) {
		t.Fatalf("expected a reply field in response, got %s", w.Body.String())
	}
}

func TestChat_NoSession_Returns401(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader("message=status"))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestSpeedtest_ValidSession_RunsAndCaches(t *testing.T) {
	fh, err := os.CreateTemp("", "speedtest_cache_*.json")
	if err != nil {
		t.Fatal(err)
	}
	path := fh.Name()
	fh.Close()
	os.Remove(path)
	t.Cleanup(func() { os.Remove(path) })

	srv, st := newTestServer(t)
	srv.cfg.SpeedtestCachePath = path
	srv.cfg.RunSpeedtest = func() (speedtest.Result, error) {
		return speedtest.Result{DownMbps: 50, UpMbps: 10, PingMs: 20}, nil
	}

	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/speedtest", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"down":50`) {
		t.Fatalf("expected down speed in response, got %s", w.Body.String())
	}
	cached, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected result to be cached: %v", err)
	}
	if !strings.Contains(string(cached), `"down":50`) {
		t.Fatalf("expected cached result to contain down speed, got %s", cached)
	}
}

func TestSetCustomMessage_ValidSession_AppearsInHealth(t *testing.T) {
	fh, err := os.CreateTemp("", "custom_message_*.txt")
	if err != nil {
		t.Fatal(err)
	}
	path := fh.Name()
	fh.Close()
	os.Remove(path)
	t.Cleanup(func() { os.Remove(path) })

	srv, st := newTestServer(t)
	srv.cfg.CustomMessagePath = path

	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/custom-message", strings.NewReader("message=Hello+home&duration_min=5"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	healthReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	healthW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(healthW, healthReq)
	if !strings.Contains(healthW.Body.String(), `"custom_message":"Hello home"`) {
		t.Fatalf("expected active custom message in health response, got %s", healthW.Body.String())
	}
}

func TestSetCustomMessage_ExpiredMessage_NotShown(t *testing.T) {
	fh, err := os.CreateTemp("", "custom_message_expired_*.txt")
	if err != nil {
		t.Fatal(err)
	}
	path := fh.Name()
	os.WriteFile(path, []byte("1\nThis expired long ago"), 0644)
	t.Cleanup(func() { os.Remove(path) })

	srv, st := newTestServer(t)
	srv.cfg.CustomMessagePath = path

	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if strings.Contains(w.Body.String(), "expired long ago") {
		t.Fatalf("expected expired message to be omitted, got %s", w.Body.String())
	}
}

func TestPCCommand_QueueThenPollWithKey(t *testing.T) {
	fh, err := os.CreateTemp("", "pc_command_*.json")
	if err != nil {
		t.Fatal(err)
	}
	path := fh.Name()
	fh.Close()
	os.Remove(path)
	t.Cleanup(func() { os.Remove(path) })

	srv, st := newTestServer(t)
	srv.cfg.PCCommandPath = path
	srv.cfg.PCCommandKey = "test-key-123"

	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/pc-command", strings.NewReader("command=restart_pc"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 queuing, got %d: %s", w.Code, w.Body.String())
	}

	pollReq := httptest.NewRequest(http.MethodGet, "/pc-command.json?key=test-key-123", nil)
	pollW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(pollW, pollReq)
	if pollW.Code != http.StatusOK {
		t.Fatalf("expected 200 polling with correct key, got %d", pollW.Code)
	}
	if !strings.Contains(pollW.Body.String(), `"command":"restart_pc"`) {
		t.Fatalf("expected queued command in poll response, got %s", pollW.Body.String())
	}
}

func TestPCCommand_PollWithWrongKey_Forbidden(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.cfg.PCCommandKey = "correct-key"
	req := httptest.NewRequest(http.MethodGet, "/pc-command.json?key=wrong-key", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for wrong key, got %d", w.Code)
	}
}

func TestPCCommand_QueueUnknownCommand_Rejected(t *testing.T) {
	srv, st := newTestServer(t)
	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/pc-command", strings.NewReader("command=launch_nukes"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown command, got %d", w.Code)
	}
}

func TestSMS_ValidSession_ReturnsFetchedMessages(t *testing.T) {
	srv, st := newTestServer(t)
	srv.cfg.FetchSMS = func(limit int) ([]smsviewer.Message, error) {
		return []smsviewer.Message{{Address: "Test", Body: "Hello", DateMs: 123}}, nil
	}
	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/sms", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"body":"Hello"`) {
		t.Fatalf("expected fetched message in response, got %s", w.Body.String())
	}
}

func TestSMS_NoSession_Returns401(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/sms", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestServiceRestart_ValidSession_CallsRestartService(t *testing.T) {
	srv, st := newTestServer(t)
	calls := []string{}
	srv.cfg.RestartService = func(name string) error { calls = append(calls, name); return nil }

	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/service/restart", strings.NewReader("service=failover"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(calls) != 1 || calls[0] != "failover" {
		t.Fatalf("expected RestartService called with 'failover', got %v", calls)
	}
}

func TestServiceRestart_NoSession_Returns401(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/service/restart", strings.NewReader("service=failover"))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestReboot_ValidSession_CallsReboot(t *testing.T) {
	srv, st := newTestServer(t)
	rebootCalled := make(chan struct{}, 1)
	srv.cfg.Reboot = func() error { rebootCalled <- struct{}{}; return nil }

	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/system/reboot", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	select {
	case <-rebootCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("expected Reboot to be called")
	}
}

func TestReboot_NoSession_Returns401(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/system/reboot", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestSetBillingDay_ValidSession_Persists(t *testing.T) {
	srv, st := newTestServer(t)
	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/billing-day", strings.NewReader("day=15"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	bc, err := st.GetBillingCycle()
	if err != nil {
		t.Fatal(err)
	}
	if bc.BillingDay != 15 {
		t.Fatalf("expected billing day 15, got %d", bc.BillingDay)
	}
}

func TestSetBillingDay_OutOfRange_Rejected(t *testing.T) {
	srv, st := newTestServer(t)
	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/billing-day", strings.NewReader("day=99"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an out-of-range day, got %d", w.Code)
	}
}

func TestResolveBorrowedName(t *testing.T) {
	cases := []struct {
		name, ssid, p1n, p1s, p2n, p2s, want string
	}{
		{name: "matches phone1", ssid: "DadsPhone", p1n: "Dad", p1s: "DadsPhone", p2n: "Mom", p2s: "MomsPhone", want: "Dad"},
		{name: "matches phone2", ssid: "MomsPhone", p1n: "Dad", p1s: "DadsPhone", p2n: "Mom", p2s: "MomsPhone", want: "Mom"},
		{name: "unmatched SSID falls back to raw SSID", ssid: "SomeOtherWifi", p1n: "Dad", p1s: "DadsPhone", want: "SomeOtherWifi"},
		{name: "empty SSID falls back to generic label", ssid: "", want: "Borrowed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveBorrowedName(c.ssid, c.p1n, c.p1s, c.p2n, c.p2s)
			if got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestDeviceBlock_ValidSession_PersistsAndCallsBlockDevice(t *testing.T) {
	srv, st := newTestServer(t)
	st.UpsertDeviceSeen("aa:bb:cc:dd:ee:ff", "5GHz", 0)

	blockCalls := []string{}
	srv.cfg.BlockDevice = func(mac string) error { blockCalls = append(blockCalls, mac); return nil }

	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/devices/block", strings.NewReader("mac=aa:bb:cc:dd:ee:ff&action=block"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(blockCalls) != 1 || blockCalls[0] != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("expected BlockDevice called once with the mac, got %v", blockCalls)
	}
	devices, err := st.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	if !devices[0].Blocked {
		t.Fatal("expected device to be persisted as blocked")
	}
}

func TestDeviceBlock_InvalidAction_Rejected(t *testing.T) {
	srv, st := newTestServer(t)
	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/devices/block", strings.NewReader("mac=aa:bb:cc:dd:ee:ff&action=nonsense"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an invalid action, got %d", w.Code)
	}
}

func TestDevices_FreshlySeenDevice_ReportsOnline(t *testing.T) {
	srv, st := newTestServer(t)
	st.UpsertDeviceSeen("aa:bb:cc:dd:ee:ff", "5GHz", 1000)

	token, err := issueTestSession(st)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"online":true`) {
		t.Fatalf("expected a just-upserted device to report online:true, got %s", w.Body.String())
	}
}

func TestAodStats_RemoteRequest_Forbidden(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/internal/aod-stats", nil)
	req.RemoteAddr = "203.0.113.5:54321" // a real remote IP, not loopback
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-loopback remote address, got %d", w.Code)
	}
}

func TestAodStats_LoopbackRequest_NoSessionNeeded(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/internal/aod-stats", nil)
	req.RemoteAddr = "127.0.0.1:54321" // no session cookie attached at all
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for a loopback request with no session (this route is intentionally not 2FA-gated), got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"label":"Down"`) {
		t.Fatalf("expected the 10-stat AOD payload, got %s", w.Body.String())
	}
}

func TestAodStats_IPv6Loopback_Allowed(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/internal/aod-stats", nil)
	req.RemoteAddr = "[::1]:54321"
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for ::1 (IPv6 loopback), got %d", w.Code)
	}
}

func TestHealthz_RemoteRequest_Forbidden(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "203.0.113.5:54321"
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a non-loopback remote address, got %d", w.Code)
	}
}

func TestHealthz_MissingStatusFile_ReportsNotOK(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("x"), bcrypt.DefaultCost)
	fh, _ := os.CreateTemp("", "webserver_healthz_test_*.db")
	fh.Close()
	path := fh.Name()
	defer os.Remove(path)
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	srv := New(Config{
		Username: "u", PasswordHash: string(hash), TotpSecret: testSecret,
		SupervisorStatusPath: "/nonexistent/path/status.json",
	}, st)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when the status file can't be read, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"ok":false`) {
		t.Fatalf("expected ok:false in body, got %s", w.Body.String())
	}
}

func TestHealthz_AllRunning_ReportsOK(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("x"), bcrypt.DefaultCost)
	fh, _ := os.CreateTemp("", "webserver_healthz_test_*.db")
	fh.Close()
	dbPath := fh.Name()
	defer os.Remove(dbPath)
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	statusFile, _ := os.CreateTemp("", "webserver_healthz_status_*.json")
	statusFile.WriteString(`{"webserver":{"state":"running","restart_count":0},"quota":{"state":"running","restart_count":2}}`)
	statusFile.Close()
	defer os.Remove(statusFile.Name())

	srv := New(Config{
		Username: "u", PasswordHash: string(hash), TotpSecret: testSecret,
		SupervisorStatusPath: statusFile.Name(),
	}, st)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 when every service reports running, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("expected ok:true in body, got %s", w.Body.String())
	}
}

func TestHealthz_OneServiceDown_ReportsNotOK(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("x"), bcrypt.DefaultCost)
	fh, _ := os.CreateTemp("", "webserver_healthz_test_*.db")
	fh.Close()
	dbPath := fh.Name()
	defer os.Remove(dbPath)
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	statusFile, _ := os.CreateTemp("", "webserver_healthz_status_*.json")
	statusFile.WriteString(`{"webserver":{"state":"running","restart_count":0},"ttlproxy":{"state":"backoff","restart_count":5}}`)
	statusFile.Close()
	defer os.Remove(statusFile.Name())

	srv := New(Config{
		Username: "u", PasswordHash: string(hash), TotpSecret: testSecret,
		SupervisorStatusPath: statusFile.Name(),
	}, st)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when a service isn't running, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"ok":false`) {
		t.Fatalf("expected ok:false in body, got %s", w.Body.String())
	}
}
