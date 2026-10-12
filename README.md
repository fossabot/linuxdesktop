# Linuxdesktop


[![LICENSE](https://img.shields.io/github/license/gostafa/linuxdesktop)](/LICENSE) [![codecov](https://codecov.io/gh/gostafa/linuxdesktop/graph/badge.svg)](https://codecov.io/gh/gostafa/linuxdesktop) [![CodSpeed](https://img.shields.io/endpoint?url=https://codspeed.io/badge.json)](https://app.codspeed.io/gostafa/linuxdesktop?utm_source=badge) [![Go Reference](https://pkg.go.dev/badge/github.com/gostafa/linuxdesktop)](https://pkg.go.dev/github.com/gostafa/linuxdesktop)
[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Fgostafa%2Flinuxdesktop.svg?type=shield)](https://app.fossa.com/projects/git%2Bgithub.com%2Fgostafa%2Flinuxdesktop?ref=badge_shield)


A Go library for detecting the Linux desktop environment, login session, display
servers, compositor, GPUs, graphics stack, and desktop portal capabilities.

Detection returns partial results alongside probe errors, so an unavailable
service does not prevent you from using information collected by other probes.
This repository provides a library; it does not install a command-line program.

## Installation

Use Go **1.27.2 or newer**, as required by [go.mod](go.mod).
Inside your application's Go module, add the dependency:

```sh
go get github.com/gostafa/linuxdesktop
```

Import `github.com/gostafa/linuxdesktop` in your application. Commit `go.mod` and
`go.sum` to keep dependency versions reproducible.

### Runtime Requirements

Run detection on Linux, preferably as the logged-in desktop user. Results depend
on the process's environment and access to display sockets, D-Bus, `/proc`, and
`/sys`. A graphical session is not required for operating-system information.
Headless machines, containers, and SSH sessions may expose fewer details.

The default graphics probe inspects the installed stack without initializing an
OpenGL context. The optional `WithOpenGL()` probe loads native graphics libraries
and attempts to query the driver; it needs suitable installed libraries and
driver access. Installing the Go module does not install graphics drivers or
desktop services.

On other operating systems, detection returns a non-nil, headless environment
and an error matching `ErrNotLinux`. This allows cross-platform applications to
import the package and handle the unsupported platform explicitly.

## Quick Start

Save this as `main.go` in your application:

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"time"

	"github.com/gostafa/linuxdesktop"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	env, err := linuxdesktop.DetectContext(ctx)
	if errors.Is(err, linuxdesktop.ErrNotLinux) {
		log.Print("desktop detection requires Linux")
		return
	}
	if err != nil {
		// Successful probes still provide useful data.
		log.Printf("desktop detection returned partial results: %v", err)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(env); err != nil {
		log.Fatal(err)
	}
}
```

Run it from your desktop session:

```sh
go run .
```

For the default detection without a caller-supplied context, use
`env, err := linuxdesktop.Detect()`.

## Configuration

Pass options to `DetectContext`. Select multiple sections with bitwise OR:

```go
env, err := linuxdesktop.DetectContext(
	ctx,
	linuxdesktop.WithSections(
		linuxdesktop.SectionOS | linuxdesktop.SectionDisplay,
	),
	linuxdesktop.WithTimeout(2*time.Second),
	linuxdesktop.WithProbeTimeout(500*time.Millisecond),
)
```

Here, `ctx` is your application's context. Handle `err` and use `env` as in the
quick-start example.

| Option | Purpose | Default |
| --- | --- | --- |
| `WithSections(sections)` | Select which result sections to populate | `SectionAll` |
| `WithTimeout(duration)` | Bound the whole detection run | 2 seconds |
| `WithProbeTimeout(duration)` | Bound each individual probe | 500 milliseconds |
| `WithConnectionRetry(policy)` | Configure D-Bus connection initialization | 3 attempts; 25 ms initial and 100 ms maximum interval |
| `WithOpenGL()` | Enable native graphics queries for driver details | Disabled |
| `WithProcessScan()` | Enable the `/proc` fallback for compositor identification | Disabled |

Available sections are `SectionOS`, `SectionSession`, `SectionDisplay`,
`SectionDesktop`, `SectionCompositor`, `SectionGraphics`, and `SectionPortal`.
Unselected sections remain unpopulated. Passing `WithSections(0)` selects all
sections. Options apply in order, so a later option can override an earlier one.

D-Bus connections are shared by probes within each detection run and closed when
the run finishes. Transient transport failures are retried with exponential
delays (multiplier 2 and ±20% jitter), within the run and probe timeout budgets.
Missing sockets, invalid addresses, and authentication or permission failures
fail immediately. Each probe's context bounds its own wait without cancelling
another probe's shared initialization. The final connection outcome is cached
for that run.

For custom retry settings:

```go
env, err := linuxdesktop.DetectContext(ctx,
	linuxdesktop.WithConnectionRetry(linuxdesktop.RetryPolicy{
		MaxAttempts:     3,
		InitialInterval: 25 * time.Millisecond,
		MaxInterval:     100 * time.Millisecond,
	}),
)
```

`MaxAttempts` includes the first attempt; set it to 1 to disable retries. Attempts
must be positive, the initial interval must be positive, and the maximum interval
must be at least the initial interval. Invalid settings return a non-nil empty
environment and an error matching `ErrInvalidRetryPolicy` before Linux probes run.

To request live graphics details, include `SectionGraphics` and add
`WithOpenGL()`. This initializes the graphics stack and can cost more than the
default filesystem probe. A compositor identified by the process-scan fallback
is reported with `ConfidenceLow`.

Successful EGL and Vulkan library handles, static function bindings, and the
initialized default EGL display are retained for the process lifetime. The
display may be shared with the host application, so detection never terminates
it. Graphics values and probe-owned EGL contexts are queried fresh on each
native probe; failed loads or initialization can be retried on a later call.
EGL queries are serialized and restore the thread's previous EGL state. An
OpenGL version is reported only when the driver provides a GL version string.
Display transport operations respond to context cancellation; a native driver
call already in progress cannot be interrupted by the Go context.

## Results and Convenience Helpers

`Environment` contains the following fields:

| Field | Information |
| --- | --- |
| `OS` | Distribution, kernel, architecture, and hostname |
| `Session` | Login session, seat, session type, and remote/active status |
| `Display` | Display protocol, X11/Wayland availability, and server details |
| `Desktop` | Desktop identity, version, and original desktop environment values |
| `Compositor` | Compositor identity, confidence, and detection method |
| `Graphics` | GPU devices and OpenGL/Vulkan information |
| `Portal` | Desktop portal availability and exported interfaces |
| `Headless` | Whether detection found no available display server |

For a single section, use `OS()`, `Session()`, `Display()`, `Desktop()`,
`Compositor()`, `Graphics()`, or `Portal()`. Each returns its section's result and
an error. Use `DetectContext` when you need cancellation or custom options.

`IsWayland()`, `IsX11()`, and `IsHeadless()` provide inexpensive availability
checks. They do not connect to display servers; use `Display()` or the display
section of `DetectContext` to check actual connections. Both X11 and Wayland can
be available in the same session.

## Best Practices

* **Keep partial results.** Log or handle probe errors while retaining successful
  data. Use `errors.Is(err, linuxdesktop.ErrNotLinux)` for platform checks because
  errors may be wrapped or joined.
* **Request only what you use.** Select relevant sections and use positive timeout
  values appropriate to your application's responsiveness requirements.
* **Detect outside hot paths.** Cache a snapshot when appropriate and refresh it
  when the session changes. Display connections and optional driver queries have
  a cost.
* **Treat unknown values as normal.** Identity fields may contain
  `linuxdesktop.Unknown`; other unavailable details may be empty or zero-valued.
  Check `env.Display.X11` and `env.Display.Wayland` for nil before reading them.
* **Use confidence and capability fields.** Consider `Compositor.Confidence` and
  `Compositor.DetectedBy` when interpreting identification. Check the specific
  portal interface you need instead of assuming it exists from the desktop name.
* **Run in the intended user's session.** Running through `sudo` or in an isolated
  container can change environment variables and service access. Preserve the
  real session environment rather than inventing display or D-Bus addresses.
* **Share diagnostics carefully.** Results can include a hostname, user/session
  identifiers, and GPU details. Remove information you do not need before sending
  diagnostic output elsewhere.

## Development

Clone the repository and download its Go dependencies:

```sh
git clone https://github.com/gostafa/linuxdesktop.git
cd linuxdesktop
go mod download
```

Dependencies are pinned in `go.mod`. The X11 adapter uses upstream XGB with
project-owned transport cancellation and authentication selection.

Run the tests and static checks:

```sh
go test ./...
go vet ./...
```

Tests involving real system facilities may skip when the platform or required
services are unavailable. Validate Linux desktop behavior in an actual Linux
session when changing probes.

The repository also includes Task automation. If Task is installed, run
`task --list-all` to discover commands, or `task taskotter:go:test` to run Go tests.
See [taskotter](taskotter/Taskfile.yml) for the included tooling tasks.

Keep the public API in the root package; probe implementations live under
`internal/`. Format changed Go files with `gofmt`, add relevant tests for behavior
changes, and keep this README consistent with public options and defaults.


## License
[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Fgostafa%2Flinuxdesktop.svg?type=large)](https://app.fossa.com/projects/git%2Bgithub.com%2Fgostafa%2Flinuxdesktop?ref=badge_large)