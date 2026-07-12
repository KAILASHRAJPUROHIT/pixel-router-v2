// Package ttlproxy is V2's replacement for V1's ttlproxy.c - same
// underlying idea (re-originate every forwarded TCP connection as a
// fresh local socket, so it naturally carries this device's own TTL/
// hop-limit instead of leaking the tethered client's), but in Go
// instead of hand-rolled C, and covering IPv6 from day one - research
// found V1's C++/iptables-only version never touched IPv6 at all,
// confirmed live: IPv6 forwarding was enabled with zero hop-limit
// normalization, a real leak in the whole tethering-detection defense
// for any dual-stack client device.
//
// With Linux TPROXY (unlike plain DNAT/REDIRECT), an accepted
// connection's LocalAddr() already reports the client's *original*
// destination - no SO_ORIGINAL_DST getsockopt dance needed. The accept
// loop and relay logic below only need a listener bound with
// IP_TRANSPARENT (see listener.go) plus a way to dial the original
// destination.
package ttlproxy

import (
	"io"
	"log"
	"net"
	"time"
)

// Dialer is the "make a fresh outbound connection" step, abstracted so
// the accept/relay logic can be unit-tested without root or a real
// TPROXY-bound socket - a plain net.Dialer{} is used in production; a
// fake pointing at an in-process listener is used in tests.
type Dialer interface {
	Dial(network, address string) (net.Conn, error)
}

type netDialer struct {
	timeout time.Duration
}

func (d netDialer) Dial(network, address string) (net.Conn, error) {
	return net.DialTimeout(network, address, d.timeout)
}

func NewNetDialer(timeout time.Duration) Dialer {
	return netDialer{timeout: timeout}
}

type Proxy struct {
	dialer Dialer
	logger *log.Logger
}

func New(dialer Dialer, logger *log.Logger) *Proxy {
	return &Proxy{dialer: dialer, logger: logger}
}

// Serve accepts connections from l forever, relaying each to its
// original destination (as reported by the accepted conn's own
// LocalAddr() under TPROXY). Blocks until the listener is closed.
func (p *Proxy) Serve(l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}
		go p.handle(conn)
	}
}

func (p *Proxy) handle(client net.Conn) {
	defer client.Close()

	// Under TPROXY, the accepted connection's local address IS the
	// original destination the client was trying to reach - this is
	// the whole mechanism, not a bug or a placeholder.
	origDest := client.LocalAddr().String()

	upstream, err := p.dialer.Dial("tcp", origDest)
	if err != nil {
		p.logf("dial %s failed: %v", origDest, err)
		return
	}
	defer upstream.Close()

	done := make(chan struct{}, 2)
	go func() {
		io.Copy(upstream, client)
		if tc, ok := upstream.(interface{ CloseWrite() error }); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		io.Copy(client, upstream)
		if tc, ok := client.(interface{ CloseWrite() error }); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	<-done
}

func (p *Proxy) logf(format string, args ...any) {
	if p.logger != nil {
		p.logger.Printf(format, args...)
	}
}
