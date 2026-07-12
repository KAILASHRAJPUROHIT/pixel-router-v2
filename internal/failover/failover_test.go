package failover

import (
	"testing"
	"time"
)

func TestShouldResendPeriodicSMS(t *testing.T) {
	base := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)

	if !ShouldResendPeriodicSMS(base, time.Time{}) {
		t.Fatal("expected resend when never sent before (zero time)")
	}
	if ShouldResendPeriodicSMS(base, base.Add(-10*time.Minute)) {
		t.Fatal("expected no resend only 10 minutes after last send (interval is 30 min)")
	}
	if !ShouldResendPeriodicSMS(base, base.Add(-31*time.Minute)) {
		t.Fatal("expected resend after 31 minutes")
	}
}

func TestPresenceSMSDecision_FiresOncePerApproach(t *testing.T) {
	send, sent := PresenceSMSDecision(true, false)
	if !send || !sent {
		t.Fatal("expected to send and set sent flag on first confirmed presence")
	}

	send, sent = PresenceSMSDecision(true, true)
	if send || !sent {
		t.Fatal("expected no resend while still present and already sent this approach")
	}

	send, sent = PresenceSMSDecision(false, true)
	if send || sent {
		t.Fatal("expected sent flag to reset once presence is lost")
	}

	send, sent = PresenceSMSDecision(true, false)
	if !send || !sent {
		t.Fatal("expected a fresh send on re-approach after presence was lost")
	}
}

func TestPresenceSMSDecision_NeverPresent(t *testing.T) {
	send, sent := PresenceSMSDecision(false, false)
	if send || sent {
		t.Fatal("expected no send when never present")
	}
}
