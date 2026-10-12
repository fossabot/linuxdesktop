//go:build linux && (amd64 || arm64 || 386 || arm || riscv64 || loong64 || ppc64le)

// Gostafa 2026.
// SPDX-License-Identifier: Apache-2.0.

package native

import (
	"reflect"
	"testing"

	"github.com/ebitengine/purego"
	"github.com/gostafa/linuxdesktop/internal/domain"
)

type fakeLibrary struct {
	loaded               bool
	functions            map[string]any
	names                []string
	bindPanic            bool
	opens, binds, closes int
}

func (f *fakeLibrary) open(string, int) (uintptr, bool) { f.opens++; return 7, f.loaded }

func (f *fakeLibrary) release(uintptr) { f.closes++ }

func (f *fakeLibrary) symbol(_ uintptr, name string) uintptr {
	if f.functions[name] == nil {
		return zero
	}
	f.names = append(f.names, name)
	return uintptr(len(f.names))
}

func (f *fakeLibrary) register(target any, address uintptr) {
	f.binds++
	if f.bindPanic {
		panic("invalid signature")
	}
	reflect.ValueOf(target).Elem().Set(reflect.ValueOf(f.functions[f.names[address-1]]))
}

func TestVulkanLoader(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                      string
		loaded, symbol, bindPanic bool
		status                    int32
		want                      string
		ok                        bool
	}{
		{name: "missing library"},
		{name: "version 1.0 loader", loaded: true, want: vkBaseVersion, ok: true},
		{name: "versioned loader", loaded: true, symbol: true, want: "1.3.283", ok: true},
		{name: "enumeration failure", loaded: true, symbol: true, status: -1},
		{name: "invalid binding", loaded: true, symbol: true, bindPanic: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			loader := &fakeLibrary{
				loaded:    test.loaded,
				bindPanic: test.bindPanic,
				functions: map[string]any{},
			}
			if test.symbol {
				loader.functions[vkEnumerateVersionName] = func(version *uint32) int32 {
					*version = 1<<vkMajorShift | 3<<vkMinorShift | 283
					return test.status
				}
			}
			if got, ok := detectVulkan(loader); got != test.want || ok != test.ok {
				t.Fatalf("version = %q, ok = %v", got, ok)
			}
		})
	}
}

func eglFixture() *fakeLibrary {
	return &fakeLibrary{loaded: true, functions: map[string]any{
		"eglGetDisplay":        func(uintptr) uintptr { return 7 },
		"eglQueryAPI":          func() uint32 { return eglOpenGLESAPI },
		"eglGetCurrentDisplay": func() uintptr { return 0 },
		"eglGetCurrentContext": func() uintptr { return 0 },
		"eglGetCurrentSurface": func(int32) uintptr { return 0 },
		"eglInitialize":        func(uintptr, *int32, *int32) uint32 { return 1 },
		"eglQueryString": func(_ uintptr, name int32) string {
			if name == eglVendor {
				return "EGL vendor"
			}
			return "EGL version"
		},
		"eglBindAPI": func(uint32) uint32 { return 1 },
		"eglChooseConfig": func(_ uintptr, _ *int32, config *uintptr, _ int32, count *int32) uint32 {
			*config, *count = 9, 1
			return 1
		},
		"eglCreateContext":  func(uintptr, uintptr, uintptr, *int32) uintptr { return 11 },
		"eglMakeCurrent":    func(uintptr, uintptr, uintptr, uintptr) uint32 { return 1 },
		"eglDestroyContext": func(uintptr, uintptr) uint32 { return 1 },
		"eglTerminate":      func(uintptr) uint32 { return 1 },
		getStringSymbol: func(name uint32) string {
			switch name {
			case glVendor:
				return "GL vendor"
			case glVersion:
				return "GL version"
			default:
				return "GL renderer"
			}
		},
	}}
}

func prepareEGL(loader *fakeLibrary) {
	loader.functions["eglGetProcAddress"] = func(name string) uintptr { return loader.symbol(7, name) }
}

func TestOpenGLLoader(t *testing.T) {
	t.Parallel()
	loader := eglFixture()
	prepareEGL(loader)
	var cleanup []string
	loader.functions["eglMakeCurrent"] = func(_, _, _, ctx uintptr) uint32 {
		if ctx == zero {
			cleanup = append(cleanup, "release")
		}
		return 1
	}
	loader.functions["eglDestroyContext"] = func(display, ctx uintptr) uint32 {
		if display != 7 || ctx != 11 {
			t.Fatal("destroyed wrong context")
		}
		cleanup = append(cleanup, "destroy")
		return 1
	}
	loader.functions["eglTerminate"] = func(display uintptr) uint32 {
		if display != 7 {
			t.Fatal("terminated wrong display")
		}
		cleanup = append(cleanup, "terminate")
		return 1
	}
	want := domain.OpenGLInfo{
		Available: true,
		Vendor:    "GL vendor",
		Version:   "GL version",
		Renderer:  "GL renderer",
	}
	if info, ok := detectOpenGL(loader); !ok || info != want {
		t.Fatalf("info = %+v, ok = %v", info, ok)
	}
	if !reflect.DeepEqual(cleanup, []string{"release", "destroy", "release"}) {
		t.Fatal(cleanup)
	}
}

func TestOpenGLFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(*fakeLibrary)
		want   domain.OpenGLInfo
		ok     bool
	}{
		{name: "missing library", change: func(f *fakeLibrary) { f.loaded = false }},
		{name: "bad EGL binding", change: func(f *fakeLibrary) { f.bindPanic = true }},
		{name: "missing display", change: func(f *fakeLibrary) { f.functions["eglGetDisplay"] = func(uintptr) uintptr { return 0 } }},
		{name: "initialization failure", change: func(f *fakeLibrary) { f.functions["eglInitialize"] = func(uintptr, *int32, *int32) uint32 { return 0 } }},
		{name: "driver panic", change: func(f *fakeLibrary) { f.functions["eglQueryString"] = func(uintptr, int32) string { panic("driver") } }},
		{
			name: "missing GL symbol", change: func(f *fakeLibrary) { delete(f.functions, getStringSymbol) },
			want: domain.OpenGLInfo{Available: true, Vendor: "EGL vendor", Version: ""}, ok: true,
		},
		{name: "empty live vendor and version", change: func(f *fakeLibrary) {
			f.functions[getStringSymbol] = func(name uint32) string {
				if name == glRenderer {
					return "renderer"
				}
				return ""
			}
		}, want: domain.OpenGLInfo{Available: true, Vendor: "EGL vendor", Version: "", Renderer: "renderer"}, ok: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			loader := eglFixture()
			prepareEGL(loader)
			test.change(loader)
			if info, ok := detectOpenGL(loader); info != test.want || ok != test.ok {
				t.Fatalf("info = %+v, ok = %v", info, ok)
			}
		})
	}
}

func TestContextCreationFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		bind    uint32
		configs int32
		ctx     uintptr
	}{
		{"bind rejected", 0, 1, 11},
		{"no config", 1, 0, 11},
		{"no context", 1, 1, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			loader := eglFixture()
			prepareEGL(loader)
			loader.functions["eglBindAPI"] = func(uint32) uint32 { return test.bind }
			loader.functions["eglChooseConfig"] = func(_ uintptr, _ *int32, _ *uintptr, _ int32, count *int32) uint32 { *count = test.configs; return 1 }
			loader.functions["eglCreateContext"] = func(uintptr, uintptr, uintptr, *int32) uintptr { return test.ctx }
			egl, ok := loadEGL(loader)
			if !ok {
				t.Fatal("failed to load fixture")
			}
			if info, got := currentGL(&egl, 7); got || info.Available {
				t.Fatalf("info = %+v, ok = %v", info, got)
			}
		})
	}
}

func TestGetStringBindingRecovery(t *testing.T) {
	t.Parallel()
	loader := eglFixture()
	prepareEGL(loader)
	egl, ok := loadEGL(loader)
	if !ok {
		t.Fatal("failed to load fixture")
	}
	loader.bindPanic = true
	if getString, bound := bindGetString(&egl); getString != nil || bound {
		t.Fatal("invalid binding succeeded")
	}
}

func TestSystemLibrary(t *testing.T) {
	t.Parallel()
	loader := systemLibrary{}
	if _, ok := loader.open("/nonexistent/linuxdesktop-test.so", purego.RTLD_NOW); ok {
		t.Fatal("missing library opened")
	}
	lib, ok := loader.open("libc.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if !ok {
		// musl exposes libc.so rather than glibc's versioned soname.
		lib, ok = loader.open("libc.so", purego.RTLD_NOW|purego.RTLD_LOCAL)
	}
	if !ok {
		t.Fatal("libc unavailable")
	}
	defer func() {
		if err := purego.Dlclose(lib); err != nil {
			t.Fatal(err)
		}
	}()
	if loader.symbol(lib, "linuxdesktop_missing_symbol") != zero {
		t.Fatal("missing symbol resolved")
	}
	var getpid func() int32
	loader.register(&getpid, loader.symbol(lib, "getpid"))
	if getpid() <= 0 {
		t.Fatal("native binding returned invalid PID")
	}
	Vulkan()
	OpenGL()
}
