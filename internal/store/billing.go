package store

// BillingCycle mirrors V1's billing_cycle_day/billing_last_reset/
// cellular_cycle_bytes/data_cutoff_active flat files as one row.
type BillingCycle struct {
	BillingDay     int
	LastResetMonth string
	CycleBytes     int64
	PrevSum        int64
	CutoffActive   bool
}

func (s *Store) GetBillingCycle() (BillingCycle, error) {
	var bc BillingCycle
	var cutoffInt int
	err := s.db.QueryRow(`SELECT billing_day, last_reset_month, cycle_bytes, prev_sum, cutoff_active FROM billing_cycle WHERE id = 1`).
		Scan(&bc.BillingDay, &bc.LastResetMonth, &bc.CycleBytes, &bc.PrevSum, &cutoffInt)
	if err != nil {
		// No row yet - defaults matching V1's own first-boot defaults
		// (billing day 1, everything else zeroed).
		_, insertErr := s.db.Exec(`INSERT OR IGNORE INTO billing_cycle (id, billing_day) VALUES (1, 1)`)
		if insertErr != nil {
			return bc, insertErr
		}
		return BillingCycle{BillingDay: 1}, nil
	}
	bc.CutoffActive = cutoffInt == 1
	return bc, nil
}

func (s *Store) SaveBillingCycle(bc BillingCycle) error {
	cutoffInt := 0
	if bc.CutoffActive {
		cutoffInt = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO billing_cycle (id, billing_day, last_reset_month, cycle_bytes, prev_sum, cutoff_active)
		VALUES (1, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			billing_day = excluded.billing_day,
			last_reset_month = excluded.last_reset_month,
			cycle_bytes = excluded.cycle_bytes,
			prev_sum = excluded.prev_sum,
			cutoff_active = excluded.cutoff_active`,
		bc.BillingDay, bc.LastResetMonth, bc.CycleBytes, bc.PrevSum, cutoffInt)
	return err
}

func (s *Store) SetBillingDay(day int) error {
	bc, err := s.GetBillingCycle()
	if err != nil {
		return err
	}
	bc.BillingDay = day
	return s.SaveBillingCycle(bc)
}
