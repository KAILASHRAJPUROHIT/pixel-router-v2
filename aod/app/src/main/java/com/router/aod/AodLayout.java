package com.router.aod;

/**
 * The six structural layout options - independent of AodTheme (color).
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
    WIDGET_CASCADE("Widget cascade", R.layout.aod_layout_cascade);

    public final String label;
    public final int layoutRes;

    AodLayout(String label, int layoutRes) {
        this.label = label;
        this.layoutRes = layoutRes;
    }
}
