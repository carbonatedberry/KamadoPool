// Minimal RFC 6455 WebSocket server implementation, stdlib-only.
//
// Scope: push JSON snapshots to subscribed clients. We never expect
// client payloads larger than a ping/pong and we don't negotiate
// extensions (compression, fragmentation). Anything fancier is out of
// scope for Phase 2b, upgrade to a real ws library once we can run
// `go mod tidy` in a real build environment.
package httpapi

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kamadopool/kamado-api/internal/state"
)

// wsGUID is the fixed magic string from RFC 6455 §1.3 used to compute
// Sec-WebSocket-Accept.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// WebSocket frame opcodes we care about.
const (
	opText   byte = 0x1
	opBinary byte = 0x2
	opClose  byte = 0x8
	opPing   byte = 0x9
	opPong   byte = 0xA
)

// wsClient is one connected WebSocket subscriber.
type wsClient struct {
	conn    net.Conn
	send    chan []byte // serialized JSON frames pending write
	writeMu sync.Mutex  // guards writes to conn (writer + reader-pong)

	// dropMisses counts consecutive Broadcast calls where this client's
	// send channel was full. After maxDropMisses we close the connection
	// instead of letting a stuck reader hold an old snapshot forever.
	dropMisses int
}

// maxDropMisses bounds how many back-to-back broadcasts we let the
// hub skip for one client before forcibly disconnecting it. With the
// poll cadence at 5s this is roughly 30s of unresponsiveness.
const maxDropMisses = 6

// writeFrameLocked writes one frame, serializing writers on the client.
func (c *wsClient) writeFrameLocked(opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return writeFrame(c.conn, opcode, payload)
}

// Hub fans out snapshot updates to all subscribed WebSocket clients.
// Register via Add / Remove from the websocket handler; Broadcast is
// called from the aggregator's OnRefresh hook.
type Hub struct {
	mu      sync.RWMutex
	clients map[*wsClient]struct{}
}

func NewHub() *Hub { return &Hub{clients: make(map[*wsClient]struct{})} }

func (h *Hub) add(c *wsClient) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) remove(c *wsClient) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
	}
	h.mu.Unlock()
}

// Broadcast serializes the snapshot once and enqueues it for every
// subscribed client. Slow clients are dropped rather than blocking
// the hub. After maxDropMisses consecutive drops for a single client,
// we forcibly close its connection so a stuck consumer doesn't hold
// resources indefinitely or freeze on a stale snapshot.
func (h *Hub) Broadcast(snap state.Snapshot) {
	payload, err := json.Marshal(snap)
	if err != nil {
		return
	}
	var toClose []*wsClient
	h.mu.Lock()
	for c := range h.clients {
		select {
		case c.send <- payload:
			c.dropMisses = 0
		default:
			c.dropMisses++
			if c.dropMisses >= maxDropMisses {
				toClose = append(toClose, c)
			}
		}
	}
	h.mu.Unlock()
	for _, c := range toClose {
		// Closing the conn unblocks the writer goroutine, which
		// removes the client from the hub through wsReader's defer.
		_ = c.conn.Close()
	}
}

// handleWS upgrades an HTTP request to a WebSocket connection and
// subscribes it to the hub. On error before the hijack, it writes a
// plain HTTP 4xx.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") ||
		!strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		http.Error(w, "expected websocket upgrade", http.StatusBadRequest)
		return
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		w.Header().Set("Sec-WebSocket-Version", "13")
		http.Error(w, "unsupported websocket version", http.StatusUpgradeRequired)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing Sec-WebSocket-Key", http.StatusBadRequest)
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		http.Error(w, "hijack failed", http.StatusInternalServerError)
		return
	}

	// Write handshake response directly to the bufio writer.
	accept := wsAcceptKey(key)
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := brw.WriteString(resp); err != nil {
		_ = conn.Close()
		return
	}
	if err := brw.Flush(); err != nil {
		_ = conn.Close()
		return
	}

	client := &wsClient{conn: conn, send: make(chan []byte, 8)}
	s.Hub.add(client)
	s.Log.Info("ws client connected", "remote", conn.RemoteAddr())

	// Immediately push the current snapshot so the UI doesn't wait for
	// the next tick.
	if payload, err := json.Marshal(s.Agg.Snapshot()); err == nil {
		select {
		case client.send <- payload:
		default:
		}
	}

	go s.wsWriter(client)
	go s.wsReader(client, brw.Reader)
}

// wsWriter pumps queued payloads as text frames until send is closed
// or a write fails.
func (s *Server) wsWriter(c *wsClient) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case payload, ok := <-c.send:
			if !ok {
				_ = c.writeFrameLocked(opClose, nil)
				_ = c.conn.Close()
				return
			}
			if err := c.writeFrameLocked(opText, payload); err != nil {
				_ = c.conn.Close()
				return
			}
		case <-ticker.C:
			if err := c.writeFrameLocked(opPing, nil); err != nil {
				_ = c.conn.Close()
				return
			}
		}
	}
}

// wsReader reads client frames mainly to notice close/ping and to
// drive cleanup when the connection dies. Ignores any app-level
// content since the protocol is server-push only.
func (s *Server) wsReader(c *wsClient, br *bufio.Reader) {
	defer s.Hub.remove(c)
	for {
		op, payload, err := readFrame(br)
		if err != nil {
			return
		}
		switch op {
		case opClose:
			return
		case opPing:
			_ = c.writeFrameLocked(opPong, payload)
		}
	}
}

// wsAcceptKey computes Sec-WebSocket-Accept from the client's key per
// RFC 6455 §4.2.2.
func wsAcceptKey(key string) string {
	h := sha1.New()
	_, _ = io.WriteString(h, key+wsGUID)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// writeFrame writes a single unmasked server frame. Only supports
// payloads up to 2^63-1 bytes which is more than we'll ever send.
func writeFrame(conn net.Conn, opcode byte, payload []byte) error {
	var hdr [10]byte
	hdr[0] = 0x80 | (opcode & 0x0F) // FIN=1
	n := len(payload)
	var hdrLen int
	switch {
	case n < 126:
		hdr[1] = byte(n)
		hdrLen = 2
	case n < 1<<16:
		hdr[1] = 126
		binary.BigEndian.PutUint16(hdr[2:4], uint16(n))
		hdrLen = 4
	default:
		hdr[1] = 127
		binary.BigEndian.PutUint64(hdr[2:10], uint64(n))
		hdrLen = 10
	}
	if _, err := conn.Write(hdr[:hdrLen]); err != nil {
		return err
	}
	if n > 0 {
		if _, err := conn.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// readFrame reads one client frame. Clients MUST mask (RFC 6455 §5.3);
// we enforce that and unmask in place.
func readFrame(br *bufio.Reader) (byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(br, h[:]); err != nil {
		return 0, nil, err
	}
	opcode := h[0] & 0x0F
	masked := h[1]&0x80 != 0
	if !masked {
		return 0, nil, errors.New("ws: client frame not masked")
	}
	length := int64(h[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(br, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(br, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint64(ext[:]))
	}
	// Sanity cap on client payloads; we never expect real data from
	// clients.
	if length > 1<<20 {
		return 0, nil, errors.New("ws: client frame too large")
	}
	var mask [4]byte
	if _, err := io.ReadFull(br, mask[:]); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(br, payload); err != nil {
		return 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return opcode, payload, nil
}
