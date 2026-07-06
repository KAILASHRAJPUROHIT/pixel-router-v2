package com.router.aod;

import android.app.Service;
import android.bluetooth.BluetoothAdapter;
import android.bluetooth.BluetoothDevice;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.os.Handler;
import android.os.IBinder;
import android.os.Looper;

import java.io.FileOutputStream;
import java.util.LinkedHashSet;
import java.util.Set;

/**
 * There's no Bluetooth scan/inquiry CLI on this Android build (confirmed
 * 2026-07-06 - `cmd bluetooth_manager` only has enable/disable/state,
 * nothing for discovery), so failover_monitor.sh can't do BT presence
 * detection with a plain shell command the way it does WiFi presence via
 * `ip neigh`. This service bridges that gap: started via
 * `am start-service -n com.router.aod/.BtScanService`, it runs a normal
 * classic-Bluetooth discovery (BluetoothAdapter.startDiscovery(), same
 * API any paired-device Android app uses) and writes every device
 * address it sees to a file root can read, same pattern as
 * dream_state/SMS-log files elsewhere in this app.
 *
 * Only useful for phones actually paired with this device first (a
 * physical step done once in Bluetooth settings) - discovery finds any
 * nearby discoverable device, paired or not, but the shell side only
 * cares about matching a specific known phone's MAC.
 */
public class BtScanService extends Service {
    private static final String RESULT_FILE = "bt_scan_result.txt";
    private static final long DISCOVERY_TIMEOUT_MS = 14000; // classic BT discovery is ~12s

    private final Set<String> found = new LinkedHashSet<>();
    private BroadcastReceiver receiver;
    private final Handler handler = new Handler(Looper.getMainLooper());

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        BluetoothAdapter adapter = BluetoothAdapter.getDefaultAdapter();
        if (adapter == null || !adapter.isEnabled()) {
            writeResult();
            stopSelf();
            return START_NOT_STICKY;
        }

        receiver = new BroadcastReceiver() {
            @Override
            public void onReceive(Context context, Intent intent) {
                String action = intent.getAction();
                if (BluetoothDevice.ACTION_FOUND.equals(action)) {
                    BluetoothDevice device = intent.getParcelableExtra(BluetoothDevice.EXTRA_DEVICE);
                    if (device != null) {
                        found.add(device.getAddress());
                    }
                } else if (BluetoothAdapter.ACTION_DISCOVERY_FINISHED.equals(action)) {
                    finish();
                }
            }
        };
        IntentFilter filter = new IntentFilter();
        filter.addAction(BluetoothDevice.ACTION_FOUND);
        filter.addAction(BluetoothAdapter.ACTION_DISCOVERY_FINISHED);
        registerReceiver(receiver, filter);

        if (adapter.isDiscovering()) {
            adapter.cancelDiscovery();
        }
        adapter.startDiscovery();

        // Safety net - discovery is supposed to self-terminate and fire
        // ACTION_DISCOVERY_FINISHED, but if it doesn't for any reason
        // this stops the service from hanging around forever anyway.
        handler.postDelayed(this::finish, DISCOVERY_TIMEOUT_MS);

        return START_NOT_STICKY;
    }

    private void finish() {
        BluetoothAdapter adapter = BluetoothAdapter.getDefaultAdapter();
        if (adapter != null && adapter.isDiscovering()) {
            adapter.cancelDiscovery();
        }
        if (receiver != null) {
            try {
                unregisterReceiver(receiver);
            } catch (IllegalArgumentException e) {
                // already unregistered - finish() can run twice (real
                // ACTION_DISCOVERY_FINISHED plus the safety-net timeout)
            }
            receiver = null;
        }
        writeResult();
        stopSelf();
    }

    private void writeResult() {
        try (FileOutputStream out = openFileOutput(RESULT_FILE, MODE_PRIVATE)) {
            StringBuilder sb = new StringBuilder();
            for (String addr : found) {
                sb.append(addr).append('\n');
            }
            out.write(sb.toString().getBytes());
        } catch (Exception e) {
            // best-effort - failover_monitor.sh treats a missing/stale
            // result file as "nothing found" and falls through safely
        }
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }
}
