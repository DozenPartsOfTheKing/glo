//go:build windows

// Тонкая обёртка над Win32 API: только то, что реально используется.
package main

import (
	"runtime"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// Всюду, где Go-указатель уходит в Win32 как uintptr, после вызова стоит
// runtime.KeepAlive: иначе сборщик мусора вправе освободить буфер прямо
// во время вызова — указателя-то на него уже нет, только число.

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")

	pShellNotifyIcon = shell32.NewProc("Shell_NotifyIconW")
	pShellExecute    = shell32.NewProc("ShellExecuteW")
	pLoadIcon        = user32.NewProc("LoadIconW")
	pPostMessage     = user32.NewProc("PostMessageW")

	pRegisterClassEx     = user32.NewProc("RegisterClassExW")
	pCreateWindowEx      = user32.NewProc("CreateWindowExW")
	pDefWindowProc       = user32.NewProc("DefWindowProcW")
	pGetMessage          = user32.NewProc("GetMessageW")
	pTranslateMessage    = user32.NewProc("TranslateMessage")
	pDispatchMessage     = user32.NewProc("DispatchMessageW")
	pPostQuitMessage     = user32.NewProc("PostQuitMessage")
	pDestroyWindow       = user32.NewProc("DestroyWindow")
	pShowWindow          = user32.NewProc("ShowWindow")
	pUpdateWindow        = user32.NewProc("UpdateWindow")
	pSetLayeredWinAttr   = user32.NewProc("SetLayeredWindowAttributes")
	pSetWindowLongPtr    = user32.NewProc("SetWindowLongPtrW")
	pGetWindowLongPtr    = user32.NewProc("GetWindowLongPtrW")
	pSetWindowPos        = user32.NewProc("SetWindowPos")
	pGetClientRect       = user32.NewProc("GetClientRect")
	pGetWindowRect       = user32.NewProc("GetWindowRect")
	pMoveWindow          = user32.NewProc("MoveWindow")
	pSendMessage         = user32.NewProc("SendMessageW")
	pSetFocus            = user32.NewProc("SetFocus")
	pBeginPaint          = user32.NewProc("BeginPaint")
	pEndPaint            = user32.NewProc("EndPaint")
	pFillRect            = user32.NewProc("FillRect")
	pDrawText            = user32.NewProc("DrawTextW")
	pInvalidateRect      = user32.NewProc("InvalidateRect")
	pSetCapture          = user32.NewProc("SetCapture")
	pReleaseCapture      = user32.NewProc("ReleaseCapture")
	pRegisterHotKey      = user32.NewProc("RegisterHotKey")
	pUnregisterHotKey    = user32.NewProc("UnregisterHotKey")
	pCreateAccelTable    = user32.NewProc("CreateAcceleratorTableW")
	pTranslateAccel      = user32.NewProc("TranslateAcceleratorW")
	pLoadCursor          = user32.NewProc("LoadCursorW")
	pCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	pAppendMenu          = user32.NewProc("AppendMenuW")
	pTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	pDestroyMenu         = user32.NewProc("DestroyMenu")
	pGetCursorPos        = user32.NewProc("GetCursorPos")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	pMessageBox          = user32.NewProc("MessageBoxW")
	pSetProcessDPIAware  = user32.NewProc("SetProcessDPIAware")
	pSetTimer            = user32.NewProc("SetTimer")
	pGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	pGetDC               = user32.NewProc("GetDC")
	pReleaseDC           = user32.NewProc("ReleaseDC")

	pCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	pDeleteObject     = gdi32.NewProc("DeleteObject")
	pCreateFont       = gdi32.NewProc("CreateFontW")
	pSetTextColor     = gdi32.NewProc("SetTextColor")
	pSetBkColor       = gdi32.NewProc("SetBkColor")
	pSetBkMode        = gdi32.NewProc("SetBkMode")
	pSelectObject     = gdi32.NewProc("SelectObject")
	pGetTextExtent    = gdi32.NewProc("GetTextExtentPoint32W")
	pCreateCompatDC   = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatBmp  = gdi32.NewProc("CreateCompatibleBitmap")
	pBitBlt           = gdi32.NewProc("BitBlt")
	pDeleteDC         = gdi32.NewProc("DeleteDC")
	pGetDeviceCaps    = gdi32.NewProc("GetDeviceCaps")

	pGetModuleHandle = kernel32.NewProc("GetModuleHandleW")

	pGetOpenFileName = comdlg32.NewProc("GetOpenFileNameW")
	pGetSaveFileName = comdlg32.NewProc("GetSaveFileNameW")
)

// ---------------------------------------------------------------- константы

const (
	wsPopup      = 0x80000000
	wsVisible    = 0x10000000
	wsChild      = 0x40000000
	wsThickFrame = 0x00040000
	wsClipChild  = 0x02000000

	wsExLayered     = 0x00080000
	wsExTopmost     = 0x00000008
	wsExToolWindow  = 0x00000080
	wsExTransparent = 0x00000020
	wsExNoActivate  = 0x08000000
	wsExAppWindow   = 0x00040000

	esMultiline  = 0x0004
	esAutoVScrol = 0x0040
	esWantReturn = 0x1000
	esNoHideSel  = 0x0100

	lwaColorKey = 0x1
	lwaAlpha    = 0x2

	gwlExStyle = -20

	wmDestroy       = 0x0002
	wmSize          = 0x0005
	wmSetFocusMsg   = 0x0007
	wmPaint         = 0x000F
	wmClose         = 0x0010
	wmEraseBkgnd    = 0x0014
	wmGetMinMaxInfo = 0x0024
	wmSetFont       = 0x0030
	wmGetTextLength = 0x000E
	wmGetText       = 0x000D
	wmSetText       = 0x000C
	wmTimer         = 0x0113
	wmCommand       = 0x0111
	wmCtlColorEdit  = 0x0133
	wmCtlColorStat  = 0x0138
	wmWindowPosChgd = 0x0047
	wmNCCalcSize    = 0x0083
	wmNCHitTest     = 0x0084
	wmNCLButtonDown = 0x00A1
	wmLButtonDown   = 0x0201
	wmLButtonUp     = 0x0202
	wmMouseMove     = 0x0200
	wmRButtonUp     = 0x0205
	wmHotKey        = 0x0312

	emSetLimitText = 0x00C5

	htClient      = 1
	htCaption     = 2
	htBottomRight = 17

	swHide        = 0
	swShow        = 5
	swShowNA      = 8 // показать, не забирая фокус
	swpNoZ        = 0x0004
	swpFrm        = 0x0020
	swpNoMv       = 0x0002
	swpNoSz       = 0x0001
	swpNoActivate = 0x0010

	hwndTopmost   = ^uintptr(0) // (HWND)-1
	hwndNoTopmost = ^uintptr(1) // (HWND)-2

	idcArrow = 32512

	dtSingleLine = 0x20
	dtVCenter    = 0x04
	dtCenter     = 0x01
	dtLeft       = 0x00

	transparentBkMode = 1
	opaqueBkMode      = 2

	logPixelsY = 90

	modAlt      = 0x0001
	modControl  = 0x0002
	modNoRepeat = 0x4000

	fVirtKey  = 0x01
	fControl  = 0x08
	fAlt      = 0x10
	tpmRetCmd = 0x0100
	tpmRight  = 0x0002

	mfString    = 0x0000
	mfSeparator = 0x0800
	mfChecked   = 0x0008
	mfPopup     = 0x0010

	nimAdd     = 0
	nimModify  = 1
	nimDelete  = 2
	nifMessage = 0x01
	nifIcon    = 0x02
	nifTip     = 0x04
	idiApp     = 32512
	wmTrayIcon = 0x8001 // WM_APP + 1
	wmNull     = 0x0000

	srcCopy = 0x00CC0020

	ofnFileMustExist  = 0x00001000
	ofnPathMustExist  = 0x00000800
	ofnHideReadOnly   = 0x00000004
	ofnOverwritePromp = 0x00000002
	ofnExplorer       = 0x00080000
)

// ------------------------------------------------------------------ структуры

type point struct{ X, Y int32 }

type rect struct{ Left, Top, Right, Bottom int32 }

func (r rect) w() int32 { return r.Right - r.Left }
func (r rect) h() int32 { return r.Bottom - r.Top }
func (r rect) has(x, y int32) bool {
	return x >= r.Left && x < r.Right && y >= r.Top && y < r.Bottom
}

type msg struct {
	Hwnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       point
	LPrivate uint32
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type paintStruct struct {
	Hdc         uintptr
	FErase      int32
	RcPaint     rect
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

type size struct{ CX, CY int32 }

type accel struct {
	FVirt byte
	Key   uint16
	Cmd   uint16
}

type minMaxInfo struct {
	PtReserved     point
	PtMaxSize      point
	PtMaxPosition  point
	PtMinTrackSize point
	PtMaxTrackSize point
}

// notifyIconData — NOTIFYICONDATAW для amd64, размер 976 байт. Поля-заполнители
// повторяют выравнивание указателей в оригинальной структуре.
type notifyIconData struct {
	CbSize           uint32
	_                uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	_                uint32
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

// openFileName — раскладка OPENFILENAMEW для amd64 (явные поля-заполнители
// рассчитаны на 8-байтное выравнивание указателей). Собирать только под 64 бита.
type openFileName struct {
	LStructSize       uint32
	_                 uint32
	HwndOwner         uintptr
	HInstance         uintptr
	LpstrFilter       *uint16
	LpstrCustomFilter *uint16
	NMaxCustFilter    uint32
	NFilterIndex      uint32
	LpstrFile         *uint16
	NMaxFile          uint32
	_                 uint32
	LpstrFileTitle    *uint16
	NMaxFileTitle     uint32
	_                 uint32
	LpstrInitialDir   *uint16
	LpstrTitle        *uint16
	Flags             uint32
	NFileOffset       uint16
	NFileExtension    uint16
	LpstrDefExt       *uint16
	LCustData         uintptr
	LpfnHook          uintptr
	LpTemplateName    *uint16
	PvReserved        uintptr
	DwReserved        uint32
	FlagsEx           uint32
}

// -------------------------------------------------------------------- хелперы

func rgb(r, g, b uint32) uint32 { return r | g<<8 | b<<16 }

func loWord(v uintptr) int32 { return int32(int16(uint16(v & 0xFFFF))) }
func hiWord(v uintptr) int32 { return int32(int16(uint16((v >> 16) & 0xFFFF))) }

func str16(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		p, _ = syscall.UTF16PtrFromString("")
	}
	return p
}

// utf16z кодирует строку, в которой сами по себе есть \x00 (фильтры диалогов),
// и добавляет завершающий ноль.
func utf16z(s string) *uint16 {
	u := utf16.Encode([]rune(s))
	u = append(u, 0)
	return &u[0]
}

// ------------------------------------------------------------------- функции

func getModuleHandle() uintptr {
	h, _, _ := pGetModuleHandle.Call(0)
	return h
}

func registerClass(wc *wndClassEx) uintptr {
	a, _, _ := pRegisterClassEx.Call(uintptr(unsafe.Pointer(wc)))
	runtime.KeepAlive(wc)
	return a
}

func createWindowEx(exStyle uint32, class, title *uint16, style uint32,
	x, y, w, h int32, parent, menu, inst uintptr) uintptr {
	hwnd, _, _ := pCreateWindowEx.Call(
		uintptr(exStyle), uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)),
		uintptr(style), uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		parent, menu, inst, 0)
	runtime.KeepAlive(class)
	runtime.KeepAlive(title)
	return hwnd
}

func defWindowProc(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	r, _, _ := pDefWindowProc.Call(hwnd, uintptr(m), wp, lp)
	return r
}

func getMessage(m *msg) int32 {
	r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(m)), 0, 0, 0)
	return int32(r)
}

func translateMessage(m *msg) { pTranslateMessage.Call(uintptr(unsafe.Pointer(m))) }
func dispatchMessage(m *msg)  { pDispatchMessage.Call(uintptr(unsafe.Pointer(m))) }
func postQuitMessage(c int32) { pPostQuitMessage.Call(uintptr(c)) }
func destroyWindow(h uintptr) { pDestroyWindow.Call(h) }
func showWindow(h uintptr, c int32) {
	pShowWindow.Call(h, uintptr(c))
}
func updateWindow(h uintptr) { pUpdateWindow.Call(h) }

func setLayered(h uintptr, key uint32, alpha byte, flags uint32) {
	pSetLayeredWinAttr.Call(h, uintptr(key), uintptr(alpha), uintptr(flags))
}

func getWindowLong(h uintptr, idx int32) uintptr {
	r, _, _ := pGetWindowLongPtr.Call(h, uintptr(idx))
	return r
}

func setWindowLong(h uintptr, idx int32, v uintptr) {
	pSetWindowLongPtr.Call(h, uintptr(idx), v)
}

func setWindowPos(h, after uintptr, x, y, w, ht int32, flags uint32) {
	pSetWindowPos.Call(h, after, uintptr(x), uintptr(y), uintptr(w), uintptr(ht), uintptr(flags))
}

func getClientRect(h uintptr) rect {
	var r rect
	pGetClientRect.Call(h, uintptr(unsafe.Pointer(&r)))
	return r
}

func getWindowRect(h uintptr) rect {
	var r rect
	pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r)))
	return r
}

func moveWindow(h uintptr, x, y, w, ht int32, repaint bool) {
	var rp uintptr
	if repaint {
		rp = 1
	}
	pMoveWindow.Call(h, uintptr(x), uintptr(y), uintptr(w), uintptr(ht), rp)
}

func sendMessage(h uintptr, m uint32, wp, lp uintptr) uintptr {
	r, _, _ := pSendMessage.Call(h, uintptr(m), wp, lp)
	return r
}

func setFocus(h uintptr) { pSetFocus.Call(h) }

func postMessage(h uintptr, m uint32, wp, lp uintptr) {
	pPostMessage.Call(h, uintptr(m), wp, lp)
}

func loadAppIcon() uintptr {
	i, _, _ := pLoadIcon.Call(0, uintptr(idiApp))
	return i
}

func shellOpen(path string) {
	op, p := str16("open"), str16(path)
	pShellExecute.Call(0, uintptr(unsafe.Pointer(op)), uintptr(unsafe.Pointer(p)),
		0, 0, uintptr(swShow))
	runtime.KeepAlive(op)
	runtime.KeepAlive(p)
}

func shellNotifyIcon(action uint32, nid *notifyIconData) bool {
	r, _, _ := pShellNotifyIcon.Call(uintptr(action), uintptr(unsafe.Pointer(nid)))
	runtime.KeepAlive(nid)
	return r != 0
}

func beginPaint(h uintptr, ps *paintStruct) uintptr {
	hdc, _, _ := pBeginPaint.Call(h, uintptr(unsafe.Pointer(ps)))
	return hdc
}

func endPaint(h uintptr, ps *paintStruct) {
	pEndPaint.Call(h, uintptr(unsafe.Pointer(ps)))
}

func fillRect(hdc uintptr, r *rect, brush uintptr) {
	pFillRect.Call(hdc, uintptr(unsafe.Pointer(r)), brush)
}

func drawText(hdc uintptr, text string, r *rect, flags uint32) {
	u := utf16.Encode([]rune(text))
	if len(u) == 0 {
		return
	}
	pDrawText.Call(hdc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)),
		uintptr(unsafe.Pointer(r)), uintptr(flags))
	runtime.KeepAlive(u)
	runtime.KeepAlive(r)
}

func textWidth(hdc uintptr, text string) int32 {
	u := utf16.Encode([]rune(text))
	if len(u) == 0 {
		return 0
	}
	var sz size
	pGetTextExtent.Call(hdc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)),
		uintptr(unsafe.Pointer(&sz)))
	runtime.KeepAlive(u)
	return sz.CX
}

func invalidate(h uintptr, r *rect) {
	var p uintptr
	if r != nil {
		p = uintptr(unsafe.Pointer(r))
	}
	pInvalidateRect.Call(h, p, 0)
}

func setCapture(h uintptr) { pSetCapture.Call(h) }
func releaseCapture()      { pReleaseCapture.Call() }

func registerHotKey(h uintptr, id int32, mods, vk uint32) bool {
	r, _, _ := pRegisterHotKey.Call(h, uintptr(id), uintptr(mods), uintptr(vk))
	return r != 0
}

func unregisterHotKey(h uintptr, id int32) { pUnregisterHotKey.Call(h, uintptr(id)) }

func createAcceleratorTable(a []accel) uintptr {
	r, _, _ := pCreateAccelTable.Call(uintptr(unsafe.Pointer(&a[0])), uintptr(len(a)))
	runtime.KeepAlive(a)
	return r
}

func translateAccelerator(h, acc uintptr, m *msg) bool {
	r, _, _ := pTranslateAccel.Call(h, acc, uintptr(unsafe.Pointer(m)))
	return r != 0
}

func loadCursorArrow() uintptr {
	c, _, _ := pLoadCursor.Call(0, uintptr(idcArrow))
	return c
}

func createPopupMenu() uintptr {
	m, _, _ := pCreatePopupMenu.Call()
	return m
}

func appendMenu(menu uintptr, flags uint32, id uintptr, text string) {
	var p uintptr
	t := str16(text)
	if flags&mfSeparator == 0 {
		p = uintptr(unsafe.Pointer(t))
	}
	pAppendMenu.Call(menu, uintptr(flags), id, p)
	runtime.KeepAlive(t)
}

func trackPopupMenu(menu uintptr, flags uint32, x, y int32, hwnd uintptr) int32 {
	r, _, _ := pTrackPopupMenu.Call(menu, uintptr(flags), uintptr(x), uintptr(y), 0, hwnd, 0)
	return int32(r)
}

func destroyMenu(m uintptr) { pDestroyMenu.Call(m) }

func getCursorPos() point {
	var p point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return p
}

func setForegroundWindow(h uintptr) { pSetForegroundWindow.Call(h) }

func messageBox(h uintptr, text, caption string, flags uint32) {
	t, c := str16(text), str16(caption)
	pMessageBox.Call(h, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), uintptr(flags))
	runtime.KeepAlive(t)
	runtime.KeepAlive(c)
}

func setProcessDPIAware() { pSetProcessDPIAware.Call() }

func setTimer(h uintptr, id uintptr, ms uint32) { pSetTimer.Call(h, id, uintptr(ms), 0) }

func getSystemMetrics(i int32) int32 {
	r, _, _ := pGetSystemMetrics.Call(uintptr(i))
	return int32(r)
}

func getDC(h uintptr) uintptr {
	d, _, _ := pGetDC.Call(h)
	return d
}

func releaseDC(h, dc uintptr) { pReleaseDC.Call(h, dc) }

func createSolidBrush(c uint32) uintptr {
	b, _, _ := pCreateSolidBrush.Call(uintptr(c))
	return b
}

func deleteObject(o uintptr) {
	if o != 0 {
		pDeleteObject.Call(o)
	}
}

func createFont(height int32, weight int32, quality uint32, face string) uintptr {
	f, _, _ := pCreateFont.Call(
		uintptr(height), 0, 0, 0, uintptr(weight), 0, 0, 0,
		1 /*DEFAULT_CHARSET*/, 0, 0, uintptr(quality), 0,
		uintptr(unsafe.Pointer(str16(face))))
	return f
}

func setTextColor(hdc uintptr, c uint32) { pSetTextColor.Call(hdc, uintptr(c)) }
func setBkColor(hdc uintptr, c uint32)   { pSetBkColor.Call(hdc, uintptr(c)) }
func setBkMode(hdc uintptr, m int32)     { pSetBkMode.Call(hdc, uintptr(m)) }

func selectObject(hdc, obj uintptr) uintptr {
	o, _, _ := pSelectObject.Call(hdc, obj)
	return o
}

func createCompatibleDC(hdc uintptr) uintptr {
	d, _, _ := pCreateCompatDC.Call(hdc)
	return d
}

func createCompatibleBitmap(hdc uintptr, w, h int32) uintptr {
	b, _, _ := pCreateCompatBmp.Call(hdc, uintptr(w), uintptr(h))
	return b
}

func bitBlt(dst uintptr, x, y, w, h int32, src uintptr, sx, sy int32, rop uint32) {
	pBitBlt.Call(dst, uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		src, uintptr(sx), uintptr(sy), uintptr(rop))
}

func deleteDC(d uintptr) { pDeleteDC.Call(d) }

func getDeviceCaps(hdc uintptr, index int32) int32 {
	r, _, _ := pGetDeviceCaps.Call(hdc, uintptr(index))
	return int32(r)
}

// fileDialog показывает системный диалог. save=true — «сохранить как».
func fileDialog(owner uintptr, save bool, defExt string) string {
	buf := make([]uint16, 1024)
	ofn := openFileName{
		HwndOwner:   owner,
		LpstrFilter: utf16z("Текстовые файлы (*.txt)\x00*.txt\x00Все файлы (*.*)\x00*.*\x00"),
		LpstrFile:   &buf[0],
		NMaxFile:    uint32(len(buf)),
		LpstrDefExt: str16(defExt),
		Flags:       ofnExplorer | ofnHideReadOnly | ofnPathMustExist,
	}
	ofn.LStructSize = uint32(unsafe.Sizeof(ofn))
	if save {
		ofn.Flags |= ofnOverwritePromp
	} else {
		ofn.Flags |= ofnFileMustExist
	}
	proc := pGetOpenFileName
	if save {
		proc = pGetSaveFileName
	}
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&ofn)))
	runtime.KeepAlive(&ofn)
	runtime.KeepAlive(buf)
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}
