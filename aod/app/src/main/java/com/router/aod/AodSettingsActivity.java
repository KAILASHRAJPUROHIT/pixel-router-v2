package com.router.aod;

import android.app.Activity;
import android.content.SharedPreferences;
import android.os.Bundle;
import android.widget.RadioButton;
import android.widget.RadioGroup;

/**
 * Lets the user pick which of the six layouts and eight color themes the
 * Dream should use. Reachable normally (launcher icon) or via the gear
 * next to the Dream entry in Settings > Display > Screen saver (wired
 * via android:settingsActivity in dream_info.xml). Selections are saved
 * to SharedPreferences and read by RouterDreamService the next time the
 * Dream starts - not hot-swapped mid-dream.
 */
public class AodSettingsActivity extends Activity {

    static final String PREFS_NAME = "aod_prefs";
    static final String KEY_LAYOUT = "layout_index";
    static final String KEY_THEME = "theme_index";

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_settings);

        SharedPreferences prefs = getSharedPreferences(PREFS_NAME, MODE_PRIVATE);
        int currentLayout = prefs.getInt(KEY_LAYOUT, 0);
        int currentTheme = prefs.getInt(KEY_THEME, 0);

        RadioGroup layoutGroup = findViewById(R.id.layout_group);
        AodLayout[] layouts = AodLayout.values();
        for (int i = 0; i < layouts.length; i++) {
            RadioButton rb = new RadioButton(this);
            rb.setText(layouts[i].label);
            rb.setTextColor(0xffe8ecf1);
            rb.setId(i);
            layoutGroup.addView(rb);
        }
        layoutGroup.check(currentLayout);
        layoutGroup.setOnCheckedChangeListener((group, checkedId) ->
                prefs.edit().putInt(KEY_LAYOUT, checkedId).apply());

        RadioGroup themeGroup = findViewById(R.id.theme_group);
        AodTheme[] themes = AodTheme.ALL;
        for (int i = 0; i < themes.length; i++) {
            RadioButton rb = new RadioButton(this);
            rb.setText(themes[i].name);
            rb.setTextColor(themes[i].accent);
            rb.setId(i);
            themeGroup.addView(rb);
        }
        themeGroup.check(currentTheme);
        themeGroup.setOnCheckedChangeListener((group, checkedId) ->
                prefs.edit().putInt(KEY_THEME, checkedId).apply());
    }
}
