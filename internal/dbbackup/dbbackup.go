// Package dbbackup periodically snapshots router.db - it holds the
// password hash, TOTP secret, recovery codes, and device list, none of
// which existed anywhere before V2 (V1 had no equivalent single
// database, just scattered flat files) with zero backup story.
package dbbackup

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const timestampLayout = "20060102-150405"
const filePrefix = "router-"
const fileSuffix = ".db"

// BackupFilename builds a timestamped backup filename from now, in the
// same directory convention cmd/dbbackup uses.
func BackupFilename(now time.Time) string {
	return filePrefix + now.UTC().Format(timestampLayout) + fileSuffix
}

// FilesToPrune is the pure, testable core of rotation: given every
// backup filename currently in the directory and how many to keep,
// returns exactly the ones that should be deleted (oldest first is
// implied by the name's timestamp sorting lexically). Ignores
// filenames that don't match the expected pattern, so a stray unrelated
// file in the same directory is never a candidate for deletion.
func FilesToPrune(existing []string, keep int) []string {
	var ours []string
	for _, f := range existing {
		base := filepath.Base(f)
		if strings.HasPrefix(base, filePrefix) && strings.HasSuffix(base, fileSuffix) {
			ours = append(ours, f)
		}
	}
	sort.Strings(ours) // timestamp format sorts lexically == chronologically
	if len(ours) <= keep {
		return nil
	}
	return ours[:len(ours)-keep]
}
