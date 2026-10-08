"""Shared entity helpers."""

from __future__ import annotations

from collections.abc import Callable
from typing import Any

from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant, callback
from homeassistant.helpers.device_registry import DeviceInfo
from homeassistant.helpers.dispatcher import async_dispatcher_connect
from homeassistant.helpers.entity import Entity

from .client import UponorWsClient
from .const import DOMAIN, SIGNAL_NEW_ROOM, SIGNAL_UPDATE


class UponorWsEntity(Entity):
    """Base class: push-updated, no polling."""

    _attr_should_poll = False
    _attr_has_entity_name = True

    def __init__(self, entry: ConfigEntry, client: UponorWsClient) -> None:
        self._entry = entry
        self._client = client

    async def async_added_to_hass(self) -> None:
        self.async_on_remove(
            async_dispatcher_connect(
                self.hass, SIGNAL_UPDATE.format(self._entry.entry_id), self.async_write_ha_state
            )
        )

    @property
    def available(self) -> bool:
        return self._client.connected


class UponorWsRoomEntity(UponorWsEntity):
    """An entity belonging to one room (thermostat channel)."""

    # Measured values go stale when the controller has lost the thermostat
    # (radio alarm), so such entities report unavailable meanwhile.
    _unavailable_on_radio_alarm = True

    def __init__(
        self, entry: ConfigEntry, client: UponorWsClient, room_id: str, gateway_device_id: str
    ) -> None:
        super().__init__(entry, client)
        self._room_id = room_id
        room = client.rooms[room_id]
        self._attr_device_info = DeviceInfo(
            identifiers={(DOMAIN, f"room_{room_id}")},
            name=room["name"],
            manufacturer="Uponor",
            model="Thermostat (Smatrix Wave)",
            via_device_id=gateway_device_id,
        )

    @property
    def room(self) -> dict[str, Any] | None:
        return self._client.rooms.get(self._room_id)

    @property
    def available(self) -> bool:
        room = self.room
        if not self._client.connected or room is None:
            return False
        return not (self._unavailable_on_radio_alarm and room.get("radio_alarm"))


def setup_room_platform(
    hass: HomeAssistant,
    entry: ConfigEntry,
    async_add_entities,
    factory: Callable[[str], list[Entity]],
) -> None:
    """Add entities for all known rooms, and for rooms that appear later."""
    client: UponorWsClient = hass.data[DOMAIN][entry.entry_id]["client"]
    known: set[str] = set()

    @callback
    def _add(room_ids) -> None:
        entities: list[Entity] = []
        for room_id in room_ids:
            if room_id not in known and room_id in client.rooms:
                known.add(room_id)
                entities.extend(factory(room_id))
        if entities:
            async_add_entities(entities)

    _add(list(client.rooms))
    entry.async_on_unload(
        async_dispatcher_connect(hass, SIGNAL_NEW_ROOM.format(entry.entry_id), _add)
    )
