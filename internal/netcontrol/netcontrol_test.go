package netcontrol

import (
	"errors"
	"testing"
)

func TestEvaluateAndHeal_GoodSignal_NoAction(t *testing.T) {
	fake := &FakeController{Signal: SignalInfo{RSRP: -72, SINR: 10, DataRegInService: true}}
	healed, _, err := EvaluateAndHeal(fake, DefaultThresholds)
	if err != nil {
		t.Fatal(err)
	}
	if healed {
		t.Fatal("expected no reconnect on good signal")
	}
	if fake.ReconnectCalls != 0 {
		t.Fatalf("expected 0 reconnect calls, got %d", fake.ReconnectCalls)
	}
}

func TestEvaluateAndHeal_PoorRSRP_TriggersReconnect(t *testing.T) {
	fake := &FakeController{Signal: SignalInfo{RSRP: -115, SINR: 10, DataRegInService: true}}
	healed, reason, err := EvaluateAndHeal(fake, DefaultThresholds)
	if err != nil {
		t.Fatal(err)
	}
	if !healed {
		t.Fatal("expected reconnect on poor RSRP")
	}
	if fake.ReconnectCalls != 1 {
		t.Fatalf("expected exactly 1 reconnect call, got %d", fake.ReconnectCalls)
	}
	if reason == "" {
		t.Fatal("expected a non-empty reason")
	}
}

func TestEvaluateAndHeal_PoorSINR_TriggersReconnect(t *testing.T) {
	fake := &FakeController{Signal: SignalInfo{RSRP: -80, SINR: 1, DataRegInService: true}}
	healed, _, err := EvaluateAndHeal(fake, DefaultThresholds)
	if err != nil {
		t.Fatal(err)
	}
	if !healed {
		t.Fatal("expected reconnect on poor SINR")
	}
}

func TestEvaluateAndHeal_OutOfService_TriggersReconnectRegardlessOfSignal(t *testing.T) {
	fake := &FakeController{Signal: SignalInfo{RSRP: -60, SINR: 20, DataRegInService: false}}
	healed, reason, err := EvaluateAndHeal(fake, DefaultThresholds)
	if err != nil {
		t.Fatal(err)
	}
	if !healed {
		t.Fatal("expected reconnect when data registration is out of service, even with great signal numbers")
	}
	if reason == "" {
		t.Fatal("expected a reason")
	}
}

func TestEvaluateAndHeal_ReconnectFailure_Propagates(t *testing.T) {
	fake := &FakeController{
		Signal:       SignalInfo{RSRP: -115, SINR: 1, DataRegInService: true},
		ReconnectErr: errors.New("boom"),
	}
	_, _, err := EvaluateAndHeal(fake, DefaultThresholds)
	if err == nil {
		t.Fatal("expected error to propagate when reconnect fails")
	}
}

func TestEvaluateAndHeal_SignalReadFailure_Propagates(t *testing.T) {
	fake := &FakeController{SignalErr: errors.New("dumpsys unavailable")}
	_, _, err := EvaluateAndHeal(fake, DefaultThresholds)
	if err == nil {
		t.Fatal("expected error when signal info can't be read")
	}
	if fake.ReconnectCalls != 0 {
		t.Fatal("should not attempt reconnect if signal couldn't even be read")
	}
}
