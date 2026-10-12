//go:build linux && (amd64 || arm64 || 386 || arm || riscv64 || loong64 || ppc64le)

//nolint:distance // This platform boundary owns concrete dynamic library handles and bindings.
package native

import (
	"context"
	"errors"
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/gostafa/linuxdesktop/internal/domain"
	"github.com/gostafa/singleton"
)

// bindingCache retains successful bindings. A load failure can be tried again
// by the next probe without resetting a successful process-owned value.
type (
	bindingCache[Value any] struct {
		provider *singleton.Provider[Value]
	}
	vulkanFuncs                            = versionBindings[uintptr, func(*uint32) int32]
	versionBindings[Handle, Enumerate any] struct {
		enumerate Enumerate
		handle    Handle
	}
)

var (
	errBindingPanic        = errors.New("native: invalid library binding")
	errBindingsUnavailable = errors.New("native: library bindings unavailable")
	//nolint:gochecknoglobals // Successful native bindings intentionally live for the process lifetime.
	eglBindingsCache = newBindingCache(func() (eglFuncs, bool) { return loadEGL(systemLibrary{}) })
	//nolint:gochecknoglobals // Vulkan entry points must retain their shared library handle.
	vulkanBindings = newBindingCache(
		func() (vulkanFuncs, bool) { return loadVulkan(systemLibrary{}) },
	)
	//nolint:gochecknoglobals // Shared EGL display initialization and queries must be serialized across calls.
	eglLifecycle sync.Mutex
)

func newBindingCache[Value any](load func() (Value, bool)) *bindingCache[Value] {
	return &bindingCache[Value]{provider: singleton.MustNew(func(context.Context) (Value, error) {
		return bindingValue(load)
	}, singleton.WithMaxAttempts(one), singleton.WithInitializationTimeout(zero))}
}

func bindingValue[Value any](load func() (Value, bool)) (value Value, err error) {
	defer func() {
		if recover() != nil {
			var empty Value

			value, err = empty, errBindingPanic
		}
	}()

	value, ok := load()
	if !ok {
		var empty Value

		return empty, errBindingsUnavailable
	}

	return value, nil
}

func (cache *bindingCache[T]) get() (T, bool) {
	value, err := cache.provider.Get(context.Background())
	if err != nil {
		cache.provider.Reset()

		return value, false
	}

	return value, true
}

func loadVulkan(loader library) (bindings vulkanFuncs, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}

		releaseFailedBinding(loader, bindings.handle, ok)
	}()

	bindings.handle, ok = loader.open(libVulkan, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if !ok {
		return bindings, false
	}

	if address := loader.symbol(bindings.handle, vkEnumerateVersionName); address != zero {
		loader.register(&bindings.enumerate, address)
	}

	return bindings, true
}

func cachedVulkan(cache *bindingCache[vulkanFuncs]) (version string, ok bool) {
	defer func() {
		if recover() != nil {
			version, ok = "", false
		}
	}()

	bindings, loaded := cache.get()
	if !loaded {
		return "", false
	}

	return queryVulkan(bindings.enumerate)
}

func cachedOpenGL(cache *bindingCache[eglFuncs]) (info domain.OpenGLInfo, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()

	egl, loaded := cache.get()
	if !loaded {
		return info, false
	}

	ok = queryCachedGL(&info, &egl)

	return info, ok
}

func queryCachedGL(info *domain.OpenGLInfo, egl *eglFuncs) bool {
	eglLifecycle.Lock()
	defer eglLifecycle.Unlock()

	runtime.LockOSThread()

	defer runtime.UnlockOSThread()

	return populateBoundGL(info, egl)
}

//nolint:revive // Cleanup explicitly depends on whether initialization successfully transferred handle ownership.
func releaseFailedBinding(loader library, handle uintptr, ok bool) {
	if !ok && handle != zero {
		releaseLibrary(loader, handle)
	}
}
