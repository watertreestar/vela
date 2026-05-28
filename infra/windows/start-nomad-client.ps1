param(
  [string]$NomadExe = "nomad.exe",
  [string]$Config = "C:\nomad\windows-client.hcl",
  [string]$ServerAddr = "127.0.0.1:4647"
)

$ErrorActionPreference = "Stop"

New-Item -ItemType Directory -Force -Path "C:\nomad" | Out-Null
Copy-Item -Force -Path "$PSScriptRoot\..\nomad\windows-client.hcl" -Destination $Config

(Get-Content $Config) -replace 'servers = \["127.0.0.1:4647"\]', "servers = [`"$ServerAddr`"]" |
  Set-Content -Encoding ASCII -Path $Config

Write-Host "Starting Nomad Windows client with raw_exec enabled."
Write-Host "This shell must run as Administrator for installer/signing workflows."
& $NomadExe agent -config $Config
