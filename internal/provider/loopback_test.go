package provider

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// The bug this guards is only visible where /etc/hosts does not answer for
// localhost, so the CI offline job (strace on connect, port 53 counted as
// reaching out) is the test that failed without the fix. This pins the
// property on any machine: the resolver is never consulted.
func TestARuntimeOnLocalhostIsDialledWithoutAskingAResolver(t *testing.T) {
	for _, network := range []string{"tcp4", "tcp6"} {
		t.Run(network, func(t *testing.T) {
			addr := "127.0.0.1:0"
			if network == "tcp6" {
				addr = "[::1]:0"
			}
			ln, err := net.Listen(network, addr)
			if err != nil {
				t.Skipf("no %s loopback here: %v", network, err)
			}
			defer ln.Close()
			go func() {
				for {
					c, err := ln.Accept()
					if err != nil {
						return
					}
					c.Close()
				}
			}()
			_, port, _ := net.SplitHostPort(ln.Addr().String())

			var asked atomic.Int32
			d := &net.Dialer{Timeout: 2 * time.Second, Resolver: &net.Resolver{
				PreferGo: true,
				Dial: func(context.Context, string, string) (net.Conn, error) {
					asked.Add(1)
					return nil, errors.New("no resolver in this test")
				},
			}}
			c, err := DialLoopback(d)(context.Background(), "tcp", net.JoinHostPort("localhost", port))
			if err != nil {
				t.Fatalf("a runtime listening only on %s was not reached as localhost: %v", ln.Addr(), err)
			}
			c.Close()
			if n := asked.Load(); n != 0 {
				t.Fatalf("dialling localhost asked a resolver %d time(s); nothing should leave the process to find loopback", n)
			}
		})
	}
}
