// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package portal

const (
	// The portal service on the session bus.
	busName     = "org.freedesktop.portal.Desktop"
	objectPath  = "/org/freedesktop/portal/desktop"
	ifacePrefix = "org.freedesktop.portal."

	// Interface suffixes this library reports on.
	ifaceScreenCast    = "ScreenCast"
	ifaceScreenshot    = "Screenshot"
	ifaceFileChooser   = "FileChooser"
	ifaceOpenURI       = "OpenURI"
	ifaceRemoteDesktop = "RemoteDesktop"
	ifaceInhibit       = "Inhibit"
	ifaceNotification  = "Notification"

	// Backend discovery.
	portalsDir    = "/usr/share/xdg-desktop-portal/portals"
	shareDir      = "/usr/share/xdg-desktop-portal"
	portalDir     = "xdg-desktop-portal"
	userConfigDir = ".config"
	configName    = "portals.conf"
	configSuffix  = "-portals.conf"
	portalSuffix  = ".portal"

	keyDefault  = "default"
	keyUseIn    = "UseIn"
	keyDBusName = "DBusName"

	// anyBackend and noBackend are the two preference entries that name no
	// backend at all: defer the choice, and refuse one.
	anyBackend = "*"
	noBackend  = "none"

	// listSeparator divides a portals.conf preference list, desktopSeparator
	// divides $XDG_CURRENT_DESKTOP, and dotByte divides a D-Bus name.
	listSeparator    = ";"
	desktopSeparator = ":"
	dotByte          = '.'

	// expectedPaths is the capacity the portals.conf search list is built at:
	// one per-user file, one per desktop token, and the system default.
	expectedPaths = 4

	// zero is the empty length a slice is built at.
	zero = 0

	// noValue is an unset variable, an unreadable file, or an unnamed backend.
	noValue = ""

	errDetectAvailability = "portal: detect availability: %w"
	errReadInterfaces     = "portal: read interfaces: %w"
)
