param(
    [string]$EnvFile = $(Join-Path $HOME ".codex-mobile-gateway.env")
)

$ErrorActionPreference = "Stop"

function Import-DotEnv {
    param([string]$Path)
    if (-not (Test-Path $Path)) {
        return
    }

    Get-Content $Path | ForEach-Object {
        $line = $_.Trim()
        if ($line.Length -eq 0 -or $line.StartsWith("#")) {
            return
        }
        $parts = $line.Split("=", 2)
        if ($parts.Count -ne 2) {
            return
        }
        $name = $parts[0].Trim()
        $value = $parts[1].Trim().Trim("'").Trim('"')
        [Environment]::SetEnvironmentVariable($name, $value, "Process")
    }
}

function Stop-PortListener {
    param([int]$Port)
    $connections = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
    foreach ($connection in $connections) {
        if ($connection.OwningProcess -gt 0) {
            Stop-Process -Id $connection.OwningProcess -Force -ErrorAction SilentlyContinue
        }
    }
}

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ServerDir = Split-Path -Parent $ScriptDir

Import-DotEnv -Path $EnvFile

if (-not $env:CODEX_MOBILE_TOKEN -or $env:CODEX_MOBILE_TOKEN.Length -lt 16) {
    throw "CODEX_MOBILE_TOKEN 必须设置，且长度至少 16 位"
}

if (-not $env:CODEX_BINARY) {
    $env:CODEX_BINARY = "codex"
}
if (-not $env:CODEX_MOBILE_HOST) {
    $env:CODEX_MOBILE_HOST = "127.0.0.1"
}
if (-not $env:CODEX_MOBILE_PORT) {
    $env:CODEX_MOBILE_PORT = "8000"
}
if (-not $env:CODEX_APP_SERVER_HOST) {
    $env:CODEX_APP_SERVER_HOST = "127.0.0.1"
}
if (-not $env:CODEX_APP_SERVER_PORT) {
    $env:CODEX_APP_SERVER_PORT = "39000"
}
if (-not $env:CODEX_MOBILE_DEFAULT_CWD) {
    $env:CODEX_MOBILE_DEFAULT_CWD = (Split-Path -Parent $ServerDir)
}
if (-not $env:CODEX_MOBILE_CLIENT_PING_INTERVAL_MS) {
    $env:CODEX_MOBILE_CLIENT_PING_INTERVAL_MS = "15000"
}
if (-not $env:CODEX_MOBILE_CLIENT_IDLE_TIMEOUT_MS) {
    $env:CODEX_MOBILE_CLIENT_IDLE_TIMEOUT_MS = "45000"
}

Stop-PortListener -Port ([int]$env:CODEX_APP_SERVER_PORT)

Set-Location $ServerDir
node dist/index.js
