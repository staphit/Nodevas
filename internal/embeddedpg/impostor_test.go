package embeddedpg

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// Another local user can be listening on the cluster's port before the
// postmaster binds it (the port is reused from run.json). Every embedded
// connection must refuse to hand such an impostor the password in clear: a
// cleartext or MD5 request, or "authentication ok" without SCRAM, ends the
// connection instead.
func TestImpostorOnClusterPortNeverGetsPassword(t *testing.T) {
	const secret = "s3cret-admin"
	for name, reply := range map[string][]byte{
		"cleartext": {'R', 0, 0, 0, 8, 0, 0, 0, 3},
		"md5":       {'R', 0, 0, 0, 12, 0, 0, 0, 5, 1, 2, 3, 4},
		"trust":     {'R', 0, 0, 0, 8, 0, 0, 0, 0, 'Z', 0, 0, 0, 5, 'I'},
	} {
		t.Run(name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			received := make(chan []byte, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					received <- nil
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				var length [4]byte
				if _, err := io.ReadFull(conn, length[:]); err != nil {
					received <- nil
					return
				}
				startup := make([]byte, binary.BigEndian.Uint32(length[:])-4)
				if _, err := io.ReadFull(conn, startup); err != nil {
					received <- nil
					return
				}
				_, _ = conn.Write(reply)
				// Whatever the client sends next (a password message, or a
				// query after "ok") is what the impostor learns.
				rest, _ := io.ReadAll(conn)
				received <- rest
			}()

			port := listener.Addr().(*net.TCPAddr).Port
			conn, err := connectOnce(context.Background(), loopbackURL(adminRole, secret, port, "postgres"))
			if err == nil {
				conn.Close(context.Background())
				t.Error("connected to an impostor")
			}
			listener.Close()
			if got := <-received; bytes.Contains(got, []byte(secret)) || bytes.HasPrefix(got, []byte{'p'}) {
				t.Fatalf("the impostor received %q", got)
			}
		})
	}
}
