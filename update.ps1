# update.ps1: get the latest margin code and put it on the tablet, in one step.
# GitHub is often unreachable from this PC, so if it fails a few times this uses the newest
# margin*.bundle in Downloads (a git file Claude can send in chat) instead.
#   .\update.cmd
$repo = $PSScriptRoot # the margin repo: the scripts are at its root
Set-Location $repo

$ok = $false
for ($i = 1; $i -le 4; $i++) {
    git pull --no-edit 2>$null
    if ($LASTEXITCODE -eq 0) { $ok = $true; break }
    Write-Host "GitHub unreachable (try $i of 4), retrying in 10 s..." -ForegroundColor Yellow
    Start-Sleep 10
}
if (-not $ok) {
    $b = Get-ChildItem "$HOME\Downloads\margin*.bundle" -ErrorAction SilentlyContinue |
        Sort-Object LastWriteTime -Descending | Select-Object -First 1
    if (-not $b) {
        Write-Host "GitHub is down and there's no margin*.bundle in Downloads. Ask Claude for a bundle." -ForegroundColor Red
        exit 1
    }
    Write-Host "==> GitHub is down: updating from $($b.Name)" -ForegroundColor Cyan
    git pull --no-edit $b.FullName main
    if ($LASTEXITCODE -ne 0) { Write-Host "update from the bundle failed" -ForegroundColor Red; exit 1 }
}

# Rebuild the margin tool if its source is newer than the installed copy.
$exe = "$HOME\.margin\bin\margin.exe"
$src = Get-ChildItem "$repo\cli\*.go" | Sort-Object LastWriteTime -Descending | Select-Object -First 1
if (-not (Test-Path $exe) -or $src.LastWriteTime -gt (Get-Item $exe).LastWriteTime) {
    Write-Host "==> rebuilding the margin tool" -ForegroundColor Cyan
    Push-Location "$repo\cli"
    go build -o margin.exe .
    .\margin.exe setup
    if ($LASTEXITCODE -ne 0) { Write-Host "close any 'margin term' window and run update again" -ForegroundColor Yellow }
    Pop-Location
}

& "$PSScriptRoot\dev.ps1"
