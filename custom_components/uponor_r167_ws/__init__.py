"""Uponor R-167 (WebSocket) integration.

Talks to uhomed, a replacement daemon running on the Uponor R-167
gateway, which pushes room and system state over a WebSocket.
"""

from __future__ import annotations

from homeassistant.config_entries import ConfigEntry
from homeassistant.const import CONF_HOST, CONF_PORT, Platform
from homeassistant.core import HomeAssistant, callback
from homeassistant.exceptions import ConfigEntryNotReady
from homeassistant.helpers import device_registry as dr
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.dispatcher import async_dispatcher_send

from .client import UponorWsClient, UponorWsError
from .const import DOMAIN, SIGNAL_NEW_ROOM, SIGNAL_UPDATE

PLATFORMS = [Platform.CLIMATE, Platform.SENSOR, Platform.BINARY_SENSOR]


async def async_setup_entry(hass: HomeAssistant, entry: ConfigEntry) -> bool:
    host, port = entry.data[CONF_HOST], entry.data[CONF_PORT]
    client = UponorWsClient(async_get_clientsession(hass), host, port)
    try:
        await client.start()
    except UponorWsError as err:
        raise ConfigEntryNotReady(str(err)) from err

    @callback
    def _on_event(kind: str, data) -> None:
        if kind == "new_room":
            async_dispatcher_send(hass, SIGNAL_NEW_ROOM.format(entry.entry_id), [data])
        elif kind == "snapshot" and data:
            async_dispatcher_send(hass, SIGNAL_NEW_ROOM.format(entry.entry_id), data)
        async_dispatcher_send(hass, SIGNAL_UPDATE.format(entry.entry_id))

    entry.async_on_unload(client.add_listener(_on_event))

    gateway = dr.async_get(hass).async_get_or_create(
        config_entry_id=entry.entry_id,
        identifiers={(DOMAIN, f"{host}:{port}")},
        name="Uponor R-167",
        manufacturer="Uponor",
        model="R-167 / U@home (uhomed)",
        sw_version=client.status.get("version"),
        configuration_url=f"http://{host}:{port}/",
    )

    hass.data.setdefault(DOMAIN, {})[entry.entry_id] = {
        "client": client,
        "gateway_device_id": gateway.id,
    }
    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    return True


async def async_unload_entry(hass: HomeAssistant, entry: ConfigEntry) -> bool:
    unloaded = await hass.config_entries.async_unload_platforms(entry, PLATFORMS)
    if unloaded:
        data = hass.data[DOMAIN].pop(entry.entry_id)
        await data["client"].stop()
    return unloaded
