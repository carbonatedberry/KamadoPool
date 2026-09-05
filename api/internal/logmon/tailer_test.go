package logmon

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ---- handleLine unit tests (no filesystem, no goroutines) ----

func TestHandleLine_Solved(t *testing.T) {
	tl := New("/unused", discardLog())
	tl.handleLine("Solved and confirmed block 840123\n")
	select {
	case ev := <-tl.Events:
		if ev.Height != 840123 {
			t.Errorf("Height = %d, want 840123", ev.Height)
		}
	default:
		t.Fatal("no BlockEvent emitted for solved line")
	}
}

func TestHandleLine_AttemptCarriedToBlock(t *testing.T) {
	tl := New("/unused", discardLog())
	tl.handleLine("Possible block solve diff 32768 !\n")
	tl.handleLine("Solved and confirmed block 900000\n")

	select {
	case <-tl.Attempts:
	default:
		t.Fatal("no AttemptEvent emitted for attempt line")
	}
	select {
	case ev := <-tl.Events:
		if ev.ShareDiff != 32768 {
			t.Errorf("ShareDiff = %v, want 32768", ev.ShareDiff)
		}
		if ev.Height != 900000 {
			t.Errorf("Height = %d, want 900000", ev.Height)
		}
	default:
		t.Fatal("no BlockEvent emitted")
	}
}

func TestHandleLine_SubmittingVariant(t *testing.T) {
	tl := New("/unused", discardLog())
	tl.handleLine("Submitting possible block solve share diff 65536 !\n")
	select {
	case ev := <-tl.Attempts:
		if ev.ShareDiff != 65536 {
			t.Errorf("ShareDiff = %v, want 65536", ev.ShareDiff)
		}
	default:
		t.Fatal("no AttemptEvent for submitting variant")
	}
	// Must NOT also emit a BlockEvent.
	select {
	case ev := <-tl.Events:
		t.Errorf("unexpected BlockEvent from attempt line: %+v", ev)
	default:
	}
}

func TestHandleLine_DiffResetAfterBlock(t *testing.T) {
	tl := New("/unused", discardLog())
	tl.handleLine("Possible block solve diff 16384 !\n")
	tl.handleLine("Solved and confirmed block 100\n")
	// Second block, no preceding attempt line; diff must NOT carry over.
	tl.handleLine("Solved and confirmed block 101\n")

	drain := func() BlockEvent {
		select {
		case ev := <-tl.Events:
			return ev
		default:
			t.Fatal("no BlockEvent")
			return BlockEvent{}
		}
	}
	ev100 := drain()
	ev101 := drain()
	if ev100.ShareDiff != 16384 {
		t.Errorf("block 100 ShareDiff = %v, want 16384", ev100.ShareDiff)
	}
	if ev101.ShareDiff != 0 {
		t.Errorf("block 101 ShareDiff = %v, want 0 (no preceding attempt)", ev101.ShareDiff)
	}
}

func TestHandleLine_Unrelated(t *testing.T) {
	tl := New("/unused", discardLog())
	for _, line := range []string{
		"Stratifier started\n",
		"New block hash 000000abc\n",
		"Worker authenticated\n",
	} {
		tl.handleLine(line)
	}
	select {
	case ev := <-tl.Events:
		t.Errorf("unexpected BlockEvent from unrelated line: %+v", ev)
	default:
	}
	select {
	case ev := <-tl.Attempts:
		t.Errorf("unexpected AttemptEvent from unrelated line: %+v", ev)
	default:
	}
}

func TestHandleLine_PowData(t *testing.T) {
	tl := New("/unused", discardLog())
	line := `[2026-08-31 10:11:12.123] Best share PoW data {"sdiff":123456789.5,"netdiff":136597951737045.0,"height":911111,` +
		`"hash":"0000000000abcdef0000000000000000000000000000000000000000deadbeef",` +
		`"header":"20000000" + "aa",` + "\n"
	// Malformed JSON must not emit an event.
	tl.handleLine(line)
	select {
	case ev := <-tl.Shares:
		t.Errorf("unexpected ShareEvent from malformed PoW line: %+v", ev)
	default:
	}

	good := `[2026-08-31 10:11:12.123] Best share PoW data {"sdiff":123456789.5,"netdiff":136597951737045.0,"height":911111,` +
		`"hash":"0000000000abcdef0000000000000000000000000000000000000000deadbeef",` +
		`"header":"0000002006226e46111a0b59caaf126043eb5bbf28c34f3a5e332a1fc7b2b73cf188910f00000000000000000000000000000000000000000000000000000000000000006859a06817e0377c00000000",` +
		`"coinbase":"01000000010000000000000000000000000000000000000000000000000000000000000000ffffffff00ffffffff0100f2052a01000000160014000000000000000000000000000000000000000000000000",` +
		`"cb1len":41,"merklebranches":["ab"],"enonce1":"a1b2c3d4","nonce2":"0011223344556677","workername":"bc1qexample.axe"}` + "\n"
	tl.handleLine(good)
	select {
	case ev := <-tl.Shares:
		if ev.Pow == nil {
			t.Fatalf("ShareEvent.Pow = nil, want parsed PoW data (event %+v)", ev)
		}
		if ev.Pow.SDiff != 123456789.5 {
			t.Errorf("SDiff = %v, want 123456789.5", ev.Pow.SDiff)
		}
		if ev.Pow.Height != 911111 {
			t.Errorf("Height = %d, want 911111", ev.Pow.Height)
		}
		if len(ev.Pow.Header) != 160 {
			t.Errorf("Header length = %d, want 160", len(ev.Pow.Header))
		}
		if ev.Pow.CB1Len != 41 {
			t.Errorf("CB1Len = %d, want 41", ev.Pow.CB1Len)
		}
		if len(ev.Pow.MerkleBranches) != 1 || ev.Pow.MerkleBranches[0] != "ab" {
			t.Errorf("MerkleBranches = %v, want [ab]", ev.Pow.MerkleBranches)
		}
		if ev.Pow.Workername != "bc1qexample.axe" {
			t.Errorf("Workername = %q", ev.Pow.Workername)
		}
	default:
		t.Fatal("no ShareEvent emitted for PoW data line")
	}
}

func powLine(hash string, sdiff float64) string {
	return `[2026-08-30 09:15:42.123] Best share PoW data {"sdiff":` +
		strconv.FormatFloat(sdiff, 'f', -1, 64) +
		`,"netdiff":1.3e14,"height":911111,"hash":"` + hash +
		`","header":"00000020aabb","coinbase":"0100","cb1len":41,` +
		`"merklebranches":[],"enonce1":"a1b2c3d4","nonce2":"0011223344556677","workername":"bc1q.axe"}` + "\n"
}

func acceptedLine(hash string, sdiff float64) string {
	return `[2026-08-30 09:15:42.124] Accepted client 7 share diff ` +
		strconv.FormatFloat(sdiff, 'f', -1, 64) + `/42/1.0K: ` + hash + "\n"
}

// FindBestPowData must return the highest-diff record that was
// confirmed accepted, skip unconfirmed ones, and recover the log
// line's timestamp.
func TestFindBestPowData(t *testing.T) {
	path := tempLog(t)
	appendLines(t, path,
		powLine("aaaa", 100),
		acceptedLine("aaaa", 100),
		// Never confirmed (e.g. rejected below vardiff): must be ignored
		// even though it carries the highest diff.
		powLine("bbbb", 99999),
		powLine("cccc", 500),
		acceptedLine("cccc", 500),
	)

	pow, at, seen, unparseable, err := FindBestPowData(path)
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if pow == nil {
		t.Fatal("FindBestPowData returned nil, want the confirmed 500-diff record")
	}
	if pow.Hash != "cccc" || pow.SDiff != 500 {
		t.Errorf("got hash %s sdiff %v, want cccc/500", pow.Hash, pow.SDiff)
	}
	if seen != 3 || unparseable != 0 {
		t.Errorf("seen=%d unparseable=%d, want 3/0", seen, unparseable)
	}
	want := time.Date(2026, 8, 30, 9, 15, 42, 0, time.Local)
	if !at.Equal(want) {
		t.Errorf("timestamp = %v, want %v", at, want)
	}
}

// Production ckpool logs have carried bytes after the JSON's closing
// brace (observed in the field: an end-of-line-anchored regex matched 0
// of 54 real PoW lines while synthetic lines passed). The parser must
// decode the JSON value and ignore ANY trailing bytes, NULs, \r,
// or an interleaved second message on the same line.
func TestHandleLine_PowDataTrailingGarbage(t *testing.T) {
	suffixes := []string{
		"\x00\x00\n",  // NUL padding
		"\r\n",        // CR before LF
		" [2026-08-31 18:00:01.125] Accepted client 3 share diff 1.0/1/1: aa\n", // interleaved write
	}
	for _, suffix := range suffixes {
		tl := New("/unused", discardLog())
		tl.handleLine(strings.TrimRight(powLine("cafe", 777), "\n") + suffix)
		select {
		case ev := <-tl.Shares:
			if ev.Pow == nil || ev.Pow.Hash != "cafe" || ev.Pow.SDiff != 777 {
				t.Errorf("suffix %q: got %+v, want cafe/777 PoW event", suffix, ev.Pow)
			}
		default:
			t.Errorf("suffix %q: no PoW event emitted", suffix)
		}
	}
}

// A pathologically long line earlier in the log must not abort the scan
// before it reaches the PoW records, bufio.Scanner did exactly that,
// silently, which left the backfill permanently empty on real logs.
func TestFindBestPowData_SurvivesOversizedLine(t *testing.T) {
	path := tempLog(t)
	appendLines(t, path,
		"junk "+strings.Repeat("x", 600*1024)+"\n", // > any fixed buffer
		powLine("abab", 250),
		acceptedLine("abab", 250),
	)
	pow, _, seen, _, err := FindBestPowData(path)
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if pow == nil || pow.Hash != "abab" || seen != 1 {
		t.Fatalf("pow=%+v seen=%d, want abab record found past the oversized line", pow, seen)
	}
}

func TestFindBestPowData_Empty(t *testing.T) {
	path := tempLog(t)
	appendLines(t, path,
		"Stratifier started\n",
		acceptedLine("dddd", 10), // accepted share without PoW data (pre-patch line)
	)
	if pow, _, _, _, err := FindBestPowData(path); pow != nil || err != nil {
		t.Errorf("got %+v err=%v, want nil/nil for log without confirmed PoW records", pow, err)
	}
	if pow, _, _, _, err := FindBestPowData(path + ".missing"); pow != nil || err == nil {
		t.Errorf("got %+v err=%v, want nil record and an error for missing file", pow, err)
	}
}

// ---- Tailer.Run integration tests (filesystem + goroutines) ----

func tempLog(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "ckpool*.log")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	return f.Name()
}

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l); err != nil {
			t.Fatal(err)
		}
	}
}

// TestTailerRun_BasicRead starts the tailer on an empty file, appends log
// lines, and verifies the expected events are emitted.
func TestTailerRun_BasicRead(t *testing.T) {
	path := tempLog(t)
	tl := New(path, discardLog())
	tl.PollWait = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go tl.Run(ctx)

	// Give the tailer time to open the empty file and park at EOF.
	time.Sleep(40 * time.Millisecond)

	appendLines(t, path,
		"Possible block solve diff 16384 !\n",
		"Solved and confirmed block 100\n",
	)

	select {
	case <-tl.Attempts:
	case <-ctx.Done():
		t.Fatal("timed out waiting for AttemptEvent")
	}
	select {
	case ev := <-tl.Events:
		if ev.Height != 100 {
			t.Errorf("Height = %d, want 100", ev.Height)
		}
		if ev.ShareDiff != 16384 {
			t.Errorf("ShareDiff = %v, want 16384", ev.ShareDiff)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for BlockEvent")
	}
}

// TestTailerRun_CursorResume verifies that a new tailer honours a saved
// cursor and does not re-emit lines that were already processed.
func TestTailerRun_CursorResume(t *testing.T) {
	path := tempLog(t)

	var mu sync.Mutex
	var savedIno uint64
	var savedOff int64

	tl := New(path, discardLog())
	tl.PollWait = 20 * time.Millisecond
	tl.SaveCursor = func(ino uint64, off int64) {
		mu.Lock()
		savedIno, savedOff = ino, off
		mu.Unlock()
	}

	ctx1, cancel1 := context.WithTimeout(context.Background(), 3*time.Second)
	go tl.Run(ctx1)
	time.Sleep(40 * time.Millisecond)

	// Phase 1: write 2 blocks and drain both events.
	appendLines(t, path,
		"Solved and confirmed block 200\n",
		"Solved and confirmed block 201\n",
	)
	for i := 0; i < 2; i++ {
		select {
		case <-tl.Events:
		case <-ctx1.Done():
			t.Fatalf("phase 1: only got %d events, want 2", i)
		}
	}
	// Allow the tailer to hit EOF and save the cursor.
	time.Sleep(100 * time.Millisecond)
	cancel1()
	// Give the goroutine time to exit and force-save.
	time.Sleep(60 * time.Millisecond)

	mu.Lock()
	ino, off := savedIno, savedOff
	mu.Unlock()
	if ino == 0 {
		t.Fatal("cursor was never saved after phase 1")
	}

	// Phase 2: append a third block, start a new tailer from the cursor.
	// It must see ONLY block 202, not replays of 200 or 201.
	appendLines(t, path, "Solved and confirmed block 202\n")

	tl2 := New(path, discardLog())
	tl2.PollWait = 20 * time.Millisecond
	tl2.LoadCursor = func() (uint64, int64, bool) { return ino, off, true }

	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	go tl2.Run(ctx2)

	select {
	case ev := <-tl2.Events:
		if ev.Height != 202 {
			t.Errorf("resumed tailer emitted height %d, want 202 (cursor resume may be broken)", ev.Height)
		}
	case <-ctx2.Done():
		t.Fatal("timed out waiting for block 202 after cursor resume")
	}
}

// TestTailerRun_Rotation verifies that when the log file is replaced
// (inode changes), the tailer re-opens it from the start and reads the
// new content.
func TestTailerRun_Rotation(t *testing.T) {
	path := tempLog(t)
	tl := New(path, discardLog())
	tl.PollWait = 30 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go tl.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	// Write a block to the original file and collect it.
	appendLines(t, path, "Solved and confirmed block 300\n")
	select {
	case ev := <-tl.Events:
		if ev.Height != 300 {
			t.Errorf("before rotation: height = %d, want 300", ev.Height)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for pre-rotation block")
	}

	// Simulate log rotation: replace the file at the same path.
	newF, err := os.CreateTemp("", "ckpool_new*.log")
	if err != nil {
		t.Fatal(err)
	}
	newF.WriteString("Solved and confirmed block 301\n")
	newF.Close()
	if err := os.Rename(newF.Name(), path); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-tl.Events:
		if ev.Height != 301 {
			t.Errorf("after rotation: height = %d, want 301", ev.Height)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for post-rotation block")
	}
}
