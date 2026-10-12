//go:build linux && (amd64 || arm64 || 386 || arm || riscv64 || loong64 || ppc64le)

package native

// Native library names and graphics API values.
const (
	// Native calls return a null address or EGL_FALSE to indicate failure.
	zero = 0
	// EGL_PBUFFER_BIT and the requested configuration count both equal one.
	one                = 1
	eglESVersion int32 = 2
	decimalBase        = 10
	libEGL             = "libEGL.so.1"
	libVulkan          = "libvulkan.so.1"

	// EGL enumerants.
	eglVendor int32 = 0x3053

	eglOpenGLAPI   uint32 = 0x30A2
	eglOpenGLESAPI uint32 = 0x30A0

	eglSurfaceType    int32 = 0x3033
	eglRenderableType int32 = 0x3040
	eglOpenGLBit      int32 = 0x0008
	eglOpenGLES2Bit   int32 = 0x0004
	eglContextVersion int32 = 0x3098
	eglNone           int32 = 0x3038
	eglDraw           int32 = 0x3059
	eglRead           int32 = 0x305A

	// OpenGL enumerants.
	glVendor   uint32 = 0x1F00
	glRenderer uint32 = 0x1F01
	glVersion  uint32 = 0x1F02

	// Vulkan. vkEnumerateInstanceVersion is absent from a 1.0 loader, where the
	// answer is 1.0.0 by definition.
	vkEnumerateVersionName = "vkEnumerateInstanceVersion"
	vkBaseVersion          = "1.0.0"

	// These decode a packed VK_MAKE_API_VERSION value.
	vkVariantShift = 29
	vkMajorShift   = 22
	vkMajorMask    = 0x7F
	vkMinorShift   = 12
	vkMinorMask    = 0x3FF
	vkPatchMask    = 0xFFF

	// getStringSymbol is resolved through eglGetProcAddress, which works for both
	// the desktop and the ES libraries without knowing which one is loaded.
	getStringSymbol = "glGetString"
)
