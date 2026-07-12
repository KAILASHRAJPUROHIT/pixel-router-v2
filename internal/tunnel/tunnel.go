// Package tunnel launches ngrok bound to a reserved static domain and
// confirms it actually came up before the caller treats the service as
// ready.
//
// This replaces an earlier cloudflared-based quick-tunnel design: that
// approach got a fresh, rotating *.trycloudflare.com URL on every
// restart, which meant careful race-free extraction of the new URL from
// cloudflared's own log stream (see git history for that version and its
// ExtractURL tests). ngrok's reserved static domain sidesteps that
// entire problem - the public URL is fixed and known in advance, so
// there's nothing to extract. What's left to get right is simpler:
// confirm ngrok actually started serving before writing the (already
// known) URL to the file the dashboard reads, since a failed launch
// (e.g. the domain still bound to another agent - ERR_NGROK_334) should
// not be silently reported as ready.
package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// classifyLine reports whether a single line of ngrok's log output is
// its tunnel-ready confirmation, a fatal startup error, or neither.
// Split out from WaitForReady so the same detection logic can drive
// both a bounded, testable scan (WaitForReady) and Launch's live,
// unbounded drain of an actually-running process's output.
func classifyLine(line string) (ready bool, failErr error) {
	if strings.Contains(line, `msg="tunnel session started"`) {
		return true, nil
	}
	if strings.Contains(line, "lvl=crit") || strings.Contains(line, `msg="command failed"`) {
		return false, fmt.Errorf("ngrok failed to start: %s", line)
	}
	return false, nil
}

// WaitForReady scans r line by line for ngrok's own confirmation that
// the tunnel session started, or for a fatal startup error, whichever
// comes first. Reader-based so it's unit-testable against a fake stream
// without a real ngrok process.
func WaitForReady(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		ready, err := classifyLine(scanner.Text())
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return fmt.Errorf("stream ended before ngrok confirmed the tunnel was ready")
}

// Launch starts ngrok bound to url (the reserved static domain) pointed
// at targetPort, waiting until it confirms it's actually serving (or
// ctx's deadline is hit). The returned *exec.Cmd is already running -
// the caller owns its lifecycle (Wait()/Process.Kill()) from here.
func Launch(ctx context.Context, binPath, url, homeDir string, targetPort int) (cmd *exec.Cmd, err error) {
	cmd = exec.Command(binPath, "http", "--url="+url, fmt.Sprintf("%d", targetPort), "--log=stdout")
	// ngrok reads its authtoken/config from $HOME/.config/ngrok/ngrok.yml -
	// HOME resolves to Android's read-only "/" in this su shell context
	// without an override, the same fix cloudflared's cert.pem needed
	// earlier. SSL_CERT_DIR is the same missing-CA-bundle fix too:
	// statically-linked Go binaries on Android can't find a CA bundle at
	// any of Go's default lookup paths, but Android's own system CA
	// store at this path is already in a format Go's loader can read
	// directly.
	cmd.Env = append(os.Environ(), "HOME="+homeDir, "SSL_CERT_DIR=/system/etc/security/cacerts")

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ngrok: %w", err)
	}

	readyCh := make(chan error, 1)
	// This goroutine must keep draining pr for the entire lifetime of
	// the ngrok process, not just until the ready/error line is found -
	// confirmed live as a real deadlock 2026-07-12: os/exec's internal
	// copy goroutine keeps writing ngrok's ongoing heartbeat/connection
	// log lines into pw for as long as the process runs, and once the
	// OS pipe buffer (~64KB) fills with nothing reading it, that write
	// blocks forever, which in turn wedges cmd.Wait() (it waits for
	// this copy goroutine to finish) - the whole service hung rather
	// than restarting. reported latches so readyCh is only ever sent to
	// once, but scanning continues regardless so the pipe never stalls.
	go func() {
		scanner := bufio.NewScanner(pr)
		reported := false
		for scanner.Scan() {
			if reported {
				continue
			}
			ready, lineErr := classifyLine(scanner.Text())
			if lineErr != nil {
				reported = true
				readyCh <- lineErr
			} else if ready {
				reported = true
				readyCh <- nil
			}
		}
		if !reported {
			if err := scanner.Err(); err != nil {
				readyCh <- err
			} else {
				readyCh <- fmt.Errorf("stream ended before ngrok confirmed the tunnel was ready")
			}
		}
	}()

	select {
	case err := <-readyCh:
		if err != nil {
			cmd.Process.Kill()
			return nil, err
		}
		return cmd, nil
	case <-ctx.Done():
		cmd.Process.Kill()
		return nil, fmt.Errorf("timed out waiting for ngrok to become ready: %w", ctx.Err())
	}
}
