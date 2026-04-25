# Blueclaw Lab

This directory contains the local M4 Mac test rig assets for `blueclaw-lab`.

The default topology is:

- macOS host
- Tart ARM Linux virtual machine
- Firecracker inside the Linux virtual machine
- Blueclaw inside the Firecracker guest

The host is the companion/browser side.
The Linux virtual machine is the simulated `InternKim`.
The Firecracker guest is the simulated production `Blueclaw` runtime boundary.

Useful commands:

```bash
go run ./cmd/blueclaw-lab --configuration config/lab.example.json image-build
go run ./cmd/blueclaw-lab --configuration config/lab.example.json vm-up
go run ./cmd/blueclaw-lab --configuration config/lab.example.json smoke-firecracker
go run ./cmd/blueclaw-lab --configuration config/lab.example.json scenario-mattermost
go run ./cmd/blueclaw-lab --configuration config/lab.example.json scenario-slack
go run ./cmd/blueclaw-lab --configuration config/lab.example.json scenario-browser-handoff
go run ./cmd/blueclaw-lab --configuration config/lab.example.json vm-down
```

Connector scenarios should exercise the unified connector runtime rather than platform-specific handler shortcuts.

- `scenario-mattermost` covers Mattermost-style receive and reply paths
- `scenario-slack` covers Slack Events API-style receive and Slack Web API-style reply paths
- future Socket Mode or Signal receivers should be added as `ConnectorTransport` implementations without changing the connector core
