// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package x11

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jezek/xgb"
)

// Close owns the transport, so cancellation never waits for an X11 reply.
func (c *connection) Close() { _ = c.transport.Close() }

func connectDisplay(ctx context.Context, display string) (*connection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	network, address, host, number, screen, err := displayAddress(display)
	if err != nil {
		return nil, err
	}
	var dialer net.Dialer
	transport, err := dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, fmt.Errorf("dial display: %w", err)
	}
	owned := &connection{Conn: nil, transport: transport}
	stop := context.AfterFunc(ctx, owned.Close)
	defer stop()
	cookie, err := authorityCookie(host, strconv.Itoa(number))
	if err != nil {
		owned.Close()
		return nil, err
	}
	// Upstream's explicit-cookie constructor avoids its implicit display lookup.
	// With no authority record, remove authentication from the initial request.
	wire := transport
	if cookie == "" {
		wire = &unauthenticatedConn{Conn: transport, started: false}
		cookie = strings.Repeat("0", 32)
	}
	owned.Conn, err = xgb.NewConnNetWithCookieHex(wire, cookie)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		owned.Close()
		return nil, fmt.Errorf("setup display: %w", err)
	}
	owned.DisplayNumber, owned.DefaultScreen = number, screen
	return owned, nil
}

// Write strips the explicit dummy cookie only from the setup request.
func (c *unauthenticatedConn) Write(data []byte) (int, error) {
	if c.started {
		return c.Conn.Write(data)
	}
	c.started = true
	setup := append([]byte(nil), data[:12]...)
	clear(setup[6:10])
	n, err := c.Conn.Write(setup)
	if err != nil {
		return 0, err
	}
	if n != len(setup) {
		return 0, io.ErrShortWrite
	}
	return len(data), nil
}

func displayAddress(display string) (network, address, host string, number, screen int, err error) {
	colon := strings.LastIndexByte(display, ':')
	if colon < 0 {
		err = fmt.Errorf("invalid display: %s", display)
		return
	}
	prefix, suffix := display[:colon], display[colon+1:]
	digits, scr, _ := strings.Cut(suffix, ".")
	number, err = strconv.Atoi(digits)
	if err != nil || number < 0 {
		err = fmt.Errorf("invalid display: %s", display)
		return
	}
	if scr != "" {
		screen, err = strconv.Atoi(scr)
		if err != nil || screen < 0 {
			err = fmt.Errorf("invalid screen: %s", display)
			return
		}
	}
	network = "unix"
	if strings.HasPrefix(prefix, "/") {
		address = prefix + ":" + digits
		return
	}
	host = prefix
	if protocol, hostname, ok := strings.Cut(prefix, "/"); ok {
		network, host = protocol, hostname
	}
	if host == "" || host == "unix" {
		host = ""
		network = "unix"
		address = "/tmp/.X11-unix/X" + digits
		return
	}
	if network == "unix" {
		network = "tcp"
	}
	address = net.JoinHostPort(host, strconv.Itoa(6000+number))
	return
}

// authorityCookie selects the same local/wildcard records as upstream XGB,
// while explicitly matching this connection's display number.
func authorityCookie(host, display string) (string, error) {
	if host == "" || host == "localhost" {
		var err error
		host, err = os.Hostname()
		if err != nil {
			return "", nil
		}
	}
	path := os.Getenv("XAUTHORITY")
	if path == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", nil
		}
		path = filepath.Join(home, ".Xauthority")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", nil
	}
	defer file.Close()
	for {
		var family uint16
		if err = binary.Read(file, binary.BigEndian, &family); err != nil {
			return "", nil
		}
		var fields [4][]byte
		for i := range fields {
			fields[i], err = authorityField(file)
			if err != nil {
				return "", nil
			}
		}
		if family != 65535 && (family != 256 || string(fields[0]) != host) {
			continue
		}
		if string(fields[1]) != "" && string(fields[1]) != display {
			continue
		}
		if string(fields[2]) != "MIT-MAGIC-COOKIE-1" || len(fields[3]) != 16 {
			return "", fmt.Errorf("unsupported X11 authority protocol: %s", fields[2])
		}
		return hex.EncodeToString(fields[3]), nil
	}
}

func authorityField(reader io.Reader) ([]byte, error) {
	var length uint16
	if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
		return nil, fmt.Errorf("read authority length: %w", err)
	}
	value := make([]byte, int(length))
	if _, err := io.ReadFull(reader, value); err != nil {
		return nil, fmt.Errorf("read authority value: %w", err)
	}
	return value, nil
}
