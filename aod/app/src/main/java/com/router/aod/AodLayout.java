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

    // 10 phone-OEM-style reference mockups added 2026-07-07 - every one
    // of these shows the FULL stat set (all ten stats + health/net/perf/
    // akd badges + panchang + weather), same "nothing omitted"
    // completeness as Bento modern/Mega grid; the mockups themselves
    // only showed a subset, but were reference for visual style only,
    // not a spec for which fields to include. (A first batch of 10 more
    // abstract sci-fi-themed mockups was built the same way and then
    // removed at the user's request as "too raw" - these 10 replaced
    // them.)
    ONE_UI_MINIMAL("One UI minimal (Samsung style)", R.layout.aod_layout_oneui_minimal),
    ONEPLUS_ACCENT("OnePlus Accent (clean & bold)", R.layout.aod_layout_oneplus_accent),
    ONEPLUS_HORIZON("OnePlus Horizon (modern & symmetrical)", R.layout.aod_layout_oneplus_horizon),
    SAMSUNG_ONE_UI_INFO("Samsung One UI information (AOD)", R.layout.aod_layout_oneui_info),
    ONEPLUS_MINIMAL("OnePlus Minimal (elegant)", R.layout.aod_layout_oneplus_minimal),
    SAMSUNG_ULTRA_WATCH("Samsung Ultra watch style", R.layout.aod_layout_watch_style),
    ONEPLUS_GRID("OnePlus Grid (balanced)", R.layout.aod_layout_oneplus_grid),
    ONEPLUS_PRO("OnePlus Pro (smart AOD)", R.layout.aod_layout_oneplus_pro),
    SAMSUNG_ONE_UI_COMPACT("Samsung One UI compact", R.layout.aod_layout_oneui_compact),
    ONEPLUS_FLUX("OnePlus Flux (dynamic)", R.layout.aod_layout_oneplus_flux),

    // 10 "vibrant multi-color" reference mockups added 2026-07-07 - each
    // stat gets its OWN fixed semantic color (wifi/internet=green,
    // download=blue, upload=orange, devices=purple, battery=green,
    // thermal/skin temp=red, signal=default) baked into the layout XML,
    // not the single-accent-color-per-theme model every other layout
    // uses. fixedStatColors=true tells RouterDreamService to leave these
    // rows' text colors alone rather than overwriting them with the
    // current AodTheme's textPrimary on every poll/theme switch - the
    // whole point of these designs IS their own fixed rainbow, immune to
    // the color picker.
    ONE_UI_COLOR("One UI Color (vibrant)", R.layout.aod_layout_oneui_color, true),
    ONEPLUS_BOLD_DYNAMIC("OnePlus Bold (dynamic)", R.layout.aod_layout_oneplus_bold, true),
    NEON_CYBER("Neon Cyber (futuristic)", R.layout.aod_layout_neon_cyber, true),
    SAMSUNG_ONE_UI_PLUS("Samsung One UI+ (colorful cards)", R.layout.aod_layout_oneui_plus, true),
    GRADIENT_FLOW("Gradient Flow (elegant)", R.layout.aod_layout_gradient_flow, true),
    NATURE_GREEN("Nature Green (fresh)", R.layout.aod_layout_nature_green, true),
    SUNSET_VIBES("Sunset Vibes (warm)", R.layout.aod_layout_sunset_vibes, true),
    OCEAN_BLUE("Ocean Blue (calm)", R.layout.aod_layout_ocean_blue, true),
    VIOLET_GLOW("Violet Glow (premium)", R.layout.aod_layout_violet_glow, true),
    RAINBOW_DASH("Rainbow Dash (playful)", R.layout.aod_layout_rainbow_dash, true);

    public final String label;
    public final int layoutRes;
    public final boolean fixedStatColors;

    AodLayout(String label, int layoutRes) {
        this(label, layoutRes, false);
    }

    AodLayout(String label, int layoutRes, boolean fixedStatColors) {
        this.label = label;
        this.layoutRes = layoutRes;
        this.fixedStatColors = fixedStatColors;
    }
}
