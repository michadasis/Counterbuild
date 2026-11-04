//go:build windows

// Counterbuild: a tiny click-through overlay that reads your live League of
// Legends game and recommends what to build against the enemy team.
package main

import (
	"errors"
	"fmt"
	"image"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"counterbuild/app"
)

func init() { runtime.LockOSThread() }

const runKey = "Counterbuild"

var version = app.Version

type ui struct {
	a          *app.App
	dir        string
	log        *log.Logger
	ctl        uintptr
	overlay    uintptr
	icon       uintptr
	frame      atomic.Pointer[app.Frame]
	cur        *app.Frame
	surf       *dib
	shown      bool
	userHidden bool
	lastX      int32
	lastY      int32
	lastScale  float64
	taskbarMsg uintptr
	stop       chan struct{}
	ticks      int
	hotkey     string
	cfgTried   time.Time
}

var theUI *ui

var sizes = []struct {
	label string
	v     float64
}{{"Small", 0.85}, {"Normal", 1}, {"Large", 1.15}, {"Extra large", 1.3}}

func main() {
	// Keep the footprint small: the app allocates little, so collect eagerly.
	debug.SetGCPercent(40)
	debug.SetMemoryLimit(64 << 20)
	yieldToGame()
	setDPIAware()
	mutex, _, err := pCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(u16(`Local\Counterbuild.Overlay`))))
	if mutex != 0 && errors.Is(err, syscall.Errno(errExists)) {
		messageBox("Counterbuild", "Counterbuild is already running. Look for its icon in the system tray.", mbOK|mbIconInfo)
		return
	}
	base, err := os.UserCacheDir()
	if err != nil {
		base = "."
	}
	dir := filepath.Join(base, "Counterbuild")
	_ = os.MkdirAll(dir, 0o755)
	logger := openLog(dir)
	logger.Printf("Counterbuild %s starting (%s)", version, runtime.Version())

	cfg, cfgErr := app.LoadConfig(filepath.Join(dir, "config.json"))
	if cfgErr != nil {
		logger.Printf("config.json is not valid JSON, using defaults: %v", cfgErr)
	}
	windir := os.Getenv("WINDIR")
	if windir == "" {
		windir = `C:\Windows`
	}
	a := app.New(cfg, dir, []string{filepath.Join(windir, "Fonts")}, logger)
	a.SetScreenReader(augmentReader(logger.Printf))
	u := &ui{a: a, dir: dir, log: logger, stop: make(chan struct{}), lastScale: 1}
	theUI = u
	if err := u.create(); err != nil {
		logger.Printf("startup failed: %v", err)
		messageBox("Counterbuild", "Could not start: "+err.Error(), mbOK|mbIconError)
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Printf("poll loop crashed: %v\n%s", r, debug.Stack())
			}
		}()
		a.Run(u.stop, func(f app.Frame) {
			fc := f
			u.frame.Store(&fc)
			pPostMessageW.Call(u.ctl, wmAppFrame, 0, 0)
		})
	}()
	u.loop()
	logger.Printf("Counterbuild stopped")
}

func openLog(dir string) *log.Logger {
	p := filepath.Join(dir, "counterbuild.log")
	if st, err := os.Stat(p); err == nil && st.Size() > 512<<10 {
		_ = os.Remove(p)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return log.New(os.Stderr, "", log.LstdFlags)
	}
	return log.New(f, "", log.LstdFlags)
}

func (u *ui) create() error {
	hinst, _, _ := pGetModuleHandleW.Call(0)
	cb := syscall.NewCallback(wndProc)
	for _, name := range []string{"CounterbuildCtl", "CounterbuildOverlay"} {
		wc := wndClassExW{lpfnWndProc: cb, hInstance: hinst, lpszClassName: u16(name)}
		wc.cbSize = uint32(unsafe.Sizeof(wc))
		if r, _, e := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			return fmt.Errorf("RegisterClassEx: %v", e)
		}
	}
	u.ctl, _, _ = pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(u16("CounterbuildCtl"))),
		uintptr(unsafe.Pointer(u16("Counterbuild"))), wsPopup, 0, 0, 0, 0, 0, 0, hinst, 0)
	ex := uintptr(wsExLayered | wsExTransparent | wsExTopmost | wsExToolWindow | wsExNoActivate)
	u.overlay, _, _ = pCreateWindowExW.Call(ex, uintptr(unsafe.Pointer(u16("CounterbuildOverlay"))),
		uintptr(unsafe.Pointer(u16("Counterbuild Overlay"))), wsPopup, 0, 0, 1, 1, 0, 0, hinst, 0)
	if u.ctl == 0 || u.overlay == 0 {
		return errors.New("CreateWindowEx failed")
	}
	u.taskbarMsg, _, _ = pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(u16("TaskbarCreated"))))
	sz, _, _ := pGetSystemMetrics.Call(smCxSmIcon)
	if sz < 16 {
		sz = 16
	}
	u.icon = makeIcon(u.a.R.AppIcon(int(sz) * 2))
	u.addTray()
	u.registerHotkey()
	pSetTimer.Call(u.ctl, 1, 250, 0)
	if !u.a.Cfg.Welcomed {
		u.balloon("Counterbuild is running",
			fmt.Sprintf("It appears during games (use Borderless window mode). %s shows or hides it. Right-click this icon for options.", u.a.Cfg.Hotkey))
		u.a.WithConfig(func(c *app.Config) { c.Welcomed = true })
	}
	return nil
}

func (u *ui) loop() {
	var m msgT
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func wndProc(hwnd, msg, wp, lp uintptr) uintptr {
	u := theUI
	if u != nil && hwnd == u.ctl {
		switch msg {
		case wmAppFrame:
			u.refresh(true)
			return 0
		case wmTimer:
			u.tick()
			return 0
		case wmHotkey:
			u.userHidden = !u.userHidden
			u.refresh(true)
			return 0
		case wmAppTray:
			switch lp & 0xFFFF {
			case wmRButtonUp, wmContextMenu:
				u.showMenu()
			case wmLButtonUp:
				u.userHidden = !u.userHidden
				u.refresh(true)
			}
			return 0
		case wmEndSession:
			u.a.Hist.Save()
		case wmDestroy:
			pPostQuitMessage.Call(0)
			return 0
		default:
			if u.taskbarMsg != 0 && msg == u.taskbarMsg {
				u.addTray()
			}
		}
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

func (u *ui) tick() {
	u.ticks++
	if u.ticks%8 == 0 && u.a.Cfg.FileChanged() {
		if st, err := os.Stat(u.a.Cfg.Path()); err == nil && !st.ModTime().Equal(u.cfgTried) {
			u.cfgTried = st.ModTime()
			if fresh, err := app.LoadConfig(u.a.Cfg.Path()); err != nil {
				u.log.Printf("settings not reloaded, config.json has an error: %v", err)
			} else {
				u.a.WithConfig(func(c *app.Config) { *c = *fresh })
				u.registerHotkey()
				u.lastScale = 0
				u.log.Printf("settings reloaded")
			}
		}
	}
	u.refresh(false)
}

// gameArea returns the League game window's client rect in screen coordinates.
func gameArea() (uintptr, rectT, bool) {
	game, _, _ := pFindWindowW.Call(uintptr(unsafe.Pointer(u16("RiotWindowClass"))), 0)
	if game == 0 {
		return 0, rectT{}, false
	}
	if iconic, _, _ := pIsIconic.Call(game); iconic != 0 {
		return game, rectT{}, false
	}
	var rc rectT
	if r, _, _ := pGetClientRect.Call(game, uintptr(unsafe.Pointer(&rc))); r == 0 {
		return game, rectT{}, false
	}
	pt := pointT{0, 0}
	pClientToScreen.Call(game, uintptr(unsafe.Pointer(&pt)))
	rc.left += pt.x
	rc.right += pt.x
	rc.top += pt.y
	rc.bottom += pt.y
	if rc.right-rc.left < 200 || rc.bottom-rc.top < 200 {
		return game, rectT{}, false
	}
	return game, rc, true
}

func screenArea() rectT {
	w, _, _ := pGetSystemMetrics.Call(smCxScreen)
	h, _, _ := pGetSystemMetrics.Call(smCyScreen)
	return rectT{0, 0, int32(w), int32(h)}
}

func anchor(pos string, area rectT, w, h, offX, offY int, s float64) (int32, int32) {
	ah := float64(area.bottom - area.top)
	m := int32(16 * s)
	var x, y int32
	switch pos {
	case "left":
		x, y = area.left+m, area.top+int32(ah*0.24)
	case "top-left":
		x, y = area.left+m, area.top+m
	case "top-right":
		x, y = area.right-int32(w)-m, area.top+int32(ah*0.08)
	case "bottom-left":
		x, y = area.left+m, area.bottom-int32(h)-int32(ah*0.30)
	default:
		x, y = area.right-int32(w)-m, area.top+int32(ah*0.24)
	}
	x += int32(offX)
	y += int32(offY)
	if x+int32(w) > area.right {
		x = area.right - int32(w)
	}
	if y+int32(h) > area.bottom {
		y = area.bottom - int32(h)
	}
	if x < area.left {
		x = area.left
	}
	if y < area.top {
		y = area.top
	}
	return x, y
}

func (u *ui) refresh(force bool) {
	cfg := u.a.Cfg
	f := u.frame.Load()
	game, area, ok := gameArea()
	if !ok {
		area = screenArea()
	}
	want := float64(area.bottom-area.top) / 1080 * cfg.Scale
	want = math.Max(0.6, math.Min(3, want))
	if math.Abs(want-u.lastScale) > 0.01 {
		u.lastScale = want
		u.a.SetScale(want)
		u.a.Wake()
	}
	visible := f != nil && f.Show && f.Img != nil && !u.userHidden
	if visible && cfg.HideWhenUnfocused {
		fg, _, _ := pGetForegroundWindow.Call()
		if game == 0 || (fg != game && fg != u.overlay && fg != u.ctl) {
			visible = false
		}
	}
	if !visible {
		if u.shown {
			pShowWindow.Call(u.overlay, swHide)
			u.shown = false
		}
		return
	}
	b := f.Img.Bounds()
	x, y := anchor(cfg.Position, area, b.Dx(), b.Dy(), cfg.OffsetX, cfg.OffsetY, u.lastScale)
	if force || f != u.cur || x != u.lastX || y != u.lastY || !u.shown {
		if err := u.push(f.Img, x, y); err != nil {
			u.log.Printf("draw: %v", err)
			return
		}
		u.cur, u.lastX, u.lastY = f, x, y
	}
	if !u.shown {
		pShowWindow.Call(u.overlay, swShowNoActivate)
		pSetWindowPos.Call(u.overlay, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
		u.shown = true
	}
}

func (u *ui) push(img *image.RGBA, x, y int32) error {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if u.surf == nil || u.surf.w != w || u.surf.h != h {
		u.surf.free()
		var err error
		if u.surf, err = newDIB(w, h); err != nil {
			u.surf = nil
			return err
		}
	}
	u.surf.fill(img, true)
	screen, _, _ := pGetDC.Call(0)
	defer pReleaseDC.Call(0, screen)
	pt, src, sz := pointT{x, y}, pointT{}, sizeT{int32(w), int32(h)}
	bf := blendFunction{BlendOp: acSrcOver, SourceConstantAlpha: 255, AlphaFormat: acSrcAlpha}
	r, _, e := pUpdateLayeredWindow.Call(u.overlay, screen, uintptr(unsafe.Pointer(&pt)), uintptr(unsafe.Pointer(&sz)),
		u.surf.dc, uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&bf)), ulwAlpha)
	if r == 0 {
		return fmt.Errorf("UpdateLayeredWindow: %v", e)
	}
	return nil
}

func (u *ui) trayData() notifyIconData {
	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = u.ctl
	nid.UID = 1
	nid.UFlags = nifMessage | nifIcon | nifTip
	nid.UCallbackMessage = wmAppTray
	nid.HIcon = u.icon
	copyU16(nid.SzTip[:], "Counterbuild ("+u.a.Cfg.Hotkey+" to show/hide)")
	return nid
}

func (u *ui) addTray() {
	nid := u.trayData()
	pShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
}

func (u *ui) removeTray() {
	nid := u.trayData()
	pShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func (u *ui) balloon(title, text string) {
	nid := u.trayData()
	nid.UFlags |= nifInfo
	copyU16(nid.SzInfoTitle[:], title)
	copyU16(nid.SzInfo[:], text)
	nid.DwInfoFlags = niifInfo
	pShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

var vkNames = map[string]uint32{
	"INSERT": 0x2D, "DELETE": 0x2E, "HOME": 0x24, "END": 0x23, "PAGEUP": 0x21, "PAGEDOWN": 0x22,
	"SPACE": 0x20, "TAB": 0x09, "`": 0xC0, "TILDE": 0xC0,
}

func parseHotkey(s string) (mods, vk uint32, ok bool) {
	parts := strings.Split(strings.ToUpper(strings.ReplaceAll(s, " ", "")), "+")
	if len(parts) == 0 {
		return 0, 0, false
	}
	for _, p := range parts[:len(parts)-1] {
		switch p {
		case "CTRL", "CONTROL":
			mods |= modControl
		case "SHIFT":
			mods |= modShift
		case "ALT":
			mods |= modAlt
		case "WIN":
			mods |= modWin
		default:
			return 0, 0, false
		}
	}
	key := parts[len(parts)-1]
	switch {
	case len(key) == 1 && key[0] >= 'A' && key[0] <= 'Z', len(key) == 1 && key[0] >= '0' && key[0] <= '9':
		vk = uint32(key[0])
	case len(key) >= 2 && key[0] == 'F':
		var n int
		if _, err := fmt.Sscanf(key[1:], "%d", &n); err == nil && n >= 1 && n <= 24 {
			vk = 0x70 + uint32(n-1)
		}
	default:
		vk = vkNames[key]
	}
	if vk == 0 {
		return 0, 0, false
	}
	// Plain letters would steal game keys; require a modifier unless it is an F-key.
	if mods == 0 && !(vk >= 0x70 && vk <= 0x87) {
		return 0, 0, false
	}
	return mods, vk, true
}

func (u *ui) registerHotkey() {
	hk := u.a.Cfg.Hotkey
	if hk == u.hotkey {
		return
	}
	pUnregisterHotKey.Call(u.ctl, 1)
	u.hotkey = hk
	mods, vk, ok := parseHotkey(hk)
	if !ok {
		u.log.Printf("hotkey %q not understood; use something like Ctrl+Shift+B or F8", hk)
		return
	}
	if r, _, e := pRegisterHotKey.Call(u.ctl, 1, uintptr(mods|modNoRepeat), uintptr(vk)); r == 0 {
		u.log.Printf("hotkey %q unavailable: %v", hk, e)
	}
}

const (
	cmdToggle    = 1
	cmdPosBase   = 100
	cmdSizeBase  = 200
	cmdUnfocused = 300
	cmdStartup   = 301
	cmdMeta      = 302
	cmdAugments  = 303
	cmdSettings  = 400
	cmdFolder    = 401
	cmdExit      = 999
)

func positionLabel(p string) string {
	switch p {
	case "right":
		return "Right side"
	case "top-right":
		return "Top right"
	case "left":
		return "Left side"
	case "top-left":
		return "Top left"
	case "bottom-left":
		return "Bottom left"
	}
	return p
}

func (u *ui) showMenu() {
	cfg := u.a.Cfg
	menu, _, _ := pCreatePopupMenu.Call()
	add := func(m, flags, id uintptr, text string) {
		pAppendMenuW.Call(m, flags, id, uintptr(unsafe.Pointer(u16(text))))
	}
	check := func(b bool) uintptr {
		if b {
			return mfChecked
		}
		return 0
	}
	add(menu, mfString|mfGrayed, 0, "Counterbuild "+version)
	add(menu, mfSeparator, 0, "")
	label := "Hide overlay"
	if u.userHidden {
		label = "Show overlay"
	}
	add(menu, mfString, cmdToggle, label+"\t"+cfg.Hotkey)
	pos, _, _ := pCreatePopupMenu.Call()
	for i, p := range app.Positions {
		add(pos, mfString|check(p == cfg.Position), uintptr(cmdPosBase+i), positionLabel(p))
	}
	add(menu, mfPopup, pos, "Position")
	sz, _, _ := pCreatePopupMenu.Call()
	for i, s := range sizes {
		add(sz, mfString|check(math.Abs(s.v-cfg.Scale) < 0.01), uintptr(cmdSizeBase+i), s.label)
	}
	add(menu, mfPopup, sz, "Size")
	add(menu, mfString|check(cfg.HideWhenUnfocused), cmdUnfocused, "Only show while the game is focused")
	add(menu, mfString|check(cfg.MetaSource != "off"), cmdMeta, "Use OP.GG builds")
	add(menu, mfString|check(cfg.Augments != "off"), cmdAugments, "Rate augment cards (ARAM Mayhem)")
	add(menu, mfString|check(regGetRun(runKey) != ""), cmdStartup, "Start with Windows")
	add(menu, mfSeparator, 0, "")
	add(menu, mfString, cmdSettings, "Edit settings...")
	add(menu, mfString, cmdFolder, "Open data folder")
	add(menu, mfSeparator, 0, "")
	add(menu, mfString, cmdExit, "Exit")

	var pt pointT
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForegroundWindow.Call(u.ctl)
	cmd, _, _ := pTrackPopupMenu.Call(menu, tpmReturnCmd|tpmRightButton|tpmBottomAlign,
		uintptr(int(pt.x)), uintptr(int(pt.y)), 0, u.ctl, 0)
	pPostMessageW.Call(u.ctl, wmNull, 0, 0)
	pDestroyMenu.Call(menu)
	u.command(int(cmd))
}

func (u *ui) command(cmd int) {
	switch {
	case cmd == cmdToggle:
		u.userHidden = !u.userHidden
	case cmd >= cmdPosBase && cmd < cmdPosBase+len(app.Positions):
		p := app.Positions[cmd-cmdPosBase]
		u.a.WithConfig(func(c *app.Config) { c.Position = p })
	case cmd >= cmdSizeBase && cmd < cmdSizeBase+len(sizes):
		v := sizes[cmd-cmdSizeBase].v
		u.a.WithConfig(func(c *app.Config) { c.Scale = v })
	case cmd == cmdUnfocused:
		u.a.WithConfig(func(c *app.Config) { c.HideWhenUnfocused = !c.HideWhenUnfocused })
	case cmd == cmdMeta:
		u.a.WithConfig(func(c *app.Config) {
			if c.MetaSource == "off" {
				c.MetaSource = "opgg"
			} else {
				c.MetaSource = "off"
			}
		})
	case cmd == cmdAugments:
		u.a.WithConfig(func(c *app.Config) {
			if c.Augments == "off" {
				c.Augments = "auto"
			} else {
				c.Augments = "off"
			}
		})
	case cmd == cmdStartup:
		var err error
		if regGetRun(runKey) != "" {
			err = regSetRun(runKey, "")
		} else if exe, e := os.Executable(); e == nil {
			err = regSetRun(runKey, `"`+exe+`"`)
		}
		if err != nil {
			u.log.Printf("startup setting: %v", err)
		}
	case cmd == cmdSettings:
		pShellExecuteW.Call(0, uintptr(unsafe.Pointer(u16("open"))), uintptr(unsafe.Pointer(u16("notepad.exe"))),
			uintptr(unsafe.Pointer(u16(`"`+u.a.Cfg.Path()+`"`))), 0, swShowNormal)
	case cmd == cmdFolder:
		pShellExecuteW.Call(0, uintptr(unsafe.Pointer(u16("open"))), uintptr(unsafe.Pointer(u16(u.dir))), 0, 0, swShowNormal)
	case cmd == cmdExit:
		u.exit()
		return
	default:
		return
	}
	// Recompute scale from the new size setting right away.
	u.lastScale = 0
	u.refresh(true)
}

func (u *ui) exit() {
	select {
	case <-u.stop:
	default:
		close(u.stop)
	}
	u.removeTray()
	pUnregisterHotKey.Call(u.ctl, 1)
	pShowWindow.Call(u.overlay, swHide)
	u.a.Hist.Save()
	// Give the poll loop a moment to finish its current request.
	time.Sleep(50 * time.Millisecond)
	pPostQuitMessage.Call(0)
}
