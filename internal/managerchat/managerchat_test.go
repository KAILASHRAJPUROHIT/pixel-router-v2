package managerchat

import (
	"strings"
	"testing"
)

func healthyContext() Context {
	return Context{
		HotspotUp: true, InternetUp: true, SMSForwarderOK: true,
		ThermalOK: true, SignalOK: true, InternetType: "self",
		TunnelStatus: "alive", OnlineDeviceCount: 2,
		Services: map[string]ServiceStatus{
			"hotspot": {State: "running", RestartCount: 0},
		},
	}
}

func TestAnswer_StatusAllHealthy(t *testing.T) {
	got := Answer("what's the status?", healthyContext())
	if !strings.Contains(got, "healthy") {
		t.Fatalf("expected a healthy status reply, got %q", got)
	}
}

func TestAnswer_StatusWithProblems(t *testing.T) {
	ctx := healthyContext()
	ctx.InternetUp = false
	got := Answer("status", ctx)
	if !strings.Contains(got, "internet is down") {
		t.Fatalf("expected internet-down mentioned, got %q", got)
	}
}

func TestAnswer_Flapping_ReportsRestartedServices(t *testing.T) {
	ctx := healthyContext()
	ctx.Services["hotspot"] = ServiceStatus{State: "running", RestartCount: 3}
	got := Answer("is anything crashing or restarting?", ctx)
	if !strings.Contains(got, "hotspot (3 restarts)") {
		t.Fatalf("expected hotspot flap mentioned, got %q", got)
	}
}

func TestAnswer_Flapping_NoneFlapping(t *testing.T) {
	got := Answer("any flapping services?", healthyContext())
	if !strings.Contains(got, "Nothing's flapping") {
		t.Fatalf("expected no-flapping reply, got %q", got)
	}
}

func TestAnswer_Problems_ReportsTunnelDead(t *testing.T) {
	ctx := healthyContext()
	ctx.TunnelStatus = "dead"
	got := Answer("any problems?", ctx)
	if !strings.Contains(got, "tunnel is dead") {
		t.Fatalf("expected tunnel-dead mentioned, got %q", got)
	}
}

func TestAnswer_Reboot_NeverAutoReboots(t *testing.T) {
	got := Answer("can you reboot?", healthyContext())
	if !strings.Contains(got, "don't auto-reboot") {
		t.Fatalf("expected honest no-auto-reboot answer, got %q", got)
	}
}

func TestAnswer_Quota_CutoffActive(t *testing.T) {
	ctx := healthyContext()
	ctx.QuotaUsedGB = 300.5
	ctx.QuotaCutoffActive = true
	got := Answer("what's my data usage?", ctx)
	if !strings.Contains(got, "ACTIVE") || !strings.Contains(got, "300.5") {
		t.Fatalf("expected cutoff-active with usage figure, got %q", got)
	}
}

func TestAnswer_Devices_ReportsOnlineCount(t *testing.T) {
	got := Answer("who's connected right now?", healthyContext())
	if !strings.Contains(got, "2 device") {
		t.Fatalf("expected device count mentioned, got %q", got)
	}
}

func TestAnswer_Help_ListsCategories(t *testing.T) {
	got := Answer("help", healthyContext())
	if !strings.Contains(got, "rule-based") {
		t.Fatalf("expected honest rule-based disclosure in help text, got %q", got)
	}
}

func TestAnswer_Empty_PromptsForInput(t *testing.T) {
	got := Answer("", healthyContext())
	if !strings.Contains(got, "Say something") {
		t.Fatalf("expected a prompt for empty input, got %q", got)
	}
}

func TestAnswer_Unmatched_FallsBackGracefully(t *testing.T) {
	got := Answer("tell me a joke", healthyContext())
	if !strings.Contains(got, "don't have a specific answer") {
		t.Fatalf("expected a graceful fallback, got %q", got)
	}
}
