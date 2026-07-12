// Package forwarder ports V1's forwarder_watchdog.sh - monitoring the
// third-party SMS/call-forwarding app's enabled state, runtime SMS
// permissions, and Doze-whitelist membership, re-applying any that
// have drifted. Confirmed live in V1: this app's permissions were
// being silently revoked by Android's own "remove permissions if app
// isn't used" auto-reset (the app has no foreground UI a person ever
// opens), as often as every 11-25 minutes - not a Doze/battery-
// restriction issue (its App Standby bucket was already the best
// possible state, Exempted). V1's watchdog polled every 3 minutes
// originally, tightened to 20s after finding the gap; V2 keeps the
// same fast interval by default.
//
// Re-applying an already-fine state (re-granting an already-granted
// permission, re-enabling an already-enabled app) is always a safe,
// idempotent no-op - unlike hotspot/USB-tether bring-up, there is no
// live-disruption risk in testing this against the real device.
package forwarder

type Controller interface {
	IsEnabled() (bool, error)
	Enable() error
	IsPermissionGranted(permission string) (bool, error)
	GrantPermission(permission string) error
	IsOnDozeWhitelist() (bool, error)
	AddToDozeWhitelist() error
}

// EnsureHealthy checks every condition V1's watchdog checked and
// corrects any that have drifted, returning a human-readable list of
// what was fixed (empty if everything was already fine) for logging.
func EnsureHealthy(ctrl Controller, permissions []string) ([]string, error) {
	var fixed []string

	enabled, err := ctrl.IsEnabled()
	if err != nil {
		return nil, err
	}
	if !enabled {
		if err := ctrl.Enable(); err != nil {
			return fixed, err
		}
		fixed = append(fixed, "app was disabled - re-enabled")
	}

	for _, perm := range permissions {
		granted, err := ctrl.IsPermissionGranted(perm)
		if err != nil {
			return fixed, err
		}
		if !granted {
			if err := ctrl.GrantPermission(perm); err != nil {
				return fixed, err
			}
			fixed = append(fixed, "permission "+perm+" was revoked - re-granted")
		}
	}

	onWhitelist, err := ctrl.IsOnDozeWhitelist()
	if err != nil {
		return fixed, err
	}
	if !onWhitelist {
		if err := ctrl.AddToDozeWhitelist(); err != nil {
			return fixed, err
		}
		fixed = append(fixed, "dropped from Doze whitelist - re-added")
	}

	return fixed, nil
}
