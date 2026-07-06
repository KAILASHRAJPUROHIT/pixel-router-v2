package com.router.aod;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.provider.Telephony;
import android.telephony.SmsMessage;

import java.io.File;
import java.io.FileWriter;
import java.io.PrintWriter;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Listens for every incoming SMS and picks out location replies from
 * the iPhones' "reply with my GPS coordinates" Shortcut (triggered by
 * failover_monitor.sh's WHEREAREYOU query). Writes each sender's most
 * recent location to a file named after their own number, in this
 * app's private storage - root (failover_monitor.sh) can always read
 * that regardless of this app's own UID sandboxing, so the shell
 * script does the actual distance-from-home decision using whatever
 * number it already has configured, rather than this receiver needing
 * to know which configured "phoneN" slot a given sender maps to (that
 * config lives in /data/adb/dashboard/, which is root:root 755 and
 * unreadable by this app's own UID - same class of issue that broke
 * SendSmsReceiver's original log path).
 *
 * Expected reply body format (set up in the iPhone Shortcut):
 *   LOC:<latitude>,<longitude>
 */
public class LocationReplyReceiver extends BroadcastReceiver {
    private static final Pattern LOC_PATTERN = Pattern.compile("LOC:(-?[0-9.]+),(-?[0-9.]+)");

    @Override
    public void onReceive(Context context, Intent intent) {
        if (!Telephony.Sms.Intents.SMS_RECEIVED_ACTION.equals(intent.getAction())) return;

        SmsMessage[] messages = Telephony.Sms.Intents.getMessagesFromIntent(intent);
        if (messages == null) return;

        StringBuilder body = new StringBuilder();
        String sender = null;
        for (SmsMessage m : messages) {
            if (m == null) continue;
            if (sender == null) sender = m.getOriginatingAddress();
            body.append(m.getMessageBody());
        }
        if (sender == null) return;

        Matcher m = LOC_PATTERN.matcher(body.toString());
        if (!m.find()) return;

        String lat = m.group(1);
        String lon = m.group(2);
        String sanitizedNumber = sender.replaceAll("[^0-9]", "");
        File outFile = new File(context.getFilesDir(), "location_" + sanitizedNumber + ".txt");
        try (PrintWriter out = new PrintWriter(new FileWriter(outFile, false))) {
            out.println(lat + "," + lon + "," + (System.currentTimeMillis() / 1000));
        } catch (Exception ignored) {
            // Best-effort - failover_monitor.sh's wait loop just times out
            // and treats the phone as unreachable/not-home if this fails.
        }
    }
}
