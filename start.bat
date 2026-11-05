@echo off
rem Switch the console to UTF-8 so that non-ASCII output is not garbled in GBK terminals.
chcp 65001 >nul 2>&1
rem ARTEX daemon start script (Windows)
rem
rem Usage:
rem   start.bat                  Run in the foreground (Ctrl-C to stop)
rem   start.bat -addr :9000      Extra arguments are passed through to artex
rem
rem This script does one thing: it runs artex.exe and, when the process exits, looks at the exit code to decide whether to relaunch it.
rem
rem   0      the user stopped it normally  -> exit the loop
rem   75     the program requested a restart -> relaunch immediately (the user clicked "One-click update" or "Rollback" in the UI)
rem   other  abnormal exit       -> relaunch after a backoff (1->2->4... up to 60 seconds)
rem
rem Downloading, SHA256 checksum verification and version replacement are not done here; artex handles all of it itself at startup
rem (selfupdate package). The script stays simple. See the comment at the top of start.sh for details.

setlocal enabledelayedexpansion
cd /d "%~dp0"

set "BIN=artex.exe"
if not exist "%BIN%" (
	echo [artex] Executable not found: %BIN% 1>&2
	exit /b 1
)

set "RESTART_CODE=75"
set "MAX_DELAY=60"
set /a delay=1

:loop
"%BIN%" %*
set "code=!ERRORLEVEL!"

if "!code!"=="0" (
	echo [artex] Exited normally
	exit /b 0
)

if "!code!"=="%RESTART_CODE%" (
	rem An update/rollback is ready. On relaunch artex completes the version swap at startup.
	echo [artex] Restart requested: applying new version...
	set /a delay=1
	goto loop
)

echo [artex] Abnormal exit ^(code=!code!^), restarting in !delay!s 1>&2
rem timeout fails in a redirected console, so ping is used instead (a delay of N seconds needs N+1 pings).
set /a pings=!delay!+1
ping -n !pings! 127.0.0.1 >nul 2>&1
set /a delay=!delay!*2
if !delay! gtr %MAX_DELAY% set /a delay=%MAX_DELAY%
goto loop
