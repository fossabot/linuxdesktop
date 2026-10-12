// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/gostafa/linuxdesktop/internal/domain"
	"github.com/gostafa/linuxdesktop/internal/port"
	"github.com/gostafa/linuxdesktop/internal/probe"
	"github.com/gostafa/linuxdesktop/internal/rules"
)

// NewConfig applies opts to the defaults. It is separate from New because the
// caller needs the effective configuration in order to decide which adapters
// to construct in the first place.
func NewConfig(opts ...Option) Config {
	cfg := DefaultConfig()

	for i := range opts {
		if opts[i] != nil {
			opts[i](&cfg)
		}
	}

	return withDefaults(&cfg)
}

// New returns an Engine wired to deps.
func New(deps *Deps, cfg *Config) *Engine {
	state := &detector{deps: *deps, cfg: withDefaults(cfg)}
	engine := Engine(
		func(ctx context.Context) (*domain.Environment, error) { return detect(ctx, state) },
	)

	return &engine
}

// WithTimeout bounds the whole detection run.
func WithTimeout(d time.Duration) Option {
	return func(c *Config) { c.Timeout = d }
}

// WithProbeTimeout bounds each individual probe.
func WithProbeTimeout(d time.Duration) Option {
	return func(c *Config) { c.ProbeTimeout = d }
}

// WithSections selects which parts of the Environment to populate.
func WithSections(s domain.Section) Option {
	return func(c *Config) { c.Sections = s }
}

// WithNativeGL enables the dlopen graphics probe.
func WithNativeGL() Option {
	return func(c *Config) { c.NativeGL = true }
}

// WithProcessScan enables the /proc compositor fallback.
func WithProcessScan() Option {
	return func(c *Config) { c.ProcessScan = true }
}

// Detect runs the probes and merges their answers. The returned Environment is
// never nil; the error reports non-fatal probe failures and can be ignored by
// callers that only want the data.
func detect(ctx context.Context, eng *detector) (*domain.Environment, error) {
	ctx = probe.Default(ctx, context.Background)

	if eng.cfg.Timeout > zero {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, eng.cfg.Timeout)

		defer cancel()
	}

	gather := newCollector(snapshot(eng))

	run(ctx, eng, gather)
	finish(eng, gather)

	return collectedValues[*domain.Environment](gather.out, gather.failed)
}

// collectedValues preserves partial results alongside non-fatal probe failures.
func collectedValues[T any](value T, failed []error) (T, error) {
	return value, errors.Join(failed...)
}

// finish is stage two: everything below is pure, with every fact collected.
func finish(eng *detector, gather *collector) {
	display := collectedDisplay(gather)

	summarize(gather, &display)

	publish(eng, gather, &display)
}

// processSection is when the /proc fallback runs, which is never unless the
// caller asked for it: an unselected section is one no probe fires for.
func processSection(eng *detector) domain.Section {
	if !eng.cfg.ProcessScan {
		return zero
	}

	return domain.SectionCompositor
}

// publish writes the finished sections the caller asked for.
func publish(eng *detector, gather *collector, display *domain.DisplayInfo) {
	if wants(eng, domain.SectionDesktop) {
		gather.out.Desktop = gather.desktop
	}

	if wants(eng, sectionDisplayProbes) {
		// Headless is only meaningful once the display probes have run;
		// asserting it from a run that skipped them would be a lie.
		gather.out.Headless = rules.Headless(display.WaylandAvailable, display.X11Available)
	}

	if wants(eng, domain.SectionDisplay) {
		gather.out.Display = *display
	}

	if wants(eng, domain.SectionCompositor) {
		gather.out.Compositor = rules.Compositor(gather.sig)
	}
}

// run starts every wired probe whose section is wanted, then waits for all of
// them.
func run(ctx context.Context, eng *detector, gather *collector) {
	steps := steps(eng)
	for i := range steps {
		if steps[i].wired && wants(eng, steps[i].section) {
			spawn(ctx, gather, timedProbe(eng, steps[i].run))
		}
	}

	gather.wait.Wait()
}

// snapshot reads the environment once, before any probe runs, so that no probe
// pays for a repeated lookup.
func snapshot(eng *detector) *domain.Signals {
	sig := new(domain.Signals)

	if eng.deps.Env != nil {
		sig.Env = eng.deps.Env.Snapshot()
	}

	return sig
}

// spawn runs one probe on its own goroutine under its own deadline, so one
// wedged server cannot consume the whole budget.
func spawn(ctx context.Context, gather *collector, run probeFunc) {
	gather.wait.Go(func() {
		record(gather, run(ctx, gather))
	})
}

// steps is every probe the engine knows how to run, with the sections it
// contributes to and whether its adapter was supplied.
func steps(eng *detector) []step {
	steps := systemSteps(eng)

	steps = append(steps, sessionSteps(eng)...)
	steps = append(steps, displaySteps(eng)...)

	return append(steps, graphicsSteps(eng)...)
}

func systemSteps(eng *detector) []step {
	return []step{osStep(eng), processStep(eng)}
}

func osStep(eng *detector) step {
	deps := &eng.deps

	return selectedStep(
		plainProbe(
			deps.OS,
			port.OSProbe.OS,
			namedTarget(osTarget, "operating system"),
		),
		domain.SectionOS,
		deps.OS != nil,
	)
}

func processStep(eng *detector) step {
	deps := &eng.deps

	return selectedStep(
		plainProbe(
			deps.Process,
			port.ProcessProbe.Processes,
			namedTarget(processTarget, "processes"),
		),
		processSection(eng), deps.Process != nil,
	)
}

func sessionSteps(eng *detector) []step {
	return []step{sessionStep(eng), desktopStep(eng), portalStep(eng)}
}

func sessionStep(eng *detector) step {
	deps := &eng.deps

	return selectedStep(
		envProbe(
			deps.Session,
			port.SessionProbe.Session,
			namedTarget(sessionTarget, "session"),
		),
		domain.SectionSession, deps.Session != nil,
	)
}

func desktopStep(eng *detector) step {
	deps := &eng.deps

	return selectedStep(
		envProbe(
			deps.Desktop,
			port.DesktopProbe.Desktop,
			namedTarget(desktopTarget, "desktop"),
		),
		sectionDesktopProbes, deps.Desktop != nil,
	)
}

func portalStep(eng *detector) step {
	deps := &eng.deps

	return selectedStep(
		envProbe(
			deps.Portal,
			port.PortalProbe.Portal,
			namedTarget(portalTarget, "portal"),
		),
		domain.SectionPortal,
		deps.Portal != nil,
	)
}

func displaySteps(eng *detector) []step {
	return []step{x11Step(eng), waylandStep(eng)}
}

func x11Step(eng *detector) step {
	deps := &eng.deps

	return selectedStep(
		envProbe(
			deps.X11,
			port.X11Probe.X11,
			namedTarget(x11Target, "X11"),
		),
		sectionDisplayProbes,
		deps.X11 != nil,
	)
}

func waylandStep(eng *detector) step {
	deps := &eng.deps

	return selectedStep(
		envProbe(
			deps.Wayland,
			port.WaylandProbe.Wayland,
			namedTarget(waylandTarget, "Wayland"),
		),
		sectionDisplayProbes, deps.Wayland != nil,
	)
}

func envProbe[P, T any](
	adapter P,
	read func(P, context.Context, *domain.Env) (T, error),
	target resultTarget[collector, T],
) probeFunc {
	return func(ctx context.Context, gather *collector) error {
		info, err := read(adapter, ctx, &gather.sig.Env)

		return storeResult(gather, err, publication{
			name: target.name, store: func() { *target.value(gather) = info },
		})
	}
}

func plainProbe[P, T any](
	adapter P,
	read func(P, context.Context) (T, error),
	target resultTarget[collector, T],
) probeFunc {
	return func(ctx context.Context, gather *collector) error {
		info, err := read(adapter, ctx)

		return storeResult(gather, err, publication{
			name: target.name, store: func() { *target.value(gather) = info },
		})
	}
}

func desktopTarget(gather *collector) *domain.DesktopInfo  { return &gather.desktop }
func osTarget(gather *collector) *domain.OSInfo            { return &gather.out.OS }
func portalTarget(gather *collector) *domain.PortalInfo    { return &gather.out.Portal }
func processTarget(gather *collector) *[]string            { return &gather.sig.Processes }
func sessionTarget(gather *collector) *domain.SessionInfo  { return &gather.out.Session }
func waylandTarget(gather *collector) **domain.WaylandInfo { return &gather.wayland }
func x11Target(gather *collector) **domain.X11Info         { return &gather.x11 }

func wants(eng *detector, s domain.Section) bool { return eng.cfg.Sections&s != zero }

func newCollector(sig *domain.Signals) *collector {
	gather := new(collector)

	gather.out = new(domain.Environment)
	gather.sig = sig

	return gather
}

// display assembles DisplayInfo from the environment and whatever the two
// protocol probes managed to reach.
func collectedDisplay(gather *collector) domain.DisplayInfo {
	env := &gather.sig.Env
	info := domain.DisplayInfo{
		Protocol:         domain.Unknown,
		WaylandDisplay:   env.WaylandDisplay,
		X11Display:       env.Display,
		X11Available:     gather.x11 != nil,
		WaylandAvailable: gather.wayland != nil,
		X11:              gather.x11,
		Wayland:          gather.wayland,
		XWayland:         false,
	}
	// An Xwayland server says so itself; otherwise a reachable X server inside
	// a Wayland session can only be Xwayland.
	info.XWayland = hasExtension(gather.x11, domain.ExtensionXWayland) ||
		(info.X11Available && info.WaylandAvailable)

	return info
}

// record keeps a probe failure. Failures are collected rather than returned,
// since one unreachable server should not hide what the others found.
func record(gather *collector, err error) {
	if err == nil {
		return
	}

	gather.lock.Lock()
	defer gather.lock.Unlock()

	gather.failed = append(gather.failed, err)
}

// recordProtocols copies what the protocol probes saw into the signals the
// classification rules read.
func recordProtocols(gather *collector) {
	if gather.x11 != nil {
		gather.sig.X11WindowManager = gather.x11.WindowManager
		gather.sig.X11Extensions = gather.x11.Extensions
	}

	if gather.wayland != nil {
		gather.sig.WaylandGlobals = gather.wayland.Globals
	}
}

// summarize records everything the rules reason over, now that every probe has
// answered, and settles which protocol a client should actually speak.
func summarize(gather *collector, display *domain.DisplayInfo) {
	sig := gather.sig

	sig.Desktop = gather.desktop.Environment
	sig.X11Reachable = display.X11Available
	sig.WaylandReachable = display.WaylandAvailable

	recordProtocols(gather)

	display.Protocol = rules.Protocol(
		gather.out.Session.Type,
		display.WaylandAvailable,
		display.X11Available,
	)
}

// under runs fn while holding the lock. Every probe writes into the same
// Environment from its own goroutine, so all of them go through here.
func under(gather *collector, fn func()) {
	gather.lock.Lock()
	defer gather.lock.Unlock()

	fn()
}

// withDefaults fills in a configuration the caller left blank.
func withDefaults(config *Config) Config {
	cfg := *config
	if cfg.Sections == zero {
		cfg.Sections = domain.SectionAll
	}

	return cfg
}

func hasExtension(info *domain.X11Info, name string) bool {
	if info == nil {
		return false
	}

	return slices.Contains(info.Extensions, name)
}

// storeResult publishes even a partial answer before reporting its probe error.
func storeResult(gather *collector, err error, result publication) error {
	under(gather, result.store)

	if err != nil {
		return fmt.Errorf("core: probe %s: %w", result.name, err)
	}

	return nil
}

func graphicsSteps(eng *detector) []step {
	return []step{gpuStep(eng), stackStep(eng)}
}

func gpuStep(eng *detector) step {
	deps := &eng.deps

	return step{
		pairedProbe(
			deps.GPUs,
			port.GPUProbe.GPUs,
			pairedTarget[collector, []domain.GPUInfo, string]{
				first: gpuTarget, second: primaryTarget, name: "GPUs",
			},
		),
		domain.SectionGraphics,
		deps.GPUs != nil,
	}
}

func stackStep(eng *detector) step {
	deps := &eng.deps

	return step{
		pairedProbe(
			deps.Stack,
			port.StackProbe.Stack,
			pairedTarget[collector, domain.OpenGLInfo, domain.VulkanInfo]{
				first: openglTarget, second: vulkanTarget, name: "graphics stack",
			},
		),
		domain.SectionGraphics,
		deps.Stack != nil,
	}
}

func pairedProbe[P, A, B any](
	adapter P,
	read func(P, context.Context) (A, B, error),
	target pairedTarget[collector, A, B],
) probeFunc {
	return func(ctx context.Context, gather *collector) error {
		left, right, err := read(adapter, ctx)

		return storeResult(gather, err, publication{name: target.name, store: func() {
			*target.first(gather) = left
			*target.second(gather) = right
		}})
	}
}

func gpuTarget(gather *collector) *[]domain.GPUInfo     { return &gather.out.Graphics.GPUs }
func primaryTarget(gather *collector) *string           { return &gather.out.Graphics.PrimaryGPU }
func openglTarget(gather *collector) *domain.OpenGLInfo { return &gather.out.Graphics.OpenGL }
func vulkanTarget(gather *collector) *domain.VulkanInfo { return &gather.out.Graphics.Vulkan }

func timedProbe(eng *detector, run probeFunc) probeFunc {
	return func(ctx context.Context, gather *collector) error {
		probeCtx, cancel := context.WithCancel(ctx)

		if eng.cfg.ProbeTimeout > zero {
			cancel()

			probeCtx, cancel = context.WithTimeout(ctx, eng.cfg.ProbeTimeout)
		}

		defer cancel()

		return run(probeCtx, gather)
	}
}

func selectedStep(run probeFunc, section domain.Section, wired bool) step {
	return step{run: run, section: section, wired: wired}
}

func namedTarget[T any](value func(*collector) *T, name string) resultTarget[collector, T] {
	return resultTarget[collector, T]{value: value, name: name}
}

// Detect delegates to the configured function.
func (run Func[T]) Detect(ctx context.Context) (T, error) {
	result, err := run(ctx)
	if err != nil {
		return result, fmt.Errorf("probe: run: %w", err)
	}

	return result, nil
}
