package testpg

import (
	"io"
	"net"
	"net/url"
	"testing"
	"time"

	"nodevas/internal/db"
)

// slowAnswer is how long slowServer holds back a new connection's first
// answer: longer than the 5 s connect timeout embedded server URLs carry,
// the way a backend starting on a machine saturated by a parallel
// "go test ./..." can be.
const slowAnswer = 6 * time.Second

// slowServer is a TCP proxy to target whose connections answer only after
// slowAnswer, then forward normally. It returns the proxy's address.
func slowServer(t *testing.T, target string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer client.Close()
				server, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer server.Close()
				go func() { _, _ = io.Copy(server, client); _ = server.Close() }()
				time.Sleep(slowAnswer)
				_, _ = io.Copy(client, server)
			}()
		}
	}()
	return listener.Addr().String()
}

// A database testpg hands out is on a server the tests started and just
// used: one slow to answer is busy, not gone, and the test waits for it
// instead of failing with "postgres unreachable".
func TestASlowServerIsWaitedFor(t *testing.T) {
	location, err := url.Parse(NewURL(t))
	if err != nil {
		t.Fatal(err)
	}
	location.Host = slowServer(t, location.Host)
	database, err := db.OpenPostgres(location.String(), 0)
	if err != nil {
		t.Fatalf("open through a slow server: %v", err)
	}
	defer database.Close()
	var one int
	if err := database.QueryRow(`SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("query = %d, %v", one, err)
	}
}
