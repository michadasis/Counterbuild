//go:build windows

package main

// Reading the augment cards: capture the middle of the game window with GDI
// and read it with the OCR engine built into Windows 10/11
// (Windows.Media.Ocr), called through WinRT's COM interfaces directly so no
// runtime or cgo is needed. Nothing is saved or sent anywhere.

import (
	"errors"
	"fmt"
	"image"
	"math"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"counterbuild/app"

	xdraw "golang.org/x/image/draw"
)

var (
	combase = syscall.NewLazyDLL("combase.dll")

	pRoInitialize              = combase.NewProc("RoInitialize")
	pRoGetActivationFactory    = combase.NewProc("RoGetActivationFactory")
	pWindowsCreateString       = combase.NewProc("WindowsCreateString")
	pWindowsDeleteString       = combase.NewProc("WindowsDeleteString")
	pWindowsGetStringRawBuffer = combase.NewProc("WindowsGetStringRawBuffer")

	pBitBlt          = gdi32.NewProc("BitBlt")
	pGetWindowRect   = user32.NewProc("GetWindowRect")
	pIsWindowVisible = user32.NewProc("IsWindowVisible")
)

type guid struct {
	d1     uint32
	d2, d3 uint16
	d4     [8]byte
}

var (
	iidOcrEngineStatics      = guid{0x5BFFA85A, 0x3384, 0x3540, [8]byte{0x99, 0x40, 0x69, 0x91, 0x20, 0xD4, 0x28, 0xA8}}
	iidSoftwareBitmapFactory = guid{0xC99FEB69, 0x2D62, 0x4D47, [8]byte{0xA6, 0xB3, 0x4F, 0xDB, 0x6A, 0x07, 0xFD, 0xF8}}
	iidLanguageFactory       = guid{0x9B0252AC, 0x0C27, 0x44F8, [8]byte{0xB7, 0x92, 0x97, 0x93, 0xFB, 0x66, 0xC6, 0x3E}}
	iidMemoryBuffer          = guid{0xFBC4DD2A, 0x245B, 0x11E4, [8]byte{0xAF, 0x98, 0x68, 0x94, 0x23, 0x26, 0x0C, 0xF8}}
	iidClosable              = guid{0x30D5A829, 0x7FA4, 0x4026, [8]byte{0x83, 0xBB, 0xD7, 0x5B, 0xAE, 0x4E, 0xA9, 0x9E}}
	iidAsyncInfo             = guid{0x00000036, 0x0000, 0x0000, [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidMemoryBufferByteAcc   = guid{0x5B0D3235, 0x4DBA, 0x4D44, [8]byte{0x86, 0x5E, 0x8F, 0x1D, 0x0E, 0x4F, 0xD0, 0x4D}}
)

// Vtable slots. Every WinRT interface starts with IUnknown (0-2) and
// IInspectable (3-5); the interface's own methods follow in metadata order.
const (
	slotQueryInterface = 0
	slotRelease        = 2

	slotLanguageCreate = 6 // ILanguageFactory.CreateLanguage

	slotOcrIsLanguageSupported = 8  // IOcrEngineStatics.IsLanguageSupported
	slotOcrTryCreateFromLang   = 9  // IOcrEngineStatics.TryCreateFromLanguage
	slotOcrTryCreateFromUser   = 10 // IOcrEngineStatics.TryCreateFromUserProfileLanguages
	slotOcrRecognizeAsync      = 6  // IOcrEngine.RecognizeAsync

	slotBitmapCreate        = 6  // ISoftwareBitmapFactory.Create
	slotBitmapLockBuffer    = 15 // ISoftwareBitmap.LockBuffer
	slotBufferPlaneDesc     = 7  // IBitmapBuffer.GetPlaneDescription
	slotMemCreateReference  = 6  // IMemoryBuffer.CreateReference
	slotByteAccessGetBuffer = 3  // IMemoryBufferByteAccess.GetBuffer (plain COM)
	slotClose               = 6  // IClosable.Close

	slotAsyncStatus     = 7 // IAsyncInfo.get_Status
	slotAsyncGetResults = 8 // IAsyncOperation<T>.GetResults

	slotResultLines     = 6 // IOcrResult.get_Lines
	slotResultTextAngle = 7 // IOcrResult.get_TextAngle (IReference<double>, may be null)
	slotReferenceValue  = 6 // IReference<T>.get_Value
	slotVectorGetAt     = 6 // IVectorView<T>.GetAt
	slotVectorSize      = 7 // IVectorView<T>.get_Size
	slotLineWords       = 6 // IOcrLine.get_Words
	slotWordRect        = 6 // IOcrWord.get_BoundingRect
	slotWordText        = 7 // IOcrWord.get_Text

	pixelFormatBgra8 = 87
	bufferModeWrite  = 2
	srcCopy          = 0x00CC0020
)

// ptrOf turns an address returned by a system call into a pointer.
func ptrOf(u uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&u)) }

func vcall(obj unsafe.Pointer, slot int, args ...uintptr) error {
	vt := *(**[64]uintptr)(obj)
	r, _, _ := syscall.SyscallN(vt[slot], append([]uintptr{uintptr(obj)}, args...)...)
	if int32(r) < 0 {
		return fmt.Errorf("HRESULT 0x%08X", uint32(r))
	}
	return nil
}

func release(obj unsafe.Pointer) {
	if obj != nil {
		vcall(obj, slotRelease)
	}
}

func queryInterface(obj unsafe.Pointer, iid *guid) (unsafe.Pointer, error) {
	var out unsafe.Pointer
	if err := vcall(obj, slotQueryInterface, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out))); err != nil {
		return nil, err
	}
	return out, nil
}

func closeObj(obj unsafe.Pointer) {
	if c, err := queryInterface(obj, &iidClosable); err == nil {
		vcall(c, slotClose)
		release(c)
	}
}

func newHString(s string) (uintptr, error) {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return 0, err
	}
	var h uintptr
	r, _, _ := pWindowsCreateString.Call(uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&h)))
	if int32(r) < 0 {
		return 0, fmt.Errorf("WindowsCreateString: 0x%08X", uint32(r))
	}
	return h, nil
}

func hstringText(h uintptr) string {
	if h == 0 {
		return ""
	}
	var n uint32
	p, _, _ := pWindowsGetStringRawBuffer.Call(h, uintptr(unsafe.Pointer(&n)))
	if p == 0 || n == 0 {
		return ""
	}
	return syscall.UTF16ToString(unsafe.Slice((*uint16)(ptrOf(p)), n))
}

func activationFactory(class string, iid *guid) (unsafe.Pointer, error) {
	h, err := newHString(class)
	if err != nil {
		return nil, err
	}
	defer pWindowsDeleteString.Call(h)
	var f unsafe.Pointer
	r, _, _ := pRoGetActivationFactory.Call(h, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&f)))
	if int32(r) < 0 || f == nil {
		return nil, fmt.Errorf("%s: 0x%08X", class, uint32(r))
	}
	return f, nil
}

type ocrWord struct {
	text       string
	x, y, w, h float32
}

type ocrReq struct {
	pix  []byte // BGRA, top-down, w*4 bytes per row
	w, h int
	out  chan ocrResp
}

type ocrResp struct {
	lines [][]ocrWord
	err   error
}

// ocrEngine runs all WinRT calls on one locked OS thread.
type ocrEngine struct {
	reqs    chan ocrReq
	english bool // false when Windows has no English text recognition
}

func startOCR() (*ocrEngine, error) {
	e := &ocrEngine{reqs: make(chan ocrReq)}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		var eng, fac unsafe.Pointer
		err := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("OCR setup crashed: %v", r)
				}
			}()
			pRoInitialize.Call(1) // multithreaded; "already initialized" is fine too
			eng, fac, e.english, err = createOCR()
			return err
		}()
		ready <- err
		if err != nil {
			return
		}
		for r := range e.reqs {
			var resp ocrResp
			func() {
				defer func() {
					if p := recover(); p != nil {
						resp.err = fmt.Errorf("OCR crashed: %v", p)
					}
				}()
				resp.lines, resp.err = recognize(eng, fac, r.pix, r.w, r.h)
			}()
			r.out <- resp
		}
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	return e, nil
}

func (e *ocrEngine) read(pix []byte, w, h int) ([][]ocrWord, error) {
	out := make(chan ocrResp, 1)
	e.reqs <- ocrReq{pix, w, h, out}
	r := <-out
	return r.lines, r.err
}

// createOCR makes an English OCR engine when the English language pack is
// installed (augment names are English), else one for the user's languages.
func createOCR() (eng, bitmapFactory unsafe.Pointer, english bool, err error) {
	if err := pRoInitialize.Find(); err != nil {
		return nil, nil, false, errors.New("Windows OCR needs Windows 10 or newer")
	}
	statics, err := activationFactory("Windows.Media.Ocr.OcrEngine", &iidOcrEngineStatics)
	if err != nil {
		return nil, nil, false, err
	}
	defer release(statics)
	if lf, err := activationFactory("Windows.Globalization.Language", &iidLanguageFactory); err == nil {
		for _, tag := range []string{"en-US", "en-GB", "en"} {
			h, _ := newHString(tag)
			var lang unsafe.Pointer
			if vcall(lf, slotLanguageCreate, h, uintptr(unsafe.Pointer(&lang))) == nil && lang != nil {
				var ok uint8
				if vcall(statics, slotOcrIsLanguageSupported, uintptr(lang), uintptr(unsafe.Pointer(&ok))) == nil && ok != 0 {
					vcall(statics, slotOcrTryCreateFromLang, uintptr(lang), uintptr(unsafe.Pointer(&eng)))
				}
				release(lang)
			}
			pWindowsDeleteString.Call(h)
			if eng != nil {
				english = true
				break
			}
		}
		release(lf)
	}
	if eng == nil {
		vcall(statics, slotOcrTryCreateFromUser, uintptr(unsafe.Pointer(&eng)))
	}
	if eng == nil {
		return nil, nil, false, errors.New("no text recognition language is installed in Windows")
	}
	bitmapFactory, err = activationFactory("Windows.Graphics.Imaging.SoftwareBitmap", &iidSoftwareBitmapFactory)
	if err != nil {
		release(eng)
		return nil, nil, false, err
	}
	return eng, bitmapFactory, english, nil
}

type planeDesc struct {
	start, width, height, stride int32
}

// fillBitmap copies BGRA pixels into a SoftwareBitmap.
func fillBitmap(bmp unsafe.Pointer, pix []byte, w, h int) error {
	var buf unsafe.Pointer
	if err := vcall(bmp, slotBitmapLockBuffer, bufferModeWrite, uintptr(unsafe.Pointer(&buf))); err != nil {
		return fmt.Errorf("LockBuffer: %w", err)
	}
	defer func() { closeObj(buf); release(buf) }()
	var pd planeDesc
	if err := vcall(buf, slotBufferPlaneDesc, 0, uintptr(unsafe.Pointer(&pd))); err != nil {
		return fmt.Errorf("GetPlaneDescription: %w", err)
	}
	mb, err := queryInterface(buf, &iidMemoryBuffer)
	if err != nil {
		return fmt.Errorf("IMemoryBuffer: %w", err)
	}
	defer release(mb)
	var ref unsafe.Pointer
	if err := vcall(mb, slotMemCreateReference, uintptr(unsafe.Pointer(&ref))); err != nil {
		return fmt.Errorf("CreateReference: %w", err)
	}
	defer func() { closeObj(ref); release(ref) }()
	acc, err := queryInterface(ref, &iidMemoryBufferByteAcc)
	if err != nil {
		return fmt.Errorf("IMemoryBufferByteAccess: %w", err)
	}
	defer release(acc)
	var data unsafe.Pointer
	var capacity uint32
	if err := vcall(acc, slotByteAccessGetBuffer, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&capacity))); err != nil {
		return fmt.Errorf("GetBuffer: %w", err)
	}
	stride := int(pd.stride)
	if stride < w*4 || int(pd.start)+stride*(h-1)+w*4 > int(capacity) {
		return fmt.Errorf("unexpected bitmap layout (stride %d, capacity %d)", stride, capacity)
	}
	dst := unsafe.Slice((*byte)(data), int(capacity))
	for y := 0; y < h; y++ {
		copy(dst[int(pd.start)+y*stride:int(pd.start)+y*stride+w*4], pix[y*w*4:(y+1)*w*4])
	}
	return nil
}

func recognize(eng, fac unsafe.Pointer, pix []byte, w, h int) ([][]ocrWord, error) {
	var bmp unsafe.Pointer
	if err := vcall(fac, slotBitmapCreate, pixelFormatBgra8, uintptr(w), uintptr(h), uintptr(unsafe.Pointer(&bmp))); err != nil {
		return nil, fmt.Errorf("SoftwareBitmap: %w", err)
	}
	defer func() { closeObj(bmp); release(bmp) }()
	if err := fillBitmap(bmp, pix, w, h); err != nil {
		return nil, err
	}
	var op unsafe.Pointer
	if err := vcall(eng, slotOcrRecognizeAsync, uintptr(bmp), uintptr(unsafe.Pointer(&op))); err != nil {
		return nil, fmt.Errorf("RecognizeAsync: %w", err)
	}
	defer release(op)
	info, err := queryInterface(op, &iidAsyncInfo)
	if err != nil {
		return nil, err
	}
	defer release(info)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var status int32
		if err := vcall(info, slotAsyncStatus, uintptr(unsafe.Pointer(&status))); err != nil {
			return nil, err
		}
		if status == 1 {
			break
		}
		if status != 0 {
			return nil, fmt.Errorf("OCR failed (status %d)", status)
		}
		if time.Now().After(deadline) {
			return nil, errors.New("OCR timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
	var res unsafe.Pointer
	if err := vcall(op, slotAsyncGetResults, uintptr(unsafe.Pointer(&res))); err != nil || res == nil {
		return nil, fmt.Errorf("OCR results: %v", err)
	}
	defer release(res)
	// Word positions are given as if the image had been turned so the text
	// is level; OCR sometimes sees a small tilt in level text, so turn them
	// back, or words on one line get different heights across the screen.
	var angle float64
	var angleRef unsafe.Pointer
	if vcall(res, slotResultTextAngle, uintptr(unsafe.Pointer(&angleRef))) == nil && angleRef != nil {
		vcall(angleRef, slotReferenceValue, uintptr(unsafe.Pointer(&angle)))
		release(angleRef)
	}
	var linesV unsafe.Pointer
	if err := vcall(res, slotResultLines, uintptr(unsafe.Pointer(&linesV))); err != nil {
		return nil, err
	}
	defer release(linesV)
	var out [][]ocrWord
	for i, n := uint32(0), vectorSize(linesV); i < n; i++ {
		var line unsafe.Pointer
		if vcall(linesV, slotVectorGetAt, uintptr(i), uintptr(unsafe.Pointer(&line))) != nil || line == nil {
			continue
		}
		var wordsV unsafe.Pointer
		if vcall(line, slotLineWords, uintptr(unsafe.Pointer(&wordsV))) == nil && wordsV != nil {
			var words []ocrWord
			for j, m := uint32(0), vectorSize(wordsV); j < m; j++ {
				var word unsafe.Pointer
				if vcall(wordsV, slotVectorGetAt, uintptr(j), uintptr(unsafe.Pointer(&word))) != nil || word == nil {
					continue
				}
				var rect [4]float32 // x, y, width, height
				vcall(word, slotWordRect, uintptr(unsafe.Pointer(&rect)))
				var hs uintptr
				vcall(word, slotWordText, uintptr(unsafe.Pointer(&hs)))
				x, y := unrotate(float64(rect[0]+rect[2]/2), float64(rect[1]+rect[3]/2), float64(w), float64(h), angle)
				words = append(words, ocrWord{text: hstringText(hs), x: float32(x) - rect[2]/2, y: float32(y) - rect[3]/2, w: rect[2], h: rect[3]})
				pWindowsDeleteString.Call(hs)
				release(word)
			}
			out = append(out, words)
			release(wordsV)
		}
		release(line)
	}
	return out, nil
}

// unrotate turns a point given for the image turned by angle degrees (OCR's
// TextAngle) back to the image as captured, around the image center.
func unrotate(x, y, w, h, angle float64) (float64, float64) {
	if angle == 0 {
		return x, y
	}
	s, c := math.Sincos(angle * math.Pi / 180)
	dx, dy := x-w/2, y-h/2
	return w/2 + dx*c - dy*s, h/2 + dx*s + dy*c
}

func vectorSize(v unsafe.Pointer) uint32 {
	var n uint32
	if vcall(v, slotVectorSize, uintptr(unsafe.Pointer(&n))) != nil {
		return 0
	}
	return n
}

// captureScreen copies a screen rectangle as BGRA. It can include the overlay
// itself, see maskOverlay.
func captureScreen(x, y, w, h int) ([]byte, error) {
	d, err := newDIB(w, h)
	if err != nil {
		return nil, err
	}
	defer d.free()
	screen, _, _ := pGetDC.Call(0)
	defer pReleaseDC.Call(0, screen)
	if r, _, e := pBitBlt.Call(d.dc, 0, 0, uintptr(w), uintptr(h), screen, uintptr(x), uintptr(y), srcCopy); r == 0 {
		return nil, fmt.Errorf("BitBlt: %v", e)
	}
	pix := make([]byte, w*h*4)
	copy(pix, unsafe.Slice((*byte)(d.bits), w*h*4))
	for i := 3; i < len(pix); i += 4 {
		pix[i] = 255
	}
	return pix, nil
}

// maskOverlay blacks out our own panel in a capture of the screen rectangle
// starting at (x, y), so its augment names are never read back as cards.
func maskOverlay(pix []byte, x, y, w, h int) {
	u := theUI
	if u == nil || u.overlay == 0 {
		return
	}
	if v, _, _ := pIsWindowVisible.Call(u.overlay); v == 0 {
		return
	}
	var r rectT
	if ok, _, _ := pGetWindowRect.Call(u.overlay, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return
	}
	x0, y0 := max(int(r.left)-x, 0), max(int(r.top)-y, 0)
	x1, y1 := min(int(r.right)-x, w), min(int(r.bottom)-y, h)
	for yy := y0; yy < y1; yy++ {
		row := pix[yy*w*4 : (yy+1)*w*4]
		for xx := x0; xx < x1; xx++ {
			row[xx*4], row[xx*4+1], row[xx*4+2] = 0, 0, 0
		}
	}
}

// augmentReader reads the middle of the game window, where the augment cards
// are, while the game is the foreground window. The OCR engine starts on the
// first read, so it costs nothing unless you play ARAM Mayhem.
func augmentReader(logf func(string, ...any)) app.ScreenReader {
	var (
		once sync.Once
		eng  *ocrEngine
		err  error
	)
	return func() ([]app.OCRLine, error) {
		game, area, ok := gameArea()
		if !ok {
			return nil, nil
		}
		if fg, _, _ := pGetForegroundWindow.Call(); fg != game {
			return nil, nil
		}
		once.Do(func() {
			if eng, err = startOCR(); err == nil && !eng.english {
				logf("augment cards: Windows has no English text recognition, so card names may not be read. " +
					"Add English in Settings, Time & language, Language & region.")
			}
		})
		if err != nil {
			return nil, err
		}
		aw, ah := int(area.right-area.left), int(area.bottom-area.top)
		x, y, w, h := titleStrip(int(area.left), int(area.top), aw, ah)
		pix, err := captureScreen(x, y, w, h)
		if err != nil {
			return nil, err
		}
		maskOverlay(pix, x, y, w, h)
		if ah > 1100 {
			pix, w, h = downscale(pix, w, h, 1080/float64(ah))
		}
		raw, err := eng.read(pix, w, h)
		if err != nil {
			return nil, err
		}
		return toLines(raw, w, h), nil
	}
}

// titleStrip is the part of the game window the card titles are in: the
// middle 76% of the width, from 28% to 54% of the height (the titles sit at
// about 41%). Reading only this strip roughly halves the OCR work.
func titleStrip(left, top, w, h int) (x, y, sw, sh int) {
	return left + w*12/100, top + h*28/100, w * 76 / 100, h * 26 / 100
}

// downscale shrinks a BGRA capture by f, so screens taller than 1080p cost no
// more to read than 1080p. The text stays as big as on a 1080p screen, which
// OCR reads reliably (smaller than that, short names get misread).
func downscale(pix []byte, w, h int, f float64) ([]byte, int, int) {
	nw, nh := int(float64(w)*f), int(float64(h)*f)
	if nw < 1 || nh < 1 {
		return pix, w, h
	}
	src := &image.RGBA{Pix: pix, Stride: w * 4, Rect: image.Rect(0, 0, w, h)}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	return dst.Pix, nw, nh
}

// toLines converts OCR output to resolution-independent lines.
func toLines(raw [][]ocrWord, w, h int) []app.OCRLine {
	lines := make([]app.OCRLine, 0, len(raw))
	for _, l := range raw {
		var line app.OCRLine
		for _, wd := range l {
			line = append(line, app.OCRWord{Text: wd.text,
				X: float64(wd.x) / float64(w), W: float64(wd.w) / float64(w),
				Y: float64(wd.y+wd.h/2) / float64(h), H: float64(wd.h) / float64(h)})
		}
		lines = append(lines, line)
	}
	return lines
}
