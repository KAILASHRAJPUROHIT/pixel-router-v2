package powergovernor

import "testing"

type fakeController struct {
	throttled     bool
	dreamState    string
	hasDreamState bool
	wakefulness   string

	setCPUCalls []int
	setGPUCalls []int
}

func (f *fakeController) ThermallyThrottled() (bool, error) { return f.throttled, nil }
func (f *fakeController) DreamState() (string, bool, error) {
	return f.dreamState, f.hasDreamState, nil
}
func (f *fakeController) Wakefulness() (string, error) { return f.wakefulness, nil }
func (f *fakeController) SetCPUPct(pct int) error {
	f.setCPUCalls = append(f.setCPUCalls, pct)
	return nil
}
func (f *fakeController) SetGPUPct(pct int) error {
	f.setGPUCalls = append(f.setGPUCalls, pct)
	return nil
}

func TestTick_ThermallyThrottled_Skipped(t *testing.T) {
	ctrl := &fakeController{throttled: true}
	st := &State{}
	result := Tick(ctrl, st)

	if !result.Skipped {
		t.Fatal("expected to skip entirely when thermally throttled")
	}
	if len(ctrl.setCPUCalls) != 0 {
		t.Fatal("expected no clock changes while thermally throttled")
	}
}

func TestTick_DreamStateFile_Dreaming_DropsToAODTier(t *testing.T) {
	ctrl := &fakeController{dreamState: "dreaming", hasDreamState: true}
	st := &State{LastTier: TierBase}
	result := Tick(ctrl, st)

	if !result.TierChanged || result.NewTier != TierAOD {
		t.Fatal("expected AOD tier when dream_state=dreaming")
	}
	if len(ctrl.setCPUCalls) != 1 || ctrl.setCPUCalls[0] != AODCPUPct {
		t.Fatalf("expected CPU set to AOD pct, got %v", ctrl.setCPUCalls)
	}
}

func TestTick_DreamStateFile_Awake_RestoresBaseline(t *testing.T) {
	ctrl := &fakeController{dreamState: "awake", hasDreamState: true}
	st := &State{LastTier: TierAOD}
	result := Tick(ctrl, st)

	if !result.TierChanged || result.NewTier != TierBase {
		t.Fatal("expected base tier when dream_state=awake")
	}
	if len(ctrl.setCPUCalls) != 1 || ctrl.setCPUCalls[0] != BaseCPUPct {
		t.Fatalf("expected CPU set to base pct, got %v", ctrl.setCPUCalls)
	}
}

func TestTick_NoChangeWhenTierAlreadyCorrect(t *testing.T) {
	ctrl := &fakeController{dreamState: "dreaming", hasDreamState: true}
	st := &State{LastTier: TierAOD}
	result := Tick(ctrl, st)

	if result.TierChanged {
		t.Fatal("expected no redundant clock change when tier is already correct")
	}
	if len(ctrl.setCPUCalls) != 0 {
		t.Fatalf("expected no calls, got %v", ctrl.setCPUCalls)
	}
}

func TestTick_FallsBackToWakefulnessWhenDreamStateMissing(t *testing.T) {
	ctrl := &fakeController{hasDreamState: false, wakefulness: "Asleep"}
	st := &State{LastTier: TierBase}
	result := Tick(ctrl, st)

	if !result.TierChanged || result.NewTier != TierAOD {
		t.Fatal("expected AOD tier from wakefulness fallback when dream_state file is missing/stale")
	}
}

func TestTick_WakefulnessAwake_RestoresBaseline(t *testing.T) {
	ctrl := &fakeController{hasDreamState: false, wakefulness: "Awake"}
	st := &State{LastTier: TierAOD}
	result := Tick(ctrl, st)

	if !result.TierChanged || result.NewTier != TierBase {
		t.Fatal("expected base tier when Awake via wakefulness fallback")
	}
}
