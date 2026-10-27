package dbtest

import (
	"io"
	"net"
	"net/url"
	"os"
	"sync"
	"testing"

	"nodevas/internal/db"
)

// Stall is a TCP proxy between the tests and their PostgreSQL server that can
// be made to hang: once Hang is called it drops every connection it carries
// and accepts new ones without ever answering them, the way a paused or
// I/O-stalled server does. A connection attempt then ends in the driver's own
// connect timeout, an error that wraps context.DeadlineExceeded while the
// caller's context is still alive.
type Stall struct {
	listener net.Listener
	target   string

	mu      sync.Mutex
	stalled bool
	conns   []net.Conn
}

// UseStalledPostgres is UsePostgres with db.Open pointed through a Stall, and
// a one-second connect timeout so a hung connection attempt ends quickly. It
// returns nil when no server is configured.
func UseStalledPostgres(t testing.TB) *Stall {
	t.Helper()
	if !UsePostgres(t) {
		return nil
	}
	location, err := url.Parse(os.Getenv(db.URLEnv))
	if err != nil {
		t.Fatalf("%s: %v", db.URLEnv, err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	stall := &Stall{listener: listener, target: location.Host}
	t.Cleanup(stall.close)
	go stall.serve()

	location.Host = listener.Addr().String()
	query := location.Query()
	query.Set("connect_timeout", "1")
	location.RawQuery = query.Encode()
	t.Setenv(db.URLEnv, location.String())
	return stall
}

// Hang drops the connections already made and leaves every new one waiting.
func (s *Stall) Hang() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stalled = true
	for _, conn := range s.conns {
		_ = conn.Close()
	}
	s.conns = nil
}

func (s *Stall) serve() {
	for {
		client, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		stalled := s.stalled
		s.conns = append(s.conns, client)
		s.mu.Unlock()
		if stalled {
			// Held open, never read and never answered, until Hang's next call
			// or the test's end closes it.
			continue
		}
		go s.forward(client)
	}
}

func (s *Stall) forward(client net.Conn) {
	server, err := net.Dial("tcp", s.target)
	if err != nil {
		_ = client.Close()
		return
	}
	s.mu.Lock()
	if s.stalled {
		s.mu.Unlock()
		_ = server.Close()
		return
	}
	s.conns = append(s.conns, server)
	s.mu.Unlock()
	go func() {
		_, _ = io.Copy(server, client)
		_ = server.Close()
	}()
	_, _ = io.Copy(client, server)
	_ = client.Close()
}

func (s *Stall) close() {
	_ = s.listener.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, conn := range s.conns {
		_ = conn.Close()
	}
	s.conns = nil
}
