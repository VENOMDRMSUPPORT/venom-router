# dev.ps1 — Venom Router Dev Server Manager
# Usage: .\dev.ps1 [status|start|stop|restart|logs]
param(
    [ValidateSet('status','start','stop','restart','logs')]
    [string]$Action = 'status'
)

$ROOT    = Split-Path $PSScriptRoot -Parent
$DEV_DIR = "$ROOT\.dev"
if (-not (Test-Path $DEV_DIR)) {
    $null = New-Item -ItemType Directory -Path $DEV_DIR -Force
    if ($env:OS -like "*Windows*") {
        (Get-Item $DEV_DIR).Attributes = 'Hidden'
    }
}
$PIDFILE = "$DEV_DIR\dev.pid"
$OUTLOG  = "$DEV_DIR\dev.out"
$ERRLOG  = "$DEV_DIR\dev.err"
$PORT    = 8081

function Find-ServerPid {
    if (Test-Path $PIDFILE) {
        $raw = Get-Content $PIDFILE -Raw -ErrorAction SilentlyContinue
        $p = if ($raw) { $raw.Trim() } else { '' }
        if ($p -match '^\d+$' -and (Get-Process -Id ([int]$p) -ErrorAction SilentlyContinue)) {
            return [int]$p
        }
        Remove-Item $PIDFILE -Force -ErrorAction SilentlyContinue
    }
    $lines = & netstat -ano 2>&1
    foreach ($line in $lines) {
        if ($line -match "TCP\s+\S+:$PORT\s+\S+\s+LISTENING\s+(\d+)") {
            return [int]$Matches[1]
        }
    }
    return $null
}

function Test-Up {
    try {
        $null = Invoke-WebRequest "http://localhost:$PORT" -TimeoutSec 2 -UseBasicParsing -ErrorAction Stop
        return $true
    } catch { return $false }
}

function Do-Stop {
    $devPid = Find-ServerPid
    if ($devPid) {
        Write-Host "  Stopping PID $devPid..." -ForegroundColor DarkGray
        Stop-Process -Id $devPid -Force -ErrorAction SilentlyContinue
        Start-Sleep -Milliseconds 700
    }
    $lines = & netstat -ano 2>&1
    foreach ($line in $lines) {
        if ($line -match "TCP\s+\S+:$PORT\s+\S+\s+LISTENING\s+(\d+)") {
            Stop-Process -Id ([int]$Matches[1]) -Force -ErrorAction SilentlyContinue
        }
    }
    Remove-Item $PIDFILE -Force -ErrorAction SilentlyContinue
}

function Do-Start {
    $proc = Start-Process "cmd.exe" `
                -ArgumentList "/c bun dev > `"$OUTLOG`" 2>`"$ERRLOG`"" `
                -WorkingDirectory $ROOT `
                -WindowStyle Hidden -PassThru
    $proc.Id | Set-Content $PIDFILE
    Write-Host "  Waiting for http://localhost:$PORT" -NoNewline -ForegroundColor DarkGray
    for ($i = 0; $i -lt 25; $i++) {
        Start-Sleep 1
        Write-Host "." -NoNewline -ForegroundColor DarkGray
        if (Test-Up) {
            Write-Host " Ready!" -ForegroundColor Green
            return $proc.Id
        }
    }
    Write-Host " Timed out!" -ForegroundColor Red
    return $null
}

switch ($Action) {
    'status' {
        $devPid = Find-ServerPid
        if ($devPid -and (Test-Up)) {
            Write-Host "RUNNING  PID $devPid  ->  http://localhost:$PORT" -ForegroundColor Green
        } elseif ($devPid) {
            Write-Host "STARTING  PID $devPid  (not responding yet)" -ForegroundColor Yellow
        } else {
            Write-Host "STOPPED" -ForegroundColor Red
        }
    }
    'start' {
        $devPid = Find-ServerPid
        if ($devPid -and (Test-Up)) {
            Write-Host "Already running  PID $devPid  ->  http://localhost:$PORT" -ForegroundColor Green
            exit 0
        }
        Write-Host "Starting dev server..." -ForegroundColor Cyan
        $newPid = Do-Start
        if (-not $newPid) { exit 1 }
        Write-Host "Started  PID $newPid  ->  http://localhost:$PORT" -ForegroundColor Green
    }
    'stop' {
        Write-Host "Stopping dev server..." -ForegroundColor Cyan
        Do-Stop
        Write-Host "Stopped." -ForegroundColor Yellow
    }
    'restart' {
        Write-Host "Restarting dev server..." -ForegroundColor Cyan
        Do-Stop
        $newPid = Do-Start
        if (-not $newPid) { exit 1 }
        Write-Host "Restarted  PID $newPid  ->  http://localhost:$PORT" -ForegroundColor Green
    }
    'logs' {
        Write-Host "--- stdout (last 40 lines) ---" -ForegroundColor DarkGray
        if (Test-Path $OUTLOG) { Get-Content $OUTLOG -Tail 40 } else { Write-Host "(no output log)" }
        Write-Host "--- stderr (last 10 lines) ---" -ForegroundColor DarkGray
        if (Test-Path $ERRLOG) { Get-Content $ERRLOG -Tail 10 } else { Write-Host "(no error log)" }
    }
}
