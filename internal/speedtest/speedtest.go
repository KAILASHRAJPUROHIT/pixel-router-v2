// Package speedtest ports V1's on-demand "Run Speed Test" button. V1
// shelled out to a speedtest CLI; no such binary exists on this device
// (confirmed 2026-07-12 - the only "speedtest_manual.bin" file present
// was a stray 24-byte text file, not a real binary). This instead uses
// Cloudflare's own public speed-test edge endpoints (the same ones
// speed.cloudflare.com's own page calls) - reliable, fast, and requires
// no API key.
package speedtest

import (
	"io"
	"net/http"
	"time"
)

type Result struct {
	DownMbps float64
	UpMbps   float64
	PingMs   float64
}

// BaseURL is overridden in tests to point at a local httptest server
// instead of the real Cloudflare edge.
var BaseURL = "https://speed.cloudflare.com"

const downloadBytes = 10_000_000 // 10MB - big enough to get past TCP slow-start, small enough to run in a few seconds even on a slow link
const uploadBytes = 5_000_000

// Run performs a real download, upload, and latency measurement
// against BaseURL. Client is injected (rather than http.DefaultClient)
// so callers can set a deadline appropriate to their own context.
func Run(client *http.Client) (Result, error) {
	var res Result

	pingStart := time.Now()
	pingResp, err := client.Get(BaseURL + "/__down?bytes=0")
	if err != nil {
		return res, err
	}
	io.Copy(io.Discard, pingResp.Body)
	pingResp.Body.Close()
	res.PingMs = float64(time.Since(pingStart).Microseconds()) / 1000

	downStart := time.Now()
	downResp, err := client.Get(BaseURL + "/__down?bytes=" + itoa(downloadBytes))
	if err != nil {
		return res, err
	}
	n, _ := io.Copy(io.Discard, downResp.Body)
	downResp.Body.Close()
	downSecs := time.Since(downStart).Seconds()
	if downSecs > 0 {
		res.DownMbps = float64(n) * 8 / downSecs / 1_000_000
	}

	upStart := time.Now()
	upReq, err := http.NewRequest(http.MethodPost, BaseURL+"/__up", &zeroReader{remaining: uploadBytes})
	if err != nil {
		return res, err
	}
	upReq.ContentLength = uploadBytes
	upResp, err := client.Do(upReq)
	if err != nil {
		return res, err
	}
	io.Copy(io.Discard, upResp.Body)
	upResp.Body.Close()
	upSecs := time.Since(upStart).Seconds()
	if upSecs > 0 {
		res.UpMbps = uploadBytes * 8 / upSecs / 1_000_000
	}

	return res, nil
}

// zeroReader streams N zero bytes without allocating the whole buffer
// up front - the upload test only cares about how long the transfer
// takes, not the actual content.
type zeroReader struct{ remaining int64 }

func (z *zeroReader) Read(p []byte) (int, error) {
	if z.remaining <= 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > z.remaining {
		n = z.remaining
	}
	for i := int64(0); i < n; i++ {
		p[i] = 0
	}
	z.remaining -= n
	return int(n), nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
