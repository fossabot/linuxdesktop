//go:build linux && (amd64 || arm64 || 386 || arm || riscv64 || loong64 || ppc64le)

package native

import (
	"runtime"
	"strconv"

	"github.com/ebitengine/purego"
	"github.com/gostafa/linuxdesktop/internal/domain"
)

// Vulkan asks the loader for its API version.
func Vulkan() (string, bool) { return cachedVulkan(vulkanBindings) }

func detectVulkan(loader library) (version string, ok bool) {
	// purego may panic on an unexpected native symbol signature.
	defer func() {
		if recover() != nil {
			version, ok = "", false
		}
	}()

	return vulkanVersion(loader)
}

func vulkanVersion(loader library) (string, bool) {
	lib, loaded := loader.open(libVulkan, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if !loaded {
		return "", false
	}

	sym := loader.symbol(lib, vkEnumerateVersionName)
	if sym == zero {
		return vkBaseVersion, true
	}

	return enumerateVersion(loader, sym)
}

func enumerateVersion(loader library, symbol uintptr) (string, bool) {
	var enumerate func(*uint32) int32

	loader.register(&enumerate, symbol)

	return queryVulkan(enumerate)
}

func queryVulkan(enumerate func(*uint32) int32) (string, bool) {
	if enumerate == nil {
		return vkBaseVersion, true
	}

	var packed uint32

	if enumerate(&packed) != zero {
		return "", false
	}

	return decodeVersion(packed), true
}

// OpenGL initializes EGL and reads renderer details through a surfaceless context.
func OpenGL() (domain.OpenGLInfo, bool) { return cachedOpenGL(eglBindingsCache) }

func detectOpenGL(loader library) (info domain.OpenGLInfo, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()

	// EGL contexts belong to the current OS thread.
	runtime.LockOSThread()

	defer runtime.UnlockOSThread()

	ok = populateGL(&info, loader)

	return info, ok
}

func populateGL(info *domain.OpenGLInfo, loader library) bool {
	egl, loaded := loadEGL(loader)
	if !loaded {
		return false
	}

	return populateBoundGL(info, &egl)
}

func populateBoundGL(info *domain.OpenGLInfo, egl *eglFuncs) bool {
	display, initialized := initializeDisplay(egl)
	if !initialized {
		return false
	}

	*info = displayInfo(egl, display)
	mergeCurrent(info, egl, display)

	return true
}

func initializeDisplay(egl *eglFuncs) (uintptr, bool) {
	display := retainedDisplay(egl)
	if display != zero {
		return display, true
	}

	display = egl.getDisplay(zero)
	if display == zero {
		return display, false
	}

	initialized := initializeEGL(egl, display)
	if initialized {
		rememberDisplay(egl, display)
	}

	return display, initialized
}

func initializeEGL(egl *eglFuncs, display uintptr) bool {
	var major, minor int32

	return egl.initialize(display, &major, &minor) != zero
}

func retainedDisplay(egl *eglFuncs) uintptr {
	if egl.displayState == nil {
		return zero
	}

	return egl.displayState.display
}

func rememberDisplay(egl *eglFuncs, display uintptr) {
	if egl.displayState != nil {
		egl.displayState.display = display
	}
}

func displayInfo(egl *eglFuncs, display uintptr) domain.OpenGLInfo {
	return domain.OpenGLInfo{
		Available: true,
		Vendor:    egl.queryString(display, eglVendor),
		Version:   "",
		Renderer:  "",
	}
}

func mergeCurrent(info *domain.OpenGLInfo, egl *eglFuncs, display uintptr) {
	live, got := currentGL(egl, display)
	if !got {
		return
	}

	info.Renderer = live.Renderer
	info.Vendor = preferLive(info.Vendor, live.Vendor)
	info.Version = preferLive(info.Version, live.Version)
}

func preferLive(known, live string) string {
	if live != "" {
		return live
	}

	return known
}

// currentGL tries desktop OpenGL before OpenGL ES.
func currentGL(egl *eglFuncs, display uintptr) (domain.OpenGLInfo, bool) {
	if egl.queryAPI != nil {
		state := snapshotEGLState(egl)
		defer restoreEGLState(egl, display, &state)
	}

	apis := []clientAPI{
		{[]int32{eglNone}, eglOpenGLAPI, eglOpenGLBit},
		{[]int32{eglContextVersion, eglESVersion, eglNone}, eglOpenGLESAPI, eglOpenGLES2Bit},
	}
	for i := range apis {
		if info, ok := tryAPI(egl, display, &apis[i]); ok {
			return info, true
		}
	}

	var empty domain.OpenGLInfo

	return empty, false
}

func snapshotEGLState(egl *eglFuncs) eglThreadState {
	return eglThreadState{
		api: egl.queryAPI(), display: egl.currentDisplay(), context: egl.currentContext(),
		draw: egl.currentSurface(eglDraw), read: egl.currentSurface(eglRead),
	}
}

func restoreEGLState(egl *eglFuncs, display uintptr, state *eglThreadState) {
	egl.bindAPI(state.api)

	if state.display == zero {
		state.display = display
	}

	egl.makeCurrent(state.display, state.draw, state.read, state.context)
}

func tryAPI(egl *eglFuncs, display uintptr, api *clientAPI) (domain.OpenGLInfo, bool) {
	var empty domain.OpenGLInfo

	ctx := createGLContext(egl, display, api)

	if ctx == zero {
		return empty, false
	}

	defer egl.destroyContext(display, ctx)

	return contextInfo(egl, display, ctx)
}

func createGLContext(egl *eglFuncs, display uintptr, api *clientAPI) uintptr {
	if egl.bindAPI(api.enum) == zero {
		return zero
	}

	config, chosen := chooseConfig(egl, display, api.renderable)
	if !chosen {
		return zero
	}

	return egl.createContext(display, config, zero, &api.ctxAttrs[0])
}

func chooseConfig(egl *eglFuncs, display uintptr, renderable int32) (uintptr, bool) {
	attrs := []int32{eglSurfaceType, one, eglRenderableType, renderable, eglNone}

	var (
		config uintptr
		count  int32
	)

	chosen := egl.chooseConfig(display, &attrs[0], &config, one, &count)

	return config, chosen != zero && count != zero
}

func contextInfo(egl *eglFuncs, display, ctx uintptr) (domain.OpenGLInfo, bool) {
	var empty domain.OpenGLInfo

	if egl.makeCurrent(display, zero, zero, ctx) == zero {
		return empty, false
	}

	defer egl.makeCurrent(display, zero, zero, zero)

	return readGLStrings(egl)
}

func readGLStrings(egl *eglFuncs) (domain.OpenGLInfo, bool) {
	var empty domain.OpenGLInfo

	getString, bound := bindGetString(egl)

	if !bound {
		return empty, false
	}

	info := domain.OpenGLInfo{
		Available: true,
		Vendor:    getString(glVendor),
		Renderer:  getString(glRenderer),
		Version:   getString(glVersion),
	}

	return info, info.Renderer != ""
}

func bindGetString(egl *eglFuncs) (getString func(uint32) string, ok bool) {
	defer func() {
		if recover() != nil {
			getString, ok = nil, false
		}
	}()

	addr := egl.getProcAddress(getStringSymbol)
	if addr == zero {
		return nil, false
	}

	getString = egl.bindString(addr)

	return getString, true
}

func loadEGL(loader library) (egl eglFuncs, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()

	return eglBindings(loader)
}

func eglBindings(loader library) (egl eglFuncs, ok bool) {
	lib, loaded := loader.open(libEGL, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if !loaded {
		return egl, false
	}

	defer func() {
		releaseFailedBinding(loader, lib, ok)
	}()

	return boundEGL(loader, lib), true
}

func boundEGL(loader library, lib uintptr) eglFuncs {
	egl := new(eglFuncs)

	egl.displayState = new(eglDisplayState)

	egl.bindString = func(address uintptr) func(uint32) string {
		var getString func(uint32) string

		loader.register(&getString, address)

		return getString
	}
	bindEGL(egl, loader, lib)

	return *egl
}

func (systemLibrary) open(name string, flags int) (uintptr, bool) {
	lib, err := purego.Dlopen(name, flags)

	return lib, err == nil && lib != zero
}

func (systemLibrary) symbol(lib uintptr, name string) uintptr {
	address, err := purego.Dlsym(lib, name)
	if err != nil {
		return zero
	}

	return address
}

func (systemLibrary) register(target any, address uintptr) {
	purego.RegisterFunc(target, address)
}

func (systemLibrary) release(handle uintptr) {
	//nolint:errcheck,gosec // Binding failure is already handled as unavailable; dlclose has no actionable recovery.
	purego.Dlclose(handle)
}

func releaseLibrary(loader library, handle uintptr) {
	if closer, ok := loader.(interface{ release(handle uintptr) }); ok {
		closer.release(handle)
	}
}

func bindEGL(egl *eglFuncs, loader library, lib uintptr) {
	targets := eglTargets(egl)
	for name := range targets {
		loader.register(targets[name], loader.symbol(lib, name))
	}
}

func eglTargets(egl *eglFuncs) map[string]any {
	return map[string]any{
		"eglQueryAPI":          &egl.queryAPI,
		"eglGetCurrentDisplay": &egl.currentDisplay,
		"eglGetCurrentContext": &egl.currentContext,
		"eglGetCurrentSurface": &egl.currentSurface,
		"eglGetDisplay":        &egl.getDisplay,
		"eglInitialize":        &egl.initialize,
		"eglQueryString":       &egl.queryString,
		"eglBindAPI":           &egl.bindAPI,
		"eglChooseConfig":      &egl.chooseConfig,
		"eglCreateContext":     &egl.createContext,
		"eglMakeCurrent":       &egl.makeCurrent,
		"eglDestroyContext":    &egl.destroyContext,
		"eglGetProcAddress":    &egl.getProcAddress,
	}
}

// decodeVersion unpacks a VK_MAKE_API_VERSION value.
func decodeVersion(packed uint32) string {
	major := (packed >> vkMajorShift) & vkMajorMask
	minor := (packed >> vkMinorShift) & vkMinorMask
	patch := packed & vkPatchMask
	version := strconv.FormatUint(uint64(major), decimalBase) + "." +
		strconv.FormatUint(uint64(minor), decimalBase) + "." +
		strconv.FormatUint(uint64(patch), decimalBase)

	if variant := packed >> vkVariantShift; variant != zero {
		return version + " (variant " + strconv.FormatUint(uint64(variant), decimalBase) + ")"
	}

	return version
}
