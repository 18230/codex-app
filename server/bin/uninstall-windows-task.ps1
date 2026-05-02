param(
    [string]$TaskName = "CodexMobileGateway"
)

$ErrorActionPreference = "Stop"

if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
    Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
    Write-Host "已卸载 Windows 计划任务: $TaskName"
}
else {
    Write-Host "未找到 Windows 计划任务: $TaskName"
}
