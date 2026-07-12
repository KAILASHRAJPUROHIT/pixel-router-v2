package speedtest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestRun_AgainstFakeServer_ComputesPlausibleResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/__down":
			n, _ := strconv.Atoi(r.URL.Query().Get("bytes"))
			buf := make([]byte, 4096)
			written := 0
			for written < n {
				chunk := len(buf)
				if n-written < chunk {
					chunk = n - written
				}
				w.Write(buf[:chunk])
				written += chunk
			}
		case r.Method == http.MethodPost && r.URL.Path == "/__up":
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	oldBase := BaseURL
	BaseURL = srv.URL
	defer func() { BaseURL = oldBase }()

	res, err := Run(&http.Client{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if res.DownMbps <= 0 {
		t.Fatalf("expected a positive download speed, got %v", res.DownMbps)
	}
	if res.UpMbps <= 0 {
		t.Fatalf("expected a positive upload speed, got %v", res.UpMbps)
	}
	if res.PingMs < 0 {
		t.Fatalf("expected a non-negative ping, got %v", res.PingMs)
	}
}
