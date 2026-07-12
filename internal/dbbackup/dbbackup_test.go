package dbbackup

import (
	"testing"
	"time"
)

func TestBackupFilename_SortsChronologically(t *testing.T) {
	t1 := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 7, 11, 11, 0, 0, 0, time.UTC)
	f1, f2 := BackupFilename(t1), BackupFilename(t2)
	if f1 >= f2 {
		t.Fatalf("expected %q to sort before %q lexically", f1, f2)
	}
}

func TestFilesToPrune_KeepsNewestN(t *testing.T) {
	existing := []string{
		"/state/backups/router-20260709-100000.db",
		"/state/backups/router-20260710-100000.db",
		"/state/backups/router-20260711-100000.db",
		"/state/backups/router-20260708-100000.db",
	}
	got := FilesToPrune(existing, 2)
	want := map[string]bool{
		"/state/backups/router-20260708-100000.db": true,
		"/state/backups/router-20260709-100000.db": true,
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 files to prune, got %d: %v", len(got), got)
	}
	for _, f := range got {
		if !want[f] {
			t.Fatalf("unexpected file marked for pruning: %s", f)
		}
	}
}

func TestFilesToPrune_FewerThanKeep_NoneRemoved(t *testing.T) {
	existing := []string{
		"/state/backups/router-20260711-100000.db",
	}
	if got := FilesToPrune(existing, 5); len(got) != 0 {
		t.Fatalf("expected nothing pruned when count <= keep, got %v", got)
	}
}

func TestFilesToPrune_IgnoresUnrelatedFiles(t *testing.T) {
	existing := []string{
		"/state/backups/router-20260709-100000.db",
		"/state/backups/router-20260710-100000.db",
		"/state/backups/README.txt",
		"/state/backups/router.db.tmp",
	}
	got := FilesToPrune(existing, 1)
	if len(got) != 1 || got[0] != "/state/backups/router-20260709-100000.db" {
		t.Fatalf("expected only the older real backup pruned, got %v", got)
	}
}
