package com.router.aod;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.telephony.SmsManager;

/**
 * Triggered from a rooted shell script (failover_monitor.sh) via:
 *   am broadcast -a com.router.aod.SEND_SMS --es number "+91..." --es message "..."
 * Exists because sending SMS from a shell script has no stable public
 * command (no `cmd sms`, and the raw `service call isms` binder
 * approach uses transaction numbers that vary across Android versions
 * - the same class of fragility that broke WifiConfigStoreSoftAp.xml
 * parsing tonight). Going through the real SmsManager API from our own
 * app is what a normal SMS app does, so it isn't tied to OS-version
 * internals.
 */
public class SendSmsReceiver extends BroadcastReceiver {
    @Override
    public void onReceive(Context context, Intent intent) {
        String number = intent.getStringExtra("number");
        String message = intent.getStringExtra("message");
        if (number == null || message == null) return;
        try {
            SmsManager.getDefault().sendTextMessage(number, null, message, null, null);
        } catch (Exception ignored) {
            // Best-effort - failover_monitor.sh doesn't get a delivery
            // receipt either way, it just moves on to scanning for the
            // hotspot SSID regardless.
        }
    }
}
