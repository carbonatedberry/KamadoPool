package ckpool

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeServer spawns a goroutine that listens on a unix socket under `dir`,
// reads one request in the ckpool wire format, and responds with `reply`.
// It validates the request format and reports errors via `t`.
func fakeServer(t *testing.T, dir, sockName, wantCmd, reply string) {
	t.Helper()
	path := filepath.Join(dir, sockName)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Read 4-byte LE length
		var lenBuf [4]byte
		if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
			t.Errorf("server read len: %v", err)
			return
		}
		n := binary.LittleEndian.Uint32(lenBuf[:])
		buf := make([]byte, n)
		if _, err := io.ReadFull(conn, buf); err != nil {
			t.Errorf("server read payload: %v", err)
			return
		}
		if string(buf) != wantCmd {
			t.Errorf("server got cmd %q, want %q", string(buf), wantCmd)
			return
		}

		// Respond with length prefix + payload
		binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(reply)))
		if _, err := conn.Write(lenBuf[:]); err != nil {
			t.Errorf("server write len: %v", err)
			return
		}
		if _, err := conn.Write([]byte(reply)); err != nil {
			t.Errorf("server write reply: %v", err)
			return
		}
	}()
}

func tempSockDir(t *testing.T) string {
	t.Helper()
	// Use a short dir: unix socket paths are limited to ~108 chars on Linux.
	dir, err := os.MkdirTemp("", "ck")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestClient_Ping(t *testing.T) {
	dir := tempSockDir(t)
	fakeServer(t, dir, SocketStratifier, "ping", "pong")

	c := New(dir)
	c.Timeout = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestClient_PoolStats(t *testing.T) {
	dir := tempSockDir(t)
	reply := `{"start":1775000000,"update":1775949393,"workers":42664,"users":24679,"disconnected":6119,"shares":338031,"sps1":6.48,"sps5":6.49,"sps15":6.49,"sps60":6.48,"accepted":338031,"rejected":29602,"dsps1":0.274,"dsps5":0.302,"dsps15":0.317,"dsps60":0.309,"dsps360":0.298,"dsps1440":0.290,"dsps10080":0.285}`
	fakeServer(t, dir, SocketStratifier, "poolstats", reply)

	c := New(dir)
	c.Timeout = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ps, err := c.PoolStats(ctx)
	if err != nil {
		t.Fatalf("poolstats: %v", err)
	}
	if ps.Workers != 42664 {
		t.Errorf("Workers = %d, want 42664", ps.Workers)
	}
	if got := DSPSToHashrate(ps.DSPS1); got < 1e9 {
		t.Errorf("DSPS1 hashrate = %v, want > 1GH/s", got)
	}
}

func TestClient_Clients(t *testing.T) {
	dir := tempSockDir(t)
	reply := `{"clients":[{"id":12345,"useragent":"Bitaxe/2.4.0","address":"192.168.1.100","workername":"bc1q.abc","authorised":true,"bestdiff":12345.67}]}`
	fakeServer(t, dir, SocketStratifier, "clients", reply)

	c := New(dir)
	c.Timeout = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	clients, err := c.Clients(ctx)
	if err != nil {
		t.Fatalf("clients: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("got %d clients, want 1", len(clients))
	}
	if clients[0].UserAgent != "Bitaxe/2.4.0" {
		t.Errorf("UserAgent = %q, want Bitaxe/2.4.0", clients[0].UserAgent)
	}
}

func TestClient_DialError(t *testing.T) {
	c := New("/nonexistent/path")
	c.Timeout = 200 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Ping(ctx); err == nil {
		t.Error("expected error, got nil")
	}
}
