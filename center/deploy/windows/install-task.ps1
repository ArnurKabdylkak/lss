# Регистрация автозапуска центра в планировщике Windows.
#
# Запускать один раз от администратора:
#   powershell -ExecutionPolicy Bypass -File deploy\windows\install-task.ps1
#
# Задача выполняется от SYSTEM при старте системы — вход пользователя
# не требуется. Это важно: Docker Desktop сам по себе стартует только
# при входе в сеанс, и без этой задачи после перезагрузки сервис
# поднимется лишь тогда, когда кто-нибудь зайдёт на машину.

$ErrorActionPreference = 'Stop'

$Project  = 'C:\lss\center'
$Script   = Join-Path $Project 'deploy\windows\start-mchs.ps1'
$TaskName = 'MCHS-Center'

if (-not (Test-Path $Script)) {
    throw "Не найден $Script — поправьте путь `$Project в этом файле"
}

# -UseWsl нужен, когда движок Docker живёт в WSL, а не в Docker Desktop:
# из-под SYSTEM сокет Docker Desktop недоступен. См. docs/autostart.md.
$arguments = "-NoProfile -ExecutionPolicy Bypass -File `"$Script`" -UseWsl"

$action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument $arguments

$trigger = New-ScheduledTaskTrigger -AtStartup

# Задержка: сеть и Docker после включения питания поднимаются не мгновенно.
$trigger.Delay = 'PT1M'

$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' `
    -LogonType ServiceAccount -RunLevel Highest

$settings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
    -StartWhenAvailable -ExecutionTimeLimit (New-TimeSpan -Hours 1)

Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger `
    -Principal $principal -Settings $settings -Force

Write-Output "Задача '$TaskName' зарегистрирована. Проверить: Get-ScheduledTask $TaskName"
Write-Output "Запустить сейчас:  Start-ScheduledTask $TaskName"
