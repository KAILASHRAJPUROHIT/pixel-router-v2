// Package vpntether ports V1's vpn_tether_routing.sh - conditionally
// routes hotspot-client traffic (wlan2) through a WireGuard tunnel
// (tun0) while on the phone's own cellular uplink, since Airtel's
// tethering detection can fingerprint raw tethered traffic but has no
// visibility into a masked-through-VPN connection. Only active while
// internet_type=self (borrowed WiFi-to-WiFi has no Airtel visibility
// at all, so bypassing would be pointless and dangerous if the tunnel
// were ever down); only while the tunnel is confirmed alive via RX
// byte movement, not just interface presence (a dead peer can sit
// there with TX still incrementing from our own keepalive while RX
// stays frozen).
package vpntether

import "time"

const (
	deadStreakBeforeReconnect = 3
	reconnectCooldown         = 5 * time.Minute
)

type Controller interface {
	// ReadInternetType reads the SAME state V2's failover service
	// writes ("self" or "borrowed") - this package deliberately reads
	// it rather than owning that decision itself.
	ReadInternetType() (string, error)
	TunnelRxBytes() (bytes int64, hasReading bool, err error)
	ApplyBypassRouting() error
	RemoveBypassRouting() error
	ReconnectTunnel() error
	// EnsureLocalSubnetRoute asserts a routing rule that keeps the
	// router's OWN outbound connections to its hotspot clients (e.g. the
	// dashboard's webproxy reaching a laptop on the AP subnet) off the
	// WireGuard tunnel. The tunnel's AllowedIPs=0.0.0.0/0 config makes it
	// the default route for every locally-originated process on the
	// router the moment it's up - not just the wlan2 client traffic this
	// package's own bypass marking targets - so without this, the router
	// can never reach its own hotspot clients while the VPN is
	// connected. Idempotent and cheap, so it's asserted every tick
	// regardless of bypass state, mirroring usbtether's NAT-rule
	// assertion pattern.
	EnsureLocalSubnetRoute() error
}

type State struct {
	CurrentlyBypassing   bool
	PrevRX               int64
	HasPrevRX            bool
	DeadStreak           int
	LastReconnectAttempt time.Time
}

type TickResult struct {
	TunnelAlive        bool
	BypassChanged      bool
	NowBypassing       bool
	ReconnectAttempted bool
	Err                error
}

// Tick mirrors V1's loop body exactly: sample tun0's RX byte counter
// across ticks (RX only moving means genuinely alive, not just
// present), decide whether to bypass-route based on internet_type +
// tunnel-alive, and reconnect the tunnel via the WireGuard app's own
// broadcast API after several consecutive dead readings (rate-limited
// by a cooldown so a stuck-dead tunnel doesn't get hammered).
func Tick(ctrl Controller, st *State, now time.Time) TickResult {
	var result TickResult

	ctrl.EnsureLocalSubnetRoute()

	internetType, _ := ctrl.ReadInternetType()

	curRX, hasReading, _ := ctrl.TunnelRxBytes()
	tunnelAlive := false
	if hasReading && st.HasPrevRX && curRX > st.PrevRX {
		tunnelAlive = true
	}
	if hasReading {
		st.PrevRX = curRX
		st.HasPrevRX = true
	} else {
		st.HasPrevRX = false
	}
	result.TunnelAlive = tunnelAlive

	if internetType == "self" && !tunnelAlive {
		st.DeadStreak++
	} else {
		st.DeadStreak = 0
	}

	if st.DeadStreak >= deadStreakBeforeReconnect {
		if now.Sub(st.LastReconnectAttempt) >= reconnectCooldown {
			result.ReconnectAttempted = true
			result.Err = ctrl.ReconnectTunnel()
			st.LastReconnectAttempt = now
		}
		st.DeadStreak = 0
	}

	shouldBypass := internetType == "self" && tunnelAlive
	if shouldBypass && !st.CurrentlyBypassing {
		if err := ctrl.ApplyBypassRouting(); err == nil {
			st.CurrentlyBypassing = true
			result.BypassChanged = true
		} else {
			result.Err = err
		}
	} else if !shouldBypass && st.CurrentlyBypassing {
		if err := ctrl.RemoveBypassRouting(); err == nil {
			st.CurrentlyBypassing = false
			result.BypassChanged = true
		} else {
			result.Err = err
		}
	}
	result.NowBypassing = st.CurrentlyBypassing

	return result
}
