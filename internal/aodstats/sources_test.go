package aodstats

import "testing"

// This is the exact bug caught live on-device 2026-07-12: an earlier
// version of parseVirtualSkinTemp used one regex spanning the whole
// dumpsys output, searching for "VIRTUAL-SKIN" and then the NEXT
// "mValue=" after it - but mValue actually appears BEFORE mName on
// each real line, so that regex skipped past the true skin reading
// (41.08475) and grabbed the following, unrelated sensor's value
// (usb_pwr_therm's 45.748), which was high enough to spuriously
// trigger the thermal-safety cutoff.
const realDumpsysSnippet = `	Temperature{mValue=48.445004, mType=-1, mName=gnss_tcxo_therm, mStatus=0}
	Temperature{mValue=41.08475, mType=3, mName=VIRTUAL-SKIN, mStatus=1}
	Temperature{mValue=45.748, mType=-1, mName=usb_pwr_therm, mStatus=0}
	Temperature{mValue=56.000004, mType=9, mName=TPU, mStatus=0}`

func TestParseVirtualSkinTemp_DoesNotLeakIntoNextSensor(t *testing.T) {
	got, err := parseVirtualSkinTemp(realDumpsysSnippet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 41.08475 {
		t.Fatalf("expected the real VIRTUAL-SKIN reading 41.08475, got %v (this is the exact 2026-07-12 regression: grabbing the following sensor's value instead)", got)
	}
}

func TestParseVirtualSkinTemp_TakesLastMatchingLine(t *testing.T) {
	dump := `	Temperature{mValue=40.0, mType=3, mName=VIRTUAL-SKIN, mStatus=1}
	Temperature{mValue=41.5, mType=3, mName=VIRTUAL-SKIN, mStatus=1}`
	got, err := parseVirtualSkinTemp(dump)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 41.5 {
		t.Fatalf("expected the LAST VIRTUAL-SKIN line's value (41.5, matching V1's tail -1 convention), got %v", got)
	}
}

func TestParseVirtualSkinTemp_NoMatch(t *testing.T) {
	if _, err := parseVirtualSkinTemp("nothing relevant here"); err == nil {
		t.Fatal("expected an error when no VIRTUAL-SKIN line is present")
	}
}
