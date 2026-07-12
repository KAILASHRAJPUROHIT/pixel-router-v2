package quota

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var rmnetLineRe = regexp.MustCompile(`^\s*(rmnet\d+):\s*(.*)$`)

// CurrentRmnetBytes sums RX+TX across all rmnet* interfaces from
// /proc/net/dev - cellular-only by design (never wlan0/wlan2), so
// borrowed-hotspot or LAN browsing never counts against the cellular
// cutoff, same as V1.
func CurrentRmnetBytes() (int64, error) {
	return sumRmnetBytesFromPath("/proc/net/dev")
}

// sumRmnetBytesFromPath holds the actual parsing logic, factored out
// so it can be unit-tested against a synthetic file with the real
// /proc/net/dev column layout, instead of only ever being verifiable
// live on the device.
func sumRmnetBytesFromPath(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var total int64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		m := rmnetLineRe.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		fields := strings.Fields(m[2])
		if len(fields) < 9 {
			continue
		}
		rx, _ := strconv.ParseInt(fields[0], 10, 64)
		tx, _ := strconv.ParseInt(fields[8], 10, 64)
		total += rx + tx
	}
	return total, scanner.Err()
}

type RealController struct {
	// NotifyNumber is the owner's phone number to SMS on cutoff - the
	// same am-broadcast mechanism V1 used to reach its own AOD app's
	// SendSmsReceiver. Leave empty to disable notification (logged by
	// the caller, not an error here).
	NotifyNumber string
}

func (RealController) CurrentRmnetBytes() (int64, error) {
	return CurrentRmnetBytes()
}

func (RealController) DisableMobileData() error {
	return exec.Command("svc", "data", "disable").Run()
}

func (RealController) EnableMobileData() error {
	return exec.Command("svc", "data", "enable").Run()
}

func (r RealController) NotifyCutoff(message string) error {
	if r.NotifyNumber == "" {
		return nil
	}
	return exec.Command("am", "broadcast",
		"-n", "com.router.aod/.SendSmsReceiver",
		"-a", "com.router.aod.SEND_SMS",
		"--es", "number", r.NotifyNumber,
		"--es", "message", message).Run()
}
