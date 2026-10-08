"""Temperature sensors: one per room, the system-wide ones and the gateway itself."""

from __future__ import annotations

from homeassistant.components.sensor import SensorDeviceClass, SensorEntity, SensorStateClass
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import EntityCategory, UnitOfTemperature
from homeassistant.core import HomeAssistant
from homeassistant.helpers.device_registry import DeviceInfo

from .const import DOMAIN
from .entity import UponorWsEntity, UponorWsRoomEntity, setup_room_platform


async def async_setup_entry(hass: HomeAssistant, entry: ConfigEntry, async_add_entities) -> None:
    data = hass.data[DOMAIN][entry.entry_id]
    client = data["client"]
    async_add_entities(
        [
            UponorWsSystemSensor(entry, client, "outdoor_temperature"),
            UponorWsSystemSensor(entry, client, "average_temperature"),
            UponorWsDeviceTemperature(entry, client),
        ]
    )
    setup_room_platform(
        hass,
        entry,
        async_add_entities,
        lambda room_id: [UponorWsRoomTemperature(entry, client, room_id, data["gateway_device_id"])],
    )


class _TemperatureSensor(SensorEntity):
    _attr_device_class = SensorDeviceClass.TEMPERATURE
    _attr_native_unit_of_measurement = UnitOfTemperature.CELSIUS
    _attr_state_class = SensorStateClass.MEASUREMENT
    _attr_suggested_display_precision = 1


class UponorWsSystemSensor(UponorWsEntity, _TemperatureSensor):
    """Outdoor or average indoor temperature."""

    def __init__(self, entry: ConfigEntry, client, key: str) -> None:
        super().__init__(entry, client)
        self._key = key
        self._attr_translation_key = key
        self._attr_unique_id = f"{DOMAIN}_{key}"
        host, port = entry.data["host"], entry.data["port"]
        self._attr_device_info = DeviceInfo(identifiers={(DOMAIN, f"{host}:{port}")})

    @property
    def native_value(self) -> float | None:
        return self._client.system.get(self._key)

    @property
    def available(self) -> bool:
        return self._client.connected and self.native_value is not None


class UponorWsDeviceTemperature(UponorWsEntity, _TemperatureSensor):
    """Temperature of the R-167's processor (its internal sensor)."""

    _attr_entity_category = EntityCategory.DIAGNOSTIC
    _attr_translation_key = "device_temperature"

    def __init__(self, entry: ConfigEntry, client) -> None:
        super().__init__(entry, client)
        self._attr_unique_id = f"{DOMAIN}_device_temperature"
        host, port = entry.data["host"], entry.data["port"]
        self._attr_device_info = DeviceInfo(identifiers={(DOMAIN, f"{host}:{port}")})

    @property
    def native_value(self) -> float | None:
        return self._client.device.get("temperature")

    @property
    def available(self) -> bool:
        return self._client.connected and self.native_value is not None


class UponorWsRoomTemperature(UponorWsRoomEntity, _TemperatureSensor):
    """A room's measured temperature."""

    _attr_translation_key = "temperature"

    def __init__(self, entry, client, room_id, gateway_device_id) -> None:
        super().__init__(entry, client, room_id, gateway_device_id)
        self._attr_unique_id = f"{DOMAIN}_{room_id}_temperature"

    @property
    def native_value(self) -> float | None:
        return self.room["temperature"] if self.room else None
