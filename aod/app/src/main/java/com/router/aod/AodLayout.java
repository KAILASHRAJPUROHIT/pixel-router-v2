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
    GLYPH_MINIMAL("Glyph minimal", R.layout.aod_layout_glyph);

    public final String label;
    public final int layoutRes;

    AodLayout(String label, int layoutRes) {
        this.label = label;
        this.layoutRes = layoutRes;
    }
}
