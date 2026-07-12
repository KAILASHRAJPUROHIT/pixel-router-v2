//go:build linux

// IP_TRANSPARENT/TPROXY is a Linux-only kernel feature - this file is
// built only for GOOS=linux (i.e. when actually cross-compiling for
// the phone), so relay.go's portable logic can still be unit-tested
// directly on Windows via `go test` without this file even attempting
// to compile there.
package ttlproxy

import (
	"context"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// ListenTransparent binds a TCP listener with IP_TRANSPARENT (IPv4) or
// IPV6_TRANSPARENT (IPv6) set, which is what lets it accept connections
// for destination addresses that aren't actually its own - the
// prerequisite for TPROXY redirection to work at all. Requires
// CAP_NET_ADMIN (root, same as V1's ttlproxy.c requirement).
func ListenTransparent(network, address string) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: func(fam, addr string, c syscall.RawConn) error {
			var sockErr error
			err := c.Control(func(fd uintptr) {
				if isIPv6Network(fam) {
					sockErr = unix.SetsockoptInt(int(fd), unix.SOL_IPV6, unix.IPV6_TRANSPARENT, 1)
				} else {
					sockErr = unix.SetsockoptInt(int(fd), unix.SOL_IP, unix.IP_TRANSPARENT, 1)
				}
				if sockErr == nil {
					unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
				}
			})
			if err != nil {
				return err
			}
			return sockErr
		},
	}
	return lc.Listen(context.Background(), network, address)
}

func isIPv6Network(network string) bool {
	return network == "tcp6"
}
