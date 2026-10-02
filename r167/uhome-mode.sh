#!/bin/sh
# Switch the Uponor R-167 between the original Uponor software and uhomed.
#
#   uhome-mode.sh install   add uhomed to monit (stays in original mode)
#   uhome-mode.sh custom    stop platform + KickWatchdog, run uhomed
#                           (also stops Uponor's cloud VPN, software update
#                           and FTP server, which are useless without platform)
#   uhome-mode.sh original  stop uhomed, run platform + KickWatchdog
#                           and the Uponor services again
#   uhome-mode.sh status    show which mode is active
#
# The chosen mode survives reboots (monit remembers what it monitors).

UHOMED=/mnt/UserFS/uhomed
MONITRC=/etc/monitrc
# Uponor services that only make sense with the original software.
# openvpn and vsftpd are also started at boot by /etc/init.d, so their
# boot scripts are wrapped (and restored) as well.
EXTRA_SERVICES="openvpn softwareupdate vsftpd"
BOOT_SCRIPTS="S60openvpn S70vsftpd"

# Flag file: when present, the wrapped boot scripts do not start their
# service. The original scripts are kept as off.<name> and a small
# wrapper takes their place, so monit's configuration still finds the
# programs it refers to.
FLAG=/mnt/UserFS/.uhome-custom

disable_boot_scripts() {
	touch "$FLAG"
	for s in $BOOT_SCRIPTS; do
		f=/etc/init.d/$s
		[ -f /etc/init.d/off.$s ] || mv "$f" /etc/init.d/off.$s
		cat > "$f" <<EOF
#!/bin/sh
# uhome-mode.sh wrapper: original script is /etc/init.d/off.$s
if [ "\$1" = start ] && [ -f $FLAG ]; then
	echo "$s: disabled while uhomed runs (uhome-mode.sh custom)"
	exit 0
fi
exec /etc/init.d/off.$s "\$@"
EOF
		chmod 755 "$f"
	done
}

enable_boot_scripts() {
	rm -f "$FLAG"
	for s in $BOOT_SCRIPTS; do
		[ -f /etc/init.d/off.$s ] && mv /etc/init.d/off.$s /etc/init.d/$s
	done
}

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
	for s in $EXTRA_SERVICES; do monit_do stop $s; done
	# monit runs the stop scripts asynchronously; let them finish before
	# the boot scripts are renamed
	wait_gone openvpn || killall openvpn 2>/dev/null
	wait_gone vsftpd || killall vsftpd 2>/dev/null
	wait_gone /mnt/UserFS/softwareupdate || killall softwareupdate 2>/dev/null
	disable_boot_scripts
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
	enable_boot_scripts
	for s in $EXTRA_SERVICES; do monit_do start $s; done
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
	monit summary | grep -E "platform|KickWatchdog|uhomed|openvpn|softwareupdate|vsftpd"
	;;
*)
	echo "usage: $0 install|custom|original|status"
	exit 1
	;;
esac
