// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package x11

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDisplayAddresses(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		display string
		want    displayTarget
	}{
		{":0", displayTarget{network: "unix", address: "/tmp/.X11-unix/X0"}},
		{"unix:2.1", displayTarget{network: "unix", address: "/tmp/.X11-unix/X2", number: 2, screen: 1}},
		{"unix/:3.", displayTarget{network: "unix", address: "/tmp/.X11-unix/X3", number: 3}},
		{"server:4", displayTarget{network: "tcp", address: "server:6004", host: "server", number: 4}},
		{"tcp/server:05.2", displayTarget{network: "tcp", address: "server:6005", host: "server", number: 5, screen: 2}},
		{"tcp6/::1:6", displayTarget{network: "tcp6", address: "[::1]:6006", host: "::1", number: 6}},
		{"/tmp/custom:7.1", displayTarget{network: "unix", address: "/tmp/custom:7", number: 7, screen: 1}},
	} {
		t.Run(test.display, func(t *testing.T) {
			t.Parallel()
			got, err := displayAddress(test.display)
			if err != nil || !reflect.DeepEqual(*got, test.want) {
				t.Fatalf("got %+v, %v; want %+v", got, err, test.want)
			}
		})
	}
	for _, display := range []string{"missing", ":", ":-1", ":abc", ":0.-1", ":0.bad", ":0.1.2"} {
		t.Run(display, func(t *testing.T) {
			t.Parallel()
			if _, err := displayAddress(display); err == nil {
				t.Fatal("accepted malformed display")
			}
		})
	}
}

type writeConn struct {
	net.Conn
	data   []byte
	count  int
	err    error
	closed bool
}

func (conn *writeConn) Write(data []byte) (int, error) {
	conn.data = append(conn.data, data...)
	if conn.count >= 0 {
		return conn.count, conn.err
	}
	return len(data), conn.err
}

func (conn *writeConn) Close() error { conn.closed = true; return conn.err }

func TestUnauthenticatedWrites(t *testing.T) {
	t.Parallel()
	request := bytes.Repeat([]byte{7}, 48)
	transport := &writeConn{count: -1}
	conn := &unauthenticatedConn{Conn: transport}
	count, err := conn.Write(request)
	if err != nil || count != len(request) {
		t.Fatal(count, err)
	}
	if len(transport.data) != 12 || !bytes.Equal(transport.data[6:10], make([]byte, 4)) {
		t.Fatal(transport.data)
	}
	if !bytes.Equal(request, bytes.Repeat([]byte{7}, 48)) {
		t.Fatal("request modified")
	}
	count, err = conn.Write([]byte{1, 2, 3})
	if err != nil || count != 3 || !bytes.Equal(transport.data[12:], []byte{1, 2, 3}) {
		t.Fatal(count, err, transport.data)
	}
	for _, test := range []struct {
		name   string
		length int
		count  int
		err    error
		want   error
	}{
		{"truncated", 3, -1, nil, io.ErrUnexpectedEOF},
		{"short", 48, 5, nil, io.ErrShortWrite},
		{"failed", 48, 0, net.ErrClosed, net.ErrClosed},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			wire := &writeConn{count: test.count, err: test.err}
			adapter := &unauthenticatedConn{Conn: wire}
			if n, err := adapter.Write(
				request[:test.length],
			); n != 0 ||
				!errors.Is(err, test.want) {
				t.Fatal(n, err)
			}
		})
	}
	transport.err, transport.count = net.ErrClosed, 2
	if count, err := conn.Write(request); count != 2 || !errors.Is(err, net.ErrClosed) {
		t.Fatal(count, err)
	}
	owned := newConnection(transport)
	owned.Close()
	if !transport.closed {
		t.Fatal("transport not closed")
	}
}

func authorityBytes(t *testing.T, family uint16, fields ...[]byte) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := binary.Write(&data, binary.BigEndian, family); err != nil {
		t.Fatal(err)
	}
	for _, field := range fields {
		if err := binary.Write(&data, binary.BigEndian, uint16(len(field))); err != nil {
			t.Fatal(err)
		}
		data.Write(field)
	}
	return data.Bytes()
}

func TestAuthoritySelection(t *testing.T) {
	t.Parallel()
	cookie := bytes.Repeat([]byte{7}, 16)
	for _, test := range []struct {
		name                    string
		family                  uint16
		host, display, protocol string
		cookie                  []byte
		want                    string
		wantErr                 error
	}{
		{"local", 256, "host", "2", cookieProtocol, cookie, hex.EncodeToString(cookie), nil},
		{"wildcard", 65535, "other", "", cookieProtocol, cookie, hex.EncodeToString(cookie), nil},
		{"wrong family", 0, "host", "2", cookieProtocol, cookie, "", nil},
		{"wrong host", 256, "other", "2", cookieProtocol, cookie, "", nil},
		{"wrong display", 256, "host", "3", cookieProtocol, cookie, "", nil},
		{"wrong protocol", 256, "host", "2", "unknown", cookie, "", errAuthorityProtocol},
		{"wrong cookie length", 256, "host", "2", cookieProtocol, []byte{7}, "", errAuthorityProtocol},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			data := authorityBytes(
				t,
				test.family,
				[]byte(test.host),
				[]byte(test.display),
				[]byte(test.protocol),
				test.cookie,
			)
			got, err := findAuthority(bytes.NewReader(data), "host", "2")
			if got != test.want || !errors.Is(err, test.wantErr) {
				t.Fatal(got, err)
			}
		})
	}
	data := authorityBytes(t, 256, []byte("host"), []byte("2"), []byte(cookieProtocol), cookie)
	for length := range len(data) {
		got, err := findAuthority(bytes.NewReader(data[:length]), "host", "2")
		if got != "" || err != nil {
			t.Fatal(length, got, err)
		}
	}
}

type failedReader struct{}

func (failedReader) Read([]byte) (int, error) { return 0, net.ErrClosed }

func TestAuthorityReadFailures(t *testing.T) {
	t.Parallel()
	reader := io.MultiReader(bytes.NewReader([]byte{0, 1}), failedReader{})
	if _, err := authorityField(reader); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	if _, err := readAuthorityFile(t.TempDir()); err == nil {
		t.Fatal("read directory succeeded")
	}
	if host := authorityHost("", func() (string, error) { return "", net.ErrClosed }); host != "" {
		t.Fatal(host)
	}
	if host := authorityHost(
		"localhost",
		func() (string, error) { return "host", nil },
	); host != "host" {
		t.Fatal(host)
	}
	if cookie, err := authorityCookieForHost("", 0); cookie != "" || err != nil {
		t.Fatal(cookie, err)
	}
}

func TestAuthorityPathsAndSetupFailure(t *testing.T) {
	t.Setenv("XAUTHORITY", "")
	t.Setenv("HOME", "")
	if path := authorityPath(); path != "" {
		t.Fatal(path)
	}
	if cookie, err := authorityCookie("remote", 0); cookie != "" || err != nil {
		t.Fatal(cookie, err)
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if path := authorityPath(); path != filepath.Join(dir, ".Xauthority") {
		t.Fatal(path)
	}
	path := filepath.Join(dir, "authority")
	data := authorityBytes(t, 65535, nil, nil, []byte("unsupported"), bytes.Repeat([]byte{7}, 16))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XAUTHORITY", path)
	if cookie, err := authorityCookie(
		"remote",
		0,
	); cookie != "" ||
		!errors.Is(err, errAuthorityProtocol) {
		t.Fatal(cookie, err)
	}
	transport := &writeConn{count: -1}
	if conn, err := setupDisplay(
		t.Context(),
		transport,
		&displayTarget{host: "remote"},
	); conn != nil ||
		!errors.Is(err, errAuthorityProtocol) {
		t.Fatal(conn, err)
	}
	if !transport.closed {
		t.Fatal("failed setup left transport open")
	}
}

func TestDisplayDialFailure(t *testing.T) {
	t.Parallel()
	if conn, err := connectDisplay(
		t.Context(),
		filepath.Join(t.TempDir(), "missing")+":0",
	); conn != nil ||
		err == nil {
		t.Fatal(conn, err)
	}
}

func TestAuthoritySymlink(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "authority")
	want := []byte("authority data")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	got, err := readAuthorityFile(link)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal(got, err)
	}
	// A dangling link is an optional, missing authority file.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if data := optionalAuthorityFile(link); data != nil {
		t.Fatal(data)
	}
	// A socket exists and resolves, but cannot be opened as an authority file.
	socket := filepath.Join(t.TempDir(), "socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	if data := optionalAuthorityFile(socket); data != nil {
		t.Fatal(data)
	}
}
