//go:build windows

// Glasspad — прозрачный блокнот поверх всех окон, прозрачность 0-100%.
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

// Пиксели этого цвета становятся полностью прозрачными (LWA_COLORKEY).
var colorKey = rgb(255, 0, 254)

const (
	barBG     = 0x241E1C // COLORREF = 0x00BBGGRR
	barTop    = 0x3E3632 // верх панели светлее низа: панель — тоже стекло
	barFG     = 0xC6BEB8
	barHot    = 0xFFFFFF
	trackBG   = 0x3A302C
	knobBG    = 0xF0F0F0
	dimFG     = 0x8C7C70
	appTitle  = "Glo"
	className = "GloWnd"
	backClass = "GloBackdropWnd"
	glowClass = "GloGlowWnd"

	// Папка данных осталась от прежнего имени: там лежит заметка
	// пользователя, и переезд ради красивого имени потерял бы её.
	dataFolder = "Glasspad"

	cfgVersion = 2 // выросла, когда появились сияние и стекло
)

// Плашка под буквами: включена или нет. Прозрачность к буквам отношения не
// имеет — она живёт на отдельном окне-подложке.
const (
	modePlain  = 0
	modeMarker = 1
)

var modeNames = []string{"Без плашки", "Маркер"}

type palette struct {
	name   string
	fg     uint32 // сами буквы
	marker uint32 // плашка под буквами, она же цвет стекла
	glow   uint32 // ореол вокруг букв в режиме сияния
}

// Ореол везде белый: цветной он сливается с буквами того же цвета и буквы
// тонут в собственном свечении. Белое сияние вокруг цветного глифа держит
// его форму читаемой на любом фоне. Исключение одно — белые буквы: там
// белый ореол слился бы с ними самими, поэтому ему оставлен холодный
// голубой отлив.
var palettes = []palette{
	{"Белый", rgb(255, 255, 255), rgb(0, 0, 0), rgb(150, 205, 255)},
	{"Чёрный", rgb(0, 0, 0), rgb(255, 255, 255), rgb(255, 255, 255)},
	{"Красный", rgb(255, 95, 95), rgb(24, 0, 0), rgb(255, 255, 255)},
	{"Зелёный", rgb(105, 255, 150), rgb(0, 22, 8), rgb(255, 255, 255)},
	{"Синий", rgb(120, 175, 255), rgb(0, 6, 28), rgb(255, 255, 255)},
	{"Циан", rgb(95, 250, 255), rgb(0, 20, 22), rgb(255, 255, 255)},
	{"Розовый", rgb(255, 120, 225), rgb(22, 0, 18), rgb(255, 255, 255)},
	{"Жёлтый", rgb(255, 225, 95), rgb(22, 15, 0), rgb(255, 255, 255)},
}

const (
	fontMin = 8
	fontMax = 72
)

var fontPresets = []int{12, 16, 20, 28, 36, 48, 72}

// команды (акселераторы, кнопки панели, глобальные хоткеи)
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

// Пункты подменю: команда = база + номер варианта.
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
	// Уводить ли окно под низ после прокола. Выключено: заметка-оверлей
	// должна оставаться на виду, а прокол и так отдаёт клик вниз.
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
	// Прозрачность по умолчанию невысокая: на 80% подложка выглядит просто
	// чёрным прямоугольником, и непонятно, что окно вообще прозрачное.
	// Режим по умолчанию — без плашки: она непрозрачна и закрыла бы ореол.
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
	punchArmed                  bool // двойной щелчок был, ждём отпускания кнопки
	dropped                     bool // окно уведено под низ после прокола
	punching                    bool // идёт разовый прокол по двойному щелчку
	hidden                      bool

	glow      glowSurface
	glowShown bool
	glowDirty bool
	glowLine  int // первая видимая строка на прошлой отрисовке ореола
	cornersW  int32
	cornersH  int32

	beats                      int
	tray                       notifyIconData
	savedText                  string
	notePath, cfgPath, dataDir string
}

var a app

// Очередь сообщений в Windows принадлежит потоку, который создал окно.
// Главная горутина Go по умолчанию не привязана к потоку ОС и после любого
// блокирующего вызова может переехать на другой — тогда GetMessage крутится
// не там, где живёт окно, и оно намертво «не отвечает». Прибиваем гвоздями.
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
	// Ореол перерисовывается по таймеру, а не сразу на каждую букву: при
	// быстром наборе это склеивает десяток перерисовок в одну. Тот же таймер
	// ловит прокрутку — EDIT о ней не сообщает, когда её делают с клавиатуры.
	setTimer(a.hwnd, timerGlow, 60)
	logf("вход в цикл сообщений")

	accels := createAcceleratorTable(accelTable())
	var m msg
	for getMessage(&m) > 0 {
		if !translateAccelerator(a.hwnd, accels, &m) {
			translateMessage(&m)
			dispatchMessage(&m)
		}
	}
	logf("=== выход, всё чисто")
	closeLog()
}

func (a *app) scale(v int32) int32 { return v * a.dpi / 96 }

func (a *app) initPaths() {
	// %AppData%\Glasspad — то же место, что использует и питоновская версия.
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
	logf("папка данных: %s", a.dataDir)
}

// ------------------------------------------------------------------ создание

func (a *app) registerClass() {
	a.bgBrush = createSolidBrush(colorKey)
	wc := wndClassEx{
		// CS_DBLCLKS обязателен: без него окно вообще не получает
		// WM_LBUTTONDBLCLK, и прокола по двойному щелчку не будет.
		Style:         0x0002 | 0x0001 | csDblClks, // CS_HREDRAW | CS_VREDRAW
		LpfnWndProc:   syscall.NewCallback(wndProc),
		HInstance:     a.hInst,
		HCursor:       loadCursorArrow(),
		HbrBackground: 0, // фон рисуем сами
		LpszClassName: str16(className),
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	registerClass(&wc)

	back := wc
	back.LpfnWndProc = syscall.NewCallback(backProc)
	back.LpszClassName = str16(backClass)
	registerClass(&back)

	// Окно сияния ничего не обрабатывает: картинку в него кладёт
	// UpdateLayeredWindow, мышь оно не ловит по стилю.
	glow := wc
	glow.LpfnWndProc = syscall.NewCallback(defProc)
	glow.LpszClassName = str16(glowClass)
	registerClass(&glow)

	// Меню — такое же слоёное окно, как сияние, но с мышью: пока оно
	// открыто, захват мыши держит корневое окно меню.
	mn := wc
	mn.LpfnWndProc = syscall.NewCallback(menuProc)
	mn.LpszClassName = str16(menuClass)
	registerClass(&mn)
}

func defProc(hwnd, m, wp, lp uintptr) uintptr {
	return defWindowProc(hwnd, uint32(m), wp, lp)
}

func (a *app) createWindows() {
	// Подложка — отдельное окно позади основного. Вся прозрачность живёт на нём,
	// поэтому буквы в основном окне никогда не выцветают.
	a.hBack = createWindowEx(
		wsExLayered|wsExToolWindow|wsExNoActivate,
		str16(backClass), str16(appTitle),
		wsPopup,
		int32(a.cfg.X), int32(a.cfg.Y), int32(a.cfg.W), int32(a.cfg.H),
		0, 0, a.hInst)

	// Без WS_EX_TOOLWINDOW: окно должно быть в панели задач и в Alt+Tab.
	// Оверлей без единого привычного способа закрыться — это ловушка для
	// того, кто получил программу без инструкции.
	a.hwnd = createWindowEx(
		wsExLayered|wsExAppWindow,
		str16(className), str16(appTitle),
		wsPopup|wsThickFrame|wsClipChild,
		int32(a.cfg.X), int32(a.cfg.Y), int32(a.cfg.W), int32(a.cfg.H),
		0, 0, a.hInst)

	// Ключевой цвет — навсегда и без LWA_ALPHA: фон проваливается насквозь,
	// всё нарисованное поверх остаётся полностью непрозрачным.
	setLayered(a.hwnd, colorKey, 255, lwaColorKey)

	// Сияние — третье окно, между подложкой и основным. Прозрачность у него
	// попиксельная (UpdateLayeredWindow), поэтому ореол мягко гаснет к краям
	// и не выцветает вместе с подложкой. Мышь оно не ловит никогда.
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
	// Поля внутри EDIT обнуляем: ореол рисуется тем же DrawText в те же
	// координаты, и лишние пиксели слева увели бы его в сторону от букв.
	sendMessage(a.hEdit, emSetMargins, ecLeftMargin|ecRightMargin, 0)
	// Двойной щелчок по тексту — это прокол, а не выделение слова, поэтому
	// сообщение перехватываем до штатной обработки EDIT.
	editPrevProc = setWindowLong(a.hEdit, gwlWndProc, syscall.NewCallback(editProc))

	a.barBrush = createSolidBrush(barBG)
	a.trackBrush = createSolidBrush(trackBG)
	a.knobBrush = createSolidBrush(knobBG)
	a.knobPen = createPen(knobBG, 1)
	a.lineBrush = createSolidBrush(shade(barBG, 26))
	a.barFont = createFont(-a.scale(12), 400, 5, "Segoe UI")
	// Меню лежит на попиксельно-прозрачном холсте, и ClearType на нём
	// оставил бы цветную кайму: субпиксели рассчитаны на плотный фон.
	// Отсюда ANTIALIASED_QUALITY — ровно как у шрифта сияния.
	a.menuFont = createFont(-a.scale(13), 400, 4, "Segoe UI")
	a.layoutChildren()
}

func (a *app) layoutChildren() {
	if a.hwnd == 0 || a.hEdit == 0 {
		return // WM_SIZE прилетает ещё внутри CreateWindowEx
	}
	c := getClientRect(a.hwnd)
	top := a.barH
	if a.cfg.Compact {
		top = 0
	}
	moveWindow(a.hEdit, a.margin, top, c.w()-2*a.margin, c.h()-top-a.margin, true)
	a.glowDirty = true
}

// editProc перехватывает двойной щелчок по тексту. Всё остальное отдаём
// штатной процедуре EDIT: каретка, выделение и ввод должны работать как были.
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

// ------------------------------------------------------------- вид и режимы

// syncBackdrop держит подложку строго под основным окном — и по координатам,
// и по z-порядку. Вставка сразу за основным окном заодно подтягивает ей
// признак «поверх всех», если он включён.
func (a *app) syncBackdrop() {
	if a.hwnd == 0 || a.hBack == 0 {
		return
	}
	r := getWindowRect(a.hwnd)
	// Порядок важен: сияние сразу за основным окном, подложка — за сиянием.
	if a.hGlow != 0 && a.glowShown {
		setWindowPos(a.hGlow, a.hwnd, r.Left, r.Top, r.w(), r.h(), swpNoActivate)
		setWindowPos(a.hBack, a.hGlow, r.Left, r.Top, r.w(), r.h(), swpNoActivate)
	} else {
		setWindowPos(a.hBack, a.hwnd, r.Left, r.Top, r.w(), r.h(), swpNoActivate)
	}
	a.applyCorners(r.w(), r.h())
}

// applyCorners скругляет оба видимых окна. SetWindowRgn перерисовывает окно
// целиком, поэтому дёргаем его только когда размер действительно изменился —
// иначе перетаскивание окна превратилось бы в поток перерисовок.
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
	a.backBrush = createSolidBrush(pal.marker) // подложка в цвет плашки маркера
	invalidate(a.hBack, nil)
	invalidate(a.hwnd, nil)
	invalidate(a.hEdit, nil)
	deleteObject(old)
	a.glowDirty = true
}

// applyBlur включает системное размытие за стеклом. Без него подложка просто
// затемняет то, что под ней; с ним получается настоящая матовая стекляшка.
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

// У слоёного окна пиксели ключевого цвета не ловят мышь — клик по пустому
// месту заметки провалился бы в приложение под ней. Эти клики принимает
// подложка и передаёт фокус в текст. Раньше она пропускала их насквозь при
// нулевой прозрачности, и заметка «проваливалась» сама собой; теперь наружу
// пускают только два явных действия: двойной щелчок и режим сквозного клика.
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

// wantFontQuality: когда буквы висят прямо над рабочим столом (нет ни плашки,
// ни заметной подложки), сглаживание смешивается с ключевым цветом и даёт
// розовую кайму по краям глифов — тогда его выключаем.
// Сияние здесь ничего не меняет: ореол живёт на отдельном окне, а сглаженные
// края глифов смешиваются с фоном своего окна — то есть с ключевым цветом.
func (a *app) wantFontQuality() uint32 {
	if a.cfg.Mode == modeMarker || a.cfg.Alpha >= 25 {
		return 5 // CLEARTYPE_QUALITY
	}
	return 3 // NONANTIALIASED_QUALITY
}

func (a *app) applyFont() {
	a.fontQ = a.wantFontQuality()
	h := -(int32(a.cfg.FontSize) * a.dpi / 72)
	// В сиянии буквы жирные: тонкий глиф тонет в собственном ореоле.
	weight := int32(400)
	if a.cfg.Glow {
		weight = 700
	}
	old, oldGlow := a.editFont, a.glowFont
	a.editFont = createFont(h, weight, a.fontQ, "Consolas")
	// Шрифт ореола обязан совпадать со шрифтом поля по всем метрикам, иначе
	// свечение уедет от букв. Отличается только сглаживание: маске нужны
	// серые края, а не цветные субпиксели ClearType.
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
	// Плашка непрозрачна и лежит выше сияния: вместе они не работают,
	// поэтому включение одного гасит другое.
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

// ------------------------------------------------------------- значок в трее

func (a *app) addTrayIcon() {
	if a.tray.CbSize != 0 {
		return // уже висит, повторный NIM_ADD не пройдёт
	}
	nid := notifyIconData{
		HWnd:             a.hwnd,
		UID:              1,
		UFlags:           nifMessage | nifIcon | nifTip,
		UCallbackMessage: wmTrayIcon,
		HIcon:            smallIcon(a.hInst),
	}
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	tip := utf16.Encode([]rune("Glo — правый клик: меню, двойной: показать/скрыть"))
	copy(nid.SzTip[:len(nid.SzTip)-1], tip)
	a.tray = nid
	logf("tray add: %v", shellNotifyIcon(nimAdd, &a.tray))
}

// Значок у часов и кнопка в панели задач — два способа добраться до окна.
// Выключить оба сразу нельзя: программа стала бы неубиваемой без диспетчера
// задач, ровно та ловушка, из-за которой всё и переделывалось.
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
	// Windows смотрит на эти стили только в момент показа окна, поэтому
	// его надо спрятать и показать заново.
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

// setAppIcon ставит окну свой значок: он виден в панели задач и по Alt+Tab.
// В самом exe тот же значок лежит ресурсом (см. icon/make_icon.py), поэтому
// проводник рисует файл им же.
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

// ------------------------------------------------------------ меню настроек

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
		{label: "Прозрачность подложки", sub: alpha},
		{label: "Размер шрифта", sub: sizes},
		{label: "Цвет букв", sub: colors},
		{sep: true},
		{cmd: cmdGlow, label: "Сияние букв", accel: "Ctrl+G", check: a.cfg.Glow},
		{cmd: cmdMode, label: "Плашка маркера", accel: "Ctrl+M", check: a.cfg.Mode == modeMarker},
		{cmd: cmdBlur, label: "Матовое стекло", check: a.cfg.Blur},
		{cmd: cmdTopmost, label: "Поверх всех окон", accel: "Ctrl+T", check: a.cfg.Topmost},
		{cmd: cmdDropBelow, label: "Двойной щелчок уводит окно вниз", check: a.cfg.DropBelow},
		{cmd: cmdClickThrough, label: "Сквозной клик насовсем", accel: "Ctrl+Alt+E", check: a.clickThrough},
		{cmd: cmdCompact, label: "Скрыть эту панель", accel: "Ctrl+H", check: a.cfg.Compact},
		{sep: true},
		{cmd: cmdTray, label: "Значок у часов", check: a.cfg.Tray},
		{cmd: cmdTaskbar, label: "Кнопка в панели задач", check: a.cfg.Taskbar},
		{sep: true},
		{cmd: cmdOpen, label: "Открыть файл…", accel: "Ctrl+O"},
		{cmd: cmdSave, label: "Сохранить как…", accel: "Ctrl+S"},
		{cmd: cmdOpenDir, label: "Папка с заметкой"},
		{sep: true},
		{cmd: cmdQuit, label: "Выход", accel: "Ctrl+Q"},
	}

	if cmd := a.trackMenu(items, x, y); cmd != 0 {
		a.command(cmd)
	}
	setFocus(a.hEdit)
}

func (a *app) trayMenu() {
	show := "Свернуть"
	if a.hidden {
		show = "Развернуть"
	}
	items := []mItem{{cmd: cmdShowHide, label: show}}
	if a.clickThrough {
		// Окно сейчас не ловит мышь, панель нажать нельзя — без этого пункта
		// выключить режим можно было бы только горячей клавишей.
		items = append(items, mItem{cmd: cmdClickThrough, label: "Выключить сквозной клик"})
	}
	items = append(items, mItem{cmd: cmdQuit, label: "Закрыть"})

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

// setMouseTransparent снимает или возвращает окну способность ловить мышь.
// WS_EX_TRANSPARENT у слоёного окна действует сразу, без SetWindowPos.
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

// Прокол взводится двойным щелчком, а срабатывает на отпускании кнопки.
// Раньше это делалось прямо по WM_LBUTTONDBLCLK, но в тот момент кнопка
// физически ещё нажата, и посланное системе нажатие пришлось бы на уже
// нажатую кнопку — приложение снизу получило бы вместо клика непонятно что.
func (a *app) punchArm() {
	if a.clickThrough || a.punching {
		return
	}
	a.punchArmed = true
	// Если отпускание уйдёт мимо окна (увели курсор и отпустили снаружи),
	// взвод не должен висеть до следующего случайного клика.
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

// punchThrough — «разовый прокол»: окно на четверть секунды перестаёт ловить
// мышь и само шлёт системе одиночный клик — он достаётся тому окну, что лежит
// под заметкой. Постоянный сквозной режим для этого не годится: из него потом
// надо как-то выбираться, а здесь всё возвращается само.
func (a *app) punchThrough() {
	if a.clickThrough || a.punching {
		return // и так всё летит насквозь
	}
	a.punching = true
	a.setMouseTransparent(true)
	clickAtCursor()
	if a.cfg.DropBelow {
		a.dropBelow()
	}
	setTimer(a.hwnd, timerPunch, 250)
}

// dropBelow уводит заметку под то окно, по которому только что щёлкнули.
// Признак «поверх всех» в настройках не трогаем: он вернётся, как только
// пользователь снова возьмётся за заметку.
func (a *app) dropBelow() {
	a.dropped = true
	setWindowPos(a.hwnd, hwndNoTopmost, 0, 0, 0, 0, swpNoMv|swpNoSz|swpNoActivate)
	a.syncBackdrop()
}

// raiseBack возвращает заметку наверх. Вызывается на любом обращении к ней:
// клик по стеклу, клик по панели, разворот из трея.
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
		a.cfg.Mode = modePlain // непрозрачная плашка закрыла бы ореол
	}
	a.applyFont() // в сиянии буквы жирнее
	a.renderGlow()
	invalidate(a.hwnd, nil)
	invalidate(a.hEdit, nil)
}

func (a *app) toggleBlur() {
	a.cfg.Blur = !a.cfg.Blur
	a.applyBlur()
}

// glowTick вызывается таймером: перерисовывает ореол, если текст изменился
// или заметку прокрутили.
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

// ---------------------------------------------------------------- отрисовка

func (a *app) buildBar(hdc uintptr, width int32) {
	pal := palettes[a.cfg.Palette%len(palettes)]
	a.items = a.items[:0]

	pad := a.scale(7)
	x := a.scale(10)

	// ползунок прозрачности
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

	// Сияние крутят часто, поэтому оно на панели: надпись горит цветом
	// ореола, выключено — тусклая. Значок вместо слова не ставим: в шрифте
	// панели подходящего глифа может не оказаться, и выйдет пустой квадрат.
	glowFG := uint32(dimFG)
	if a.cfg.Glow {
		glowFG = pal.glow
	}
	add(cmdGlow, "Сияние", glowFG, barBG)
	add(cmdMode, modeNames[a.cfg.Mode], barFG, barBG)

	// «Поверх» и «Сквозной» переехали в настройки: с ними панель не влезала
	// в окно по умолчанию. Здесь остаётся только то, что крутят постоянно.
	settingsFG := uint32(barFG)
	if a.clickThrough {
		settingsFG = 0x6B6BFF // сквозной клик включён — заметное состояние
	}
	add(cmdSettings, "Настройки", settingsFG, barBG)

	// «✕» прижимаем вправо
	w := textWidth(hdc, "✕") + 2*pad
	a.items = append(a.items, barItem{cmdQuit, "✕", rect{width - w, 0, width, a.barH}, barFG, barBG})
}

// paint принимает hwnd параметром, а не берёт a.hwnd: сообщение может прийти
// ещё изнутри CreateWindowEx, когда поле структуры не заполнено.
func (a *app) paint(hwnd uintptr) {
	var ps paintStruct
	hdc := beginPaint(hwnd, &ps)
	c := getClientRect(hwnd)

	// поля вокруг поля ввода — фоном окна
	full := rect{0, 0, c.w(), c.h()}
	fillRect(hdc, &full, a.bgBrush)

	if !a.cfg.Compact {
		mem := createCompatibleDC(hdc)
		bmp := createCompatibleBitmap(hdc, c.w(), a.barH)
		oldBmp := selectObject(mem, bmp)
		oldFont := selectObject(mem, a.barFont)

		bar := rect{0, 0, c.w(), a.barH}
		fillRect(mem, &bar, a.barBrush)
		gradientV(mem, bar, barTop, barBG) // панель — тоже стекло, сверху светлее
		hair := rect{0, a.barH - a.scale(1), c.w(), a.barH}
		fillRect(mem, &hair, a.lineBrush) // светлая нить по нижнему краю
		a.buildBar(mem, c.w())

		// ползунок
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

// brushFor — кисть под цвет образца «Aa»; кэш на один цвет, больше и не нужно.
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

// ------------------------------------------------------------------- мышь

func (a *app) hitTest(hwnd uintptr, x, y int32) int32 {
	c := getClientRect(hwnd)
	if c.w() == 0 || c.h() == 0 {
		return htClient
	}
	b := a.scale(6)
	// Сверху полоска для растягивания тоньше — иначе она съедает верх кнопок.
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
		return htCaption // пустое место панели — таскаем окно
	}
	return htClient
}

func (a *app) onLButtonDown(x, y int32) {
	a.raiseBack() // взялись за окно — значит, оно снова нужно наверху
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
				// Меню разворачивается от нижнего края кнопки. Клиентская
				// область равна всему окну, так что смещение — это его угол.
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
			a.setPalette(a.cfg.Palette + 1) // ПКМ по «Aa» — цвета по кругу
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

// --------------------------------------------------------------- команды

func (a *app) command(cmd int32) {
	// Пункты подменю приходят диапазонами.
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
	// Глобальные — чтобы вернуть окно, когда прозрачность 0% или включён
	// сквозной клик и мышью до окна не дотянуться.
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

// ----------------------------------------------------------------- текст

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

// Текст первого запуска: программа приходит одним файлом, без сопроводиловки,
// поэтому пусть объясняет себя сама. Стирается как обычный текст.
const welcomeNote = `Glo — заметка поверх всех окон.

Как закрыть: крестик справа в панельке, Alt+F4,
или правый клик по значку у часов -> Выход.

Двойной щелчок по заметке = клик по тому, что под ней.
Обычный клик остаётся заметке, так что окно больше
не мешает работать с приложением снизу.

Панель сверху:
  ползунок   прозрачность стекла. Буквы не выцветают никогда
  - 16 +     размер шрифта, 8-72. Клик по числу — по размерам
  Aa         белые/чёрные буквы. Правой кнопкой — цвета
  Сияние     неоновый ореол и жирный контур, Ctrl+G
  Маркер     плашка под буквами, чтобы читалось на любом фоне
  Настройки  всё остальное: поверх окон, матовое стекло, трей,
             кнопка в панели задач, цвета, файлы, выход

Двигать — за пустое место панели. Размер — за края и углы.
Ctrl+H прячет панель, Ctrl+Alt+H — всё окно целиком.
Текст сохраняется сам, каждые 5 секунд.

Этот текст можно стереть.`

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

// saveNote читает текст в потоке сообщений, а пишет на диск в стороне: если
// антивирус или диск задумаются на секунду, окно не должно застывать вместе
// с ними — иначе Windows объявит его зависшим.
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
		messageBox(a.hwnd, "Не удалось открыть файл:\n"+path, appTitle, 0x10)
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
		messageBox(a.hwnd, "Не удалось сохранить файл:\n"+path, appTitle, 0x10)
	}
}

// --------------------------------------------------------------- настройки

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
		// Настройки от прежней версии: полей сияния и стекла в них нет, а
		// пропущенное поле в JSON — это false, то есть «выключено». Молча
		// выключать новое в обновлении неправильно, включаем сами.
		cfg.Glow, cfg.Blur = true, true
		if cfg.Mode == modeMarker {
			cfg.Mode = modePlain // плашка непрозрачна и закрыла бы ореол
		}
		cfg.Version = cfgVersion
	}
	if cfg.Alpha < 0 || cfg.Alpha > 100 {
		cfg.Alpha = 80
	}
	if cfg.Mode < 0 || cfg.Mode >= len(modeNames) {
		cfg.Mode = modeMarker // сюда же попадает старый режим «Текст» = 2
	}
	if cfg.Palette < 0 || cfg.Palette >= len(palettes) {
		cfg.Palette = 0
	}
	if cfg.FontSize < fontMin || cfg.FontSize > fontMax {
		cfg.FontSize = 16
	}
	if !cfg.Tray && !cfg.Taskbar {
		cfg.Tray = true // до окна всегда должен быть хоть один путь
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

// ------------------------------------------------------------- оконная процедура

// backProc — окно-подложка: та самая стекляшка. LWA_ALPHA применяется к ней,
// а не к тексту, поэтому буквы не выцветают вместе с фоном.
func backProc(hwnd, m, wp, lp uintptr) uintptr {
	switch m {
	case wmEraseBkgnd:
		return 1

	case wmLButtonDblClk:
		// Двойной щелчок по пустому месту заметки — прокол в то, что снизу.
		a.punchArm()
		return 0

	case wmLButtonUp:
		a.punchRelease()
		return 0

	case wmLButtonDown:
		// Клик по подложке = клик по заметке: поднимаем окно и уводим фокус
		// в текст, чтобы можно было сразу печатать.
		if a.hwnd != 0 {
			a.raiseBack()
			setForegroundWindow(a.hwnd)
			setFocus(a.hEdit)
		}
		return 0
	case wmPaint:
		// Рисуем через промежуточный холст: слоёв несколько, и без него
		// градиенты моргали бы друг сквозь друга при каждом обновлении.
		var ps paintStruct
		hdc := beginPaint(hwnd, &ps)
		c := getClientRect(hwnd)
		r := rect{0, 0, c.w(), c.h()}
		mem := createCompatibleDC(hdc)
		bmp := createCompatibleBitmap(hdc, c.w(), c.h())
		old := selectObject(mem, bmp)
		if a.backBrush != 0 {
			fillRect(mem, &r, a.backBrush) // на случай, если градиента нет
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

// Все параметры — uintptr: syscall.NewCallback принимает только аргументы
// размером со слово, uint32 здесь дал бы панику при регистрации коллбэка.
func wndProc(hwnd, m, wp, lp uintptr) uintptr {
	seq := logMsgIn("main", m)
	defer logMsgOut(seq, m)

	switch m {
	case wmNCCalcSize:
		if wp != 0 {
			return 0 // клиентская область = всё окно, рамку рисуем сами
		}

	// Рамку окно рисует себе само (см. wmNCCalcSize), но при потере фокуса
	// DefWindowProc всё равно перекрашивает неклиентскую область WS_THICKFRAME
	// — и по краям вспыхивала белая рамка, которая держалась до следующего
	// SetWindowPos. lParam = -1 говорит «состояние обнови, перерисовку не
	// затевай», поэтому обработку не подменяем, а лишь глушим отрисовку.
	case wmNCActivate:
		return defWindowProc(hwnd, uint32(m), wp, ^uintptr(0))

	case wmNCHitTest:
		p := point{loWord(lp), hiWord(lp)}
		wr := getWindowRect(hwnd)
		return uintptr(a.hitTest(hwnd, p.X-wr.Left, p.Y-wr.Top))

	case wmGetMinMaxInfo:
		// go vet ругается на unsafe.Pointer(lp) — здесь это нормально:
		// lParam и есть указатель на MINMAXINFO, выданный системой.
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
		// Одно сообщение и на перемещение, и на изменение размера, и на смену
		// z-порядка — подложке достаточно этого, чтобы никогда не отставать.
		a.syncBackdrop()
		return defWindowProc(hwnd, uint32(m), wp, lp)

	// WM_CTLCOLORSTATIC — на случай, если система решит красить поле как
	// статику: цвета должны быть те же, иначе фон станет системным белым.
	case wmCtlColorEdit, wmCtlColorStat:
		pal := palettes[a.cfg.Palette%len(palettes)]
		setTextColor(wp, pal.fg)
		// Режим фона всегда непрозрачный. EDIT перерисовывает изменённую
		// строку одним TextOut и рассчитывает, что тот сам затрёт старые
		// пиксели фоновым цветом; при transparentBkMode затирания нет, и
		// стёртая буква остаётся на экране, а новая ложится поверх неё.
		// Вне маркера фоновый цвет = ключевой, то есть прозрачный: вид
		// прежний, а старые глифы уходят.
		bk := colorKey
		if a.cfg.Mode == modeMarker {
			bk = pal.marker // непрозрачная плашка под буквами
		}
		setBkColor(wp, bk)
		setBkMode(wp, opaqueBkMode)
		return a.bgBrush // ключевой цвет: сквозь фон видно подложку

	case wmLButtonDown:
		a.onLButtonDown(loWord(lp), hiWord(lp))
		return 0

	case wmLButtonDblClk:
		// На панели двойной щелчок ничего не прокалывает: там кнопки.
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
			// Текст изменили или прокрутили — ореол пора перерисовать.
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
		case wmRButtonUp, 0x0204: // правая кнопка — меню
			a.trayMenu()
		case 0x0203: // двойной левый клик — показать/спрятать
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
			// Пульс: пока эти строки идут, цикл сообщений жив. Если журнал
			// обрывается на «IN #N» без «OUT #N» — встали на том сообщении.
			a.beats++
			logf("пульс %d", a.beats)
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
		logf("WM_DESTROY: сохраняюсь и выхожу")
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
