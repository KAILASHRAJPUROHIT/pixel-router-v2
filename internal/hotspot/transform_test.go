package hotspot

import "testing"

func TestPatchBandOptimization(t *testing.T) {
	in := `<Something><boolean name="BandOptimization" value="true" /></Something>`
	want := `<Something><boolean name="BandOptimization" value="false" /></Something>`
	if got := PatchBandOptimization(in); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestPatchBandOptimization_AlreadyFalse_Unchanged(t *testing.T) {
	in := `<boolean name="BandOptimization" value="false" />`
	if got := PatchBandOptimization(in); got != in {
		t.Fatalf("expected no change, got %q", got)
	}
}

func TestPatchPMF(t *testing.T) {
	in := "ssid=Test\nieee80211w=1\nwpa=2\n"
	want := "ssid=Test\nieee80211w=0\nwpa=2\n"
	if got := PatchPMF(in); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestPatchPMF_NoMatch_Unchanged(t *testing.T) {
	in := "ssid=Test\nwpa=2\n"
	if got := PatchPMF(in); got != in {
		t.Fatalf("expected no change, got %q", got)
	}
}

func TestHasACLConfig(t *testing.T) {
	if HasACLConfig("ssid=Test\nwpa=2\n") {
		t.Fatal("expected false for conf without macaddr_acl")
	}
	if !HasACLConfig("ssid=Test\nmacaddr_acl=1\n") {
		t.Fatal("expected true for conf with macaddr_acl")
	}
}

func TestBuildDenyList2Ghz(t *testing.T) {
	got := BuildDenyList2Ghz([]string{"aa:bb:cc:dd:ee:ff", "11:22:33:44:55:66"})
	want := "aa:bb:cc:dd:ee:ff\n11:22:33:44:55:66\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
