[CmdletBinding()]
param(
    [string]$IsccPath = "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe",
    [string]$SignToolPath = "${env:ProgramFiles(x86)}\Windows Kits\10\bin\10.0.26100.0\x64\signtool.exe",
    [string]$ApiUrl = "http://192.168.1.1:8080"
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

function Assert-LastExitCode {
    param([Parameter(Mandatory = $true)][string]$Operation)

    if ($LASTEXITCODE -ne 0) {
        throw "$Operation failed with exit code $LASTEXITCODE"
    }
}

$repoRoot = Split-Path -Parent $PSScriptRoot
$templatePath = Join-Path $repoRoot "infra\windows\acceptance\setup.iss"
$payloadPath = Join-Path $repoRoot "README.md"
$tempParent = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
$tempRoot = Join-Path $tempParent ("vela-windows-toolchain-acceptance-" + [guid]::NewGuid().ToString("N"))
$outputDir = Join-Path $tempRoot "output"
$installDir = Join-Path $tempRoot "installed"
$bunCacheDir = Join-Path $tempRoot "bun-cache"
$artifactRoot = Join-Path $tempRoot "artifacts"
$secretsDir = Join-Path $tempRoot "secrets"
$certificateThumbprint = $null

try {
    foreach ($requiredFile in @($IsccPath, $SignToolPath, $templatePath, $payloadPath)) {
        if (-not (Test-Path -LiteralPath $requiredFile -PathType Leaf)) {
            throw "Required file does not exist: $requiredFile"
        }
    }

    foreach ($directory in @($tempRoot, $outputDir, $installDir, $bunCacheDir, $artifactRoot, $secretsDir)) {
        New-Item -ItemType Directory -Path $directory -Force | Out-Null
    }

    Write-Host "Compiling setup.exe with Inno Setup..."
    & $IsccPath "/Qp" "/O$outputDir" "/DSourceFile=$payloadPath" "/DAcceptanceVersion=2026.10.11" $templatePath
    Assert-LastExitCode "Inno Setup compilation"

    $installerPath = Join-Path $outputDir "vela-toolchain-acceptance.exe"
    if (-not (Test-Path -LiteralPath $installerPath -PathType Leaf)) {
        throw "Inno Setup did not produce the expected installer"
    }

    Write-Host "Creating a temporary code-signing identity..."
    $certificate = New-SelfSignedCertificate `
        -Type CodeSigningCert `
        -Subject "CN=Vela Windows Toolchain Acceptance" `
        -CertStoreLocation "Cert:\CurrentUser\My" `
        -KeyAlgorithm RSA `
        -KeyLength 2048 `
        -HashAlgorithm SHA256 `
        -NotAfter (Get-Date).AddDays(1)
    $certificateThumbprint = $certificate.Thumbprint

    Write-Host "Signing and verifying the installer with SignTool..."
    & $SignToolPath sign /fd SHA256 /sha1 $certificateThumbprint /s My $installerPath
    Assert-LastExitCode "SignTool signing"
    $previousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $verificationOutput = (& $SignToolPath verify /pa /v $installerPath 2>&1 | Out-String)
        $verificationExitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
    $signature = Get-AuthenticodeSignature -LiteralPath $installerPath
    if (-not $signature.SignerCertificate -or $signature.SignerCertificate.Thumbprint -ne $certificateThumbprint) {
        throw "Authenticode did not return the temporary signing identity"
    }
    if ($signature.Status -eq [System.Management.Automation.SignatureStatus]::HashMismatch) {
        throw "Authenticode detected an installer hash mismatch"
    }
    if ($verificationExitCode -ne 0) {
        $expectedSelfSignedFailure =
            $verificationOutput -match "0x800B0109" -or
            $verificationOutput -match "not trusted" -or
            $verificationOutput -match "terminated in a root certificate"
        if (-not $expectedSelfSignedFailure) {
            throw "SignTool verification failed for a reason other than the temporary self-signed root"
        }
        Write-Host "SignTool verified the signature and reported the expected untrusted test root."
    }
    elseif ($signature.Status -ne [System.Management.Automation.SignatureStatus]::Valid) {
        throw "SignTool succeeded but Authenticode returned $($signature.Status)"
    }

    Write-Host "Installing silently and checking the installed payload..."
    $installerProcess = Start-Process `
        -FilePath $installerPath `
        -ArgumentList @("/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/DIR=$installDir") `
        -WindowStyle Hidden `
        -Wait `
        -PassThru
    if ($installerProcess.ExitCode -ne 0) {
        throw "Silent installer execution failed with exit code $($installerProcess.ExitCode)"
    }

    $installedPayload = Join-Path $installDir "acceptance-payload.md"
    if (-not (Test-Path -LiteralPath $installedPayload -PathType Leaf)) {
        throw "The installed acceptance payload is missing"
    }
    $sourceHash = (Get-FileHash -LiteralPath $payloadPath -Algorithm SHA256).Hash
    $installedHash = (Get-FileHash -LiteralPath $installedPayload -Algorithm SHA256).Hash
    if ($sourceHash -ne $installedHash) {
        throw "The installed payload SHA-256 does not match the source payload"
    }

    Write-Host "Running the Windows runner production-readiness preflight..."
    $env:PATH = (Split-Path -Parent $IsccPath) + ";" + (Split-Path -Parent $SignToolPath) + ";" + $env:PATH
    $packagerCommand = "ISCC.exe setup.iss"
    $signingCommand = "signtool.exe sign /fd SHA256 %OUTPUT%"
    $readinessScript = Join-Path $repoRoot "scripts\windows_runner_check_cli.py"
    & python $readinessScript check `
        --api-url $ApiUrl `
        --node-class windows-packager `
        --runner-command "bun runner\package-runner.js" `
        --packager-kind inno `
        --packager-command $packagerCommand `
        --signing-command $signingCommand `
        --bun-cache-dir $bunCacheDir `
        --artifact-root $artifactRoot `
        --secrets-dir $secretsDir
    Assert-LastExitCode "Windows runner readiness preflight"

    Write-Host "Windows toolchain acceptance passed."
    Write-Host "  Inno Setup: $IsccPath"
    Write-Host "  SignTool: $SignToolPath"
    Write-Host "  Authenticode: $($signature.Status) (temporary self-signed root)"
    Write-Host "  Payload SHA-256: $sourceHash"
}
finally {
    if ($certificateThumbprint) {
        foreach ($storeName in @("My", "Root", "TrustedPublisher")) {
            $storeCertificate = "Cert:\CurrentUser\$storeName\$certificateThumbprint"
            if (Test-Path -LiteralPath $storeCertificate) {
                Remove-Item -LiteralPath $storeCertificate -Force
            }
        }
    }

    $resolvedTempRoot = [System.IO.Path]::GetFullPath($tempRoot)
    if (
        (Test-Path -LiteralPath $resolvedTempRoot) -and
        $resolvedTempRoot.StartsWith($tempParent, [System.StringComparison]::OrdinalIgnoreCase) -and
        ([System.IO.Path]::GetFileName($resolvedTempRoot) -like "vela-windows-toolchain-acceptance-*")
    ) {
        Remove-Item -LiteralPath $resolvedTempRoot -Recurse -Force
    }
}
