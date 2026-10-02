# Changelog

## [0.3.1] - 2026-10-02

### Changed
- `uhome-mode.sh custom` now also stops Uponor's cloud VPN (`openvpn`),
  software update (`softwareupdate`) and FTP server (`vsftpd`), and
  disables the boot scripts that would start openvpn and vsftpd again
  after a reboot. They do nothing useful without the original software,
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
