package connmonitor

import "testing"

type fakeController struct {
	ttlproxyIntact  bool
	restartCalls    int
	restartFixesIt  bool
	hoppatchPresent bool
	hoppatchCreated int

	lossPct    int
	avgRTTms   float64
	hasPing    bool
	rsrp       int
	sinr       int
	hasSignal  bool
	dataReg    int
	hasDataReg bool
	manualSel  bool

	reconnectCalls int

	conntrackDrift, dnsDrift, softapDrift, netmodeDrift bool
}

func (f *fakeController) TTLProxyRulesIntact() (bool, error) { return f.ttlproxyIntact, nil }
func (f *fakeController) RestartTTLProxy() error {
	f.restartCalls++
	if f.restartFixesIt {
		f.ttlproxyIntact = true
	}
	return nil
}
func (f *fakeController) HoppatchDisableFlagPresent() (bool, error) { return f.hoppatchPresent, nil }
func (f *fakeController) CreateHoppatchDisableFlag() error {
	f.hoppatchCreated++
	f.hoppatchPresent = true
	return nil
}

func (f *fakeController) PingStats(target string, count int) (int, float64, bool, error) {
	return f.lossPct, f.avgRTTms, f.hasPing, nil
}
func (f *fakeController) SignalReading() (int, int, bool, error) {
	return f.rsrp, f.sinr, f.hasSignal, nil
}
func (f *fakeController) DataRegistrationState() (int, bool, error) {
	return f.dataReg, f.hasDataReg, nil
}
func (f *fakeController) IsManualNetworkSelection() (bool, error) { return f.manualSel, nil }
func (f *fakeController) ReconnectCellular() error                { f.reconnectCalls++; return nil }

func (f *fakeController) AssertConntrackMax() (bool, error)          { return f.conntrackDrift, nil }
func (f *fakeController) AssertPrivateDNS() (bool, error)            { return f.dnsDrift, nil }
func (f *fakeController) AssertSoftApTimeoutDisabled() (bool, error) { return f.softapDrift, nil }
func (f *fakeController) AssertPreferredNetworkMode() (bool, error)  { return f.netmodeDrift, nil }

func healthyStable() *fakeController {
	return &fakeController{
		ttlproxyIntact: true, hoppatchPresent: true,
		lossPct: 0, avgRTTms: 50, hasPing: true,
		rsrp: -80, sinr: 20, hasSignal: true,
		dataReg: 0, hasDataReg: true,
	}
}

func TestTickDetectionSafety_RestartsWhenNotIntact(t *testing.T) {
	ctrl := &fakeController{ttlproxyIntact: false, restartFixesIt: true, hoppatchPresent: true}
	result := TickDetectionSafety(ctrl, func() {})

	if result.WasIntact {
		t.Fatal("expected WasIntact=false")
	}
	if !result.RestartAttempted || ctrl.restartCalls != 1 {
		t.Fatalf("expected exactly one restart attempt, got %d", ctrl.restartCalls)
	}
	if !result.RestartConfirmed {
		t.Fatal("expected restart to be confirmed since restartFixesIt=true")
	}
}

func TestTickDetectionSafety_NoRestartWhenIntact(t *testing.T) {
	ctrl := &fakeController{ttlproxyIntact: true, hoppatchPresent: true}
	result := TickDetectionSafety(ctrl, func() {})
	if result.RestartAttempted || ctrl.restartCalls != 0 {
		t.Fatal("expected no restart when already intact")
	}
}

func TestTickDetectionSafety_CreatesMissingHoppatchFlag(t *testing.T) {
	ctrl := &fakeController{ttlproxyIntact: true, hoppatchPresent: false}
	result := TickDetectionSafety(ctrl, func() {})
	if !result.HoppatchFlagCreated || ctrl.hoppatchCreated != 1 {
		t.Fatal("expected the hoppatch disable flag to be created when missing")
	}
}

func TestTickStability_HealthyDoesNothing(t *testing.T) {
	ctrl := healthyStable()
	result := TickStability(ctrl)
	if result.Unstable || result.ReconnectAttempted || ctrl.reconnectCalls != 0 {
		t.Fatal("expected no reconnect when everything is healthy")
	}
}

func TestTickStability_HighPacketLossTriggersReconnect(t *testing.T) {
	ctrl := healthyStable()
	ctrl.lossPct = 50
	result := TickStability(ctrl)
	if !result.Unstable || !result.ReconnectAttempted || ctrl.reconnectCalls != 1 {
		t.Fatal("expected reconnect on high packet loss")
	}
}

func TestTickStability_HighLatencyTriggersReconnect(t *testing.T) {
	ctrl := healthyStable()
	ctrl.avgRTTms = 2000
	result := TickStability(ctrl)
	if !result.Unstable || result.Reason != "high latency" {
		t.Fatalf("expected high-latency reconnect, got unstable=%v reason=%q", result.Unstable, result.Reason)
	}
}

func TestTickStability_PoorSignalTriggersReconnect(t *testing.T) {
	ctrl := healthyStable()
	ctrl.rsrp = -115
	result := TickStability(ctrl)
	if !result.Unstable || result.Reason != "poor signal" {
		t.Fatalf("expected poor-signal reconnect, got unstable=%v reason=%q", result.Unstable, result.Reason)
	}
}

func TestTickStability_DataRegNotInServiceTriggersReconnect(t *testing.T) {
	ctrl := healthyStable()
	ctrl.dataReg = 1
	result := TickStability(ctrl)
	if !result.Unstable || result.Reason != "data registration not IN_SERVICE" {
		t.Fatalf("expected data-reg reconnect, got unstable=%v reason=%q", result.Unstable, result.Reason)
	}
}

func TestTickStability_ReportsDriftFixed(t *testing.T) {
	ctrl := healthyStable()
	ctrl.conntrackDrift = true
	ctrl.netmodeDrift = true
	result := TickStability(ctrl)
	if len(result.DriftFixed) != 2 {
		t.Fatalf("expected 2 drift fixes reported, got %v", result.DriftFixed)
	}
}

func TestTickStability_ManualSelectionSurfaced(t *testing.T) {
	ctrl := healthyStable()
	ctrl.manualSel = true
	result := TickStability(ctrl)
	if !result.ManualSelection {
		t.Fatal("expected manual network selection to be surfaced")
	}
}
