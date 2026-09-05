// Package zmqmon subscribes to bitcoind's ZMQ `hashblock` topic and
// emits a lightweight event each time a new chain tip appears.
//
// This is strictly an "immediate refresh trigger", it does NOT replace
// the ckpool log tailer, which still handles our own solved-block
// detection. ZMQ just shortens the latency between bitcoind seeing a
// new tip and the Kamado dashboard reflecting it (otherwise we'd wait
// up to PollInterval seconds for the next state refresh).
package zmqmon

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-zeromq/zmq4"
)

// TipEvent is emitted when bitcoind publishes a new block hash.
type TipEvent struct {
	Hash   string    // lowercase hex, no 0x prefix
	SeenAt time.Time
}

// Monitor subscribes to the hashblock topic at Endpoint (e.g.
// "tcp://bitcoind:28332") and publishes TipEvents to Events. Run blocks
// until ctx is cancelled.
type Monitor struct {
	Endpoint string
	Log      *slog.Logger
	Events   chan TipEvent
}

// New returns a Monitor with a buffered event channel. Endpoint may be
// empty, in which case Run exits immediately as a no-op, useful for
// deployments where ZMQ isn't configured on bitcoind.
func New(endpoint string, log *slog.Logger) *Monitor {
	return &Monitor{
		Endpoint: endpoint,
		Log:      log,
		Events:   make(chan TipEvent, 16),
	}
}

// Run connects, subscribes to "hashblock", and relays tip hashes until
// ctx is cancelled. On connection failure it backs off and retries so
// bitcoind restarts don't kill the subscriber permanently.
func (m *Monitor) Run(ctx context.Context) {
	defer close(m.Events)
	if m.Endpoint == "" {
		m.Log.Info("zmqmon disabled (no endpoint)")
		return
	}

	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		if err := m.runOnce(ctx); err != nil && ctx.Err() == nil {
			m.Log.Warn("zmqmon reconnecting", "err", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}
		// ctx cancelled
		return
	}
}

func (m *Monitor) runOnce(ctx context.Context) error {
	sub := zmq4.NewSub(ctx)
	defer sub.Close()

	if err := sub.Dial(m.Endpoint); err != nil {
		return fmt.Errorf("dial %s: %w", m.Endpoint, err)
	}
	if err := sub.SetOption(zmq4.OptionSubscribe, "hashblock"); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	m.Log.Info("zmqmon connected", "endpoint", m.Endpoint)

	for {
		// Recv blocks until a message arrives or the underlying context
		// is cancelled.
		msg, err := sub.Recv()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("recv: %w", err)
		}
		// bitcoind publishes 3 frames: topic, body, sequence.
		if len(msg.Frames) < 2 {
			continue
		}
		topic := string(msg.Frames[0])
		if topic != "hashblock" {
			continue
		}
		hash := hex.EncodeToString(msg.Frames[1])
		select {
		case m.Events <- TipEvent{Hash: hash, SeenAt: time.Now()}:
		case <-ctx.Done():
			return nil
		default:
			// Dropping is fine: the consumer only uses this as a
			// "refresh now" signal, not as an event log.
			m.Log.Debug("zmqmon event dropped (consumer slow)", "hash", hash)
		}
	}
}
