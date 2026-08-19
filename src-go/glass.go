//go:build windows

// Стекло и сияние: всё, что рисует «жидкое стекло» подложки и неоновый ореол
// вокруг букв, плюс обёртки Win32, нужные только этой части.
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

	// Размытие за окном — недокументированная, но живущая со времён Win10
	// функция. Если её нет, стекло просто останется без размытия.
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

// ------------------------------------------------------------------ структуры

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

// mouseInput — INPUT с MOUSEINPUT внутри. Раскладка совпадает с системной на
// amd64: 4 байта типа, 4 выравнивания, дальше поля мыши.
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

// ------------------------------------------------------------------- обёртки

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
	bi := bitmapInfoHeader{Width: w, Height: -h, Planes: 1, BitCount: 32} // минус = сверху вниз
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

// setCorners скругляет окно. Регион отдаётся системе, освобождать его нельзя.
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

// clickAtCursor — одиночный щелчок там, где сейчас курсор. Пока окно помечено
// WS_EX_TRANSPARENT, система отдаёт его тому, кто лежит под нами.
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

// setBlurBehind включает системное размытие того, что лежит за окном. Функция
// недокументированная: если её нет (или система отказала), окно останется
// просто полупрозрачным — ничего не ломается.
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

// ---------------------------------------------------------------- цвет

func chanOf(c uint32, i uint) int32 { return int32((c >> (8 * i)) & 0xFF) }

// mixColor — цвет ровно между c1 и c2 в доле num/den. Нужен, чтобы полоса
// глянца заканчивалась тем же цветом, каким в этом месте идёт основной
// градиент: иначе на их стыке видна ступенька.
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

// shade осветляет (pct > 0) или затемняет (pct < 0) COLORREF на проценты.
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

// ------------------------------------------------------------- сияние букв

// Сияние живёт на отдельном окне между подложкой и основным. Оно рисуется
// через UpdateLayeredWindow, то есть с настоящей попиксельной прозрачностью:
// ореол мягко гаснет к краям и не зависит от ползунка подложки. Ключевой цвет
// так не умеет — им можно сделать только «есть пиксель / нет пикселя».
type glowSurface struct {
	dc, bmp, oldBmp uintptr
	px              []uint32
	mask, tmp       []uint8
	halo, tight     []uint8
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

// resize пересоздаёт холст под новый размер окна. Буферы размытия живут рядом
// с пикселями: пересобирать их на каждую букву — лишний мусор для сборщика.
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
		halo: make([]uint8, n), tight: make([]uint8, n),
		w: w, h: h,
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

// boxBlur — два прохода бегущей суммой (по строкам, потом по столбцам).
// Коробочное размытие грубее гауссова, но за два прохода даёт достаточно
// мягкий край, а стоит O(пиксели) независимо от радиуса.
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

// visibleText отдаёт текст, начиная с первой видимой строки поля ввода: ниже
// него рисуется ровно то же и тем же шрифтом, что показывает EDIT.
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

// renderGlow перерисовывает ореол. Порядок такой: рисуем текст белым по
// чёрному холсту, из яркости получаем маску, маску размываем двумя радиусами
// (тугой — «жирный контур», широкий — само сияние) и уже из неё собираем
// пиксели с предумноженной альфой, как того требует UpdateLayeredWindow.
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
		g.mask[i] = uint8(p & 0xFF) // текст белый — любой канал годится
	}

	fontPx := int32(a.cfg.FontSize) * a.dpi / 72
	boxBlur(g.mask, g.tight, g.tmp, g.w, g.h, clampi(fontPx/14, 1, 4))
	// Широкий ореол размываем дважды: одного коробочного прохода мало —
	// он обрывается ступенькой и вокруг слов встают квадратные пятна.
	// Второй проход превращает ступеньку в плавный спад. Размывать на месте
	// можно: первый проход целиком перекладывает картинку во временный
	// буфер, и дальше исходный уже не читается.
	r := clampi(fontPx/6, 2, 10)
	boxBlur(g.mask, g.halo, g.tmp, g.w, g.h, r)
	boxBlur(g.halo, g.halo, g.tmp, g.w, g.h, r)

	pal := palettes[a.cfg.Palette%len(palettes)]
	gr, gg, gb := chanOf(pal.glow, 0), chanOf(pal.glow, 1), chanOf(pal.glow, 2)
	for i := range g.px {
		al := int32(g.mask[i]) + int32(g.tight[i])*22/10 + int32(g.halo[i])*26/10
		if al > 255 {
			al = 255
		}
		if al == 0 {
			g.px[i] = 0
			continue
		}
		g.px[i] = uint32(al)<<24 | uint32(gb*al/255)<<16 | uint32(gg*al/255)<<8 | uint32(gr*al/255)
	}

	if !a.glowShown {
		showWindow(a.hGlow, swShowNA)
		a.glowShown = true
		a.syncBackdrop() // вернуть окно на своё место в z-порядке
	}
	updateLayered(a.hGlow, point{wr.Left, wr.Top}, size{g.w, g.h}, g.dc)
}

// ------------------------------------------------------------ стекло подложки

// paintGlass рисует «жидкое стекло»: корпус градиентом, глянцевый верх,
// отблеск снизу и светлый ободок по краю. Всё это лежит на окне-подложке,
// поэтому целиком подчиняется ползунку прозрачности, а буквы — нет.
func (a *app) paintGlass(hdc uintptr, c rect) {
	base := palettes[a.cfg.Palette%len(palettes)].marker
	r := rect{0, 0, c.w(), c.h()}
	top, bottom := shade(base, 18), shade(base, -20)
	gradientV(hdc, r, top, bottom)

	// верхняя треть — глянец, как отражение неба на крышке
	cut := r.Bottom * 38 / 100
	gradientV(hdc, rect{0, 0, r.Right, cut}, shade(base, 42), mixColor(top, bottom, cut, r.Bottom))

	// узкая подсветка у самого низа: без неё стекло выглядит плоским
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
