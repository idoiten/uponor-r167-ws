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

    def _eco_offset(self) -> float:
        """How much the room's setpoint is lowered right now (0 on comfort).

        The controller's setpoint is the comfort setpoint; while the room
        runs ECO the I-167 shows (and the room uses) setpoint − ECO offset,
        so that is what is shown and set here too.
        """
        if self.preset_mode != PRESET_ECO:
            return 0.0
        return (self.room or {}).get("eco_offset") or 0.0

    @property
    def target_temperature(self) -> float | None:
        sp = (self.room or {}).get("setpoint")
        return None if sp is None else round(sp - self._eco_offset(), 1)

    @property
    def min_temp(self) -> float:
        return (self.room or {}).get("min") or 5.0

    @property
    def max_temp(self) -> float:
        return (self.room or {}).get("max") or 35.0

    @property
    def preset_mode(self) -> str | None:
        # The room runs ECO when the system is in ECO mode (Away on the
        # I-167, known within seconds) and the thermostat's switch allows
        # it. The room's own ECO status (3D 0x0008) only refreshes when the
        # I-167 polls the room, which can take minutes; it is the fallback.
        room = self.room or {}
        mode = self._client.system.get("eco_mode")
        allowed = room.get("eco_allowed")
        if mode is not None and allowed is not None:
            eco = mode and allowed
        else:
            eco = room.get("eco_active")
        if eco is None:
            return None
        return PRESET_ECO if eco else PRESET_COMFORT

    async def async_set_preset_mode(self, preset_mode: str) -> None:
        # Display only: a room runs ECO when the whole system is in ECO mode
        # (Home/Away on the I-167) and the thermostat's switch allows it.
        # The I-167 does not accept ECO per room.
        raise HomeAssistantError(
            "The mode follows ECO mode for the whole system (Home/Away on the I-167) "
            "and cannot be set per room"
        )

    @property
    def extra_state_attributes(self):
        room = self.room or {}
        regs = room.get("registers") or {}
        attrs = {f"register_{k}": v for k, v in regs.items()}
        attrs["eco_allowed"] = room.get("eco_allowed")
        attrs["comfort_temperature"] = room.get("setpoint")
        attrs["eco_offset"] = room.get("eco_offset")
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
        # in ECO the shown value is comfort − offset; write the comfort setpoint
        offset = self._eco_offset()
        comfort = round(float(value) + offset, 1)
        if offset and comfort > self.max_temp:
            raise HomeAssistantError(
                f"In ECO the setpoint can be at most {self.max_temp - offset:.1f} °C "
                f"(max {self.max_temp:.1f} °C minus ECO offset {offset:.1f} °C)"
            )
        try:
            await self._client.set_setpoint(self._room_id, comfort)
        except UponorWsError as err:
            raise HomeAssistantError(f"Could not change the setpoint: {err}") from err

    async def async_set_hvac_mode(self, hvac_mode: HVACMode) -> None:
        """Only heating is supported; nothing to change."""
