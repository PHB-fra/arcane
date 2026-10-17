# Arcane agent on Windows Server 2022 (unofficial preview)

This branch builds the Arcane **agent** as a Windows container. It is **not an official
Arcane image** and is not supported by the Arcane maintainers. Feedback goes to
[Discussion #4019](https://github.com/getarcaneapp/arcane/discussions/4019).

Image: `ghcr.io/phb-fra/arcane-agent-windows:ltsc2022-preview`, built by
[this workflow](.github/workflows/windows-agent-image.yml) from this branch only.

## Changes compared with upstream `main`

- `backend/internal/system/children/backup/children/snapshots`: Unix-only owner lookup moved
  behind build tags.
- `backend/go.mod`: copacetic v0.15.0 replaced by
  [PHB-fra/copacetic@arcane-windows-v0.15.0](https://github.com/PHB-fra/copacetic/tree/arcane-windows-v0.15.0)
  (v0.15.0 plus the fix proposed in project-copacetic/copacetic#1735), until a copacetic
  release includes it.
- `docker/Dockerfile-agent-windows`: nanoserver ltsc2022 image.

## Requirements

- Windows Server 2022 with Docker Engine in Windows containers mode (process isolation).
  On Windows 11 or Windows Server 2025, add `--isolation hyperv`.

## Run

Direct mode (the manager connects to port 3553):

```powershell
docker run -d --name arcane-agent --restart unless-stopped `
  -p 3553:3553 `
  -e AGENT_TOKEN=arc_... -e MANAGER_API_URL=http://manager:3552 `
  -v \\.\pipe\docker_engine:\\.\pipe\docker_engine `
  -v arcane-agent-data:C:\app\data `
  ghcr.io/phb-fra/arcane-agent-windows:ltsc2022-preview
```

Edge mode (the agent connects to the manager, no inbound port): replace `-p 3553:3553`
with `-e EDGE_AGENT=true`.

Verify the image provenance:

```powershell
gh attestation verify oci://ghcr.io/phb-fra/arcane-agent-windows:ltsc2022-preview --owner PHB-fra
```

## Known limitations

- Self-upgrade, system backup/restore and container self-detection assume a Linux
  container and are not expected to work.
- HTTPS to the manager relies on the nanoserver root certificate store.
