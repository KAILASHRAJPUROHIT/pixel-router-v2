package store

import (
	"testing"
	"time"
)

func TestDeviceLifecycle(t *testing.T) {
	st := newTestStore(t)

	if err := st.UpsertDeviceSeen("aa:bb:cc:dd:ee:ff", "5GHz", 1000); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertDeviceSeen("aa:bb:cc:dd:ee:ff", "5GHz", 500); err != nil {
		t.Fatal(err)
	}

	devices, err := st.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devices))
	}
	if devices[0].CumulativeBytes != 1500 {
		t.Fatalf("expected cumulative 1500, got %d", devices[0].CumulativeBytes)
	}
	if devices[0].Name != "" {
		t.Fatalf("expected empty name by default, got %q", devices[0].Name)
	}

	if err := st.SetDeviceName("aa:bb:cc:dd:ee:ff", "Living Room TV"); err != nil {
		t.Fatal(err)
	}
	devices, _ = st.ListDevices()
	if devices[0].Name != "Living Room TV" {
		t.Fatalf("expected name to be set, got %q", devices[0].Name)
	}
}

func TestPruneStaleDevices(t *testing.T) {
	st := newTestStore(t)

	st.UpsertDeviceSeen("11:11:11:11:11:11", "5GHz", 100)
	st.UpsertDeviceSeen("22:22:22:22:22:22", "2.4GHz", 100)

	// Manually backdate one device's last_seen to simulate it being stale.
	if _, err := st.db.Exec(`UPDATE devices SET last_seen = ? WHERE mac = ?`,
		time.Now().Add(-5*24*time.Hour).Unix(), "11:11:11:11:11:11"); err != nil {
		t.Fatal(err)
	}

	n, err := st.PruneStaleDevices(4 * 24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 pruned, got %d", n)
	}

	devices, _ := st.ListDevices()
	if len(devices) != 1 {
		t.Fatalf("expected 1 remaining device, got %d", len(devices))
	}
	if devices[0].MAC != "22:22:22:22:22:22" {
		t.Fatalf("expected the fresh device to survive, got %s pruned instead", devices[0].MAC)
	}
}
