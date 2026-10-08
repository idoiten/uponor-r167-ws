# Changelog

## [0.11.2] - 2026-10-08

### Fixed
- Switching ECO mode again while the same change was still waiting for the
  I-167 replaced it and restarted the 2-minute timeout, so a slow change
  never produced an error. The waiting change is now kept and its timeout
  counts from the first request; every request gets the same result. The
  timeout also applies when no acknowledgements are being sent.

### Added
- While an ECO mode change waits for the I-167, uhomed logs every 20 s how
  many name frames arrived and how many acknowledgements carried the flag,
  to find out why the I-167 sometimes takes minutes to pick a change up.
  The confirmation line now says how long it took.

## [0.11.1] - 2026-10-08

### Fixed
- While a room runs ECO, Home Assistant and the web page showed the comfort
  setpoint (e.g. 25 °C) instead of the lowered one the I-167 shows and the
  room uses (25 − ECO offset 2 = 23 °C). The thermostat card now shows
  setpoint − ECO offset in ECO; changing it there writes the matching
  comfort setpoint (23 → 24 writes 25). Min and max stay as set on the
  I-167, so in ECO the setpoint can be raised to at most max − ECO offset.
  The web page marks it "Setpoint (ECO)".

### Added
- Room field `eco_offset` (°C, register 3C) over the WebSocket, and the
  attributes `comfort_temperature` and `eco_offset` on the thermostats.

## [0.11.0] - 2026-10-08

### Added
- **ECO mode can be switched from Home Assistant.** The *ECO-läge* switch
  on the Uponor R-167 device now sets Away (on) or Home (off) on the I-167,
  using the same exchange as Uponor's original software (captured on a
  live system): uhomed flags a system change, the I-167 asks for it, and
  uhomed answers with the I-167's own system registers with the ECO bit
  changed. The switch waits until the I-167 reports the new state (usually
  within about 15 s); otherwise HA shows an error after 2 minutes.
- WebSocket command `{"type": "set_eco_mode", "id": 1, "value": true}`.

### Removed
- The experimental `POST /debug/probe-system` from 0.10.0, no longer needed.

## [0.10.0] - 2026-10-08

### Added
- **ECO mode** (Home/Away on the I-167) for the whole system, read from the
  I-167's own system frame (`FF 17`, bit `0x0800` of its first register;
  confirmed by toggling Away/Home). Shown within about 15 s: in HA as the
  switch **ECO mode** (*ECO-läge*) on the Uponor R-167 device – switching
  it from HA is not supported yet – and as an "ECO mode" badge on the web
  page. uhomed logs when it changes.
- The thermostats' comfort/eco preset and the web page's ECO badge now
  follow ECO mode and the thermostat switch directly (eco = ECO mode on and
  switch on Comfort/ECO), instead of waiting minutes for the room's own
  status to refresh.
- Experimental, read only: `POST /debug/probe-system` flags a pending
  system change for 60 s and logs what the I-167 asks for (nothing is
  answered).

### Changed
- The I-167's system frame is logged on change without its running clock,
  so the watch log is not flooded every 8 s.

## [0.9.0] - 2026-10-08

### Removed
- The experimental `POST /debug/eco` from 0.8.0. Tested on a live system:
  the I-167 does not accept the ECO bit per room. ECO is a whole-system
  setting (Home/Away on the I-167).

### Changed
- The thermostats' comfort/eco preset is display only; selecting it in HA
  explains that it follows ECO mode for the whole system.

### Added
- Logging of frames uhomed does not fully decode – unknown frame types,
  name frames, the system frame and the system record – whenever their
  content changes (`watch ...` lines, at most 30 per minute). Used to find
  the I-167's Home/Away (ECO mode) command so it can be offered as an
  "ECO mode" switch in HA.

## [0.8.1] - 2026-10-08

### Fixed
- **Setpoint writes reset the room's ECO offset.** The write sent to the
  I-167 carries, after the setpoint, the room's ECO offset (register 3C),
  which uhomed sent as zero (as in the original capture, where the offset
  was zero). A setpoint change from HA or the web page therefore set
  "ECO justering" to 0 for that room. uhomed now sends the offset the room
  has, and refuses to write before it has read it (shortly after a
  restart: "room settings not received yet").
- Register 3C confirmed as the ECO offset in 0.1 °F (`0024` = 2.0 °C).

## [0.8.0] - 2026-10-08

### Added
- **ECO status per room.** 3D `0x0008` is set while the room runs ECO
  (the system is on Away on the I-167 and the thermostat's switch is on
  Comfort/ECO); 3F `0x0008` is the switch position (ECO allowed); 35
  `0x0008` is the I-167's ECO command for the room. Rooms carry
  `eco_active` and `eco_allowed`. In HA the thermostats get a preset mode,
  **comfort** or **eco**, read-only for now, and an `eco_allowed`
  attribute. The web page shows an "ECO" badge.
- Register 3C (believed to be the ECO offset, "ECO justering") is exposed
  raw as `register_3c` until its encoding is confirmed.
- Experimental `POST /debug/eco?room=<id>&on=1|0`: asks the I-167 to put a
  room in or out of ECO by sending its current setpoint with the ECO bit
  changed in the settings block – the same write the I-167 accepts for
  setpoints. For testing whether ECO can be controlled from HA.

## [0.7.1] - 2026-10-08

### Changed
- Web page: rooms are sorted alphabetically by name (they were sorted by
  channel), and re-sorted if a room is renamed on the I-167.
- Web page: the raw register values (35/3D/3E/3F) are no longer shown on
  the room cards. They are still available as `register_*` attributes on
  the climate entities in HA.
- README: 3F `0x0400` is "cooling allowed" as reported back by the system
  (it cleared in every room when cooling was disabled on the I-167).

## [0.7.0] - 2026-10-08

### Added
- **Battery and technical alarms per room**, with bit positions taken from
  the register map in Uponor's own gateway software: 3E `0x0040` battery
  alarm, 3E `0x0003` technical alarm (radio alarm `0x0020` from 0.6.0 is
  confirmed by the same map). Rooms carry `battery_alarm` and
  `technical_alarm`; uhomed logs when they are raised and cleared. In HA
  each room gets the diagnostic binary sensors **Battery alarm**
  (*Batterilarm*, device class battery) and **Technical alarm**
  (*Tekniskt larm*, device class problem). Unlike the radio alarm they do
  not make the room unavailable.
- **Register 35**, the room settings set on the I-167 (bypass, remote
  control allowed, cooling allowed), is exposed with the other raw
  registers (`register_35` on the climate entity, and on the web page).
  Changes are logged.
- Web page: each room shows a row of status dots – Radio, Battery,
  Technical – green when OK, red on alarm, grey until the room has been
  heard; hover for details. Replaces the "Radio alarm" badge.
- README: full register bit map.

## [0.6.0] - 2026-10-08

### Added
- **Radio alarm per room.** Register 3E bit `0x0020` is set when the
  controller has lost contact with a thermostat (the I-167 shows
  "Term. saknas", about an hour after the thermostat went silent) and
  clears as soon as it is heard again. Rooms carry a new `radio_alarm`
  field; uhomed logs when it is raised and cleared. In HA each room gets a
  diagnostic binary sensor **Radio alarm** (*Radiolarm*, device class
  problem), and the room's thermostat and temperature sensor turn
  unavailable while the alarm is active instead of showing stale values.
  The web page shows a red "Radio alarm" badge.
- README: register bits mapped so far (3D `0040` heating demand, 3D `0200`
  active alarm, 3E `0020` radio alarm, 3F `0800` thermostat just started).

### Fixed
- Setpoints outside the room's min/max are ignored (and logged). While the
  I-167 restarts it briefly reported 30 °C for a room with a 25 °C maximum,
  which reached HA.
- Setpoint writes: the I-167 sometimes needs well over 10 s to pass a
  change on, and flagging the write again restarted its work, so a write
  could time out although it went through. uhomed now resends after 30 s
  instead of 10 s, waits up to 2 minutes instead of 1, and recognises a
  confirmation arriving shortly after the timeout as its own write instead
  of logging it as a change made on the system.

## [0.5.0] - 2026-10-08

### Added
- **Device temperature.** uhomed reads the R-167's processor temperature
  (the NXP Vybrid's internal sensor, `/sys/bus/iio/devices/iio:device0/in_temp_input`)
  every minute, averaging five samples, and pushes a new `device` message
  when the value changes. Shown in the web page footer and as the
  diagnostic sensor **Device temperature** (*Enhetstemperatur*) on the
  Uponor R-167 device in HA. The sensor is uncalibrated, so treat the value
  as a trend rather than an exact reading. New flag `-temp-sensor` (empty
  disables it).

## [0.4.3] - 2026-10-02

### Changed
- Temperatures (rooms, outdoor, average indoor) are now truncated to one
  decimal instead of rounded, like the I-167 display does: 73.0 °F
  (22.78 °C) is shown as 22.7, not 22.8. HA and the web page now match the
  I-167 and uponor-x165. The conversion uses integer arithmetic, so float
  error cannot move a value across a tenth. Setpoints and limits are
  unaffected (half degrees are exact in Uponor's 0.1 °F encoding).

## [0.4.2] - 2026-10-02

### Fixed
- Integration: when uhomed restarts, its first snapshot has no rooms
  until it has relearned them over the radio. The thermostats now keep
  their last values meanwhile instead of all turning unavailable.

### Docs
- README: the R-167 must stay linked (U@home) in the I-167, and how to
  link it again (original mode, hold the link button until the RF-link
  LED lights, add it from the I-167, back to custom mode).

## [0.4.1] - 2026-10-02

### Changed
- The web page footer (uhomed version, frames, records, rejected data
  frames) now updates live. uhomed pushes a new `stats` message with the
  counters every 10 s while radio traffic arrives. The HA integration
  ignores it, so the radio sensor's attributes are not rewritten (and
  recorded) every 10 s.

## [0.4.0] - 2026-10-02

### Added
- **Raw controller registers per room**, to map the remaining bits (the
  alarms in particular) by provoking them. Rooms now carry
  `registers: {"3d": ..., "3e": ..., "3f": ...}` – 3D status (heating
  demand, limits, ECO), 3E alarms (technical, tamper, RF, battery, RH
  sensor), 3F thermostat type / regulation mode – decoded from the data
  frames and records. Every change is logged, e.g.
  `alarm register (3e) for WC changed 0000 -> 0020`, and a non-zero alarm
  register is logged at start-up. Shown on the web page and as
  `register_3d` / `register_3e` / `register_3f` attributes on each
  climate entity.

## [0.3.1] - 2026-10-02

### Changed
- `uhome-mode.sh custom` now also stops Uponor's cloud VPN (`openvpn`),
  software update (`softwareupdate`) and FTP server (`vsftpd`), and
  keeps openvpn and vsftpd from starting again at boot: their boot
  scripts are replaced by small wrappers that skip `start` while
  `/mnt/UserFS/.uhome-custom` exists. The originals are kept as
  `off.S60openvpn` / `off.S70vsftpd`, so monit's configuration stays
  valid. They do nothing useful without the original software,
  and an outbound VPN to the vendor plus an unencrypted FTP server are
  needless openings into the home network. `original` restores the boot
  scripts and starts the services again.

## [0.3.0] - 2026-10-02

### Added
- **Bypass per room.** Decoded from bit 0x01 of the room header (byte 16)
  and of the room record (byte 12); it matches the rooms with bypass
  enabled on the I-167. Shown as a binary sensor per room in Home
  Assistant, as `bypass` in the WebSocket data and as a badge on the web
  page.

### Removed
- The per-room "Heating demand" binary sensor added in 0.2.0. The same
  information is the climate entity's action (heating/idle). Existing
  heating demand entities are removed from the entity registry
  automatically when the integration loads.

## [0.2.1] - 2026-10-02

### Added
- **Change setpoints from the web page.** Each room has − / + buttons
  (0.5 °C steps, within the room's limits). The change is sent 1.5 s after
  the last click and the page shows when the controller has confirmed it.
- **Log line for setpoint changes made elsewhere** (I-167, thermostat or
  the original app), e.g. `setpoint for K-E-V changed from 24.5 to 22.0
  (changed on the system, e.g. I-167 or thermostat)`. Changes made through
  uhomed are logged as queued/sent/confirmed as before and not repeated.

### Changed
- The web page shows when a room was last updated as relative time
  ("updated 5 seconds ago", "updated 3 minutes ago").
- Rooms are pushed to clients at least once a minute even when nothing
  changed, so "last update" reflects when data last arrived.

## [0.2.0] - 2026-10-02

### Added
- **Changing setpoints.** uhomed implements the same exchange the original
  firmware uses: it flags the room in its next acknowledgement, the I-167
  asks for the change, uhomed answers with the room's settings and the new
  setpoint, and the controller's room header confirms it. The frame uhomed
  sends is byte-for-byte identical to one captured from the original
  firmware. WebSocket command:
  `{"type": "set_setpoint", "id": 1, "room": "4a", "value": 24.5}`, answered
  with `{"type": "result", "data": {"id": 1, "success": true}}` once the
  controller confirms (or with an error after 60 s).
- **Heating demand ("room in demand")** per room, decoded from the data
  frames and records. Shown on the web page, as the climate entity's
  action (heating/idle) and as a new binary sensor per room.

### Changed
- The climate entity's target temperature can now be changed. Values are
  rounded to 0.5 °C and must be within the room's limits.

## [0.1.1] - 2026-10-02

### Fixed
- `uhome-mode.sh` collided with monit ("Other action already in progress"),
  so `custom` never started uhomed and fell back to the original mode. It
  now issues one monit action per service, retries while monit is busy and
  waits for the process to actually start or stop.

### Changed
- uhomed now requests records for rooms it has not heard from yet before
  refreshing known rooms, so all rooms appear sooner after start-up
  (previously a room could wait for a full round-robin cycle).

## [0.1.0] - 2026-10-02

First version (read-only).

### Added
- **uhomed**, a replacement daemon for the Uponor R-167 gateway. It talks to
  the R-167's internal 868 MHz radio modem directly, acknowledges the I-167
  interface's frames like the original software, requests every room's
  record (which carries the room's address) and feeds the hardware watchdog.
- Room data is only accepted when the room is positively identified: by
  address (records and room headers) or by the room's output bitmask (data
  frames). This removes the original firmware's habit of storing one room's
  temperature under a neighbouring room.
- WebSocket (`/ws`), JSON (`/state`) and a small live web page on port 8765.
- `uhome-mode.sh` to switch the R-167 between the original Uponor software
  and uhomed; the chosen mode survives reboots.
- Home Assistant integration `uponor_r167_ws` (local push): one climate
  entity and one temperature sensor per room, outdoor and average indoor
  temperature, and a radio connectivity sensor. English and Swedish.

### Not yet supported
- Changing setpoints (the climate entity is read-only for now).
- Alarms (battery, radio, tamper, technical) and "room in demand".
