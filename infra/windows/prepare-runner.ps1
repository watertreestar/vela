param(
  [string]$RunnerDir = "C:\packaging-runner",
  [string]$ApiUrl = "http://127.0.0.1:8080",
  [string]$MinioPublicUrl = "",
  [string]$WindowsPackagerCmd = "",
  [string]$RunnerTokenFile = "vela-runner-auth"
)

$ErrorActionPreference = "Stop"

New-Item -ItemType Directory -Force -Path $RunnerDir | Out-Null
Copy-Item -Force -Path "$PSScriptRoot\..\..\runner\package-runner.js" -Destination "$RunnerDir\package-runner.js"
Copy-Item -Force -Path "$PSScriptRoot\..\..\runner\package.json" -Destination "$RunnerDir\package.json"

@"
PACKAGING_API_URL=$ApiUrl
RUNNER_ID=windows-$env:COMPUTERNAME
TARGET=windows
MINIO_PUBLIC_URL=$MinioPublicUrl
WINDOWS_PACKAGER_CMD=$WindowsPackagerCmd
PACKAGING_API_TOKEN_FILE=$RunnerTokenFile
# AUTH_RUNNER_TOKEN is deliberately never written to runner.env. Nomad writes it
# to NOMAD_SECRETS_DIR\\$RunnerTokenFile for dispatched allocations.
"@ | Set-Content -Encoding ASCII -Path "$RunnerDir\runner.env"

@"
`$envFile = "$RunnerDir\runner.env"
Get-Content `$envFile | ForEach-Object {
  if (`$_ -notmatch "^\s*#" -and `$_ -match "=") {
    `$name, `$value = `$_.Split("=", 2)
    [Environment]::SetEnvironmentVariable(`$name, `$value, "Process")
  }
}
bun "$RunnerDir\package-runner.js"
"@ | Set-Content -Encoding ASCII -Path "$RunnerDir\run-once.ps1"

Write-Host "Windows runner prepared at $RunnerDir"
Write-Host "Install Bun, Nomad, Inno Setup/WiX, then run:"
Write-Host "  powershell -ExecutionPolicy Bypass -File $RunnerDir\run-once.ps1"
