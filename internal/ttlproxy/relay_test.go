package ttlproxy

import (
	"bufio"
	"net"
	"testing"
	"time"
)

// fakeTPROXYConn wraps a real net.Conn but overrides LocalAddr() to
// report an arbitrary address - simulating what a real TPROXY-bound
// socket reports (the client's original destination), without needing
// root/CAP_NET_ADMIN/actual iptables TPROXY rules to test the relay
// logic that consumes it.
type fakeTPROXYConn struct {
	net.Conn
	fakeLocal net.Addr
}

func (f fakeTPROXYConn) LocalAddr() net.Addr { return f.fakeLocal }

func startEchoServer(t *testing.T) net.Addr {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						c.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}(c)
		}
	}()
	return ln.Addr()
}

// TestProxyRelaysToOriginalDestination proves the actual mechanism
// this package depends on: handle() dials whatever address the
// accepted connection's LocalAddr() reports (which, under real TPROXY,
// is the client's original destination - not the proxy's own listening
// address) and relays bytes bidirectionally.
func TestProxyRelaysToOriginalDestination(t *testing.T) {
	echoAddr := startEchoServer(t)

	// A plain TCP pair to stand in for "the tethered client's
	// connection" - what matters for this test is only that handle()
	// treats its LocalAddr() as the dial target, not how the conn
	// itself was established.
	clientListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer clientListener.Close()

	var serverSide net.Conn
	accepted := make(chan struct{})
	go func() {
		serverSide, _ = clientListener.Accept()
		close(accepted)
	}()
	clientSide, err := net.Dial("tcp", clientListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer clientSide.Close()
	<-accepted
	defer serverSide.Close()

	fakeConn := fakeTPROXYConn{Conn: serverSide, fakeLocal: echoAddr}

	proxy := New(NewNetDialer(2*time.Second), nil)
	go proxy.handle(fakeConn)

	clientSide.SetDeadline(time.Now().Add(3 * time.Second))
	msg := "hello through the proxy\n"
	if _, err := clientSide.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}

	reader := bufio.NewReader(clientSide)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if line != msg {
		t.Fatalf("expected echo of %q, got %q", msg, line)
	}
}

// fakeDialer lets a test assert exactly which address handle() tried
// to dial, without needing a real listener there at all.
type fakeDialer struct {
	dialedAddress string
	conn          net.Conn
	err           error
}

func (f *fakeDialer) Dial(network, address string) (net.Conn, error) {
	f.dialedAddress = address
	return f.conn, f.err
}

func TestHandleDialsTheReportedLocalAddr(t *testing.T) {
	echoAddr := startEchoServer(t)

	clientListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer clientListener.Close()

	var serverSide net.Conn
	accepted := make(chan struct{})
	go func() {
		serverSide, _ = clientListener.Accept()
		close(accepted)
	}()
	clientSide, err := net.Dial("tcp", clientListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer clientSide.Close()
	<-accepted
	defer serverSide.Close()

	realUpstream, err := net.Dial("tcp", echoAddr.String())
	if err != nil {
		t.Fatal(err)
	}
	defer realUpstream.Close()

	fd := &fakeDialer{conn: realUpstream}
	fakeConn := fakeTPROXYConn{Conn: serverSide, fakeLocal: echoAddr}

	proxy := New(fd, nil)
	handleDone := make(chan struct{})
	go func() {
		proxy.handle(fakeConn)
		close(handleDone)
	}()

	// handle() only cares which address got dialed for this test - closing
	// both ends of the "client" pipe immediately lets io.Copy see EOF on
	// both legs so handle() returns, instead of blocking forever waiting
	// for data neither side ever sends.
	clientSide.Close()

	select {
	case <-handleDone:
	case <-time.After(2 * time.Second):
		t.Fatal("handle() did not return after connections closed")
	}

	if fd.dialedAddress != echoAddr.String() {
		t.Fatalf("expected dial to %s (the reported original destination), got %s", echoAddr, fd.dialedAddress)
	}
}
