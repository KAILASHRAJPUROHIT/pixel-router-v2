package store

import "time"

// Device replaces V1's per-MAC pair of flat files
// (perdevice/<mac>.bytes + perdevice/<mac>.prevsum) with one row - no
// more orphaned files silently surviving days after a device stops
// connecting (found and manually pruned during V1's cleanup pass).
type Device struct {
	MAC             string
	Name            string
	Band            string
	CumulativeBytes int64
	FirstSeen       time.Time
	LastSeen        time.Time
	Blocked         bool
}

// UpsertDeviceSeen records that mac was seen just now on the given
// band, adding bytesDelta to its running total. Creates the row (with
// FirstSeen set to now) if this MAC has never been seen before.
func (s *Store) UpsertDeviceSeen(mac, band string, bytesDelta int64) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`
		INSERT INTO devices (mac, band, cumulative_bytes, first_seen, last_seen)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(mac) DO UPDATE SET
			band = excluded.band,
			cumulative_bytes = cumulative_bytes + excluded.cumulative_bytes,
			last_seen = excluded.last_seen`,
		mac, band, bytesDelta, now, now)
	return err
}

func (s *Store) SetDeviceName(mac, name string) error {
	_, err := s.db.Exec(`UPDATE devices SET name = ? WHERE mac = ?`, name, mac)
	return err
}

// SetDeviceBlocked flips the persisted block flag - the caller is
// still responsible for actually applying/removing the iptables rule
// that enforces it (see internal/blocklist); this just records the
// intent so it survives a reboot and can be re-asserted at startup.
func (s *Store) SetDeviceBlocked(mac string, blocked bool) error {
	b := 0
	if blocked {
		b = 1
	}
	_, err := s.db.Exec(`UPDATE devices SET blocked = ? WHERE mac = ?`, b, mac)
	return err
}

func (s *Store) ListDevices() ([]Device, error) {
	rows, err := s.db.Query(`SELECT mac, name, band, cumulative_bytes, first_seen, last_seen, blocked FROM devices ORDER BY last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []Device
	for rows.Next() {
		var d Device
		var firstSeen, lastSeen int64
		var blocked int
		if err := rows.Scan(&d.MAC, &d.Name, &d.Band, &d.CumulativeBytes, &firstSeen, &lastSeen, &blocked); err != nil {
			return nil, err
		}
		d.FirstSeen = time.Unix(firstSeen, 0)
		d.LastSeen = time.Unix(lastSeen, 0)
		d.Blocked = blocked != 0
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

// PruneStaleDevices deletes any device not seen within olderThan -
// the direct, scheduled fix for the exact bug V1 needed a manual
// cleanup pass to catch (two per-device file pairs sitting dead for
// 4+ days, discovered only by an ad-hoc audit, not by design).
// Returns how many rows were removed.
func (s *Store) PruneStaleDevices(olderThan time.Duration) (int, error) {
	cutoff := time.Now().Add(-olderThan).Unix()
	res, err := s.db.Exec(`DELETE FROM devices WHERE last_seen < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
