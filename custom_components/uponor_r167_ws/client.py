"""WebSocket client for uhomed running on the Uponor R-167."""

from __future__ import annotations

import asyncio
from collections.abc import Callable
import json
import logging
from typing import Any

import aiohttp

_LOGGER = logging.getLogger(__name__)

RECONNECT_MIN = 2  # seconds
RECONNECT_MAX = 60  # seconds


class UponorWsError(Exception):
    """Could not talk to uhomed."""


class UponorWsClient:
    """Keeps a live copy of uhomed's state, pushed over a WebSocket."""

    def __init__(self, session: aiohttp.ClientSession, host: str, port: int) -> None:
        self._session = session
        self.url = f"http://{host}:{port}/ws"
        self.rooms: dict[str, dict[str, Any]] = {}
        self.system: dict[str, Any] = {}
        self.status: dict[str, Any] = {}
        self.connected = False
        self._task: asyncio.Task | None = None
        self._first_snapshot = asyncio.Event()
        self._listeners: list[Callable[[str, Any], None]] = []
        self._ws: aiohttp.ClientWebSocketResponse | None = None
        self._next_id = 0
        self._results: dict[int, asyncio.Future] = {}

    def add_listener(self, listener: Callable[[str, Any], None]) -> Callable[[], None]:
        """Register a callback(kind, data); returns a function that removes it."""
        self._listeners.append(listener)
        return lambda: self._listeners.remove(listener)

    def _notify(self, kind: str, data: Any) -> None:
        for listener in list(self._listeners):
            try:
                listener(kind, data)
            except Exception:  # noqa: BLE001
                _LOGGER.exception("Uponor R-167 WS: listener failed")

    @property
    def radio_ok(self) -> bool:
        return self.connected and bool(self.status.get("radio_ok"))

    async def start(self, timeout: float = 15) -> None:
        """Start the connection loop and wait for the first snapshot."""
        self._task = asyncio.create_task(self._run())
        try:
            await asyncio.wait_for(self._first_snapshot.wait(), timeout)
        except asyncio.TimeoutError as err:
            await self.stop()
            raise UponorWsError(f"No snapshot from {self.url} within {timeout} s") from err

    async def stop(self) -> None:
        if self._task:
            self._task.cancel()
            try:
                await self._task
            except (asyncio.CancelledError, Exception):  # noqa: BLE001
                pass
            self._task = None
        self.connected = False

    async def _run(self) -> None:
        delay = RECONNECT_MIN
        while True:
            try:
                async with self._session.ws_connect(self.url, heartbeat=30, timeout=10) as ws:
                    _LOGGER.info("Uponor R-167 WS: connected to %s", self.url)
                    self._ws = ws
                    delay = RECONNECT_MIN
                    async for msg in ws:
                        if msg.type == aiohttp.WSMsgType.TEXT:
                            self._handle(msg.data)
                        elif msg.type in (aiohttp.WSMsgType.CLOSED, aiohttp.WSMsgType.ERROR):
                            break
            except asyncio.CancelledError:
                raise
            except (aiohttp.ClientError, asyncio.TimeoutError, OSError) as err:
                _LOGGER.debug("Uponor R-167 WS: connection failed: %s", err)
            self._ws = None
            for fut in self._results.values():
                if not fut.done():
                    fut.set_exception(UponorWsError("connection lost"))
            self._results.clear()
            if self.connected:
                _LOGGER.warning("Uponor R-167 WS: lost connection to %s, reconnecting", self.url)
                self.connected = False
                self._notify("connection", False)
            await asyncio.sleep(delay)
            delay = min(delay * 2, RECONNECT_MAX)

    def _handle(self, raw: str) -> None:
        try:
            msg = json.loads(raw)
        except ValueError:
            _LOGGER.debug("Uponor R-167 WS: invalid message %s", raw)
            return
        kind, data = msg.get("type"), msg.get("data")
        if kind == "snapshot":
            self.status = data.get("status", {})
            self.system = data.get("system", {})
            new = {r["id"]: r for r in data.get("rooms", [])}
            added = [rid for rid in new if rid not in self.rooms]
            # Merge instead of replace: right after uhomed restarts its
            # snapshot is empty until it has relearned the rooms over the
            # radio, and the entities should keep their last values
            # meanwhile instead of all turning unavailable.
            self.rooms.update(new)
            self.connected = True
            self._first_snapshot.set()
            self._notify("snapshot", added)
        elif kind == "room":
            is_new = data["id"] not in self.rooms
            self.rooms[data["id"]] = data
            self._notify("new_room" if is_new else "room", data["id"])
        elif kind == "system":
            self.system = data
            self._notify("system", data)
        elif kind == "status":
            self.status = data
            self._notify("status", data)
        elif kind == "result":
            fut = self._results.pop(data.get("id"), None)
            if fut and not fut.done():
                if data.get("success"):
                    fut.set_result(None)
                else:
                    fut.set_exception(UponorWsError(data.get("error") or "command failed"))
        elif kind == "error":
            _LOGGER.warning("Uponor R-167 WS: device reported: %s", data)


    async def set_setpoint(self, room_id: str, value: float, timeout: float = 75) -> None:
        """Change a room's setpoint and wait until the controller confirms it."""
        await self._command({"type": "set_setpoint", "room": room_id, "value": value}, timeout)

    async def _command(self, payload: dict[str, Any], timeout: float) -> None:
        if self._ws is None or self._ws.closed:
            raise UponorWsError("not connected to uhomed")
        self._next_id += 1
        cmd_id = self._next_id
        fut = asyncio.get_running_loop().create_future()
        self._results[cmd_id] = fut
        try:
            await self._ws.send_str(json.dumps({**payload, "id": cmd_id}))
            await asyncio.wait_for(fut, timeout)
        except asyncio.TimeoutError as err:
            raise UponorWsError("no answer from uhomed") from err
        finally:
            self._results.pop(cmd_id, None)


async def async_probe(session: aiohttp.ClientSession, host: str, port: int) -> dict[str, Any]:
    """Connect once and return the first snapshot (used by the config flow)."""
    url = f"http://{host}:{port}/ws"
    try:
        async with session.ws_connect(url, timeout=10) as ws:
            msg = await ws.receive(timeout=10)
            if msg.type != aiohttp.WSMsgType.TEXT:
                raise UponorWsError("unexpected message")
            data = json.loads(msg.data)
            if data.get("type") != "snapshot":
                raise UponorWsError("no snapshot received")
            return data["data"]
    except (aiohttp.ClientError, asyncio.TimeoutError, OSError, ValueError) as err:
        raise UponorWsError(str(err)) from err
