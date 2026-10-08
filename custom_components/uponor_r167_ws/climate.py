"""Climate entities (one per room)."""

from __future__ import annotations

from homeassistant.components.climate import (
    ATTR_TEMPERATURE,
    PRESET_COMFORT,
    PRESET_ECO,
    ClimateEntity,
    ClimateEntityFeature,
    HVACAction,
    HVACMode,
)
from homeassistant.config_entries import ConfigEntry
from homeassistant.const import UnitOfTemperature
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import HomeAssistantError

from .client import UponorWsError
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
    _attr_supported_features = ClimateEntityFeature.TARGET_TEMPERATURE | ClimateEntityFeature.PRESET_MODE
    _attr_preset_modes = [PRESET_COMFORT, PRESET_ECO]

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

    @property
    def preset_mode(self) -> str | None:
        eco = (self.room or {}).get("eco_active")
        if eco is None:
            return None
        return PRESET_ECO if eco else PRESET_COMFORT

    async def async_set_preset_mode(self, preset_mode: str) -> None:
        # Read-only for now: ECO follows Home/Away on the I-167 and the
        # Comfort/ECO switch on the thermostat. Writing it is being tested.
        raise HomeAssistantError(
            "ECO is set with Home/Away on the I-167 (and the switch on the thermostat); "
            "setting it from Home Assistant is not supported yet"
        )

    @property
    def extra_state_attributes(self):
        room = self.room or {}
        regs = room.get("registers") or {}
        attrs = {f"register_{k}": v for k, v in regs.items()}
        attrs["eco_allowed"] = room.get("eco_allowed")
        return attrs

    @property
    def hvac_action(self) -> HVACAction | None:
        heating = (self.room or {}).get("heating")
        if heating is None:
            return None
        return HVACAction.HEATING if heating else HVACAction.IDLE

    async def async_set_temperature(self, **kwargs) -> None:
        value = kwargs.get(ATTR_TEMPERATURE)
        if value is None:
            return
        try:
            await self._client.set_setpoint(self._room_id, float(value))
        except UponorWsError as err:
            raise HomeAssistantError(f"Could not change the setpoint: {err}") from err

    async def async_set_hvac_mode(self, hvac_mode: HVACMode) -> None:
        """Only heating is supported; nothing to change."""
