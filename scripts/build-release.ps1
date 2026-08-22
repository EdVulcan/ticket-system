param(
    [switch]$SkipInstall
)

$ErrorActionPreference = 'Stop'

$repositoryRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$releaseDirectory = [System.IO.Path]::GetFullPath((Join-Path $repositoryRoot 'release'))
$expectedReleaseDirectory = Join-Path $repositoryRoot 'release'
if ($releaseDirectory.TrimEnd('\') -ne $expectedReleaseDirectory.TrimEnd('\')) {
    throw "Unexpected release directory: $releaseDirectory"
}

$adminDirectory = Join-Path $repositoryRoot 'admin'
$backendDirectory = Join-Path $repositoryRoot 'backend'

if (-not $SkipInstall) {
    Push-Location $adminDirectory
    try {
        npm ci
        if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' }
        npm audit
        if ($LASTEXITCODE -ne 0) { throw 'npm audit failed' }
    }
    finally {
        Pop-Location
    }
}

Push-Location $adminDirectory
try {
    npm run build
    if ($LASTEXITCODE -ne 0) { throw 'admin build failed' }
}
finally {
    Pop-Location
}

$env:CGO_ENABLED = '0'

if (Test-Path -LiteralPath $releaseDirectory) {
    Remove-Item -LiteralPath $releaseDirectory -Recurse -Force
}
$releaseBackend = Join-Path $releaseDirectory 'backend'
$releaseConfig = Join-Path $releaseBackend 'config'
$releaseAdmin = Join-Path $releaseDirectory 'admin\dist'
$releaseGate = Join-Path $releaseDirectory 'gate-client'
New-Item -ItemType Directory -Path $releaseConfig, $releaseAdmin, $releaseGate -Force | Out-Null

Push-Location $backendDirectory
try {
    go build -trimpath -o (Join-Path $releaseBackend 'ticket-system.exe') ./cmd
    if ($LASTEXITCODE -ne 0) { throw 'backend build failed' }
}
finally {
    Pop-Location
}

Copy-Item -LiteralPath (Join-Path $backendDirectory 'config\config.yaml') -Destination $releaseConfig
Copy-Item -Path (Join-Path $adminDirectory 'dist\*') -Destination $releaseAdmin -Recurse -Force

# The field controller is Linux. Build a glibc-independent amd64 pair; the
# installer consumes these binaries without needing Go on the gate computer.
$previousGoOS = $env:GOOS
$previousGoArch = $env:GOARCH
try {
    $env:GOOS = 'linux'
    $env:GOARCH = 'amd64'
    go build -trimpath -o (Join-Path $releaseGate 'gate-client') ./cmd/gate-client
    if ($LASTEXITCODE -ne 0) { throw 'gate-client linux build failed' }
    go build -trimpath -o (Join-Path $releaseGate 'gate-provision') ./cmd/gate-provision
    if ($LASTEXITCODE -ne 0) { throw 'gate-provision linux build failed' }
}
finally {
    $env:GOOS = $previousGoOS
    $env:GOARCH = $previousGoArch
}
Copy-Item -LiteralPath (Join-Path $repositoryRoot 'scripts\gate-client\install.sh') -Destination $releaseGate
Copy-Item -LiteralPath (Join-Path $repositoryRoot 'scripts\gate-client\ticket-gate.service') -Destination $releaseGate
Copy-Item -LiteralPath (Join-Path $repositoryRoot 'scripts\gate-client\ticket-gate-cli.sh') -Destination $releaseGate

Write-Host "Release created at $releaseDirectory"
