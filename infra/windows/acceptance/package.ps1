$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$isccPath = "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe"
$templatePath = Join-Path $PSScriptRoot "setup.iss"
$outputPath = $env:OUTPUT

if (-not $outputPath) {
    throw "OUTPUT is required"
}
foreach ($requiredFile in @($isccPath, $templatePath)) {
    if (-not (Test-Path -LiteralPath $requiredFile -PathType Leaf)) {
        throw "Required file does not exist: $requiredFile"
    }
}

$outputDirectory = Split-Path -Parent $outputPath
$workspace = Split-Path -Parent $outputDirectory
$payloadPath = Join-Path $workspace "context\rendered\windows-acceptance-payload.txt"
$compiledPath = Join-Path $outputDirectory "vela-toolchain-acceptance.exe"

@(
    "Vela Windows Nomad acceptance payload"
    "Build: $env:BUILD_ID"
    "Product: $env:PRODUCT_ID"
    "Target: $env:TARGET"
    "Version: $env:VERSION"
) | Set-Content -LiteralPath $payloadPath -Encoding UTF8

& $isccPath `
    "/Qp" `
    "/O$outputDirectory" `
    "/DSourceFile=$payloadPath" `
    "/DAcceptanceVersion=$env:VERSION" `
    $templatePath
if ($LASTEXITCODE -ne 0) {
    throw "Inno Setup compilation failed with exit code $LASTEXITCODE"
}
if (-not (Test-Path -LiteralPath $compiledPath -PathType Leaf)) {
    throw "Inno Setup did not produce the expected installer"
}

Move-Item -LiteralPath $compiledPath -Destination $outputPath -Force

if ($env:NOMAD_ALLOC_DIR) {
    Set-Content `
        -LiteralPath (Join-Path $env:NOMAD_ALLOC_DIR "windows-acceptance-artifact-path.txt") `
        -Value $outputPath `
        -Encoding ASCII
}
