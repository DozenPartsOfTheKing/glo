//go:build windows

// A custom dropdown menu. The system's TrackPopupMenu draws a window owned
// by the system: you can't strip its white background, its border, or its
// opacity — on top of the glass note it looked like a foreign patch. So the
// menu here is our own: the same layers, the same glass, the same blur as
// the note itself.
//
// Design. Each menu is a window with per-pixel transparency
// (UpdateLayeredWindow): the glass is translucent, the letters on top of it
// are solid. The glow is built the same way, except there the mask gets
// blurred, while here it paints the labels. The mouse is captured by the
// root window for the whole time the menu is shown, so all clicks and moves
// arrive at one procedure, and the submenu doesn't need to catch events
// itself — it only draws.
package main

import (
	"runtime"
	"unsafe"
)

const menuClass = "GloMenuWnd"

// The menu is denser than the note: you need to read labels through it, not wallpaper.
const menuAlpha = 224

const (
	wmKeyDown     = 0x0100
	wmKeyUp       = 0x0101
	wmChar        = 0x0102
	wmSysKeyDown  = 0x0104
	wmSysKeyUp    = 0x0105
	wmSysChar     = 0x0106
	wmCaptureChgd = 0x0215

	vkReturn = 0x0D
	vkEscape = 0x1B
	vkLeft   = 0x25
	vkUp     = 0x26
	vkRight  = 0x27
	vkDown   = 0x28

	monitorNearest = 0x00000002
)

var (
	pMonitorFromPoint = user32.NewProc("MonitorFromPoint")
	pGetMonitorInfo   = user32.NewProc("GetMonitorInfoW")
)

type monitorInfo struct {
	CbSize            uint32
	RcMonitor, RcWork rect
	DwFlags           uint32
}

// workArea — the work area of the monitor the point lies on (excluding the
// taskbar). A single GetSystemMetrics call would suffice, but on a second
// monitor the menu would then end up on the first one.
func workArea(p point) rect {
	mon, _, _ := pMonitorFromPoint.Call(
		uintptr(uint32(p.X))|uintptr(uint32(p.Y))<<32, monitorNearest)
	if mon != 0 {
		mi := monitorInfo{}
		mi.CbSize = uint32(unsafe.Sizeof(mi))
		r, _, _ := pGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi)))
		runtime.KeepAlive(&mi)
		if r != 0 {
			return mi.RcWork
		}
	}
	return rect{0, 0, getSystemMetrics(0), getSystemMetrics(1)}
}

// ------------------------------------------------------------------ items

// mItem — a single item. An empty label with sep=true gives a separator, a
// non-empty sub gives a submenu (then cmd isn't needed).
type mItem struct {
	cmd   int32
	label string
	accel string // hotkey label shown on the right
	check bool
	sep   bool
	sub   []mItem
}

// ------------------------------------------------------------------ canvas

// menuSurface — the same trick as the glow: a DIB that GDI draws into, and a
// parallel buffer out holding real alpha. GDI doesn't know about alpha and
// scribbles garbage into it, so the final pixels are assembled by hand.
type menuSurface struct {
	dc, bmp, oldBmp uintptr
	px              []uint32 // what GDI draws into
	out             []uint32 // what goes into UpdateLayeredWindow
	cov             []uint8  // rounded-corner coverage, 0..255
	w, h            int32
}

func (s *menuSurface) free() {
	if s.dc != 0 {
		selectObject(s.dc, s.oldBmp)
		deleteDC(s.dc)
	}
	deleteObject(s.bmp)
	*s = menuSurface{}
}

func (s *menuSurface) resize(w, h, radius int32) bool {
	if w <= 0 || h <= 0 {
		return false
	}
	if s.dc != 0 && s.w == w && s.h == h {
		return true
	}
	s.free()
	screen := getDC(0)
	dc := createCompatibleDC(screen)
	releaseDC(0, screen)
	if dc == 0 {
		return false
	}
	bmp, bits := createDIBSection(dc, w, h)
	if bmp == 0 || bits == nil {
		deleteDC(dc)
		return false
	}
	n := int(w) * int(h)
	*s = menuSurface{
		dc: dc, bmp: bmp, oldBmp: selectObject(dc, bmp),
		px:  unsafe.Slice((*uint32)(bits), n),
		out: make([]uint32, n),
		cov: roundCoverage(w, h, radius),
		w:   w, h: h,
	}
	return true
}

// roundCoverage — the fraction of a pixel inside a rounded rectangle. With a
// region (SetWindowRgn) the corners would come out stepped: a region only
// knows "inside" and "outside". Here edge pixels get their own opacity, and
// the cut comes out smooth. Computed once per size, read thereafter.
func roundCoverage(w, h, r int32) []uint8 {
	cov := make([]uint8, int(w)*int(h))
	for i := range cov {
		cov[i] = 255
	}
	if r > w/2 {
		r = w / 2
	}
	if r > h/2 {
		r = h / 2
	}
	if r <= 0 {
		return cov
	}
	const ss = 4 // 4x4 subsamples per pixel — enough for the eye already
	cx := [2]int32{r, w - r}
	cy := [2]int32{r, h - r}
	x0 := [2]int32{0, w - r}
	y0 := [2]int32{0, h - r}
	rr := float64(r) * float64(r)
	for by := 0; by < 2; by++ {
		for bx := 0; bx < 2; bx++ {
			for y := y0[by]; y < y0[by]+r; y++ {
				for x := x0[bx]; x < x0[bx]+r; x++ {
					n := 0
					for sy := 0; sy < ss; sy++ {
						for sx := 0; sx < ss; sx++ {
							dx := float64(x) + (float64(sx)+0.5)/ss - float64(cx[bx])
							dy := float64(y) + (float64(sy)+0.5)/ss - float64(cy[by])
							if dx*dx+dy*dy <= rr {
								n++
							}
						}
					}
					cov[int(y)*int(w)+int(x)] = uint8(n * 255 / (ss * ss))
				}
			}
		}
	}
	return cov
}

// dibOf reorders COLORREF bytes (0x00BBGGRR) into DIB pixel order
// (0x00RRGGBB). Without this, red on the canvas comes out blue.
func dibOf(c uint32) uint32 {
	return uint32(chanOf(c, 0))<<16 | uint32(chanOf(c, 1))<<8 | uint32(chanOf(c, 2))
}

// premul — a GDI pixel with a given alpha, already multiplied by it:
// UpdateLayeredWindow only accepts premultiplied colors.
func premul(p uint32, al int32) uint32 {
	r := int32((p>>16)&0xFF) * al / 255
	g := int32((p>>8)&0xFF) * al / 255
	b := int32(p&0xFF) * al / 255
	return uint32(al)<<24 | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
}

// over places opaque color src on top of dst with coverage cov (0..255).
// Both sides are premultiplied, so this is an ordinary "source over".
func over(dst, src uint32, cov int32) uint32 {
	if cov <= 0 {
		return dst
	}
	if cov > 255 {
		cov = 255
	}
	inv := 255 - cov
	al := cov + int32(dst>>24)*inv/255
	r := int32((src>>16)&0xFF)*cov/255 + int32((dst>>16)&0xFF)*inv/255
	g := int32((src>>8)&0xFF)*cov/255 + int32((dst>>8)&0xFF)*inv/255
	b := int32(src&0xFF)*cov/255 + int32(dst&0xFF)*inv/255
	return uint32(al)<<24 | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
}

// ------------------------------------------------------------------ window

type menuWin struct {
	h     uintptr
	items []mItem
	rows  []rect // rows in canvas coordinates
	hot   int    // highlighted row, -1 — none
	w, ht int32
	x, y  int32

	textX, accelR, arrowX int32
	surf                  menuSurface
}

func (m *menuWin) free() {
	m.surf.free()
	if m.h != 0 {
		destroyWindow(m.h)
		m.h = 0
	}
}

func (m *menuWin) rect() rect { return rect{m.x, m.y, m.x + m.w, m.y + m.ht} }

// at — the row under the point (screen coordinates), or -1.
func (m *menuWin) at(p point) int {
	if m == nil || !m.rect().has(p.X, p.Y) {
		return -1
	}
	x, y := p.X-m.x, p.Y-m.y
	for i, r := range m.rows {
		if r.has(x, y) {
			if m.items[i].sep {
				return -1
			}
			return i
		}
	}
	return -1
}

func (a *app) measureMenu(m *menuWin) {
	dc := getDC(0)
	old := selectObject(dc, a.menuFont)

	padL, padR := a.scale(12), a.scale(12)
	checkW := a.scale(22)
	rowH, sepH := a.scale(28), a.scale(9)

	var maxL, maxA, arrowW int32
	for _, it := range m.items {
		if it.sep {
			continue
		}
		if w := textWidth(dc, it.label); w > maxL {
			maxL = w
		}
		if w := textWidth(dc, it.accel); w > maxA {
			maxA = w
		}
		if it.sub != nil {
			arrowW = a.scale(18)
		}
	}
	if maxA > 0 {
		maxA += a.scale(30) // gap between the label and the hotkey
	}

	m.w = padL + checkW + maxL + maxA + arrowW + padR
	m.textX = padL + checkW
	m.arrowX = m.w - padR - arrowW
	m.accelR = m.arrowX
	if arrowW == 0 {
		m.accelR = m.w - padR
	}

	y := a.scale(6)
	m.rows = m.rows[:0]
	for _, it := range m.items {
		h := rowH
		if it.sep {
			h = sepH
		}
		m.rows = append(m.rows, rect{0, y, m.w, y + h})
		y += h
	}
	m.ht = y + a.scale(6)

	selectObject(dc, old)
	releaseDC(0, dc)
}

// ------------------------------------------------------------------ drawing

func (a *app) renderMenu(m *menuWin) {
	rad := a.scale(10)
	if !m.surf.resize(m.w, m.ht, rad) {
		return
	}
	s := &m.surf
	oldFont := selectObject(s.dc, a.menuFont)

	a.paintMenuBack(m, s, rad)
	for i, p := range s.px {
		s.out[i] = premul(p, menuAlpha*int32(s.cov[i])/255)
	}

	// Text isn't placed on top of the GDI image but in separate passes: each
	// one is drawn white on black, and brightness becomes coverage.
	// Otherwise the anti-aliased letter edges would blend with the
	// background before the pixel even had alpha, leaving grime along the
	// glyph outlines.
	for grp := 0; grp < 3; grp++ {
		for i := range s.px {
			s.px[i] = 0
		}
		setBkMode(s.dc, transparentBkMode)
		setTextColor(s.dc, 0xFFFFFF)
		if !a.drawMenuText(m, s.dc, grp) {
			continue
		}
		col := dibOf([3]uint32{barFG, dimFG, barHot}[grp])
		for i := range s.px {
			c := int32(s.px[i] & 0xFF)
			if c == 0 {
				continue
			}
			s.out[i] = over(s.out[i], col, c*int32(s.cov[i])/255)
		}
	}

	copy(s.px, s.out)
	selectObject(s.dc, oldFont)
	updateLayered(m.h, point{m.x, m.y}, size{s.w, s.h}, s.dc)
}

func (a *app) paintMenuBack(m *menuWin, s *menuSurface, rad int32) {
	full := rect{0, 0, s.w, s.h}
	gradientV(s.dc, full, barTop, barBG) // lighter at the top — like the note's bar

	if m.hot >= 0 && m.hot < len(m.rows) && !m.items[m.hot].sep {
		r := m.rows[m.hot]
		r.Left += a.scale(4)
		r.Right -= a.scale(4)
		fill := shade(barBG, 34)
		br := createSolidBrush(fill)
		pn := createPen(fill, 1)
		ob, op := selectObject(s.dc, br), selectObject(s.dc, pn)
		roundRect(s.dc, r, a.scale(6))
		selectObject(s.dc, op)
		selectObject(s.dc, ob)
		deleteObject(br)
		deleteObject(pn)
	}

	line := createSolidBrush(shade(barBG, 26))
	for i, it := range m.items {
		if !it.sep {
			continue
		}
		y := m.rows[i].Top + m.rows[i].h()/2
		ln := rect{a.scale(10), y, s.w - a.scale(10), y + a.scale(1)}
		fillRect(s.dc, &ln, line)
	}
	deleteObject(line)

	pen := createPen(shade(barBG, 40), a.scale(1))
	op := selectObject(s.dc, pen)
	ob := selectObject(s.dc, getStockObject(nullBrush))
	roundRect(s.dc, full, rad)
	selectObject(s.dc, ob)
	selectObject(s.dc, op)
	deleteObject(pen)
}

// drawMenuText draws a single color layer and reports whether there was
// anything to draw: 0 — regular labels, 1 — hotkeys, 2 — everything bright
// (the highlighted row, checkmarks, submenu arrows).
func (a *app) drawMenuText(m *menuWin, dc uintptr, grp int) bool {
	drew := false
	for i, it := range m.items {
		if it.sep {
			continue
		}
		row := m.rows[i]
		hot := i == m.hot
		switch grp {
		case 0:
			if hot || it.label == "" {
				continue
			}
			r := rect{m.textX, row.Top, m.accelR, row.Bottom}
			drawText(dc, it.label, &r, dtSingleLine|dtVCenter|dtLeft|dtNoPrefix)
		case 1:
			if it.accel == "" {
				continue
			}
			r := rect{m.textX, row.Top, m.accelR, row.Bottom}
			drawText(dc, it.accel, &r, dtSingleLine|dtVCenter|dtRight|dtNoPrefix)
		case 2:
			if hot && it.label != "" {
				r := rect{m.textX, row.Top, m.accelR, row.Bottom}
				drawText(dc, it.label, &r, dtSingleLine|dtVCenter|dtLeft|dtNoPrefix)
			}
			if it.check {
				r := rect{a.scale(12), row.Top, m.textX, row.Bottom}
				drawText(dc, "✓", &r, dtSingleLine|dtVCenter|dtCenter|dtNoPrefix)
			}
			if it.sub != nil {
				r := rect{m.arrowX, row.Top, m.w - a.scale(12), row.Bottom}
				drawText(dc, "›", &r, dtSingleLine|dtVCenter|dtRight|dtNoPrefix)
			}
		default:
			continue
		}
		drew = true
	}
	return drew
}

// ------------------------------------------------------------------ display

func (a *app) newMenuWin(items []mItem) *menuWin {
	m := &menuWin{items: items, hot: -1}
	a.measureMenu(m)
	m.h = createWindowEx(
		wsExLayered|wsExToolWindow|wsExNoActivate|wsExTopmost,
		str16(menuClass), str16(appTitle), wsPopup,
		0, 0, m.w, m.ht, 0, 0, a.hInst)
	if m.h != 0 {
		setBlurBehind(m.h, a.cfg.Blur)
	}
	return m
}

// showMenuWin positions the window so it fits fully on screen, then shows it
// without taking focus: the note beneath the menu stays active.
func (a *app) showMenuWin(m *menuWin, x, y int32) {
	wa := workArea(point{x, y})
	if x+m.w > wa.Right {
		x = wa.Right - m.w
	}
	if x < wa.Left {
		x = wa.Left
	}
	if y+m.ht > wa.Bottom {
		y = wa.Bottom - m.ht
	}
	if y < wa.Top {
		y = wa.Top
	}
	m.x, m.y = x, y
	a.renderMenu(m) // UpdateLayeredWindow also moves the window into place
	showWindow(m.h, swShowNA)
}

// ------------------------------------------------------------------ session

// Display session. The menu is modal: while it's on screen, its own message
// loop spins, and the mouse is captured by the root window.
type menuSess struct {
	root   *menuWin
	child  *menuWin
	openAt int // root row whose submenu is open, -1 — none
	result int32
	done   bool

	origin point // where the mouse was at the moment of opening
	moved  bool  // whether the cursor has since left that spot
}

var menuSes *menuSess

// inside — whether the point falls inside any of the menu windows.
func (s *menuSess) inside(p point) bool {
	if s.root != nil && s.root.rect().has(p.X, p.Y) {
		return true
	}
	return s.child != nil && s.child.rect().has(p.X, p.Y)
}

// trackMenu shows the menu and returns the chosen command (0 — cancelled).
func (a *app) trackMenu(items []mItem, x, y int32) int32 {
	if menuSes != nil || len(items) == 0 {
		return 0
	}
	setForegroundWindow(a.hwnd)
	root := a.newMenuWin(items)
	if root.h == 0 {
		return 0
	}
	menuSes = &menuSess{root: root, openAt: -1, origin: getCursorPos()}
	a.showMenuWin(root, x, y)
	setCapture(root.h)

	var m msg
	for !menuSes.done {
		if getMessage(&m) <= 0 {
			postQuitMessage(0) // WM_QUIT needs to be returned to the main loop
			break
		}
		if menuKey(&m) {
			continue // keys don't reach the edit control while the menu is open
		}
		translateMessage(&m)
		dispatchMessage(&m)
	}

	releaseCapture()
	res := menuSes.result
	if menuSes.child != nil {
		menuSes.child.free()
	}
	root.free()
	menuSes = nil
	return res
}

func menuCloseChild() {
	if menuSes.child != nil {
		menuSes.child.free()
		menuSes.child = nil
	}
	menuSes.openAt = -1
}

func (a *app) menuOpenChild(idx int) {
	menuCloseChild()
	it := menuSes.root.items[idx]
	if it.sub == nil {
		return
	}
	c := a.newMenuWin(it.sub)
	if c.h == 0 {
		return
	}
	row := menuSes.root.rows[idx]
	x := menuSes.root.x + menuSes.root.w - a.scale(4)
	wa := workArea(point{x, menuSes.root.y})
	if x+c.w > wa.Right {
		x = menuSes.root.x - c.w + a.scale(4) // didn't fit on the right — open left instead
	}
	menuSes.child = c
	menuSes.openAt = idx
	a.showMenuWin(c, x, menuSes.root.y+row.Top-a.scale(6))
}

// menuHover drives the highlight and submenu based on cursor position.
func (a *app) menuHover(p point) {
	s := menuSes
	if s.child != nil {
		if i := s.child.at(p); i >= 0 {
			if s.child.hot != i {
				s.child.hot = i
				a.renderMenu(s.child)
			}
			return
		}
	}
	i := s.root.at(p)
	if i < 0 {
		// Cursor is off everything: clear the highlight in the submenu, but
		// leave the submenu itself open — otherwise it would slam shut on
		// the way to it.
		if s.child != nil && s.child.hot != -1 {
			s.child.hot = -1
			a.renderMenu(s.child)
		}
		return
	}
	if s.root.hot != i {
		s.root.hot = i
		a.renderMenu(s.root)
	}
	if s.root.items[i].sub != nil {
		if s.openAt != i {
			a.menuOpenChild(i)
		}
	} else if s.openAt >= 0 {
		menuCloseChild()
	}
}

func (a *app) menuPick(m *menuWin, idx int) {
	if m == nil || idx < 0 || idx >= len(m.items) {
		return
	}
	it := m.items[idx]
	if it.sep {
		return
	}
	if it.sub != nil {
		if m == menuSes.root && menuSes.openAt != idx {
			a.menuOpenChild(idx)
		}
		return
	}
	menuSes.result = it.cmd
	menuSes.done = true
}

// menuProc serves both menu windows, but the mouse only arrives at the root
// one: it holds the capture, so clicks outside the menu land here too.
func menuProc(hwnd, m, wp, lp uintptr) uintptr {
	if menuSes == nil {
		return defWindowProc(hwnd, uint32(m), wp, lp)
	}
	switch m {
	case wmMouseMove:
		p := getCursorPos()
		d := a.scale(4)
		if p.X-menuSes.origin.X > d || menuSes.origin.X-p.X > d ||
			p.Y-menuSes.origin.Y > d || menuSes.origin.Y-p.Y > d {
			menuSes.moved = true
		}
		a.menuHover(p)
		return 0

	case wmLButtonDown:
		p := getCursorPos()
		if !menuSes.inside(p) {
			menuSes.done = true // click outside — just close
		}
		return 0

	case wmLButtonUp:
		// The release of the button that opened the menu arrives here too —
		// we already hold the mouse capture by this point. As long as the
		// cursor hasn't moved from where it opened, this doesn't count as a
		// selection: otherwise a menu that popped up under the cursor
		// (near the bottom of the screen it opens upward) would immediately
		// fire whatever item happened to land there.
		if !menuSes.moved {
			return 0
		}
		p := getCursorPos()
		if i := menuSes.child.at(p); i >= 0 {
			a.menuPick(menuSes.child, i)
			return 0
		}
		if i := menuSes.root.at(p); i >= 0 {
			a.menuPick(menuSes.root, i)
		}
		return 0

	case wmRButtonUp:
		return 0

	case wmCaptureChgd:
		menuSes.done = true // capture was taken away — nothing left to hold the menu open
		return 0
	}
	return defWindowProc(hwnd, uint32(m), wp, lp)
}

// menuKey handles the keyboard right in the loop: the menu window doesn't
// take focus, so keystrokes are addressed to the edit control and would
// never reach the menu procedure. Everything pressed while the menu is open
// gets eaten here — otherwise arrow keys would move the caret, and letters
// would fall into the note's text.
func menuKey(m *msg) bool {
	switch m.Message {
	case wmChar, wmSysChar, wmKeyUp, wmSysKeyUp:
		return true
	case wmKeyDown, wmSysKeyDown:
	default:
		return false
	}

	s := menuSes
	act := s.root
	if s.child != nil {
		act = s.child
	}
	switch m.WParam {
	case vkEscape:
		if s.child != nil {
			menuCloseChild()
			a.renderMenu(s.root)
		} else {
			s.done = true
		}
	case vkUp:
		menuStep(act, -1)
	case vkDown:
		menuStep(act, +1)
	case vkRight:
		if s.child == nil && s.root.hot >= 0 && s.root.items[s.root.hot].sub != nil {
			a.menuOpenChild(s.root.hot)
			menuStep(s.child, +1)
		}
	case vkLeft:
		if s.child != nil {
			menuCloseChild()
			a.renderMenu(s.root)
		}
	case vkReturn:
		a.menuPick(act, act.hot)
	}
	return true
}

// menuStep moves the highlight to the next selectable item, wrapping around.
func menuStep(m *menuWin, d int) {
	if m == nil || len(m.items) == 0 {
		return
	}
	i := m.hot
	for n := 0; n < len(m.items); n++ {
		i += d
		if i < 0 {
			i = len(m.items) - 1
		}
		if i >= len(m.items) {
			i = 0
		}
		if !m.items[i].sep {
			m.hot = i
			a.renderMenu(m)
			return
		}
	}
}
