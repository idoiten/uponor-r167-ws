#!/bin/sh
# Switch the Uponor R-167 between the original Uponor software and uhomed.
#
#   uhome-mode.sh install   add uhomed to monit (stays in original mode)
#   uhome-mode.sh custom    stop platform + KickWatchdog, run uhomed
#   uhome-mode.sh original  stop uhomed, run platform + KickWatchdog
#   uhome-mode.sh status    show which mode is active
#
# The chosen mode survives reboots (monit remembers what it monitors).

UHOMED=/mnt/UserFS/uhomed
MONITRC=/etc/monitrc

running() { ps | grep -v grep | grep -q "$1"; }

# monit only runs one action at a time; retry while it is busy.
monit_do() {
	for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
		out=$(monit "$1" "$2" 2>&1)
		case "$out" in
		*"in progress"*) sleep 2 ;;
		*) return 0 ;;
		esac
	done
	echo "monit $1 $2: still busy, giving up"
	return 1
}

wait_gone() {
	i=0
	while [ $i -lt 30 ]; do running "$1" || return 0; sleep 1; i=$((i + 1)); done
	return 1
}

wait_running() {
	i=0
	while [ $i -lt 30 ]; do running "$1" && return 0; sleep 1; i=$((i + 1)); done
	return 1
}

case "$1" in
install)
	[ -x "$UHOMED" ] || chmod +x "$UHOMED" || { echo "missing $UHOMED"; exit 1; }
	[ -f "$MONITRC.orig" ] || cp -p "$MONITRC" "$MONITRC.orig"
	if ! grep -q "check process uhomed" "$MONITRC"; then
		cat >> "$MONITRC" <<'EOF'
 check process uhomed
  matching "/mnt/UserFS/uhomed"
  start program = "/bin/sh -c '/mnt/UserFS/uhomed >/dev/null 2>&1 &'"
  stop program = "/usr/bin/killall uhomed"
EOF
	fi
	monit -t || { echo "monitrc invalid, restoring backup"; cp -p "$MONITRC.orig" "$MONITRC"; exit 1; }
	monit reload
	sleep 5
	monit_do stop uhomed
	echo "installed (mode: original). Use '$0 custom' to switch."
	;;
custom)
	monit_do stop platform
	monit_do stop KickWatchdog
	wait_gone /mnt/UserFS/platform || { killall platform 2>/dev/null; wait_gone /mnt/UserFS/platform; }
	wait_gone KickWatchdog || { killall KickWatchdog 2>/dev/null; wait_gone KickWatchdog; }
	# The hardware watchdog keeps counting (60 s) until uhomed opens it.
	monit_do start uhomed
	if wait_running "$UHOMED"; then
		echo "mode: custom (uhomed on port 8765)"
	else
		echo "uhomed did not start - switching back to original"
		tail -5 /tmp/uhomed.log 2>/dev/null
		"$0" original
		exit 1
	fi
	;;
original)
	monit_do stop uhomed
	wait_gone "$UHOMED" || { killall uhomed 2>/dev/null; wait_gone "$UHOMED"; }
	monit_do start KickWatchdog
	wait_running KickWatchdog || echo "warning: KickWatchdog not running"
	monit_do start platform
	if wait_running /mnt/UserFS/platform; then
		echo "mode: original (Uponor platform)"
	else
		echo "warning: platform not running yet - check 'monit summary'"
	fi
	;;
status)
	if running "$UHOMED"; then echo "mode: custom (uhomed running)"
	elif running /mnt/UserFS/platform; then echo "mode: original (platform running)"
	else echo "mode: unknown - neither uhomed nor platform is running"; fi
	monit summary | grep -E "platform|KickWatchdog|uhomed"
	;;
*)
	echo "usage: $0 install|custom|original|status"
	exit 1
	;;
esac
