//go:build linux && (amd64 || arm64 || 386 || arm || riscv64 || loong64 || ppc64le)

package native

import "testing"

func TestSharedEGLDisplayRetainedAndInitializationRetried(t *testing.T) {
	t.Parallel()
	loader := eglFixture()
	prepareEGL(loader)
	initializations, terminations := 0, 0
	loader.functions["eglInitialize"] = func(uintptr, *int32, *int32) uint32 {
		initializations++
		if initializations == 1 {
			return 0
		}
		return 1
	}
	loader.functions["eglTerminate"] = func(uintptr) uint32 { terminations++; return 1 }
	cache := newBindingCache(func() (eglFuncs, bool) { return loadEGL(loader) })
	if _, ok := cachedOpenGL(cache); ok {
		t.Fatal("failed initialization succeeded")
	}
	for range 2 {
		if _, ok := cachedOpenGL(cache); !ok {
			t.Fatal("successful initialization did not recover")
		}
	}
	if initializations != 2 || terminations != 0 {
		t.Fatal(initializations, terminations)
	}
}

func TestEGLApplicationStateRestored(t *testing.T) {
	t.Parallel()
	for _, panicDriver := range []bool{false, true} {
		loader := eglFixture()
		prepareEGL(loader)
		var restored bool
		api := eglOpenGLESAPI
		loader.functions["eglQueryAPI"] = func() uint32 { return api }
		loader.functions["eglBindAPI"] = func(value uint32) uint32 { api = value; return 1 }
		loader.functions["eglGetCurrentDisplay"] = func() uintptr { return 30 }
		loader.functions["eglGetCurrentContext"] = func() uintptr { return 31 }
		loader.functions["eglGetCurrentSurface"] = func(which int32) uintptr {
			if which == eglDraw {
				return 32
			}
			return 33
		}
		loader.functions["eglMakeCurrent"] = func(display, draw, read, ctx uintptr) uint32 {
			if display == 30 {
				restored = draw == 32 && read == 33 && ctx == 31 && api == eglOpenGLESAPI
			}
			return 1
		}
		destroyed := 0
		loader.functions["eglDestroyContext"] = func(_ uintptr, ctx uintptr) uint32 {
			if ctx != 11 {
				t.Fatalf("destroyed application context %d", ctx)
			}
			destroyed++
			return 1
		}
		if panicDriver {
			loader.functions[getStringSymbol] = func(uint32) string { panic("driver error") }
		}
		_, _ = detectOpenGL(loader)
		if !restored || destroyed != 1 {
			t.Fatal(restored, destroyed)
		}
	}
}
