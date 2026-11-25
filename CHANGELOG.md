# Changelog

## Unreleased

- Phase 1: the protocols, tested against fakes: catalog, serial port, USB identification, ESP32 and nRF52 flashing,
  and repeater/room-server setup over the serial console. Nothing to run yet: the screens come in Phase 2.
- Phase 2 (under way): the owner's answers to the screens' plan are in `SPEC.md`. New with them: a screen lock (hold
  Tab 3 s; any key, then Tab, then Enter unlocks; the screen goes dark meanwhile, and a flash or setup carries on), and
  Ctrl+C while a setup is being sent quits at once, writing to `screen.log` what the node was left with.
  `--screen-test` names meshflash and shows the characters its screens use.
