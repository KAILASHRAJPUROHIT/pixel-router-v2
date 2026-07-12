package smsviewer

import "testing"

// Real captured `content query` output shape, confirmed live 2026-07-12.
const sampleOutput = `Row: 0 address=JX-DOTMUM-G, body=Test message one, date=1783831182362
Row: 1 address=51026, body=Thanks for calling, we would like your feedback, date=1783764558705
Row: 2 address=AD-AIRINF-T, body=<#> 1682 is your OTP to login, date=1783752097211
`

func TestParse_ExtractsAllRows(t *testing.T) {
	msgs := Parse(sampleOutput, 10)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Address != "JX-DOTMUM-G" || msgs[0].Body != "Test message one" || msgs[0].DateMs != 1783831182362 {
		t.Fatalf("unexpected first message: %+v", msgs[0])
	}
	if msgs[1].Body != "Thanks for calling, we would like your feedback" {
		t.Fatalf("expected body with embedded comma preserved, got %q", msgs[1].Body)
	}
}

func TestParse_RespectsLimit(t *testing.T) {
	msgs := Parse(sampleOutput, 2)
	if len(msgs) != 2 {
		t.Fatalf("expected exactly 2 messages (limit), got %d", len(msgs))
	}
}

func TestParse_BodyContainingDateMarker_UsesLastOccurrence(t *testing.T) {
	// A body that coincidentally contains the literal ", date=" text -
	// the real assigned date (from the query's own date column) must
	// still be extracted from the LAST occurrence, not the one embedded
	// in the message body itself.
	tricky := `Row: 0 address=Test, body=Your bill, date=12 is due, date=1783831182362
`
	msgs := Parse(tricky, 10)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Body != "Your bill, date=12 is due" {
		t.Fatalf("expected embedded ', date=' text preserved in body, got %q", msgs[0].Body)
	}
	if msgs[0].DateMs != 1783831182362 {
		t.Fatalf("expected the real trailing date extracted, got %d", msgs[0].DateMs)
	}
}

func TestParse_EmptyOutput_ReturnsNoMessages(t *testing.T) {
	msgs := Parse("", 10)
	if len(msgs) != 0 {
		t.Fatalf("expected no messages for empty output, got %d", len(msgs))
	}
}
