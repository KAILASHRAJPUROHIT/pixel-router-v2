// Package aodstats produces the same 10-stat contract V1's dashboard
// HTML exposed (Down, Up, Devices, Total Used, Battery, Thermal, Skin
// Temp, SMS Fwd, Internet, Signal) as clean JSON instead of HTML for
// the AOD app to regex-scrape. The Android app already fetches over
// HTTP (it was never reading files directly) - only the format and
// endpoint change, from scraping rendered HTML to parsing real JSON
// via org.json (already imported in RouterDreamService.java).
package aodstats

import "fmt"

type Stat struct {
	Label string `json:"label"`
	Value string `json:"value"`
	// Color is a hex string like V1's inline --c:#hex convention -
	// empty means "use the layout's default text color".
	Color string `json:"color,omitempty"`
}

// Sources is every data dependency this package needs, each already
// built and tested elsewhere in V2 - kept as plain function fields
// (same injection pattern as netcontrol/hotspot/usbtether) so Collect
// is unit-testable with fakes, no device required.
type Sources struct {
	DeviceCount      func() (int, error)
	CycleGB          func() (float64, error)
	BatteryPercent   func() (int, error)
	SkinTempC        func() (float64, error)
	DownUpMbps       func() (down, up float64, err error)
	ForwarderHealthy func() (bool, error)
	HotspotUp        func() bool
	USBTetherUp      func() bool
	RSRP             func() (rsrp int, hasReading bool, err error)
}

const (
	colorGood = "#3ecf8e"
	colorWarn = "#f5a623"
	colorCrit = "#ff5468"
)

// Collect gathers all 10 stats, matching V1's stat set and ordering.
// Each source that errors reports "n/a" for that one stat rather than
// failing the whole response - one broken sensor shouldn't blank the
// entire AOD screen, same resilience principle V1's dashboard used
// ("last-known-good" pattern for best-effort stats).
func Collect(src Sources) []Stat {
	stats := make([]Stat, 0, 10)

	down, up, err := call2(src.DownUpMbps)
	if err != nil {
		stats = append(stats, Stat{Label: "Down", Value: "n/a"}, Stat{Label: "Up", Value: "n/a"})
	} else {
		stats = append(stats,
			Stat{Label: "Down", Value: fmt.Sprintf("%.1f Mbps", down)},
			Stat{Label: "Up", Value: fmt.Sprintf("%.1f Mbps", up)})
	}

	if n, err := callInt(src.DeviceCount); err == nil {
		stats = append(stats, Stat{Label: "Devices", Value: fmt.Sprintf("%d", n)})
	} else {
		stats = append(stats, Stat{Label: "Devices", Value: "n/a"})
	}

	if gb, err := callFloat(src.CycleGB); err == nil {
		stats = append(stats, Stat{Label: "Total Used", Value: fmt.Sprintf("%.1f GB", gb)})
	} else {
		stats = append(stats, Stat{Label: "Total Used", Value: "n/a"})
	}

	if pct, err := callInt(src.BatteryPercent); err == nil {
		color := colorGood
		if pct < 30 {
			color = colorCrit
		} else if pct < 50 {
			color = colorWarn
		}
		stats = append(stats, Stat{Label: "Battery", Value: fmt.Sprintf("%d%%", pct), Color: color})
	} else {
		stats = append(stats, Stat{Label: "Battery", Value: "n/a"})
	}

	if temp, err := callFloat(src.SkinTempC); err == nil {
		color := colorGood
		if temp >= 45 {
			color = colorCrit
		} else if temp >= 40 {
			color = colorWarn
		}
		stats = append(stats,
			Stat{Label: "Thermal", Value: fmt.Sprintf("%.0f°C", temp), Color: color},
			Stat{Label: "Skin Temp", Value: fmt.Sprintf("%.1f°C", temp), Color: color})
	} else {
		stats = append(stats, Stat{Label: "Thermal", Value: "n/a"}, Stat{Label: "Skin Temp", Value: "n/a"})
	}

	if src.ForwarderHealthy != nil {
		if ok, err := src.ForwarderHealthy(); err == nil {
			if ok {
				stats = append(stats, Stat{Label: "SMS Fwd", Value: "OK", Color: colorGood})
			} else {
				stats = append(stats, Stat{Label: "SMS Fwd", Value: "ISSUE", Color: colorCrit})
			}
		} else {
			stats = append(stats, Stat{Label: "SMS Fwd", Value: "n/a"})
		}
	} else {
		stats = append(stats, Stat{Label: "SMS Fwd", Value: "n/a"})
	}

	internetValue, internetColor := "Down", colorCrit
	hotspotUp := src.HotspotUp != nil && src.HotspotUp()
	tetherUp := src.USBTetherUp != nil && src.USBTetherUp()
	if hotspotUp || tetherUp {
		internetValue, internetColor = "Up", colorGood
	}
	stats = append(stats, Stat{Label: "Internet", Value: internetValue, Color: internetColor})

	if src.RSRP != nil {
		if rsrp, has, err := src.RSRP(); err == nil && has {
			color := colorGood
			if rsrp < -108 {
				color = colorCrit
			} else if rsrp < -95 {
				color = colorWarn
			}
			stats = append(stats, Stat{Label: "Signal", Value: fmt.Sprintf("%d dBm", rsrp), Color: color})
		} else {
			stats = append(stats, Stat{Label: "Signal", Value: "n/a"})
		}
	} else {
		stats = append(stats, Stat{Label: "Signal", Value: "n/a"})
	}

	return stats
}

func callInt(fn func() (int, error)) (int, error) {
	if fn == nil {
		return 0, fmt.Errorf("not wired up")
	}
	return fn()
}

func callFloat(fn func() (float64, error)) (float64, error) {
	if fn == nil {
		return 0, fmt.Errorf("not wired up")
	}
	return fn()
}

func call2(fn func() (float64, float64, error)) (float64, float64, error) {
	if fn == nil {
		return 0, 0, fmt.Errorf("not wired up")
	}
	return fn()
}
