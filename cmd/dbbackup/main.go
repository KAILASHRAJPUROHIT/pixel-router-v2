// Command dbbackup periodically snapshots router.db via SQLite's own
// VACUUM INTO (consistent even against a live, actively-written
// database) and prunes old backups beyond a configured count.
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/antriva/pixel-router-v2/internal/dbbackup"
	"github.com/antriva/pixel-router-v2/internal/store"
)

func main() {
	dbPath := flag.String("db", "/data/adb/router_v2/state/router.db", "path to the live SQLite state db")
	backupDir := flag.String("backup-dir", "/data/adb/router_v2/state/backups", "directory to write timestamped backups into")
	interval := flag.Duration("interval", 6*time.Hour, "how often to take a backup")
	keep := flag.Int("keep", 10, "how many recent backups to retain")
	flag.Parse()

	if err := os.MkdirAll(*backupDir, 0700); err != nil {
		log.Fatalf("create backup dir: %v", err)
	}

	for {
		if err := runBackup(*dbPath, *backupDir, *keep); err != nil {
			log.Printf("backup failed: %v", err)
		}
		time.Sleep(*interval)
	}
}

func runBackup(dbPath, backupDir string, keep int) error {
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	destPath := filepath.Join(backupDir, dbbackup.BackupFilename(time.Now()))
	if err := st.BackupTo(destPath); err != nil {
		return err
	}
	log.Printf("backed up to %s", destPath)

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, filepath.Join(backupDir, e.Name()))
	}
	for _, stale := range dbbackup.FilesToPrune(names, keep) {
		if err := os.Remove(stale); err != nil {
			log.Printf("prune %s: %v", stale, err)
		} else {
			log.Printf("pruned old backup %s", stale)
		}
	}
	return nil
}
