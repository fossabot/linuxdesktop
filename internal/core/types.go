// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package core

import (
	"context"
	"sync"

	"github.com/gostafa/linuxdesktop/internal/domain"
	"github.com/gostafa/linuxdesktop/internal/port"
	"github.com/gostafa/linuxdesktop/internal/schema"
)

type (
	// Func delegates the probe to its configured function.
	Func[T any] func(ctx context.Context) (T, error)

	// resultTarget binds a result's destination to its diagnostic label.
	resultTarget[C, T any] struct {
		value func(*C) *T
		name  string
	}
	// pairedTarget binds both destinations for a probe with two results.
	pairedTarget[C, A, B any] struct {
		first  func(*C) *A
		second func(*C) *B
		name   string
	}
	// publication pairs the probe label with the synchronized update.
	publication struct {
		store func()
		name  string
	}

	// Config controls a detection run.
	Config = schema.Config[domain.Section]

	// Option mutates a Config.
	Option func(*Config)

	// Deps are the adapters an Engine runs. Every field may be nil, in which
	// case the corresponding data is simply left unset.
	Deps = dependencySet[
		port.EnvProbe,
		port.OSProbe,
		port.SessionProbe,
		port.X11Probe,
		port.WaylandProbe,
		port.DesktopProbe,
		port.GPUProbe,
		port.StackProbe,
		port.PortalProbe,
		port.ProcessProbe,
	]

	// dependencySet groups independently supplied probe implementations.
	dependencySet[E, O, S, X, W, D, G, L, P, F any] struct {
		// Env synchronous environment snapshot provider.
		Env E
		// OS distribution and kernel identity probe.
		OS O
		// Session logind seat session probe.
		Session S
		// X11 x server connection and metadata probe.
		X11 X
		// Wayland wayland connection and registry probe.
		Wayland W
		// Desktop desktop environment and version probe.
		Desktop D
		// GPUs dRM hardware enumeration probe.
		GPUs G
		// Stack probe for the client-side OpenGL and Vulkan implementations.
		Stack L
		// Portal desktop portal introspection probe.
		Portal P
		// Process optional fallback probe for compositor processes.
		Process F
	}

	// Engine runs a configured set of probes.
	Engine = Func[*domain.Environment]

	// detector holds the immutable dependencies and configuration captured by Engine.
	detector = executionState[Deps, Config]

	// executionState binds dependencies to configuration without selecting concrete implementations.
	executionState[D, C any] struct {
		deps D
		cfg  C
	}

	// probeFunc is one adapter call, already bound to the engine that owns it.
	probeFunc func(context.Context, *collector) error

	// step is one probe the engine may run: the work it does, the sections it
	// contributes to, and whether its adapter was wired in at all.
	step = probeStep[probeFunc, domain.Section]

	// probeStep associates work with its selection mask and availability.
	probeStep[R, S any] struct {
		run     R
		section S
		wired   bool
	}

	// collector gathers what the probes find. Each one runs on its own
	// goroutine, so every field here is written while holding lock.
	collector = collection[domain.Environment, domain.Signals, domain.X11Info, domain.WaylandInfo]

	// collection synchronizes results and evidence produced by concurrent probes.
	collection[O, S, X, W any] struct {
		out     *O
		sig     *S
		x11     *X
		wayland *W
		desktop domain.DesktopInfo
		failed  []error
		wait    sync.WaitGroup
		lock    sync.Mutex
	}
)
