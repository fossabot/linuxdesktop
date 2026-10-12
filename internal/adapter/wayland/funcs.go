// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package wayland

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/gostafa/linuxdesktop/internal/domain"
	"github.com/gostafa/linuxdesktop/internal/probe"
	"github.com/gostafa/linuxdesktop/internal/sysfs"
)

// New returns a Wayland probe.
func New() Probe { return detectWayland }

// SocketPath resolves the compositor socket, returning "" when there is none.
// It only reports a path that is actually a socket, so callers can use it as a
// cheap availability test without connecting.
func SocketPath(env *domain.Env) string {
	name := displayName(env)
	if filepath.IsAbs(name) {
		return socketAt(name)
	}

	if env.RuntimeDir == noValue {
		return noValue
	}

	return socketAt(filepath.Join(env.RuntimeDir, name))
}

// Has reports whether a named interface is present in a registry listing.
func Has(globals []domain.WaylandGlobal, iface string) bool {
	for i := range globals {
		if globals[i].Interface == iface {
			return true
		}
	}

	return false
}

// Wayland connects to the compositor and enumerates its global registry. A nil
// result with a nil error means there was no compositor to talk to.
//
//nolint:wrapcheck // Registry helpers already add the enumeration operation to errors.
func detectWayland(ctx context.Context, env *domain.Env) (*domain.WaylandInfo, error) {
	path := SocketPath(env)
	if path == noValue {
		return inherited(env), nil
	}

	globals, err := listen(ctx, path)

	return registryValues(env, globals, err)
}

//nolint:wrapcheck // enumerationValues supplies the diagnostic context while preserving partial data.
func registryValues(
	env *domain.Env,
	globals []domain.WaylandGlobal,
	err error,
) (*domain.WaylandInfo, error) {
	if err != nil && len(globals) == wireZero {
		return enumerationValues[*domain.WaylandInfo](nil, err)
	}

	info := &domain.WaylandInfo{
		Display:  displayName(env),
		Globals:  globals,
		SocketFD: wireZero,
	}
	if descriptor, ok := inheritedFD(env); ok {
		info.SocketFD = descriptor
	}

	return enumerationValues(info, err)
}

// enumerationValues keeps usable registry events alongside a later failure.
func enumerationValues[T any](info T, err error) (T, error) {
	if err != nil {
		return info, fmt.Errorf("wayland: enumerate globals: %w", err)
	}

	return info, nil
}

// newRegistry prepares a listing sized for what a real compositor advertises,
// so the common case costs one allocation apiece for the buffer and the slice.
func newRegistry() *registry {
	return &registry{
		globals: slices.Grow([]domain.WaylandGlobal(nil), expectedGlobals),
		buf:     make([]byte, initialReadBuf),
		filled:  wireZero,
	}
}

// compact moves the bytes of a partly received message to the front, so the
// next read continues it rather than overwriting it.
func compact(reg *registry, used int) {
	copy(reg.buf, reg.buf[used:reg.filled])

	reg.filled -= used
}

// dispatch routes one decoded message to the object it names. Anything else is
// a message this client never asked for.
func dispatch(reg *registry, event *wireEvent) error {
	object, opcode, body := event.object, event.opcode, event.body
	if object == objRegistry {
		record(reg, opcode, body)

		return nil
	}

	err := dispatchControl(object, opcode, body)
	if err != nil {
		return fmt.Errorf(errDispatchEvent, err)
	}

	return nil
}

func dispatchControl(object, opcode uint32, body []byte) error {
	handlers := map[uint32]func(uint32, []byte) error{
		objCallback: callbackEvent,
		wireOne:     displayEvent,
	}
	handler, ok := handlers[object]

	if !ok {
		return nil
	}

	err := handler(opcode, body)
	if err != nil {
		return fmt.Errorf("wayland: control event %d: %w", object, err)
	}

	return nil
}

func callbackEvent(opcode uint32, _ []byte) error {
	err := complete(opcode)
	if err != nil {
		return fmt.Errorf("wayland: callback event: %w", err)
	}

	return nil
}

func displayEvent(opcode uint32, body []byte) error {
	err := failure(opcode, body)
	if err != nil {
		return fmt.Errorf("wayland: display event: %w", err)
	}

	return nil
}

// grow doubles the buffer when it is full, refusing to pass the cap that keeps
// a hostile peer from making the client allocate without end.
func grow(reg *registry) error {
	if reg.filled < len(reg.buf) {
		return nil
	}

	if reg.filled >= maxMessageBytes {
		return ErrProtocol
	}

	reg.buf = append(reg.buf, make([]byte, len(reg.buf))...)

	return nil
}

// message handles the one message starting at offset and reports its size. The
// false result means the buffer does not hold all of it yet.
func message(reg *registry, offset int) (size int, ready bool, err error) {
	object, opcode, size := messageHeader(reg.buf[offset:])

	if size < headerSize || size > maxMessageBytes {
		return size, false, ErrProtocol
	}

	if reg.filled-offset < size {
		return size, false, nil
	}

	body := reg.buf[offset+headerSize : offset+size]

	callErr := dispatch(reg, &wireEvent{object: object, opcode: opcode, body: body})
	if callErr != nil {
		return size, true, fmt.Errorf("wayland: decode message: %w", callErr)
	}

	return size, true, nil
}

// parse handles every message the buffer now holds in full, then compacts what
// is left of a partly received one to the front.
func parse(reg *registry) error {
	var used int

	for reg.filled-used >= headerSize {
		size, ready, err := message(reg, used)
		if err != nil {
			return fmt.Errorf("wayland: parse events: %w", err)
		}

		if !ready {
			break
		}

		used += size
	}

	compact(reg, used)

	return nil
}

// record keeps a wl_registry.global event and ignores every other one.
func record(reg *registry, opcode uint32, body []byte) {
	if opcode != wireZero {
		return
	}

	if global, ok := parseGlobal(body); ok {
		reg.globals = append(reg.globals, global)
	}
}

// step reads one chunk from the compositor and handles every message it
// completes. Parsing happens before the read error is acted on: a compositor is
// entitled to send the whole registry and close, delivering the data and the
// EOF in one read, and that data is exactly what was asked for.
func step(reg *registry, reader io.Reader) error {
	err := grow(reg)
	if err != nil {
		return fmt.Errorf(errReadEvents, err)
	}

	callErr := read(reg, reader)
	if callErr != nil {
		return fmt.Errorf(errReadEvents, callErr)
	}

	return nil
}

func read(reg *registry, reader io.Reader) error {
	got, err := reader.Read(reg.buf[reg.filled:])

	reg.filled += got

	parseErr := parse(reg)
	if parseErr != nil {
		return fmt.Errorf("wayland: read parsed events: %w", parseErr)
	}

	callErr := stalled(got, err)
	if callErr != nil {
		return fmt.Errorf("wayland: read progress: %w", callErr)
	}

	return nil
}

func messageHeader(frame []byte) (object, opcode uint32, size int) {
	order := binary.NativeEndian
	word := order.Uint32(frame[wordBytes:])

	return order.Uint32(frame), word & opcodeMask, int(word >> opcodeBits)
}

// listen dials the compositor and returns the registry it advertises.
func listen(ctx context.Context, path string) ([]domain.WaylandGlobal, error) {
	var dialer net.Dialer

	conn, err := dialer.DialContext(ctx, unixNetwork, path)
	if err != nil {
		return nil, fmt.Errorf("wayland: connect compositor: %w", err)
	}

	globals, err := converse(ctx, conn)

	closed := conn.Close()

	if err == nil {
		err = closed
	}

	return globals, err
}

// converse bounds the connection, sends the handshake and reads the reply.
//
//nolint:wrapcheck // converseWire wraps transport errors; context errors are wrapped by probe.
func converse(ctx context.Context, conn net.Conn) (globals []domain.WaylandGlobal, failure error) {
	stop := context.AfterFunc(ctx, func() { cancelConnection(conn) })
	defer stop()
	defer func() {
		contextErr := probe.ContextError(ctx)
		if contextErr != nil {
			failure = contextErr
		}
	}()

	return converseWire(ctx, conn)
}

func cancelConnection(conn net.Conn) {
	//nolint:errcheck,gosec // Cancellation is already the actionable error; close only interrupts I/O.
	_ = conn.Close()
}

func converseWire(ctx context.Context, conn net.Conn) ([]domain.WaylandGlobal, error) {
	err := prepareConnection(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf(errQueryRegistry, err)
	}

	result, callErr := readGlobals(conn)
	if callErr != nil {
		return result, fmt.Errorf(errQueryRegistry, callErr)
	}

	return result, nil
}

func prepareConnection(ctx context.Context, conn net.Conn) error {
	err := probe.ContextError(ctx)
	if err != nil {
		return fmt.Errorf(errQueryRegistry, err)
	}

	err = bound(ctx, conn)
	if err != nil {
		return fmt.Errorf(errQueryRegistry, err)
	}

	err = handshake(conn)
	if err != nil {
		return fmt.Errorf(errQueryRegistry, err)
	}

	return nil
}

// bound applies the caller's deadline to the connection, if it set one.
func bound(ctx context.Context, conn net.Conn) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil
	}

	callErr := conn.SetDeadline(deadline)
	if callErr != nil {
		return fmt.Errorf("wayland: set deadline: %w", callErr)
	}

	return nil
}

// handshake writes wl_display.get_registry and wl_display.sync as one frame, so
// the entire exchange costs a single round trip.
func handshake(writer io.Writer) error {
	var buf [handshakeSize]byte

	putRequest(buf[:requestSize], wireOne, objRegistry)
	putRequest(buf[requestSize:], wireZero, objCallback)

	written, err := writer.Write(buf[:])
	if err == nil && written != len(buf) {
		return io.ErrShortWrite
	}

	if err != nil {
		return fmt.Errorf("wayland: write handshake: %w", err)
	}

	return nil
}

// putRequest writes one wl_display request: the object id, the packed
// size/opcode word, and the new_id the request allocates.
func putRequest(frame []byte, opcode, newID uint32) {
	order := binary.NativeEndian

	order.PutUint32(frame, wireOne)
	order.PutUint32(frame[wordBytes:], requestSize<<opcodeBits|opcode)
	order.PutUint32(frame[headerSize:], newID)
}

// readGlobals collects wl_registry.global events until the sync callback fires,
// which is the compositor's guarantee that the listing is complete.
func readGlobals(reader io.Reader) ([]domain.WaylandGlobal, error) {
	reg := newRegistry()

	var err error

	for err == nil {
		err = step(reg, reader)
	}

	callErr := registryError(err)
	if callErr != nil {
		return reg.globals, fmt.Errorf("wayland: read registry: %w", callErr)
	}

	return reg.globals, nil
}

// finished turns the sentinel that ends a complete listing back into success.
func finished(err error) error {
	if errors.Is(err, errComplete) {
		return nil
	}

	return err
}

// stalled reports the read error, or the failure to make any progress that
// would otherwise spin the loop forever against a silent peer.
func stalled(progress int, err error) error {
	if err != nil {
		return err
	}

	if progress == wireZero {
		return io.ErrNoProgress
	}

	return nil
}

// complete reports the sync callback that ends the listing.
func complete(opcode uint32) error {
	if opcode != wireZero {
		return nil
	}

	return errComplete
}

// failure turns a wl_display.error event into the error it describes.
func failure(opcode uint32, body []byte) error {
	if opcode != wireZero {
		return nil
	}

	return fmt.Errorf("wayland: decode protocol error: %w", parseError(body))
}

// parseGlobal decodes name, interface and version. Wayland strings are a
// length-prefixed, NUL-terminated blob padded to a word boundary.
func parseGlobal(body []byte) (domain.WaylandGlobal, bool) {
	var empty domain.WaylandGlobal

	if len(body) < requestSize {
		return empty, false
	}

	return decodeGlobal(body)
}

func decodeGlobal(body []byte) (domain.WaylandGlobal, bool) {
	order := binary.NativeEndian
	length := int(order.Uint32(body[wordBytes:]))
	rest := body[wordBytes+wordBytes:]
	padded := align(length)

	// A padded length below one word means an empty interface name, which no
	// real global has.
	if padded < wordBytes || padded+wordBytes > len(rest) {
		var empty domain.WaylandGlobal

		return empty, false
	}

	return domain.WaylandGlobal{
		Name:      order.Uint32(body),
		Interface: text(rest, length),
		Version:   order.Uint32(rest[padded:]),
	}, true
}

// parseError turns a wl_display.error event into a Go error.
func parseError(body []byte) error {
	if len(body) < requestSize {
		return ErrProtocol
	}

	order := binary.NativeEndian
	code := order.Uint32(body[wordBytes:])
	length := int(order.Uint32(body[wordBytes+wordBytes:]))

	return fmt.Errorf(
		"%w %d: %s",
		ErrProtocol, code, text(body[requestSize:], length),
	)
}

// text lifts the NUL-terminated string of the declared length off the front of
// rest, yielding "" when that length does not fit.
func text(rest []byte, length int) string {
	if length > len(rest) {
		return noValue
	}

	return string(bytes.TrimRight(rest[:length], nul))
}

// align rounds a Wayland string length up to the word boundary the wire format
// pads it to.
func align(length int) int {
	return length + (wordBytes-length%wordBytes)%wordBytes
}

// socketAt returns path when a unix socket lives there, and "" otherwise.
func socketAt(path string) string {
	if sysfs.IsSocket(path) {
		return path
	}

	return noValue
}

func displayName(env *domain.Env) string {
	if env.WaylandDisplay != noValue {
		return env.WaylandDisplay
	}

	return defaultDisplay
}

// inherited reports the connected socket a compositor handed down when there is
// no path to dial, and nothing when there is none.
func inherited(env *domain.Env) *domain.WaylandInfo {
	descriptor, ok := inheritedFD(env)
	if !ok {
		return nil
	}

	return &domain.WaylandInfo{
		Display:  env.WaylandDisplay,
		SocketFD: descriptor,
		Globals:  nil,
	}
}

// inheritedFD reads $WAYLAND_SOCKET. The descriptor is only ever reported,
// never consumed: it belongs to the host application.
func inheritedFD(env *domain.Env) (int, bool) {
	if env.WaylandSocket == noValue {
		return wireZero, false
	}

	descriptor, err := strconv.Atoi(env.WaylandSocket)
	if err != nil || descriptor < wireZero {
		return wireZero, false
	}

	return descriptor, true
}

func registryError(err error) error {
	callErr := finished(err)
	if callErr != nil {
		return fmt.Errorf("wayland: collect globals: %w", callErr)
	}

	return nil
}

// Wayland delegates to the configured function.
func (run Func[E, T]) Wayland(
	ctx context.Context,
	env *E,
) (T, error) {
	result, err := run(ctx, env)
	if err != nil {
		return result, fmt.Errorf("probe: run: %w", err)
	}

	return result, nil
}
