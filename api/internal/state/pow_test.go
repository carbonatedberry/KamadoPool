package state

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/kamadopool/kamado-api/internal/logmon"
)

// ingest runs IngestShareEvents over the given events and waits for the
// goroutine to drain them.
func ingest(t *testing.T, a *Aggregator, events ...logmon.ShareEvent) {
	t.Helper()
	ch := make(chan logmon.ShareEvent, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)
	done := make(chan struct{})
	go func() {
		a.IngestShareEvents(context.Background(), ch)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("IngestShareEvents did not drain")
	}
}

func powFixture(hash string, sdiff float64) *logmon.PowData {
	return &logmon.PowData{
		SDiff:          sdiff,
		NetDiff:        1.36e14,
		Height:         911111,
		Hash:           hash,
		Header:         "00000020" + "aa" + "bb", // content is opaque to the aggregator
		Coinbase:       "0100",
		CB1Len:         41,
		MerkleBranches: []string{"cd"},
		Enonce1:        "a1b2c3d4",
		Nonce2:         "0011223344556677",
		Workername:     "bc1qexample.axe",
	}
}

// A PoW data line followed by its matching accepted share must promote
// the parked record to bestPow and persist it.
func TestPowPairing_AcceptedPromotes(t *testing.T) {
	s := openTempStore(t)
	a := New(nil, nil, time.Minute, discardLog())
	a.Store = s

	const hash = "0000000000abcdef0000000000000000000000000000000000000000deadbeef"
	ingest(t, a,
		logmon.ShareEvent{SeenAt: time.Now(), Pow: powFixture(hash, 5000)},
		logmon.ShareEvent{SeenAt: time.Now(), Diff: 5000, Hash: hash},
	)

	a.mu.RLock()
	pow := a.bestPow
	a.mu.RUnlock()
	if pow == nil {
		t.Fatal("bestPow not set after accepted share matched PoW data")
	}
	if pow.Hash != hash || pow.SDiff != 5000 {
		t.Errorf("bestPow = %+v, want hash %s sdiff 5000", pow.PowData, hash)
	}

	// Reload from the store to verify persistence round-trips.
	b := New(nil, nil, time.Minute, discardLog())
	b.Store = s
	b.loadPersistedState()
	b.mu.RLock()
	reloaded := b.bestPow
	b.mu.RUnlock()
	if reloaded == nil || reloaded.Hash != hash || reloaded.CB1Len != 41 {
		t.Fatalf("persisted bestPow did not reload, got %+v", reloaded)
	}
}

// PoW data for a share that is never confirmed accepted (e.g. rejected
// below vardiff right after a ckpool restart) must not become bestPow.
func TestPowPairing_UnmatchedIsNotPromoted(t *testing.T) {
	a := New(nil, nil, time.Minute, discardLog())

	ingest(t, a,
		logmon.ShareEvent{SeenAt: time.Now(), Pow: powFixture("aa11", 9000)},
		logmon.ShareEvent{SeenAt: time.Now(), Rejected: true, Reason: "Above target"},
	)

	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.bestPow != nil {
		t.Errorf("bestPow = %+v, want nil for unconfirmed PoW data", a.bestPow)
	}
	if _, ok := a.pendingPow["aa11"]; !ok {
		t.Error("pending PoW entry missing; expected it parked until matched or evicted")
	}
}

// The all-time best share may hugely outrank anything recent (found
// before header capture existed). The PoW record must still populate
// from the first confirmed share, it tracks "best since the feature
// went live", never waiting for the all-time best to be beaten, while
// leaving the all-time bestDiff/bestShareHash untouched.
func TestPowPairing_BelowAllTimeBestStillPopulates(t *testing.T) {
	a := New(nil, nil, time.Minute, discardLog())
	a.mu.Lock()
	a.bestDiff = 4.2e11 // legacy all-time best, no PoW data recorded for it
	a.bestShareHash = "legacyhash"
	a.mu.Unlock()

	ingest(t, a,
		logmon.ShareEvent{SeenAt: time.Now(), Pow: powFixture("smallhash", 1234)},
		logmon.ShareEvent{SeenAt: time.Now(), Diff: 1234, Hash: "smallhash"},
	)

	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.bestPow == nil || a.bestPow.Hash != "smallhash" {
		t.Fatalf("bestPow = %+v, want the 1234-diff record despite the 4.2e11 all-time best", a.bestPow)
	}
	if a.bestDiff != 4.2e11 || a.bestShareHash != "legacyhash" {
		t.Errorf("all-time best mutated: diff %v hash %q", a.bestDiff, a.bestShareHash)
	}
}

// With no persisted PoW record, loadPersistedState must adopt the best
// confirmed record already present in the ckpool log, so the inspector
// never waits for ckpool's long-lived in-process best to be beaten.
func TestPowBackfill_FromLog(t *testing.T) {
	logPath := tempLogFile(t,
		`[2026-08-30 09:15:42.123] Best share PoW data {"sdiff":777,"netdiff":1.3e14,"height":911111,"hash":"eeee","header":"00000020aabb","coinbase":"0100","cb1len":41,"merklebranches":[],"enonce1":"a1","nonce2":"b2","workername":"w"}`+"\n",
		`[2026-08-30 09:15:42.124] Accepted client 7 share diff 777/42/1.0K: eeee`+"\n",
	)
	s := openTempStore(t)
	a := New(nil, nil, time.Minute, discardLog())
	a.Store = s
	a.LogFilePath = logPath
	a.loadPersistedState()

	a.mu.RLock()
	pow := a.bestPow
	a.mu.RUnlock()
	if pow == nil || pow.Hash != "eeee" || pow.SDiff != 777 {
		t.Fatalf("bestPow = %+v, want backfilled eeee/777", pow)
	}
	if pow.SeenAt.IsZero() {
		t.Error("SeenAt is zero, want the log line timestamp")
	}

	// A persisted record must suppress the backfill on the next load,
	// even when the log holds a lower-diff record.
	b := New(nil, nil, time.Minute, discardLog())
	b.Store = s
	b.LogFilePath = tempLogFile(t,
		`[2026-08-31 10:00:00.000] Best share PoW data {"sdiff":5,"netdiff":1.3e14,"height":911112,"hash":"ffff","header":"00000020ccdd","coinbase":"0100","cb1len":41,"merklebranches":[],"enonce1":"a1","nonce2":"b2","workername":"w"}`+"\n",
		`[2026-08-31 10:00:00.001] Accepted client 7 share diff 5/42/1.0K: ffff`+"\n",
	)
	b.loadPersistedState()
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.bestPow == nil || b.bestPow.Hash != "eeee" {
		t.Fatalf("bestPow = %+v, want the persisted eeee record kept", b.bestPow)
	}
}

func tempLogFile(t *testing.T, lines ...string) string {
	t.Helper()
	f, err := os.CreateTemp("", "ckpool_pow*.log")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if _, werr := f.WriteString(l); werr != nil {
			t.Fatal(werr)
		}
	}
	f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	return f.Name()
}

// A lower-diff PoW record (ckpool restarts reset its in-process best,
// so it re-logs on the first share of each run) must not replace a
// higher one already held.
func TestPowPairing_LowerDiffDoesNotReplace(t *testing.T) {
	a := New(nil, nil, time.Minute, discardLog())

	ingest(t, a,
		logmon.ShareEvent{SeenAt: time.Now(), Pow: powFixture("high", 9000)},
		logmon.ShareEvent{SeenAt: time.Now(), Diff: 9000, Hash: "high"},
		// ckpool restarted: first share of the new process is its
		// in-process best and gets logged again at a lower diff.
		logmon.ShareEvent{SeenAt: time.Now(), Pow: powFixture("low", 42)},
		logmon.ShareEvent{SeenAt: time.Now(), Diff: 42, Hash: "low"},
	)

	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.bestPow == nil || a.bestPow.Hash != "high" {
		t.Fatalf("bestPow = %+v, want the 9000-diff record kept", a.bestPow)
	}
}

// Reproduces the startup race that destroyed accumulated share
// statistics: the ingest goroutine runs concurrently with the restore,
// and its first event triggers a persist. If that persist is allowed to
// run before the counters are loaded, it writes the zero-valued
// in-memory histogram over the stored history, which the restore then
// reads back, making the loss permanent.
func TestShareStats_NotPersistedBeforeLoad(t *testing.T) {
	s := openTempStore(t)
	// Seed the store as a long-running pool would: 4M accepted shares.
	const seeded = `{"b":[4000000,0,0,0,12,3],"n":4000015,"s":4.2e10}`
	if err := s.SetKV(kvDiffDistAll, seeded); err != nil {
		t.Fatal(err)
	}

	// An aggregator that has NOT loaded yet, as during startup.
	a := New(nil, nil, time.Minute, discardLog())
	a.Store = s
	ingest(t, a, logmon.ShareEvent{SeenAt: time.Now(), Diff: 1234, Hash: "aa"})

	v, err := s.GetKV(kvDiffDistAll)
	if err != nil {
		t.Fatal(err)
	}
	if v != seeded {
		t.Fatalf("history overwritten before load:\n got %s\nwant %s", v, seeded)
	}

	// After loading, the accumulated history is in memory and further
	// shares extend it instead of replacing it.
	a.LoadPersisted()
	a.mu.RLock()
	count := a.alltimeDiffCount
	bucket0 := a.alltimeDiffDist[0]
	a.mu.RUnlock()
	if count != 4000015 || bucket0 != 4000000 {
		t.Fatalf("loaded count=%d bucket0=%d, want 4000015/4000000", count, bucket0)
	}

	ingest(t, a, logmon.ShareEvent{SeenAt: time.Now(), Diff: 1234, Hash: "bb"})
	a.mu.RLock()
	after := a.alltimeDiffCount
	a.mu.RUnlock()
	if after != 4000016 {
		t.Errorf("alltime count = %d after one more share, want 4000016", after)
	}
}

// Recovery path: rebuild the all-time histogram by rescanning the log
// when the stored counters hold fewer shares than the log accounts for.
func TestRebuildShareStatsFromLog(t *testing.T) {
	logPath := tempLogFile(t,
		`[2026-09-02 10:00:00.001] Accepted client 1 share diff 500/42/1.0K: aa`+"\n",
		`[2026-09-02 10:00:00.002] Accepted client 1 share diff 2000000/42/1.0K: bb`+"\n",
		`[2026-09-02 10:00:00.003] Accepted client 1 share diff 500000000000/42/1.0K: cc`+"\n",
		`[2026-09-02 10:00:00.004] Rejected client 1 dupe diff 500/42/1.0K: aa`+"\n",
		`[2026-09-02 10:00:00.005] Rejected client 1 invalid share Stale`+"\n",
	)
	s := openTempStore(t)
	a := New(nil, nil, time.Minute, discardLog())
	a.Store = s
	a.LogFilePath = logPath
	a.LoadPersisted()

	adopted, scanned, err := a.RebuildShareStatsFromLog()
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if !adopted || scanned != 3 {
		t.Fatalf("adopted=%v scanned=%d, want true/3", adopted, scanned)
	}

	a.mu.RLock()
	dist := a.alltimeDiffDist
	count := a.alltimeDiffCount
	reasons := a.alltimeRejectReasons
	a.mu.RUnlock()
	// buckets: <1M, 1M-100M, 100M-1G, 1G-100G, 100G-1T, >=1T
	if dist[0] != 1 || dist[1] != 1 || dist[4] != 1 || count != 3 {
		t.Errorf("dist=%v count=%d, want one share each in buckets 0,1,4", dist, count)
	}
	if reasons["Duplicate"] != 1 || reasons["Stale"] != 1 {
		t.Errorf("reject reasons = %v, want one Duplicate and one Stale", reasons)
	}

	// A rebuild that would shrink the history must be refused.
	a.mu.Lock()
	a.alltimeDiffCount = 4000000
	a.mu.Unlock()
	adopted, _, err = a.RebuildShareStatsFromLog()
	if err != nil || adopted {
		t.Errorf("adopted=%v err=%v, want refusal when the log holds fewer shares", adopted, err)
	}
}
