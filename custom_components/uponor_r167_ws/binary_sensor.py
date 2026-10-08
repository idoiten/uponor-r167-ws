"""Binary sensors: radio connectivity, per-room bypass and radio alarm."""

from __future__ import annotations

from homeassistant.components.binary_sensor import BinarySensorDeviceClass, BinarySensorEntity
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import EntityCategory
from homeassistant.core import HomeAssistant
from homeassistant.helpers.device_registry import DeviceInfo

from .const import DOMAIN
from .entity import UponorWsEntity, UponorWsRoomEntity, setup_room_platform


async def async_setup_entry(hass: HomeAssistant, entry: ConfigEntry, async_add_entities) -> None:
    data = hass.data[DOMAIN][entry.entry_id]
    client = data["client"]
    async_add_entities([UponorWsRadioSensor(entry, client)])
    setup_room_platform(
        hass,
        entry,
        async_add_entities,
        lambda room_id: [
            UponorWsBypassSensor(entry, client, room_id, data["gateway_device_id"]),
            UponorWsRadioAlarmSensor(entry, client, room_id, data["gateway_device_id"]),
        ],
    )


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


class UponorWsBypassSensor(UponorWsRoomEntity, BinarySensorEntity):
    """On when bypass is enabled for the room (set on the I-167)."""

    _attr_translation_key = "bypass"
    _unavailable_on_radio_alarm = False  # a setting, not a measurement

    def __init__(self, entry, client, room_id, gateway_device_id) -> None:
        super().__init__(entry, client, room_id, gateway_device_id)
        self._attr_unique_id = f"{DOMAIN}_{room_id}_bypass"

    @property
    def is_on(self) -> bool | None:
        return (self.room or {}).get("bypass")


class UponorWsRadioAlarmSensor(UponorWsRoomEntity, BinarySensorEntity):
    """On when the controller has lost contact with the room's thermostat.

    Raised by the system about an hour after the thermostat went silent
    (dead batteries, removed from the wall); the I-167 shows "Term. saknas".
    """

    _attr_device_class = BinarySensorDeviceClass.PROBLEM
    _attr_entity_category = EntityCategory.DIAGNOSTIC
    _attr_translation_key = "radio_alarm"
    _unavailable_on_radio_alarm = False  # this sensor reports the alarm

    def __init__(self, entry, client, room_id, gateway_device_id) -> None:
        super().__init__(entry, client, room_id, gateway_device_id)
        self._attr_unique_id = f"{DOMAIN}_{room_id}_radio_alarm"

    @property
    def is_on(self) -> bool | None:
        return (self.room or {}).get("radio_alarm")
