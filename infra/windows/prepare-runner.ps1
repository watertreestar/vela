param(
  [string]$RunnerDir = "C:\packaging-runner",
  [string]$ApiUrl = "http://127.0.0.1:8080",
  [string]$MinioPublicUrl = "",
  [string]$WindowsPackagerCmd = ""
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
