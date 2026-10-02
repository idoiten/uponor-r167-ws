# Changelog

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
