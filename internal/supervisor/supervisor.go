// Package supervisor is V2's replacement for V1's "N independent shell
// scripts, each polling ps -ef every 60-120s to notice a dead sibling"
// pattern. That pattern was the root cause behind most of what V1's
// audit/fix pass found: CPU-frequency drift, tc qdisc churn, duplicate
// iptables rules, and the AOD/Doze fight all had the same shape -
// something goes wrong, and nothing notices until the next poll tick,
// sometimes minutes later. A real supervisor instead learns about a
// child's death the moment the kernel reports it (via Wait()), and
// restarts with backoff instead of a fixed poll interval - no window
// where a dead service silently isn't running.
package supervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

type RestartPolicy string

const (
	RestartAlways    RestartPolicy = "always"
	RestartOnFailure RestartPolicy = "on-failure"
	RestartNever     RestartPolicy = "never"
)

type ServiceSpec struct {
	Name             string        `json:"name"`
	Command          string        `json:"command"`
	Args             []string      `json:"args"`
	Restart          RestartPolicy `json:"restart"`
	BackoffInitialMs int           `json:"backoff_initial_ms"`
	BackoffMaxMs     int           `json:"backoff_max_ms"`
	// A restart within this many ms of starting counts as a crash (not
	// a clean, long-lived run that just happened to exit) - this is
	// what drives backoff growth vs reset.
	CrashThresholdMs int `json:"crash_threshold_ms"`
}

type ServiceState string

const (
	StateStarting ServiceState = "starting"
	StateRunning  ServiceState = "running"
	StateBackoff  ServiceState = "backoff"
	StateStopped  ServiceState = "stopped"
)

type ServiceStatus struct {
	Name         string       `json:"name"`
	State        ServiceState `json:"state"`
	Pid          int          `json:"pid,omitempty"`
	LastExitCode int          `json:"last_exit_code"`
	RestartCount int          `json:"restart_count"`
	StartedAt    string       `json:"started_at,omitempty"`
	LastEventAt  string       `json:"last_event_at"`
}

type LogEvent struct {
	Ts           string `json:"ts"`
	Level        string `json:"level"`
	Service      string `json:"service"`
	Event        string `json:"event"`
	Detail       string `json:"detail,omitempty"`
	ExitCode     *int   `json:"exit_code,omitempty"`
	RanMs        *int64 `json:"ran_ms,omitempty"`
	RestartCount int    `json:"restart_count,omitempty"`
}

type Supervisor struct {
	StatusPath string
	LogPath    string
	// ServiceLogDir, if set, captures each service's own stdout/stderr
	// into <dir>/<name>.log - confirmed live 2026-07-11 that without
	// this, every service's own log.Printf output (not just the
	// supervisor's own structured start/stop/backoff events) was
	// silently discarded, since exec.Cmd defaults to a nil Writer
	// rather than inheriting the parent's own stdout/stderr.
	ServiceLogDir string
	// Alerter, if set, gets a RecordRestart call every time a service
	// restarts - nil is a valid, fully-supported "alerting disabled"
	// state (CrashLoopAlerter.RecordRestart no-ops on a nil receiver).
	Alerter *CrashLoopAlerter

	mu       sync.Mutex
	statuses map[string]*ServiceStatus
	stopping bool

	logMu sync.Mutex
	logFh *os.File
}

func New(statusPath, logPath, serviceLogDir string) (*Supervisor, error) {
	fh, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	return &Supervisor{
		StatusPath:    statusPath,
		LogPath:       logPath,
		ServiceLogDir: serviceLogDir,
		statuses:      make(map[string]*ServiceStatus),
		logFh:         fh,
	}, nil
}

func (s *Supervisor) log(level, service, event, detail string, exitCode *int, ranMs *int64, restartCount int) {
	ev := LogEvent{
		Ts:           time.Now().UTC().Format(time.RFC3339Nano),
		Level:        level,
		Service:      service,
		Event:        event,
		Detail:       detail,
		ExitCode:     exitCode,
		RanMs:        ranMs,
		RestartCount: restartCount,
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.logFh.Write(line)
	s.logFh.Write([]byte("\n"))
}

func (s *Supervisor) setStatus(st *ServiceStatus) {
	s.mu.Lock()
	s.statuses[st.Name] = st
	// Copy the map to avoid holding the lock during file I/O.
	snapshot := make(map[string]*ServiceStatus, len(s.statuses))
	for k, v := range s.statuses {
		cp := *v
		snapshot[k] = &cp
	}
	s.mu.Unlock()
	s.writeStatusFile(snapshot)
}

// Atomic write (temp file + rename) - a direct, deliberate fix for the
// class of bug V1 hit repeatedly (a corrupted/partial file left behind
// by an interrupted write, e.g. the literal \r\r-suffixed log filename
// found and removed during V1's cleanup pass). A rename is atomic at
// the filesystem level; a reader never sees a half-written file.
func (s *Supervisor) writeStatusFile(snapshot map[string]*ServiceStatus) {
	tmp := s.StatusPath + ".tmp"
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return
	}
	os.Rename(tmp, s.StatusPath)
}

func (s *Supervisor) IsStopping() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopping
}

func (s *Supervisor) StopAll() {
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()
}

// protectFromLowMemoryKiller writes the most negative valid
// oom_score_adj (-1000, the same value Android gives its own
// persistent system_server-tier processes) so lmkd treats this child
// as effectively unkillable under memory pressure. Confirmed live
// 2026-07-12 as the likely root cause of a real, recurring incident: a
// connected laptop kept losing and regaining internet every 15-90
// minutes, traced through supervisor.jsonl to the hotspot service
// exiting with exit_code -1 (Go's exec package reports -1 specifically
// for a signal-terminated child, never observed with any panic/fatal
// logged beforehand) - consistent with lmkd killing it, since nothing
// in this codebase had ever protected supervised children from it.
// Every hotspot process restart tears the AP radio down and back up
// (see cmd/hotspot/main.go's own doc comment), deauthorizing every
// connected client as a side effect - exactly the symptom reported.
// Best-effort: a failure here (e.g. running as a non-root test, or a
// kernel without this file) shouldn't stop the service from running,
// just leave it as exposed to lmkd as it always was.
func protectFromLowMemoryKiller(pid int) {
	os.WriteFile(fmt.Sprintf("/proc/%d/oom_score_adj", pid), []byte("-1000"), 0644)
}

// Run starts the service and blocks, restarting it per policy until
// the supervisor is told to stop. Meant to be called in its own
// goroutine per service.
func (s *Supervisor) Run(spec ServiceSpec) {
	backoff := time.Duration(spec.BackoffInitialMs) * time.Millisecond
	maxBackoff := time.Duration(spec.BackoffMaxMs) * time.Millisecond
	crashThreshold := time.Duration(spec.CrashThresholdMs) * time.Millisecond
	restartCount := 0

	for {
		if s.IsStopping() {
			s.setStatus(&ServiceStatus{Name: spec.Name, State: StateStopped, RestartCount: restartCount, LastEventAt: time.Now().UTC().Format(time.RFC3339)})
			return
		}

		cmd := exec.Command(spec.Command, spec.Args...)
		var serviceLogFh *os.File
		if s.ServiceLogDir != "" {
			if fh, err := os.OpenFile(s.ServiceLogDir+"/"+spec.Name+".log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
				serviceLogFh = fh
				cmd.Stdout = fh
				cmd.Stderr = fh
			}
		}
		startedAt := time.Now()
		if err := cmd.Start(); err != nil {
			if serviceLogFh != nil {
				serviceLogFh.Close()
			}
			s.log("error", spec.Name, "start_failed", err.Error(), nil, nil, restartCount)
			s.setStatus(&ServiceStatus{Name: spec.Name, State: StateBackoff, RestartCount: restartCount, LastEventAt: time.Now().UTC().Format(time.RFC3339)})
			time.Sleep(backoff)
			continue
		}

		protectFromLowMemoryKiller(cmd.Process.Pid)

		s.setStatus(&ServiceStatus{
			Name: spec.Name, State: StateRunning, Pid: cmd.Process.Pid,
			RestartCount: restartCount, StartedAt: startedAt.UTC().Format(time.RFC3339),
			LastEventAt: time.Now().UTC().Format(time.RFC3339),
		})
		s.log("info", spec.Name, "started", "", nil, nil, restartCount)

		err := cmd.Wait()
		if serviceLogFh != nil {
			serviceLogFh.Close()
		}
		ranFor := time.Since(startedAt)
		ranMs := ranFor.Milliseconds()
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
		}

		s.setStatus(&ServiceStatus{
			Name: spec.Name, State: StateBackoff, LastExitCode: exitCode,
			RestartCount: restartCount, LastEventAt: time.Now().UTC().Format(time.RFC3339),
		})
		s.log("warn", spec.Name, "exited", "", &exitCode, &ranMs, restartCount)

		if spec.Restart == RestartNever {
			s.setStatus(&ServiceStatus{Name: spec.Name, State: StateStopped, LastExitCode: exitCode, RestartCount: restartCount, LastEventAt: time.Now().UTC().Format(time.RFC3339)})
			return
		}
		if spec.Restart == RestartOnFailure && exitCode == 0 {
			s.setStatus(&ServiceStatus{Name: spec.Name, State: StateStopped, LastExitCode: exitCode, RestartCount: restartCount, LastEventAt: time.Now().UTC().Format(time.RFC3339)})
			return
		}

		if ranFor < crashThreshold {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			s.log("warn", spec.Name, "crash_loop_backoff", fmt.Sprintf("ran only %dms, backing off to %s", ranMs, backoff), nil, nil, restartCount)
		} else {
			backoff = time.Duration(spec.BackoffInitialMs) * time.Millisecond
		}

		restartCount++
		s.Alerter.RecordRestart(spec.Name, time.Now())
		if s.IsStopping() {
			s.setStatus(&ServiceStatus{Name: spec.Name, State: StateStopped, RestartCount: restartCount, LastEventAt: time.Now().UTC().Format(time.RFC3339)})
			return
		}
		time.Sleep(backoff)
	}
}
