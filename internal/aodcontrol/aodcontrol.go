// Package aodcontrol ports V1's set_aod_layout.cgi/set_aod_theme.cgi -
// switches the already-installed com.router.aod app's active layout/
// theme by editing its own shared_prefs XML directly, then forcing a
// fresh render. No Android app rebuild is involved: all 30 layouts and
// 9 themes are Java enums already compiled into the APK from earlier
// work on this project - this package only ever changes which INDEX
// is currently selected, the same way V1's CGI scripts did.
package aodcontrol

import (
	"fmt"
	"regexp"
)

// LayoutNames must stay in the exact order of the app's AodLayout.java
// enum (its ordinal is what layout_index actually stores) - there's no
// way to query the enum's labels from outside the APK, so this list is
// manually kept in sync with that file, same as V1's dashboard did.
var LayoutNames = []string{
	"Corner stack", "Centered radial", "Split halves + grid", "Sidebar rail",
	"Ticker marquee", "Widget cascade", "Giant stack", "Mega grid",
	"Bento modern", "Glyph minimal", "One UI minimal (Samsung style)",
	"OnePlus Accent (clean & bold)", "OnePlus Horizon (modern & symmetrical)",
	"Samsung One UI information (AOD)", "OnePlus Minimal (elegant)",
	"Samsung Ultra watch style", "OnePlus Grid (balanced)", "OnePlus Pro (smart AOD)",
	"Samsung One UI compact", "OnePlus Flux (dynamic)", "One UI Color (vibrant)",
	"OnePlus Bold (dynamic)", "Neon Cyber (futuristic)", "Samsung One UI+ (colorful cards)",
	"Gradient Flow (elegant)", "Nature Green (fresh)", "Sunset Vibes (warm)",
	"Ocean Blue (calm)", "Violet Glow (premium)", "Rainbow Dash (playful)",
}

// ThemeNames mirrors AodTheme.java's ALL[] array (its index is what
// theme_index actually stores) - same manual-sync caveat as above.
var ThemeNames = []string{
	"Neon cyberpunk", "Glass frost", "Retro CRT terminal", "Luxury gold",
	"Vaporwave sunset", "E-ink newspaper", "Holographic", "Nature organic",
	"True Black (OLED)",
}

var layoutIndexRe = regexp.MustCompile(`layout_index" value="[0-9]+"`)
var themeIndexRe = regexp.MustCompile(`theme_index" value="[0-9]+"`)

// PatchLayoutIndex/PatchThemeIndex rewrite the one <int .../> element
// aod_prefs.xml's index value lives in - pure string transforms, split
// out from the actual file I/O so they're unit-testable without a
// device, same pattern as internal/hotspot's transform.go.
func PatchLayoutIndex(xml string, idx int) string {
	return layoutIndexRe.ReplaceAllString(xml, fmt.Sprintf(`layout_index" value="%d"`, idx))
}

func PatchThemeIndex(xml string, idx int) string {
	return themeIndexRe.ReplaceAllString(xml, fmt.Sprintf(`theme_index" value="%d"`, idx))
}

// ParseLayoutIndex/ParseThemeIndex read the current value back out, for
// reporting to the dashboard which layout/theme is presently active.
func ParseLayoutIndex(xml string) (int, bool) {
	return parseIndex(xml, layoutIndexRe)
}

func ParseThemeIndex(xml string) (int, bool) {
	return parseIndex(xml, themeIndexRe)
}

var indexValueRe = regexp.MustCompile(`value="([0-9]+)"`)

func parseIndex(xml string, elemRe *regexp.Regexp) (int, bool) {
	elem := elemRe.FindString(xml)
	if elem == "" {
		return 0, false
	}
	m := indexValueRe.FindStringSubmatch(elem)
	if m == nil {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(m[1], "%d", &n); err != nil {
		return 0, false
	}
	return n, true
}
