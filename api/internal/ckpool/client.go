// Package ckpool implements a client for CKPool's Unix-domain-socket control
// protocol. Each request opens a fresh connection, writes a 4-byte little-
// endian length prefix followed by the payload, half-closes the write side,
// then reads a 4-byte length prefix and that many bytes of response.
//
// See src/libckpool.c in upstream CKPool for the reference implementation
// (send_unix_msg / recv_unix_msg).
package ckpool

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// Default socket filenames under the sockdir.
const (
	SocketListener   = "listener"
	SocketStratifier = "stratifier"
	SocketConnector  = "connector"
)

// Client talks to the ckpool Unix sockets at SockDir. It is safe for
// concurrent use: each Send opens its own short-lived connection, matching
// CKPool's own one-shot request-response model.
type Client struct {
	SockDir string
	Timeout time.Duration
}

// New returns a Client configured with a sensible default timeout.
func New(sockDir string) *Client {
	return &Client{
		SockDir: sockDir,
		Timeout: 5 * time.Second,
	}
}

// maxMsgLen matches the upper bound in libckpool.c (0x80000000). Anything
// above this indicates a protocol error and we refuse to allocate for it.
const maxMsgLen = 0x80000000

// ErrProtocol is returned when the server's framing is invalid.
var ErrProtocol = errors.New("ckpool: protocol error")

// Send delivers a single command to the named ckpool socket and returns the
// raw response bytes. `sockName` is one of SocketListener, SocketStratifier,
// or SocketConnector.
//
// If the server closes the connection before writing a response (common
// during ckpool warm-up, the stratifier accepts the socket but its stats
// aren't populated yet), Send retries once after a short backoff.
func (c *Client) Send(ctx context.Context, sockName, cmd string) ([]byte, error) {
	buf, err := c.sendOnce(ctx, sockName, cmd)
	if err != nil && errors.Is(err, io.EOF) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
		return c.sendOnce(ctx, sockName, cmd)
	}
	return buf, err
}

func (c *Client) sendOnce(ctx context.Context, sockName, cmd string) ([]byte, error) {
	if cmd == "" {
		return nil, fmt.Errorf("ckpool: empty command")
	}
	path := c.SockDir + "/" + sockName

	var d net.Dialer
	dialCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	conn, err := d.DialContext(dialCtx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("ckpool: dial %s: %w", path, err)
	}
	defer conn.Close()

	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(c.Timeout))
	}

	// ---- write: 4-byte LE length + payload + half-close ----
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(cmd)))
	if _, err := conn.Write(lenBuf[:]); err != nil {
		return nil, fmt.Errorf("ckpool: write len: %w", err)
	}
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return nil, fmt.Errorf("ckpool: write payload: %w", err)
	}
	// Half-close write so the server sees EOF and responds, mirroring
	// libckpool's shutdown(SHUT_WR) at the end of send_unix_msg.
	if uc, ok := conn.(*net.UnixConn); ok {
		if err := uc.CloseWrite(); err != nil {
			return nil, fmt.Errorf("ckpool: close write: %w", err)
		}
	}

	// ---- read: 4-byte LE length + payload ----
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return nil, fmt.Errorf("ckpool: read len: %w", err)
	}
	msgLen := binary.LittleEndian.Uint32(lenBuf[:])
	if msgLen == 0 || msgLen > maxMsgLen {
		return nil, fmt.Errorf("%w: invalid msg len %d", ErrProtocol, msgLen)
	}
	buf := make([]byte, msgLen)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, fmt.Errorf("ckpool: read payload: %w", err)
	}
	return buf, nil
}

// SendJSON sends a command and unmarshals the response into `out`.
// If the response is not valid JSON, the raw bytes are returned in the error.
func (c *Client) SendJSON(ctx context.Context, sockName, cmd string, out any) error {
	raw, err := c.Send(ctx, sockName, cmd)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("ckpool: unmarshal %q: %w (raw=%s)", cmd, err, string(raw))
	}
	return nil
}

// Ping returns nil if the stratifier replies "pong".
func (c *Client) Ping(ctx context.Context) error {
	raw, err := c.Send(ctx, SocketStratifier, "ping")
	if err != nil {
		return err
	}
	if string(raw) != "pong" {
		return fmt.Errorf("ckpool: expected pong, got %q", string(raw))
	}
	return nil
}
