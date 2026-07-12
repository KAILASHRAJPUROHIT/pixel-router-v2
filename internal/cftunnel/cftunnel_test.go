package cftunnel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"testing"
	"time"
)

func TestGenerateConfig_ProducesExpectedYAML(t *testing.T) {
	got := GenerateConfig("abc-123", "/creds/abc-123.json", "router.ambicdigital.in", 9091)
	want := `tunnel: abc-123
credentials-file: /creds/abc-123.json
ingress:
  - hostname: router.ambicdigital.in
    service: http://127.0.0.1:9091
  - service: http_status:404
`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestClassifyLine_RecognizesReadyAndFailure(t *testing.T) {
	ready, err := classifyLine(`2026-07-12T10:22:26Z INF Registered tunnel connection connIndex=0`)
	if !ready || err != nil {
		t.Fatalf("expected ready=true err=nil, got ready=%v err=%v", ready, err)
	}

	ready, err = classifyLine(`2026-07-12T10:22:35Z ERR no more connections active and exiting`)
	if ready || err == nil {
		t.Fatalf("expected ready=false with an error, got ready=%v err=%v", ready, err)
	}

	ready, err = classifyLine(`2026-07-12T10:22:25Z INF Starting tunnel tunnelID=abc`)
	if ready || err != nil {
		t.Fatalf("expected an unrelated info line to be neither ready nor an error, got ready=%v err=%v", ready, err)
	}
}

func TestLaunch_KeepsDrainingOutputAfterReady_DoesNotDeadlock(t *testing.T) {
	// Regression test mirroring internal/tunnel's own - a process that
	// prints its ready line then keeps producing enough further output
	// to exceed a typical 64KB pipe buffer must not wedge cmd.Wait().
	script := `echo 'msg="Registered tunnel connection" connIndex=0'; ` +
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
