// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package x11

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/jezek/xgb"
)

// Write strips the explicit dummy cookie only from the setup request.
func (conn *unauthenticatedConn) Write(data []byte) (int, error) {
	writer := writeSetup

	if conn.started {
		writer = writeTransport
	}

	conn.started = true

	count, err := writer(conn.Conn, data)
	if err != nil {
		return count, fmt.Errorf(setupErrorFormat, err)
	}

	return count, nil
}

func connectDisplay(ctx context.Context, display string) (*connection, error) {
	err := ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("connect display: %w", err)
	}

	target, err := displayAddress(display)
	if err != nil {
		return nil, fmt.Errorf("parse display: %w", err)
	}

	conn, err := dialDisplay(ctx, target)
	if err != nil {
		return nil, fmt.Errorf(connectionErrorFormat, err)
	}

	return conn, nil
}

func dialDisplay(ctx context.Context, target *displayTarget) (*connection, error) {
	var dialer net.Dialer

	transport, err := dialer.DialContext(ctx, target.network, target.address)
	if err != nil {
		return nil, fmt.Errorf("dial display: %w", err)
	}

	conn, err := setupDisplay(ctx, transport, target)
	if err != nil {
		return nil, fmt.Errorf(connectionErrorFormat, err)
	}

	return conn, nil
}

func finishSetup(ctx context.Context, owned *connection, err error) (*connection, error) {
	err = errors.Join(err, ctx.Err())
	if err != nil {
		owned.Close()

		return nil, fmt.Errorf("setup display: %w", err)
	}

	return owned, nil
}

func newConnection(transport net.Conn) *connection {
	return &connection{Conn: nil, Close: func() { closeTransport(transport) }}
}

func closeTransport(transport net.Conn) {
	err := transport.Close()
	if err != nil {
		return
	}
}

func setupDisplay(
	ctx context.Context,
	transport net.Conn,
	target *displayTarget,
) (*connection, error) {
	owned := newConnection(transport)
	stop := context.AfterFunc(ctx, owned.Close)

	defer stop()

	protocol, err := displayProtocol(transport, target)

	owned.Conn = protocol

	conn, err := finishSetup(ctx, owned, err)
	if err != nil {
		return nil, fmt.Errorf(connectionErrorFormat, err)
	}

	return conn, nil
}

func displayProtocol(transport net.Conn, target *displayTarget) (*xgb.Conn, error) {
	cookie, err := authorityCookie(target.host, target.number)
	if err != nil {
		return nil, fmt.Errorf("read setup authority: %w", err)
	}

	conn, err := setupTransport(transport, cookie)
	if err != nil {
		return nil, fmt.Errorf("connect protocol: %w", err)
	}

	conn.DisplayNumber, conn.DefaultScreen = target.number, target.screen

	return conn, nil
}

func setupTransport(transport net.Conn, cookie string) (*xgb.Conn, error) {
	if cookie == noValue {
		transport = &unauthenticatedConn{Conn: transport, started: false}
		cookie = dummyCookie
	}

	conn, err := xgb.NewConnNetWithCookieHex(transport, cookie)
	if err != nil {
		return nil, fmt.Errorf("initialize protocol: %w", err)
	}

	return conn, nil
}

func writeSetup(transport net.Conn, data []byte) (int, error) {
	if len(data) < setupHeaderBytes {
		return zero, fmt.Errorf(setupErrorFormat, io.ErrUnexpectedEOF)
	}

	setup := unauthenticatedSetup(data)

	count, err := writeTransport(transport, setup)
	if err != nil {
		return zero, fmt.Errorf(setupErrorFormat, err)
	}

	count, err = setupWriteCount(count, len(data))
	if err != nil {
		return zero, fmt.Errorf(setupErrorFormat, err)
	}

	return count, nil
}

func setupWriteCount(count, length int) (int, error) {
	if count != setupHeaderBytes {
		return zero, fmt.Errorf(setupErrorFormat, io.ErrShortWrite)
	}

	return length, nil
}

func writeTransport(transport net.Conn, data []byte) (int, error) {
	count, err := transport.Write(data)
	if err != nil {
		return count, fmt.Errorf("write transport: %w", err)
	}

	return count, nil
}

func unauthenticatedSetup(data []byte) []byte {
	setup := append([]byte(nil), data[:setupHeaderBytes]...)
	clear(setup[authNameOffset:authEndOffset])

	return setup
}
