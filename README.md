# meshflash

A MeshCore flasher for the M5Stack CardputerZero. Plug a LoRa board into the Zero's USB-A port, pick the board, the
firmware role and the version from MeshCore's official flasher catalog, and meshflash flashes it, verifies it, and sets
up repeaters and room servers over their serial console. Made for the field: flash and configure a repeater on a
hilltop with no laptop.

Status: in development. Phase 1 (the flashing and setup protocols, tested against fake boards and recordings of the
web flasher) is done; Phase 2 (the screens) is next. `SPEC.md` is the plan; `docs/meshcore-flashing.md` is the
research.

Install steps and keys come in Phase 2 and 3.

MIT licence; see `THIRD_PARTY_NOTICES` for what it carries from others.
