package com.router.aod;

import android.graphics.Color;

/**
 * A color palette applied on top of whichever layout is chosen -
 * layout (structure) and theme (color) are independent choices, same
 * split as the "8 styles x 6 layouts" concept mockups this was built
 * from. Each theme supplies enough colors to recolor any of the six
 * layouts consistently (background, panel/card fill, muted label text,
 * primary value text, and one or two accent colors for the clock and
 * highlighted stats).
 */
public class AodTheme {

    public final String name;
    public final int background;
    public final int panel;
    public final int textMuted;
    public final int textPrimary;
    public final int accent;
    public final int accent2; // second accent for a two-color clock gradient
    public final String fontFamily; // null = default sans-serif

    public AodTheme(String name, int background, int panel, int textMuted,
                     int textPrimary, int accent, int accent2, String fontFamily) {
        this.name = name;
        this.background = background;
        this.panel = panel;
        this.textMuted = textMuted;
        this.textPrimary = textPrimary;
        this.accent = accent;
        this.accent2 = accent2;
        this.fontFamily = fontFamily;
    }

    public static final AodTheme[] ALL = new AodTheme[]{
            new AodTheme("Neon cyberpunk",
                    Color.parseColor("#050014"), Color.parseColor("#12071f"),
                    Color.parseColor("#7a8099"), Color.parseColor("#f5f5ff"),
                    Color.parseColor("#ff2d95"), Color.parseColor("#00e5ff"),
                    "monospace"),
            new AodTheme("Glass frost",
                    Color.parseColor("#3b2f80"), Color.parseColor("#4a3d99"),
                    Color.parseColor("#c9c3ea"), Color.parseColor("#ffffff"),
                    Color.parseColor("#ffffff"), Color.parseColor("#d6cdfb"),
                    null),
            new AodTheme("Retro CRT terminal",
                    Color.parseColor("#020402"), Color.parseColor("#08170a"),
                    Color.parseColor("#0a6b2e"), Color.parseColor("#00ff66"),
                    Color.parseColor("#00ff66"), Color.parseColor("#00ff66"),
                    "monospace"),
            new AodTheme("Luxury gold",
                    Color.parseColor("#0d0d0d"), Color.parseColor("#181818"),
                    Color.parseColor("#8a7642"), Color.parseColor("#d4af37"),
                    Color.parseColor("#d4af37"), Color.parseColor("#f4dd8c"),
                    "serif"),
            new AodTheme("Vaporwave sunset",
                    Color.parseColor("#2e1a47"), Color.parseColor("#4a2a63"),
                    Color.parseColor("#c98bb0"), Color.parseColor("#ffe3f1"),
                    Color.parseColor("#ff2d95"), Color.parseColor("#f7b955"),
                    null),
            new AodTheme("E-ink newspaper",
                    Color.parseColor("#f2ead9"), Color.parseColor("#e6dcc4"),
                    Color.parseColor("#6b6b6b"), Color.parseColor("#1a1a1a"),
                    Color.parseColor("#1a1a1a"), Color.parseColor("#4a4a4a"),
                    "serif"),
            new AodTheme("Holographic",
                    Color.parseColor("#08080c"), Color.parseColor("#141420"),
                    Color.parseColor("#8b8fa8"), Color.parseColor("#f0f0ff"),
                    Color.parseColor("#a78bfa"), Color.parseColor("#22d3ee"),
                    null),
            new AodTheme("Nature organic",
                    Color.parseColor("#0b3d2e"), Color.parseColor("#134a38"),
                    Color.parseColor("#a9c9b8"), Color.parseColor("#f4ecd8"),
                    Color.parseColor("#f4ecd8"), Color.parseColor("#8fd4b8"),
                    null),
            // True Black (OLED) - added 2026-07-07. Background AND panel
            // are both pure #000000, not just "dark" like every other
            // theme here - on an actual OLED panel, a pixel driven to
            // exactly (0,0,0) is fully off (no current through that
            // subpixel), which is the whole point of a real AOD: only
            // the pixels under actual text/icons draw power, everything
            // else is a genuinely dead black, not a backlit dark gray.
            // Every other theme's background (e.g. Neon cyberpunk's
            // #050014, Nature organic's #0b3d2e) is non-zero and would
            // keep the whole panel lit at low brightness instead.
            new AodTheme("True Black (OLED)",
                    Color.parseColor("#000000"), Color.parseColor("#000000"),
                    Color.parseColor("#707070"), Color.parseColor("#ffffff"),
                    Color.parseColor("#ffffff"), Color.parseColor("#bbbbbb"),
                    null),
    };
}
