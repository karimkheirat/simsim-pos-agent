# simsim-pos-agent

Local desktop agent for POS peripherals (thermal printer + cash drawer) in the [Simsim](https://github.com/karimkheirat/simsim) ecosystem.

Runs as a Windows service on the cashier's PC. Receives print jobs from the Simsim web POS over `127.0.0.1`, renders ESC/POS, and drives a USB-connected thermal printer through the Windows Print Spooler. Opens the cash drawer wired to the printer's DK port. Heartbeats and telemetry to the Simsim cloud when online; queues offline.

- **Target OS:** Windows 10 / 11 (64-bit)
- **Language:** Go 1.22+ — single static binary, no CGO
- **Pilot:** Hamoud Boualem store, Oran. SP-331 thermal printer.

See [POS_AGENT_SPEC.md](POS_AGENT_SPEC.md) for the full build spec.

## Status

Pre-M1 scaffold. Not yet functional.

## License

[MIT](LICENSE)
