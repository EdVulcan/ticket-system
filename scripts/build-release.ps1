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
New-Item -ItemType Directory -Path $releaseConfig, $releaseAdmin -Force | Out-Null

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

Write-Host "Release created at $releaseDirectory"
