package supervisor

import (
	"testing"
	"time"
)

func TestCrashLoopAlerter_FiresAtThreshold(t *testing.T) {
	var sent []string
	a := NewCrashLoopAlerter(3, 5*time.Minute, time.Hour, func(msg string) error {
		sent = append(sent, msg)
		return nil
	})

	base := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	a.RecordRestart("webserver", base)
	a.RecordRestart("webserver", base.Add(1*time.Minute))
	if len(sent) != 0 {
		t.Fatalf("expected no alert before threshold, got %v", sent)
	}
	a.RecordRestart("webserver", base.Add(2*time.Minute))
	if len(sent) != 1 {
		t.Fatalf("expected exactly one alert at the 3rd restart within the window, got %v", sent)
	}
}

func TestCrashLoopAlerter_OldRestartsFallOutOfWindow(t *testing.T) {
	var sent []string
	a := NewCrashLoopAlerter(3, 5*time.Minute, time.Hour, func(msg string) error {
		sent = append(sent, msg)
		return nil
	})

	base := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	a.RecordRestart("webserver", base)
	a.RecordRestart("webserver", base.Add(1*time.Minute))
	// This restart is 10 minutes after the first two - outside the 5min
	// window, so only 2 restarts (this one + itself) are "recent", not 3.
	a.RecordRestart("webserver", base.Add(11*time.Minute))
	if len(sent) != 0 {
		t.Fatalf("expected no alert - the first two restarts should have aged out of the window, got %v", sent)
	}
}

func TestCrashLoopAlerter_CooldownPreventsRepeatedAlerts(t *testing.T) {
	var sent []string
	a := NewCrashLoopAlerter(2, 5*time.Minute, 30*time.Minute, func(msg string) error {
		sent = append(sent, msg)
		return nil
	})

	base := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	a.RecordRestart("ttlproxy", base)
	a.RecordRestart("ttlproxy", base.Add(1*time.Minute))
	if len(sent) != 1 {
		t.Fatalf("expected first alert to fire, got %v", sent)
	}
	// Still crash-looping a minute later - within cooldown, shouldn't repeat.
	a.RecordRestart("ttlproxy", base.Add(2*time.Minute))
	if len(sent) != 1 {
		t.Fatalf("expected no repeat alert within cooldown, got %v", sent)
	}
	// Well past cooldown - should alert again if still crash-looping.
	a.RecordRestart("ttlproxy", base.Add(40*time.Minute))
	a.RecordRestart("ttlproxy", base.Add(41*time.Minute))
	if len(sent) != 2 {
		t.Fatalf("expected a second alert once cooldown passed, got %v", sent)
	}
}

func TestCrashLoopAlerter_DifferentServicesTrackedIndependently(t *testing.T) {
	var sent []string
	a := NewCrashLoopAlerter(2, 5*time.Minute, time.Hour, func(msg string) error {
		sent = append(sent, msg)
		return nil
	})

	base := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	a.RecordRestart("webserver", base)
	a.RecordRestart("ttlproxy", base)
	if len(sent) != 0 {
		t.Fatalf("expected no alert - each service only has 1 restart so far, got %v", sent)
	}
}

func TestCrashLoopAlerter_NilReceiverIsNoOp(t *testing.T) {
	var a *CrashLoopAlerter
	a.RecordRestart("webserver", time.Now()) // must not panic
}
