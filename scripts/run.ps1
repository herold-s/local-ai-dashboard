$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot 'build.ps1')
$projectRoot = Split-Path $PSScriptRoot -Parent
Start-Process -FilePath (Join-Path $projectRoot 'bin/local-ai-dashboard.exe') -WorkingDirectory $projectRoot
