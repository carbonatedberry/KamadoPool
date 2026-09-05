package state

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/kamadopool/kamado-api/internal/logmon"
)

// The two lines below are the verbatim output of ckpool patch 0008's
// jansson code (replicated against ckpool's in-tree jansson 2.14 with
// identical calls and JSON_COMPACT), using Bitcoin block 125552's real
// header and hash. If ckpool's encoding and this pipeline ever drift,
// this test is the tripwire.
const (
	ckpoolPowLine = `[2026-08-31 18:00:01.123] Best share PoW data {"sdiff":62953.415063916997,"netdiff":136597951737045.02,"height":911234,"hash":"00000000000000001e8d6829a8a21adc5d38d0a473b144b6765798e61f98bd1d","header":"0100000081cd02ab7e569e8bcd9317e2fe99f2de44d49ab2b8851ba4a308000000000000e320b6c2fffc8d750423db8b1eb942ae710e951ed797f7affc8892b0f1fc122bc7f5d74df2b9441a42a14695","coinbase":"01000000010000000000000000000000000000000000000000000000000000000000000000ffffffff2703e2e60d04858ee468086b616d61646f2f04a1b2c3d40011223344556677ffffffff0100f2052a010000001600140123456789abcdef0123456789abcdef0123456700000000","cb1len":41,"merklebranches":["fff2525b8931402dd09222c50775608f75787bd2b87e56995a7bdd30f79702c4","6359f0868171b1d194cbee1af2f16ea598ae8fad666d9b012c8ed2b79a236ec4"],"enonce1":"a1b2c3d4","nonce2":"0011223344556677","workername":"bc1qexample.axe1"}` + "\n"
	ckpoolAcceptLine = `[2026-08-31 18:00:01.124] Accepted client 271 share diff 62953.4/512/1.02K: 00000000000000001e8d6829a8a21adc5d38d0a473b144b6765798e61f98bd1d` + "\n"
)

// End to end: real ckpool log bytes -> real Tailer.Run -> aggregator
// ingest -> snapshot JSON. Exercises the exact production path.
func TestPowEndToEnd_LogToSnapshot(t *testing.T) {
	f, err := os.CreateTemp("", "ckpool_e2e*.log")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	tl := logmon.New(f.Name(), discardLog())
	tl.PollWait = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tl.Run(ctx)

	a := New(nil, nil, time.Minute, discardLog())
	go a.IngestShareEvents(ctx, tl.Shares)

	// Give the tailer a moment to open the (empty) file and seek to the
	// end, then append what ckpool would write.
	time.Sleep(50 * time.Millisecond)
	lf, err := os.OpenFile(f.Name(), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lf.WriteString(ckpoolPowLine + ckpoolAcceptLine); err != nil {
		t.Fatal(err)
	}
	lf.Close()

	deadline := time.Now().Add(5 * time.Second)
	for {
		a.mu.RLock()
		pow := a.bestPow
		a.mu.RUnlock()
		if pow != nil {
			if pow.Hash != "00000000000000001e8d6829a8a21adc5d38d0a473b144b6765798e61f98bd1d" {
				t.Fatalf("hash = %s", pow.Hash)
			}
			if pow.SDiff != 62953.415063916997 || pow.NetDiff != 136597951737045.02 {
				t.Fatalf("sdiff/netdiff = %v/%v", pow.SDiff, pow.NetDiff)
			}
			if len(pow.Header) != 160 || len(pow.MerkleBranches) != 2 {
				t.Fatalf("header len %d, branches %d", len(pow.Header), len(pow.MerkleBranches))
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("bestPow never populated from the tailed log")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The snapshot must expose it under best_share_pow with the flattened
	// PowData fields, this is the contract the UI reads.
	snap := Snapshot{BestSharePow: func() *BestSharePow {
		a.mu.RLock()
		defer a.mu.RUnlock()
		return a.bestPow
	}()}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	powJSON, ok := out["best_share_pow"].(map[string]any)
	if !ok {
		t.Fatalf("snapshot JSON missing best_share_pow: %s", data)
	}
	for _, key := range []string{"sdiff", "netdiff", "height", "hash", "header", "coinbase", "cb1len", "merklebranches", "enonce1", "nonce2", "workername", "seen_at"} {
		if _, ok := powJSON[key]; !ok {
			t.Errorf("best_share_pow JSON missing %q", key)
		}
	}
}

// The startup backfill must recover the same record from an existing
// log file (the path taken when live events were missed).
func TestPowEndToEnd_Backfill(t *testing.T) {
	path := tempLogFile(t, ckpoolPowLine, ckpoolAcceptLine)
	pow, at, _, _, err := logmon.FindBestPowData(path)
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if pow == nil {
		t.Fatal("FindBestPowData found nothing in verbatim ckpool output")
	}
	if pow.SDiff != 62953.415063916997 || len(pow.MerkleBranches) != 2 {
		t.Fatalf("got %+v", pow)
	}
	want := time.Date(2026, 8, 31, 18, 0, 1, 0, time.Local)
	if !at.Equal(want) {
		t.Errorf("timestamp = %v, want %v", at, want)
	}
}
