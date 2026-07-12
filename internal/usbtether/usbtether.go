// Package usbtether ports V1's usb_tether_on_boot.sh /
// usb_tether_watchdog.sh - both consolidated here into one owning
// service, same reasoning as internal/hotspot.
//
// The core mechanism can't be reimplemented in pure Go: Android's
// TetheringManager.startTethering() is the only API that reliably
// brings up USB NCM tethering on this device (confirmed in V1 via
// extensive live testing - the raw `svc usb setFunctions ncm` gadget-
// function approach reliably fails right after a real reboot even
// with the cable genuinely connected). That API enforces the
// signature|privileged TETHER_PRIVILEGED permission, which `pm grant`
// structurally refuses to hand to any app even as root, and there's no
// `cmd`/`svc` shell equivalent for it at all. V1's fix - a small
// compiled Java tool (TetherStarter.dex, reused here unchanged) run
// via `app_process` as root, so the Binder call itself carries UID 0 -
// is the only proven way in, and there's no Go-native substitute for a
// Binder call into a hidden system-server API. Go's job here is purely
// orchestration: shell out to the exact same already-proven `app_process`
// invocation, then verify the result and retry with backoff, same as
// V1's watchdog did.
package usbtether

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"
)

// IsUp mirrors V1's check - the ncm0 interface simply existing, not a
// deeper carrier-state check. USB NCM tethering not persisting across
// reboots/cable reconnects on this device is exactly the gap this
// whole package exists to close.
func IsUp() bool {
	_, err := net.InterfaceByName("ncm0")
	return err == nil
}

// isUpFn is indirected through a package variable purely so
// EnsureUp's retry/backoff decision logic can be unit-tested against a
// simulated "interface comes up after N attempts" scenario, without a
// real ncm0 interface or root access. Production code never touches
// this directly - only IsUp() above, which tests override via this var.
var isUpFn = IsUp

type Starter interface {
	// Start invokes the actual app_process/TetheringManager call and
	// returns its combined output for logging - errors from the
	// process itself (non-zero exit) are not necessarily failures,
	// since success is judged by IsUp() afterward, same as V1.
	Start() (output string, err error)
}

type RealStarter struct {
	DexPath string
	WorkDir string
}

func (r RealStarter) Start() (string, error) {
	if _, err := os.Stat(r.DexPath); err != nil {
		return "", fmt.Errorf("dex missing at %s: %w", r.DexPath, err)
	}
	if err := os.MkdirAll(r.WorkDir, 0755); err != nil {
		return "", fmt.Errorf("create workdir: %w", err)
	}

	cmd := exec.Command("app_process", r.WorkDir, "TetherStarter")
	cmd.Env = append(os.Environ(), "CLASSPATH="+r.DexPath)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// EnsureUp retries Start() up to maxAttempts times with settleDelay
// between each, checking IsUp() after every attempt - the exact
// pattern V1's boot script used (the real API can need the USB gadget
// stack to settle a bit more on some boots). Returns nil once ncm0 is
// up; a non-nil error after exhausting all attempts is not necessarily
// a real problem - it usually just means no USB data cable is
// currently connected (charger-only, or unplugged), same as V1's log
// message noted.
func EnsureUp(s Starter, maxAttempts int, settleDelay time.Duration) (attempts int, lastOutput string, err error) {
	for attempts = 1; attempts <= maxAttempts; attempts++ {
		out, startErr := s.Start()
		lastOutput = out
		time.Sleep(settleDelay)
		if isUpFn() {
			return attempts, lastOutput, nil
		}
		if startErr != nil {
			lastOutput = fmt.Sprintf("%s (start error: %v)", out, startErr)
		}
	}
	return attempts - 1, lastOutput, fmt.Errorf("ncm0 still not up after %d attempts - normal if no USB data cable is connected", maxAttempts)
}
