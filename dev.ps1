# dev.ps1: build margin-init on this PC and run it on the tablet, in one step.
# Use it after every change:  .\dev.cmd   (the tablet must already be in takeover mode;
# if it's in Android, it arms the takeover and reboots instead).
$ErrorActionPreference = 'Stop'
$src = Join-Path $PSScriptRoot 'os\margin-init'
Push-Location $src
try {
    $env:GOOS = 'linux'; $env:GOARCH = '386'; $env:CGO_ENABLED = '0'
    Write-Host '==> building margin-init' -ForegroundColor Cyan
    go build -o margin-init .
    if ($LASTEXITCODE -ne 0) { throw 'build failed' }
} finally {
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
}
try {
    $status = margin takeover status | Out-String
    if ($status -match 'running:\s+yes') {
        Write-Host '==> installing and restarting on the tablet' -ForegroundColor Cyan
        margin takeover push margin-init
        margin takeover restart
    } else {
        Write-Host '==> tablet is in Android: arming the takeover and rebooting' -ForegroundColor Cyan
        margin takeover arm margin-init
        margin reboot
    }
} finally {
    Pop-Location
}
Write-Host '==> done. Look at the tablet, or type:  margin term' -ForegroundColor Green
