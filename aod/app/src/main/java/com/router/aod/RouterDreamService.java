package com.router.aod;

import android.content.SharedPreferences;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.Drawable;
import android.os.Handler;
import android.os.Looper;
import android.service.dreams.DreamService;
import android.text.format.DateFormat;
import android.util.Base64;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.view.Window;
import android.view.WindowManager;
import android.widget.TextView;

import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.Calendar;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Custom always-on display: runs as an Android Daydream, activated
 * whenever the device is idle while charging (this router is plugged in
 * 24/7, so in practice this is effectively always-on). Polls the same
 * local dashboard the busybox-httpd server already serves on this same
 * device (127.0.0.1:8080) and mirrors its stats natively.
 *
 * Layout (structure) and theme (color) are independent, user-selectable
 * via AodSettingsActivity - see AodLayout/AodTheme. All six layouts
 * expose the same row0..row9 <include> IDs (each containing
 * stat_label/stat_value), so this class binds any of them with one
 * generic routine rather than needing a per-layout binder class.
 */
public class RouterDreamService extends DreamService {

    private static final String DASHBOARD_URL = "http://127.0.0.1:8080/";
    // Credentials load at runtime from a file in this app's private data
    // directory (dashboard_auth.properties, mode 600, owned by this
    // app's own UID - readable by nothing else on the device) instead of
    // being compiled into the APK. Security audit 2026-07-05 found the
    // previous approach (BuildConfig.AUTH_USER/AUTH_PASS) put the
    // dashboard password in the DEX constant pool in plaintext,
    // extractable from the APK with a plain `strings` command - low risk
    // while the dashboard was LAN-only, worth fixing now that it's
    // reachable from the public internet. See README for how the
    // private-data file gets deployed (adb push + chown after install).
    private String authUser = "changeme";
    private String authPass = "changeme";

    private void loadCredentials() {
        java.io.File authFile = new java.io.File(getFilesDir(), "dashboard_auth.properties");
        try (BufferedReader reader = new BufferedReader(
                new InputStreamReader(new java.io.FileInputStream(authFile), StandardCharsets.UTF_8))) {
            java.util.Properties props = new java.util.Properties();
            props.load(reader);
            authUser = props.getProperty("auth.user", "changeme");
            authPass = props.getProperty("auth.pass", "changeme");
        } catch (Exception ignored) {
            // File missing (not deployed yet) - falls back to "changeme",
            // matching the previous BuildConfig default behavior.
        }
    }
    private static final long POLL_INTERVAL_MS = 5000;
    private static final long CLOCK_TICK_MS = 1000;

    // Weather: fixed location (this router is stationary, not carried
    // around, and location services are disabled device-wide - see
    // project notes). Open-Meteo needs no API key/account.
    // Coordinates: Boisar, Maharashtra.
    private static final String WEATHER_URL =
            "https://api.open-meteo.com/v1/forecast?latitude=19.8036&longitude=72.7560"
                    + "&current=temperature_2m,weather_code"
                    + "&daily=temperature_2m_max,temperature_2m_min"
                    + "&timezone=Asia%2FKolkata";
    private static final long WEATHER_POLL_INTERVAL_MS = 30 * 60 * 1000; // 30 min

    // Safety cutoffs: a full-brightness polling screen is worth pausing if
    // the device is already running hot or the battery is already low -
    // ends the dream (screen returns to normal sleep) rather than adding
    // more heat/drain on top of an already-stressed device.
    private static final float MAX_SAFE_TEMP_C = 45.0f;
    private static final int MIN_SAFE_BATTERY_PCT = 30;

    // Captures label, the value div's remaining attributes (may contain a
    // --c:#hex inline style for status-colored stats like Thermal/SMS Fwd/
    // Internet/Signal), and the value text itself.
    private static final Pattern STAT_PATTERN = Pattern.compile(
            "<div class=\"label\">([^<]+)</div><div class=\"value[^\"]*\"([^>]*)>([^<]+)</div>");
    private static final Pattern INLINE_COLOR_PATTERN = Pattern.compile("--c:(#[0-9a-fA-F]{3,8})");

    private static final String[] STAT_KEYS = {
            "Down", "Up", "Devices", "Total Used", "Battery",
            "Thermal", "Skin Temp", "SMS Fwd", "Internet", "Signal"
    };
    private static final int[] ROW_IDS = {
            R.id.row0, R.id.row1, R.id.row2, R.id.row3, R.id.row4,
            R.id.row5, R.id.row6, R.id.row7, R.id.row8, R.id.row9
    };
    // Index into STAT_KEYS used for the sidebar layout's two blown-up
    // "headline" stats - null-checked, only present in that one layout.
    private static final int HEADLINE1_INDEX = 4; // Battery
    private static final int HEADLINE2_INDEX = 8; // Internet

    private final Handler mainHandler = new Handler(Looper.getMainLooper());
    private ExecutorService fetchExecutor;

    private AodTheme currentTheme;

    private View bgRoot, panel1, panel2, panel3, railBg, divider1, panchangCard, weatherCard;
    private final View[] rowViews = new View[STAT_KEYS.length];
    private TextView clockView, dateView, panchangView;
    private TextView panchangHeaderView, panchangTithiView, panchangNakshatraView;
    private TextView muhuratBadge;
    private int lastMuhuratMinute = -1;
    private TextView weatherTempView, weatherConditionView;
    private int lastPanchangDay = -1;
    private final TextView[] labelViews = new TextView[STAT_KEYS.length];
    private final TextView[] valueViews = new TextView[STAT_KEYS.length];
    private TextView headline1Label, headline1Value, headline2Label, headline2Value;
    private TextView healthBadge;
    // CPU/GPU clock display, present in every layout.
    private TextView perfBadge;
    // Whether the router is on its own cellular or has failed over to a
    // borrowed family-member hotspot (failover_monitor.sh) - present in
    // every layout, same lightweight-badge pattern as perfBadge/healthBadge.
    private TextView netTypeBadge;

    // Custom message: dashboard-typed text that takes over the entire
    // screen for 5 minutes (server-enforced expiry, see update.sh/
    // message.cgi) instead of the normal stats layout. Added as a
    // programmatic overlay rather than a per-layout XML view so it works
    // across all six selectable AOD layouts without touching any of them.
    private static final String NO_MESSAGE_SENTINEL = "—"; // em dash - update.sh's "nothing active" placeholder
    private TextView messageOverlay;

    private final Runnable clockTick = new Runnable() {
        @Override
        public void run() {
            updateClock();
            mainHandler.postDelayed(this, CLOCK_TICK_MS);
        }
    };

    private final Runnable pollTick = new Runnable() {
        @Override
        public void run() {
            fetchExecutor.execute(RouterDreamService.this::fetchAndUpdate);
            mainHandler.postDelayed(this, POLL_INTERVAL_MS);
        }
    };

    private final Runnable weatherTick = new Runnable() {
        @Override
        public void run() {
            if (weatherTempView != null || weatherConditionView != null) {
                fetchExecutor.execute(RouterDreamService.this::fetchAndUpdateWeather);
            }
            mainHandler.postDelayed(this, WEATHER_POLL_INTERVAL_MS);
        }
    };

    // Burn-in protection: this runs 24/7 while charging, so the same
    // pixels would otherwise stay lit for hours at a stretch. Nudging
    // the whole layout a few px in a slow cycling pattern (same trick
    // OLED TVs and Android's own AOD use) is imperceptible at this
    // brightness but keeps any single pixel from taking sustained wear.
    private static final long BURN_IN_SHIFT_INTERVAL_MS = 60000; // 1 min
    private static final float[][] BURN_IN_OFFSETS_DP = {
            {0, 0}, {4, 0}, {4, 4}, {0, 4}, {-4, 4}, {-4, 0}, {-4, -4}, {0, -4}, {4, -4}
    };
    private int burnInShiftIndex = 0;

    private final Runnable burnInShiftTick = new Runnable() {
        @Override
        public void run() {
            applyBurnInShift();
            mainHandler.postDelayed(this, BURN_IN_SHIFT_INTERVAL_MS);
        }
    };

    private void applyBurnInShift() {
        if (bgRoot == null) return;
        float density = getResources().getDisplayMetrics().density;
        float[] offset = BURN_IN_OFFSETS_DP[burnInShiftIndex % BURN_IN_OFFSETS_DP.length];
        bgRoot.setTranslationX(offset[0] * density);
        bgRoot.setTranslationY(offset[1] * density);
        burnInShiftIndex++;
    }

    // Adaptive brightness: dimmer overnight than during the day, on top
    // of the baseline setScreenBright(false) dream-dim mode - re-checked
    // periodically rather than once, so it actually adapts as the clock
    // crosses the day/night boundary while the dream keeps running.
    private static final long BRIGHTNESS_CHECK_INTERVAL_MS = 10 * 60 * 1000; // 10 min
    // 22:00-06:00 matches idle_photo_switcher.sh's own night window, so the
    // two mechanisms agree on when "night" starts instead of drifting apart.
    private static final int NIGHT_START_HOUR = 22;
    private static final int NIGHT_END_HOUR = 6;
    private static final float DAY_BRIGHTNESS = 0.08f;
    private static final float NIGHT_BRIGHTNESS = 0.01f; // lowest usable value - anything closer to 0 risks the panel going unreadable/near-off on this display

    private final Runnable brightnessTick = new Runnable() {
        @Override
        public void run() {
            applyAdaptiveBrightness();
            mainHandler.postDelayed(this, BRIGHTNESS_CHECK_INTERVAL_MS);
        }
    };

    private void applyAdaptiveBrightness() {
        int hour = Calendar.getInstance().get(Calendar.HOUR_OF_DAY);
        boolean night = hour >= NIGHT_START_HOUR || hour < NIGHT_END_HOUR;
        Window window = getWindow();
        if (window == null) return;
        WindowManager.LayoutParams lp = window.getAttributes();
        lp.screenBrightness = night ? NIGHT_BRIGHTNESS : DAY_BRIGHTNESS;
        window.setAttributes(lp);
    }

    @Override
    public void onAttachedToWindow() {
        super.onAttachedToWindow();
        setInteractive(false);
        setFullscreen(true);
        // Real ambient displays run dim, not at full interactive
        // brightness - this is the single biggest lever for both power
        // draw and heat on a screen that stays on continuously, since
        // display brightness dominates over the lightweight 5s poll loop.
        setScreenBright(false);

        loadCredentials();

        SharedPreferences prefs = getSharedPreferences(
                AodSettingsActivity.PREFS_NAME, MODE_PRIVATE);
        AodLayout[] layouts = AodLayout.values();
        int layoutIdx = clamp(prefs.getInt(AodSettingsActivity.KEY_LAYOUT, 0), 0, layouts.length - 1);
        int themeIdx = clamp(prefs.getInt(AodSettingsActivity.KEY_THEME, 0), 0, AodTheme.ALL.length - 1);
        AodLayout currentLayout = layouts[layoutIdx];
        currentTheme = AodTheme.ALL[themeIdx];

        setContentView(currentLayout.layoutRes);
        bindViews();
        applyTheme(currentTheme);

        // Gentle fade-in on wake into the dream, matching the softer
        // wake/sleep transition One UI 8.5 added instead of AOD content
        // just snapping into place instantly.
        if (bgRoot != null) {
            bgRoot.setAlpha(0f);
            bgRoot.animate().alpha(1f).setDuration(600).start();
        }

        messageOverlay = new TextView(this);
        messageOverlay.setBackgroundColor(Color.BLACK);
        messageOverlay.setTextColor(Color.WHITE);
        messageOverlay.setGravity(Gravity.CENTER);
        messageOverlay.setTextSize(TypedValue.COMPLEX_UNIT_SP, 40);
        messageOverlay.setPadding(64, 64, 64, 64);
        messageOverlay.setVisibility(View.GONE);
        getWindow().addContentView(messageOverlay, new ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));

        fetchExecutor = Executors.newSingleThreadExecutor();
        mainHandler.post(clockTick);
        mainHandler.post(pollTick);
        mainHandler.post(weatherTick);
        burnInShiftIndex = 0;
        mainHandler.postDelayed(burnInShiftTick, BURN_IN_SHIFT_INTERVAL_MS);
        applyAdaptiveBrightness();
        mainHandler.postDelayed(brightnessTick, BRIGHTNESS_CHECK_INTERVAL_MS);
        writeDreamState("dreaming");
    }

    @Override
    public void onDetachedFromWindow() {
        super.onDetachedFromWindow();
        mainHandler.removeCallbacks(clockTick);
        mainHandler.removeCallbacks(pollTick);
        mainHandler.removeCallbacks(weatherTick);
        mainHandler.removeCallbacks(burnInShiftTick);
        mainHandler.removeCallbacks(brightnessTick);
        if (fetchExecutor != null) {
            fetchExecutor.shutdownNow();
        }
        writeDreamState("awake");
    }

    // Lets aod_power_governor.sh tell dreaming from awake with a cheap
    // file read instead of polling "dumpsys power" every 2s - root can
    // always read app-private storage regardless of Android's sandbox,
    // same trick used for the SMS send log.
    private void writeDreamState(String state) {
        try (java.io.FileOutputStream out = openFileOutput("dream_state", MODE_PRIVATE)) {
            out.write(state.getBytes());
        } catch (java.io.IOException e) {
            // best-effort - governor falls back to its own dumpsys check
        }
    }

    private int clamp(int v, int lo, int hi) {
        return Math.max(lo, Math.min(hi, v));
    }

    private void bindViews() {
        clockView = findViewById(R.id.clock);
        dateView = findViewById(R.id.date);
        panchangView = findViewById(R.id.panchang);
        panchangHeaderView = findViewById(R.id.panchang_header);
        panchangTithiView = findViewById(R.id.panchang_tithi);
        panchangNakshatraView = findViewById(R.id.panchang_nakshatra);
        muhuratBadge = findViewById(R.id.muhurat_badge);
        weatherTempView = findViewById(R.id.weather_temp);
        weatherConditionView = findViewById(R.id.weather_condition);
        panchangCard = findViewById(R.id.panchang_card);
        weatherCard = findViewById(R.id.weather_card);
        bgRoot = findViewById(R.id.bg_root);
        panel1 = findViewById(R.id.panel_1);
        panel2 = findViewById(R.id.panel_2);
        panel3 = findViewById(R.id.panel_3);
        railBg = findViewById(R.id.rail_bg);
        divider1 = findViewById(R.id.divider_1);
        headline1Label = findViewById(R.id.headline1_label);
        headline1Value = findViewById(R.id.headline1_value);
        headline2Label = findViewById(R.id.headline2_label);
        headline2Value = findViewById(R.id.headline2_value);
        healthBadge = findViewById(R.id.health_badge);
        perfBadge = findViewById(R.id.perf_badge);
        netTypeBadge = findViewById(R.id.net_type_badge);

        for (int i = 0; i < ROW_IDS.length; i++) {
            View row = findViewById(ROW_IDS[i]);
            rowViews[i] = row;
            labelViews[i] = row.findViewById(R.id.stat_label);
            valueViews[i] = row.findViewById(R.id.stat_value);
            labelViews[i].setText(STAT_KEYS[i]);
            valueViews[i].setText("--");
        }
        if (headline1Label != null) headline1Label.setText(STAT_KEYS[HEADLINE1_INDEX]);
        if (headline2Label != null) headline2Label.setText(STAT_KEYS[HEADLINE2_INDEX]);
    }

    private void applyTheme(AodTheme theme) {
        if (bgRoot != null) bgRoot.setBackgroundColor(theme.background);
        tintPanel(panel1, theme.panel);
        tintPanel(panel2, theme.panel);
        tintPanel(panel3, theme.panel);
        if (railBg != null) railBg.setBackgroundColor(theme.panel);
        if (divider1 != null) divider1.setBackgroundColor(theme.textMuted);

        Typeface bold = theme.fontFamily != null ? Typeface.create(theme.fontFamily, Typeface.BOLD) : null;
        Typeface normal = theme.fontFamily != null ? Typeface.create(theme.fontFamily, Typeface.NORMAL) : null;

        if (clockView != null) {
            clockView.setTextColor(theme.accent);
            if (bold != null) clockView.setTypeface(bold);
        }
        if (dateView != null) dateView.setTextColor(theme.textMuted);
        if (panchangView != null) panchangView.setTextColor(theme.textMuted);
        if (panchangHeaderView != null) panchangHeaderView.setTextColor(theme.accent);
        if (panchangTithiView != null) panchangTithiView.setTextColor(theme.textPrimary);
        if (panchangNakshatraView != null) panchangNakshatraView.setTextColor(theme.textMuted);
        if (muhuratBadge != null) muhuratBadge.setTextColor(theme.accent);
        if (weatherTempView != null) weatherTempView.setTextColor(theme.textPrimary);
        if (weatherConditionView != null) weatherConditionView.setTextColor(theme.textMuted);

        for (View row : rowViews) {
            tintPanelIfPresent(row, theme.panel);
        }
        tintPanelIfPresent(panchangCard, theme.panel);
        tintPanelIfPresent(weatherCard, theme.panel);
        if (healthBadge != null) healthBadge.setTextColor(theme.textMuted);
        if (perfBadge != null) perfBadge.setTextColor(theme.textMuted);
        if (netTypeBadge != null) netTypeBadge.setTextColor(theme.textMuted);

        for (int i = 0; i < labelViews.length; i++) {
            labelViews[i].setTextColor(theme.textMuted);
            valueViews[i].setTextColor(theme.textPrimary);
            if (normal != null) labelViews[i].setTypeface(normal);
            if (bold != null) valueViews[i].setTypeface(bold);
        }
        if (headline1Label != null) headline1Label.setTextColor(theme.textMuted);
        if (headline1Value != null) headline1Value.setTextColor(theme.textPrimary);
        if (headline2Label != null) headline2Label.setTextColor(theme.textMuted);
        if (headline2Value != null) headline2Value.setTextColor(theme.accent);
    }

    private void tintPanel(View panel, int color) {
        if (panel == null) return;
        Drawable bg = panel.getBackground();
        if (bg != null) {
            bg = bg.mutate();
            bg.setTint(color);
            panel.setBackground(bg);
        } else {
            panel.setBackgroundColor(color);
        }
    }

    /**
     * Same as tintPanel, but never invents a background where none
     * exists - used for the generic row0..row9 views, which are plain
     * (backgroundless) in most layouts and should stay that way.
     */
    private void tintPanelIfPresent(View panel, int color) {
        if (panel == null) return;
        Drawable bg = panel.getBackground();
        if (bg == null) return;
        bg = bg.mutate();
        bg.setTint(color);
        panel.setBackground(bg);
    }

    private void updateClock() {
        Calendar now = Calendar.getInstance();
        String time = DateFormat.format(DateFormat.is24HourFormat(this) ? "HH:mm" : "h:mm", now).toString();
        String date = DateFormat.format("EEE, d MMM", now).toString();
        if (clockView != null) clockView.setText(time);
        if (dateView != null) dateView.setText(date);

        int dayOfYear = now.get(Calendar.DAY_OF_YEAR);
        boolean wantsPanchang = panchangView != null || panchangTithiView != null;
        if (wantsPanchang && dayOfYear != lastPanchangDay) {
            lastPanchangDay = dayOfYear;
            HinduPanchang.Result panchang = HinduPanchang.compute(now);
            if (panchangView != null) {
                panchangView.setText(panchang.paksha + " " + panchang.tithi + " · " + panchang.nakshatra);
            }
            if (panchangTithiView != null) {
                panchangTithiView.setText(panchang.paksha + " " + panchang.tithi);
            }
            if (panchangNakshatraView != null) {
                panchangNakshatraView.setText(panchang.nakshatra);
            }
        }

        int minuteOfDay = now.get(Calendar.HOUR_OF_DAY) * 60 + now.get(Calendar.MINUTE);
        if (muhuratBadge != null && minuteOfDay != lastMuhuratMinute) {
            lastMuhuratMinute = minuteOfDay;
            String period = HinduPanchang.currentPeriod(now);
            if (period != null) {
                muhuratBadge.setText(period);
                muhuratBadge.setVisibility(View.VISIBLE);
            } else {
                muhuratBadge.setVisibility(View.GONE);
            }
        }
    }

    private void fetchAndUpdate() {
        try {
            URL url = new URL(DASHBOARD_URL);
            HttpURLConnection conn = (HttpURLConnection) url.openConnection();
            conn.setConnectTimeout(4000);
            conn.setReadTimeout(4000);
            String credentials = authUser + ":" + authPass;
            String basicAuth = "Basic " + Base64.encodeToString(
                    credentials.getBytes(StandardCharsets.UTF_8), Base64.NO_WRAP);
            conn.setRequestProperty("Authorization", basicAuth);

            int code = conn.getResponseCode();
            if (code != 200) {
                return;
            }

            StringBuilder html = new StringBuilder();
            try (BufferedReader reader = new BufferedReader(
                    new InputStreamReader(conn.getInputStream(), StandardCharsets.UTF_8))) {
                String line;
                while ((line = reader.readLine()) != null) {
                    html.append(line).append('\n');
                }
            }
            conn.disconnect();

            java.util.Map<String, String> stats = new java.util.HashMap<>();
            java.util.Map<String, Integer> statColors = new java.util.HashMap<>();
            Matcher m = STAT_PATTERN.matcher(html);
            while (m.find()) {
                String label = m.group(1).trim();
                String attrs = m.group(2);
                // The dashboard's HTML uses raw entities like &deg; meant for
                // browser rendering - decode the handful actually used here
                // since we're displaying the text directly, not in a WebView.
                String value = m.group(3).trim()
                        .replace("&deg;", "°")
                        .replace("&amp;", "&");
                stats.put(label, value);

                Matcher cm = INLINE_COLOR_PATTERN.matcher(attrs);
                if (cm.find()) {
                    try {
                        statColors.put(label, Color.parseColor(cm.group(1)));
                    } catch (IllegalArgumentException ignored) {
                    }
                }
            }

            Float skinTemp = parseLeadingFloat(stats.get("Skin Temp"));
            if (skinTemp != null && skinTemp > MAX_SAFE_TEMP_C) {
                mainHandler.post(() -> finish());
                return;
            }
            Integer batteryPct = parseLeadingInt(stats.get("Battery"));
            if (batteryPct != null && batteryPct < MIN_SAFE_BATTERY_PCT) {
                mainHandler.post(() -> finish());
                return;
            }

            mainHandler.post(() -> {
                for (int i = 0; i < STAT_KEYS.length; i++) {
                    String key = STAT_KEYS[i];
                    String v = stats.get(key);
                    if (v == null) continue;
                    valueViews[i].setText(v);
                    // Status-type stats keep the dashboard's own semantic
                    // color (green ok / amber warn / red down) regardless
                    // of theme - that carries real operational meaning,
                    // not just decoration. Plain numeric stats use the
                    // theme's primary text color.
                    Integer color = statColors.get(key);
                    valueViews[i].setTextColor(color != null ? color : currentTheme.textPrimary);
                }
                if (headline1Value != null) {
                    String v = stats.get(STAT_KEYS[HEADLINE1_INDEX]);
                    if (v != null) headline1Value.setText(v);
                }
                if (headline2Value != null) {
                    String v = stats.get(STAT_KEYS[HEADLINE2_INDEX]);
                    if (v != null) headline2Value.setText(v);
                }
                if (healthBadge != null) {
                    String health = stats.get("Health");
                    if (health != null) {
                        healthBadge.setText(health);
                        Integer color = statColors.get("Health");
                        healthBadge.setTextColor(color != null ? color : currentTheme.textMuted);
                    }
                }
                if (perfBadge != null) {
                    String cpu = stats.get("CPU MHz");
                    String gpu = stats.get("GPU MHz");
                    if (cpu != null && gpu != null) {
                        perfBadge.setText("CPU " + cpu + " · GPU " + gpu);
                    }
                }
                if (netTypeBadge != null) {
                    String netType = stats.get("Internet Type");
                    if (netType != null) {
                        netTypeBadge.setText("Internet: " + netType);
                        Integer color = statColors.get("Internet Type");
                        netTypeBadge.setTextColor(color != null ? color : currentTheme.textMuted);
                    }
                }
                if (messageOverlay != null) {
                    String customMessage = stats.get("Custom Message");
                    if (customMessage != null && !customMessage.isEmpty()
                            && !customMessage.equals(NO_MESSAGE_SENTINEL)) {
                        messageOverlay.setText(customMessage);
                        messageOverlay.setVisibility(View.VISIBLE);
                    } else {
                        messageOverlay.setVisibility(View.GONE);
                    }
                }
            });
        } catch (Exception ignored) {
            // Best-effort poll - a single failed fetch just leaves the
            // last-known values on screen until the next cycle succeeds.
        }
    }

    private void fetchAndUpdateWeather() {
        try {
            URL url = new URL(WEATHER_URL);
            HttpURLConnection conn = (HttpURLConnection) url.openConnection();
            conn.setConnectTimeout(6000);
            conn.setReadTimeout(6000);

            int code = conn.getResponseCode();
            if (code != 200) return;

            StringBuilder body = new StringBuilder();
            try (BufferedReader reader = new BufferedReader(
                    new InputStreamReader(conn.getInputStream(), StandardCharsets.UTF_8))) {
                String line;
                while ((line = reader.readLine()) != null) {
                    body.append(line);
                }
            }
            conn.disconnect();

            JSONObject root = new JSONObject(body.toString());
            JSONObject current = root.getJSONObject("current");
            double temp = current.getDouble("temperature_2m");
            int weatherCode = current.getInt("weather_code");

            JSONObject daily = root.getJSONObject("daily");
            double high = daily.getJSONArray("temperature_2m_max").getDouble(0);
            double low = daily.getJSONArray("temperature_2m_min").getDouble(0);

            String condition = weatherCodeToText(weatherCode);
            String tempText = Math.round(temp) + "°";
            String rangeText = condition + " · H" + Math.round(high) + " L" + Math.round(low);

            mainHandler.post(() -> {
                if (weatherTempView != null) weatherTempView.setText(tempText);
                if (weatherConditionView != null) weatherConditionView.setText(rangeText);
            });
        } catch (Exception ignored) {
            // Best-effort - keeps last-known weather on screen until the
            // next successful poll, same as the dashboard stat fetch.
        }
    }

    /** Maps Open-Meteo's WMO weather codes to a short display label. */
    private String weatherCodeToText(int code) {
        switch (code) {
            case 0: return "Clear";
            case 1: return "Mostly clear";
            case 2: return "Partly cloudy";
            case 3: return "Cloudy";
            case 45:
            case 48: return "Fog";
            case 51:
            case 53:
            case 55: return "Drizzle";
            case 61:
            case 63:
            case 65: return "Rain";
            case 71:
            case 73:
            case 75: return "Snow";
            case 80:
            case 81:
            case 82: return "Rain showers";
            case 95:
            case 96:
            case 99: return "Thunderstorm";
            default: return "—";
        }
    }

    /** Extracts the leading numeric value from strings like "35.1°C" or "70%". */
    private Float parseLeadingFloat(String s) {
        if (s == null) return null;
        Matcher nm = Pattern.compile("[-+]?[0-9]*\\.?[0-9]+").matcher(s);
        if (nm.find()) {
            try {
                return Float.parseFloat(nm.group());
            } catch (NumberFormatException e) {
                return null;
            }
        }
        return null;
    }

    private Integer parseLeadingInt(String s) {
        Float f = parseLeadingFloat(s);
        return f == null ? null : Math.round(f);
    }
}
