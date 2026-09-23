package com.router.aod;

import android.app.PendingIntent;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.telephony.SmsManager;

import java.io.File;
import java.io.FileWriter;
import java.io.PrintWriter;
import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.Locale;

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
 *
 * Logs to a file (not just logcat, which rotates fast and isn't
 * reliably grep-able from a shell script) because sendTextMessage()
 * returning without throwing does NOT mean the SMS actually sent - the
 * real result only shows up later via the sent-status PendingIntent,
 * which is where actual failures (no signal, radio off, modem
 * rejecting it, etc.) actually surface.
 */
public class SendSmsReceiver extends BroadcastReceiver {
    public static final String ACTION_SMS_SENT = "com.router.aod.SMS_SENT";

    // /data/adb/dashboard/ is root:root 755 - our app's own UID has no
    // write access there (confirmed 2026-07-06: every log() call was
    // silently failing, making it look like onReceive never ran at all
    // when the real problem was just the log destination). This app's
    // own private files dir is always writable by the app itself; root
    // (failover_monitor.sh, adb shell su) can still read it regardless
    // of Android's per-app sandboxing.
    private static void log(Context context, String msg) {
        String ts = new SimpleDateFormat("yyyy-MM-dd HH:mm:ss", Locale.US).format(new Date());
        File logFile = new File(context.getFilesDir(), "sms_send.log");
        try (PrintWriter out = new PrintWriter(new FileWriter(logFile, true))) {
            out.println(ts + " - " + msg);
        } catch (Exception ignored) {
            // Nothing else to fall back to for logging a logging failure.
        }
    }

    @Override
    public void onReceive(Context context, Intent intent) {
        if (ACTION_SMS_SENT.equals(intent.getAction())) {
            int result = getResultCode();
            String desc;
            switch (result) {
                case android.app.Activity.RESULT_OK: desc = "SENT_OK"; break;
                case SmsManager.RESULT_ERROR_GENERIC_FAILURE: desc = "ERROR_GENERIC_FAILURE"; break;
                case SmsManager.RESULT_ERROR_NO_SERVICE: desc = "ERROR_NO_SERVICE"; break;
                case SmsManager.RESULT_ERROR_NULL_PDU: desc = "ERROR_NULL_PDU"; break;
                case SmsManager.RESULT_ERROR_RADIO_OFF: desc = "ERROR_RADIO_OFF"; break;
                case SmsManager.RESULT_ERROR_LIMIT_EXCEEDED: desc = "ERROR_LIMIT_EXCEEDED"; break;
                default: desc = "UNKNOWN_CODE_" + result;
            }
            log(context, "Send result for " + intent.getStringExtra("number") + ": " + desc);
            return;
        }

        String number = intent.getStringExtra("number");
        String message = intent.getStringExtra("message");
        log(context, "onReceive: number=" + number + " message=" + message);
        if (number == null || message == null) {
            log(context, "Missing number or message extra - aborting");
            return;
        }
        try {
            Intent sentIntent = new Intent(ACTION_SMS_SENT);
            sentIntent.putExtra("number", number);
            PendingIntent pi = PendingIntent.getBroadcast(
                    context, number.hashCode(), sentIntent,
                    PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
            SmsManager.getDefault().sendTextMessage(number, null, message, pi, null);
            log(context, "sendTextMessage() call completed without throwing - waiting for sent-status broadcast");
        } catch (Exception e) {
            log(context, "sendTextMessage() threw: " + e);
        }
    }
}
