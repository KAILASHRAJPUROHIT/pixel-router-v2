// Package hotspot ports V1's hotspot_on_boot.sh - a script whose
// comments document several real, hard-won workarounds discovered
// through live debugging (BandOptimization needing to be disabled for
// true simultaneous dual-band, PMF/802.11w breaking one specific
// laptop's WiFi driver, hostapd regenerating its ACL config on every
// start). None of that domain knowledge is being rediscovered here -
// it's ported faithfully. What changes is *how* it's supervised: V1
// needed a file-based mutex because four different scripts (the boot
// script plus three separate watchdogs) could all call this same
// bring-up logic concurrently. In V2, exactly one supervised service
// owns this responsibility, so concurrent-call races structurally
// can't happen - no mutex needed.
//
// This file holds the pure, testable string-transform logic (editing
// XML/conf file *contents*) separately from the actual system calls
// that read/write those files and run `cmd wifi ...` (in controller.go)
// - the same split used in internal/netcontrol, for the same reason:
// V1's equivalent logic could only ever be validated live on the
// device; these specific transforms can be unit tested with no root
// and no phone.
package hotspot

import (
	"regexp"
	"strings"
)

// PatchBandOptimization disables the BandOptimization flag in
// WifiConfigStoreSoftAp.xml - confirmed necessary (not just the
// bridged_2_5 band flag on start-softap) for both hotspot radios to
// come up together immediately instead of the 5GHz leg only appearing
// reactively once a client happened to negotiate it.
func PatchBandOptimization(xml string) string {
	return strings.ReplaceAll(xml,
		`<boolean name="BandOptimization" value="true" />`,
		`<boolean name="BandOptimization" value="false" />`)
}

var pmfRe = regexp.MustCompile(`(?m)^ieee80211w=1$`)

// PatchPMF disables 802.11w/PMF in a live hostapd conf - Android
// regenerates it with ieee80211w=1 (PMF-optional) on every
// start-softap, a combination that broke one real laptop's WiFi driver
// even with the correct password (confirmed: connected instantly once
// PMF was off, failed every time with it on).
func PatchPMF(conf string) string {
	return pmfRe.ReplaceAllString(conf, "ieee80211w=0")
}

// HasACLConfig reports whether a hostapd conf already has the
// macaddr_acl directive - hostapd regenerates its conf (wiping any
// previously-appended ACL lines) on every single start-softap, so this
// needs to be re-checked and re-appended every time, not applied once.
func HasACLConfig(conf string) bool {
	return strings.Contains(conf, "macaddr_acl")
}

// BuildACLAppend returns the lines to append to a hostapd conf to wire
// up a deny-file-based MAC ACL for the 2.4GHz radio specifically -
// denying specific devices there (not blocking them outright) so their
// OS roams to 5GHz on its own, since the same SSID broadcasts on both.
func BuildACLAppend(denyFilePath string) string {
	return "\nmacaddr_acl=1\ndeny_mac_file=" + denyFilePath + "\n"
}

// BuildDenyList2Ghz renders the deny-file contents for MACs that
// should be pushed off the 2.4GHz radio.
func BuildDenyList2Ghz(macs []string) string {
	var sb strings.Builder
	for _, m := range macs {
		sb.WriteString(m)
		sb.WriteString("\n")
	}
	return sb.String()
}
