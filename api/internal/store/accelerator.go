package store

// BoostedTx represents a transaction whose priority has been adjusted
// via bitcoind's prioritisetransaction RPC.
type BoostedTx struct {
	Txid            string  `json:"txid"`
	OriginalFeerate float64 `json:"original_feerate"` // sat/vB before boost
	BoostedFeerate  float64 `json:"boosted_feerate"`  // sat/vB after boost
	FeeDelta        int64   `json:"fee_delta"`        // sats applied
	Vsize           int64   `json:"vsize"`
	BoostedAt       int64   `json:"boosted_at"` // unix timestamp
}

const acceleratorSchema = `
CREATE TABLE IF NOT EXISTS boosted_txs (
    txid              TEXT PRIMARY KEY,
    original_feerate  REAL NOT NULL,
    boosted_feerate   REAL NOT NULL,
    fee_delta         INTEGER NOT NULL,
    vsize             INTEGER NOT NULL,
    boosted_at        INTEGER NOT NULL
);
`

// migrateAccelerator ensures the boosted_txs table exists.
func (s *BlockStore) migrateAccelerator() error {
	_, err := s.db.Exec(acceleratorSchema)
	return err
}

// InsertBoostedTx records a newly boosted transaction.
func (s *BlockStore) InsertBoostedTx(tx BoostedTx) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO boosted_txs(txid, original_feerate, boosted_feerate, fee_delta, vsize, boosted_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		tx.Txid, tx.OriginalFeerate, tx.BoostedFeerate, tx.FeeDelta, tx.Vsize, tx.BoostedAt,
	)
	return err
}

// RemoveBoostedTx deletes a boosted tx record (confirmed, dropped, or cancelled).
func (s *BlockStore) RemoveBoostedTx(txid string) error {
	_, err := s.db.Exec(`DELETE FROM boosted_txs WHERE txid = ?`, txid)
	return err
}

// ListBoostedTxs returns all currently boosted transactions, newest first.
func (s *BlockStore) ListBoostedTxs() ([]BoostedTx, error) {
	rows, err := s.db.Query(
		`SELECT txid, original_feerate, boosted_feerate, fee_delta, vsize, boosted_at
		 FROM boosted_txs ORDER BY boosted_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BoostedTx
	for rows.Next() {
		var tx BoostedTx
		if err := rows.Scan(&tx.Txid, &tx.OriginalFeerate, &tx.BoostedFeerate, &tx.FeeDelta, &tx.Vsize, &tx.BoostedAt); err != nil {
			return nil, err
		}
		out = append(out, tx)
	}
	return out, rows.Err()
}

// GetBoostedTx returns a single boosted tx by txid, or nil if not found.
func (s *BlockStore) GetBoostedTx(txid string) (*BoostedTx, error) {
	var tx BoostedTx
	err := s.db.QueryRow(
		`SELECT txid, original_feerate, boosted_feerate, fee_delta, vsize, boosted_at
		 FROM boosted_txs WHERE txid = ?`, txid,
	).Scan(&tx.Txid, &tx.OriginalFeerate, &tx.BoostedFeerate, &tx.FeeDelta, &tx.Vsize, &tx.BoostedAt)
	if err != nil {
		return nil, err
	}
	return &tx, nil
}
