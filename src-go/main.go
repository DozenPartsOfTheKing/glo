//go:build windows

// Glasspad — a transparent notepad that stays on top of all windows, opacity 0-100%.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// Pixels of this color become fully transparent (LWA_COLORKEY).
var colorKey = rgb(255, 0, 254)

const (
	barBG     = 0x241E1C // COLORREF = 0x00BBGGRR
	barTop    = 0x3E3632 // bar top is lighter than the bottom: the bar is glass too
	barFG     = 0xC6BEB8
	barHot    = 0xFFFFFF
	trackBG   = 0x3A302C
	knobBG    = 0xF0F0F0
	dimFG     = 0x8C7C70
	appTitle  = "Glo"
	className = "GloWnd"
	backClass = "GloBackdropWnd"
	glowClass = "GloGlowWnd"

	// The data folder kept its old name: the user's note lives there,
	// and renaming it for a prettier name would lose it.
	dataFolder = "Glasspad"

	cfgVersion = 2 // bumped when glow and glass were added
)

// The marker plate behind the letters: on or off. Opacity has nothing to
// do with the letters — it lives on a separate backdrop window.
const (
	modePlain  = 0
	modeMarker = 1
)

var modeNames = []string{txtNoMarker, txtMarker}

type palette struct {
	name   string
	fg     uint32 // the letters themselves
	marker uint32 // the plate behind the letters, also the glass color
	glow   uint32 // the halo around the letters in glow mode
}

// The halo is white everywhere: a colored one blends with letters of the
// same color and the letters drown in their own glow. A white glow under
// a colored glyph keeps its shape readable on any background. One
// exception — white letters: a white glow under them would be
// indistinguishable from the letters themselves, so there it's dark gray
// and works as a shadow.
var palettes = []palette{
	{txtWhite, rgb(255, 255, 255), rgb(0, 0, 0), rgb(70, 70, 70)},
	{txtBlack, rgb(0, 0, 0), rgb(255, 255, 255), rgb(255, 255, 255)},
	{txtRed, rgb(255, 95, 95), rgb(24, 0, 0), rgb(255, 255, 255)},
	{txtGreen, rgb(105, 255, 150), rgb(0, 22, 8), rgb(255, 255, 255)},
	{txtBlue, rgb(120, 175, 255), rgb(0, 6, 28), rgb(255, 255, 255)},
	{txtCyan, rgb(95, 250, 255), rgb(0, 20, 22), rgb(255, 255, 255)},
	{txtPink, rgb(255, 120, 225), rgb(22, 0, 18), rgb(255, 255, 255)},
	{txtYellow, rgb(255, 225, 95), rgb(22, 15, 0), rgb(255, 255, 255)},
}

const (
	fontMin = 8
	fontMax = 72
)

var fontPresets = []int{12, 16, 20, 28, 36, 48, 72}

// commands (accelerators, toolbar buttons, global hotkeys)
const (
	cmdMode = 100 + iota
	cmdPalette
	cmdBW
	cmdTopmost
	cmdCompact
	cmdClickThrough
	cmdGlow
	cmdBlur
	cmdDropBelow
	cmdOpen
	cmdSave
	cmdQuit
	cmdShowHide
	cmdAlphaUp
	cmdAlphaDown
	cmdFontUp
	cmdFontDown
	cmdFontPreset
	cmdSettings
	cmdTray
	cmdTaskbar
	cmdOpenDir
)

// Submenu items: command = base + option index.
const (
	cmdPalBase   = 300
	cmdSizeBase  = 340
	cmdAlphaBase = 380
)

var alphaPresets = []int{0, 25, 50, 75, 100}

const (
	hkAlphaUp = 1 + iota
	hkAlphaDown
	hkClickThrough
	hkHideAll
)

const (
	htLeft       = 10
	htRight      = 11
	htTop        = 12
	htTopLeft    = 13
	htTopRight   = 14
	htBottom     = 15
	htBottomLeft = 16
	idEdit       = 1000
	timerSave    = 1
	timerBeat    = 2
	timerPunch   = 3
	timerGlow    = 4
	timerArm     = 5
)

type config struct {
	Version  int  `json:"version"`
	Alpha    int  `json:"alpha"`
	Mode     int  `json:"mode"`
	Palette  int  `json:"palette"`
	FontSize int  `json:"font_size"`
	Glow     bool `json:"glow"`
	Blur     bool `json:"blur"`
	// Whether to drop the window below after a punch-through. Off by
	// default: the note overlay should stay visible, and the punch
	// already forwards the click down anyway.
	DropBelow bool `json:"drop_below"`
	Topmost   bool `json:"topmost"`
	Compact   bool `json:"compact"`
	Tray      bool `json:"tray"`
	Taskbar   bool `json:"taskbar"`
	X         int  `json:"x"`
	Y         int  `json:"y"`
	W         int  `json:"w"`
	H         int  `json:"h"`
}

func defaultConfig() config {
	// Default opacity is low: at 80% the backdrop looks like a plain
	// black rectangle and it's not obvious the window is transparent at
	// all. Default mode is without the marker plate: it's opaque and
	// would hide the glow.
	return config{Version: cfgVersion, Alpha: 30, Mode: modePlain, Palette: 0,
		FontSize: 16, Glow: true, Blur: true,
		Topmost: true, Tray: true, Taskbar: true,
		X: 120, Y: 120, W: 560, H: 360}
}

type barItem struct {
	cmd   int32
	label string
	r     rect
	fg    uint32
	bg    uint32
}

type app struct {
	hwnd, hEdit, hBack, hGlow, hInst uintptr
	cfg                              config
	dpi                              int32
	barH                             int32
	margin                           int32

	editFont, barFont, glowFont uintptr
	menuFont                    uintptr
	bgBrush, barBrush           uintptr
	backBrush                   uintptr
	trackBrush, knobBrush       uintptr
	knobPen, lineBrush          uintptr
	fontQ                       uint32
	items                       []barItem
	sliderRect                  rect
	draggingSlider              bool
	clickThrough                bool
	punchArmed                  bool // double-click happened, waiting for button release
	dropped                     bool // window dropped below after a punch-through
	punching                    bool // a one-shot punch-through from a double-click is in progress
	hidden                      bool

	glow      glowSurface
	glowShown bool
	glowDirty bool
	glowLine  int // first visible line at the last glow redraw
	cornersW  int32
	cornersH  int32

	beats                      int
	tray                       notifyIconData
	savedText                  string
	notePath, cfgPath, dataDir string
}

var a app

// The Windows message queue belongs to the thread that created the
// window. Go's main goroutine isn't pinned to an OS thread by default and
// can migrate to another one after any blocking call — then GetMessage
// spins on the wrong thread and the window "stops responding" for good.
// Nail it down.
func init() {
	runtime.LockOSThread()
}

func main() {
	setProcessDPIAware()
	a.initPaths()
	a.cfg = loadConfig(a.cfgPath)

	dc := getDC(0)
	a.dpi = getDeviceCaps(dc, logPixelsY)
	releaseDC(0, dc)
	if a.dpi <= 0 {
		a.dpi = 96
	}
	a.barH = a.scale(30)
	a.margin = a.scale(6)

	a.hInst = getModuleHandle()
	a.registerClass()
	a.createWindows()
	a.applyColors()
	a.applyFont()
	a.applyAlpha(a.cfg.Alpha)
	a.applyTopmost()
	a.applyBlur()
	a.setAppIcon()
	a.loadNote()

	showWindow(a.hBack, swShowNA)
	showWindow(a.hwnd, swShow)
	a.syncBackdrop()
	updateWindow(a.hwnd)
	setFocus(a.hEdit)
	a.renderGlow()

	if a.cfg.Tray {
		a.addTrayIcon()
	}
	if !a.cfg.Taskbar {
		a.setTaskbar(false)
	}
	a.registerHotKeys()
	setTimer(a.hwnd, timerSave, 5000)
	setTimer(a.hwnd, timerBeat, 1000)
	// The glow redraws on a timer, not on every keystroke: this coalesces
	// a dozen redraws into one during fast typing. The same timer catches
	// scrolling — EDIT doesn't notify about it when done from the keyboard.
	setTimer(a.hwnd, timerGlow, 60)
	logf("entering message loop")

	accels := createAcceleratorTable(accelTable())
	var m msg
	for getMessage(&m) > 0 {
		if !translateAccelerator(a.hwnd, accels, &m) {
			translateMessage(&m)
			dispatchMessage(&m)
		}
	}
	logf("=== exiting, all clean")
	closeLog()
}

func (a *app) scale(v int32) int32 { return v * a.dpi / 96 }

func (a *app) initPaths() {
	// %AppData%\Glasspad — the same location the Python version uses.
	base, err := os.UserConfigDir()
	if err != nil {
		if base, err = os.UserHomeDir(); err != nil {
			base = "."
		}
	}
	a.dataDir = filepath.Join(base, dataFolder)
	os.MkdirAll(a.dataDir, 0o755)
	a.notePath = filepath.Join(a.dataDir, "note.txt")
	a.cfgPath = filepath.Join(a.dataDir, "settings.json")
	openLog(a.dataDir)
	logf("data folder: %s", a.dataDir)
}

// ------------------------------------------------------------------ creation

func (a *app) registerClass() {
	a.bgBrush = createSolidBrush(colorKey)
	wc := wndClassEx{
		// CS_DBLCLKS is mandatory: without it the window never gets
		// WM_LBUTTONDBLCLK, and there's no punch-through on double-click.
		Style:         0x0002 | 0x0001 | csDblClks, // CS_HREDRAW | CS_VREDRAW
		LpfnWndProc:   syscall.NewCallback(wndProc),
		HInstance:     a.hInst,
		HCursor:       loadCursorArrow(),
		HbrBackground: 0, // we paint the background ourselves
		LpszClassName: str16(className),
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	registerClass(&wc)

	back := wc
	back.LpfnWndProc = syscall.NewCallback(backProc)
	back.LpszClassName = str16(backClass)
	registerClass(&back)

	// The glow window handles nothing itself: UpdateLayeredWindow puts
	// the image into it, and its style keeps it from catching the mouse.
	glow := wc
	glow.LpfnWndProc = syscall.NewCallback(defProc)
	glow.LpszClassName = str16(glowClass)
	registerClass(&glow)

	// The menu is a layered window like the glow, but with mouse input:
	// while it's open, the root menu window holds mouse capture.
	mn := wc
	mn.LpfnWndProc = syscall.NewCallback(menuProc)
	mn.LpszClassName = str16(menuClass)
	registerClass(&mn)
}

func defProc(hwnd, m, wp, lp uintptr) uintptr {
	return defWindowProc(hwnd, uint32(m), wp, lp)
}

func (a *app) createWindows() {
	// The backdrop is a separate window behind the main one. All the
	// transparency lives on it, so the letters in the main window never fade.
	a.hBack = createWindowEx(
		wsExLayered|wsExToolWindow|wsExNoActivate,
		str16(backClass), str16(appTitle),
		wsPopup,
		int32(a.cfg.X), int32(a.cfg.Y), int32(a.cfg.W), int32(a.cfg.H),
		0, 0, a.hInst)

	// No WS_EX_TOOLWINDOW: the window must show up in the taskbar and
	// Alt+Tab. An overlay with no familiar way to close is a trap for
	// whoever got the program without instructions.
	a.hwnd = createWindowEx(
		wsExLayered|wsExAppWindow,
		str16(className), str16(appTitle),
		wsPopup|wsThickFrame|wsClipChild,
		int32(a.cfg.X), int32(a.cfg.Y), int32(a.cfg.W), int32(a.cfg.H),
		0, 0, a.hInst)

	// The color key is permanent and without LWA_ALPHA: the background
	// falls through completely, and everything drawn on top stays fully
	// opaque.
	setLayered(a.hwnd, colorKey, 255, lwaColorKey)

	// The glow is a third window, between the backdrop and the main one.
	// Its transparency is per-pixel (UpdateLayeredWindow), so the halo
	// fades softly at the edges and doesn't fade along with the backdrop.
	// It never catches the mouse.
	a.hGlow = createWindowEx(
		wsExLayered|wsExTransparent|wsExToolWindow|wsExNoActivate,
		str16(glowClass), str16(appTitle),
		wsPopup,
		int32(a.cfg.X), int32(a.cfg.Y), int32(a.cfg.W), int32(a.cfg.H),
		0, 0, a.hInst)

	a.hEdit = createWindowEx(0, str16("EDIT"), str16(""),
		wsChild|wsVisible|esMultiline|esAutoVScrol|esWantReturn|esNoHideSel,
		0, 0, 10, 10, a.hwnd, uintptr(idEdit), a.hInst)
	sendMessage(a.hEdit, emSetLimitText, 0, 0)
	// Zero out the margins inside EDIT: the glow is drawn with the same
	// DrawText at the same coordinates, and extra pixels on the left
	// would shift it away from the letters.
	sendMessage(a.hEdit, emSetMargins, ecLeftMargin|ecRightMargin, 0)
	// A double-click on the text is a punch-through, not a word
	// selection, so we intercept the message before EDIT's normal handling.
	editPrevProc = setWindowLong(a.hEdit, gwlWndProc, syscall.NewCallback(editProc))

	a.barBrush = createSolidBrush(barBG)
	a.trackBrush = createSolidBrush(trackBG)
	a.knobBrush = createSolidBrush(knobBG)
	a.knobPen = createPen(knobBG, 1)
	a.lineBrush = createSolidBrush(shade(barBG, 26))
	a.barFont = createFont(-a.scale(12), 400, 5, "Segoe UI")
	// The menu sits on a per-pixel transparent canvas, and ClearType on
	// it would leave a colored fringe: subpixels are tuned for a solid
	// background. Hence ANTIALIASED_QUALITY — same as the glow font.
	a.menuFont = createFont(-a.scale(13), 400, 4, "Segoe UI")
	a.layoutChildren()
}

func (a *app) layoutChildren() {
	if a.hwnd == 0 || a.hEdit == 0 {
		return // WM_SIZE can arrive while still inside CreateWindowEx
	}
	c := getClientRect(a.hwnd)
	top := a.barH
	if a.cfg.Compact {
		top = 0
	}
	moveWindow(a.hEdit, a.margin, top, c.w()-2*a.margin, c.h()-top-a.margin, true)
	a.glowDirty = true
}

// editProc intercepts a double-click on the text. Everything else is
// passed to EDIT's normal procedure: caret, selection and input must
// keep working as before.
var editPrevProc uintptr

func editProc(hwnd, m, wp, lp uintptr) uintptr {
	switch m {
	case wmLButtonDblClk:
		a.punchArm()
		return 0
	case wmLButtonUp:
		if a.punchRelease() {
			return 0
		}
	}
	return callWindowProc(editPrevProc, hwnd, uint32(m), wp, lp)
}

// ------------------------------------------------------------- view and modes

// syncBackdrop keeps the backdrop strictly under the main window — both
// in coordinates and z-order. Inserting it right after the main window
// also carries over the "always on top" flag, if it's enabled.
func (a *app) syncBackdrop() {
	if a.hwnd == 0 || a.hBack == 0 {
		return
	}
	r := getWindowRect(a.hwnd)
	// Order matters: the glow right after the main window, the backdrop after the glow.
	if a.hGlow != 0 && a.glowShown {
		setWindowPos(a.hGlow, a.hwnd, r.Left, r.Top, r.w(), r.h(), swpNoActivate)
		setWindowPos(a.hBack, a.hGlow, r.Left, r.Top, r.w(), r.h(), swpNoActivate)
	} else {
		setWindowPos(a.hBack, a.hwnd, r.Left, r.Top, r.w(), r.h(), swpNoActivate)
	}
	a.applyCorners(r.w(), r.h())
}

// applyCorners rounds both visible windows. SetWindowRgn repaints the
// whole window, so we call it only when the size actually changed —
// otherwise dragging the window would turn into a stream of redraws.
func (a *app) applyCorners(w, h int32) {
	if w == a.cornersW && h == a.cornersH {
		return
	}
	a.cornersW, a.cornersH = w, h
	rad := a.scale(12)
	setCorners(a.hwnd, w, h, rad)
	setCorners(a.hBack, w, h, rad)
	a.glowDirty = true
}

func (a *app) applyColors() {
	pal := palettes[a.cfg.Palette%len(palettes)]
	old := a.backBrush
	a.backBrush = createSolidBrush(pal.marker) // backdrop colored like the marker plate
	invalidate(a.hBack, nil)
	invalidate(a.hwnd, nil)
	invalidate(a.hEdit, nil)
	deleteObject(old)
	a.glowDirty = true
}

// applyBlur turns on system blur-behind. Without it the backdrop just
// darkens what's beneath it; with it, a genuine frosted-glass look.
func (a *app) applyBlur() {
	setBlurBehind(a.hBack, a.cfg.Blur)
	invalidate(a.hBack, nil)
}

func (a *app) applyAlpha(v int) {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	a.cfg.Alpha = v
	setLayered(a.hBack, 0, byte(v*255/100), lwaAlpha)
	a.updateBackdropHitTest()
	if a.fontQ != a.wantFontQuality() {
		a.applyFont()
	}
	invalidate(a.hwnd, &rect{0, 0, getClientRect(a.hwnd).w(), a.barH})
}

// A layered window's color-key pixels don't catch the mouse — a click on
// an empty spot of the note would fall through to the app underneath. The
// backdrop catches those clicks and hands focus to the text. It used to
// pass them through at zero opacity, and the note would "fall through" on
// its own; now only two explicit actions let clicks through: a
// double-click and click-through mode.
func (a *app) updateBackdropHitTest() {
	if a.hBack == 0 {
		return
	}
	ex := getWindowLong(a.hBack, gwlExStyle)
	if a.clickThrough || a.punching {
		ex |= wsExTransparent
	} else {
		ex &^= wsExTransparent
	}
	setWindowLong(a.hBack, gwlExStyle, ex)
}

// wantFontQuality: when the letters float directly over the desktop (no
// marker plate, no noticeable backdrop), antialiasing blends with the
// color key and produces a pink fringe around the glyph edges — so we
// turn it off then.
// Glow doesn't change anything here: the halo lives on a separate window,
// and the smoothed glyph edges blend with that window's own background —
// i.e. with the color key.
func (a *app) wantFontQuality() uint32 {
	if a.cfg.Mode == modeMarker || a.cfg.Alpha >= 25 {
		return 5 // CLEARTYPE_QUALITY
	}
	return 3 // NONANTIALIASED_QUALITY
}

func (a *app) applyFont() {
	a.fontQ = a.wantFontQuality()
	h := -(int32(a.cfg.FontSize) * a.dpi / 72)
	// Letters are bold in glow mode: a thin glyph drowns in its own halo.
	weight := int32(400)
	if a.cfg.Glow {
		weight = 700
	}
	old, oldGlow := a.editFont, a.glowFont
	a.editFont = createFont(h, weight, a.fontQ, "Consolas")
	// The glow font must match the field font in every metric, or the
	// glow will drift away from the letters. Only antialiasing differs:
	// the mask needs gray edges, not ClearType's colored subpixels.
	a.glowFont = createFont(h, weight, 4, "Consolas") // ANTIALIASED_QUALITY
	sendMessage(a.hEdit, wmSetFont, a.editFont, 1)
	deleteObject(old)
	deleteObject(oldGlow)
	a.glowDirty = true
}

func (a *app) applyTopmost() {
	after := hwndNoTopmost
	if a.cfg.Topmost {
		after = hwndTopmost
	}
	setWindowPos(a.hwnd, after, 0, 0, 0, 0, swpNoMv|swpNoSz|swpNoActivate)
	a.syncBackdrop()
}

func (a *app) setMode(m int) {
	a.cfg.Mode = ((m % len(modeNames)) + len(modeNames)) % len(modeNames)
	// The marker plate is opaque and sits above the glow: the two
	// don't work together, so enabling one turns off the other.
	if a.cfg.Mode == modeMarker && a.cfg.Glow {
		a.cfg.Glow = false
		a.renderGlow()
	}
	a.applyFont()
	invalidate(a.hwnd, nil)
	invalidate(a.hEdit, nil)
}

func (a *app) setPalette(p int) {
	a.cfg.Palette = ((p % len(palettes)) + len(palettes)) % len(palettes)
	a.applyColors()
}

// ------------------------------------------------------------- tray icon

func (a *app) addTrayIcon() {
	if a.tray.CbSize != 0 {
		return // already present, a repeat NIM_ADD wouldn't go through
	}
	nid := notifyIconData{
		HWnd:             a.hwnd,
		UID:              1,
		UFlags:           nifMessage | nifIcon | nifTip,
		UCallbackMessage: wmTrayIcon,
		HIcon:            smallIcon(a.hInst),
	}
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	tip := utf16.Encode([]rune(txtTrayTip))
	copy(nid.SzTip[:len(nid.SzTip)-1], tip)
	a.tray = nid
	logf("tray add: %v", shellNotifyIcon(nimAdd, &a.tray))
}

// The tray icon and the taskbar button are two ways to reach the window.
// Turning both off at once isn't allowed: the program would become
// unkillable without Task Manager — exactly the trap this was all
// redesigned to avoid.
func (a *app) setTray(on bool) {
	if !on && !a.cfg.Taskbar {
		a.setTaskbar(true)
	}
	a.cfg.Tray = on
	if on {
		a.addTrayIcon()
	} else {
		a.removeTrayIcon()
	}
}

func (a *app) setTaskbar(on bool) {
	if !on && !a.cfg.Tray {
		a.setTray(true)
	}
	a.cfg.Taskbar = on
	// Windows only checks these styles at the moment the window is
	// shown, so it has to be hidden and shown again.
	visible := !a.hidden
	if visible {
		showWindow(a.hwnd, swHide)
	}
	ex := getWindowLong(a.hwnd, gwlExStyle)
	if on {
		ex |= wsExAppWindow
		ex &^= wsExToolWindow
	} else {
		ex &^= wsExAppWindow
		ex |= wsExToolWindow
	}
	setWindowLong(a.hwnd, gwlExStyle, ex)
	if visible {
		showWindow(a.hwnd, swShow)
		a.syncBackdrop()
		setFocus(a.hEdit)
	}
}

// setAppIcon sets the window's own icon: visible in the taskbar and
// Alt+Tab. The same icon is embedded in the exe as a resource (see
// icon/make_icon.py), so Explorer draws the file with it too.
func (a *app) setAppIcon() {
	if i := bigIcon(a.hInst); i != 0 {
		sendMessage(a.hwnd, wmSetIcon, iconBig, i)
	}
	if i := smallIcon(a.hInst); i != 0 {
		sendMessage(a.hwnd, wmSetIcon, iconSmall, i)
	}
}

func (a *app) removeTrayIcon() {
	if a.tray.CbSize != 0 {
		shellNotifyIcon(nimDelete, &a.tray)
		a.tray.CbSize = 0
	}
}

// ------------------------------------------------------------ settings menu

func (a *app) settingsMenu(x, y int32) {
	alpha := make([]mItem, 0, len(alphaPresets))
	for i, v := range alphaPresets {
		alpha = append(alpha, mItem{cmd: cmdAlphaBase + int32(i),
			label: strconv.Itoa(v) + "%", check: a.cfg.Alpha == v})
	}
	sizes := make([]mItem, 0, len(fontPresets))
	for i, v := range fontPresets {
		sizes = append(sizes, mItem{cmd: cmdSizeBase + int32(i),
			label: strconv.Itoa(v), check: a.cfg.FontSize == v})
	}
	colors := make([]mItem, 0, len(palettes))
	for i, p := range palettes {
		colors = append(colors, mItem{cmd: cmdPalBase + int32(i),
			label: p.name, check: a.cfg.Palette == i})
	}

	items := []mItem{
		{label: txtGlassOpacity, sub: alpha},
		{label: txtFontSize, sub: sizes},
		{label: txtTextColor, sub: colors},
		{sep: true},
		{cmd: cmdGlow, label: txtTextGlow, accel: "Ctrl+G", check: a.cfg.Glow},
		{cmd: cmdMode, label: txtMarkerBG, accel: "Ctrl+M", check: a.cfg.Mode == modeMarker},
		{cmd: cmdBlur, label: txtFrostedGlass, check: a.cfg.Blur},
		{cmd: cmdTopmost, label: txtAlwaysOnTop, accel: "Ctrl+T", check: a.cfg.Topmost},
		{cmd: cmdDropBelow, label: txtDropBelow, check: a.cfg.DropBelow},
		{cmd: cmdClickThrough, label: txtClickThrough, accel: "Ctrl+Alt+E", check: a.clickThrough},
		{cmd: cmdCompact, label: txtHideToolbar, accel: "Ctrl+H", check: a.cfg.Compact},
		{sep: true},
		{cmd: cmdTray, label: txtTrayIcon, check: a.cfg.Tray},
		{cmd: cmdTaskbar, label: txtTaskbarButton, check: a.cfg.Taskbar},
		{sep: true},
		{cmd: cmdOpen, label: txtOpenFile, accel: "Ctrl+O"},
		{cmd: cmdSave, label: txtSaveAs, accel: "Ctrl+S"},
		{cmd: cmdOpenDir, label: txtNoteFolder},
		{sep: true},
		{cmd: cmdQuit, label: txtExit, accel: "Ctrl+Q"},
	}

	if cmd := a.trackMenu(items, x, y); cmd != 0 {
		a.command(cmd)
	}
	setFocus(a.hEdit)
}

func (a *app) trayMenu() {
	show := txtHide
	if a.hidden {
		show = txtShow
	}
	items := []mItem{{cmd: cmdShowHide, label: show}}
	if a.clickThrough {
		// The window isn't catching the mouse right now, the toolbar can't
		// be clicked — without this item the mode could only be turned off
		// with the hotkey.
		items = append(items, mItem{cmd: cmdClickThrough, label: txtClickThroughOff})
	}
	items = append(items, mItem{cmd: cmdQuit, label: txtClose})

	p := getCursorPos()
	if cmd := a.trackMenu(items, p.X, p.Y); cmd != 0 {
		a.command(cmd)
	}
}

func (a *app) toggleHidden() {
	a.hidden = !a.hidden
	if a.hidden {
		showWindow(a.hwnd, swHide)
		showWindow(a.hBack, swHide)
		showWindow(a.hGlow, swHide)
		a.glowShown = false
		return
	}
	showWindow(a.hBack, swShowNA)
	showWindow(a.hwnd, swShow)
	a.raiseBack()
	a.syncBackdrop()
	setFocus(a.hEdit)
	a.renderGlow()
}

func (a *app) setFontSize(v int) {
	if v < fontMin {
		v = fontMin
	}
	if v > fontMax {
		v = fontMax
	}
	a.cfg.FontSize = v
	a.applyFont()
	invalidate(a.hwnd, nil)
}

func (a *app) stepFont(dir int) {
	step := 1
	if a.cfg.FontSize >= 24 {
		step = 2
	}
	a.setFontSize(a.cfg.FontSize + dir*step)
}

func (a *app) nextFontPreset() {
	for _, p := range fontPresets {
		if p > a.cfg.FontSize {
			a.setFontSize(p)
			return
		}
	}
	a.setFontSize(fontPresets[0])
}

func (a *app) toggleCompact() {
	a.cfg.Compact = !a.cfg.Compact
	a.layoutChildren()
	invalidate(a.hwnd, nil)
	a.renderGlow()
}

func (a *app) toggleClickThrough() {
	a.clickThrough = !a.clickThrough
	a.setMouseTransparent(a.clickThrough)
	invalidate(a.hwnd, nil)
}

// setMouseTransparent removes or restores the window's ability to catch
// the mouse. WS_EX_TRANSPARENT on a layered window takes effect
// immediately, no SetWindowPos needed.
func (a *app) setMouseTransparent(on bool) {
	ex := getWindowLong(a.hwnd, gwlExStyle)
	if on {
		ex |= wsExTransparent
	} else {
		ex &^= wsExTransparent
	}
	setWindowLong(a.hwnd, gwlExStyle, ex)
	a.updateBackdropHitTest()
}

// The punch-through is armed by a double-click and fires on button
// release. It used to happen right on WM_LBUTTONDBLCLK, but at that
// moment the button is still physically down, and the click sent to the
// system would land on an already-pressed button — the app underneath
// would get something other than a clean click.
func (a *app) punchArm() {
	if a.clickThrough || a.punching {
		return
	}
	a.punchArmed = true
	// If the release happens off-window (cursor dragged out and released
	// outside), the armed state shouldn't linger until the next random click.
	setTimer(a.hwnd, timerArm, 700)
}

func (a *app) punchRelease() bool {
	if !a.punchArmed {
		return false
	}
	a.disarmPunch()
	a.punchThrough()
	return true
}

func (a *app) disarmPunch() {
	a.punchArmed = false
	killTimer(a.hwnd, timerArm)
}

// punchThrough — a "one-shot punch": the window stops catching the mouse
// for a quarter second and sends the system a single click itself — it
// lands on whatever window is beneath the note. Permanent click-through
// mode doesn't work for this: you'd then have to get out of it somehow,
// while here everything reverts on its own.
func (a *app) punchThrough() {
	if a.clickThrough || a.punching {
		return // already all falling through
	}
	a.punching = true
	a.setMouseTransparent(true)
	clickAtCursor()
	if a.cfg.DropBelow {
		a.dropBelow()
	}
	setTimer(a.hwnd, timerPunch, 250)
}

// dropBelow sends the note below whichever window was just clicked. The
// "always on top" setting is left untouched: it comes back as soon as
// the user reaches for the note again.
func (a *app) dropBelow() {
	a.dropped = true
	setWindowPos(a.hwnd, hwndNoTopmost, 0, 0, 0, 0, swpNoMv|swpNoSz|swpNoActivate)
	a.syncBackdrop()
}

// raiseBack brings the note back on top. Called on any interaction with
// it: a click on the glass, a click on the bar, restoring from the tray.
func (a *app) raiseBack() {
	if !a.dropped {
		return
	}
	a.dropped = false
	a.applyTopmost()
}

func (a *app) endPunch() {
	if !a.punching {
		return
	}
	killTimer(a.hwnd, timerPunch)
	a.punching = false
	a.setMouseTransparent(a.clickThrough)
}

func (a *app) toggleGlow() {
	a.cfg.Glow = !a.cfg.Glow
	if a.cfg.Glow && a.cfg.Mode == modeMarker {
		a.cfg.Mode = modePlain // an opaque plate would hide the glow
	}
	a.applyFont() // letters are bolder in glow mode
	a.renderGlow()
	invalidate(a.hwnd, nil)
	invalidate(a.hEdit, nil)
}

func (a *app) toggleBlur() {
	a.cfg.Blur = !a.cfg.Blur
	a.applyBlur()
}

// glowTick is called by the timer: redraws the glow if the text changed
// or the note was scrolled.
func (a *app) glowTick() {
	if !a.cfg.Glow || a.hidden {
		return
	}
	line := int(sendMessage(a.hEdit, emGetFirstVisibleLine, 0, 0))
	if !a.glowDirty && line == a.glowLine {
		return
	}
	a.glowDirty, a.glowLine = false, line
	a.renderGlow()
}

// ---------------------------------------------------------------- drawing

func (a *app) buildBar(hdc uintptr, width int32) {
	pal := palettes[a.cfg.Palette%len(palettes)]
	a.items = a.items[:0]

	pad := a.scale(7)
	x := a.scale(10)

	// opacity slider
	sw := a.scale(110)
	a.sliderRect = rect{x, (a.barH - a.scale(14)) / 2, x + sw, (a.barH + a.scale(14)) / 2}
	x += sw + a.scale(6)

	add := func(cmd int32, label string, fg, bg uint32) {
		w := textWidth(hdc, label) + 2*pad
		a.items = append(a.items, barItem{cmd, label, rect{x, 0, x + w, a.barH}, fg, bg})
		x += w
	}

	add(-1, strconv.Itoa(a.cfg.Alpha)+"%", dimFG, barBG)
	add(cmdFontDown, "−", barFG, barBG)
	add(cmdFontPreset, strconv.Itoa(a.cfg.FontSize), barFG, barBG)
	add(cmdFontUp, "+", barFG, barBG)
	add(cmdBW, " Aa ", pal.fg, pal.marker)

	// Glow gets toggled often, so it lives on the bar: the label glows in
	// the halo color, dim when off. No icon instead of a word: the bar
	// font might not have a fitting glyph, and we'd get an empty square.
	glowFG := uint32(dimFG)
	if a.cfg.Glow {
		glowFG = pal.glow
	}
	add(cmdGlow, txtGlow, glowFG, barBG)
	add(cmdMode, modeNames[a.cfg.Mode], barFG, barBG)

	// "Always on top" and "click-through" moved into settings: with them
	// the bar didn't fit in the default window size. Only what gets
	// toggled constantly stays here.
	settingsFG := uint32(barFG)
	if a.clickThrough {
		settingsFG = 0x6B6BFF // click-through is on — a noticeable state
	}
	add(cmdSettings, txtSettings, settingsFG, barBG)

	// pin "✕" to the right
	w := textWidth(hdc, "✕") + 2*pad
	a.items = append(a.items, barItem{cmdQuit, "✕", rect{width - w, 0, width, a.barH}, barFG, barBG})
}

// paint takes hwnd as a parameter instead of using a.hwnd: the message
// can arrive from inside CreateWindowEx, before the struct field is set.
func (a *app) paint(hwnd uintptr) {
	var ps paintStruct
	hdc := beginPaint(hwnd, &ps)
	c := getClientRect(hwnd)

	// margins around the edit field — window background
	full := rect{0, 0, c.w(), c.h()}
	fillRect(hdc, &full, a.bgBrush)

	if !a.cfg.Compact {
		mem := createCompatibleDC(hdc)
		bmp := createCompatibleBitmap(hdc, c.w(), a.barH)
		oldBmp := selectObject(mem, bmp)
		oldFont := selectObject(mem, a.barFont)

		bar := rect{0, 0, c.w(), a.barH}
		fillRect(mem, &bar, a.barBrush)
		gradientV(mem, bar, barTop, barBG) // the bar is glass too, lighter at the top
		hair := rect{0, a.barH - a.scale(1), c.w(), a.barH}
		fillRect(mem, &hair, a.lineBrush) // a light hairline along the bottom edge
		a.buildBar(mem, c.w())

		// slider
		tr := a.sliderRect
		track := rect{tr.Left, tr.Top + tr.h()/2 - a.scale(2), tr.Right, tr.Top + tr.h()/2 + a.scale(2)}
		fillRect(mem, &track, a.trackBrush)
		kw := a.scale(10)
		kx := tr.Left + (tr.w()-kw)*int32(a.cfg.Alpha)/100
		knob := rect{kx, tr.Top, kx + kw, tr.Bottom}
		oldBr := selectObject(mem, a.knobBrush)
		oldPn := selectObject(mem, a.knobPen)
		roundRect(mem, knob, a.scale(3))
		selectObject(mem, oldPn)
		selectObject(mem, oldBr)

		setBkMode(mem, transparentBkMode)
		for _, it := range a.items {
			if it.bg != barBG {
				r := it.r
				fillRect(mem, &r, brushFor(it.bg))
			}
			setTextColor(mem, it.fg)
			r := it.r
			drawText(mem, it.label, &r, dtSingleLine|dtVCenter|dtCenter)
		}

		bitBlt(hdc, 0, 0, c.w(), a.barH, mem, 0, 0, srcCopy)
		selectObject(mem, oldFont)
		selectObject(mem, oldBmp)
		deleteObject(bmp)
		deleteDC(mem)
	}
	endPaint(hwnd, &ps)
}

// brushFor — a brush colored for the "Aa" sample; a one-color cache, that's all we need.
var (
	cachedBrushColor uint32 = 0xFFFFFFFF
	cachedBrush      uintptr
)

func brushFor(c uint32) uintptr {
	if c != cachedBrushColor {
		deleteObject(cachedBrush)
		cachedBrush = createSolidBrush(c)
		cachedBrushColor = c
	}
	return cachedBrush
}

// ------------------------------------------------------------------- mouse

func (a *app) hitTest(hwnd uintptr, x, y int32) int32 {
	c := getClientRect(hwnd)
	if c.w() == 0 || c.h() == 0 {
		return htClient
	}
	b := a.scale(6)
	// The resize strip at the top is thinner — otherwise it eats into the top of the buttons.
	bt := a.scale(3)
	left, right := x < b, x >= c.w()-b
	top, bottom := y < bt, y >= c.h()-b

	switch {
	case bottom && right:
		return htBottomRight
	case bottom && left:
		return htBottomLeft
	case top && left:
		return htTopLeft
	case top && right:
		return htTopRight
	case bottom:
		return htBottom
	case top:
		return htTop
	case left:
		return htLeft
	case right:
		return htRight
	}
	if !a.cfg.Compact && y < a.barH {
		if a.sliderRect.has(x, y) {
			return htClient
		}
		for _, it := range a.items {
			if it.r.has(x, y) {
				return htClient
			}
		}
		return htCaption // empty space on the bar — drag the window
	}
	return htClient
}

func (a *app) onLButtonDown(x, y int32) {
	a.raiseBack() // the window was grabbed — it's needed on top again
	if a.cfg.Compact || y >= a.barH {
		return
	}
	if a.sliderRect.has(x, y) {
		a.draggingSlider = true
		setCapture(a.hwnd)
		a.alphaFromX(x)
		return
	}
	for _, it := range a.items {
		if it.cmd > 0 && it.r.has(x, y) {
			if it.cmd == cmdSettings {
				// The menu unfolds from the button's bottom edge. The
				// client area equals the whole window, so the offset is its corner.
				wr := getWindowRect(a.hwnd)
				a.settingsMenu(wr.Left+it.r.Left, wr.Top+a.barH)
				return
			}
			a.command(it.cmd)
			setFocus(a.hEdit)
			return
		}
	}
}

func (a *app) onRButtonUp(x, y int32) {
	if a.cfg.Compact || y >= a.barH {
		return
	}
	for _, it := range a.items {
		if it.cmd == cmdBW && it.r.has(x, y) {
			a.setPalette(a.cfg.Palette + 1) // right-click on "Aa" — cycle colors
			return
		}
	}
}

func (a *app) alphaFromX(x int32) {
	tr := a.sliderRect
	kw := a.scale(10)
	v := int((x - tr.Left - kw/2) * 100 / max32(1, tr.w()-kw))
	a.applyAlpha(v)
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// --------------------------------------------------------------- commands

func (a *app) command(cmd int32) {
	// Submenu items arrive as ranges.
	switch {
	case cmd >= cmdPalBase && cmd < cmdPalBase+int32(len(palettes)):
		a.setPalette(int(cmd - cmdPalBase))
		return
	case cmd >= cmdSizeBase && cmd < cmdSizeBase+int32(len(fontPresets)):
		a.setFontSize(fontPresets[cmd-cmdSizeBase])
		return
	case cmd >= cmdAlphaBase && cmd < cmdAlphaBase+int32(len(alphaPresets)):
		a.applyAlpha(alphaPresets[cmd-cmdAlphaBase])
		return
	}

	switch cmd {
	case cmdMode:
		a.setMode(a.cfg.Mode + 1)
	case cmdPalette:
		a.setPalette(a.cfg.Palette + 1)
	case cmdBW:
		if a.cfg.Palette == 1 {
			a.setPalette(0)
		} else {
			a.setPalette(1)
		}
	case cmdTopmost:
		a.cfg.Topmost = !a.cfg.Topmost
		a.dropped = false
		a.applyTopmost()
		invalidate(a.hwnd, nil)
	case cmdCompact:
		a.toggleCompact()
	case cmdClickThrough:
		a.toggleClickThrough()
	case cmdGlow:
		a.toggleGlow()
	case cmdBlur:
		a.toggleBlur()
	case cmdDropBelow:
		a.cfg.DropBelow = !a.cfg.DropBelow
	case cmdAlphaUp:
		a.applyAlpha(a.cfg.Alpha + 5)
	case cmdAlphaDown:
		a.applyAlpha(a.cfg.Alpha - 5)
	case cmdFontUp:
		a.stepFont(1)
	case cmdFontDown:
		a.stepFont(-1)
	case cmdFontPreset:
		a.nextFontPreset()
	case cmdOpen:
		a.openFile()
	case cmdSave:
		a.saveAs()
	case cmdShowHide:
		a.toggleHidden()
	case cmdSettings:
		p := getCursorPos()
		a.settingsMenu(p.X, p.Y)
	case cmdTray:
		a.setTray(!a.cfg.Tray)
	case cmdTaskbar:
		a.setTaskbar(!a.cfg.Taskbar)
	case cmdOpenDir:
		shellOpen(a.dataDir)
	case cmdQuit:
		destroyWindow(a.hwnd)
	}
}

func accelTable() []accel {
	k := func(vk uint16, cmd uint16, alt bool) accel {
		f := byte(fVirtKey | fControl)
		if alt {
			f |= fAlt
		}
		return accel{FVirt: f, Key: vk, Cmd: cmd}
	}
	return []accel{
		k('M', cmdMode, false),
		k('P', cmdPalette, false),
		k('T', cmdTopmost, false),
		k('H', cmdCompact, false),
		k('O', cmdOpen, false),
		k('S', cmdSave, false),
		k('Q', cmdQuit, false),
		k(0x26, cmdAlphaUp, false),   // VK_UP
		k(0x28, cmdAlphaDown, false), // VK_DOWN
		k(0xBB, cmdFontUp, false),    // VK_OEM_PLUS
		k(0xBD, cmdFontDown, false),  // VK_OEM_MINUS
		k(0x6B, cmdFontUp, false),    // VK_ADD
		k(0x6D, cmdFontDown, false),  // VK_SUBTRACT
		k('G', cmdGlow, false),
		k('E', cmdClickThrough, true),
	}
}

func (a *app) registerHotKeys() {
	// Global — to bring the window back when opacity is 0% or
	// click-through is on and the mouse can't reach the window.
	registerHotKey(a.hwnd, hkAlphaUp, modControl|modAlt, 0x26)
	registerHotKey(a.hwnd, hkAlphaDown, modControl|modAlt, 0x28)
	registerHotKey(a.hwnd, hkClickThrough, modControl|modAlt|modNoRepeat, 'E')
	registerHotKey(a.hwnd, hkHideAll, modControl|modAlt|modNoRepeat, 'H')
}

func (a *app) unregisterHotKeys() {
	for id := int32(hkAlphaUp); id <= hkHideAll; id++ {
		unregisterHotKey(a.hwnd, id)
	}
}

// ----------------------------------------------------------------- text

func (a *app) text() string {
	n := int(sendMessage(a.hEdit, wmGetTextLength, 0, 0))
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	sendMessage(a.hEdit, wmGetText, uintptr(n+1), uintptr(unsafe.Pointer(&buf[0])))
	runtime.KeepAlive(buf)
	return syscall.UTF16ToString(buf)
}

func (a *app) setText(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", "\r\n")
	p := str16(s)
	sendMessage(a.hEdit, wmSetText, 0, uintptr(unsafe.Pointer(p)))
	runtime.KeepAlive(p)
}

func (a *app) loadNote() {
	data, err := os.ReadFile(a.notePath)
	if err != nil {
		a.setText(welcomeNote)
		a.savedText = a.text()
		return
	}
	a.setText(string(data))
	a.savedText = a.text()
}

// saveNote reads the text on the message thread but writes to disk on
// the side: if the antivirus or the disk hangs for a second, the window
// shouldn't freeze along with it — or Windows would declare it not responding.
func (a *app) saveNote() {
	t := a.text()
	if t == a.savedText {
		return
	}
	a.savedText = t
	out := strings.ReplaceAll(t, "\r\n", "\n")
	path := a.notePath
	go func() { os.WriteFile(path, []byte(out), 0o644) }()
}

func (a *app) saveNoteSync() {
	t := a.text()
	a.savedText = t
	os.WriteFile(a.notePath, []byte(strings.ReplaceAll(t, "\r\n", "\n")), 0o644)
}

func (a *app) openFile() {
	path := fileDialog(a.hwnd, false, "txt")
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		messageBox(a.hwnd, txtOpenFailed+path, appTitle, 0x10)
		return
	}
	a.setText(string(data))
}

func (a *app) saveAs() {
	path := fileDialog(a.hwnd, true, "txt")
	if path == "" {
		return
	}
	out := strings.ReplaceAll(a.text(), "\r\n", "\n")
	if os.WriteFile(path, []byte(out), 0o644) != nil {
		messageBox(a.hwnd, txtSaveFailed+path, appTitle, 0x10)
	}
}

// --------------------------------------------------------------- settings

func loadConfig(path string) config {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	if json.Unmarshal(data, &cfg) != nil {
		return defaultConfig()
	}
	if cfg.Version < cfgVersion {
		// Settings from a previous version: they have no glow/blur
		// fields, and a missing JSON field means false, i.e. "off".
		// Silently turning off new features on update is wrong, so we
		// turn them on ourselves.
		cfg.Glow, cfg.Blur = true, true
		if cfg.Mode == modeMarker {
			cfg.Mode = modePlain // the plate is opaque and would hide the glow
		}
		cfg.Version = cfgVersion
	}
	if cfg.Alpha < 0 || cfg.Alpha > 100 {
		cfg.Alpha = 80
	}
	if cfg.Mode < 0 || cfg.Mode >= len(modeNames) {
		cfg.Mode = modeMarker // the old "Text" mode = 2 also lands here
	}
	if cfg.Palette < 0 || cfg.Palette >= len(palettes) {
		cfg.Palette = 0
	}
	if cfg.FontSize < fontMin || cfg.FontSize > fontMax {
		cfg.FontSize = 16
	}
	if !cfg.Tray && !cfg.Taskbar {
		cfg.Tray = true // there must always be at least one way to reach the window
	}
	if cfg.W < 220 {
		cfg.W = 560
	}
	if cfg.H < 120 {
		cfg.H = 360
	}
	return cfg
}

func (a *app) saveConfig() {
	r := getWindowRect(a.hwnd)
	a.cfg.X, a.cfg.Y = int(r.Left), int(r.Top)
	a.cfg.W, a.cfg.H = int(r.w()), int(r.h())
	if data, err := json.MarshalIndent(a.cfg, "", "  "); err == nil {
		os.WriteFile(a.cfgPath, data, 0o644)
	}
}

// ------------------------------------------------------------- window procedure

// backProc — the backdrop window: the actual glass. LWA_ALPHA applies to
// it, not to the text, so the letters don't fade along with the background.
func backProc(hwnd, m, wp, lp uintptr) uintptr {
	switch m {
	case wmEraseBkgnd:
		return 1

	case wmLButtonDblClk:
		// A double-click on empty space in the note punches through to what's below.
		a.punchArm()
		return 0

	case wmLButtonUp:
		a.punchRelease()
		return 0

	case wmLButtonDown:
		// A click on the backdrop = a click on the note: raise the
		// window and move focus to the text so typing can start right away.
		if a.hwnd != 0 {
			a.raiseBack()
			setForegroundWindow(a.hwnd)
			setFocus(a.hEdit)
		}
		return 0
	case wmPaint:
		// Draw through an intermediate canvas: there are several
		// layers, and without it the gradients would flicker through
		// each other on every update.
		var ps paintStruct
		hdc := beginPaint(hwnd, &ps)
		c := getClientRect(hwnd)
		r := rect{0, 0, c.w(), c.h()}
		mem := createCompatibleDC(hdc)
		bmp := createCompatibleBitmap(hdc, c.w(), c.h())
		old := selectObject(mem, bmp)
		if a.backBrush != 0 {
			fillRect(mem, &r, a.backBrush) // in case there's no gradient
		}
		a.paintGlass(mem, c)
		bitBlt(hdc, 0, 0, c.w(), c.h(), mem, 0, 0, srcCopy)
		selectObject(mem, old)
		deleteObject(bmp)
		deleteDC(mem)
		endPaint(hwnd, &ps)
		return 0
	}
	return defWindowProc(hwnd, uint32(m), wp, lp)
}

// All parameters are uintptr: syscall.NewCallback only accepts
// word-sized arguments, uint32 here would panic when registering the callback.
func wndProc(hwnd, m, wp, lp uintptr) uintptr {
	seq := logMsgIn("main", m)
	defer logMsgOut(seq, m)

	switch m {
	case wmNCCalcSize:
		if wp != 0 {
			return 0 // client area = the whole window, we draw the frame ourselves
		}

	// The window draws its own frame (see wmNCCalcSize), but on losing
	// focus DefWindowProc still repaints the WS_THICKFRAME non-client
	// area — and a white frame flashed at the edges, staying until the
	// next SetWindowPos. lParam = -1 says "update the state, don't
	// repaint", so we don't override the handling, just suppress the redraw.
	case wmNCActivate:
		return defWindowProc(hwnd, uint32(m), wp, ^uintptr(0))

	case wmNCHitTest:
		p := point{loWord(lp), hiWord(lp)}
		wr := getWindowRect(hwnd)
		return uintptr(a.hitTest(hwnd, p.X-wr.Left, p.Y-wr.Top))

	case wmGetMinMaxInfo:
		// go vet complains about unsafe.Pointer(lp) — that's fine here:
		// lParam is exactly the MINMAXINFO pointer the system handed us.
		mmi := (*minMaxInfo)(unsafe.Pointer(lp))
		mmi.PtMinTrackSize = point{a.scale(260), a.scale(90)}
		return 0

	case wmEraseBkgnd:
		return 1

	case wmPaint:
		a.paint(hwnd)
		return 0

	case wmSize:
		a.layoutChildren()
		invalidate(hwnd, nil)
		a.renderGlow()
		return 0

	case wmWindowPosChgd:
		// One message covers move, resize, and z-order changes — that's
		// enough for the backdrop to never fall behind.
		a.syncBackdrop()
		return defWindowProc(hwnd, uint32(m), wp, lp)

	// WM_CTLCOLORSTATIC — in case the system decides to paint the field
	// as static: the colors must match, or the background turns system white.
	case wmCtlColorEdit, wmCtlColorStat:
		pal := palettes[a.cfg.Palette%len(palettes)]
		setTextColor(wp, pal.fg)
		// The background mode is always opaque. EDIT redraws the changed
		// line with a single TextOut and relies on it to overwrite the old
		// pixels with the background color; with transparentBkMode there's
		// no overwrite, and the erased letter stays on screen with the new
		// one drawn on top of it. Outside marker mode the background color
		// is the color key, i.e. transparent: the look stays the same, and
		// the old glyphs go away.
		bk := colorKey
		if a.cfg.Mode == modeMarker {
			bk = pal.marker // opaque plate behind the letters
		}
		setBkColor(wp, bk)
		setBkMode(wp, opaqueBkMode)
		return a.bgBrush // color key: the backdrop shows through the background

	case wmLButtonDown:
		a.onLButtonDown(loWord(lp), hiWord(lp))
		return 0

	case wmLButtonDblClk:
		// A double-click on the bar doesn't punch through anything: those are buttons.
		if a.cfg.Compact || hiWord(lp) >= a.barH {
			a.punchArm()
		}
		return 0

	case wmMouseMove:
		if a.draggingSlider {
			a.alphaFromX(loWord(lp))
		}
		return 0

	case wmLButtonUp:
		if a.draggingSlider {
			a.draggingSlider = false
			releaseCapture()
			return 0
		}
		a.punchRelease()
		return 0

	case wmRButtonUp:
		a.onRButtonUp(loWord(lp), hiWord(lp))
		return 0

	case wmCommand:
		if loWord(wp) == idEdit {
			// The text changed or was scrolled — time to redraw the glow.
			switch hiWord(wp) {
			case enChange, enVScroll:
				a.glowDirty = true
			}
			return 0
		}
		a.command(int32(loWord(wp)))
		return 0

	case wmTrayIcon:
		switch uint32(lp) {
		case wmRButtonUp, 0x0204: // right button — menu
			a.trayMenu()
		case 0x0203: // double left-click — show/hide
			a.toggleHidden()
		}
		return 0

	case wmHotKey:
		switch int32(wp) {
		case hkAlphaUp:
			a.applyAlpha(a.cfg.Alpha + 5)
		case hkAlphaDown:
			a.applyAlpha(a.cfg.Alpha - 5)
		case hkClickThrough:
			a.toggleClickThrough()
		case hkHideAll:
			a.toggleHidden()
		}
		return 0

	case wmTimer:
		switch wp {
		case timerSave:
			a.saveNote()
		case timerPunch:
			a.endPunch()
		case timerArm:
			a.disarmPunch()
		case timerGlow:
			a.glowTick()
		case timerBeat:
			// Heartbeat: as long as these lines keep coming, the message
			// loop is alive. If the log cuts off at "IN #N" without "OUT
			// #N" — it got stuck on that message.
			a.beats++
			logf("heartbeat %d", a.beats)
		}
		return 0

	case wmSetFocusMsg:
		if a.hEdit != 0 {
			setFocus(a.hEdit)
		}
		return 0

	case wmClose:
		destroyWindow(hwnd)
		return 0

	case wmDestroy:
		logf("WM_DESTROY: saving and exiting")
		a.saveNoteSync()
		a.saveConfig()
		a.removeTrayIcon()
		a.unregisterHotKeys()
		if a.hGlow != 0 {
			destroyWindow(a.hGlow)
			a.hGlow = 0
		}
		a.glow.free()
		if a.hBack != 0 {
			destroyWindow(a.hBack)
			a.hBack = 0
		}
		postQuitMessage(0)
		return 0
	}
	return defWindowProc(hwnd, uint32(m), wp, lp)
}
