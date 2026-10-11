[CmdletBinding()]
param(
    [string]$NomadExe = "C:\tools\nomad\nomad.exe",
    [string]$NomadConfig = "C:\nomad\phase7-windows-secure-client.hcl",
    [string]$NomadAddr = "http://192.168.1.1:4646",
    [string]$NetworkInterface = "Realtek PCIe 2.5GbE Family Controller",
    [int]$ApiPort = 18080
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

function New-RandomSecret {
    $bytes = New-Object byte[] 32
    $generator = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $generator.GetBytes($bytes)
    }
    finally {
        $generator.Dispose()
    }
    return [Convert]::ToBase64String($bytes)
}

function Set-RestrictedDirectoryAcl {
    param([Parameter(Mandatory = $true)][string]$Path)

    $currentSid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
    $security = [System.Security.AccessControl.DirectorySecurity]::new()
    $security.SetOwner($currentSid)
    $security.SetAccessRuleProtection($true, $false)
    $inheritance = [System.Security.AccessControl.InheritanceFlags]::ContainerInherit -bor
        [System.Security.AccessControl.InheritanceFlags]::ObjectInherit
    foreach ($sidValue in @($currentSid.Value, "S-1-5-18", "S-1-5-32-544")) {
        $sid = [System.Security.Principal.SecurityIdentifier]::new($sidValue)
        $rule = [System.Security.AccessControl.FileSystemAccessRule]::new(
            $sid,
            [System.Security.AccessControl.FileSystemRights]::FullControl,
            $inheritance,
            [System.Security.AccessControl.PropagationFlags]::None,
            [System.Security.AccessControl.AccessControlType]::Allow
        )
        [void]$security.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $Path -AclObject $security
}

function Invoke-ApiJson {
    param(
        [Parameter(Mandatory = $true)][string]$Method,
        [Parameter(Mandatory = $true)][string]$BaseUrl,
        [Parameter(Mandatory = $true)][string]$Path,
        [string]$Token = "",
        [object]$Body = $null
    )

    $parameters = @{
        Method = $Method
        Uri = $BaseUrl.TrimEnd("/") + $Path
        TimeoutSec = 30
    }
    if ($Token) {
        $parameters.Headers = @{ Authorization = "Bearer $Token" }
    }
    if ($null -ne $Body) {
        $parameters.ContentType = "application/json"
        $parameters.Body = $Body | ConvertTo-Json -Depth 30 -Compress
    }
    return Invoke-RestMethod @parameters
}

function Send-ApiUpload {
    param(
        [Parameter(Mandatory = $true)][string]$BaseUrl,
        [Parameter(Mandatory = $true)][string]$Token,
        [Parameter(Mandatory = $true)][string]$FilePath
    )

    Add-Type -AssemblyName System.Net.Http
    $client = [System.Net.Http.HttpClient]::new()
    $client.DefaultRequestHeaders.Authorization =
        [System.Net.Http.Headers.AuthenticationHeaderValue]::new("Bearer", $Token)
    $multipart = [System.Net.Http.MultipartFormDataContent]::new()
    $stream = [System.IO.File]::OpenRead($FilePath)
    $content = [System.Net.Http.StreamContent]::new($stream)
    $content.Headers.ContentType = [System.Net.Http.Headers.MediaTypeHeaderValue]::new("application/octet-stream")
    $multipart.Add($content, "file", [System.IO.Path]::GetFileName($FilePath))
    try {
        $response = $client.PostAsync($BaseUrl.TrimEnd("/") + "/api/uploads", $multipart).GetAwaiter().GetResult()
        $payload = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
        if (-not $response.IsSuccessStatusCode) {
            throw "Artifact upload failed with HTTP $([int]$response.StatusCode)"
        }
        return $payload | ConvertFrom-Json
    }
    finally {
        $content.Dispose()
        $stream.Dispose()
        $multipart.Dispose()
        $client.Dispose()
    }
}

function Wait-ForApiHealth {
    param([string]$BaseUrl, [int]$TimeoutSeconds = 30)

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        try {
            $health = Invoke-ApiJson -Method Get -BaseUrl $BaseUrl -Path "/api/health"
            if ($health.status -eq "ok") {
                return
            }
        }
        catch {
        }
        Start-Sleep -Seconds 1
    }
    throw "Conductor health endpoint did not become ready"
}

function Wait-ForWindowsNode {
    param([string]$Address, [string[]]$ExistingNodeIds, [int]$TimeoutSeconds = 45)

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $nodes = Invoke-RestMethod -Uri ($Address.TrimEnd("/") + "/v1/nodes") -TimeoutSec 10
        foreach ($node in $nodes) {
            if ($ExistingNodeIds -contains $node.ID -or $node.Status -ne "ready") {
                continue
            }
            $detail = Invoke-RestMethod -Uri ($Address.TrimEnd("/") + "/v1/node/" + $node.ID) -TimeoutSec 10
            if (
                $detail.Attributes."kernel.name" -eq "windows" -and
                $detail.Meta.node_class -eq "windows-packager" -and
                $detail.Drivers.raw_exec.Healthy -eq $true
            ) {
                return $node.ID
            }
        }
        Start-Sleep -Seconds 2
    }
    throw "A new ready Windows raw_exec node did not register"
}

function Wait-ForBuildSuccess {
    param([string]$BaseUrl, [string]$BuildId, [int]$TimeoutSeconds = 240)

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $build = Invoke-ApiJson -Method Get -BaseUrl $BaseUrl -Path "/api/builds/$BuildId"
        if ($build.status -eq "succeeded") {
            return $build
        }
        if ($build.status -in @("failed", "canceled", "rejected")) {
            throw "Windows build ended in status $($build.status): $($build.error)"
        }
        Start-Sleep -Seconds 2
    }
    throw "Windows build did not succeed before the acceptance timeout"
}

function Wait-ForAllocationCompletion {
    param([string]$Address, [string]$JobId, [int]$TimeoutSeconds = 120)

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $allocations = Invoke-RestMethod -Uri ($Address.TrimEnd("/") + "/v1/job/$JobId/allocations") -TimeoutSec 10
        if (@($allocations).Count -eq 1) {
            $allocation = @($allocations)[0]
            if ($allocation.ClientStatus -eq "complete") {
                return $allocation
            }
            if ($allocation.ClientStatus -in @("failed", "lost")) {
                throw "Windows allocation ended in status $($allocation.ClientStatus)"
            }
        }
        Start-Sleep -Seconds 2
    }
    throw "Windows allocation did not complete before the acceptance timeout"
}

function Restore-ProcessEnvironment {
    param([hashtable]$PreviousValues)

    foreach ($entry in $PreviousValues.GetEnumerator()) {
        [Environment]::SetEnvironmentVariable($entry.Key, $entry.Value, "Process")
    }
}

$repoRoot = Split-Path -Parent $PSScriptRoot
$apiBaseUrl = "http://127.0.0.1:$ApiPort"
$guid = [guid]::NewGuid().ToString("N")
$tempParent = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
$tempRoot = Join-Path $tempParent "vela-windows-nomad-live-$guid"
$nomadParent = [System.IO.Path]::GetFullPath("C:\nomad")
$nomadDataRoot = Join-Path $nomadParent "vela-windows-nomad-live-$guid"
$conductorData = Join-Path $tempRoot "conductor-data"
$artifactRoot = Join-Path $tempRoot "artifacts"
$operatorTokenFile = Join-Path $tempRoot "operator.token"
$nomadTokenFile = Join-Path $tempRoot "nomad-read.token"
$conductorExe = Join-Path $tempRoot "conductor.exe"
$conductorLog = Join-Path $tempRoot "conductor.log"
$conductorErrorLog = Join-Path $tempRoot "conductor.err.log"
$nomadLog = Join-Path $tempRoot "nomad-client.log"
$nomadErrorLog = Join-Path $tempRoot "nomad-client.err.log"
$downloadedInstaller = Join-Path $tempRoot "downloaded-setup.exe"
$packageScript = Join-Path $repoRoot "infra\windows\acceptance\package.ps1"
$liveRunnerScript = Join-Path $repoRoot "infra\windows\acceptance\run-nomad-live.ps1"
$signToolPath = "${env:ProgramFiles(x86)}\Windows Kits\10\bin\10.0.26100.0\x64\signtool.exe"
$isccPath = "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe"

$nomadProcess = $null
$conductorProcess = $null
$certificateThumbprint = $null
$windowsNodeId = $null
$jobId = $null
$previousEnvironment = @{}
$environmentWasChanged = $false

try {
    foreach ($requiredFile in @($NomadExe, $NomadConfig, $packageScript, $liveRunnerScript, $signToolPath, $isccPath)) {
        if (-not (Test-Path -LiteralPath $requiredFile -PathType Leaf)) {
            throw "Required file does not exist: $requiredFile"
        }
    }
    if (Get-Process nomad -ErrorAction SilentlyContinue) {
        throw "A Nomad process is already running on this Windows host"
    }

    foreach ($directory in @($tempRoot, $nomadDataRoot, $conductorData, $artifactRoot)) {
        New-Item -ItemType Directory -Path $directory -Force | Out-Null
    }
    Set-RestrictedDirectoryAcl -Path $tempRoot
    Set-RestrictedDirectoryAcl -Path $nomadDataRoot

    Write-Host "Building the current Conductor for isolated live acceptance..."
    Push-Location (Join-Path $repoRoot "conductor")
    try {
        & go build -o $conductorExe ./cmd/server
        if ($LASTEXITCODE -ne 0) {
            throw "Conductor build failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }

    $operatorToken = New-RandomSecret
    $adminToken = New-RandomSecret
    $viewerToken = New-RandomSecret
    $runnerToken = New-RandomSecret
    $executionKey = New-RandomSecret
    $nomadReadToken = New-RandomSecret
    Set-Content -LiteralPath $operatorTokenFile -Value $operatorToken -Encoding ASCII -NoNewline
    Set-Content -LiteralPath $nomadTokenFile -Value $nomadReadToken -Encoding ASCII -NoNewline

    Write-Host "Creating a temporary Windows signing identity..."
    $certificate = New-SelfSignedCertificate `
        -Type CodeSigningCert `
        -Subject "CN=Vela Nomad Windows Live Acceptance" `
        -CertStoreLocation "Cert:\CurrentUser\My" `
        -KeyAlgorithm RSA `
        -KeyLength 2048 `
        -HashAlgorithm SHA256 `
        -NotAfter (Get-Date).AddDays(1)
    $certificateThumbprint = $certificate.Thumbprint

    $existingNodeIds = @(
        (Invoke-RestMethod -Uri ($NomadAddr.TrimEnd("/") + "/v1/nodes") -TimeoutSec 10) |
            ForEach-Object { $_.ID }
    )
    $networkAdapter = Get-NetAdapter | Where-Object {
        $_.Name -eq $NetworkInterface -or $_.InterfaceDescription -eq $NetworkInterface
    } | Select-Object -First 1
    if (-not $networkAdapter) {
        throw "The requested Windows network interface was not found"
    }

    Write-Host "Starting an isolated Windows Nomad client..."
    $nomadProcess = Start-Process `
        -FilePath $NomadExe `
        -ArgumentList @(
            "agent",
            "-config=$NomadConfig",
            "-data-dir=$nomadDataRoot",
            "-network-interface=$($networkAdapter.Name)",
            "-preferred-address-family=ipv4"
        ) `
        -WindowStyle Hidden `
        -RedirectStandardOutput $nomadLog `
        -RedirectStandardError $nomadErrorLog `
        -PassThru
    $windowsNodeId = Wait-ForWindowsNode -Address $NomadAddr -ExistingNodeIds $existingNodeIds

    $signingCommand = '"' + $signToolPath + '" sign /fd SHA256 /sha1 ' + $certificateThumbprint + ' /s My "%OUTPUT%"'
    $packagerCommand = 'powershell -NoProfile -ExecutionPolicy Bypass -File "' + $packageScript + '"'
    $runnerCommand = "& '$liveRunnerScript'"
    $conductorEnvironment = @{
        PORT = "$ApiPort"
        DATA_DIR = $conductorData
        PRODUCTS_DIR = (Join-Path $repoRoot "products")
        STORE_DRIVER = "json"
        DRY_RUN = "false"
        NOMAD_ADDR = $NomadAddr
        NOMAD_REGION = "global"
        NOMAD_NAMESPACE = "default"
        NOMAD_TOKEN = $nomadReadToken
        NOMAD_WINDOWS_NODE_CLASS = "windows-packager"
        PACKAGING_API_URL = $apiBaseUrl
        WINDOWS_RUNNER_COMMAND = $runnerCommand
        WINDOWS_PACKAGER_CMD = $packagerCommand
        WINDOWS_SIGNING_CMD = $signingCommand
        ARTIFACT_STORE = "local"
        ARTIFACT_ROOT = $artifactRoot
        AUTH_ENABLED = "true"
        AUTH_ADMIN_TOKEN = $adminToken
        AUTH_OPERATOR_TOKEN = $operatorToken
        AUTH_VIEWER_TOKEN = $viewerToken
        AUTH_RUNNER_TOKEN = $runnerToken
        RUNNER_AUTH_REQUIRED = "true"
        RUNNER_EXECUTION_HMAC_KEY = $executionKey
        RUNNER_CAPABILITY_TTL_SECONDS = "600"
        RECONCILE_INTERVAL_SECONDS = "1"
        RUNNER_MAX_RUN_SECONDS = "600"
    }
    foreach ($entry in $conductorEnvironment.GetEnumerator()) {
        $previousEnvironment[$entry.Key] = [Environment]::GetEnvironmentVariable($entry.Key, "Process")
        [Environment]::SetEnvironmentVariable($entry.Key, $entry.Value, "Process")
    }
    $environmentWasChanged = $true
    $conductorProcess = Start-Process `
        -FilePath $conductorExe `
        -WorkingDirectory (Join-Path $repoRoot "conductor") `
        -WindowStyle Hidden `
        -RedirectStandardOutput $conductorLog `
        -RedirectStandardError $conductorErrorLog `
        -PassThru
    Restore-ProcessEnvironment -PreviousValues $previousEnvironment
    $environmentWasChanged = $false
    Wait-ForApiHealth -BaseUrl $apiBaseUrl

    Write-Host "Uploading isolated component inputs..."
    $product = Invoke-ApiJson -Method Get -BaseUrl $apiBaseUrl -Path "/api/products/lsap"
    $componentOverrides = [ordered]@{}
    foreach ($component in $product.components) {
        $componentFile = Join-Path $tempRoot ($component.id + ".bin")
        @(
            "Vela Windows Nomad live acceptance component"
            "Component: $($component.id)"
            "Run: $guid"
        ) | Set-Content -LiteralPath $componentFile -Encoding UTF8
        $upload = Send-ApiUpload -BaseUrl $apiBaseUrl -Token $operatorToken -FilePath $componentFile
        $componentOverrides[$component.id] = @{
            kind = "manual-upload"
            uploadId = $upload.id
        }
    }

    $buildRequest = @{
        productId = "lsap"
        target = "windows"
        profile = "qa"
        customer = "example-customer"
        version = "1.0.0"
        releaseChannel = "preview"
        variables = @{
            requestedBy = "windows_nomad_live_acceptance.ps1"
            windows_live_operator_token_file = $operatorTokenFile
            windows_live_nomad_token_file = $nomadTokenFile
            windows_live_nomad_addr = $NomadAddr
            windows_signing_thumbprint = $certificateThumbprint
        }
        configOverrides = @{
            installDir = "C:\Program Files\LSAP Acceptance"
            ports = @{ web = 49410 }
            customer = @{ name = "Windows Nomad Acceptance"; license = "enterprise" }
            database = @{ passwordPolicy = "strong" }
            modules = @{ camera = $true; push = $true; map = $true }
        }
        componentOverrides = $componentOverrides
    }

    Write-Host "Creating and approving an auth-enabled Windows preview build..."
    $build = Invoke-ApiJson -Method Post -BaseUrl $apiBaseUrl -Path "/api/builds" -Token $operatorToken -Body $buildRequest
    $buildId = $build.id
    $approved = Invoke-ApiJson -Method Post -BaseUrl $apiBaseUrl -Path "/api/builds/$buildId/approve" -Token $operatorToken -Body @{}
    $jobId = $approved.nomadJobId
    if (-not $jobId) {
        throw "Approved build did not return a Nomad job ID"
    }

    $succeededBuild = Wait-ForBuildSuccess -BaseUrl $apiBaseUrl -BuildId $buildId
    $allocation = Wait-ForAllocationCompletion -Address $NomadAddr -JobId $jobId

    Write-Host "Cross-checking completed Conductor and Nomad evidence..."
    $env:PACKAGING_OPERATOR_TOKEN = $operatorToken
    $env:NOMAD_ADDR = $NomadAddr
    $env:NOMAD_TOKEN = $nomadReadToken
    try {
        & python (Join-Path $repoRoot "scripts\runner_e2e_cli.py") check `
            --live `
            --api-url $apiBaseUrl `
            --build-id $buildId `
            --nomad-job-id $jobId
        if ($LASTEXITCODE -ne 0) {
            throw "Runner E2E live gate failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Remove-Item Env:PACKAGING_OPERATOR_TOKEN -ErrorAction SilentlyContinue
        Remove-Item Env:NOMAD_TOKEN -ErrorAction SilentlyContinue
    }

    Invoke-WebRequest `
        -UseBasicParsing `
        -Uri $succeededBuild.downloadUrl `
        -Headers @{ Authorization = "Bearer $operatorToken" } `
        -OutFile $downloadedInstaller `
        -TimeoutSec 60
    $downloadedSignature = Get-AuthenticodeSignature -LiteralPath $downloadedInstaller
    if (
        -not $downloadedSignature.SignerCertificate -or
        $downloadedSignature.SignerCertificate.Thumbprint -ne $certificateThumbprint -or
        $downloadedSignature.Status -in @(
            [System.Management.Automation.SignatureStatus]::NotSigned,
            [System.Management.Automation.SignatureStatus]::HashMismatch
        )
    ) {
        throw "Downloaded installer Authenticode evidence did not match the dispatched signing identity"
    }

    $validated = Invoke-ApiJson -Method Post -BaseUrl $apiBaseUrl -Path "/api/builds/$buildId/validate-preview" -Token $operatorToken -Body @{}
    if ($validated.status -ne "preview_validated") {
        throw "Preview validation did not reach preview_validated"
    }
    $releaseCheck = Invoke-ApiJson -Method Get -BaseUrl $apiBaseUrl -Path "/api/builds/$buildId/release-check"
    if ($releaseCheck.releaseReady -ne $true) {
        throw "Release gate did not accept the verified Windows preview"
    }

    Write-Host "Windows Nomad live acceptance passed."
    Write-Host "  Build ID: $buildId"
    Write-Host "  Nomad job ID: $jobId"
    Write-Host "  Allocation ID: $($allocation.ID)"
    Write-Host "  Windows node ID: $windowsNodeId"
    Write-Host "  Authenticode: $($downloadedSignature.Status) (temporary self-signed root)"
}
finally {
    if ($environmentWasChanged) {
        Restore-ProcessEnvironment -PreviousValues $previousEnvironment
    }
    foreach ($name in @("PACKAGING_OPERATOR_TOKEN", "NOMAD_TOKEN")) {
        Remove-Item "Env:$name" -ErrorAction SilentlyContinue
    }

    if ($conductorProcess -and (Get-Process -Id $conductorProcess.Id -ErrorAction SilentlyContinue)) {
        Stop-Process -Id $conductorProcess.Id -Force
    }
    if ($nomadProcess -and (Get-Process -Id $nomadProcess.Id -ErrorAction SilentlyContinue)) {
        Stop-Process -Id $nomadProcess.Id -Force
        Start-Sleep -Seconds 1
    }
    if ($jobId) {
        $variablePath = "nomad/jobs/$jobId/builder/package/runner"
        & $NomadExe var purge "-address=$NomadAddr" $variablePath 2>$null | Out-Null
        & $NomadExe job stop "-address=$NomadAddr" -purge -detach -yes $jobId 2>$null | Out-Null
    }
    if ($windowsNodeId) {
        try {
            Invoke-RestMethod `
                -Method Post `
                -Uri ($NomadAddr.TrimEnd("/") + "/v1/node/$windowsNodeId/purge") `
                -TimeoutSec 10 | Out-Null
        }
        catch {
            Write-Warning "Could not purge the isolated Windows Nomad node"
        }
    }
    if ($certificateThumbprint) {
        foreach ($storeName in @("My", "Root", "TrustedPublisher")) {
            $certificatePath = "Cert:\CurrentUser\$storeName\$certificateThumbprint"
            if (Test-Path -LiteralPath $certificatePath) {
                Remove-Item -LiteralPath $certificatePath -Force
            }
        }
    }

    $resolvedTempRoot = [System.IO.Path]::GetFullPath($tempRoot)
    if (
        (Test-Path -LiteralPath $resolvedTempRoot) -and
        $resolvedTempRoot.StartsWith($tempParent, [System.StringComparison]::OrdinalIgnoreCase) -and
        ([System.IO.Path]::GetFileName($resolvedTempRoot) -like "vela-windows-nomad-live-*")
    ) {
        Remove-Item -LiteralPath $resolvedTempRoot -Recurse -Force
    }
    $resolvedNomadDataRoot = [System.IO.Path]::GetFullPath($nomadDataRoot)
    if (
        (Test-Path -LiteralPath $resolvedNomadDataRoot) -and
        $resolvedNomadDataRoot.StartsWith($nomadParent, [System.StringComparison]::OrdinalIgnoreCase) -and
        ([System.IO.Path]::GetFileName($resolvedNomadDataRoot) -like "vela-windows-nomad-live-*")
    ) {
        Remove-Item -LiteralPath $resolvedNomadDataRoot -Recurse -Force
    }
}
