//go:build linux && (amd64 || arm64 || 386 || arm || riscv64 || loong64 || ppc64le)

// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package native

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ebitengine/purego"
)

func TestCachedVulkanQueriesFreshValues(t *testing.T) {
	t.Parallel()
	packed := uint32(1 << vkMajorShift)
	loader := &fakeLibrary{loaded: true, functions: map[string]any{
		vkEnumerateVersionName: func(version *uint32) int32 { *version = packed; return 0 },
	}}
	cache := newBindingCache(func() (vulkanFuncs, bool) { return loadVulkan(loader) })
	for _, want := range []string{"1.0.0", "1.1.0"} {
		if got, ok := cachedVulkan(cache); !ok || got != want {
			t.Fatal(got, ok)
		}
		packed += 1 << vkMinorShift
	}
	if loader.opens != 1 || loader.binds != 1 {
		t.Fatal(loader.opens, loader.binds)
	}
}

func TestCachedOpenGLQueriesFreshValues(t *testing.T) {
	t.Parallel()
	loader := eglFixture()
	prepareEGL(loader)
	cache := newBindingCache(func() (eglFuncs, bool) { return loadEGL(loader) })
	first, ok := cachedOpenGL(cache)
	if !ok || first.Renderer != "GL renderer" {
		t.Fatal(first, ok)
	}
	loader.functions[getStringSymbol] = func(uint32) string { return "new driver result" }
	second, ok := cachedOpenGL(cache)
	if !ok || second.Renderer != "new driver result" {
		t.Fatal(second, ok)
	}
	if loader.opens != 1 || loader.binds != len(eglTargets(new(eglFuncs)))+2 {
		t.Fatal(loader.opens, loader.binds)
	}
}

func TestBindingFailureRecovery(t *testing.T) {
	t.Parallel()
	for _, panicFirst := range []bool{false, true} {
		var loads int
		cache := newBindingCache(func() (int, bool) {
			loads++
			if loads == 1 {
				if panicFirst {
					panic("binding failed")
				}
				return 0, false
			}
			return 7, true
		})
		if _, ok := cache.get(); ok {
			t.Fatal("failed load succeeded")
		}
		for range 2 {
			if value, ok := cache.get(); !ok || value != 7 {
				t.Fatal(value, ok)
			}
		}
		if loads != 2 {
			t.Fatal(loads)
		}
	}
}

func TestFailedNativeBindingsReleaseHandle(t *testing.T) {
	t.Parallel()
	for _, egl := range []bool{false, true} {
		loader := eglFixture()
		prepareEGL(loader)
		loader.functions[vkEnumerateVersionName] = func(*uint32) int32 { return 0 }
		loader.bindPanic = true
		if egl {
			cache := newBindingCache(func() (eglFuncs, bool) { return loadEGL(loader) })
			if _, ok := cache.get(); ok {
				t.Fatal("invalid binding succeeded")
			}
			loader.bindPanic = false
			if _, ok := cache.get(); !ok {
				t.Fatal("EGL did not recover")
			}
		} else {
			cache := newBindingCache(func() (vulkanFuncs, bool) { return loadVulkan(loader) })
			if _, ok := cache.get(); ok {
				t.Fatal("invalid binding succeeded")
			}
			loader.bindPanic = false
			if _, ok := cache.get(); !ok {
				t.Fatal("Vulkan did not recover")
			}
		}
		if loader.opens != 2 || loader.closes != 1 {
			t.Fatal(loader.opens, loader.closes)
		}
	}
}

func TestConcurrentEGLQueriesSerializeLifecycle(t *testing.T) {
	t.Parallel()
	loader := eglFixture()
	prepareEGL(loader)
	var active, maximum atomic.Int32
	loader.functions[getStringSymbol] = func(name uint32) string {
		current := active.Add(1)
		maximum.Store(max(maximum.Load(), current))
		defer active.Add(-1)
		if name == glRenderer {
			return "GL renderer"
		}
		return "GL value"
	}
	cache := newBindingCache(func() (eglFuncs, bool) { return loadEGL(loader) })
	var callers sync.WaitGroup
	failures := make(chan error, 16)
	for range cap(failures) {
		callers.Go(func() {
			if info, ok := cachedOpenGL(cache); !ok || info.Renderer != "GL renderer" {
				failures <- fmt.Errorf("unexpected graphics result: %+v, %v", info, ok)
			}
		})
	}
	callers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if maximum.Load() != 1 || active.Load() != 0 || loader.opens != 1 {
		t.Fatal(maximum.Load(), active.Load(), loader.opens)
	}
}

func TestCachedVulkanFallbackAndPanic(t *testing.T) {
	t.Parallel()
	loader := &fakeLibrary{loaded: true, functions: map[string]any{}}
	cache := newBindingCache(func() (vulkanFuncs, bool) { return loadVulkan(loader) })
	if got, ok := cachedVulkan(cache); !ok || got != vkBaseVersion {
		t.Fatal(got, ok)
	}
	cache = newBindingCache(func() (vulkanFuncs, bool) {
		return vulkanFuncs{enumerate: func(*uint32) int32 { panic("driver failure") }}, true
	})
	if got, ok := cachedVulkan(cache); ok || got != "" {
		t.Fatal(got, ok)
	}
}

func TestCachedVulkanUnavailableRetries(t *testing.T) {
	t.Parallel()
	loader := &fakeLibrary{functions: map[string]any{}}
	cache := newBindingCache(func() (vulkanFuncs, bool) { return loadVulkan(loader) })
	if got, ok := cachedVulkan(cache); ok || got != "" {
		t.Fatal("unavailable library succeeded", got, ok)
	}
	loader.loaded = true
	for range 2 {
		if got, ok := cachedVulkan(cache); !ok || got != vkBaseVersion {
			t.Fatal("Vulkan did not recover", got, ok)
		}
	}
	if loader.opens != 2 {
		t.Fatal("successful binding was not cached", loader.opens)
	}
}

func TestCachedOpenGLUnavailableAndPanic(t *testing.T) {
	t.Parallel()
	cache := newBindingCache(func() (eglFuncs, bool) { return eglFuncs{}, false })
	if _, ok := cachedOpenGL(cache); ok {
		t.Fatal("unavailable library succeeded")
	}
	loader := eglFixture()
	prepareEGL(loader)
	loader.functions[getStringSymbol] = func(uint32) string { panic("driver failure") }
	cache = newBindingCache(func() (eglFuncs, bool) { return loadEGL(loader) })
	if info, ok := cachedOpenGL(cache); ok || info.Vendor != "EGL vendor" {
		t.Fatal("panic fallback lost partial results", info, ok)
	}
}

func TestSystemLibraryRelease(t *testing.T) {
	t.Parallel()
	loader := systemLibrary{}
	handle, ok := loader.open("libc.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if !ok {
		t.Fatal("Linux C library unavailable")
	}
	loader.release(handle)
}

func BenchmarkNativeBindings(b *testing.B) {
	for _, kind := range []string{"EGL", "Vulkan"} {
		for _, cached := range []bool{false, true} {
			b.Run(fmt.Sprintf("%s/cached=%v", kind, cached), func(b *testing.B) {
				loader := eglFixture()
				prepareEGL(loader)
				loader.functions[vkEnumerateVersionName] = func(*uint32) int32 { return 0 }
				load := func() (any, bool) {
					if kind == "EGL" {
						return loadEGL(loader)
					}
					return loadVulkan(loader)
				}
				cache := newBindingCache(load)
				if cached {
					if _, ok := cache.get(); !ok {
						b.Fatal("warmup failed")
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if !cached {
						loader.names = loader.names[:0]
					}
					var ok bool
					if cached {
						_, ok = cache.get()
					} else {
						_, ok = load()
					}
					if !ok {
						b.Fatal("load failed")
					}
				}
				b.ReportMetric(float64(loader.opens), "loads")
				b.ReportMetric(float64(loader.binds), "bindings")
			})
		}
	}
}
