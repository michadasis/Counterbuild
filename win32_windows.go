//go:build windows

package main

import (
	"errors"
	"image"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")

	pRegisterClassExW       = user32.NewProc("RegisterClassExW")
	pCreateWindowExW        = user32.NewProc("CreateWindowExW")
	pDefWindowProcW         = user32.NewProc("DefWindowProcW")
	pGetMessageW            = user32.NewProc("GetMessageW")
	pTranslateMessage       = user32.NewProc("TranslateMessage")
	pDispatchMessageW       = user32.NewProc("DispatchMessageW")
	pPostMessageW           = user32.NewProc("PostMessageW")
	pPostQuitMessage        = user32.NewProc("PostQuitMessage")
	pShowWindow             = user32.NewProc("ShowWindow")
	pUpdateLayeredWindow    = user32.NewProc("UpdateLayeredWindow")
	pGetDC                  = user32.NewProc("GetDC")
	pReleaseDC              = user32.NewProc("ReleaseDC")
	pSetTimer               = user32.NewProc("SetTimer")
	pRegisterHotKey         = user32.NewProc("RegisterHotKey")
	pUnregisterHotKey       = user32.NewProc("UnregisterHotKey")
	pFindWindowW            = user32.NewProc("FindWindowW")
	pGetForegroundWindow    = user32.NewProc("GetForegroundWindow")
	pGetClientRect          = user32.NewProc("GetClientRect")
	pClientToScreen         = user32.NewProc("ClientToScreen")
	pSetWindowPos           = user32.NewProc("SetWindowPos")
	pCreatePopupMenu        = user32.NewProc("CreatePopupMenu")
	pAppendMenuW            = user32.NewProc("AppendMenuW")
	pTrackPopupMenu         = user32.NewProc("TrackPopupMenu")
	pDestroyMenu            = user32.NewProc("DestroyMenu")
	pSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	pGetCursorPos           = user32.NewProc("GetCursorPos")
	pCreateIconIndirect     = user32.NewProc("CreateIconIndirect")
	pMessageBoxW            = user32.NewProc("MessageBoxW")
	pSetProcessDpiAwareCtx  = user32.NewProc("SetProcessDpiAwarenessContext")
	pSetProcessDPIAware     = user32.NewProc("SetProcessDPIAware")
	pGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	pIsIconic               = user32.NewProc("IsIconic")
	pRegisterWindowMessageW = user32.NewProc("RegisterWindowMessageW")

	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
	pCreateBitmap       = gdi32.NewProc("CreateBitmap")

	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	pCreateMutexW     = kernel32.NewProc("CreateMutexW")

	pGetCurrentProcess     = kernel32.NewProc("GetCurrentProcess")
	pSetPriorityClass      = kernel32.NewProc("SetPriorityClass")
	pSetProcessInformation = kernel32.NewProc("SetProcessInformation")

	pShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	pShellExecuteW    = shell32.NewProc("ShellExecuteW")

	pRegSetValueExW  = advapi32.NewProc("RegSetValueExW")
	pRegDeleteValueW = advapi32.NewProc("RegDeleteValueW")
)

const (
	wmDestroy     = 0x0002
	wmClose       = 0x0010
	wmEndSession  = 0x0016
	wmNull        = 0x0000
	wmTimer       = 0x0113
	wmHotkey      = 0x0312
	wmCommand     = 0x0111
	wmLButtonUp   = 0x0202
	wmRButtonUp   = 0x0205
	wmContextMenu = 0x007B
	wmApp         = 0x8000
	wmAppFrame    = wmApp + 1
	wmAppTray     = wmApp + 2

	wsPopup         = 0x80000000
	wsExLayered     = 0x00080000
	wsExTransparent = 0x00000020
	wsExTopmost     = 0x00000008
	wsExToolWindow  = 0x00000080
	wsExNoActivate  = 0x08000000

	swHide           = 0
	swShowNoActivate = 4

	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoActivate = 0x0010

	ulwAlpha    = 0x02
	acSrcOver   = 0x00
	acSrcAlpha  = 0x01
	biRGB       = 0
	dibRGBColor = 0

	modAlt      = 0x0001
	modControl  = 0x0002
	modShift    = 0x0004
	modWin      = 0x0008
	modNoRepeat = 0x4000

	nimAdd        = 0
	nimModify     = 1
	nimDelete     = 2
	nimSetVersion = 4
	nifMessage    = 0x01
	nifIcon       = 0x02
	nifTip        = 0x04
	nifInfo       = 0x10
	niifInfo      = 0x01

	mfString    = 0x0000
	mfGrayed    = 0x0001
	mfChecked   = 0x0008
	mfPopup     = 0x0010
	mfSeparator = 0x0800

	tpmReturnCmd   = 0x0100
	tpmRightButton = 0x0002
	tpmBottomAlign = 0x0020

	smCxScreen   = 0
	smCyScreen   = 1
	smCxSmIcon   = 49
	errExists    = 183
	hkeyCurUser  = 0x80000001
	keySetValue  = 0x0002
	keyQueryVal  = 0x0001
	regSZ        = 1
	mbOK         = 0x00000000
	mbIconInfo   = 0x00000040
	mbIconError  = 0x00000010
	swShowNormal = 1
)

var hwndTopmost = ^uintptr(0) // (HWND)-1

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type pointT struct{ x, y int32 }
type sizeT struct{ cx, cy int32 }
type rectT struct{ left, top, right, bottom int32 }

type msgT struct {
	hwnd     uintptr
	message  uint32
	wParam   uintptr
	lParam   uintptr
	time     uint32
	pt       pointT
	lPrivate uint32
}

type blendFunction struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type bitmapInfo struct {
	bmiHeader bitmapInfoHeader
	bmiColors [1]uint32
}

type iconInfo struct {
	fIcon    int32
	xHotspot uint32
	yHotspot uint32
	hbmMask  uintptr
	hbmColor uintptr
}

type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func copyU16(dst []uint16, s string) {
	src, _ := syscall.UTF16FromString(s)
	n := copy(dst[:len(dst)-1], src)
	dst[n] = 0
}

func messageBox(title, text string, flags uintptr) {
	pMessageBoxW.Call(0, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16(title))), flags)
}

// yieldToGame runs Counterbuild below normal priority, so whenever the game
// needs the CPU it gets it first, and asks Windows to run it in efficiency
// mode (EcoQoS): lower clock speeds or efficient cores, which on a laptop
// leaves more of the shared power and heat budget to the game.
func yieldToGame() {
	const (
		belowNormalPriorityClass = 0x00004000
		processPowerThrottling   = 4 // PROCESS_INFORMATION_CLASS
		throttleExecutionSpeed   = 1 // PROCESS_POWER_THROTTLING_EXECUTION_SPEED
	)
	self, _, _ := pGetCurrentProcess.Call()
	pSetPriorityClass.Call(self, belowNormalPriorityClass)
	if pSetProcessInformation.Find() == nil { // Windows 8 and newer; ignored before Windows 10 1709
		state := struct{ version, controlMask, stateMask uint32 }{1, throttleExecutionSpeed, throttleExecutionSpeed}
		pSetProcessInformation.Call(self, processPowerThrottling, uintptr(unsafe.Pointer(&state)), unsafe.Sizeof(state))
	}
}

func setDPIAware() {
	if pSetProcessDpiAwareCtx.Find() == nil {
		const perMonitorV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
		if r, _, _ := pSetProcessDpiAwareCtx.Call(perMonitorV2); r != 0 {
			return
		}
	}
	if pSetProcessDPIAware.Find() == nil {
		pSetProcessDPIAware.Call()
	}
}

// dib is a 32-bit top-down DIB section selected into a memory DC.
type dib struct {
	dc   uintptr
	bmp  uintptr
	old  uintptr
	bits unsafe.Pointer
	w, h int
}

func newDIB(w, h int) (*dib, error) {
	screen, _, _ := pGetDC.Call(0)
	defer pReleaseDC.Call(0, screen)
	dc, _, _ := pCreateCompatibleDC.Call(screen)
	if dc == 0 {
		return nil, errors.New("CreateCompatibleDC failed")
	}
	bi := bitmapInfo{bmiHeader: bitmapInfoHeader{
		biSize: uint32(unsafe.Sizeof(bitmapInfoHeader{})), biWidth: int32(w), biHeight: -int32(h),
		biPlanes: 1, biBitCount: 32, biCompression: biRGB,
	}}
	var bits unsafe.Pointer
	bmp, _, _ := pCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&bi)), dibRGBColor, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		pDeleteDC.Call(dc)
		return nil, errors.New("CreateDIBSection failed")
	}
	old, _, _ := pSelectObject.Call(dc, bmp)
	return &dib{dc: dc, bmp: bmp, old: old, bits: bits, w: w, h: h}, nil
}

func (d *dib) free() {
	if d == nil {
		return
	}
	pSelectObject.Call(d.dc, d.old)
	pDeleteObject.Call(d.bmp)
	pDeleteDC.Call(d.dc)
}

// fill copies a premultiplied RGBA image into the DIB as BGRA.
func (d *dib) fill(img *image.RGBA, premultiplied bool) {
	dst := unsafe.Slice((*byte)(d.bits), d.w*d.h*4)
	for y := 0; y < d.h; y++ {
		src := img.Pix[y*img.Stride : y*img.Stride+d.w*4]
		row := dst[y*d.w*4 : (y+1)*d.w*4]
		for x := 0; x < d.w*4; x += 4 {
			r, g, b, a := src[x], src[x+1], src[x+2], src[x+3]
			if !premultiplied && a != 0 && a != 255 {
				r = uint8(uint16(r) * 255 / uint16(a))
				g = uint8(uint16(g) * 255 / uint16(a))
				b = uint8(uint16(b) * 255 / uint16(a))
			}
			row[x], row[x+1], row[x+2], row[x+3] = b, g, r, a
		}
	}
}

// makeIcon builds an HICON from an RGBA image.
func makeIcon(img *image.RGBA) uintptr {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	d, err := newDIB(w, h)
	if err != nil {
		return 0
	}
	d.fill(img, false)
	pSelectObject.Call(d.dc, d.old)
	mask, _, _ := pCreateBitmap.Call(uintptr(w), uintptr(h), 1, 1, 0)
	ii := iconInfo{fIcon: 1, hbmMask: mask, hbmColor: d.bmp}
	icon, _, _ := pCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	pDeleteObject.Call(mask)
	pDeleteObject.Call(d.bmp)
	pDeleteDC.Call(d.dc)
	return icon
}

func regSetRun(name, value string) error {
	var k syscall.Handle
	if err := syscall.RegOpenKeyEx(hkeyCurUser, u16(`Software\Microsoft\Windows\CurrentVersion\Run`), 0, keySetValue, &k); err != nil {
		return err
	}
	defer syscall.RegCloseKey(k)
	if value == "" {
		r, _, _ := pRegDeleteValueW.Call(uintptr(k), uintptr(unsafe.Pointer(u16(name))))
		if r != 0 && r != 2 {
			return syscall.Errno(r)
		}
		return nil
	}
	data, _ := syscall.UTF16FromString(value)
	r, _, _ := pRegSetValueExW.Call(uintptr(k), uintptr(unsafe.Pointer(u16(name))), 0, regSZ,
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2))
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

func regGetRun(name string) string {
	var k syscall.Handle
	if err := syscall.RegOpenKeyEx(hkeyCurUser, u16(`Software\Microsoft\Windows\CurrentVersion\Run`), 0, keyQueryVal, &k); err != nil {
		return ""
	}
	defer syscall.RegCloseKey(k)
	var typ, n uint32
	if syscall.RegQueryValueEx(k, u16(name), nil, &typ, nil, &n) != nil || n == 0 {
		return ""
	}
	buf := make([]uint16, n/2+1)
	if syscall.RegQueryValueEx(k, u16(name), nil, &typ, (*byte)(unsafe.Pointer(&buf[0])), &n) != nil {
		return ""
	}
	return syscall.UTF16ToString(buf)
}
