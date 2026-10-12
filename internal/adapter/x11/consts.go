// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package x11

const (
	connectionErrorFormat     = "x11 connection: %w"
	detailErrorFormat         = "%w: %s"
	authorityValueErrorFormat = "read authority value: %w"
	setupErrorFormat          = "write setup: %w"
	setupHeaderBytes          = 12
	authNameOffset            = 6
	authEndOffset             = 10
	dummyCookie               = "00000000000000000000000000000000"
	x11TCPPort                = 6000
	pathSeparator             = "/"
	remoteNetwork             = "tcp"
	authorityLocal            = 256
	authorityWildcard         = 65535
	cookieBytes               = 16
	cookieProtocol            = "MIT-MAGIC-COOKIE-1"
	authorityFilename         = ".Xauthority"
	localhost                 = "localhost"
	// EWMH and ICCCM atom names.
	atomSupportingWMCheck = "_NET_SUPPORTING_WM_CHECK"
	atomNetWMName         = "_NET_WM_NAME"
	atomUTF8String        = "UTF8_STRING"

	// Local display sockets. "unix" is the historical spelling for a local
	// display in $DISPLAY and means the same thing as an empty host.
	unixSocketDir = "/tmp/.X11-unix"
	localHost     = "unix"

	// socketPrefix names a display socket inside unixSocketDir.
	socketPrefix = "/X"

	// displaySeparator divides the host from the display number in $DISPLAY,
	// and screenByte introduces the optional screen suffix after it.
	displaySeparator = ":"
	screenByte       = '.'

	// maxNameWords bounds the GetProperty read for a window manager name, in
	// 32-bit words. 64 words is 256 bytes, far more than any real name.
	maxNameWords = 64

	// oneWord is the single 32-bit window id _NET_SUPPORTING_WM_CHECK holds,
	// and wordBytes is how many bytes that is.
	oneWord   = 1
	wordBytes = 4

	// zero is an unresolved atom, an absent window, an empty length, and the
	// offset every property is read from. It is one constant rather than five
	// because a package may not declare two constants sharing a value.
	zero = 0

	// noValue is an unset variable or a name the server does not carry.
	noValue = ""
)
