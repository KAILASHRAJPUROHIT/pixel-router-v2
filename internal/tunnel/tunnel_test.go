package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestWaitForReady_ConfirmsOnSessionStarted(t *testing.T) {
	log := `t=2026-07-12T05:45:05+0000 lvl=info msg="starting web service" obj=web addr=127.0.0.1:4040
t=2026-07-12T05:45:05+0000 lvl=info msg="client session established" obj=tunnels.session
t=2026-07-12T05:45:05+0000 lvl=info msg="tunnel session started" obj=tunnels.session
`
	if err := WaitForReady(strings.NewReader(log)); err != nil {
		t.Fatal(err)
	}
}

func TestWaitForReady_ReturnsErrorOnDuplicateEndpoint(t *testing.T) {
	// The exact ERR_NGROK_334 failure hit live 2026-07-12 when a second
	// ngrok agent (the phone's) tried to bind a domain another agent
	// (the PC's local-router tunnel) already held.
	log := `t=2026-07-12T05:45:05+0000 lvl=info msg="client session established" obj=tunnels.session
t=2026-07-12T05:45:05+0000 lvl=eror msg="session closing" obj=tunnels.session err="failed to start tunnel: already online"
t=2026-07-12T05:45:05+0000 lvl=crit msg="command failed" err="failed to start tunnel: already online"
`
	err := WaitForReady(strings.NewReader(log))
	if err == nil {
		t.Fatal("expected an error for a failed startup, got nil")
	}
}

func TestWaitForReady_StreamEndsWithoutConfirmation_ReturnsError(t *testing.T) {
	log := "just some unrelated log lines\nnothing to see here\n"
	if err := WaitForReady(strings.NewReader(log)); err == nil {
		t.Fatal("expected an error when the stream ends without a ready confirmation")
	}
}

func TestLaunch_KeepsDrainingOutputAfterReady_DoesNotDeadlock(t *testing.T) {
	// Regression test for a real deadlock hit live 2026-07-12: once the
	// ready line was found, the old code stopped reading the pipe
	// entirely, so once ngrok's continuing heartbeat/connection log
	// lines filled the OS pipe buffer (~64KB), its write blocked forever
	// and cmd.Wait() never returned. Using `sh -c` here to emit the
	// ready line followed by enough further output to exceed a typical
	// 64KB pipe buffer, then exit - Launch must return promptly, and
	// cmd.Wait() afterward must also return promptly (not hang).
	script := `echo 'msg="tunnel session started"'; ` +
		`for i in $(seq 1 2000); do echo "heartbeat line $i padding padding padding padding padding padding"; done`

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.Command("sh", "-c", script)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		t.Skipf("sh not available to run this regression test: %v", err)
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
			readyCh <- fmt.Errorf("stream ended before ready")
		}
	}()

	select {
	case err := <-readyCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for ready signal")
	}

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case <-waitDone:
	case <-time.After(10 * time.Second):
		t.Fatal("cmd.Wait() deadlocked - the pipe was never drained after the ready line")
	}
}
