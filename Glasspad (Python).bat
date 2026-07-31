@echo off
rem Запуск питоновской версии без чёрного окна консоли.
cd /d "%~dp0"
start "" pythonw glasspad.py
if errorlevel 1 start "" python glasspad.py
