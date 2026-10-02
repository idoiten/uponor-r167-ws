"""Constants for the Uponor R-167 (WebSocket) integration."""

DOMAIN = "uponor_r167_ws"

DEFAULT_HOST = "10.10.10.151"
DEFAULT_PORT = 8765

SIGNAL_UPDATE = f"{DOMAIN}_update_{{}}"
SIGNAL_NEW_ROOM = f"{DOMAIN}_new_room_{{}}"
