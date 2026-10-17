# Arcane agent on Windows Server (unofficial preview)

This branch builds the Arcane **agent** as a Windows container for Windows Server 2019 and 2022. It is **not an official
Arcane image** and is not supported by the Arcane maintainers. Feedback goes to
[Discussion #4019](https://github.com/getarcaneapp/arcane/discussions/4019).

Images, built by [this workflow](.github/workflows/windows-agent-image.yml) from this branch only:

| Tag | Host |
| --- | --- |
| `ghcr.io/phb-fra/arcane-agent-windows:ltsc-preview` | Windows Server 2019 or 2022: Docker picks the matching image |
| `ghcr.io/phb-fra/arcane-agent-windows:ltsc2022-preview` | Windows Server 2022 |
| `ghcr.io/phb-fra/arcane-agent-windows:ltsc2019-preview` | Windows Server 2019 |

Versioned tags (`2.16.1-ltsc-preview`, ...) are published as well. Both images contain the same
`arcane-agent.exe`. CI only runs the Windows Server 2022 image; the 2019 image has not been
started in CI.

## Changes compared with upstream `main`

- `backend/internal/system/children/backup/children/snapshots`: Unix-only owner lookup moved
  behind build tags.
- `backend/go.mod`: copacetic v0.15.0 replaced by
  [PHB-fra/copacetic@arcane-windows-v0.15.0](https://github.com/PHB-fra/copacetic/tree/arcane-windows-v0.15.0)
  (v0.15.0 plus the fix proposed in project-copacetic/copacetic#1735), until a copacetic
  release includes it.
- `docker/windows-image`: assembles the nanoserver 1809 (2019) and ltsc2022 images without a Docker
  daemon, so a single Windows Server 2022 runner builds both.
- `docker/Dockerfile-agent-windows`: equivalent Dockerfile, to build on a Windows host.

## Requirements

- Windows Server 2019 or 2022 with Docker Engine in Windows containers mode (process isolation).
  On Windows 11 or Windows Server 2025, use the `ltsc2022-preview` tag with `--isolation hyperv`.

## Run

Direct mode (the manager connects to port 3553):

```powershell
docker run -d --name arcane-agent --restart unless-stopped `
  -p 3553:3553 `
  -e AGENT_TOKEN=arc_... -e MANAGER_API_URL=http://manager:3552 `
  -v \\.\pipe\docker_engine:\\.\pipe\docker_engine `
  -v arcane-agent-data:C:\app\data `
  ghcr.io/phb-fra/arcane-agent-windows:ltsc-preview
```

Edge mode (the agent connects to the manager, no inbound port): replace `-p 3553:3553`
with `-e EDGE_AGENT=true`.

Verify the image provenance:

```powershell
gh attestation verify oci://ghcr.io/phb-fra/arcane-agent-windows:ltsc-preview --owner PHB-fra
```

## Known limitations

- Self-upgrade, system backup/restore and container self-detection assume a Linux
  container and are not expected to work.
- HTTPS to the manager relies on the nanoserver root certificate store.
