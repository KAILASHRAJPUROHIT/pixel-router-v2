// Package devicescan is the missing half of V2's per-device tracking:
// internal/store/devices.go had the SQLite persistence layer
// (UpsertDeviceSeen/ListDevices/PruneStaleDevices) built and tested
// from early in the V2 rebuild, but nothing in any cmd/*/main.go ever
// actually called UpsertDeviceSeen - confirmed live 2026-07-12 that a
// TV genuinely connected to the hotspot the whole time while
// /internal/aod-stats kept reporting "Devices: 0", because the table
// was simply never populated. This package is the scanner that was
// missing.
package devicescan

import "time"

// Client is one live neighbor-table entry, deduped by MAC (a device
// can show multiple rows - IPv4, IPv6, link-local - sharing one MAC).
type Client struct {
	MAC  string
	Band string // which interface it was seen on (ap_br_wlan2/wlan2 for hotspot, ncm0 for USB tether)
}

type Controller interface {
	// ListLiveClients returns only genuinely-connected clients - V1's
	// update.sh only trusted REACHABLE/DELAY/PROBE neighbor states,
	// specifically excluding STALE (a device that roamed away or slept
	// but whose neighbor-table entry hasn't aged out yet) since counting
	// those made the dashboard show ghost devices as "Active".
	ListLiveClients() ([]Client, error)
}

// DeviceStore is the subset of *store.Store this package needs -
// narrowed to keep this package's dependency on store minimal and this
// interface easy to fake in tests.
type DeviceStore interface {
	UpsertDeviceSeen(mac, band string, bytesDelta int64) error
	PruneStaleDevices(olderThan time.Duration) (int, error)
}

const staleDeviceRetention = 7 * 24 * time.Hour

// Tick runs one scan pass: record every currently-live client (0 byte
// delta - V2 doesn't yet attribute NAT traffic to individual source
// IPs, so presence/first-seen/last-seen tracking is what this restores
// first; cumulative_bytes stays at whatever it already was) and prune
// anything not seen in staleDeviceRetention, matching V1's cleanup
// pass motivation (orphaned per-device entries sitting dead for days).
func Tick(ctrl Controller, st DeviceStore) (seen int, pruned int, err error) {
	clients, err := ctrl.ListLiveClients()
	if err != nil {
		return 0, 0, err
	}
	for _, c := range clients {
		if upsertErr := st.UpsertDeviceSeen(c.MAC, c.Band, 0); upsertErr != nil {
			err = upsertErr
			continue
		}
		seen++
	}
	n, pruneErr := st.PruneStaleDevices(staleDeviceRetention)
	if pruneErr != nil && err == nil {
		err = pruneErr
	}
	return seen, n, err
}
