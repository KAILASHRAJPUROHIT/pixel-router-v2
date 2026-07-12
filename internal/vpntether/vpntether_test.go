package vpntether

import (
	"testing"
	"time"
)

type fakeController struct {
	internetType string
	rxBytes      int64
	hasRX        bool

	applyCalls       int
	removeCalls      int
	reconnectCalls   int
	ensureRouteCalls int
	applyErr         error
}

func (f *fakeController) ReadInternetType() (string, error)   { return f.internetType, nil }
func (f *fakeController) TunnelRxBytes() (int64, bool, error) { return f.rxBytes, f.hasRX, nil }
func (f *fakeController) ApplyBypassRouting() error           { f.applyCalls++; return f.applyErr }
func (f *fakeController) RemoveBypassRouting() error          { f.removeCalls++; return nil }
func (f *fakeController) ReconnectTunnel() error              { f.reconnectCalls++; return nil }
func (f *fakeController) EnsureLocalSubnetRoute() error       { f.ensureRouteCalls++; return nil }

var base = time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)

func TestTick_RXMovingMeansAlive_BypassApplied(t *testing.T) {
	ctrl := &fakeController{internetType: "self", rxBytes: 1000, hasRX: true}
	st := &State{PrevRX: 500, HasPrevRX: true}
	result := Tick(ctrl, st, base)

	if !result.TunnelAlive {
		t.Fatal("expected tunnel alive - RX increased")
	}
	if !result.BypassChanged || !result.NowBypassing || ctrl.applyCalls != 1 {
		t.Fatal("expected bypass routing applied")
	}
}

func TestTick_RXFrozenMeansDead_NoBypass(t *testing.T) {
	ctrl := &fakeController{internetType: "self", rxBytes: 500, hasRX: true}
	st := &State{PrevRX: 500, HasPrevRX: true}
	result := Tick(ctrl, st, base)

	if result.TunnelAlive {
		t.Fatal("expected tunnel dead - RX frozen (the asymmetric-silence dead-peer signature)")
	}
	if result.NowBypassing {
		t.Fatal("expected no bypass routing with a dead tunnel")
	}
}

func TestTick_BorrowedInternetNeverBypasses(t *testing.T) {
	ctrl := &fakeController{internetType: "borrowed", rxBytes: 1000, hasRX: true}
	st := &State{PrevRX: 500, HasPrevRX: true}
	result := Tick(ctrl, st, base)

	if result.NowBypassing {
		t.Fatal("expected no bypass while on borrowed internet, even with a live tunnel")
	}
}

func TestTick_RemovesBypassWhenTunnelGoesDead(t *testing.T) {
	ctrl := &fakeController{internetType: "self", rxBytes: 500, hasRX: true}
	st := &State{CurrentlyBypassing: true, PrevRX: 500, HasPrevRX: true}
	result := Tick(ctrl, st, base)

	if !result.BypassChanged || result.NowBypassing || ctrl.removeCalls != 1 {
		t.Fatal("expected bypass routing removed once the tunnel goes dead")
	}
}

func TestTick_ReconnectsAfterSustainedDeadStreak(t *testing.T) {
	ctrl := &fakeController{internetType: "self", rxBytes: 500, hasRX: true}
	st := &State{PrevRX: 500, HasPrevRX: true}

	// Two dead ticks - not enough yet (threshold is 3).
	Tick(ctrl, st, base)
	Tick(ctrl, st, base.Add(30*time.Second))
	if ctrl.reconnectCalls != 0 {
		t.Fatalf("expected no reconnect before the streak threshold, got %d", ctrl.reconnectCalls)
	}

	// Third consecutive dead tick crosses the threshold.
	result := Tick(ctrl, st, base.Add(60*time.Second))
	if !result.ReconnectAttempted || ctrl.reconnectCalls != 1 {
		t.Fatalf("expected exactly one reconnect attempt at the streak threshold, got %d", ctrl.reconnectCalls)
	}
}

func TestTick_ReconnectRespectsCooldown(t *testing.T) {
	ctrl := &fakeController{internetType: "self", rxBytes: 500, hasRX: true}
	st := &State{PrevRX: 500, HasPrevRX: true, DeadStreak: 3, LastReconnectAttempt: base}

	// Well within the 5-minute cooldown - should not reconnect again.
	result := Tick(ctrl, st, base.Add(1*time.Minute))
	if result.ReconnectAttempted || ctrl.reconnectCalls != 0 {
		t.Fatal("expected reconnect to be skipped within the cooldown window")
	}
}

func TestTick_AlwaysAssertsLocalSubnetRoute_RegardlessOfBypassState(t *testing.T) {
	// This must run every tick unconditionally - the router's own
	// outbound connections to hotspot clients get swept onto the
	// WireGuard tunnel by its own default-route config whenever the
	// tunnel is up, independent of whether this package's separate
	// wlan2 bypass marking happens to be active.
	ctrl := &fakeController{internetType: "borrowed", rxBytes: 500, hasRX: true}
	st := &State{}
	Tick(ctrl, st, base)
	if ctrl.ensureRouteCalls != 1 {
		t.Fatalf("expected EnsureLocalSubnetRoute called once per tick even without bypassing, got %d", ctrl.ensureRouteCalls)
	}
}

func TestTick_NoReconnectWhileOnBorrowedInternet(t *testing.T) {
	ctrl := &fakeController{internetType: "borrowed", rxBytes: 500, hasRX: true}
	st := &State{PrevRX: 500, HasPrevRX: true}

	for i := 0; i < 5; i++ {
		Tick(ctrl, st, base.Add(time.Duration(i)*time.Minute))
	}
	if ctrl.reconnectCalls != 0 {
		t.Fatal("expected no reconnect attempts while on borrowed internet - nothing needs the tunnel")
	}
}
