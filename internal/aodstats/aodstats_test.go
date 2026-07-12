package aodstats

import (
	"errors"
	"testing"
)

func findStat(stats []Stat, label string) (Stat, bool) {
	for _, s := range stats {
		if s.Label == label {
			return s, true
		}
	}
	return Stat{}, false
}

func fullHealthySources() Sources {
	return Sources{
		DeviceCount:      func() (int, error) { return 3, nil },
		CycleGB:          func() (float64, error) { return 12.5, nil },
		BatteryPercent:   func() (int, error) { return 80, nil },
		SkinTempC:        func() (float64, error) { return 35.0, nil },
		DownUpMbps:       func() (float64, float64, error) { return 50.2, 10.1, nil },
		ForwarderHealthy: func() (bool, error) { return true, nil },
		HotspotUp:        func() bool { return true },
		USBTetherUp:      func() bool { return false },
		RSRP:             func() (int, bool, error) { return -72, true, nil },
	}
}

func TestCollect_AllHealthy_ReturnsAllTenStats(t *testing.T) {
	stats := Collect(fullHealthySources())
	if len(stats) != 10 {
		t.Fatalf("expected 10 stats, got %d", len(stats))
	}
	wantLabels := []string{"Down", "Up", "Devices", "Total Used", "Battery", "Thermal", "Skin Temp", "SMS Fwd", "Internet", "Signal"}
	for _, label := range wantLabels {
		if _, ok := findStat(stats, label); !ok {
			t.Errorf("missing stat %q", label)
		}
	}
}

func TestCollect_DeviceCountError_ReportsNA_DoesNotBlankOthers(t *testing.T) {
	src := fullHealthySources()
	src.DeviceCount = func() (int, error) { return 0, errors.New("db error") }

	stats := Collect(src)
	dc, _ := findStat(stats, "Devices")
	if dc.Value != "n/a" {
		t.Fatalf("expected n/a for Devices, got %q", dc.Value)
	}
	// Every other stat should still be present and correct.
	batt, _ := findStat(stats, "Battery")
	if batt.Value != "80%" {
		t.Fatalf("expected Battery unaffected by Devices error, got %q", batt.Value)
	}
}

func TestCollect_NilSources_AllReportNA_NoPanic(t *testing.T) {
	stats := Collect(Sources{})
	if len(stats) != 10 {
		t.Fatalf("expected 10 stats even with nothing wired up, got %d", len(stats))
	}
	for _, s := range stats {
		// Internet is intentionally the one exception: with no
		// HotspotUp/USBTetherUp source configured, both default to
		// false, giving a real "Down" - a safer default for
		// connectivity than reporting "unknown", unlike every other
		// stat here which genuinely has no fallback value to assume.
		if s.Label == "Internet" {
			if s.Value != "Down" {
				t.Errorf("expected Internet=Down (safe default) with no sources wired, got %q", s.Value)
			}
			continue
		}
		if s.Value != "n/a" {
			t.Errorf("expected n/a for %q with no sources wired, got %q", s.Label, s.Value)
		}
	}
}

func TestCollect_BatteryColorThresholds(t *testing.T) {
	cases := []struct {
		pct       int
		wantColor string
	}{
		{80, colorGood},
		{45, colorWarn},
		{20, colorCrit},
	}
	for _, c := range cases {
		src := fullHealthySources()
		src.BatteryPercent = func() (int, error) { return c.pct, nil }
		stats := Collect(src)
		batt, _ := findStat(stats, "Battery")
		if batt.Color != c.wantColor {
			t.Errorf("battery %d%%: expected color %s, got %s", c.pct, c.wantColor, batt.Color)
		}
	}
}

func TestCollect_ThermalColorThresholds(t *testing.T) {
	cases := []struct {
		temp      float64
		wantColor string
	}{
		{30, colorGood},
		{42, colorWarn},
		{50, colorCrit},
	}
	for _, c := range cases {
		src := fullHealthySources()
		src.SkinTempC = func() (float64, error) { return c.temp, nil }
		stats := Collect(src)
		th, _ := findStat(stats, "Thermal")
		if th.Color != c.wantColor {
			t.Errorf("temp %.0f: expected color %s, got %s", c.temp, c.wantColor, th.Color)
		}
	}
}

func TestCollect_InternetUp_WhenEitherHotspotOrTetherUp(t *testing.T) {
	src := fullHealthySources()
	src.HotspotUp = func() bool { return false }
	src.USBTetherUp = func() bool { return true }
	stats := Collect(src)
	inet, _ := findStat(stats, "Internet")
	if inet.Value != "Up" {
		t.Fatalf("expected Internet=Up when tether is up even if hotspot is down, got %q", inet.Value)
	}
}

func TestCollect_InternetDown_WhenBothDown(t *testing.T) {
	src := fullHealthySources()
	src.HotspotUp = func() bool { return false }
	src.USBTetherUp = func() bool { return false }
	stats := Collect(src)
	inet, _ := findStat(stats, "Internet")
	if inet.Value != "Down" || inet.Color != colorCrit {
		t.Fatalf("expected Internet=Down/crit when both are down, got %q/%s", inet.Value, inet.Color)
	}
}

func TestCollect_SignalNoReading_ReportsNA(t *testing.T) {
	src := fullHealthySources()
	src.RSRP = func() (int, bool, error) { return 0, false, nil }
	stats := Collect(src)
	sig, _ := findStat(stats, "Signal")
	if sig.Value != "n/a" {
		t.Fatalf("expected n/a when no signal reading is available, got %q", sig.Value)
	}
}
