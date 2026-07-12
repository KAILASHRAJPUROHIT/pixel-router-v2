package dreamwatchdog

import "testing"

func TestThermalGate_OscillationAroundEnterDoesNotFlap(t *testing.T) {
	g := NewThermalGate()

	// Below the enter threshold - stays unpaused.
	if g.Update(43.0, true, 0, true) {
		t.Fatal("expected not paused at 43.0C")
	}

	// Crosses the enter threshold - pauses.
	if !g.Update(45.7, true, 0, true) {
		t.Fatal("expected paused at 45.7C")
	}

	// This is the exact bug: a brief dip back under the enter threshold
	// (but still above the exit threshold) used to immediately resume
	// the old single-line check. The gate must stay paused.
	if !g.Update(44.5, true, 0, true) {
		t.Fatal("expected to remain paused at 44.5C (above exit threshold, below enter threshold)")
	}
	if !g.Update(44.9, true, 0, true) {
		t.Fatal("expected to remain paused at 44.9C")
	}

	// Only once temp genuinely cools to the exit threshold does the
	// gate release.
	if g.Update(44.0, true, 0, true) {
		t.Fatal("expected released at 44.0C (at exit threshold)")
	}

	// And re-entering works the same way afterward.
	if !g.Update(45.0, true, 0, true) {
		t.Fatal("expected paused again at 45.0C after having released")
	}
}

func TestThermalGate_SevereStatusAlwaysPauses(t *testing.T) {
	g := NewThermalGate()
	if !g.Update(30.0, true, 3, true) {
		t.Fatal("expected paused when thermal status is SEVERE (3), regardless of skin temp")
	}
	// Status back to normal but temp still above exit threshold - stays paused.
	if !g.Update(44.5, true, 0, true) {
		t.Fatal("expected to remain paused - temp hasn't reached the exit threshold yet")
	}
}

func TestThermalGate_MissingReadingsNeverFlipState(t *testing.T) {
	g := NewThermalGate()
	if g.Update(0, false, 0, false) {
		t.Fatal("expected not paused when no readings are available and gate started unpaused")
	}

	g.Update(46.0, true, 0, true)
	if !g.paused {
		t.Fatal("setup: expected paused")
	}
	if !g.Update(0, false, 0, false) {
		t.Fatal("expected to remain paused when a subsequent tick has no readings at all")
	}
}
