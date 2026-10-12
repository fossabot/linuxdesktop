package wayland

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gostafa/linuxdesktop/internal/domain"
)

func TestRegistryCancellation(t *testing.T) {
	t.Parallel()
	for _, deadline := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "no deadline", true: "with deadline"}[deadline],
			func(t *testing.T) {
				client, server := net.Pipe()
				defer client.Close()
				defer server.Close()
				ctx, cancel := context.WithCancel(t.Context())
				if deadline {
					var stop context.CancelFunc
					ctx, stop = context.WithTimeout(ctx, time.Hour)
					defer stop()
				}
				defer cancel()
				finished := make(chan error, 1)
				go func() { _, err := converse(ctx, client); finished <- err }()
				if _, err := io.ReadFull(server, make([]byte, handshakeSize)); err != nil {
					t.Fatal(err)
				}
				cancel()
				select {
				case err := <-finished:
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("registry read ignored cancellation")
				}
			},
		)
	}
}

func TestAlreadyCancelledRegistry(t *testing.T) {
	t.Parallel()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if globals, err := converse(
		ctx,
		client,
	); len(globals) != 0 ||
		!errors.Is(err, context.Canceled) {
		t.Fatal(globals, err)
	}
}

func TestPartialRegistrySurvivesFailure(t *testing.T) {
	t.Parallel()
	for _, protocolError := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "EOF", true: "protocol error"}[protocolError],
			func(t *testing.T) {
				dir, err := os.MkdirTemp("/tmp", "wl-test-")
				if err != nil {
					t.Fatal(err)
				}
				defer os.RemoveAll(dir)
				listener, err := net.Listen("unix", filepath.Join(dir, defaultDisplay))
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				serverDone := make(chan error, 1)
				go func() {
					conn, err := listener.Accept()
					if err != nil {
						serverDone <- err
						return
					}
					defer conn.Close()
					_, err = io.ReadFull(conn, make([]byte, handshakeSize))
					if err == nil {
						_, err = conn.Write(frame(objRegistry, 0, globalBody("wl_compositor")))
					}
					if err == nil && protocolError {
						_, err = conn.Write(frame(1, 0, nil))
					}
					serverDone <- err
				}()
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				info, err := New().Wayland(ctx, &domain.Env{RuntimeDir: dir})
				if err == nil || info == nil || len(info.Globals) != 1 {
					t.Fatalf("partial registry lost: %+v %v", info, err)
				}
				if protocolError && !errors.Is(err, ErrProtocol) {
					t.Fatal(err)
				}
				if err := <-serverDone; err != nil {
					t.Fatal(err)
				}
			},
		)
	}
}
