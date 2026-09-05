// Package logmon tails the ckpool log file looking for notable events,
// primarily block-solve lines, which are the most reliable signal we have
// that a block was found, short of a ZMQ hashblock subscription.
//
// ckpool-solo logs a line like:
//
//	Solved and confirmed block 840123
//
// from stratifier.c via LOGWARNING when a submitted share passes network
// difficulty and bitcoind confirms acceptance. We parse these lines,
// emit BlockEvent values on Events, and let the aggregator enrich them
// with hash/reward via bitcoind RPC.
package logmon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// BlockEvent is emitted when the tailer sees a "Solved and confirmed block"
// line in the ckpool log. Hash and worker are populated later by the
// aggregator once it cross-references bitcoind. ShareDiff is the
// difficulty of the winning share, captured from the "Possible block
// solve" line that precedes the confirmation line.
type BlockEvent struct {
	Height    int64     `json:"height"`
	SeenAt    time.Time `json:"seen_at"`
	RawLine   string    `json:"raw_line"`
	ShareDiff float64   `json:"share_diff,omitempty"`
}

// AttemptEvent is emitted when ckpool logs a "Possible block solve"
// or "Submitting possible block solve" line, i.e. ckpool decided a
// share met network difficulty and is attempting to submit it to
// bitcoind. This precedes the "Solved and confirmed" line that drives
// BlockEvent. Counting attempts vs confirmations surfaces submission
// failures (bitcoind rejected, RPC timeout, etc.) that would otherwise
// be invisible.
type AttemptEvent struct {
	SeenAt    time.Time
	ShareDiff float64
	RawLine   string
}

// LatencyEvent is emitted when ckpool logs the "Block update latency"
// line from our patch 0005, the milliseconds between ZMQ trigger and
// mining.notify broadcast completion.
type LatencyEvent struct {
	SeenAt    time.Time
	LatencyMs int64
}

// ShareEvent is emitted for every individual share submission logged by
// ckpool (both accepted and rejected). Used to build rejection-reason
// breakdowns and accepted-share difficulty distributions.
type ShareEvent struct {
	SeenAt   time.Time
	Diff     float64 // share difficulty (always set for accepted; 0 for rejected)
	Hash     string  // block header hash (hex, accepted shares only)
	Rejected bool
	Reason   string // rejection reason (e.g. "Stale", "Duplicate"); empty for accepted
	// Pow is set instead of the fields above when the line was a
	// "Best share PoW data" record (ckpool patch 0008). It travels on
	// the same channel as accepted/rejected events so ordering with
	// the matching "Accepted client" line is preserved.
	Pow *PowData
}

// PowData is the proof-of-work reproduction record ckpool logs (patch
// 0008) when a share sets a new process-wide best difficulty: the raw
// 80-byte block header exactly as it was double-SHA256'd, the assembled
// coinbase transaction, and the merkle branch hashes linking that
// coinbase to the merkle root committed in the header. Field names
// mirror the JSON emitted by the patch.
type PowData struct {
	SDiff          float64  `json:"sdiff"`
	NetDiff        float64  `json:"netdiff"`
	Height         int64    `json:"height"`
	Hash           string   `json:"hash"`     // display-order (big-endian) block header hash
	Header         string   `json:"header"`   // 160 hex chars, hashing byte order
	Coinbase       string   `json:"coinbase"` // full serialized coinbase tx, hex
	CB1Len         int      `json:"cb1len"`   // bytes preceding extranonce1 in the coinbase
	MerkleBranches []string `json:"merklebranches"`
	Enonce1        string   `json:"enonce1"`
	Nonce2         string   `json:"nonce2"`
	Workername     string   `json:"workername"`
}

// Tailer follows a log file, surviving rotation/truncation, and emits
// parsed events. Create with New, then Run in a goroutine.
type Tailer struct {
	Path      string
	Events    chan BlockEvent
	Attempts  chan AttemptEvent
	Latencies chan LatencyEvent
	Shares    chan ShareEvent
	Log       *slog.Logger
	PollWait  time.Duration // how long to sleep between EOF polls

	// LoadCursor / SaveCursor, if both non-nil, persist the read
	// position across process restarts. LoadCursor returns (inode,
	// offset, true) when a saved cursor exists for this path; the
	// tailer will resume from offset only if the inode still matches.
	// SaveCursor is invoked on a throttled cadence as we read, so a
	// crash loses at most ~1s of unread bytes.
	LoadCursor func() (inode uint64, offset int64, ok bool)
	SaveCursor func(inode uint64, offset int64)

	// lastSolveDiff remembers the share diff from the most recent
	// "Possible block solve" line so handleLine can attach it to the
	// subsequent "Solved and confirmed block" event. Reset after use.
	lastSolveDiff float64

	lastCursorSave time.Time
}

func New(path string, log *slog.Logger) *Tailer {
	return &Tailer{
		Path:      path,
		Events:    make(chan BlockEvent, 16),
		Attempts:  make(chan AttemptEvent, 16),
		Latencies: make(chan LatencyEvent, 16),
		Shares:    make(chan ShareEvent, 64),
		Log:       log,
		PollWait:  500 * time.Millisecond,
	}
}

var (
	solvedRE = regexp.MustCompile(`Solved and confirmed block\s+(\d+)`)
	// Matches the three "Possible ... block solve ... diff <float>" lines
	// ckpool emits from stratifier.c right before a block is submitted:
	//   "Possible block solve diff N !"
	//   "Possible stale share block solve diff N !"
	//   "Submitting possible block solve share diff N !"
	//   "Possible remote block solve diff N !"
	solveDiffRE = regexp.MustCompile(`(?:Possible|Submitting[^"]*possible).*block solve.*diff\s+([0-9eE.+-]+)`)
	// Matches the latency line from our patch 0005:
	//   "Block update latency: 92ms (ZMQ trigger to mining.notify broadcast)"
	latencyRE = regexp.MustCompile(`Block update latency:\s+(\d+)ms`)

	// Share events from ckpool's stratifier.c (v1.0 / cfb0f83b):
	//   "Accepted client 42 share diff 1234.5/65536/1.234G: <hexhash>"
	//   "Rejected client 42 dupe diff 1234.5/65536/1.234G: <hexhash>"
	//   "Rejected client 42 high diff 1234.5/65536/1.234G: <hexhash>"
	//   "Rejected client 42 invalid share Stale"
	// The first number after "diff " is sdiff (share difficulty solved).
	acceptedShareRE = regexp.MustCompile(`Accepted client \S+ share diff ([0-9.]+)/[^:]+:\s*([0-9a-fA-F]+)`)
	// Rejected shares come in two forms:
	//   1. "dupe diff" / "high diff", share was valid but duplicate or below target
	//   2. "invalid share <reason>", share was structurally invalid (Stale, etc.)
	rejectedDiffRE  = regexp.MustCompile(`Rejected client \S+ (\w+) diff [0-9.]+/`)
	rejectedInvalRE = regexp.MustCompile(`Rejected client \S+ invalid share (.+)`)
)

// powDataMarker precedes the JSON payload of a PoW reproduction line
// from our patch 0008, logged just before the matching "Accepted client"
// line when a share sets a new process-wide best:
//
//	"Best share PoW data {"sdiff":...,"header":"...",...}"
const powDataMarker = "Best share PoW data "

// parsePowLine extracts the PoW record from a log line carrying
// powDataMarker. It decodes exactly one JSON value starting at the first
// '{' after the marker and deliberately ignores everything after it,
// production logs have carried trailing bytes after the closing brace,
// which silently defeated an end-of-line-anchored regex. Returns
// (nil, nil) when the marker is absent, and a non-nil error for a
// present-but-undecodable record.
func parsePowLine(line string) (*PowData, error) {
	i := strings.Index(line, powDataMarker)
	if i < 0 {
		return nil, nil
	}
	rest := line[i+len(powDataMarker):]
	j := strings.IndexByte(rest, '{')
	if j < 0 {
		return nil, errors.New("no JSON object after PoW data marker")
	}
	var pd PowData
	if err := json.NewDecoder(strings.NewReader(rest[j:])).Decode(&pd); err != nil {
		return nil, err
	}
	if pd.Header == "" || pd.Hash == "" {
		return nil, errors.New("PoW record missing header or hash")
	}
	return &pd, nil
}

// Run blocks until ctx is cancelled. It opens the file, seeks to either
// the saved cursor (if LoadCursor returns one and the inode still
// matches) or the end on first-ever run, and reads new lines as they
// are appended. If the file is rotated (shrinks, or inode changes), it
// re-opens.
func (t *Tailer) Run(ctx context.Context) {
	defer close(t.Events)
	defer close(t.Attempts)
	defer close(t.Latencies)
	defer close(t.Shares)

	var (
		f       *os.File
		reader  *bufio.Reader
		lastIno uint64
		lastPos int64
		// firstOpen distinguishes the very first Open of this tailer
		// (where we honor LoadCursor and replay the backlog) from
		// rotation re-opens (where we always start at 0).
		firstOpen = true
	)

	open := func() error {
		if f != nil {
			_ = f.Close()
		}
		nf, err := os.Open(t.Path)
		if err != nil {
			return err
		}
		st, err := nf.Stat()
		if err != nil {
			_ = nf.Close()
			return err
		}
		ino := inodeOf(st)
		size := st.Size()

		// Decide where to start reading.
		var startAt int64
		if firstOpen {
			if t.LoadCursor != nil {
				if savedIno, savedOff, ok := t.LoadCursor(); ok &&
					savedIno == ino && savedOff <= size {
					startAt = savedOff
					if savedOff < size {
						t.Log.Info("logmon: resuming from saved cursor",
							"path", t.Path, "offset", savedOff, "size", size)
					}
				} else {
					// No cursor, or stale (file was rotated since we
					// last saw it, so we have no way to know what we
					// already read). Start at end to avoid replaying
					// the entire log.
					startAt = size
				}
			} else {
				startAt = size
			}
			firstOpen = false
		} else {
			// Rotation: read the whole replacement file from the start.
			startAt = 0
		}

		if _, err := nf.Seek(startAt, io.SeekStart); err != nil {
			_ = nf.Close()
			return err
		}
		f = nf
		reader = bufio.NewReader(f)
		lastIno = ino
		lastPos = startAt
		return nil
	}

	saveCursor := func(force bool) {
		if t.SaveCursor == nil || f == nil {
			return
		}
		now := time.Now()
		if !force && now.Sub(t.lastCursorSave) < time.Second {
			return
		}
		t.SaveCursor(lastIno, lastPos)
		t.lastCursorSave = now
	}

	// Initial open; retry on failure until the file exists.
	for {
		if err := open(); err != nil {
			t.Log.Warn("logmon: waiting for log file", "path", t.Path, "err", err)
			if !sleep(ctx, 2*time.Second) {
				return
			}
			continue
		}
		break
	}
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()

	for {
		if ctx.Err() != nil {
			saveCursor(true)
			return
		}
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			t.handleLine(line)
			lastPos, _ = f.Seek(0, io.SeekCurrent)
		}
		if err == nil {
			continue
		}
		if !errors.Is(err, io.EOF) {
			t.Log.Warn("logmon: read error, reopening", "err", err)
			saveCursor(true)
			if !sleep(ctx, t.PollWait) {
				return
			}
			_ = open()
			continue
		}

		// EOF: persist where we are (caught up to current end), then
		// check for rotation (inode changed) or truncation (size < pos).
		saveCursor(false)
		if st, statErr := os.Stat(t.Path); statErr == nil {
			if inodeOf(st) != lastIno || st.Size() < lastPos {
				t.Log.Info("logmon: log rotated, reopening", "path", t.Path)
				if err := open(); err != nil {
					t.Log.Warn("logmon: reopen failed", "err", err)
				}
				continue
			}
		}
		if !sleep(ctx, t.PollWait) {
			saveCursor(true)
			return
		}
	}
}

func (t *Tailer) handleLine(line string) {
	// PoW reproduction data (patch 0008). Must ship on the Shares
	// channel so it stays ordered before its "Accepted client" line.
	if strings.Contains(line, powDataMarker) {
		pd, err := parsePowLine(line)
		if err != nil {
			t.Log.Warn("logmon: unparseable PoW data line", "err", err)
			return
		}
		if pd != nil {
			select {
			case t.Shares <- ShareEvent{SeenAt: time.Now(), Pow: pd}:
			default:
			}
		}
		return
	}
	// Accepted share: extract share difficulty and block header hash.
	if m := acceptedShareRE.FindStringSubmatch(line); m != nil {
		if d, err := strconv.ParseFloat(m[1], 64); err == nil {
			select {
			case t.Shares <- ShareEvent{SeenAt: time.Now(), Diff: d, Hash: m[2]}:
			default:
			}
		}
		// Don't return, an accepted share may also be a block solve,
		// so let the solve-diff regex below get a chance to match too.
	}
	// Rejected share with diff info: "Rejected client <id> dupe|high diff ..."
	if m := rejectedDiffRE.FindStringSubmatch(line); m != nil {
		reason := rejectKeyword(m[1])
		select {
		case t.Shares <- ShareEvent{SeenAt: time.Now(), Rejected: true, Reason: reason}:
		default:
		}
		return
	}
	// Rejected share without diff: "Rejected client <id> invalid share <reason>"
	if m := rejectedInvalRE.FindStringSubmatch(line); m != nil {
		reason := strings.TrimSpace(m[1])
		select {
		case t.Shares <- ShareEvent{SeenAt: time.Now(), Rejected: true, Reason: reason}:
		default:
		}
		return
	}
	if m := latencyRE.FindStringSubmatch(line); m != nil {
		if ms, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			select {
			case t.Latencies <- LatencyEvent{SeenAt: time.Now(), LatencyMs: ms}:
			default:
			}
		}
		return
	}
	if m := solveDiffRE.FindStringSubmatch(line); m != nil {
		var d float64
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			d = v
			t.lastSolveDiff = v
		}
		select {
		case t.Attempts <- AttemptEvent{SeenAt: time.Now(), ShareDiff: d, RawLine: line}:
		default:
			// Best-effort metric, dropping is fine.
		}
		return
	}
	m := solvedRE.FindStringSubmatch(line)
	if m == nil {
		return
	}
	height, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return
	}
	ev := BlockEvent{
		Height:    height,
		SeenAt:    time.Now(),
		RawLine:   line,
		ShareDiff: t.lastSolveDiff,
	}
	t.lastSolveDiff = 0
	select {
	case t.Events <- ev:
		t.Log.Info("logmon: block solved", "height", height, "share_diff", ev.ShareDiff)
	default:
		t.Log.Warn("logmon: events channel full, dropping", "height", height)
	}
}

// rejectKeyword maps the short keyword ckpool uses in "Rejected client
// <id> <keyword> diff ..." log lines to a human-readable reason.
func rejectKeyword(kw string) string {
	switch strings.ToLower(kw) {
	case "dupe":
		return "Duplicate"
	case "high":
		return "Above target"
	default:
		return kw
	}
}

// FindBestShareHash scans the log file for the accepted share with the
// highest difficulty and returns its diff and block header hash. Used as
// a one-time backfill when bestDiff is persisted but the hash is not
// (pre-upgrade shares). Returns (0, "") if no accepted shares are found.
func FindBestShareHash(path string) (float64, string) {
	f, err := os.Open(path)
	if err != nil {
		return 0, ""
	}
	defer f.Close()

	var bestDiff float64
	var bestHash string
	_ = scanLines(f, func(line string) {
		if !strings.Contains(line, "Accepted client") {
			return
		}
		m := acceptedShareRE.FindStringSubmatch(line)
		if m == nil {
			return
		}
		d, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return
		}
		if d >= bestDiff {
			bestDiff = d
			bestHash = m[2]
		}
	})
	return bestDiff, bestHash
}

// scanLines invokes fn for every line of r. Unlike bufio.Scanner, a line
// of any length can never abort the scan: an oversized line is passed to
// fn truncated to the buffer size and its remainder is discarded. A
// months-old production log with one pathological line must not silently
// stop a backfill scan partway through, that failure mode is invisible
// and permanent.
func scanLines(f *os.File, fn func(line string)) error {
	r := bufio.NewReaderSize(f, 256*1024)
	skipping := false
	for {
		chunk, isPrefix, err := r.ReadLine()
		if len(chunk) > 0 && !skipping {
			fn(string(chunk))
		}
		skipping = isPrefix
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// logStampRE extracts the "[YYYY-MM-DD HH:MM:SS.mmm]" prefix ckpool
// puts on every log line (local time of the ckpool process).
var logStampRE = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})`)

// FindBestPowData scans the log for the highest-difficulty "Best share
// PoW data" record (patch 0008) whose share was subsequently confirmed
// by an "Accepted client" line with the same hash. Used as a one-time
// backfill when kamado-api holds no persisted record but the log
// already contains some (fresh database, or PoW events dropped under
// load), without it the PoW inspector would sit empty until a share
// beat ckpool's in-process best, which after a long uptime can take
// arbitrarily long. The returned time is parsed from the log line's
// timestamp; zero when unparseable. Returns a nil record if no confirmed
// record exists; err reports open/read failures and the returned counts
// say how many PoW lines were seen and how many failed to parse, so the
// caller can log a diagnosable outcome instead of a silent nil.
func FindBestPowData(path string) (best *PowData, bestAt time.Time, seen, unparseable int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, time.Time{}, 0, 0, err
	}
	defer f.Close()

	// Records awaiting their "Accepted client" confirmation, keyed by
	// hash. Bounded the same way as the aggregator's live pairing.
	type parked struct {
		pow *PowData
		at  time.Time
	}
	pending := make(map[string]parked)
	err = scanLines(f, func(line string) {
		if strings.Contains(line, powDataMarker) {
			seen++
			pd, perr := parsePowLine(line)
			if perr != nil || pd == nil {
				unparseable++
				return
			}
			var at time.Time
			if sm := logStampRE.FindStringSubmatch(line); sm != nil {
				at, _ = time.ParseInLocation("2006-01-02 15:04:05", sm[1], time.Local)
			}
			if len(pending) >= 16 {
				pending = make(map[string]parked)
			}
			pending[pd.Hash] = parked{pow: pd, at: at}
			return
		}
		if !strings.Contains(line, "Accepted client") {
			return
		}
		m := acceptedShareRE.FindStringSubmatch(line)
		if m == nil {
			return
		}
		if p, ok := pending[m[2]]; ok {
			delete(pending, m[2])
			if best == nil || p.pow.SDiff > best.SDiff {
				best = p.pow
				bestAt = p.at
			}
		}
	})
	return best, bestAt, seen, unparseable, err
}

// ShareStats is a full accounting of the accepted and rejected shares
// recorded in a ckpool log, used to rebuild the all-time counters when
// the stored ones are lost or truncated. Diffs holds each accepted
// share's difficulty in log order so the caller can bucket them with
// its own thresholds rather than duplicating that policy here.
type ShareStats struct {
	Accepted      int64
	DiffSum       float64
	Diffs         []float64
	RejectReasons map[string]int64
}

// ScanShareStats reads an entire ckpool log and tallies every accepted
// and rejected share it contains. On a busy pool this walks millions of
// lines, so it is meant for explicit recovery, never for the startup
// path.
func ScanShareStats(path string) (*ShareStats, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	st := &ShareStats{RejectReasons: make(map[string]int64)}
	err = scanLines(f, func(line string) {
		if strings.Contains(line, "Accepted client") {
			m := acceptedShareRE.FindStringSubmatch(line)
			if m == nil {
				return
			}
			d, perr := strconv.ParseFloat(m[1], 64)
			if perr != nil {
				return
			}
			st.Accepted++
			st.DiffSum += d
			st.Diffs = append(st.Diffs, d)
			return
		}
		if !strings.Contains(line, "Rejected client") {
			return
		}
		if m := rejectedDiffRE.FindStringSubmatch(line); m != nil {
			st.RejectReasons[rejectKeyword(m[1])]++
			return
		}
		if m := rejectedInvalRE.FindStringSubmatch(line); m != nil {
			reason := strings.TrimSpace(m[1])
			if reason == "" {
				reason = "Unknown"
			}
			st.RejectReasons[reason]++
		}
	})
	if err != nil {
		return nil, err
	}
	return st, nil
}

// sleep returns false if ctx was cancelled during the wait.
func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
