"""Config flow for the Uponor R-167 (WebSocket) integration."""

from __future__ import annotations

from typing import Any

import voluptuous as vol

from homeassistant.config_entries import ConfigFlow, ConfigFlowResult
from homeassistant.const import CONF_HOST, CONF_PORT
from homeassistant.helpers.aiohttp_client import async_get_clientsession

from .client import UponorWsError, async_probe
from .const import DEFAULT_HOST, DEFAULT_PORT, DOMAIN


class UponorWsConfigFlow(ConfigFlow, domain=DOMAIN):
    VERSION = 1

    async def async_step_user(self, user_input: dict[str, Any] | None = None) -> ConfigFlowResult:
        errors: dict[str, str] = {}
        if user_input is not None:
            host, port = user_input[CONF_HOST].strip(), user_input[CONF_PORT]
            await self.async_set_unique_id(f"{host}:{port}")
            self._abort_if_unique_id_configured()
            try:
                snapshot = await async_probe(async_get_clientsession(self.hass), host, port)
            except UponorWsError:
                errors["base"] = "cannot_connect"
            else:
                rooms = len(snapshot.get("rooms", []))
                return self.async_create_entry(
                    title=f"Uponor R-167 ({host})",
                    data={CONF_HOST: host, CONF_PORT: port},
                    description_placeholders={"rooms": str(rooms)},
                )
        schema = vol.Schema(
            {
                vol.Required(CONF_HOST, default=DEFAULT_HOST): str,
                vol.Required(CONF_PORT, default=DEFAULT_PORT): int,
            }
        )
        return self.async_show_form(step_id="user", data_schema=schema, errors=errors)
