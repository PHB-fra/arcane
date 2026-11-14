<#
.SYNOPSIS
  Pulls (or loads) and starts the unofficial Arcane agent Windows container
  on Windows Server 2019, 2022 or 2025.

  Arcane v3 model: the agent connects OUTBOUND to the manager over a tunnel and
  enrolls with an enrollment token. The old direct/polling agent (inbound port
  3553) was removed in v3, so there is no inbound port and no -Edge switch any
  more. ARCANE_AGENT_TOKEN must be a fresh enrollment token issued by a v3
  manager, and the manager itself must be running v3.

.EXAMPLE
  $env:ARCANE_AGENT_TOKEN = 'arc_...'   # enrollment token from the v3 manager
  .\deploy-arcane-agent-windows.ps1 -ManagerUrl https://arcane.example.com
  .\deploy-arcane-agent-windows.ps1 -ManagerUrl http://10.1.1.4:3552 -Transport grpc
  # Host without access to ghcr.io: load the tarball saved for its Windows release.
  .\deploy-arcane-agent-windows.ps1 -ManagerUrl http://10.1.1.4:3552 -Tarball .\arcane-agent-ltsc2019.tar `
    -Image ghcr.io/phb-fra/arcane-agent-windows:ltsc2019-preview
#>
param(
    [Parameter(Mandatory)] [string] $ManagerUrl,
    # Multi-release tag: Docker pulls the image matching the host's Windows release.
    [string] $Image = 'ghcr.io/phb-fra/arcane-agent-windows:ltsc-preview',
    [string] $Tarball,
    [string] $Name = 'arcane-agent',
    # Tunnel transport: auto (default, lets the agent negotiate), grpc or websocket.
    [ValidateSet('auto', 'grpc', 'websocket')] [string] $Transport = 'websocket'
)
$ErrorActionPreference = 'Stop'

if (-not $env:ARCANE_AGENT_TOKEN) {
    throw 'Set $env:ARCANE_AGENT_TOKEN to a fresh enrollment token from the v3 manager.'
}

$os = docker version --format '{{.Server.Os}}'
if ($os -ne 'windows') { throw "The Docker engine is in '$os' mode: switch it to Windows containers." }

if ($Tarball) { docker load -i $Tarball }
else { docker pull $Image }
if ($LASTEXITCODE -ne 0) { throw "Image $Image is not available." }

if (docker ps -aq --filter "name=^$Name$") { docker rm -f $Name | Out-Null }

$runArgs = @(
    'run', '-d', '--name', $Name, '--restart', 'unless-stopped',
    '--isolation', 'process',
    '-e', 'ARCANE_AGENT=true',
    '-e', "ARCANE_MANAGER_API_URL=$ManagerUrl",
    '-e', "ARCANE_AGENT_TOKEN=$env:ARCANE_AGENT_TOKEN",
    '-e', "ARCANE_AGENT_TRANSPORT=$Transport",
    '-v', '\\.\pipe\docker_engine:\\.\pipe\docker_engine',
    '-v', "${Name}-data:C:\app\data"
)

docker @runArgs $Image
Start-Sleep -Seconds 5
docker logs --tail 30 $Name
