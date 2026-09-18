//go:build windows

// Glass and glow: everything that draws the "liquid glass" backdrop and the
// neon halo around the letters, plus the Win32 wrappers needed only here.
package main

import (
	"runtime"
	"syscall"
	"unsafe"
)

var (
	msimg32 = syscall.NewLazyDLL("msimg32.dll")

	pGradientFill   = msimg32.NewProc("GradientFill")
	pCreateDIBSect  = gdi32.NewProc("CreateDIBSection")
	pCreateRoundRgn = gdi32.NewProc("CreateRoundRectRgn")
	pCreatePen      = gdi32.NewProc("CreatePen")
	pRoundRect      = gdi32.NewProc("RoundRect")
	pGetStockObj    = gdi32.NewProc("GetStockObject")

	pUpdateLayered  = user32.NewProc("UpdateLayeredWindow")
	pSetWindowRgn   = user32.NewProc("SetWindowRgn")
	pCallWindowProc = user32.NewProc("CallWindowProcW")
	pSendInput      = user32.NewProc("SendInput")
	pKillTimer      = user32.NewProc("KillTimer")
	pLoadImage      = user32.NewProc("LoadImageW")

	// Blur-behind-window — an undocumented function that has existed since
	// Win10. If it's missing, the glass simply stays without blur.
	pSetWinCompAttr = user32.NewProc("SetWindowCompositionAttribute")
)

const (
	gwlWndProc = -4

	csDblClks       = 0x0008
	wmLButtonDblClk = 0x0203
	wmVScroll       = 0x0115

	emGetFirstVisibleLine = 0x00CE
	emLineIndex           = 0x00BB
	emSetMargins          = 0x00D3
	ecLeftMargin          = 0x0001
	ecRightMargin         = 0x0002
	enChange              = 0x0300
	enVScroll             = 0x0602

	dtNoPrefix    = 0x0800
	dtWordBreak   = 0x0010
	dtExpandTabs  = 0x0040
	dtEditControl = 0x2000
	dtNoClip      = 0x0100

	ulwAlpha   = 0x02
	acSrcOver  = 0x00
	acSrcAlpha = 0x01

	gradientFillRectV = 0x00000001
	dibRGBColors      = 0

	nullBrush = 5
	psSolid   = 0

	imageIcon      = 1
	lrDefaultColor = 0x0000

	mouseEventLeftDown = 0x0002
	mouseEventLeftUp   = 0x0004
	inputMouse         = 0

	// WCA_ACCENT_POLICY / ACCENT_ENABLE_BLURBEHIND
	wcaAccentPolicy  = 19
	accentEnableBlur = 3
	accentDisabled   = 0
)

// ------------------------------------------------------------------ structs

type trivertex struct {
	X, Y                    int32
	Red, Green, Blue, Alpha uint16
}

type gradientRect struct{ UpperLeft, LowerRight uint32 }

type blendFunction struct {
	BlendOp, BlendFlags, SourceConstantAlpha, AlphaFormat byte
}

type bitmapInfoHeader struct {
	Size                         uint32
	Width, Height                int32
	Planes, BitCount             uint16
	Compression, SizeImage       uint32
	XPelsPerMeter, YPelsPerMeter int32
	ClrUsed, ClrImportant        uint32
}

// mouseInput — INPUT with a MOUSEINPUT inside. Layout matches the system one
// on amd64: 4 bytes for the type, 4 for padding, then the mouse fields.
type mouseInput struct {
	Type      uint32
	_         uint32
	Dx, Dy    int32
	MouseData uint32
	DwFlags   uint32
	Time      uint32
	ExtraInfo uintptr
}

type accentPolicy struct {
	AccentState, AccentFlags, GradientColor, AnimationID uint32
}

type winCompAttrData struct {
	Attrib uint32
	PvData uintptr
	CbData uintptr
}

// ------------------------------------------------------------------- wrappers

func gradientV(hdc uintptr, r rect, top, bottom uint32) {
	if r.w() <= 0 || r.h() <= 0 {
		return
	}
	v := [2]trivertex{
		{X: r.Left, Y: r.Top,
			Red: uint16(top&0xFF) << 8, Green: uint16((top>>8)&0xFF) << 8, Blue: uint16((top>>16)&0xFF) << 8},
		{X: r.Right, Y: r.Bottom,
			Red: uint16(bottom&0xFF) << 8, Green: uint16((bottom>>8)&0xFF) << 8, Blue: uint16((bottom>>16)&0xFF) << 8},
	}
	g := gradientRect{0, 1}
	pGradientFill.Call(hdc, uintptr(unsafe.Pointer(&v[0])), 2,
		uintptr(unsafe.Pointer(&g)), 1, gradientFillRectV)
	runtime.KeepAlive(v)
	runtime.KeepAlive(g)
}

func createDIBSection(hdc uintptr, w, h int32) (bmp uintptr, bits unsafe.Pointer) {
	bi := bitmapInfoHeader{Width: w, Height: -h, Planes: 1, BitCount: 32} // negative = top-down
	bi.Size = uint32(unsafe.Sizeof(bi))
	r, _, _ := pCreateDIBSect.Call(hdc, uintptr(unsafe.Pointer(&bi)), dibRGBColors,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	runtime.KeepAlive(&bi)
	return r, bits
}

func updateLayered(hwnd uintptr, pos point, sz size, src uintptr) {
	from := point{0, 0}
	bf := blendFunction{acSrcOver, 0, 255, acSrcAlpha}
	pUpdateLayered.Call(hwnd, 0, uintptr(unsafe.Pointer(&pos)), uintptr(unsafe.Pointer(&sz)),
		src, uintptr(unsafe.Pointer(&from)), 0, uintptr(unsafe.Pointer(&bf)), ulwAlpha)
	runtime.KeepAlive(&pos)
	runtime.KeepAlive(&sz)
	runtime.KeepAlive(&bf)
}

// setCorners rounds the window's corners. The region is handed to the
// system; it must not be freed afterwards.
func setCorners(h uintptr, w, ht, radius int32) {
	if h == 0 || w <= 0 || ht <= 0 {
		return
	}
	rgn, _, _ := pCreateRoundRgn.Call(0, 0, uintptr(w+1), uintptr(ht+1),
		uintptr(2*radius), uintptr(2*radius))
	pSetWindowRgn.Call(h, rgn, 1)
}

func callWindowProc(prev, hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	r, _, _ := pCallWindowProc.Call(prev, hwnd, uintptr(m), wp, lp)
	return r
}

func killTimer(h uintptr, id uintptr) { pKillTimer.Call(h, id) }

// clickAtCursor — a single click wherever the cursor currently is. While the
// window is marked WS_EX_TRANSPARENT, the system hands the click to whatever
// lies beneath us.
func clickAtCursor() {
	in := [2]mouseInput{
		{Type: inputMouse, DwFlags: mouseEventLeftDown},
		{Type: inputMouse, DwFlags: mouseEventLeftUp},
	}
	pSendInput.Call(2, uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
	runtime.KeepAlive(in)
}

func loadIconRes(inst uintptr, id uintptr, cx, cy int32) uintptr {
	i, _, _ := pLoadImage.Call(inst, id, imageIcon, uintptr(cx), uintptr(cy), lrDefaultColor)
	return i
}

func drawTextRaw(hdc uintptr, u []uint16, r *rect, flags uint32) {
	if len(u) == 0 {
		return
	}
	pDrawText.Call(hdc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)),
		uintptr(unsafe.Pointer(r)), uintptr(flags))
	runtime.KeepAlive(u)
	runtime.KeepAlive(r)
}

func createPen(color uint32, width int32) uintptr {
	p, _, _ := pCreatePen.Call(psSolid, uintptr(width), uintptr(color))
	return p
}

func getStockObject(i int32) uintptr {
	o, _, _ := pGetStockObj.Call(uintptr(i))
	return o
}

func roundRect(hdc uintptr, r rect, radius int32) {
	pRoundRect.Call(hdc, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom),
		uintptr(2*radius), uintptr(2*radius))
}

// setBlurBehind turns on the system's blur of whatever lies behind the
// window. The function is undocumented: if it's missing (or the system
// refuses), the window just stays plainly translucent — nothing breaks.
func setBlurBehind(h uintptr, on bool) {
	if h == 0 || pSetWinCompAttr.Find() != nil {
		return
	}
	pol := accentPolicy{AccentState: accentDisabled}
	if on {
		pol.AccentState = accentEnableBlur
	}
	data := winCompAttrData{
		Attrib: wcaAccentPolicy,
		PvData: uintptr(unsafe.Pointer(&pol)),
		CbData: unsafe.Sizeof(pol),
	}
	pSetWinCompAttr.Call(h, uintptr(unsafe.Pointer(&data)))
	runtime.KeepAlive(&pol)
	runtime.KeepAlive(&data)
}

// ---------------------------------------------------------------- color

func chanOf(c uint32, i uint) int32 { return int32((c >> (8 * i)) & 0xFF) }

// mixColor — the color exactly between c1 and c2 at fraction num/den. Needed
// so the gloss band ends at the same color the main gradient has at that
// point: otherwise a visible step appears at the seam.
func mixColor(c1, c2 uint32, num, den int32) uint32 {
	if den <= 0 {
		return c1
	}
	var out uint32
	for i := uint(0); i < 3; i++ {
		v := chanOf(c1, i) + (chanOf(c2, i)-chanOf(c1, i))*num/den
		out |= uint32(clampi(v, 0, 255)) << (8 * i)
	}
	return out
}

// shade lightens (pct > 0) or darkens (pct < 0) a COLORREF by a percentage.
func shade(c uint32, pct int32) uint32 {
	var out uint32
	for i := uint(0); i < 3; i++ {
		v := chanOf(c, i)
		if pct >= 0 {
			v += (255 - v) * pct / 100
		} else {
			v = v * (100 + pct) / 100
		}
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		out |= uint32(v) << (8 * i)
	}
	return out
}

// ------------------------------------------------------------- letter glow

const (
	// How much the blurred blob is boosted, and the opacity ceiling it's
	// allowed to reach. The ceiling is low: the highlight stays dim, and the
	// sphere's top that it clips is covered anyway by the letter itself —
	// it's drawn on top, in a different window.
	glowGain = 26 // tenths, i.e. 2.6x
	glowPeak = 140
)

// The glow lives in a separate window between the backdrop and the main one.
// It's drawn via UpdateLayeredWindow, i.e. with real per-pixel transparency:
// the halo fades softly at the edges and doesn't depend on the backdrop's
// opacity slider. A color key can't do that — it only gives "pixel present /
// pixel absent".
type glowSurface struct {
	dc, bmp, oldBmp uintptr
	px              []uint32
	mask, tmp       []uint8
	halo            []uint8
	w, h            int32
}

func (g *glowSurface) free() {
	if g.dc != 0 {
		selectObject(g.dc, g.oldBmp)
		deleteDC(g.dc)
	}
	deleteObject(g.bmp)
	*g = glowSurface{}
}

// resize recreates the canvas for the new window size. The blur buffers live
// alongside the pixels: rebuilding them per letter would just be needless
// garbage for the collector.
func (g *glowSurface) resize(w, h int32) bool {
	if w <= 0 || h <= 0 {
		return false
	}
	if g.dc != 0 && g.w == w && g.h == h {
		return true
	}
	g.free()
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
	*g = glowSurface{
		dc: dc, bmp: bmp, oldBmp: selectObject(dc, bmp),
		px:   unsafe.Slice((*uint32)(bits), n),
		mask: make([]uint8, n), tmp: make([]uint8, n),
		halo: make([]uint8, n),
		w:    w, h: h,
	}
	return true
}

func clampi(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// boxBlur — two passes of a running sum (rows, then columns). Box blur is
// coarser than Gaussian, but two passes give a soft enough edge, and it
// costs O(pixels) regardless of radius.
func boxBlur(src, dst, tmp []uint8, w, h, r int32) {
	if r < 1 {
		copy(dst, src)
		return
	}
	win := 2*r + 1
	for y := int32(0); y < h; y++ {
		row := y * w
		var sum int32
		for x := -r; x <= r; x++ {
			sum += int32(src[row+clampi(x, 0, w-1)])
		}
		for x := int32(0); x < w; x++ {
			tmp[row+x] = uint8(sum / win)
			sum += int32(src[row+clampi(x+r+1, 0, w-1)]) - int32(src[row+clampi(x-r, 0, w-1)])
		}
	}
	for x := int32(0); x < w; x++ {
		var sum int32
		for y := -r; y <= r; y++ {
			sum += int32(tmp[clampi(y, 0, h-1)*w+x])
		}
		for y := int32(0); y < h; y++ {
			dst[y*w+x] = uint8(sum / win)
			sum += int32(tmp[clampi(y+r+1, 0, h-1)*w+x]) - int32(tmp[clampi(y-r, 0, h-1)*w+x])
		}
	}
}

// visibleText returns the text starting at the edit control's first visible
// line: below it, we draw exactly what EDIT shows, in the same font.
func (a *app) visibleText() []uint16 {
	n := int(sendMessage(a.hEdit, wmGetTextLength, 0, 0))
	if n == 0 {
		return nil
	}
	buf := make([]uint16, n+1)
	sendMessage(a.hEdit, wmGetText, uintptr(n+1), uintptr(unsafe.Pointer(&buf[0])))
	runtime.KeepAlive(buf)
	first := int(sendMessage(a.hEdit, emGetFirstVisibleLine, 0, 0))
	idx := int(int32(sendMessage(a.hEdit, emLineIndex, uintptr(first), 0)))
	if idx < 0 || idx > n {
		idx = 0
	}
	return buf[idx:n]
}

// renderGlow redraws the halo. The order is: draw the text white on a black
// canvas, turn the brightness into a mask, blur the mask at two radii (tight
// — a "bold outline", wide — the glow itself), then build premultiplied-alpha
// pixels from it, as UpdateLayeredWindow requires.
func (a *app) renderGlow() {
	if a.hGlow == 0 {
		return
	}
	if !a.cfg.Glow || a.hidden {
		if a.glowShown {
			showWindow(a.hGlow, swHide)
			a.glowShown = false
		}
		return
	}
	wr := getWindowRect(a.hwnd)
	if !a.glow.resize(wr.w(), wr.h()) {
		return
	}
	g := &a.glow
	for i := range g.px {
		g.px[i] = 0
	}

	top := a.barH
	if a.cfg.Compact {
		top = 0
	}
	tr := rect{a.margin, top, g.w - a.margin, g.h - a.margin}
	oldFont := selectObject(g.dc, a.glowFont)
	setTextColor(g.dc, 0xFFFFFF)
	setBkMode(g.dc, transparentBkMode)
	drawTextRaw(g.dc, a.visibleText(), &tr,
		dtNoPrefix|dtWordBreak|dtExpandTabs|dtEditControl|dtNoClip)
	selectObject(g.dc, oldFont)

	for i, p := range g.px {
		g.mask[i] = uint8(p & 0xFF) // text is white — any channel works
	}

	// Blur radius is half the font size. At that radius the glyph keeps no
	// shape: each letter smears into a round blob, and under the line lies a
	// chain of soft spheres rather than outlined letters. Previously the
	// glow also blended in the raw mask and a tight blur — that produced a
	// second, smeared copy of the letter on top of the real one.
	fontPx := int32(a.cfg.FontSize) * a.dpi / 72
	r := clampi(fontPx/2, 3, 48)
	// Blur twice: a single box-blur pass isn't enough — it cuts off in a
	// step and square blobs appear around words. The second pass turns the
	// step into a smooth falloff. Blurring in place is fine: the first pass
	// moves the whole image into a temporary buffer, so the source is no
	// longer read afterward.
	boxBlur(g.mask, g.halo, g.tmp, g.w, g.h, r)
	boxBlur(g.halo, g.halo, g.tmp, g.w, g.h, r)

	pal := palettes[a.cfg.Palette%len(palettes)]
	gr, gg, gb := chanOf(pal.glow, 0), chanOf(pal.glow, 1), chanOf(pal.glow, 2)
	for i := range g.px {
		// A blob smeared over this radius is faint on its own, so we boost
		// it — but with a ceiling: the sphere must stay a dim highlight, not
		// glow brighter than the letter itself.
		al := int32(g.halo[i]) * glowGain / 10
		if al > glowPeak {
			al = glowPeak
		}
		if al == 0 {
			g.px[i] = 0
			continue
		}
		// COLORREF is 0x00BBGGRR, a DIB pixel is 0x00RRGGBB: red and blue
		// swap places.
		g.px[i] = uint32(al)<<24 | uint32(gr*al/255)<<16 | uint32(gg*al/255)<<8 | uint32(gb*al/255)
	}

	if !a.glowShown {
		showWindow(a.hGlow, swShowNA)
		a.glowShown = true
		a.syncBackdrop() // put the window back in its place in the z-order
	}
	updateLayered(a.hGlow, point{wr.Left, wr.Top}, size{g.w, g.h}, g.dc)
}

// ------------------------------------------------------------ backdrop glass

// paintGlass draws the "liquid glass": the body as a gradient, a glossy top,
// a bottom highlight, and a light rim along the edge. All of this lives on
// the backdrop window, so it fully obeys the opacity slider, unlike the
// letters.
func (a *app) paintGlass(hdc uintptr, c rect) {
	base := palettes[a.cfg.Palette%len(palettes)].marker
	r := rect{0, 0, c.w(), c.h()}
	top, bottom := shade(base, 18), shade(base, -20)
	gradientV(hdc, r, top, bottom)

	// top third — gloss, like a reflection of the sky on the lid
	cut := r.Bottom * 38 / 100
	gradientV(hdc, rect{0, 0, r.Right, cut}, shade(base, 42), mixColor(top, bottom, cut, r.Bottom))

	// a thin highlight right at the bottom: without it the glass looks flat
	lift := r.Bottom - a.scale(20)
	gradientV(hdc, rect{0, lift, r.Right, r.Bottom},
		mixColor(top, bottom, lift, r.Bottom), shade(base, 10))

	rad := a.scale(12)
	pen := createPen(shade(base, 55), a.scale(1))
	oldPen := selectObject(hdc, pen)
	oldBrush := selectObject(hdc, getStockObject(nullBrush))
	roundRect(hdc, rect{0, 0, r.Right, r.Bottom}, rad)
	selectObject(hdc, oldBrush)
	selectObject(hdc, oldPen)
	deleteObject(pen)
}
