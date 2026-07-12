package quota

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRmnetLineRegex_ParsesRealFormat(t *testing.T) {
	// Real /proc/net/dev header + a couple of sample lines, matching
	// the actual column layout: iface: rx_bytes rx_packets ... (8 rx
	// fields) tx_bytes tx_packets ... - field[0]=rx_bytes, field[8]=tx_bytes.
	sample := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:  1234       10    0    0    0     0          0         0     1234       10    0    0    0     0       0          0
rmnet1: 100000      50    0    0    0     0          0         0    20000       30    0    0    0     0       0          0
rmnet5:  5000       10    0    0    0     0          0         0     3000       10    0    0    0     0       0          0
 wlan2:  9999        9    0    0    0     0          0         0     9999        9    0    0    0     0       0          0
`
	dir := t.TempDir()
	path := filepath.Join(dir, "fake_net_dev")
	if err := os.WriteFile(path, []byte(sample), 0644); err != nil {
		t.Fatal(err)
	}

	// Exercise the same line-matching/summing logic as
	// CurrentRmnetBytes(), pointed at our fake file instead of the
	// real /proc/net/dev.
	total, err := sumRmnetBytesFromPath(path)
	if err != nil {
		t.Fatal(err)
	}
	// rmnet1: 100000+20000=120000, rmnet5: 5000+3000=8000, lo/wlan2 excluded.
	want := int64(120000 + 8000)
	if total != want {
		t.Fatalf("expected %d, got %d", want, total)
	}
}
