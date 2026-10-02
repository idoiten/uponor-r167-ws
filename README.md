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

> **Status: 0.2.0.** Temperatures, setpoints, limits and heating demand are
> read, and setpoints can be changed. Alarms are not supported yet. While uhomed runs, the
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
| `snapshot` | `{status, system, rooms[]}` – sent on connect and on request |
| `room` | one room, sent when any of its values change |
| `system` | `{outdoor_temperature, average_temperature}` |
| `status` | `{version, radio_ok, last_frame, frames, records, rejected_data_frames}` |

A room: `{"id": "4a", "channel": 18, "name": "K-E-V", "temperature": 22.2,
"setpoint": 25.0, "min": 15.0, "max": 25.0, "bitmask": "0406", "heating": true, "last_update": "..."}`.
`id` is the room's controller address and is stable.

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
