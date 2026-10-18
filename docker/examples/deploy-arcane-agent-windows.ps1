<#
.SYNOPSIS
  Pulls (or loads) and starts the unofficial Arcane agent Windows container
  on Windows Server 2019, 2022 or 2025.

.EXAMPLE
  $env:ARCANE_AGENT_TOKEN = 'arc_...'
  .\deploy-arcane-agent-windows.ps1 -ManagerUrl http://10.1.1.4:3552
  .\deploy-arcane-agent-windows.ps1 -ManagerUrl https://arcane.example.com -Edge
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
    [int] $Port = 3553,
    # Edge mode: the agent connects to the manager, no inbound port is opened.
    [switch] $Edge
)
$ErrorActionPreference = 'Stop'

if (-not $env:ARCANE_AGENT_TOKEN) { throw 'Set $env:ARCANE_AGENT_TOKEN to the agent token given by the manager.' }

$os = docker version --format '{{.Server.Os}}'
if ($os -ne 'windows') { throw "The Docker engine is in '$os' mode: switch it to Windows containers." }

if ($Tarball) { docker load -i $Tarball }
else { docker pull $Image }
if ($LASTEXITCODE -ne 0) { throw "Image $Image is not available." }

if (docker ps -aq --filter "name=^$Name$") { docker rm -f $Name | Out-Null }

$runArgs = @(
    'run', '-d', '--name', $Name, '--restart', 'unless-stopped',
    '--isolation', 'process',
    '-e', "AGENT_TOKEN=$env:ARCANE_AGENT_TOKEN",
    '-e', "MANAGER_API_URL=$ManagerUrl",
    '-e', 'EDGE_TRANSPORT=websocket',
    '-v', '\\.\pipe\docker_engine:\\.\pipe\docker_engine',
    '-v', "${Name}-data:C:\app\data"
)
if ($Edge) { $runArgs += @('-e', 'EDGE_AGENT=true') }
else { $runArgs += @('-p', "${Port}:3553") }

docker @runArgs $Image
Start-Sleep -Seconds 5
docker logs --tail 30 $Name
