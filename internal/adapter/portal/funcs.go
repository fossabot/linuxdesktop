// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package portal

import (
	"context"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gostafa/linuxdesktop/internal/domain"
	"github.com/gostafa/linuxdesktop/internal/port"
	"github.com/gostafa/linuxdesktop/internal/sysfs"
)

// New returns a portal probe. bus may be nil, which reports the portal as
// unavailable without attempting to connect.
func New(bus port.Bus) Probe { return portalAt("", bus) }

// Portal reports whether xdg-desktop-portal is running and which of its
// interfaces the active backend exports.
func portalAt(files string, bus port.Bus) Probe {
	return func(ctx context.Context, env *domain.Env) (domain.PortalInfo, error) {
		var info domain.PortalInfo

		info.Backend = backend(files, env)

		return detectPortal(ctx, &info, bus)
	}
}

func detectPortal(
	ctx context.Context,
	info *domain.PortalInfo,
	bus port.Bus,
) (domain.PortalInfo, error) {
	err := portalAvailability(ctx, info, bus)
	if err != nil {
		return *info, fmt.Errorf(errDetectAvailability, err)
	}

	if !info.Available {
		return *info, nil
	}

	result, callErr := interfaces(ctx, info, bus)
	if callErr != nil {
		return result, fmt.Errorf(errDetectAvailability, callErr)
	}

	return result, nil
}

func portalAvailability(ctx context.Context, info *domain.PortalInfo, bus port.Bus) error {
	if bus == nil {
		return nil
	}

	owned, err := bus.HasOwner(ctx, port.SessionBus, busName)

	info.Available = owned

	if err != nil {
		return fmt.Errorf("portal: check owner: %w", err)
	}

	return nil
}

// interfaces asks the running portal what it exports, which is the only way to
// learn what the active backend actually implements.
func interfaces(
	ctx context.Context,
	info *domain.PortalInfo,
	bus port.Bus,
) (domain.PortalInfo, error) {
	object := port.Object{Destination: busName, Path: objectPath}

	raw, err := bus.Introspect(ctx, port.SessionBus, &object)
	if err != nil {
		return *info, fmt.Errorf(errReadInterfaces, err)
	}

	root, err := parseInterfaces(raw)
	if err != nil {
		return *info, fmt.Errorf(errReadInterfaces, err)
	}

	applyInterfaces(info, root.Interfaces)

	return *info, nil
}

// applyInterfaces marks every portal interface the backend exports.
func applyInterfaces(info *domain.PortalInfo, ifaces []iface) {
	flags := interfaceFlags()

	for i := range ifaces {
		record(info, flags, ifaces[i].Name)
	}
}

// record marks the one interface a fully qualified name refers to. A name
// outside the portal namespace is some other interface on the same object.
func record(info *domain.PortalInfo, flags map[string]flagSetter, full string) {
	name, found := strings.CutPrefix(full, ifacePrefix)
	if !found {
		return
	}

	info.DesktopPortal = true

	set, ok := flags[name]
	if ok {
		set(info)
	}
}

// interfaceFlags maps each portal interface this library reports on onto the
// field that records it.
func interfaceFlags() map[string]flagSetter {
	return map[string]flagSetter{
		ifaceScreenCast:    func(info *domain.PortalInfo) { info.ScreenCast = true },
		ifaceScreenshot:    func(info *domain.PortalInfo) { info.Screenshot = true },
		ifaceFileChooser:   func(info *domain.PortalInfo) { info.FileChooser = true },
		ifaceOpenURI:       func(info *domain.PortalInfo) { info.OpenURI = true },
		ifaceRemoteDesktop: func(info *domain.PortalInfo) { info.RemoteDesktop = true },
		ifaceInhibit:       func(info *domain.PortalInfo) { info.Inhibit = true },
		ifaceNotification:  func(info *domain.PortalInfo) { info.Notification = true },
	}
}

// backend names the portal implementation that should be servicing this
// desktop, preferring explicit configuration over the legacy UseIn matching.
func backend(files string, env *domain.Env) string {
	paths := configPaths(env)
	for i := range paths {
		if name := preferredDefault(files, paths[i]); name != noValue {
			return name
		}
	}

	return backendFromPortalFiles(files, env)
}

// configPaths lists portals.conf locations from most to least specific.
func configPaths(env *domain.Env) []string {
	paths := slices.Grow([]string(nil), expectedPaths)

	paths = append(paths, userConfig(env)...)

	tokens := desktopTokens(env)
	for i := range tokens {
		paths = append(paths, filepath.Join(shareDir, strings.ToLower(tokens[i])+configSuffix))
	}

	return append(paths, filepath.Join(shareDir, configName))
}

// userConfig is the per-user portals.conf, when the environment says where the
// user's configuration lives.
func userConfig(env *domain.Env) []string {
	if env.ConfigHome != noValue {
		return []string{filepath.Join(env.ConfigHome, portalDir, configName)}
	}

	if env.Home != noValue {
		return []string{filepath.Join(env.Home, userConfigDir, portalDir, configName)}
	}

	return nil
}

// preferredDefault reads the [preferred] default= entry, which is a
// semicolon-separated preference list.
func preferredDefault(files, path string) string {
	data, err := sysfs.Bytes(sysfs.Path(files, path))
	if err != nil {
		return noValue
	}

	for candidate := range strings.SplitSeq(sysfs.Field(data, keyDefault), listSeparator) {
		name := strings.TrimSpace(candidate)
		if named(name) {
			return name
		}
	}

	return noValue
}

// named reports whether a preference entry names a backend, rather than
// deferring the choice or refusing one.
func named(candidate string) bool {
	return candidate != noValue && candidate != anyBackend && candidate != noBackend
}

// backendFromPortalFiles matches the UseIn field of each installed .portal file
// against $XDG_CURRENT_DESKTOP. A lone installed backend wins by default.
func backendFromPortalFiles(files string, env *domain.Env) string {
	names, err := sysfs.DirNames(sysfs.Path(files, portalsDir))
	if err != nil {
		return noValue
	}

	found := installed(files, names)
	wanted := desktopTokens(env)

	for i := range found {
		if servesAny(found[i].uses, wanted) {
			return found[i].name
		}
	}

	return onlyOne(found)
}

// installed reads every .portal file in the portals directory, skipping the
// ones that are unreadable or not portal files at all.
func installed(files string, names []string) []backendFile {
	found := make([]backendFile, zero, len(names))

	for i := range names {
		file, ok := readPortalFile(files, names[i])
		if !ok {
			continue
		}

		found = append(found, file)
	}

	return found
}

// readPortalFile describes one .portal file.
func readPortalFile(files, name string) (backendFile, bool) {
	var empty backendFile

	if !strings.HasSuffix(name, portalSuffix) {
		return empty, false
	}

	data, err := sysfs.Bytes(sysfs.Path(files, filepath.Join(portalsDir, name)))
	if err != nil {
		return empty, false
	}

	file := backendFile{
		name: implName(strings.TrimSuffix(name, portalSuffix), data),
		uses: uses(data),
	}

	return file, true
}

// implName is the last element of the DBusName field, falling back to what the
// filename already said.
func implName(fallback string, data []byte) string {
	dbusName := sysfs.Field(data, keyDBusName)
	if dbusName == noValue {
		return fallback
	}

	last := afterLastDot(dbusName)
	if last == noValue {
		return fallback
	}

	return last
}

// afterLastDot is the final element of a dotted D-Bus name, or "" when there is
// no dot or nothing after it.
func afterLastDot(name string) string {
	idx := strings.LastIndexByte(name, dotByte)
	if idx < zero {
		return noValue
	}

	return name[idx+dotWidth:]
}

// uses is the desktop list a .portal file declares itself for.
func uses(data []byte) []string {
	var list []string

	for use := range strings.SplitSeq(sysfs.Field(data, keyUseIn), listSeparator) {
		trimmed := strings.TrimSpace(use)
		if trimmed != noValue {
			list = append(list, trimmed)
		}
	}

	return list
}

// servesAny reports whether a backend declares itself for any of the desktops
// currently running.
func servesAny(declared, wanted []string) bool {
	for i := range declared {
		if matchesAny(declared[i], wanted) {
			return true
		}
	}

	return false
}

func matchesAny(use string, wanted []string) bool {
	for i := range wanted {
		if strings.EqualFold(use, wanted[i]) {
			return true
		}
	}

	return false
}

// onlyOne names the single installed backend, which wins by default when none
// of them declared itself for this desktop.
func onlyOne(found []backendFile) string {
	name := noValue

	for i := range found {
		if name != noValue {
			return noValue // more than one is installed, so none is the default
		}

		name = found[i].name
	}

	return name
}

func desktopTokens(env *domain.Env) []string {
	if env.CurrentDesktop == noValue {
		return nil
	}

	return strings.Split(env.CurrentDesktop, desktopSeparator)
}

func parseInterfaces(raw string) (node, error) {
	var root node

	err := xml.Unmarshal([]byte(raw), &root)
	if err != nil {
		return root, fmt.Errorf("portal: parse interfaces: %w", err)
	}

	return root, nil
}

// Portal delegates to the configured function.
func (run Func[E, T]) Portal(ctx context.Context, env *E) (T, error) {
	result, err := run(ctx, env)
	if err != nil {
		return result, fmt.Errorf("probe: run: %w", err)
	}

	return result, nil
}
