//go:build windows

// Своё выпадающее меню. Системное TrackPopupMenu рисует система: это чужое
// окно, у которого не отнять ни белого фона, ни рамки, ни непрозрачности —
// поверх стеклянной заметки оно выглядело чужеродной заплатой. Поэтому меню
// здесь своё: те же слои, то же стекло и то же размытие, что у самой заметки.
//
// Устройство. Каждое меню — окно с попиксельной прозрачностью
// (UpdateLayeredWindow): стекло полупрозрачное, буквы поверх него — плотные.
// Так же сделано сияние, только там маска размывается, а тут ею красятся
// подписи. Мышь на всё время показа захвачена корневым окном, поэтому все
// щелчки и движения приходят в одну процедуру, и подменю не нужно ловить
// события самому — оно только рисуется.
package main

import (
	"runtime"
	"unsafe"
)

const menuClass = "GloMenuWnd"

// Меню плотнее заметки: сквозь него надо читать подписи, а не обои.
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

// workArea — рабочая область экрана, на котором лежит точка (без панели
// задач). По одному GetSystemMetrics обошлись бы, но на втором мониторе
// меню тогда уезжало бы на первый.
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

// ------------------------------------------------------------------ пункты

// mItem — один пункт. Пустой label с sep=true даёт разделитель, непустой
// sub — подменю (тогда cmd не нужен).
type mItem struct {
	cmd   int32
	label string
	accel string // подпись горячей клавиши справа
	check bool
	sep   bool
	sub   []mItem
}

// ------------------------------------------------------------------ холст

// menuSurface — тот же приём, что у сияния: DIB, в который рисует GDI, и
// параллельный ему буфер out с настоящей альфой. GDI про альфу не знает и
// затирает её мусором, поэтому итоговые пиксели собираются вручную.
type menuSurface struct {
	dc, bmp, oldBmp uintptr
	px              []uint32 // то, во что рисует GDI
	out             []uint32 // то, что уйдёт в UpdateLayeredWindow
	cov             []uint8  // покрытие скруглённого угла, 0..255
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

// roundCoverage — доля пикселя внутри скруглённого прямоугольника. Регионом
// (SetWindowRgn) углы вышли бы ступеньками: регион знает только «внутри» и
// «снаружи». Здесь у краевых пикселей своя прозрачность, и срез получается
// гладким. Считается один раз на размер, дальше только читается.
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
	const ss = 4 // 4x4 подвыборки на пиксель — глазу этого уже хватает
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

// dibOf переставляет байты COLORREF (0x00BBGGRR) в порядок пикселя DIB
// (0x00RRGGBB). Без этого красное на холсте выходит синим.
func dibOf(c uint32) uint32 {
	return uint32(chanOf(c, 0))<<16 | uint32(chanOf(c, 1))<<8 | uint32(chanOf(c, 2))
}

// premul — пиксель GDI с заданной альфой, уже помноженный на неё:
// UpdateLayeredWindow принимает только предумноженные цвета.
func premul(p uint32, al int32) uint32 {
	r := int32((p>>16)&0xFF) * al / 255
	g := int32((p>>8)&0xFF) * al / 255
	b := int32(p&0xFF) * al / 255
	return uint32(al)<<24 | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
}

// over кладёт непрозрачный цвет src поверх dst с покрытием cov (0..255).
// Обе стороны предумножены, поэтому это обычное «источник поверх».
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

// ------------------------------------------------------------------ окно

type menuWin struct {
	h     uintptr
	items []mItem
	rows  []rect // строки в координатах холста
	hot   int    // подсвеченная строка, -1 — никакой
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

// at — строка под точкой (экранные координаты) или -1.
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
		maxA += a.scale(30) // зазор между подписью и горячей клавишей
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

// ------------------------------------------------------------------ рисование

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

	// Текст кладётся не поверх GDI-картинки, а отдельными проходами: каждый
	// рисуется белым по чёрному, и яркость становится покрытием. Иначе
	// сглаженные края букв смешались бы с фоном ещё до того, как у пикселя
	// появится альфа, и по контуру глифов пошла бы грязь.
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
	gradientV(s.dc, full, barTop, barBG) // сверху светлее — как панель заметки

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

// drawMenuText рисует один цветовой слой и говорит, было ли что рисовать:
// 0 — обычные подписи, 1 — горячие клавиши, 2 — всё яркое (подсвеченная
// строка, галочки, стрелки подменю).
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

// ------------------------------------------------------------------ показ

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

// showMenuWin ставит окно так, чтобы оно целиком помещалось на экране, и
// показывает его, не забирая фокус: заметка под меню остаётся активной.
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
	a.renderMenu(m) // UpdateLayeredWindow заодно двигает окно на место
	showWindow(m.h, swShowNA)
}

// ------------------------------------------------------------------ сеанс

// Сеанс показа. Меню модально: пока оно на экране, крутится свой цикл
// сообщений, а мышь захвачена корневым окном.
type menuSess struct {
	root   *menuWin
	child  *menuWin
	openAt int // строка root, чьё подменю открыто, -1 — нет
	result int32
	done   bool

	origin point // где была мышь в момент открытия
	moved  bool  // курсор с тех пор уходил с этого места
}

var menuSes *menuSess

// inside — попала ли точка хоть в одно из окон меню.
func (s *menuSess) inside(p point) bool {
	if s.root != nil && s.root.rect().has(p.X, p.Y) {
		return true
	}
	return s.child != nil && s.child.rect().has(p.X, p.Y)
}

// trackMenu показывает меню и возвращает выбранную команду (0 — отказ).
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
			postQuitMessage(0) // WM_QUIT надо вернуть главному циклу
			break
		}
		if menuKey(&m) {
			continue // клавиши, пока меню открыто, полю ввода не достаются
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
		x = menuSes.root.x - c.w + a.scale(4) // справа не влезло — раскроем влево
	}
	menuSes.child = c
	menuSes.openAt = idx
	a.showMenuWin(c, x, menuSes.root.y+row.Top-a.scale(6))
}

// menuHover ведёт подсветку и подменю по положению курсора.
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
		// Курсор мимо всего: подсветку в подменю гасим, само подменю
		// оставляем — иначе оно захлопывалось бы на пути к нему.
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

// menuProc обслуживает оба окна меню, но мышь приходит только в корневое:
// оно держит захват, поэтому щелчки мимо меню тоже попадают сюда.
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
			menuSes.done = true // щелчок мимо — просто закрыть
		}
		return 0

	case wmLButtonUp:
		// Отпускание кнопки, которой меню и открыли, приходит уже сюда —
		// захват мыши к этому моменту у нас. Пока курсор не сдвинулся с
		// места открытия, выбором это не считается: иначе меню, вылезшее
		// из-под курсора (внизу экрана оно раскрывается вверх), тут же
		// сработало бы первым попавшимся пунктом.
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
		menuSes.done = true // захват увели — держать меню больше не на чем
		return 0
	}
	return defWindowProc(hwnd, uint32(m), wp, lp)
}

// menuKey разбирает клавиатуру прямо в цикле: окно меню фокуса не берёт,
// поэтому нажатия адресованы полю ввода и до процедуры меню не дойдут.
// Всё, что нажато при открытом меню, съедается — иначе стрелки уехали бы
// каретке, а буквы упали бы в текст заметки.
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

// menuStep двигает подсветку на следующий выбираемый пункт по кругу.
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
