package qos

import "testing"

type fakeController struct {
	wanIf      string
	priorityIP string
	connected  bool
	qdiscOK    bool
	applyCalls int
	applyErr   error
}

func (f *fakeController) DetectWanInterface() (string, error) { return f.wanIf, nil }
func (f *fakeController) PriorityDeviceIP(mac string) (string, bool, error) {
	return f.priorityIP, f.connected, nil
}
func (f *fakeController) QdiscPresent(iface string) (bool, error) { return f.qdiscOK, nil }
func (f *fakeController) ApplyQoS(iface, priorityIP string) error {
	f.applyCalls++
	return f.applyErr
}

func TestTick_AppliesOnFirstRun(t *testing.T) {
	ctrl := &fakeController{wanIf: "rmnet1", priorityIP: "10.0.0.5", connected: true, qdiscOK: true}
	st := &State{}
	result := Tick(ctrl, st, "aa:bb:cc:dd:ee:ff")

	if !result.Applied || ctrl.applyCalls != 1 {
		t.Fatalf("expected QoS applied on first run, got %d calls", ctrl.applyCalls)
	}
	if st.LastWanIf != "rmnet1" || st.LastPriorityIP != "10.0.0.5" {
		t.Fatal("expected state updated after apply")
	}
}

func TestTick_NoReapplyWhenUnchangedAndPresent(t *testing.T) {
	ctrl := &fakeController{wanIf: "rmnet1", priorityIP: "10.0.0.5", connected: true, qdiscOK: true}
	st := &State{LastWanIf: "rmnet1", LastPriorityIP: "10.0.0.5"}
	result := Tick(ctrl, st, "aa:bb:cc:dd:ee:ff")

	if result.Applied || ctrl.applyCalls != 0 {
		t.Fatal("expected no reapply when nothing changed and qdisc is present")
	}
}

func TestTick_ReappliesWhenPriorityIPChanges(t *testing.T) {
	ctrl := &fakeController{wanIf: "rmnet1", priorityIP: "10.0.0.9", connected: true, qdiscOK: true}
	st := &State{LastWanIf: "rmnet1", LastPriorityIP: "10.0.0.5"}
	result := Tick(ctrl, st, "aa:bb:cc:dd:ee:ff")

	if !result.Applied || ctrl.applyCalls != 1 {
		t.Fatal("expected reapply when priority device's IP changed (DHCP lease renewal)")
	}
}

func TestTick_ReappliesWhenQdiscDrifted(t *testing.T) {
	ctrl := &fakeController{wanIf: "rmnet1", priorityIP: "10.0.0.5", connected: true, qdiscOK: false}
	st := &State{LastWanIf: "rmnet1", LastPriorityIP: "10.0.0.5"}
	result := Tick(ctrl, st, "aa:bb:cc:dd:ee:ff")

	if !result.Applied || result.Reason == "" {
		t.Fatal("expected reapply when qdisc state silently drifted")
	}
}

func TestTick_NoActionWhenPriorityDeviceNotConnected(t *testing.T) {
	ctrl := &fakeController{connected: false}
	st := &State{LastWanIf: "rmnet1", LastPriorityIP: "10.0.0.5"}
	result := Tick(ctrl, st, "aa:bb:cc:dd:ee:ff")

	if result.Applied || result.PriorityConnected {
		t.Fatal("expected no action when priority device isn't currently connected")
	}
}
