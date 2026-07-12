package aodcontrol

import "testing"

const samplePrefs = `<?xml version='1.0' encoding='utf-8' standalone='yes' ?>
<map>
    <int name="layout_index" value="23" />
    <int name="theme_index" value="8" />
</map>
`

func TestPatchLayoutIndex_UpdatesOnlyLayout(t *testing.T) {
	got := PatchLayoutIndex(samplePrefs, 5)
	idx, ok := ParseLayoutIndex(got)
	if !ok || idx != 5 {
		t.Fatalf("expected layout_index=5, got %d (ok=%v)", idx, ok)
	}
	themeIdx, ok := ParseThemeIndex(got)
	if !ok || themeIdx != 8 {
		t.Fatalf("expected theme_index unchanged at 8, got %d (ok=%v)", themeIdx, ok)
	}
}

func TestPatchThemeIndex_UpdatesOnlyTheme(t *testing.T) {
	got := PatchThemeIndex(samplePrefs, 2)
	idx, ok := ParseThemeIndex(got)
	if !ok || idx != 2 {
		t.Fatalf("expected theme_index=2, got %d (ok=%v)", idx, ok)
	}
	layoutIdx, ok := ParseLayoutIndex(got)
	if !ok || layoutIdx != 23 {
		t.Fatalf("expected layout_index unchanged at 23, got %d (ok=%v)", layoutIdx, ok)
	}
}

func TestParseLayoutIndex_MissingElement_ReturnsFalse(t *testing.T) {
	_, ok := ParseLayoutIndex("<map></map>")
	if ok {
		t.Fatal("expected ok=false for missing element")
	}
}

func TestLayoutNames_Has30Entries(t *testing.T) {
	if len(LayoutNames) != 30 {
		t.Fatalf("expected 30 layout names, got %d", len(LayoutNames))
	}
}

func TestThemeNames_Has9Entries(t *testing.T) {
	if len(ThemeNames) != 9 {
		t.Fatalf("expected 9 theme names, got %d", len(ThemeNames))
	}
}
