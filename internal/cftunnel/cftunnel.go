// Package cftunnel runs a named Cloudflare Tunnel pointed at the
// webproxy service, giving the dashboard a permanent, real-domain URL
// (router.ambicdigital.in) alongside the existing ngrok tunnel - added
// 2026-07-12 once ambicdigital.in's nameservers finished propagating to
// Cloudflare, unblocking the zone requirement that `cloudflared tunnel
// login` needs (see internal/tunnel's own doc comment for why the
// earlier attempt at this, before any zone existed, hit that wall and
// fell back to ngrok instead).
//
// Deliberately kept as a SEPARATE service from the ngrok-based
// internal/tunnel rather than replacing it - local-router and AKD's
// config still point at the ngrok domain's /local-router path, so
// retiring ngrok is a decision left to the user, not something this
// change forces.
package cftunnel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// GenerateConfig builds the YAML ingress config `cloudflared tunnel
// run` needs - a single-hostname passthrough to targetPort, falling
// through to a 404 for anything else (Cloudflare Tunnel ingress
// requires an explicit catch-all rule as the last entry). Pure and
// testable without a real cloudflared process.
func GenerateConfig(tunnelID, credentialsFile, hostname string, targetPort int) string {
	return fmt.Sprintf(`tunnel: %s
credentials-file: %s
ingress:
  - hostname: %s
    service: http://127.0.0.1:%d
  - service: http_status:404
`, tunnelID, credentialsFile, hostname, targetPort)
}

// classifyLine matches cloudflared's plain (non-JSON) log format, e.g.
// "2026-07-12T10:22:26Z INF Registered tunnel connection connIndex=0
// connection=... event=0 ip=198.41.200.33 location=bom12 protocol=quic"
// - confirmed live 2026-07-12 against a real run.
func classifyLine(line string) (ready bool, failErr error) {
	if strings.Contains(line, "Registered tunnel connection") {
		return true, nil
	}
	if strings.Contains(line, "no more connections active and exiting") {
		return false, fmt.Errorf("cloudflared failed to start: %s", line)
	}
	return false, nil
}

// Launch starts `cloudflared tunnel --config configPath run`, waiting
// until at least one connection registers (or ctx's deadline hits).
// The returned *exec.Cmd is already running - the caller owns its
// lifecycle from here. Keeps draining cloudflared's log output for the
// process's entire lifetime after ready, not just until the ready line
// - the exact same deadlock this project's ngrok launcher hit live
// 2026-07-12 (see internal/tunnel/tunnel.go's doc comment) applies
// identically here: cloudflared keeps emitting connection-retry/
// heartbeat log lines for as long as it runs, and an unread pipe
// eventually blocks the write, wedging cmd.Wait() forever.
func Launch(ctx context.Context, binPath, configPath, homeDir string) (cmd *exec.Cmd, err error) {
	cmd = exec.Command(binPath, "tunnel", "--config", configPath, "run")
	cmd.Env = append(os.Environ(), "HOME="+homeDir, "SSL_CERT_DIR=/system/etc/security/cacerts")

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start cloudflared: %w", err)
	}

	readyCh := make(chan error, 1)
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
				readyCh <- fmt.Errorf("stream ended before cloudflared confirmed the tunnel was ready")
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
		return nil, fmt.Errorf("timed out waiting for cloudflared to become ready: %w", ctx.Err())
	}
}
