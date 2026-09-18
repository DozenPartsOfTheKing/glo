//go:build windows

// UI language. The settings menu picks one explicitly (saved in
// settings.json as "lang"); "auto" follows GLO_LANG if set, otherwise the
// Windows display language, English for anything not listed below.
//
// All user-visible strings live in this file. To add a language: add a lang
// constant, its code and name, a case in systemLang and one more argument to
// every tr call.
package main

import (
	"os"
	"strings"
)

const (
	langEN = iota
	langRU
	langES
	langZH
	langFR
)

// Codes as stored in settings.json, and names as shown in the menu: each
// language is named in itself, so it can be found from any other one.
var (
	langCodes = []string{"en", "ru", "es", "zh", "fr"}
	langNames = []string{"English", "Русский", "Español", "中文", "Français"}
)

var pGetUserDefaultUILanguage = kernel32.NewProc("GetUserDefaultUILanguage")

var uiLang = langEN

// Strings must exist before main runs: loadConfig already reads modeNames.
func init() { setLang("") }

func langIndex(code string) int {
	for i, c := range langCodes {
		if c == code {
			return i
		}
	}
	return -1
}

func systemLang() int {
	if i := langIndex(strings.ToLower(os.Getenv("GLO_LANG"))); i >= 0 {
		return i
	}
	r, _, _ := pGetUserDefaultUILanguage.Call()
	switch r & 0x3FF { // low 10 bits of a LANGID are the primary language
	case 0x19:
		return langRU
	case 0x0A:
		return langES
	case 0x04: // Simplified and Traditional both get Simplified
		return langZH
	case 0x0C:
		return langFR
	}
	return langEN
}

// setLang switches the UI language; "" or an unknown code means auto.
func setLang(code string) {
	uiLang = langIndex(code)
	if uiLang < 0 {
		uiLang = systemLang()
	}
	loadStrings()
}

// tr picks the string for the UI language. Argument order: en, ru, es, zh, fr.
func tr(en, ru, es, zh, fr string) string {
	return [...]string{en, ru, es, zh, fr}[uiLang]
}

var (
	// UI strings, filled by loadStrings.
	txtNoMarker,
	txtMarker,
	txtWhite,
	txtBlack,
	txtRed,
	txtGreen,
	txtBlue,
	txtCyan,
	txtPink,
	txtYellow,
	txtTrayTip,
	txtGlow,
	txtSettings,
	txtGlassOpacity,
	txtFontSize,
	txtTextColor,
	txtTextGlow,
	txtMarkerBG,
	txtFrostedGlass,
	txtAlwaysOnTop,
	txtDropBelow,
	txtClickThrough,
	txtHideToolbar,
	txtTrayIcon,
	txtTaskbarButton,
	txtOpenFile,
	txtSaveAs,
	txtNoteFolder,
	txtExit,
	txtHide,
	txtShow,
	txtClickThroughOff,
	txtClose,
	txtOpenFailed,
	txtSaveFailed,
	txtFileFilter,
	txtLanguage,
	txtLangAuto string

	modeNames   []string
	txtColors   []string // palette names, same order as palettes
	welcomeNote string
)

// loadStrings fills every UI string for uiLang. Called again when the
// language changes, so nothing may cache these values across a switch.
func loadStrings() {
	txtNoMarker = tr("No marker", "Без плашки", "Sin resaltado", "无底色", "Sans surlignage")
	txtMarker = tr("Marker", "Маркер", "Resaltado", "底色", "Surlignage")

	txtWhite = tr("White", "Белый", "Blanco", "白色", "Blanc")
	txtBlack = tr("Black", "Чёрный", "Negro", "黑色", "Noir")
	txtRed = tr("Red", "Красный", "Rojo", "红色", "Rouge")
	txtGreen = tr("Green", "Зелёный", "Verde", "绿色", "Vert")
	txtBlue = tr("Blue", "Синий", "Azul", "蓝色", "Bleu")
	txtCyan = tr("Cyan", "Циан", "Cian", "青色", "Cyan")
	txtPink = tr("Pink", "Розовый", "Rosa", "粉色", "Rose")
	txtYellow = tr("Yellow", "Жёлтый", "Amarillo", "黄色", "Jaune")

	txtTrayTip = tr(
		"Glo — right-click: menu, double-click: show/hide",
		"Glo — правый клик: меню, двойной: показать/скрыть",
		"Glo — clic derecho: menú, doble clic: mostrar/ocultar",
		"Glo — 右键：菜单，双击：显示/隐藏",
		"Glo — clic droit : menu, double-clic : afficher/masquer")

	// toolbar
	txtGlow = tr("Glow", "Сияние", "Brillo", "发光", "Lueur")
	txtSettings = tr("Settings", "Настройки", "Ajustes", "设置", "Réglages")

	// settings menu
	txtGlassOpacity = tr("Glass opacity", "Прозрачность подложки", "Opacidad del cristal", "玻璃不透明度", "Opacité du verre")
	txtFontSize = tr("Font size", "Размер шрифта", "Tamaño de fuente", "字号", "Taille de police")
	txtTextColor = tr("Text color", "Цвет букв", "Color del texto", "文字颜色", "Couleur du texte")
	txtTextGlow = tr("Text glow", "Сияние букв", "Brillo del texto", "文字发光", "Lueur du texte")
	txtMarkerBG = tr("Marker background", "Плашка маркера", "Fondo resaltado", "文字底色", "Surlignage du texte")
	txtFrostedGlass = tr("Frosted glass", "Матовое стекло", "Cristal esmerilado", "磨砂玻璃", "Verre dépoli")
	txtAlwaysOnTop = tr("Always on top", "Поверх всех окон", "Siempre visible", "窗口置顶", "Toujours au premier plan")
	txtDropBelow = tr("Double-click sends window back", "Двойной щелчок уводит окно вниз", "Doble clic envía la ventana atrás", "双击后窗口移到底层", "Double-clic : fenêtre en arrière-plan")
	txtClickThrough = tr("Click-through mode", "Сквозной клик насовсем", "Modo clic a través", "鼠标穿透模式", "Mode clic traversant")
	txtHideToolbar = tr("Hide this toolbar", "Скрыть эту панель", "Ocultar esta barra", "隐藏工具栏", "Masquer cette barre")
	txtTrayIcon = tr("Tray icon", "Значок у часов", "Icono en la bandeja", "托盘图标", "Icône de notification")
	txtTaskbarButton = tr("Taskbar button", "Кнопка в панели задач", "Botón en la barra de tareas", "任务栏按钮", "Bouton dans la barre des tâches")
	txtOpenFile = tr("Open file…", "Открыть файл…", "Abrir archivo…", "打开文件…", "Ouvrir un fichier…")
	txtSaveAs = tr("Save as…", "Сохранить как…", "Guardar como…", "另存为…", "Enregistrer sous…")
	txtNoteFolder = tr("Open note folder", "Папка с заметкой", "Abrir carpeta de la nota", "打开便签文件夹", "Ouvrir le dossier de la note")
	txtExit = tr("Exit", "Выход", "Salir", "退出", "Quitter")
	// Always also says "Language" in English, so a user stuck in an
	// unfamiliar language can still find the way back.
	txtLanguage = tr("Language", "Язык (Language)", "Idioma (Language)", "语言 (Language)", "Langue (Language)")
	txtLangAuto = tr("Auto (as in Windows)", "Авто (как в Windows)", "Automático (como en Windows)", "自动（跟随 Windows）", "Auto (comme Windows)")

	// tray menu
	txtHide = tr("Hide", "Свернуть", "Ocultar", "隐藏", "Masquer")
	txtShow = tr("Show", "Развернуть", "Mostrar", "显示", "Afficher")
	txtClickThroughOff = tr("Turn off click-through", "Выключить сквозной клик", "Desactivar clic a través", "关闭鼠标穿透", "Désactiver le clic traversant")
	txtClose = tr("Close", "Закрыть", "Cerrar", "关闭", "Fermer")

	txtOpenFailed = tr("Could not open file:\n", "Не удалось открыть файл:\n",
		"No se pudo abrir el archivo:\n", "无法打开文件：\n", "Impossible d’ouvrir le fichier :\n")
	txtSaveFailed = tr("Could not save file:\n", "Не удалось сохранить файл:\n",
		"No se pudo guardar el archivo:\n", "无法保存文件：\n", "Impossible d’enregistrer le fichier :\n")

	// file dialog filter: pairs of "label\x00pattern\x00"
	txtFileFilter = tr(
		"Text files (*.txt)\x00*.txt\x00All files (*.*)\x00*.*\x00",
		"Текстовые файлы (*.txt)\x00*.txt\x00Все файлы (*.*)\x00*.*\x00",
		"Archivos de texto (*.txt)\x00*.txt\x00Todos los archivos (*.*)\x00*.*\x00",
		"文本文件 (*.txt)\x00*.txt\x00所有文件 (*.*)\x00*.*\x00",
		"Fichiers texte (*.txt)\x00*.txt\x00Tous les fichiers (*.*)\x00*.*\x00")

	modeNames = []string{txtNoMarker, txtMarker}
	txtColors = []string{txtWhite, txtBlack, txtRed, txtGreen, txtBlue, txtCyan, txtPink, txtYellow}

	// First-run text: the program ships as a single file with no docs next to it,
	// so it explains itself. It is erased like any other text.
	welcomeNote = tr(`Glo — a note that stays on top of all windows.

To close: the cross at the right of the toolbar, Alt+F4,
or right-click the tray icon -> Exit.

Double-click the note = click whatever is under it.
A single click stays with the note, so the window
never gets in the way of the app below.

Toolbar:
  slider     glass opacity. The text never fades
  - 16 +     font size, 8-72. Click the number for presets
  Aa         white/black text. Right-click for colors
  Glow       neon halo and bold outline, Ctrl+G
  Marker     background under the text, readable on anything
  Settings   everything else: always on top, frosted glass,
             tray, taskbar button, colors, files, exit

Drag by an empty spot of the toolbar. Resize by edges and corners.
Ctrl+H hides the toolbar, Ctrl+Alt+H hides the whole window.
Text saves itself every 5 seconds.

You can delete this text.`,

		`Glo — заметка поверх всех окон.

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

Этот текст можно стереть.`,

		`Glo — una nota que siempre está encima de todas las ventanas.

Para cerrar: la cruz a la derecha de la barra, Alt+F4,
o clic derecho en el icono de la bandeja -> Salir.

Doble clic en la nota = clic en lo que hay debajo.
Un clic normal se queda en la nota, así la ventana
nunca estorba a la aplicación de abajo.

Barra superior:
  control    opacidad del cristal. El texto nunca se desvanece
  - 16 +     tamaño de fuente, 8-72. Clic en el número: tamaños
  Aa         texto blanco/negro. Clic derecho: colores
  Brillo     halo de neón y contorno grueso, Ctrl+G
  Resaltado  fondo bajo el texto, legible sobre cualquier cosa
  Ajustes    todo lo demás: siempre visible, cristal esmerilado,
             bandeja, barra de tareas, colores, archivos, salir

Arrastra desde un hueco vacío de la barra. Cambia el tamaño por los bordes.
Ctrl+H oculta la barra, Ctrl+Alt+H oculta toda la ventana.
El texto se guarda solo cada 5 segundos.

Puedes borrar este texto.`,

		`Glo — 始终置顶的便签。

关闭方法：工具栏右侧的叉号、Alt+F4，
或右键托盘图标 -> 退出。

双击便签 = 点击便签下面的内容。
单击仍然作用于便签，窗口不会妨碍下面的程序。

顶部工具栏：
  滑块      玻璃不透明度，文字永远不会变淡
  - 16 +    字号 8-72，点击数字切换预设
  Aa        白色/黑色文字，右键切换颜色
  发光      霓虹光晕和粗轮廓，Ctrl+G
  底色      文字下方的底色，任何背景上都清晰
  设置      其他功能：置顶、磨砂玻璃、托盘、
            任务栏按钮、颜色、文件、退出

拖动工具栏空白处移动窗口，拖动边缘和角调整大小。
Ctrl+H 隐藏工具栏，Ctrl+Alt+H 隐藏整个窗口。
文字每 5 秒自动保存。

这段文字可以删除。`,

		`Glo — une note qui reste au-dessus de toutes les fenêtres.

Pour fermer : la croix à droite de la barre, Alt+F4,
ou clic droit sur l’icône de notification -> Quitter.

Double-clic sur la note = clic sur ce qui est dessous.
Un clic simple reste pour la note : la fenêtre
ne gêne jamais l’application du dessous.

Barre du haut :
  curseur     opacité du verre. Le texte ne pâlit jamais
  - 16 +      taille de police, 8-72. Clic sur le nombre : tailles
  Aa          texte blanc/noir. Clic droit : couleurs
  Lueur       halo néon et contour épais, Ctrl+G
  Surlignage  fond sous le texte, lisible sur tout
  Réglages    tout le reste : premier plan, verre dépoli,
              notification, barre des tâches, couleurs, fichiers

Déplacez par un endroit vide de la barre. Redimensionnez par les bords.
Ctrl+H masque la barre, Ctrl+Alt+H masque toute la fenêtre.
Le texte s’enregistre tout seul toutes les 5 secondes.

Vous pouvez effacer ce texte.`)
}
