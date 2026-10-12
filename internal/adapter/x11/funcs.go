// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package x11

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/gostafa/linuxdesktop/internal/domain"
	"github.com/gostafa/linuxdesktop/internal/probe"
	"github.com/gostafa/linuxdesktop/internal/sysfs"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// New returns an X11 probe.
func New() Probe { return detectX11 }

// Available is a cheap test for an X server, doing no more than a stat. A
// display on a remote host is reported as available without verification,
// since confirming it would mean connecting. Callers that need certainty
// should run the probe, which connects.
func Available(env *domain.Env) bool {
	host, number, ok := splitDisplay(env.Display)
	if !ok {
		return false
	}

	if remote(host) {
		return true
	}

	return number != noValue && sysfs.IsSocket(unixSocketDir+socketPrefix+number)
}

// X11 connects to $DISPLAY and reports what the server says about itself. A
// nil result with a nil error means there was no X server to talk to, which on
// a pure Wayland or headless session is the expected outcome.
//
//nolint:wrapcheck // queryValues supplies the query diagnostic while preserving partial metadata.
func detectX11(ctx context.Context, env *domain.Env) (*domain.X11Info, error) {
	result, err := queryDisplay(ctx, env, func(display string) (*connection, error) {
		return connectDisplay(ctx, display)
	})

	return queryValues(result, err)
}

// queryValues preserves metadata collected before a query failed.
func queryValues[T any](info T, err error) (T, error) {
	if err != nil {
		return info, fmt.Errorf("x11: query display: %w", err)
	}

	return info, nil
}

//nolint:wrapcheck // queryValues adds the query operation to cancellation errors.
func queryDisplay(
	ctx context.Context,
	env *domain.Env,
	connect func(string) (*connection, error),
) (*domain.X11Info, error) {
	if env.Display == noValue {
		var absent *domain.X11Info

		return absent, nil
	}

	conn, err := connect(env.Display)
	if err != nil {
		return nil, fmt.Errorf("x11: connect display: %w", err)
	}

	info := queryConnection(ctx, conn, env.Display)

	return queryValues(info, probe.ContextError(ctx))
}

func queryConnection(ctx context.Context, conn *connection, display string) *domain.X11Info {
	defer conn.Close()

	// xgb's Reply blocks indefinitely, so cancellation is expressed by closing
	// the connection out from under it.
	stop := watchConnection(ctx, conn)
	defer close(stop)

	return describe(conn.Conn, display)
}

// resolve reads the three atoms back off the wire. Any the server does not know
// comes back zero, which the property lookup treats as absent.
func resolve(cookies atomCookies) atomSet {
	return atomSet{
		check: atomOf(cookies.check),
		name:  atomOf(cookies.name),
		utf8:  atomOf(cookies.utf8),
	}
}

// property names _NET_WM_NAME as a UTF8_STRING, or nothing at all when the
// server knows neither atom.
func atomProperty(set atomSet) property {
	var empty property

	if set.name == zero || set.utf8 == zero {
		return empty
	}

	return property{set.name, set.utf8}
}

// splitDisplay takes $DISPLAY apart into its host and its display number, with
// the optional screen suffix dropped.
func splitDisplay(display string) (host, number string, ok bool) {
	before, after, cut := strings.Cut(display, displaySeparator)
	if !cut {
		return noValue, noValue, false
	}

	return before, beforeScreen(after), true
}

// beforeScreen drops the optional screen suffix, so "0.1" and "0" both yield
// "0".
func beforeScreen(number string) string {
	for i := range len(number) {
		if number[i] == screenByte {
			return number[:i]
		}
	}

	return number
}

// remote reports whether $DISPLAY names a host other than this one. Such a
// display is assumed reachable, since confirming it would mean connecting.
func remote(host string) bool {
	return host != noValue && host != localHost
}

// watch closes the connection when the caller gives up, which is the only way
// to interrupt a blocked xgb reply.
func watch(ctx context.Context, conn *connection, stop <-chan struct{}) {
	select {
	case <-ctx.Done():
		conn.Close()
	case <-stop:
	}
}

// describe asks the server everything this library wants to know, sending every
// independent request before any reply is read so the whole exchange costs one
// round trip.
func describe(conn *xgb.Conn, display string) *domain.X11Info {
	setup := xproto.Setup(conn)
	extCookie := xproto.ListExtensions(conn)
	atoms := internAtoms(conn)

	info := &domain.X11Info{
		Display:       display,
		Screen:        conn.DefaultScreen,
		Vendor:        setup.Vendor,
		ProtocolMajor: int(setup.ProtocolMajorVersion),
		ProtocolMinor: int(setup.ProtocolMinorVersion),
		Extensions:    extensions(extCookie),
		WindowManager: "",
	}

	info.WindowManager = wmName(conn, setup, resolve(atoms))

	return info
}

// wmName walks the EWMH _NET_SUPPORTING_WM_CHECK chain: the root window points
// at a window owned by the window manager, and that window carries the
// manager's name.
func wmName(conn *xgb.Conn, setup *xproto.SetupInfo, found atomSet) string {
	owner := checkWindow(conn, setup, found.check)
	if owner == zero {
		return noValue
	}

	name := textProperty(conn, owner, atomProperty(found))
	if name != noValue {
		return name
	}

	// Window managers that predate EWMH only set WM_NAME.
	return textProperty(conn, owner, legacyProperty())
}

// checkWindow reads _NET_SUPPORTING_WM_CHECK off the root window, which points
// at the window the manager owns.
func checkWindow(conn *xgb.Conn, setup *xproto.SetupInfo, check xproto.Atom) xproto.Window {
	root, ok := rootWindow(conn, setup)
	if !ok || check == zero {
		return zero
	}

	reply, err := xproto.GetProperty(
		conn, false, root, check, xproto.AtomWindow, zero, oneWord,
	).Reply()

	return window(reply, err)
}

// rootWindow is the root of the screen the connection defaulted to.
func rootWindow(conn *xgb.Conn, setup *xproto.SetupInfo) (xproto.Window, bool) {
	if len(setup.Roots) <= conn.DefaultScreen {
		return zero, false
	}

	return setup.Roots[conn.DefaultScreen].Root, true
}

// window decodes the single window id a GetProperty reply should carry.
func window(reply *xproto.GetPropertyReply, err error) xproto.Window {
	if err != nil || reply == nil || len(reply.Value) < wordBytes {
		return zero
	}

	return xproto.Window(xgb.Get32(reply.Value))
}

// legacyProperty is WM_NAME, which every window manager sets, including the
// ones that predate EWMH.
func legacyProperty() property {
	return property{xproto.AtomWmName, xproto.AtomString}
}

// textProperty reads a string property, tolerating the BadWindow that a stale
// _NET_SUPPORTING_WM_CHECK produces after a window manager crash.
func textProperty(conn *xgb.Conn, win xproto.Window, prop property) string {
	if prop.name == zero {
		return noValue
	}

	reply, err := xproto.GetProperty(
		conn, false, win, prop.name, prop.typ, zero, maxNameWords,
	).Reply()
	if err != nil || reply == nil {
		return noValue
	}

	return firstString(reply.Value)
}

// firstString takes the leading NUL-terminated string out of a property value,
// which may carry several.
func firstString(value []byte) string {
	for i := range value {
		if value[i] == '\x00' {
			return string(value[:i])
		}
	}

	return string(value)
}

// extensions lists what the server advertises, which is how an Xwayland server
// is told apart from a native one.
func extensions(cookie xproto.ListExtensionsCookie) []string {
	reply, err := cookie.Reply()
	if err != nil || reply == nil {
		return nil
	}

	names := make([]string, zero, len(reply.Names))
	for i := range reply.Names {
		names = append(names, reply.Names[i].Name)
	}

	return names
}

// internAtoms sends all three InternAtom requests before waiting for any of
// them.
func internAtoms(conn *xgb.Conn) atomCookies {
	return atomCookies{
		check: internAtom(conn, atomSupportingWMCheck),
		name:  internAtom(conn, atomNetWMName),
		utf8:  internAtom(conn, atomUTF8String),
	}
}

func internAtom(conn *xgb.Conn, name string) xproto.InternAtomCookie {
	length := len(name)
	if length > math.MaxUint16 {
		return xproto.InternAtomCookie{Cookie: conn.NewCookie(true, false)}
	}

	return xproto.InternAtom(conn, true, uint16(length), name)
}

// atomOf resolves an InternAtom cookie, yielding zero when the atom does not
// exist on this server.
func atomOf(cookie xproto.InternAtomCookie) xproto.Atom {
	reply, err := cookie.Reply()
	if err != nil || reply == nil {
		return zero
	}

	return reply.Atom
}

func watchConnection(ctx context.Context, conn *connection) chan struct{} {
	stop := make(chan struct{})

	go watch(ctx, conn, stop)

	return stop
}

// X11 delegates to the configured function.
func (run Func[E, T]) X11(ctx context.Context, env *E) (T, error) {
	result, err := run(ctx, env)
	if err != nil {
		return result, fmt.Errorf("probe: run: %w", err)
	}

	return result, nil
}
