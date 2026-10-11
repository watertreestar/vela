$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSScriptRoot))
$runnerPath = Join-Path $repoRoot "runner\package-runner.js"
$readinessPath = Join-Path $repoRoot "scripts\windows_runner_check_cli.py"
$operatorTokenFile = $env:VAR_WINDOWS_LIVE_OPERATOR_TOKEN_FILE
$nomadTokenFile = $env:VAR_WINDOWS_LIVE_NOMAD_TOKEN_FILE
$expectedThumbprint = $env:VAR_WINDOWS_SIGNING_THUMBPRINT
$runnerEnvFile = Join-Path $env:NOMAD_ALLOC_DIR "runner.env"
$artifactPathFile = Join-Path $env:NOMAD_ALLOC_DIR "windows-acceptance-artifact-path.txt"
$isccDirectory = "$env:LOCALAPPDATA\Programs\Inno Setup 6"
$signToolDirectory = "${env:ProgramFiles(x86)}\Windows Kits\10\bin\10.0.26100.0\x64"

foreach ($requiredFile in @($runnerPath, $readinessPath, $operatorTokenFile, $nomadTokenFile)) {
    if (-not (Test-Path -LiteralPath $requiredFile -PathType Leaf)) {
        throw "Required live-acceptance file does not exist"
    }
}

@(
    "PACKAGING_API_URL=$env:PACKAGING_API_URL"
    "RUNNER_ID=$env:RUNNER_ID"
    "TARGET=$env:TARGET"
    "NOMAD_JOB_ID=$env:NOMAD_JOB_ID"
) | Set-Content -LiteralPath $runnerEnvFile -Encoding ASCII

& bun $runnerPath
if ($LASTEXITCODE -ne 0) {
    throw "Windows package runner failed with exit code $LASTEXITCODE"
}

if (-not (Test-Path -LiteralPath $artifactPathFile -PathType Leaf)) {
    throw "Windows packager did not record the generated installer path"
}
$artifactPath = (Get-Content -LiteralPath $artifactPathFile -Raw).Trim()
$signature = Get-AuthenticodeSignature -LiteralPath $artifactPath
if (-not $signature.SignerCertificate -or $signature.SignerCertificate.Thumbprint -ne $expectedThumbprint) {
    throw "Generated installer signer does not match the acceptance identity"
}
if ($signature.Status -in @(
    [System.Management.Automation.SignatureStatus]::NotSigned,
    [System.Management.Automation.SignatureStatus]::HashMismatch
)) {
    throw "Generated installer Authenticode verification failed: $($signature.Status)"
}

$taskSid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$env:PACKAGING_OPERATOR_TOKEN = (Get-Content -LiteralPath $operatorTokenFile -Raw).Trim()
$env:NOMAD_TOKEN = (Get-Content -LiteralPath $nomadTokenFile -Raw).Trim()
$env:NOMAD_ADDR = $env:VAR_WINDOWS_LIVE_NOMAD_ADDR
$env:PATH = "$isccDirectory;$signToolDirectory;$env:PATH"

try {
    & python $readinessPath check `
        --live `
        --api-url $env:PACKAGING_API_URL `
        --node-class windows-packager `
        --runner-command "bun runner\package-runner.js" `
        --packager-kind inno `
        --packager-command "ISCC.exe setup.iss" `
        --signing-command "signtool.exe sign /fd SHA256 %OUTPUT%" `
        --bun-cache-dir $env:BUN_INSTALL_CACHE_DIR `
        --artifact-root $env:ARTIFACT_ROOT `
        --secrets-dir $env:NOMAD_SECRETS_DIR `
        --runner-env-file $runnerEnvFile `
        --allocation-id $env:NOMAD_ALLOC_ID `
        --build-id $env:BUILD_ID `
        --nomad-job-id $env:NOMAD_JOB_ID `
        --task-sid $taskSid
    if ($LASTEXITCODE -ne 0) {
        throw "Windows allocation live gate failed with exit code $LASTEXITCODE"
    }
}
finally {
    Remove-Item Env:PACKAGING_OPERATOR_TOKEN -ErrorAction SilentlyContinue
    Remove-Item Env:NOMAD_TOKEN -ErrorAction SilentlyContinue
}

Write-Host "Windows raw_exec live acceptance passed for build $env:BUILD_ID."
