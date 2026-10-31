package testpg

import (
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"nodevas/internal/db"
)

// slowFirstServer is a TCP proxy to target whose first connection answers
// only after delay; later ones answer at once. It returns its address.
func slowFirstServer(t *testing.T, target string, delay time.Duration) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	var accepted atomic.Int64
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			first := accepted.Add(1) == 1
			go func() {
				defer client.Close()
				server, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer server.Close()
				go func() { _, _ = io.Copy(server, client); _ = server.Close() }()
				if first {
					time.Sleep(delay)
				}
				_, _ = io.Copy(client, server)
			}()
		}
	}()
	return listener.Addr().String()
}

// A stalled server's one-second connect timeout is for the dials after Hang.
// Opening the workspace before it, on a server busy enough that one answer
// takes longer than that, still succeeds.
func TestAStalledPostgresOpensOnABusyServer(t *testing.T) {
	useStalledPostgres(t, func(server string) string {
		return slowFirstServer(t, server, 2*time.Second)
	})
	database, err := db.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatalf("open before the stall: %v", err)
	}
	defer database.Close()
	var one int
	if err := database.QueryRow(`SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("query = %d, %v", one, err)
	}
}
