"""ECO mode (Home/Away on the I-167) for the whole system."""

from __future__ import annotations

from homeassistant.components.switch import SwitchEntity
from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers.device_registry import DeviceInfo

from .client import UponorWsError
from .const import DOMAIN
from .entity import UponorWsEntity


async def async_setup_entry(hass: HomeAssistant, entry: ConfigEntry, async_add_entities) -> None:
    client = hass.data[DOMAIN][entry.entry_id]["client"]
    async_add_entities([UponorWsEcoModeSwitch(entry, client)])


class UponorWsEcoModeSwitch(UponorWsEntity, SwitchEntity):
    """On when the system is set to Away (ECO) on the I-167.

    Every room whose thermostat switch is on Comfort/ECO then lowers its
    setpoint by its ECO offset. Switching waits until the I-167 reports
    the new state.
    """

    _attr_translation_key = "eco_mode"
    _attr_icon = "mdi:leaf"

    def __init__(self, entry: ConfigEntry, client) -> None:
        super().__init__(entry, client)
        self._attr_unique_id = f"{DOMAIN}_eco_mode"
        host, port = entry.data["host"], entry.data["port"]
        self._attr_device_info = DeviceInfo(identifiers={(DOMAIN, f"{host}:{port}")})

    @property
    def is_on(self) -> bool | None:
        return self._client.system.get("eco_mode")

    @property
    def available(self) -> bool:
        return self._client.connected and self.is_on is not None

    async def async_turn_on(self, **kwargs) -> None:
        await self._set(True)

    async def async_turn_off(self, **kwargs) -> None:
        await self._set(False)

    async def _set(self, on: bool) -> None:
        try:
            await self._client.set_eco_mode(on)
        except UponorWsError as err:
            raise HomeAssistantError(f"Could not change ECO mode: {err}") from err
