// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package x11

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gostafa/linuxdesktop/internal/domain"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

func serverConnection(t *testing.T, mode string) *connection {
	t.Helper()
	client, server := net.Pipe()
	go func() { defer server.Close(); serve(server, mode) }()
	conn, err := xgb.NewConnNetWithCookieHex(client, "00000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	owned := &connection{Conn: conn, transport: client}
	t.Cleanup(owned.Close)
	return owned
}

func serve(conn net.Conn, mode string) {
	header := make([]byte, 12)
	if _, err := io.ReadFull(conn, header); err != nil {
		return
	}
	auth := make([]byte, xgb.Pad(int(xgb.Get16(header[6:])))+xgb.Pad(int(xgb.Get16(header[8:]))))
	if _, err := io.ReadFull(conn, auth); err != nil {
		return
	}
	setup := xproto.SetupInfo{
		Status:               1,
		ProtocolMajorVersion: 11,
		ResourceIdBase:       0x20000000,
		ResourceIdMask:       0x1fffff,
		VendorLen:            7,
		Vendor:               "fixture",
		MaximumRequestLength: 65535,
		RootsLen:             1,
		Roots:                []xproto.ScreenInfo{{Root: 1}},
	}
	data := setup.Bytes()
	xgb.Put16(data[6:], uint16((len(data)-8)/4))
	if _, err := conn.Write(data); err != nil {
		return
	}
	var sequence uint16
	for {
		request := make([]byte, 4)
		if _, err := io.ReadFull(conn, request); err != nil {
			return
		}
		rest := make([]byte, int(xgb.Get16(request[2:]))*4-4)
		if _, err := io.ReadFull(conn, rest); err != nil {
			return
		}
		request = append(request, rest...)
		sequence++
		reply := make([]byte, 32)
		reply[0] = 1
		xgb.Put16(reply[2:], sequence)
		switch request[0] {
		case 99:
			reply[1] = 1
			reply = append(reply, 8)
			reply = append(reply, []byte("XWAYLAND")...)
		case 16:
			name := string(request[8 : 8+int(xgb.Get16(request[4:]))])
			atom := uint32(4)
			if name == atomSupportingWMCheck {
				atom = 2
			}
			if name == atomNetWMName {
				atom = 3
			}
			if mode == "no-wm" {
				atom = 0
			}
			xgb.Put32(reply[8:], atom)
		case 20:
			property := xgb.Get32(request[8:])
			typ := xgb.Get32(request[12:])
			if mode == "error" {
				reply[0] = 0
				reply[1] = 3
				reply[10] = 20
				break
			}
			xgb.Put32(reply[8:], typ)
			if property == 2 {
				reply[1] = 32
				xgb.Put32(reply[16:], 1)
				value := make([]byte, 4)
				xgb.Put32(value, 42)
				reply = append(reply, value...)
			} else {
				reply[1] = 8
				name := "Fixture WM\x00ignored"
				if mode == "legacy" && property == 3 {
					name = ""
				}
				xgb.Put32(reply[16:], uint32(len(name)))
				reply = append(reply, []byte(name)...)
			}
		}
		for len(reply)%4 != 0 {
			reply = append(reply, 0)
		}
		xgb.Put32(reply[4:], uint32((len(reply)-32)/4))
		if _, err := conn.Write(reply); err != nil {
			return
		}
	}
}

func TestDisplayProtocol(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"name", "legacy", "no-wm", "error"} {
		t.Run(mode, func(t *testing.T) {
			conn := serverConnection(t, mode)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			info, err := queryDisplay(
				ctx,
				&domain.Env{Display: ":fixture"},
				func(string) (*connection, error) { return conn, nil },
			)
			if err != nil || info.Vendor != "fixture" || info.ProtocolMajor != 11 ||
				len(info.Extensions) != 1 ||
				info.Extensions[0] != "XWAYLAND" {
				t.Fatal(info, err)
			}
			want := "Fixture WM"
			if mode == "no-wm" || mode == "error" {
				want = ""
			}
			if info.WindowManager != want {
				t.Fatal(info)
			}
		})
	}
}

func TestAvailabilityAndMissingProperties(t *testing.T) {
	t.Parallel()
	if Available(&domain.Env{}) || Available(&domain.Env{Display: "invalid"}) ||
		!Available(&domain.Env{Display: "remote:1.2"}) ||
		Available(&domain.Env{Display: "unix:987654"}) {
		t.Fatal("display availability")
	}
	if _, err := New().X11(t.Context(), &domain.Env{}); err != nil {
		t.Fatal(err)
	}
	if _, err := New().X11(t.Context(), &domain.Env{Display: "invalid"}); err == nil {
		t.Fatal("invalid display accepted")
	}
	conn := new(xgb.Conn)
	if atomProperty(atomSet{}) != (property{}) {
		t.Fatal("missing atoms produced a property")
	}
	if _, ok := rootWindow(conn, &xproto.SetupInfo{}); ok {
		t.Fatal("missing root")
	}
	if checkWindow(conn, &xproto.SetupInfo{}, 0) != 0 || textProperty(conn, 0, property{}) != "" {
		t.Fatal("missing properties")
	}
	if window(nil, nil) != 0 || window(&xproto.GetPropertyReply{Value: []byte{1}}, nil) != 0 ||
		window(nil, errors.New("failed")) != 0 {
		t.Fatal("invalid window")
	}
	if firstString([]byte("plain")) != "plain" || firstString([]byte("nul\x00tail")) != "nul" {
		t.Fatal("string property")
	}
	bad := conn.NewCookie(true, false)
	if atomOf(xproto.InternAtomCookie{Cookie: bad}) != 0 ||
		extensions(xproto.ListExtensionsCookie{Cookie: bad}) != nil {
		t.Fatal("failed cookie")
	}
}

func TestCancellationWatcher(t *testing.T) {
	t.Parallel()
	conn := serverConnection(t, "name")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	watch(ctx, conn, make(chan struct{}))
	stop := make(chan struct{})
	close(stop)
	watch(t.Context(), conn, stop)
	errorConn := serverConnection(t, "error")
	if textProperty(errorConn.Conn, 42, legacyProperty()) != "" {
		t.Fatal("failed property returned text")
	}
}

func TestOversizedAtom(t *testing.T) {
	t.Parallel()
	conn := serverConnection(t, "name")
	name := strings.Repeat("a", math.MaxUint16+1)
	if atomOf(internAtom(conn.Conn, name)) != zero {
		t.Fatal("oversized atom was sent to the server")
	}
}
