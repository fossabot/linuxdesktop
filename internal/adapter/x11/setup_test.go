package x11

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gostafa/linuxdesktop/internal/domain"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

func tcpDisplay(t *testing.T) (net.Listener, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener, fmt.Sprintf("127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port-6000)
}

func readSetupRequest(conn net.Conn) ([]byte, error) {
	header := make([]byte, 12)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	nameLength, dataLength := int(xgb.Get16(header[6:])), int(xgb.Get16(header[8:]))
	auth := make([]byte, xgb.Pad(nameLength)+xgb.Pad(dataLength))
	_, err := io.ReadFull(conn, auth)
	return auth[xgb.Pad(nameLength) : xgb.Pad(nameLength)+dataLength], err
}

func sendSetupReply(conn net.Conn) error {
	setup := xproto.SetupInfo{
		Status:               1,
		ProtocolMajorVersion: 11,
		ResourceIdBase:       0x20000000,
		ResourceIdMask:       0x1fffff,
		MaximumRequestLength: 65535,
		RootsLen:             2,
		Roots:                []xproto.ScreenInfo{{Root: 1}, {Root: 2}},
	}
	data := setup.Bytes()
	xgb.Put16(data[6:], uint16((len(data)-8)/4))
	_, err := conn.Write(data)
	return err
}

func TestSetupHonorsCancellationAndDeadline(t *testing.T) {
	t.Parallel()
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint(deadline), func(t *testing.T) {
			listener, display := tcpDisplay(t)
			accepted := make(chan net.Conn, 1)
			go func() {
				conn, err := listener.Accept()
				if err == nil {
					accepted <- conn
				}
			}()
			ctx, cancel := context.WithCancel(t.Context())
			want := error(context.Canceled)
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
				want = context.DeadlineExceeded
			}
			defer cancel()
			finished := make(chan error, 1)
			go func() { _, err := connectDisplay(ctx, display); finished <- err }()
			var server net.Conn
			select {
			case server = <-accepted:
			case <-time.After(time.Second):
				t.Fatal("connection not established")
			}
			defer server.Close()
			_ = server.SetDeadline(time.Now().Add(time.Second))
			if _, err := readSetupRequest(server); err != nil {
				t.Fatal(err)
			}
			if !deadline {
				cancel()
			}
			select {
			case err := <-finished:
				if !errors.Is(err, want) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("setup ignored context")
			}
			if _, err := server.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
				t.Fatalf("failed setup socket not closed: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := connectDisplay(
		ctx,
		"unreachable:0",
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatal(err)
	}
}

func TestSetupPreservesAuthorityAndScreen(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		t.Run(fmt.Sprint(authenticated), func(t *testing.T) {
			listener, display := tcpDisplay(t)
			_, number, _ := splitDisplay(display)
			cookie := bytes.Repeat([]byte{7}, 16)
			var authority bytes.Buffer
			if authenticated {
				for _, record := range []struct {
					display string
					cookie  []byte
				}{{"99999", bytes.Repeat([]byte{9}, 16)}, {number, cookie}} {
					_ = binary.Write(&authority, binary.BigEndian, uint16(65535))
					for _, value := range [][]byte{nil, []byte(record.display), []byte("MIT-MAGIC-COOKIE-1"), record.cookie} {
						_ = binary.Write(&authority, binary.BigEndian, uint16(len(value)))
						authority.Write(value)
					}
				}
			}
			path := filepath.Join(t.TempDir(), "authority")
			if err := os.WriteFile(path, authority.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("XAUTHORITY", path)
			seen := make(chan []byte, 1)
			done := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				auth, err := readSetupRequest(conn)
				seen <- auth
				if err == nil {
					err = sendSetupReply(conn)
				}
				if err == nil {
					_, err = io.Copy(io.Discard, conn)
				}
				done <- err
			}()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			conn, err := connectDisplay(ctx, display+".1")
			if err != nil {
				t.Fatal(err)
			}
			if conn.DefaultScreen != 1 || conn.DisplayNumber == 0 {
				t.Fatal(conn.DefaultScreen, conn.DisplayNumber)
			}
			conn.Close()
			auth := <-seen
			if authenticated && !bytes.Equal(auth, cookie) || !authenticated && len(auth) != 0 {
				t.Fatalf("incorrect authority selection: %x", auth)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMetadataCancellationPreservesSetup(t *testing.T) {
	t.Parallel()
	listener, display := tcpDisplay(t)
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_, err = readSetupRequest(conn)
		if err == nil {
			err = sendSetupReply(conn)
		}
		if err == nil {
			_, err = io.ReadFull(conn, make([]byte, 4))
		}
		close(ready)
		if err == nil {
			_, _ = io.Copy(io.Discard, conn)
		}
		done <- err
	}()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type result struct {
		info *domain.X11Info
		err  error
	}
	finished := make(chan result, 1)
	go func() { info, err := New().X11(ctx, &domain.Env{Display: display}); finished <- result{info, err} }()
	<-ready
	// Wait until the server has completed setup before cancelling metadata queries.
	cancel()
	select {
	case out := <-finished:
		if !errors.Is(out.err, context.Canceled) {
			t.Fatal(out.err)
		}
		if out.info == nil || out.info.ProtocolMajor != 11 {
			t.Fatal("completed setup lost", out.info)
		}
	case <-time.After(time.Second):
		t.Fatal("metadata ignored cancellation")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLocalDisplaySetup(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("/tmp", "x11-setup-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	display := filepath.Join(dir, "display") + ":3"
	listener, err := net.Listen("unix", display)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_, err = readSetupRequest(conn)
		if err == nil {
			err = sendSetupReply(conn)
		}
		if err == nil {
			_, _ = io.Copy(io.Discard, conn)
		}
		done <- err
	}()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	conn, err := connectDisplay(ctx, display+".1")
	if err != nil {
		t.Fatal(err)
	}
	if conn.DisplayNumber != 3 || conn.DefaultScreen != 1 {
		t.Fatal(conn.DisplayNumber, conn.DefaultScreen)
	}
	conn.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
