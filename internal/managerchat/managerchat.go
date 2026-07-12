// Package managerchat ports V1's dashboard "Chat with the Manager" -
// explicitly NOT a language model, exactly as V1's own header comment
// insisted: this matches keywords in what's typed against the router's
// real, already-collected state (the same data the dashboard's own
// cards already show) and answers from that, honestly, rather than
// pretending to be a general AI. It can only talk about what V2
// actually watches.
//
// V1's version leaned on a separate hotspot_manager.sh daemon with its
// own adaptive poll-interval/ops-budget/flap-detection state that V2's
// architecture (one supervisor + many independent services, each with
// its own restart_count) has no direct equivalent of - rather than
// building a parallel daemon just to answer chat questions, this reads
// straight from the same data the dashboard's /api/health already
// assembles.
package managerchat

import (
	"fmt"
	"sort"
	"strings"
)

type ServiceStatus struct {
	State        string
	RestartCount int
}

// Context is every real signal this package can answer questions from -
// all of it already collected elsewhere for the dashboard, passed in
// rather than fetched directly so Answer stays pure and testable.
type Context struct {
	HotspotUp         bool
	InternetUp        bool
	SMSForwarderOK    bool
	ThermalOK         bool
	SignalOK          bool
	InternetType      string
	TunnelStatus      string // "alive", "dead", "not_present", or ""
	QuotaUsedGB       float64
	QuotaCutoffActive bool
	OnlineDeviceCount int
	Services          map[string]ServiceStatus
}

// flapThreshold mirrors the spirit of V1's flap detection (a service
// that keeps restarting is worth flagging) without needing V1's exact
// sliding-window mechanism - any nonzero restart count on a currently-
// running service is enough to mention, since a healthy V2 service
// normally has restart_count 0 for its whole uptime.
const flapThreshold = 1

// Answer matches msg against a fixed set of keyword categories and
// replies from ctx - exported and pure so it's fully unit-testable
// without a device or any real state files.
func Answer(msg string, ctx Context) string {
	m := strings.ToLower(strings.TrimSpace(msg))

	switch {
	case m == "":
		return "Say something and I'll check the router's real state for an answer."

	case containsAny(m, "status", "how are you", "health", "all good"):
		return answerStatus(ctx)

	case containsAny(m, "flap", "restart", "crash", "loop"):
		return answerFlapping(ctx)

	case containsAny(m, "alert", "warn", "problem", "issue", "wrong"):
		return answerProblems(ctx)

	case containsAny(m, "reboot"):
		return "I don't auto-reboot the router - if you need one, use the \"Reboot Router\" button on the dashboard."

	case containsAny(m, "quota", "data usage", "billing", "cellular"):
		if ctx.QuotaCutoffActive {
			return fmt.Sprintf("Cellular quota cutoff is ACTIVE - %.1f GB used this cycle, switched to borrowed internet until the cycle resets.", ctx.QuotaUsedGB)
		}
		return fmt.Sprintf("%.1f GB used this cellular cycle, no cutoff active.", ctx.QuotaUsedGB)

	case containsAny(m, "tunnel", "vpn", "wireguard"):
		return answerTunnel(ctx)

	case containsAny(m, "device", "connected", "who's online", "whos online"):
		return fmt.Sprintf("%d device(s) online right now.", ctx.OnlineDeviceCount)

	case containsAny(m, "help", "what can you", "command"):
		return "Ask me about: status, problems/alerts, flapping/restarting services, quota/billing, the WireGuard tunnel, or connected devices. I'm rule-based, not a language model - I can only answer about what the router itself actually tracks."

	default:
		return "I don't have a specific answer for that. Try: status, problems, flapping services, quota, tunnel, or devices."
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func answerStatus(ctx Context) string {
	if ctx.HotspotUp && ctx.InternetUp && ctx.SMSForwarderOK && ctx.ThermalOK && ctx.SignalOK {
		return fmt.Sprintf("Everything looks healthy - hotspot, internet, SMS forwarding, thermal, and signal are all fine. Currently on %s internet.", orDefault(ctx.InternetType, "unknown"))
	}
	var issues []string
	if !ctx.HotspotUp {
		issues = append(issues, "hotspot is down")
	}
	if !ctx.InternetUp {
		issues = append(issues, "internet is down")
	}
	if !ctx.SMSForwarderOK {
		issues = append(issues, "SMS forwarder is down")
	}
	if !ctx.ThermalOK {
		issues = append(issues, "thermal is elevated")
	}
	if !ctx.SignalOK {
		issues = append(issues, "signal is poor/unknown")
	}
	return "Something's off: " + strings.Join(issues, ", ") + "."
}

func answerFlapping(ctx Context) string {
	var flapping []string
	for name, svc := range ctx.Services {
		if svc.RestartCount >= flapThreshold {
			flapping = append(flapping, fmt.Sprintf("%s (%d restarts)", name, svc.RestartCount))
		}
	}
	if len(flapping) == 0 {
		return "Nothing's flapping right now - no service has restarted since the last deploy/boot."
	}
	sort.Strings(flapping)
	return "These services have restarted at least once: " + strings.Join(flapping, ", ") + "."
}

func answerProblems(ctx Context) string {
	var problems []string
	if !ctx.InternetUp {
		problems = append(problems, "internet is offline")
	}
	if !ctx.SMSForwarderOK {
		problems = append(problems, "SMS forwarder is down")
	}
	if ctx.TunnelStatus == "dead" {
		problems = append(problems, "WireGuard tunnel is dead")
	}
	if ctx.QuotaCutoffActive {
		problems = append(problems, "cellular quota cutoff is active")
	}
	for name, svc := range ctx.Services {
		if svc.State != "running" {
			problems = append(problems, name+" is "+svc.State)
		}
	}
	if len(problems) == 0 {
		return "No problems I can see - nothing's tripped any of my checks."
	}
	sort.Strings(problems)
	return "Current issues: " + strings.Join(problems, ", ") + "."
}

func answerTunnel(ctx Context) string {
	switch ctx.TunnelStatus {
	case "alive":
		return "WireGuard tunnel is alive."
	case "dead":
		return "WireGuard tunnel is DEAD - it may need a manual reconnect on the phone."
	case "not_present":
		return "No WireGuard tunnel interface is present right now."
	default:
		return "I don't have a current reading on the tunnel status."
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
