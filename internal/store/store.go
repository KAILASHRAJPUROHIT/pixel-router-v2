// Package store centralizes V2's persistent state in one SQLite
// database instead of V1's ~80 scattered flat files with no schema -
// the pattern directly responsible for several V1 bugs this project
// found (corrupted filenames, dead per-device entries nobody pruned,
// stale config nobody remembered existed). Uses the pure-Go
// modernc.org/sqlite driver - no cgo, no C toolchain dependency,
// confirmed working natively on this exact device.
package store

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// Confirmed live 2026-07-12: multiple V2 services (webserver, quota,
	// devicescan, ...) all open this same file at boot, and one lost a
	// SQLITE_BUSY race during schema creation without this - SQLite's
	// default is to fail immediately on a lock conflict rather than
	// wait, which single-process-per-db assumptions elsewhere in the
	// codebase don't account for. busy_timeout makes a blocked writer
	// retry for up to 5s before giving up, which is enough to ride out
	// a one-time schema-creation race between services starting
	// simultaneously.
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}
	schema := `
	CREATE TABLE IF NOT EXISTS sessions (
		token TEXT PRIMARY KEY,
		expires_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS failed_attempts (
		client_key TEXT PRIMARY KEY,
		count INTEGER NOT NULL,
		last_attempt_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS recovery_codes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		code_hash TEXT NOT NULL,
		used INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		used_at INTEGER
	);
	CREATE TABLE IF NOT EXISTS devices (
		mac TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		band TEXT NOT NULL DEFAULT '',
		cumulative_bytes INTEGER NOT NULL DEFAULT 0,
		first_seen INTEGER NOT NULL,
		last_seen INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS billing_cycle (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		billing_day INTEGER NOT NULL DEFAULT 1,
		last_reset_month TEXT NOT NULL DEFAULT '',
		cycle_bytes INTEGER NOT NULL DEFAULT 0,
		prev_sum INTEGER NOT NULL DEFAULT 0,
		cutoff_active INTEGER NOT NULL DEFAULT 0
	);
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	// ALTER TABLE ADD COLUMN has no IF NOT EXISTS support on the SQLite
	// version this driver ships - the "duplicate column" error is
	// SQLite's own message for "already migrated", so it's ignored
	// rather than treated as a real failure, making this safe to run on
	// every Open() call against an already-migrated database.
	if _, err := db.Exec(`ALTER TABLE devices ADD COLUMN blocked INTEGER NOT NULL DEFAULT 0`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		db.Close()
		return nil, fmt.Errorf("migrate devices.blocked: %w", err)
	}
	return &Store{db: db}, nil
}

// BackupTo writes a consistent point-in-time copy of the whole
// database to destPath using SQLite's own VACUUM INTO - safe to run
// against a live database being actively written by other connections
// (unlike a raw file copy, which could otherwise capture a torn/
// inconsistent snapshot mid-write). destPath must not already exist;
// VACUUM INTO refuses to overwrite a file, so the caller is expected
// to pass a fresh timestamped name each time (see cmd/dbbackup).
func (s *Store) BackupTo(destPath string) error {
	_, err := s.db.Exec("VACUUM INTO ?", destPath)
	if err != nil {
		return fmt.Errorf("vacuum into %s: %w", destPath, err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) CreateSession(token string, ttl time.Duration) error {
	expiresAt := time.Now().Add(ttl).Unix()
	_, err := s.db.Exec(`INSERT INTO sessions (token, expires_at) VALUES (?, ?)`, token, expiresAt)
	return err
}

func (s *Store) IsSessionValid(token string) bool {
	if token == "" {
		return false
	}
	var expiresAt int64
	err := s.db.QueryRow(`SELECT expires_at FROM sessions WHERE token = ?`, token).Scan(&expiresAt)
	if err != nil {
		return false
	}
	return time.Now().Unix() < expiresAt
}

func (s *Store) PruneExpiredSessions() error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
	return err
}

const lockoutThreshold = 5
const lockoutWindow = 15 * time.Minute

// RecordFailedAttempt increments the failure count for clientKey,
// resetting it first if the previous failure was outside the lockout
// window (same "detect and lock out" logic V1's totp_login.cgi used,
// now living in one place instead of duplicated shell).
func (s *Store) RecordFailedAttempt(clientKey string) (int, error) {
	now := time.Now().Unix()
	var count int
	var lastAttempt int64
	err := s.db.QueryRow(`SELECT count, last_attempt_at FROM failed_attempts WHERE client_key = ?`, clientKey).Scan(&count, &lastAttempt)
	if err == sql.ErrNoRows {
		count = 0
	} else if err != nil {
		return 0, err
	}

	if time.Now().Unix()-lastAttempt > int64(lockoutWindow.Seconds()) {
		count = 0
	}
	count++

	_, err = s.db.Exec(`INSERT INTO failed_attempts (client_key, count, last_attempt_at) VALUES (?, ?, ?)
		ON CONFLICT(client_key) DO UPDATE SET count=excluded.count, last_attempt_at=excluded.last_attempt_at`,
		clientKey, count, now)
	return count, err
}

func (s *Store) IsLockedOut(clientKey string) (bool, error) {
	var count int
	var lastAttempt int64
	err := s.db.QueryRow(`SELECT count, last_attempt_at FROM failed_attempts WHERE client_key = ?`, clientKey).Scan(&count, &lastAttempt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if count < lockoutThreshold {
		return false, nil
	}
	return time.Now().Unix()-lastAttempt < int64(lockoutWindow.Seconds()), nil
}

func (s *Store) ClearFailedAttempts(clientKey string) error {
	_, err := s.db.Exec(`DELETE FROM failed_attempts WHERE client_key = ?`, clientKey)
	return err
}

const recoveryCodeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567" // no 0/1/O/I ambiguity

func generateRecoveryCode() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	var sb strings.Builder
	for i, x := range b {
		if i == 4 {
			sb.WriteByte('-')
		}
		sb.WriteByte(recoveryCodeAlphabet[int(x)%len(recoveryCodeAlphabet)])
	}
	return sb.String(), nil
}

// GenerateRecoveryCodes replaces any existing recovery codes with a
// fresh set of n one-time-use codes - the direct fix for a real gap
// found in both V1 and the early V2 build: if the device with Authy on
// it is ever lost, factory-reset, or has its app data cleared, there
// was no way back into the dashboard at all, since the TOTP secret
// only ever existed on the router and in Authy. Returns the plaintext
// codes ONCE (only hashes are persisted) - this is the only time
// they're ever visible, same convention as GitHub/Google/every real
// 2FA recovery-code flow.
func (s *Store) GenerateRecoveryCodes(n int) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM recovery_codes`); err != nil {
		return nil, err
	}

	codes := make([]string, 0, n)
	now := time.Now().Unix()
	for i := 0; i < n; i++ {
		code, err := generateRecoveryCode()
		if err != nil {
			return nil, err
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT INTO recovery_codes (code_hash, used, created_at) VALUES (?, 0, ?)`, string(hash), now); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return codes, nil
}

// CheckAndConsumeRecoveryCode returns true and marks the matching code
// used if submitted matches any unused recovery code. Single-use by
// design - a recovery code that stayed valid forever after use would
// just be a permanent second password with none of a TOTP code's
// time-boxing.
func (s *Store) CheckAndConsumeRecoveryCode(submitted string) (bool, error) {
	normalized := strings.ToUpper(strings.TrimSpace(submitted))
	if normalized == "" {
		return false, nil
	}

	rows, err := s.db.Query(`SELECT id, code_hash FROM recovery_codes WHERE used = 0`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	type candidate struct {
		id   int64
		hash string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.hash); err != nil {
			return false, err
		}
		candidates = append(candidates, c)
	}

	for _, c := range candidates {
		if bcrypt.CompareHashAndPassword([]byte(c.hash), []byte(normalized)) == nil {
			_, err := s.db.Exec(`UPDATE recovery_codes SET used = 1, used_at = ? WHERE id = ?`, time.Now().Unix(), c.id)
			return true, err
		}
	}
	return false, nil
}

// RemainingRecoveryCodes reports how many unused codes are left, so
// the dashboard can warn the user to regenerate before they run out.
func (s *Store) RemainingRecoveryCodes() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM recovery_codes WHERE used = 0`).Scan(&count)
	return count, err
}
