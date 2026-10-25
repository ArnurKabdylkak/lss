# Запуск центра на Windows-хосте.
#
# Вызывается планировщиком при старте системы (см. install-task.ps1).
# Дожидается демона Docker и поднимает compose: после включения питания
# демон готов не сразу.
#
#   -UseWsl  — работать через движок Docker внутри WSL, а не через
#              Docker Desktop. Нужен, когда задача выполняется от SYSTEM
#              без входа пользователя: Docker Desktop живёт в сеансе
#              пользователя, и из-под SYSTEM его сокет недоступен.
#
# Какой режим ваш — см. docs/autostart.md.

param(
    [switch]$UseWsl,
    [string]$WslDistro = 'Ubuntu'
)

$ErrorActionPreference = 'Stop'

# Каталог с compose.yaml. Поправьте под своё размещение.
$Project = 'C:\lss\center'
$LogFile = Join-Path $Project 'autostart.log'

function Log($message) {
    $line = "{0}  {1}" -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $message
    Add-Content -Path $LogFile -Value $line
    Write-Output $line
}

Set-Location $Project
Log ("запуск автостарта" + $(if ($UseWsl) { " (через WSL: $WslDistro)" } else { " (Docker Desktop)" }))

# Внутри WSL проект виден по пути /mnt/<диск>/… — переводим путь один раз.
$WslProject = ($Project -replace '^([A-Za-z]):', { "/mnt/" + $_.Groups[1].Value.ToLower() }) -replace '\\', '/'

function Invoke-Docker([string]$arguments) {
    if ($UseWsl) {
        # Служба Docker в WSL не запускается сама: её поднимает первая
        # же команда, поэтому service docker start вызывается всегда.
        wsl.exe -d $WslDistro -u root -e sh -lc "service docker start >/dev/null 2>&1; cd '$WslProject' && docker $arguments"
    } else {
        Invoke-Expression "docker $arguments"
    }
}

# Docker Desktop после включения питания поднимается до минуты.
# Ждём демон, а не просто пробуем один раз.
$deadline = (Get-Date).AddMinutes(10)
while ((Get-Date) -lt $deadline) {
    Invoke-Docker 'info' *> $null
    if ($LASTEXITCODE -eq 0) { break }
    Log 'демон Docker ещё не готов, жду'
    Start-Sleep -Seconds 10
}

Invoke-Docker 'info' *> $null
if ($LASTEXITCODE -ne 0) {
    Log 'ОШИБКА: демон Docker не поднялся за 10 минут'
    exit 1
}

# Профиль sim не включаем: имитатор запускают руками.
Invoke-Docker 'compose up -d --remove-orphans'
if ($LASTEXITCODE -ne 0) {
    Log 'ОШИБКА: compose не поднялся'
    exit 1
}

Log 'центр поднят'
