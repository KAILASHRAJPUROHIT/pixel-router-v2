package com.router.aod;

import android.graphics.Color;
import android.graphics.drawable.Drawable;
import android.os.Handler;
import android.os.Looper;
import android.service.dreams.DreamService;
import android.text.format.DateFormat;
import android.util.Base64;
import android.view.View;
import android.widget.TextView;

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
 * device (127.0.0.1:8080) and mirrors its stats natively, instead of
 * showing a browser or relying on the periodic lock-screen notification.
 */
public class RouterDreamService extends DreamService {

    private static final String DASHBOARD_URL = "http://127.0.0.1:8080/";
    // Credentials come from dashboard.properties (gitignored, not
    // committed) via BuildConfig - see dashboard.properties.example.
    private static final String AUTH_USER = BuildConfig.AUTH_USER;
    private static final String AUTH_PASS = BuildConfig.AUTH_PASS;
    private static final long POLL_INTERVAL_MS = 5000;
    private static final long CLOCK_TICK_MS = 1000;

    // Safety cutoffs: a full-brightness polling screen is worth pausing if
    // the device is already running hot or the battery is already low -
    // ends the dream (screen returns to normal sleep) rather than adding
    // more heat/drain on top of an already-stressed device.
    private static final float MAX_SAFE_TEMP_C = 45.0f;
    private static final int MIN_SAFE_BATTERY_PCT = 30;

    // Captures label, the value div's remaining attributes (may contain a
    // --c:#hex inline style for status-colored stats like Thermal/SMS Fwd),
    // and the value text itself.
    private static final Pattern STAT_PATTERN = Pattern.compile(
            "<div class=\"label\">([^<]+)</div><div class=\"value[^\"]*\"([^>]*)>([^<]+)</div>");
    private static final Pattern INLINE_COLOR_PATTERN = Pattern.compile("--c:(#[0-9a-fA-F]{3,8})");

    private static final int DEFAULT_ACCENT_COLOR = Color.parseColor("#8FD3FF");
    private static final int NEUTRAL_PILL_COLOR = Color.parseColor("#888888");

    private final Handler mainHandler = new Handler(Looper.getMainLooper());
    private ExecutorService fetchExecutor;

    private TextView clockView, dateView, connStatusView, statusOnlineView, statusSignalView;
    private TextView[] labelViews = new TextView[8];
    private TextView[] valueViews = new TextView[8];
    private View[] dotViews = new View[8];
    private static final String[] STAT_KEYS = {
            "Down", "Up", "Devices", "Total Used", "Battery", "Thermal", "Skin Temp", "SMS Fwd"
    };
    private static final int[] ROW_IDS = {
            R.id.row_down, R.id.row_up, R.id.row_devices, R.id.row_total,
            R.id.row_battery, R.id.row_thermal, R.id.row_skintemp, R.id.row_smsfwd
    };

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

    @Override
    public void onAttachedToWindow() {
        super.onAttachedToWindow();
        setInteractive(false);
        setFullscreen(true);
        setContentView(R.layout.dream_router);

        clockView = findViewById(R.id.clock);
        dateView = findViewById(R.id.date);
        connStatusView = findViewById(R.id.conn_status);
        statusOnlineView = findViewById(R.id.status_online);
        statusSignalView = findViewById(R.id.status_signal);

        for (int i = 0; i < ROW_IDS.length; i++) {
            View row = findViewById(ROW_IDS[i]);
            labelViews[i] = row.findViewById(R.id.stat_label);
            valueViews[i] = row.findViewById(R.id.stat_value);
            dotViews[i] = row.findViewById(R.id.stat_dot);
            labelViews[i].setText(STAT_KEYS[i]);
            valueViews[i].setText("--");
            // Hide the trailing divider after the last row so the card
            // doesn't end with a stray line before its bottom padding.
            if (i == ROW_IDS.length - 1) {
                row.findViewById(R.id.stat_divider).setVisibility(View.GONE);
            }
        }

        fetchExecutor = Executors.newSingleThreadExecutor();
        mainHandler.post(clockTick);
        mainHandler.post(pollTick);
    }

    @Override
    public void onDetachedFromWindow() {
        super.onDetachedFromWindow();
        mainHandler.removeCallbacks(clockTick);
        mainHandler.removeCallbacks(pollTick);
        if (fetchExecutor != null) {
            fetchExecutor.shutdownNow();
        }
    }

    private void updateClock() {
        Calendar now = Calendar.getInstance();
        String time = DateFormat.format(DateFormat.is24HourFormat(this) ? "HH:mm" : "h:mm", now).toString();
        String date = DateFormat.format("EEE, d MMM", now).toString();
        clockView.setText(time);
        dateView.setText(date);
    }

    private void fetchAndUpdate() {
        try {
            URL url = new URL(DASHBOARD_URL);
            HttpURLConnection conn = (HttpURLConnection) url.openConnection();
            conn.setConnectTimeout(4000);
            conn.setReadTimeout(4000);
            String credentials = AUTH_USER + ":" + AUTH_PASS;
            String basicAuth = "Basic " + Base64.encodeToString(
                    credentials.getBytes(StandardCharsets.UTF_8), Base64.NO_WRAP);
            conn.setRequestProperty("Authorization", basicAuth);

            int code = conn.getResponseCode();
            if (code != 200) {
                postConnStatus("dashboard error: HTTP " + code);
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
                    if (v != null) {
                        valueViews[i].setText(v);
                    }
                    Integer color = statColors.get(key);
                    tintDot(dotViews[i], color != null ? color : DEFAULT_ACCENT_COLOR);
                }

                String internet = stats.get("Internet");
                if (internet != null) {
                    statusOnlineView.setText((internet.equalsIgnoreCase("Online") ? "● " : "○ ") + internet);
                    tintPill(statusOnlineView, colorOrDefault(statColors.get("Internet")));
                }
                String signal = stats.get("Signal");
                if (signal != null) {
                    statusSignalView.setText("Signal: " + signal);
                    tintPill(statusSignalView, colorOrDefault(statColors.get("Signal")));
                }
            });
            postConnStatus("live");
        } catch (Exception e) {
            postConnStatus("unreachable: " + e.getClass().getSimpleName());
        }
    }

    private int colorOrDefault(Integer color) {
        return color != null ? color : NEUTRAL_PILL_COLOR;
    }

    private void tintDot(View dot, int color) {
        Drawable bg = dot.getBackground().mutate();
        bg.setTint(color);
        dot.setBackground(bg);
    }

    private void tintPill(TextView pill, int color) {
        Drawable bg = pill.getBackground().mutate();
        bg.setTint(color);
        pill.setBackground(bg);
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

    private void postConnStatus(String text) {
        mainHandler.post(() -> connStatusView.setText(text));
    }
}
