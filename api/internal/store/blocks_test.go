package store

import (
	"database/sql"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTemp(t *testing.T) *BlockStore {
	t.Helper()
	f, err := os.CreateTemp("", "kamado*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	s, err := Open(f.Name())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestInsertBlock_Roundtrip(t *testing.T) {
	s := openTemp(t)
	now := time.Now().UTC().Truncate(time.Second)

	b := Block{
		Height:   840000,
		Hash:     "000000000000000000024bead8df69990852c202db0e0097c1a12ea637d7e96d",
		RewardBT: 3.125,
		FoundAt:  now,
		Source:   "logmon",
		ShareDiff: 8_000_000,
		Chain:    "main",
	}
	inserted, err := s.InsertBlock(b)
	if err != nil {
		t.Fatalf("InsertBlock: %v", err)
	}
	if !inserted {
		t.Fatal("expected inserted=true for a new block")
	}

	blocks, err := s.Recent(10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("Recent returned %d blocks, want 1", len(blocks))
	}
	got := blocks[0]
	if got.Height != b.Height {
		t.Errorf("Height = %d, want %d", got.Height, b.Height)
	}
	if got.Hash != b.Hash {
		t.Errorf("Hash = %q, want %q", got.Hash, b.Hash)
	}
	if got.RewardBT != b.RewardBT {
		t.Errorf("RewardBT = %v, want %v", got.RewardBT, b.RewardBT)
	}
	if !got.FoundAt.Equal(b.FoundAt) {
		t.Errorf("FoundAt = %v, want %v", got.FoundAt, b.FoundAt)
	}
	if got.Chain != "main" {
		t.Errorf("Chain = %q, want \"main\"", got.Chain)
	}
	if !got.OrphanedAt.IsZero() {
		t.Errorf("OrphanedAt should be zero, got %v", got.OrphanedAt)
	}
}

func TestInsertBlock_Dedup(t *testing.T) {
	s := openTemp(t)
	b := Block{Height: 100, FoundAt: time.Now(), Chain: "main"}

	inserted1, err := s.InsertBlock(b)
	if err != nil || !inserted1 {
		t.Fatalf("first insert: err=%v inserted=%v", err, inserted1)
	}

	inserted2, err := s.InsertBlock(b)
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if inserted2 {
		t.Error("second insert at same height should return inserted=false")
	}

	blocks, _ := s.Recent(10)
	if len(blocks) != 1 {
		t.Errorf("store has %d blocks after dedup, want 1", len(blocks))
	}
}

func TestMarkOrphaned_RoundTrip(t *testing.T) {
	s := openTemp(t)
	b := Block{Height: 200, Hash: "abc123", FoundAt: time.Now(), Chain: "main"}
	s.InsertBlock(b)

	orphanTime := time.Now().UTC().Truncate(time.Second)
	if err := s.MarkOrphaned(200, orphanTime); err != nil {
		t.Fatalf("MarkOrphaned: %v", err)
	}

	blocks, _ := s.Recent(10)
	if len(blocks) == 0 {
		t.Fatal("no blocks after MarkOrphaned")
	}
	if blocks[0].OrphanedAt.IsZero() {
		t.Error("OrphanedAt should be set after MarkOrphaned")
	}
	if !blocks[0].OrphanedAt.Equal(orphanTime) {
		t.Errorf("OrphanedAt = %v, want %v", blocks[0].OrphanedAt, orphanTime)
	}
}

func TestUpdateEnrichment(t *testing.T) {
	s := openTemp(t)
	s.InsertBlock(Block{Height: 300, FoundAt: time.Now(), Chain: "main"})

	if err := s.UpdateEnrichment(300, "newhash", 3.125, "bc1qtest.rig1"); err != nil {
		t.Fatalf("UpdateEnrichment: %v", err)
	}

	blocks, _ := s.Recent(10)
	if blocks[0].Hash != "newhash" {
		t.Errorf("Hash = %q, want newhash", blocks[0].Hash)
	}
	if blocks[0].RewardBT != 3.125 {
		t.Errorf("RewardBT = %v, want 3.125", blocks[0].RewardBT)
	}
	if blocks[0].Miner != "bc1qtest.rig1" {
		t.Errorf("Miner = %q, want bc1qtest.rig1", blocks[0].Miner)
	}
}

func TestBlocksNeedingEnrichment(t *testing.T) {
	s := openTemp(t)
	now := time.Now()

	// Block missing hash → needs enrichment.
	s.InsertBlock(Block{Height: 1, FoundAt: now, Chain: "main"})
	// Block with hash but no reward → needs enrichment.
	s.InsertBlock(Block{Height: 2, Hash: "abc", RewardBT: 0, FoundAt: now, Chain: "main"})
	// Block fully enriched → does NOT need enrichment.
	s.InsertBlock(Block{Height: 3, Hash: "def", RewardBT: 3.125, FoundAt: now, Chain: "main", Miner: "bc1q.rig1"})

	since := now.Add(-time.Hour)
	missing, err := s.BlocksNeedingEnrichment(since)
	if err != nil {
		t.Fatalf("BlocksNeedingEnrichment: %v", err)
	}
	if len(missing) != 2 {
		t.Errorf("got %d blocks needing enrichment, want 2", len(missing))
	}
	for _, b := range missing {
		if b.Height == 3 {
			t.Error("fully enriched block should not appear in missing list")
		}
	}
}

// TestMigrate_AddsChainColumn creates a database with the old schema
// (no chain column) and verifies Open() migrates it transparently.
func TestMigrate_AddsChainColumn(t *testing.T) {
	f, err := os.CreateTemp("", "kamado_old*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	// Build old-style schema without the chain column.
	db, err := sql.Open("sqlite", f.Name())
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE blocks (
		height       INTEGER PRIMARY KEY,
		hash         TEXT NOT NULL DEFAULT '',
		reward_btc   REAL NOT NULL DEFAULT 0,
		found_at     INTEGER NOT NULL,
		source       TEXT NOT NULL DEFAULT '',
		share_diff   REAL NOT NULL DEFAULT 0,
		orphaned_at  INTEGER NOT NULL DEFAULT 0
	)`)
	if err != nil {
		db.Close()
		t.Fatalf("create old schema: %v", err)
	}
	_, err = db.Exec(`INSERT INTO blocks(height, found_at) VALUES (999, ?)`, time.Now().Unix())
	if err != nil {
		db.Close()
		t.Fatalf("insert old row: %v", err)
	}
	db.Close()

	// Open via our store, migrate() must add the chain column.
	s, err := Open(f.Name())
	if err != nil {
		t.Fatalf("Open after migration: %v", err)
	}
	defer s.Close()

	// Insert a block with a chain value, would fail if migration didn't add
	// the column.
	inserted, err := s.InsertBlock(Block{
		Height:  1000,
		FoundAt: time.Now(),
		Chain:   "test",
	})
	if err != nil {
		t.Fatalf("InsertBlock after migration: %v", err)
	}
	if !inserted {
		t.Error("expected inserted=true")
	}

	blocks, err := s.Recent(10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	// Row 999 (old, no chain) and row 1000 (new, chain="test").
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(blocks))
	}
	// Newest first; row 1000 is first.
	if blocks[0].Chain != "test" {
		t.Errorf("new block chain = %q, want test", blocks[0].Chain)
	}
	if blocks[1].Chain != "" {
		t.Errorf("old block chain = %q, want \"\" (empty default)", blocks[1].Chain)
	}
}

func TestKVRoundTrip(t *testing.T) {
	s := openTemp(t)

	if err := s.SetKV("foo", "bar"); err != nil {
		t.Fatalf("SetKV: %v", err)
	}
	v, err := s.GetKV("foo")
	if err != nil {
		t.Fatalf("GetKV: %v", err)
	}
	if v != "bar" {
		t.Errorf("GetKV = %q, want bar", v)
	}

	// Overwrite.
	if err := s.SetKV("foo", "baz"); err != nil {
		t.Fatalf("SetKV overwrite: %v", err)
	}
	v2, _ := s.GetKV("foo")
	if v2 != "baz" {
		t.Errorf("after overwrite GetKV = %q, want baz", v2)
	}

	// Missing key returns empty string.
	v3, err := s.GetKV("nonexistent")
	if err != nil {
		t.Fatalf("GetKV missing: %v", err)
	}
	if v3 != "" {
		t.Errorf("missing key GetKV = %q, want empty", v3)
	}
}
