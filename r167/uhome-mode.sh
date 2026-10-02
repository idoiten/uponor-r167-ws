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

wait_gone() {
	for i in 1 2 3 4 5 6 7 8 9 10; do running "$1" || return 0; sleep 0.5; done
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
	monit unmonitor uhomed
	echo "installed (mode: original). Use '$0 custom' to switch."
	;;
custom)
	monit unmonitor platform
	monit unmonitor KickWatchdog
	killall platform 2>/dev/null
	killall KickWatchdog 2>/dev/null
	wait_gone KickWatchdog || echo "warning: KickWatchdog still running"
	wait_gone /mnt/UserFS/platform || echo "warning: platform still running"
	# The hardware watchdog keeps counting (60 s) until uhomed opens it.
	monit monitor uhomed
	monit start uhomed
	sleep 5
	if running "$UHOMED"; then
		echo "mode: custom (uhomed on port 8765)"
	else
		echo "uhomed did not start - switching back to original"
		"$0" original
		exit 1
	fi
	;;
original)
	monit unmonitor uhomed
	killall uhomed 2>/dev/null   # disarms the watchdog on exit
	wait_gone "$UHOMED" || echo "warning: uhomed still running"
	monit monitor KickWatchdog
	monit start KickWatchdog
	sleep 3
	monit monitor platform
	monit start platform
	echo "mode: original (Uponor platform)"
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
