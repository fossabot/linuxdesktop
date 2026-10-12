// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package x11

import (
	"context"
	"net"

	"github.com/gostafa/linuxdesktop/internal/domain"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

type (
	// Source is the contract consumed by the detection engine.
	Source interface {
		X11(ctx context.Context, env *domain.Env) (*domain.X11Info, error)
	}

	// Func delegates the probe to its configured function.
	Func[E, T any] func(ctx context.Context, env *E) (T, error)

	// Probe implements port.X11Probe. It is stateless.
	Probe = Func[domain.Env, *domain.X11Info]

	// atomCookies holds the three InternAtom requests while they are in flight.
	atomCookies struct {
		check xproto.InternAtomCookie
		name  xproto.InternAtomCookie
		utf8  xproto.InternAtomCookie
	}

	// atomSet is those three atoms once the server has answered. Any of them
	// may be zero, meaning this server does not know that name.
	atomSet struct {
		check xproto.Atom
		name  xproto.Atom
		utf8  xproto.Atom
	}

	// property is the name and type of one X property to read. A zero name
	// means there is no property to ask for.
	property struct {
		name xproto.Atom
		typ  xproto.Atom
	}

	// connection owns both upstream protocol state and its cancellable transport.
	connection struct {
		*xgb.Conn

		// Close releases the owned transport without waiting for protocol replies.
		Close func()
	}

	// unauthenticatedConn adapts the initial setup for servers without a cookie.
	unauthenticatedConn struct {
		// Conn forwards reads and transport operations after the setup write.
		net.Conn

		started bool
	}

	// displayTarget contains the parsed socket address and screen selection.
	displayTarget struct {
		network string
		address string
		host    string
		number  int
		screen  int
	}

	// authorityRecord holds one Xauthority entry's family and counted fields.
	authorityRecord struct {
		fields [wordBytes][]byte
		family uint16
	}
)
