"""Climate entities (one per room)."""

from __future__ import annotations

from homeassistant.components.climate import ClimateEntity, ClimateEntityFeature, HVACMode
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import UnitOfTemperature
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import HomeAssistantError

from .const import DOMAIN
from .entity import UponorWsRoomEntity, setup_room_platform


async def async_setup_entry(hass: HomeAssistant, entry: ConfigEntry, async_add_entities) -> None:
    data = hass.data[DOMAIN][entry.entry_id]
    setup_room_platform(
        hass,
        entry,
        async_add_entities,
        lambda room_id: [UponorWsClimate(entry, data["client"], room_id, data["gateway_device_id"])],
    )


class UponorWsClimate(UponorWsRoomEntity, ClimateEntity):
    """A room's thermostat."""

    _attr_name = None  # primary entity: use the room (device) name
    _attr_temperature_unit = UnitOfTemperature.CELSIUS
    _attr_target_temperature_step = 0.5
    _attr_hvac_modes = [HVACMode.HEAT]
    _attr_hvac_mode = HVACMode.HEAT
    _attr_supported_features = ClimateEntityFeature.TARGET_TEMPERATURE

    def __init__(self, entry, client, room_id, gateway_device_id) -> None:
        super().__init__(entry, client, room_id, gateway_device_id)
        self._attr_unique_id = f"{DOMAIN}_{room_id}"

    @property
    def current_temperature(self) -> float | None:
        return self.room["temperature"] if self.room else None

    @property
    def target_temperature(self) -> float | None:
        return self.room["setpoint"] if self.room else None

    @property
    def min_temp(self) -> float:
        return (self.room or {}).get("min") or 5.0

    @property
    def max_temp(self) -> float:
        return (self.room or {}).get("max") or 35.0

    async def async_set_temperature(self, **kwargs) -> None:
        raise HomeAssistantError(
            "Changing the setpoint is not supported yet by uponor_r167_ws (read-only release)"
        )

    async def async_set_hvac_mode(self, hvac_mode: HVACMode) -> None:
        """Only heating is supported; nothing to change."""
