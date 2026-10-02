"""Connectivity sensor for the gateway's radio link."""

from __future__ import annotations

from homeassistant.components.binary_sensor import BinarySensorDeviceClass, BinarySensorEntity
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import EntityCategory
from homeassistant.core import HomeAssistant
from homeassistant.helpers.device_registry import DeviceInfo

from .const import DOMAIN
from .entity import UponorWsEntity


async def async_setup_entry(hass: HomeAssistant, entry: ConfigEntry, async_add_entities) -> None:
    client = hass.data[DOMAIN][entry.entry_id]["client"]
    async_add_entities([UponorWsRadioSensor(entry, client)])


class UponorWsRadioSensor(UponorWsEntity, BinarySensorEntity):
    """On when uhomed is reachable and hears radio traffic."""

    _attr_device_class = BinarySensorDeviceClass.CONNECTIVITY
    _attr_entity_category = EntityCategory.DIAGNOSTIC
    _attr_translation_key = "radio"

    def __init__(self, entry: ConfigEntry, client) -> None:
        super().__init__(entry, client)
        self._attr_unique_id = f"{DOMAIN}_radio"
        host, port = entry.data["host"], entry.data["port"]
        self._attr_device_info = DeviceInfo(identifiers={(DOMAIN, f"{host}:{port}")})

    @property
    def available(self) -> bool:
        return True  # this sensor itself reports the connection state

    @property
    def is_on(self) -> bool:
        return self._client.radio_ok

    @property
    def extra_state_attributes(self):
        s = self._client.status
        return {
            "websocket_connected": self._client.connected,
            "uhomed_version": s.get("version"),
            "last_frame": s.get("last_frame"),
            "frames": s.get("frames"),
            "records": s.get("records"),
            "rejected_data_frames": s.get("rejected_data_frames"),
        }
