# gl-test.ps1: can Margin draw with the tablet's GPU? Runs os/margin-gl/gltest on the tablet
# over Wi-Fi (the IP saved by wifi-update) and prints what it measured. Harmless: it stops
# Android's SurfaceFlinger (not used by Margin), draws test colours for a few seconds, and
# puts the screen back. Tap the tablet's screen afterwards to redraw Margin.
#   gl-test.cmd [ip]
param([string]$Ip = "")
$ipFile = "$HOME\.margin\tablet-ip"
$ipOld = "$HOME\.condor\tablet-ip" # where it was kept before the rename to Margin
if ($Ip -eq "") { foreach ($f in $ipFile, $ipOld) { if (Test-Path $f) { $Ip = (Get-Content $f -Raw).Trim(); break } } }
if ($Ip -notmatch '^\d+\.\d+\.\d+\.\d+$') { Write-Host "Which IP? gl-test.cmd 192.168.x.y" -ForegroundColor Red; exit 1 }
$ssh = @('-o', 'ConnectTimeout=8', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=accept-new')
$bin = Join-Path $PSScriptRoot 'os\margin-gl\gltest'
$ErrorActionPreference = 'Continue'
# The latest test first (GitHub is often slow to answer from this PC: a few tries).
Set-Location $PSScriptRoot
for ($i = 1; $i -le 10; $i++) {
    git pull --no-edit 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { break }
    Write-Host "git pull failed, trying again ($i)" -ForegroundColor Yellow
    Start-Sleep 5
}
& scp @ssh $bin (Join-Path $PSScriptRoot 'os\margin-gl\gl-run.sh') "root@${Ip}:/tmp/"
if ($LASTEXITCODE -ne 0) { Write-Host "copy failed: is the tablet on Wi-Fi at ${Ip}?" -ForegroundColor Red; exit 1 }
# The run itself is os/margin-gl/gl-run.sh (in Alpine): gltest runs in Android's root
# through margin-init's root shell (port 2324), with Android's environment, twice (window
# first, then GPU driver first), followed by Android's log and any crash report.
& ssh @ssh "root@$Ip" "sh /tmp/gl-run.sh"
