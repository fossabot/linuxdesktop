//go:build linux && (amd64 || arm64 || 386 || arm || riscv64 || loong64 || ppc64le)

package native

type (
	// library isolates dynamic linking from the graphics protocol.
	library interface {
		open(name string, flags int) (uintptr, bool)
		symbol(handle uintptr, name string) uintptr
		register(target any, address uintptr)
	}

	// systemLibrary binds native entry points through purego.
	systemLibrary struct{}

	// eglFuncs holds the entry points the OpenGL probe binds.
	//nolint:reusability // EGL function signatures and retained native display state are protocol-specific.
	eglFuncs struct {
		displayState   *eglDisplayState
		queryAPI       func() uint32
		currentDisplay func() uintptr
		currentContext func() uintptr
		currentSurface func(int32) uintptr
		bindString     func(uintptr) func(uint32) string
		getDisplay     func(uintptr) uintptr
		initialize     func(uintptr, *int32, *int32) uint32
		queryString    func(uintptr, int32) string
		bindAPI        func(uint32) uint32
		chooseConfig   func(uintptr, *int32, *uintptr, int32, *int32) uint32
		createContext  func(uintptr, uintptr, uintptr, *int32) uintptr
		makeCurrent    func(uintptr, uintptr, uintptr, uintptr) uint32
		destroyContext func(uintptr, uintptr) uint32
		getProcAddress func(string) uintptr
	}
	// eglDisplayState retains the shared display without claiming ownership of it.
	eglDisplayState struct{ display uintptr }
	// eglThreadState captures the application's current bindings before probing.
	eglThreadState struct {
		api                          uint32
		display, context, draw, read uintptr
	}

	// clientAPI is one of the two rendering APIs the probe will try, in order.
	clientAPI struct {
		ctxAttrs   []int32
		enum       uint32
		renderable int32
	}
)
