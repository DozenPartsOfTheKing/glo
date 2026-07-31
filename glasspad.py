# -*- coding: utf-8 -*-
"""
Glasspad — прозрачный блокнот поверх всех окон, прозрачность 0-100%.

Запуск:  python glasspad.py
"""

import json
import os
import sys
import tkinter as tk
from tkinter import filedialog, messagebox

IS_WIN = sys.platform.startswith("win")
if IS_WIN:
    import ctypes

# %AppData%\Glasspad — общая папка с exe-версией, файлы у них одни и те же.
APP_DIR = os.path.join(os.environ.get("APPDATA") or os.path.expanduser("~"), "Glasspad")
CFG_PATH = os.path.join(APP_DIR, "settings.json")
NOTE_PATH = os.path.join(APP_DIR, "note.txt")

# Цвет-ключ: пиксели этого цвета становятся полностью прозрачными (только Windows).
MAGIC = "#ff00fe"

BAR_BG = "#1c1c22"
BAR_FG = "#9aa0aa"
BAR_HOT = "#ffffff"
PANEL_BG = "#0e0f13"

# Режимы фона
MODE_PANEL = "panel"    # сплошная панель, вся затемняется прозрачностью
MODE_MARKER = "marker"  # фон насквозь, под буквами — плашка маркера
MODE_TEXT = "text"      # фон насквозь, только буквы
MODES = [MODE_PANEL, MODE_MARKER, MODE_TEXT]
MODE_NAMES = {MODE_PANEL: "Панель", MODE_MARKER: "Маркер", MODE_TEXT: "Текст"}

# (название, цвет текста, цвет плашки-маркера)
PALETTES = [
    ("Белый",   "#ffffff", "#000000"),
    ("Чёрный",  "#000000", "#ffffff"),
    ("Лайм",    "#b6ff3c", "#0a1400"),
    ("Жёлтый",  "#ffd93d", "#1a1400"),
    ("Циан",    "#6ee7ff", "#00131a"),
    ("Розовый", "#ff7ad9", "#1a0014"),
]

FONT_MIN, FONT_MAX = 8, 72
FONT_PRESETS = [12, 16, 20, 28, 36, 48, 72]

DEFAULTS = {
    "alpha": 80,
    "mode": MODE_MARKER,
    "palette": 0,
    "font_size": 16,
    "topmost": True,
    "geometry": "560x360+120+120",
    "compact": False,
}


class TransparentNotepad(object):
    def __init__(self):
        self.cfg = self._load_cfg()
        self.clickthrough = False
        self._drag = None
        self._resize = None
        self._retag_job = None
        self._hk_state = {}

        self.root = tk.Tk()
        self.root.title("Glasspad")
        self.root.overrideredirect(True)
        self.root.geometry(self.cfg["geometry"])
        self.root.minsize(220, 120)
        self.root.attributes("-topmost", bool(self.cfg["topmost"]))

        self._build_bar()
        self._build_text()
        self._build_menu()
        self._bind_keys()

        self._load_note()
        self.apply_mode()
        self.apply_palette()
        self.apply_alpha(self.cfg["alpha"])
        if self.cfg["compact"]:
            self.cfg["compact"] = False
            self.toggle_compact()

        self.root.protocol("WM_DELETE_WINDOW", self.quit)
        # Окно без рамки в Windows не всегда само забирает фокус клавиатуры.
        self.root.after(120, self.root.focus_force)
        if IS_WIN:
            self.root.after(150, self._poll_hotkeys)
        self.root.after(2000, self._autosave)

    # ---------------------------------------------------------------- UI

    def _build_bar(self):
        self.bar = tk.Frame(self.root, bg=BAR_BG, height=30)
        self.bar.pack(side="top", fill="x")
        self.bar.pack_propagate(False)

        grip = tk.Label(self.bar, text="⣿", bg=BAR_BG, fg="#4a4f59",
                        font=("Segoe UI", 10), padx=8)
        grip.pack(side="left")

        self.scale = tk.Scale(
            self.bar, from_=0, to=100, orient="horizontal", showvalue=0,
            length=110, width=7, sliderlength=14, bd=0, highlightthickness=0,
            bg=BAR_BG, fg=BAR_FG, troughcolor="#2b2d36", activebackground="#ffffff",
        )
        self.scale.set(self.cfg["alpha"])
        self.scale.config(command=self._on_scale)  # только после set(), иначе ранний вызов
        self.scale.pack(side="left", pady=6)

        self.pct = tk.Label(self.bar, text="%d%%" % self.cfg["alpha"], bg=BAR_BG,
                            fg=BAR_FG, font=("Segoe UI", 9), width=5, anchor="w", padx=2)
        self.pct.pack(side="left")

        self.tip_lbl = tk.Label(self.bar, text="", bg=BAR_BG, fg="#6b7280",
                                font=("Segoe UI", 8), anchor="w", padx=4)
        self.tip_lbl.pack(side="left", fill="x", expand=True)

        for w in (self.bar, grip, self.pct, self.tip_lbl):
            w.bind("<ButtonPress-1>", self._drag_start)
            w.bind("<B1-Motion>", self._drag_move)

        self._btn("✕", self.quit, "Закрыть  (Ctrl+Q)")
        self.corner = self._btn("◢", None, "Потянуть — изменить размер")
        self.corner.bind("<ButtonPress-1>", self._resize_start)
        self.corner.bind("<B1-Motion>", self._resize_move)
        self.btn_top = self._btn("📌", self.toggle_topmost, "Поверх всех окон  (Ctrl+T)")
        self.btn_click = self._btn("🖱", self.toggle_clickthrough,
                                   "Сквозной клик  (Ctrl+Alt+E)")
        self.btn_mode = self._btn("Маркер", self.cycle_mode, "Режим фона  (Ctrl+M)")
        self.btn_pal = self._btn("Aa", self.toggle_bw,
                                 "ЛКМ — белые/чёрные буквы, ПКМ — другие цвета",
                                 hover=False)
        self.btn_pal.bind("<Button-3>", lambda e: self.cycle_palette())

        self._btn("+", lambda: self.font_step(1), "Крупнее  (Ctrl+=)")
        self.size_lbl = self._btn(str(self.cfg["font_size"]), self.font_preset,
                                  "Размер 8-72: клик — пресет, колесо — плавно")
        self.size_lbl.config(width=2, font=("Segoe UI", 9))
        self.size_lbl.bind("<MouseWheel>", self._font_wheel)
        self.size_lbl.bind("<Button-4>", lambda e: self.font_step(1))
        self.size_lbl.bind("<Button-5>", lambda e: self.font_step(-1))
        self._btn("−", lambda: self.font_step(-1), "Мельче  (Ctrl+-)")

    def _btn(self, text, cmd, tip="", hover=True):
        b = tk.Label(self.bar, text=text, bg=BAR_BG, fg=BAR_FG,
                     font=("Segoe UI", 10), padx=7, cursor="hand2")
        b.pack(side="right")
        if hover:
            b.bind("<Enter>", lambda e: b.config(fg=BAR_HOT))
            b.bind("<Leave>", lambda e: b.config(fg=BAR_FG))
        if cmd:
            b.bind("<Button-1>", lambda e: cmd())
        if tip:
            b.bind("<Enter>", lambda e, t=tip: self._tip(t), add="+")
            b.bind("<Leave>", lambda e: self._tip(""), add="+")
        return b

    def _tip(self, text):
        self.tip_lbl.config(text=text)

    def _build_text(self):
        self.text = tk.Text(
            self.root, wrap="word", undo=True, bd=0, highlightthickness=0,
            padx=10, pady=8, spacing1=1, spacing3=2,
            font=("Consolas", self.cfg["font_size"]),
        )
        self.text.pack(fill="both", expand=True)
        self.text.bind("<<Modified>>", self._on_modified)
        self.text.focus_set()

    def _build_menu(self):
        m = tk.Menu(self.root, tearoff=0)
        m.add_command(label="Режим фона        Ctrl+M", command=self.cycle_mode)
        m.add_command(label="Цвет              Ctrl+P", command=self.cycle_palette)
        m.add_command(label="Поверх окон       Ctrl+T", command=self.toggle_topmost)
        m.add_command(label="Сквозной клик     Ctrl+Alt+E", command=self.toggle_clickthrough)
        m.add_command(label="Скрыть панель     Ctrl+H", command=self.toggle_compact)
        m.add_separator()
        m.add_command(label="Открыть…          Ctrl+O", command=self.open_file)
        m.add_command(label="Сохранить как…    Ctrl+S", command=self.save_file)
        m.add_separator()
        m.add_command(label="Выход             Ctrl+Q", command=self.quit)
        self.menu = m
        self.text.bind("<Button-3>", self._popup)
        self.bar.bind("<Button-3>", self._popup)

    def _popup(self, event):
        try:
            self.menu.tk_popup(event.x_root, event.y_root)
        finally:
            self.menu.grab_release()
        return "break"

    def _bind_keys(self):
        keys = {
            "<Control-q>": self.quit,
            "<Control-m>": self.cycle_mode,
            "<Control-p>": self.cycle_palette,
            "<Control-t>": self.toggle_topmost,
            "<Control-h>": self.toggle_compact,
            "<Control-o>": self.open_file,
            "<Control-s>": self.save_file,
            "<Control-Up>": lambda: self.bump_alpha(5),
            "<Control-Down>": lambda: self.bump_alpha(-5),
            "<Control-plus>": lambda: self.font_step(1),
            "<Control-equal>": lambda: self.font_step(1),
            "<Control-minus>": lambda: self.font_step(-1),
            "<Control-Alt-e>": self.toggle_clickthrough,
        }
        # Бинд на самом Text с "break" — иначе сработают ещё и штатные
        # бинды класса Text (Ctrl+O — перевод строки, Ctrl+H — backspace и т.д.).
        for seq, fn in keys.items():
            for widget in (self.text, self.root):
                widget.bind(seq, lambda e, f=fn: (f(), "break")[1])

    # ------------------------------------------------------- прозрачность

    def _on_scale(self, value):
        self.apply_alpha(int(float(value)))

    def apply_alpha(self, value):
        value = max(0, min(100, int(value)))
        self.cfg["alpha"] = value
        self.root.attributes("-alpha", value / 100.0)
        self.pct.config(text="%d%%" % value)

    def bump_alpha(self, delta):
        self.scale.set(max(0, min(100, self.cfg["alpha"] + delta)))

    # ------------------------------------------------------------ режимы

    def apply_mode(self):
        want = self.cfg["mode"] in (MODE_MARKER, MODE_TEXT)
        see_through = want and self._set_transparent_color(True)
        if not see_through:
            self._set_transparent_color(False)

        bg = MAGIC if see_through else PANEL_BG
        self.root.configure(bg=bg)
        self.text.configure(bg=bg)
        self.btn_mode.config(text=MODE_NAMES[self.cfg["mode"]])
        self._retag()

    def _set_transparent_color(self, on):
        """Прозрачный фон умеет только Windows. Возвращает True, если получилось."""
        try:
            self.root.attributes("-transparentcolor", MAGIC if on else "")
            return on
        except tk.TclError:
            return False

    def cycle_mode(self):
        self.cfg["mode"] = MODES[(MODES.index(self.cfg["mode"]) + 1) % len(MODES)]
        self.apply_mode()

    def apply_palette(self):
        _, fg, hl = PALETTES[self.cfg["palette"] % len(PALETTES)]
        self.text.configure(fg=fg, insertbackground=fg,
                            selectbackground=fg, selectforeground=hl)
        self.text.tag_configure("marker", background=hl)
        self.btn_pal.config(fg=fg, bg=hl)  # мини-образец: буквы на маркере
        self._retag()

    def cycle_palette(self):
        self.cfg["palette"] = (self.cfg["palette"] + 1) % len(PALETTES)
        self.apply_palette()

    def toggle_bw(self):
        """Белые буквы на чёрном маркере <-> чёрные на белом."""
        self.cfg["palette"] = 1 if self.cfg["palette"] != 1 else 0
        self.apply_palette()

    def apply_font(self):
        size = self.cfg["font_size"]
        self.text.configure(font=("Consolas", size))
        self.size_lbl.config(text=str(size))

    def font_step(self, delta):
        step = delta * (1 if self.cfg["font_size"] < 24 else 2)
        self.cfg["font_size"] = max(FONT_MIN, min(FONT_MAX, self.cfg["font_size"] + step))
        self.apply_font()

    def font_preset(self):
        size = self.cfg["font_size"]
        nxt = next((p for p in FONT_PRESETS if p > size), FONT_PRESETS[0])
        self.cfg["font_size"] = nxt
        self.apply_font()

    def _font_wheel(self, event):
        self.font_step(1 if event.delta > 0 else -1)

    def toggle_topmost(self):
        self.cfg["topmost"] = not self.cfg["topmost"]
        self.root.attributes("-topmost", self.cfg["topmost"])
        self.btn_top.config(fg=BAR_HOT if self.cfg["topmost"] else "#5a5f69")

    def toggle_compact(self):
        self.cfg["compact"] = not self.cfg["compact"]
        if self.cfg["compact"]:
            self.bar.pack_forget()
        else:
            self.bar.pack(side="top", fill="x", before=self.text)

    # ------------------------------------------------- плашка под буквами

    def _on_modified(self, _event=None):
        if self.text.edit_modified():
            self.text.edit_modified(False)
            if self._retag_job:
                self.root.after_cancel(self._retag_job)
            self._retag_job = self.root.after(40, self._retag)

    def _retag(self):
        self._retag_job = None
        self.text.tag_remove("marker", "1.0", "end")
        if self.cfg["mode"] == MODE_MARKER:
            self.text.tag_add("marker", "1.0", "end-1c")

    # --------------------------------------------------- перетаскивание

    def _drag_start(self, event):
        self._drag = (event.x_root - self.root.winfo_x(),
                      event.y_root - self.root.winfo_y())

    def _drag_move(self, event):
        if self._drag:
            self.root.geometry("+%d+%d" % (event.x_root - self._drag[0],
                                           event.y_root - self._drag[1]))

    def _resize_start(self, event):
        self._resize = (event.x_root, event.y_root,
                        self.root.winfo_width(), self.root.winfo_height())

    def _resize_move(self, event):
        if not self._resize:
            return
        x0, y0, w0, h0 = self._resize
        w = max(220, w0 + (event.x_root - x0))
        h = max(120, h0 + (event.y_root - y0))
        self.root.geometry("%dx%d" % (w, h))

    # ------------------------------------------------------ сквозной клик

    def _hwnd(self):
        return ctypes.windll.user32.GetParent(self.root.winfo_id()) or self.root.winfo_id()

    def toggle_clickthrough(self):
        if not IS_WIN:
            messagebox.showinfo("Glasspad",
                                "Сквозной клик работает только в Windows.")
            return
        self.clickthrough = not self.clickthrough
        WS_EX_LAYERED, WS_EX_TRANSPARENT, GWL_EXSTYLE = 0x80000, 0x20, -20
        u = ctypes.windll.user32
        get_ = getattr(u, "GetWindowLongPtrW", u.GetWindowLongW)
        set_ = getattr(u, "SetWindowLongPtrW", u.SetWindowLongW)
        hwnd = self._hwnd()
        style = get_(hwnd, GWL_EXSTYLE)
        if self.clickthrough:
            style |= WS_EX_LAYERED | WS_EX_TRANSPARENT
        else:
            style &= ~WS_EX_TRANSPARENT
        set_(hwnd, GWL_EXSTYLE, style)
        self.btn_click.config(fg="#ff6b6b" if self.clickthrough else BAR_FG)

    # ------------------------------------- глобальные хоткеи (Windows)

    def _pressed(self, vk):
        return bool(ctypes.windll.user32.GetAsyncKeyState(vk) & 0x8000)

    def _edge(self, name, down):
        was = self._hk_state.get(name, False)
        self._hk_state[name] = down
        return down and not was

    def _poll_hotkeys(self):
        try:
            ctrl, alt = self._pressed(0x11), self._pressed(0x12)
            if ctrl and alt:
                if self._pressed(0x26):
                    self.bump_alpha(5)
                elif self._pressed(0x28):
                    self.bump_alpha(-5)
                if self._edge("e", self._pressed(0x45)):
                    self.toggle_clickthrough()
                if self._edge("h", self._pressed(0x48)):
                    self.toggle_compact()
            else:
                self._hk_state.clear()
        except Exception:
            pass
        self.root.after(120, self._poll_hotkeys)

    # -------------------------------------------------------- файлы

    def open_file(self):
        path = filedialog.askopenfilename(
            filetypes=[("Текст", "*.txt"), ("Все файлы", "*.*")])
        if path:
            with open(path, "r", encoding="utf-8", errors="replace") as f:
                data = f.read()
            self.text.delete("1.0", "end")
            self.text.insert("1.0", data)
            self._retag()

    def save_file(self):
        path = filedialog.asksaveasfilename(
            defaultextension=".txt",
            filetypes=[("Текст", "*.txt"), ("Все файлы", "*.*")])
        if path:
            with open(path, "w", encoding="utf-8") as f:
                f.write(self.text.get("1.0", "end-1c"))

    def _load_note(self):
        if os.path.exists(NOTE_PATH):
            try:
                with open(NOTE_PATH, "r", encoding="utf-8", errors="replace") as f:
                    self.text.insert("1.0", f.read())
            except OSError:
                pass
        self.text.edit_modified(False)

    def _autosave(self):
        self._save_note()
        self.root.after(5000, self._autosave)

    def _save_note(self):
        data = self.text.get("1.0", "end-1c")
        if data == getattr(self, "_saved", None):
            return
        try:
            os.makedirs(APP_DIR, exist_ok=True)
            with open(NOTE_PATH, "w", encoding="utf-8") as f:
                f.write(data)
            self._saved = data
        except OSError:
            pass

    def _load_cfg(self):
        cfg = dict(DEFAULTS)
        try:
            with open(CFG_PATH, "r", encoding="utf-8") as f:
                saved = json.load(f)
            for k in DEFAULTS:
                if k in saved:
                    cfg[k] = saved[k]
        except (OSError, ValueError):
            pass
        try:
            cfg["alpha"] = max(0, min(100, int(cfg["alpha"])))
            cfg["font_size"] = max(FONT_MIN, min(FONT_MAX, int(cfg["font_size"])))
            cfg["palette"] = int(cfg["palette"]) % len(PALETTES)
        except (TypeError, ValueError):
            cfg["alpha"], cfg["font_size"], cfg["palette"] = 80, 16, 0
        if cfg["mode"] not in MODES:
            cfg["mode"] = MODE_MARKER
        return cfg

    def _save_cfg(self):
        try:
            os.makedirs(APP_DIR, exist_ok=True)
            self.cfg["geometry"] = "%dx%d+%d+%d" % (
                self.root.winfo_width(), self.root.winfo_height(),
                self.root.winfo_x(), self.root.winfo_y())
            with open(CFG_PATH, "w", encoding="utf-8") as f:
                json.dump(self.cfg, f, ensure_ascii=False, indent=2)
        except OSError:
            pass

    def quit(self):
        self._save_note()
        self._save_cfg()
        self.root.destroy()

    def run(self):
        self.root.mainloop()


if __name__ == "__main__":
    TransparentNotepad().run()
