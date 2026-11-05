#!/bin/sh
# ARTEX daemon start script (Linux / macOS / Docker ENTRYPOINT)
#
# Usage:
#   ./start.sh                       Run in the foreground (Ctrl-C to stop)
#   nohup ./start.sh >artex.log 2>&1 &   Run persistently in the background
#   ./start.sh -addr :9000           Extra arguments are passed through to artex
#
# This script does one thing: it runs artex and, when the process exits, looks at the exit code to decide whether to relaunch it.
#
#   0      the user stopped it normally  -> exit the loop
#   75     the program requested a restart  -> relaunch immediately (the user clicked "One-click update" or "Rollback" in the UI)
#   other  abnormal exit          -> relaunch after a backoff (1->2->4... up to 60 seconds)
#
# Downloading, SHA256 checksum verification and version replacement are deliberately not done here. That logic would have to be written twice, in sh and in bat,
# and it is exactly the part that must never be wrong. Once the binary is replaced with one that cannot run, this script would faithfully
# keep relaunching it, and the user would have to log in to the machine and recover manually. So verification and replacement all live in Go (the selfupdate package),
# where artex handles them itself at startup, and the script stays simple.
set -u

cd "$(dirname "$0")" || exit 1

BIN=./artex
[ -x "$BIN" ] || { echo "[artex] Executable not found: $BIN" >&2; exit 1; }

RESTART_CODE=75
MAX_DELAY=60

child=0
stopping=0

# Forward the stop signal to the artex process itself.
#
# This is required under Docker. docker stop sends SIGTERM only to PID 1 (this script)
# and not to child processes. If it is not forwarded, artex never receives the signal and cannot shut down gracefully;
# after 10 seconds it is killed with SIGKILL and any running work is cut off midway.
forward() {
	stopping=1
	if [ "$child" -ne 0 ]; then
		kill -TERM "$child" 2>/dev/null || true
	fi
}
trap forward INT TERM

delay=1
while :; do
	"$BIN" "$@" &
	child=$!

	# A signal interrupts wait and makes it return a value greater than 128. The child is still shutting down gracefully at that point,
	# so wait once more to get the real exit code.
	wait "$child"
	code=$?
	if [ "$code" -gt 128 ]; then
		wait "$child"
		code=$?
	fi
	child=0

	if [ "$stopping" -eq 1 ]; then
		echo "[artex] Stopped"
		exit 0
	fi

	case "$code" in
		0)
			echo "[artex] Exited normally"
			exit 0
			;;
		"$RESTART_CODE")
			# An update/rollback is ready. On relaunch artex completes the version swap at startup (see selfupdate.Bootstrap).
			echo "[artex] Restart requested (applying new version)..."
			delay=1
			;;
		*)
			echo "[artex] Abnormal exit (code=$code), restarting in ${delay}s" >&2
			sleep "$delay"
			delay=$((delay * 2))
			[ "$delay" -gt "$MAX_DELAY" ] && delay=$MAX_DELAY
			;;
	esac
done
