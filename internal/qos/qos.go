// Package qos ports V1's qos_priority.sh - best-effort QoS giving one
// priority device (by MAC) egress priority over the shared cellular
// uplink via a prio+fq_codel tc setup, matching the general approach
// OpenWrt's SQM uses.
package qos

type Controller interface {
	DetectWanInterface() (string, error)
	// PriorityDeviceIP resolves the priority MAC's current IP via the
	// neighbor table - hasReading=false if it's not currently connected.
	PriorityDeviceIP(mac string) (ip string, hasReading bool, err error)
	// QdiscPresent reports whether our "prio 1: root" qdisc is actually
	// still wired up on iface right now - cellular interface churn can
	// silently wipe tc state without the WAN interface name or priority
	// IP ever changing, so a bare state comparison alone isn't enough.
	QdiscPresent(iface string) (bool, error)
	ApplyQoS(iface, priorityIP string) error
}

// State is persisted across ticks so a real re-apply only happens when
// something's actually changed (or drifted) - re-applying every tick
// unconditionally would be wasteful and, worse, would interrupt
// in-flight classification for no reason.
type State struct {
	LastWanIf      string
	LastPriorityIP string
}

type TickResult struct {
	Applied           bool
	Reason            string
	PriorityConnected bool
}

// Tick mirrors V1's loop body: resolve the current WAN interface and
// priority device IP, and (re-)apply QoS if either changed, or if the
// tc state has silently drifted off even though nothing we'd normally
// notice changed.
func Tick(ctrl Controller, st *State, priorityMAC string) TickResult {
	var result TickResult

	wanIf, err := ctrl.DetectWanInterface()
	if err != nil || wanIf == "" {
		wanIf = "rmnet1"
	}

	priorityIP, connected, _ := ctrl.PriorityDeviceIP(priorityMAC)
	result.PriorityConnected = connected
	if !connected {
		return result
	}

	if wanIf != st.LastWanIf || priorityIP != st.LastPriorityIP {
		if err := ctrl.ApplyQoS(wanIf, priorityIP); err == nil {
			result.Applied = true
			result.Reason = "WAN interface or priority IP changed"
			st.LastWanIf = wanIf
			st.LastPriorityIP = priorityIP
		}
		return result
	}

	if present, err := ctrl.QdiscPresent(wanIf); err == nil && !present {
		if err := ctrl.ApplyQoS(wanIf, priorityIP); err == nil {
			result.Applied = true
			result.Reason = "qdisc state had drifted (interface churn wiped it)"
		}
	}

	return result
}
