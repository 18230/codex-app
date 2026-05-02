param(
    [string]$EnvFile = $(Join-Path (Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)) ".env"),
    [string]$TaskName = "CodexMobileGateway"
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

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ServerDir = Split-Path -Parent $ScriptDir
$ProjectDir = Split-Path -Parent $ServerDir
$SupportDir = Join-Path $env:LOCALAPPDATA "CodexMobileGateway"
$RuntimeDir = Join-Path $SupportDir "runtime"
$LogDir = Join-Path $SupportDir "logs"

Import-DotEnv -Path $EnvFile

if (-not $env:CODEX_MOBILE_TOKEN -or $env:CODEX_MOBILE_TOKEN.Length -lt 16) {
    throw "CODEX_MOBILE_TOKEN 必须设置，且长度至少 16 位"
}

New-Item -ItemType Directory -Force -Path $RuntimeDir, $LogDir | Out-Null

Push-Location $ServerDir
try {
    npm install
    npm run build
}
finally {
    Pop-Location
}

Remove-Item -Recurse -Force $RuntimeDir -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $RuntimeDir | Out-Null
Copy-Item -Recurse -Force (Join-Path $ServerDir "dist") $RuntimeDir
Copy-Item -Recurse -Force (Join-Path $ServerDir "node_modules") $RuntimeDir
Copy-Item -Force (Join-Path $ServerDir "package.json") $RuntimeDir
Copy-Item -Force (Join-Path $ServerDir "package-lock.json") $RuntimeDir
Copy-Item -Force (Join-Path $ScriptDir "start-gateway.ps1") $RuntimeDir
Copy-Item -Force $EnvFile (Join-Path $RuntimeDir ".env")
$RuntimeEnvFile = Join-Path $RuntimeDir ".env"
$RuntimeEnvText = Get-Content -Raw -Path $RuntimeEnvFile
if ($RuntimeEnvText -notmatch "(?m)^CODEX_MOBILE_DEFAULT_CWD=.+") {
    Add-Content -Path $RuntimeEnvFile -Value ""
    Add-Content -Path $RuntimeEnvFile -Value "CODEX_MOBILE_DEFAULT_CWD=$ProjectDir"
}

$StartScript = Join-Path $RuntimeDir "start-gateway.ps1"
$Stdout = Join-Path $LogDir "gateway.out.log"
$Stderr = Join-Path $LogDir "gateway.err.log"
$ActionArgument = "-NoProfile -ExecutionPolicy Bypass -File `"$StartScript`" -EnvFile `"$RuntimeEnvFile`" *> `"$Stdout`" 2> `"$Stderr`""
$Action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument $ActionArgument
$Trigger = New-ScheduledTaskTrigger -AtLogOn
$Principal = New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel LeastPrivilege
$Settings = New-ScheduledTaskSettingsSet -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -AllowStartIfOnBatteries -DisallowStartIfOnBatteries:$false

Register-ScheduledTask -TaskName $TaskName -Action $Action -Trigger $Trigger -Principal $Principal -Settings $Settings -Force | Out-Null
Start-ScheduledTask -TaskName $TaskName

Write-Host "已安装并启动 Windows 计划任务: $TaskName"
Write-Host "配置文件: $EnvFile"
Write-Host "运行配置: $RuntimeEnvFile"
Write-Host "运行目录: $RuntimeDir"
Write-Host "日志目录: $LogDir"
