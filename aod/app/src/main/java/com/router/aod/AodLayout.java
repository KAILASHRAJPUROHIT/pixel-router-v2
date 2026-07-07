package com.router.aod;

/**
 * The structural layout options - independent of AodTheme (color).
 * Every layout's row0..row9 <include>s expose the same stat_label/
 * stat_value child IDs, so RouterDreamService can bind any of them with
 * one generic routine instead of a per-layout binder class.
 */
public enum AodLayout {
    CORNER_STACK("Corner stack", R.layout.aod_layout_corner),
    CENTERED_RADIAL("Centered radial", R.layout.aod_layout_radial),
    SPLIT_GRID("Split halves + grid", R.layout.aod_layout_split),
    SIDEBAR_RAIL("Sidebar rail", R.layout.aod_layout_sidebar),
    TICKER_MARQUEE("Ticker marquee", R.layout.aod_layout_ticker),
    WIDGET_CASCADE("Widget cascade", R.layout.aod_layout_cascade),
    // "Loud" layouts: all ten stats visible at once, sized as large as
    // possible (autoSizeTextType fills whatever space each row/tile is
    // weighted to get) - meant to be legible from across a room.
    GIANT_STACK("Giant stack", R.layout.aod_layout_giant_stack),
    MEGA_GRID("Mega grid", R.layout.aod_layout_mega_grid),
    // Modern rounded-card bento layout with dedicated Hindu calendar
    // (Panchang) and weather cards, plus the ten stats as varied-size cards.
    BENTO_MODERN("Bento modern", R.layout.aod_layout_bento_modern),
    // Nothing Phone Glyph Matrix-inspired: deliberately sparse, only 4 of
    // the 10 stats shown, one big color-coded health "ring" as the hero
    // glance element - the opposite instinct from the "loud" layouts.
    GLYPH_MINIMAL("Glyph minimal", R.layout.aod_layout_glyph),

    // 20 reference-mockup styles added 2026-07-07 - unlike Glyph
    // Minimal above, every one of these shows the FULL stat set (all
    // ten stats + health/net/perf/akd badges + panchang + weather),
    // same "nothing omitted" completeness as Bento modern/Mega grid;
    // the mockups themselves only showed a subset, but were reference
    // for visual style only, not a spec for which fields to include.
    NOTHING_OS_MATRIX("Nothing OS Matrix", R.layout.aod_layout_nothing_matrix),
    CYBERPUNK_TERMINAL("Cyberpunk Terminal", R.layout.aod_layout_cyberpunk),
    JARVIS_HUD("Jarvis HUD", R.layout.aod_layout_jarvis),
    TESLA_DASHBOARD("Tesla style dashboard", R.layout.aod_layout_tesla),
    GAMING_PC_MONITOR("Gaming PC monitor", R.layout.aod_layout_gaming),
    APPLE_WATCH_ULTRA("Apple Watch Ultra style", R.layout.aod_layout_watch_ultra),
    SPACE_MISSION_CONTROL("Space mission control", R.layout.aod_layout_mission),
    JAPANESE_MINIMAL("Japanese minimal tech", R.layout.aod_layout_japanese),
    MATRIX_AOD("Matrix AOD", R.layout.aod_layout_matrix_aod),
    KAILASH_OS("Kailash.OS creator dashboard", R.layout.aod_layout_kailash_os),
    ONE_UI_MINIMAL("One UI minimal (Samsung style)", R.layout.aod_layout_oneui_minimal),
    ONEPLUS_ACCENT("OnePlus Accent (clean & bold)", R.layout.aod_layout_oneplus_accent),
    ONEPLUS_HORIZON("OnePlus Horizon (modern & symmetrical)", R.layout.aod_layout_oneplus_horizon),
    SAMSUNG_ONE_UI_INFO("Samsung One UI information (AOD)", R.layout.aod_layout_oneui_info),
    ONEPLUS_MINIMAL("OnePlus Minimal (elegant)", R.layout.aod_layout_oneplus_minimal),
    SAMSUNG_ULTRA_WATCH("Samsung Ultra watch style", R.layout.aod_layout_watch_style),
    ONEPLUS_GRID("OnePlus Grid (balanced)", R.layout.aod_layout_oneplus_grid),
    ONEPLUS_PRO("OnePlus Pro (smart AOD)", R.layout.aod_layout_oneplus_pro),
    SAMSUNG_ONE_UI_COMPACT("Samsung One UI compact", R.layout.aod_layout_oneui_compact),
    ONEPLUS_FLUX("OnePlus Flux (dynamic)", R.layout.aod_layout_oneplus_flux);

    public final String label;
    public final int layoutRes;

    AodLayout(String label, int layoutRes) {
        this.label = label;
        this.layoutRes = layoutRes;
    }
}
