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
	//nolint:reusability // XGB's protocol cookies have concrete types rather than reusable domain layouts.
	atomCookies struct {
		check xproto.InternAtomCookie
		name  xproto.InternAtomCookie
		utf8  xproto.InternAtomCookie
	}

	// atomSet is those three atoms once the server has answered. Any of them
	// may be zero, meaning this server does not know that name.
	//nolint:reusability // These fields are XGB wire atoms, not a cross-package domain layout.
	atomSet struct {
		check xproto.Atom
		name  xproto.Atom
		utf8  xproto.Atom
	}

	// property is the name and type of one X property to read. A zero name
	// means there is no property to ask for.
	//nolint:reusability // An XGB property is a protocol-specific atom pair.
	property struct {
		name xproto.Atom
		typ  xproto.Atom
	}

	// connection owns both upstream protocol state and its cancellable transport.
	connection struct {
		*xgb.Conn

		transport net.Conn
	}

	// unauthenticatedConn adapts the initial setup for servers without a cookie.
	unauthenticatedConn struct {
		net.Conn

		started bool
	}
)
