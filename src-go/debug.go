//go:build windows

// Журнал для отлова зависаний. Пишется в %AppData%\Glasspad\debug.log,
// перезаписывается при каждом запуске.
//
// Запись идёт обычным WriteFile без буферизации в процессе: данные сразу
// уходят в кеш ОС и переживают даже принудительное снятие задачи. Поэтому
// последняя строка журнала — это ровно то место, где программа встала.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var (
	logFile  *os.File
	logStart = time.Now()
	logSeq   int
)

func openLog(dir string) {
	if f, err := os.Create(filepath.Join(dir, "debug.log")); err == nil {
		logFile = f
	}
	logf("=== Glasspad старт, pid %d, аргументы %v", os.Getpid(), os.Args[1:])
}

func logf(format string, args ...interface{}) {
	if logFile == nil {
		return
	}
	fmt.Fprintf(logFile, "%8.3f  %s\r\n", time.Since(logStart).Seconds(),
		fmt.Sprintf(format, args...))
}

func closeLog() {
	if logFile != nil {
		logFile.Close()
		logFile = nil
	}
}

// Сообщения, которые сыплются пачками при каждом движении мыши, — их в журнал
// не пускаем, иначе полезное утонет.
func noisyMsg(m uintptr) bool {
	switch m {
	case 0x0200, // WM_MOUSEMOVE
		0x0084, // WM_NCHITTEST
		0x0020, // WM_SETCURSOR
		0x00A0, // WM_NCMOUSEMOVE
		0x02A0, // WM_NCMOUSEHOVER
		0x02A3, // WM_MOUSELEAVE
		0x0113: // WM_TIMER — вместо него идёт отдельный heartbeat
		return true
	}
	return false
}

// logMsgIn/logMsgOut ставят вокруг обработчика пару меток. Если в журнале
// есть «IN #42», но нет «OUT #42» — программа встала именно на этом сообщении.
func logMsgIn(tag string, m uintptr) int {
	if logFile == nil || noisyMsg(m) {
		return 0
	}
	logSeq++
	logf("IN  #%d %s 0x%04X", logSeq, tag, m)
	return logSeq
}

func logMsgOut(seq int, m uintptr) {
	if seq != 0 {
		logf("OUT #%d 0x%04X", seq, m)
	}
}
