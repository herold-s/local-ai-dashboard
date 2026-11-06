param([switch]$Console, [switch]$SkipFrontend)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
Push-Location $projectRoot
try {
    $env:GOTOOLCHAIN = 'auto'
    if (-not $SkipFrontend) {
        if (-not (Test-Path -LiteralPath 'frontend/node_modules/typescript/bin/tsc')) {
            throw 'Install frontend dependencies first: cd frontend; pnpm install. Use -SkipFrontend for a Go-only rebuild with existing frontend/dist.'
        }
        Push-Location 'frontend'
        try {
            node node_modules/typescript/bin/tsc --noEmit
            if ($LASTEXITCODE -ne 0) { throw 'Frontend type check failed.' }
            node node_modules/vite/bin/vite.js build
            if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed.' }
        } finally { Pop-Location }
    }
    if (-not (Test-Path -LiteralPath 'frontend/dist/index.html')) { throw 'Build the frontend before compiling Go.' }
    New-Item -ItemType Directory -Force -Path 'bin' | Out-Null
    go build -buildvcs=false -trimpath -o bin/local-ai.exe .
    if ($LASTEXITCODE -ne 0) { throw 'CLI build failed.' }
    if ($Console) {
        go build -buildvcs=false -o bin/local-ai-dashboard.exe .
    } else {
        go build -buildvcs=false -trimpath -ldflags '-H=windowsgui' -o bin/local-ai-dashboard.exe .
    }
    if ($LASTEXITCODE -ne 0) { throw 'Build failed. Check the Go toolchain and dependency download output above.' }
    Write-Host 'Built bin/local-ai.exe (CLI) and bin/local-ai-dashboard.exe (desktop)'
} finally { Pop-Location }
