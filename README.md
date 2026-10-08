# Uponor R-167 (WebSocket)

Replacement software for the **Uponor R-167 (U@home)** gateway in a Smatrix
Wave PLUS system, plus a Home Assistant integration that talks to it.

The original R-167 software occasionally stores one room's temperature under
a neighbouring room. `uhomed` replaces it: it listens to the radio traffic
between the X-165 controller and the I-167 interface, asks the I-167 for each
room's record, and only accepts values when the room is positively identified.
Changes are pushed to Home Assistant over a WebSocket – no polling.

```
X-165 ⇄ I-167 ⇄ (868 MHz) ⇄ R-167 running uhomed ── WebSocket :8765 ── Home Assistant
```

> **Status: 0.3.0.** Temperatures, setpoints, limits, heating demand and
> bypass are read, and setpoints can be changed. Alarms are not supported yet. While uhomed runs, the
> original Uponor web UI and app are not available – switch back to the
> original mode at any time.

## Requirements

- Root SSH access to the R-167 (see the uponor-r167 project for how to get it).
- Home Assistant 2026.9 or newer.

## Install on the R-167

```bash
ssh root@<r167> 'cat > /mnt/UserFS/uhomed' < r167/uhomed
ssh root@<r167> 'cat > /mnt/UserFS/uhome-mode.sh' < r167/uhome-mode.sh
ssh root@<r167> 'chmod +x /mnt/UserFS/uhomed /mnt/UserFS/uhome-mode.sh; /mnt/UserFS/uhome-mode.sh install'
```

In custom mode the script also stops Uponor's cloud VPN, software update
and FTP server (and keeps them from starting at boot); `original` brings
them back.

`install` adds uhomed to monit (a backup of `/etc/monitrc` is kept as
`/etc/monitrc.orig`) and leaves the gateway in **original** mode.

Switch modes:

```bash
ssh root@<r167> /mnt/UserFS/uhome-mode.sh custom    # run uhomed
ssh root@<r167> /mnt/UserFS/uhome-mode.sh original  # back to Uponor's software
ssh root@<r167> /mnt/UserFS/uhome-mode.sh status
```

In custom mode, open `http://<r167>:8765/` for a live overview.
uhomed logs to `/tmp/uhomed.log`.

### Keep the R-167 linked to the I-167

uhomed gets all room data from the I-167 interface, which polls the R-167
over the radio. **Do not remove the R-167 (U@home) from the I-167** – if
you do, the I-167 stops polling and no rooms show up (the radio still
looks OK, because the X-165's own broadcasts keep arriving).

To link it again, use the original software – the R-167's link button
and RF-link LED are handled by Uponor's `platform`, not by uhomed:

1. `uhome-mode.sh original`
2. Hold the small button on the R-167 for a few seconds until the RF-link
   LED lights up, then add the device from the I-167.
3. `uhome-mode.sh custom`

## Install the Home Assistant integration

Copy `custom_components/uponor_r167_ws` to your Home Assistant
`config/custom_components/` folder (or add this repository to HACS as a
custom repository), restart Home Assistant, then add **Uponor R-167
(WebSocket)** under *Settings → Devices & services*. Default port: 8765.

Rooms appear as uhomed learns them – usually all within two minutes after
uhomed starts.

## WebSocket protocol

Connect to `ws://<r167>:8765/ws`. The server sends JSON messages
`{"type": ..., "data": ...}`:

| type | data |
|---|---|
| `snapshot` | `{status, system, device, rooms[]}` – sent on connect and on request |
| `room` | one room, sent when any of its values change |
| `system` | `{outdoor_temperature, average_temperature}` |
| `status` | `{version, radio_ok, last_frame, frames, records, rejected_data_frames}` – when the radio comes or goes |
| `stats` | same as `status`, every 10 s while frames arrive (for the web page's counters) |
| `device` | `{temperature}` – the R-167's processor temperature in °C, when it changes (read every minute) |

A room: `{"id": "4a", "channel": 18, "name": "K-E-V", "temperature": 22.2,
"setpoint": 25.0, "min": 15.0, "max": 25.0, "bitmask": "0406", "heating": true, "bypass": false,
"radio_alarm": false, "battery_alarm": false, "technical_alarm": false,
"registers": {"3d": "0041", "3e": "0000", "3f": "0400"}, "last_update": "..."}`.
`id` is the room's controller address and is stable. `radio_alarm` is true
when the controller has lost contact with the thermostat (I-167: "Term. saknas",
raised about an hour after the thermostat went silent).

Register bits, from the register map in Uponor's own gateway software
(`VT_REGMAP` in `platform`); ✓ = confirmed on a live system:

| Register | Mask | Meaning |
|---|---|---|
| 35 | `0001` | bypass ✓ |
| 35 | `0800` | remote control of the thermostat allowed |
| 35 | `8000` | cooling allowed ✓ |
| 35 | `0008` | ECO commanded for the room (set by the I-167 on Away) ✓ |
| 3C | (word) | ECO offset in 0.1 °F ("ECO justering"; `0024` = 2.0 °C) ✓ |
| 3D | `0008` | room is running ECO ✓ |
| 3D | `0010` | home/away (forced ECO) |
| 3D | `0040` | room in demand (heating) ✓ |
| 3D | `0080` | RH limit reached |
| 3D | `0100` | floor limit reached |
| 3E | `0003` | technical alarm |
| 3E | `0010` | tamper (T-163 only) |
| 3E | `0020` | radio alarm ✓ |
| 3E | `0040` | battery alarm |
| 3F | `0007` | thermostat type (0 analog, 1 public, 2 digital, 3 digital programmable) |
| 3F | `0008` | thermostat switch on Comfort/ECO (ECO allowed) ✓ |
| 3F | `0300` | regulation mode |
| 3F | `0400` | cooling allowed, as reported back (cleared in every room when cooling was disabled on the I-167) ✓ |

Not used by Uponor's software: 3D `0001` and `0200` (`0200` follows an
active alarm), 3E `8000` (appears after a thermostat restart), 3F `0800`
(while a thermostat starts up).

uhomed logs frames it does not fully decode (unknown frame types, name
frames, the system frame and record) whenever their content changes, as
`watch ...` lines in `/tmp/uhomed.log` (at most 30 per minute). This is how
new commands are found: toggle something on the I-167 and look at the log.

Client commands:

- `{"type": "get_snapshot"}`
- `{"type": "set_setpoint", "id": 1, "room": "4a", "value": 24.5}` – answered
  with `{"type": "result", "data": {"id": 1, "success": true}}` when the
  controller confirms the new value, or `success: false` and an `error`.

## Building uhomed

```bash
cd daemon
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -ldflags="-s -w" -o ../r167/uhomed .
```
