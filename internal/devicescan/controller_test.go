package devicescan

import "testing"

// Real ip neigh output captured live 2026-07-12 from the exact incident
// this package fixes (a TV connected, showing 0 devices in aod-stats).
const realIPNeighOutput = `10.76.148.211 lladdr 16:97:bc:f8:01:71 STALE
10.76.148.165 lladdr 1c:d1:d7:b9:36:f2 REACHABLE
10.76.148.1 lladdr f6:b6:34:89:8b:0d STALE
10.76.148.68 lladdr 2e:12:40:6f:e9:6c STALE
fe80::230:ddba:36eb:a9dd lladdr 1c:d1:d7:b9:36:f2 REACHABLE
2401:4900:c0e6:5aed:d0e9:1ead:6022:ff3b lladdr 1c:d1:d7:b9:36:f2 REACHABLE
fe80::1cc6:12c5:3196:5238 lladdr 16:97:bc:f8:01:71 STALE
2401:4900:c0e6:5aed:5443:2c18:669c:a274  FAILED
fe80::8f5:28c0:2e0a:2afe lladdr 2e:12:40:6f:e9:6c STALE
2401:4900:c0e6:5aed:74e5:e65e:7500:17a8 lladdr 2e:12:40:6f:e9:6c STALE
2401:4900:c0e6:5aed:5476:4606:659c:4baa  FAILED
fe80::cb0:4f16:2ed2:a317 lladdr f6:b6:34:89:8b:0d STALE
2401:4900:c0e6:5aed:2db9:cf7e:c294:17bf lladdr f6:b6:34:89:8b:0d STALE`

func TestParseLiveClients_OnlyReachableCounted(t *testing.T) {
	clients := parseLiveClients(realIPNeighOutput, "ap_br_wlan2")
	// Only 1c:d1:d7:b9:36:f2 has any REACHABLE row in this snapshot -
	// the other three MACs (16:97:bc:f8:01:71, f6:b6:34:89:8b:0d,
	// 2e:12:40:6f:e9:6c) only ever appear as STALE here.
	if len(clients) == 0 {
		t.Fatal("expected at least one live client")
	}
	for _, c := range clients {
		if c.MAC != "1c:d1:d7:b9:36:f2" {
			t.Fatalf("expected only the REACHABLE MAC, got %s", c.MAC)
		}
	}
}

func TestParseLiveClients_DedupesMultipleRowsPerMAC(t *testing.T) {
	// 1c:d1:d7:b9:36:f2 appears 3 times (IPv4 + link-local IPv6 + global
	// IPv6), all REACHABLE - should collapse to one client.
	clients := dedupeByMAC(parseLiveClients(realIPNeighOutput, "ap_br_wlan2"))
	count := 0
	for _, c := range clients {
		if c.MAC == "1c:d1:d7:b9:36:f2" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one deduped entry for the repeated MAC, got %d", count)
	}
}

func TestParseLiveClients_StaleAndFailedExcluded(t *testing.T) {
	dump := `10.0.0.5 lladdr aa:bb:cc:dd:ee:ff STALE
10.0.0.6  FAILED
10.0.0.7 lladdr 11:22:33:44:55:66 INCOMPLETE`
	clients := parseLiveClients(dump, "wlan2")
	if len(clients) != 0 {
		t.Fatalf("expected no clients (all STALE/FAILED/INCOMPLETE), got %v", clients)
	}
}

func TestParseLiveClients_ReachableDelayProbeAllCount(t *testing.T) {
	dump := `10.0.0.1 lladdr aa:aa:aa:aa:aa:aa REACHABLE
10.0.0.2 lladdr bb:bb:bb:bb:bb:bb DELAY
10.0.0.3 lladdr cc:cc:cc:cc:cc:cc PROBE`
	clients := parseLiveClients(dump, "wlan2")
	if len(clients) != 3 {
		t.Fatalf("expected all 3 live states to count, got %d: %v", len(clients), clients)
	}
}

func TestDedupeByMAC_KeepsFirstBand(t *testing.T) {
	in := []Client{
		{MAC: "aa:aa:aa:aa:aa:aa", Band: "ap_br_wlan2"},
		{MAC: "aa:aa:aa:aa:aa:aa", Band: "ncm0"},
		{MAC: "bb:bb:bb:bb:bb:bb", Band: "ncm0"},
	}
	out := dedupeByMAC(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 deduped clients, got %d", len(out))
	}
	if out[0].Band != "ap_br_wlan2" {
		t.Fatalf("expected first-seen band to win, got %s", out[0].Band)
	}
}
