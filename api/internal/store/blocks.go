// Package store persists Kamado runtime data that must survive process
// restarts. Currently: the found-block history. Uses modernc.org/sqlite
// (pure Go, no CGO) so the kamado-api binary stays statically linkable.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// BlockStore persists found blocks to a single SQLite file.
type BlockStore struct {
	db *sql.DB
}

// Block mirrors state.BlockRecord without the import cycle. The store
// package is the lower layer; the state package converts to/from this
// shape when reading and writing.
type Block struct {
	Height     int64
	Hash       string
	RewardBT   float64
	FoundAt    time.Time
	Source     string
	ShareDiff  float64
	OrphanedAt time.Time // zero value = not orphaned
	Chain      string    // "main", "test", "signet", or "" for legacy rows
	Miner      string    // workername (address.worker) who found the block
}

const schema = `
CREATE TABLE IF NOT EXISTS blocks (
    height       INTEGER PRIMARY KEY,
    hash         TEXT NOT NULL DEFAULT '',
    reward_btc   REAL NOT NULL DEFAULT 0,
    found_at     INTEGER NOT NULL,
    source       TEXT NOT NULL DEFAULT '',
    share_diff   REAL NOT NULL DEFAULT 0,
    orphaned_at  INTEGER NOT NULL DEFAULT 0,
    chain        TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS blocks_found_at_idx ON blocks(found_at);

CREATE TABLE IF NOT EXISTS hashrate_samples (
    t INTEGER PRIMARY KEY,
    v REAL NOT NULL
);

CREATE TABLE IF NOT EXISTS kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

// migrations additive only; safe to run every startup. SQLite ignores
// ADD COLUMN that already exists only if we check, so we query
// pragma table_info and apply missing ones.
func (s *BlockStore) migrate() error {
	rows, err := s.db.Query(`PRAGMA table_info(blocks)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		have[name] = true
	}
	if !have["share_diff"] {
		if _, err := s.db.Exec(`ALTER TABLE blocks ADD COLUMN share_diff REAL NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("store: add share_diff: %w", err)
		}
	}
	if !have["orphaned_at"] {
		if _, err := s.db.Exec(`ALTER TABLE blocks ADD COLUMN orphaned_at INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("store: add orphaned_at: %w", err)
		}
	}
	if !have["chain"] {
		if _, err := s.db.Exec(`ALTER TABLE blocks ADD COLUMN chain TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("store: add chain: %w", err)
		}
	}
	if !have["miner"] {
		if _, err := s.db.Exec(`ALTER TABLE blocks ADD COLUMN miner TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("store: add miner: %w", err)
		}
	}
	return nil
}

// Open initializes the store at path, creating the schema if needed.
// Callers are responsible for Close().
func Open(path string) (*BlockStore, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: schema: %w", err)
	}
	s := &BlockStore{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.migrateAccelerator(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: accelerator schema: %w", err)
	}
	return s, nil
}

func (s *BlockStore) Close() error {
	return s.db.Close()
}

// InsertBlock is idempotent, duplicate heights are ignored so replayed
// log events after a restart don't trip the primary key constraint.
// Returns (true, nil) if a new row was actually inserted, (false, nil)
// if the height was already present (replay or duplicate).
//
// Limitation: the schema PK is height alone, so a self-mined block at
// the same height as a previously-orphaned self-mined block at that
// height (vanishingly unlikely for a solo pool) would also be dropped.
// Callers should log the (false, nil) case loudly so this never goes
// unnoticed.
func (s *BlockStore) InsertBlock(b Block) (bool, error) {
	res, err := s.db.Exec(
		`INSERT OR IGNORE INTO blocks(height, hash, reward_btc, found_at, source, share_diff, chain, miner)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		b.Height, b.Hash, b.RewardBT, b.FoundAt.Unix(), b.Source, b.ShareDiff, b.Chain, b.Miner,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// HashratePoint is one persisted hashrate sample.
type HashratePoint struct {
	T int64
	V float64
}

// InsertHashrateSample appends a sample; duplicate timestamps are ignored.
func (s *BlockStore) InsertHashrateSample(t int64, v float64) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO hashrate_samples(t, v) VALUES (?, ?)`,
		t, v,
	)
	return err
}

// PruneHashrateBefore drops samples older than the given unix timestamp.
func (s *BlockStore) PruneHashrateBefore(cutoff int64) error {
	_, err := s.db.Exec(`DELETE FROM hashrate_samples WHERE t < ?`, cutoff)
	return err
}

// HashrateSince returns all samples with t >= from, oldest first.
func (s *BlockStore) HashrateSince(from int64) ([]HashratePoint, error) {
	rows, err := s.db.Query(
		`SELECT t, v FROM hashrate_samples WHERE t >= ? ORDER BY t ASC`,
		from,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HashratePoint
	for rows.Next() {
		var p HashratePoint
		if err := rows.Scan(&p.T, &p.V); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetKV reads a string value by key. Returns ("", nil) when the key is
// absent so callers can distinguish "no value yet" from a real error.
func (s *BlockStore) GetKV(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetKV writes or replaces a key's value.
func (s *BlockStore) SetKV(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO kv(key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

// Recent returns up to limit blocks, newest first.
func (s *BlockStore) Recent(limit int) ([]Block, error) {
	if limit <= 0 {
		limit = 256
	}
	rows, err := s.db.Query(
		`SELECT height, hash, reward_btc, found_at, source, share_diff, orphaned_at, chain, miner
		 FROM blocks ORDER BY height DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Block, 0, limit)
	for rows.Next() {
		var b Block
		var foundUnix, orphanedUnix int64
		if err := rows.Scan(&b.Height, &b.Hash, &b.RewardBT, &foundUnix, &b.Source, &b.ShareDiff, &orphanedUnix, &b.Chain, &b.Miner); err != nil {
			return nil, err
		}
		b.FoundAt = time.Unix(foundUnix, 0).UTC()
		if orphanedUnix > 0 {
			b.OrphanedAt = time.Unix(orphanedUnix, 0).UTC()
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return out, nil
}

// BlocksNeedingEnrichment returns blocks whose hash or reward is still
// unset and that were found within the lookback window. Older entries
// are ignored, if bitcoind couldn't tell us about a day-old block,
// retrying will keep failing.
func (s *BlockStore) BlocksNeedingEnrichment(since time.Time) ([]Block, error) {
	rows, err := s.db.Query(
		`SELECT height, hash, reward_btc, found_at, source, share_diff, orphaned_at, chain, miner
		 FROM blocks
		 WHERE found_at >= ? AND (hash = '' OR reward_btc = 0 OR miner = '')
		 ORDER BY height ASC`,
		since.Unix(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Block
	for rows.Next() {
		var b Block
		var foundUnix, orphanedUnix int64
		if err := rows.Scan(&b.Height, &b.Hash, &b.RewardBT, &foundUnix, &b.Source, &b.ShareDiff, &orphanedUnix, &b.Chain, &b.Miner); err != nil {
			return nil, err
		}
		b.FoundAt = time.Unix(foundUnix, 0).UTC()
		if orphanedUnix > 0 {
			b.OrphanedAt = time.Unix(orphanedUnix, 0).UTC()
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// UpdateEnrichment fills in hash, reward, and miner for an already-recorded
// block. No-op if the row doesn't exist.
func (s *BlockStore) UpdateEnrichment(height int64, hash string, reward float64, miner string) error {
	_, err := s.db.Exec(
		`UPDATE blocks SET hash = ?, reward_btc = ?, miner = ? WHERE height = ?`,
		hash, reward, miner, height,
	)
	return err
}

// MarkOrphaned stamps a block as reorged-out at the given time. The
// found_at + reward fields stay so the UI can still render it with a
// strikethrough.
func (s *BlockStore) MarkOrphaned(height int64, at time.Time) error {
	_, err := s.db.Exec(
		`UPDATE blocks SET orphaned_at = ? WHERE height = ?`,
		at.Unix(), height,
	)
	return err
}

// UnmarkOrphaned clears the orphaned_at stamp set by MarkOrphaned.
// Used to correct false positives caused by a network switch: a block
// found on testnet is not "orphaned" when the node switches to mainnet,
// it simply belongs to a different chain and must not be compared against
// mainnet's canonical history.
func (s *BlockStore) UnmarkOrphaned(height int64) error {
	_, err := s.db.Exec(
		`UPDATE blocks SET orphaned_at = 0 WHERE height = ?`,
		height,
	)
	return err
}

// UnmarkOrphanedStampChain clears orphaned_at and simultaneously sets the
// chain field. Used for legacy rows (chain == "") that were falsely orphaned
// during a network switch: by stamping a non-mainnet chain value we prevent
// the reconcile reorg-detection pass from re-checking the block against the
// current chain's canonical hashes.
func (s *BlockStore) UnmarkOrphanedStampChain(height int64, chain string) error {
	_, err := s.db.Exec(
		`UPDATE blocks SET orphaned_at = 0, chain = ? WHERE height = ?`,
		chain, height,
	)
	return err
}

// StampChain sets the chain field on a block without touching orphaned_at.
// Used when the reconcile loop determines a block belongs to a different
// network, the block is not orphaned (a reorg), it just doesn't belong
// to the current chain.
func (s *BlockStore) StampChain(height int64, chain string) error {
	_, err := s.db.Exec(
		`UPDATE blocks SET chain = ? WHERE height = ?`,
		chain, height,
	)
	return err
}
