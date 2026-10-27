# gl-probe.ps1: can Margin use Android's own GPU route (SurfaceFlinger, like the boot
# animation)? Watch the tablet while it runs; it also lists SurfaceFlinger's functions.
#   gl-probe.cmd [ip]
param([string]$Ip = "")
$ipFile = "$HOME\.margin\tablet-ip"
$ipOld = "$HOME\.condor\tablet-ip" # where it was kept before the rename to Margin
if ($Ip -eq "") { foreach ($f in $ipFile, $ipOld) { if (Test-Path $f) { $Ip = (Get-Content $f -Raw).Trim(); break } } }
if ($Ip -notmatch '^\d+\.\d+\.\d+\.\d+$') { Write-Host "Which IP? gl-test.cmd 192.168.x.y" -ForegroundColor Red; exit 1 }
$ssh = @('-o', 'ConnectTimeout=8', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=accept-new')
$ErrorActionPreference = 'Continue'
# The latest test first (GitHub is often slow to answer from this PC: a few tries).
Set-Location $PSScriptRoot
for ($i = 1; $i -le 3; $i++) {
    git pull --no-edit 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { break }
    Write-Host "git pull failed, trying again ($i)" -ForegroundColor Yellow
    Start-Sleep 5
}
& scp @ssh (Join-Path $PSScriptRoot 'os\margin-gl\gl-probe.sh') "root@${Ip}:/tmp/"
if ($LASTEXITCODE -ne 0) { Write-Host "copy failed: is the tablet on Wi-Fi at ${Ip}?" -ForegroundColor Red; exit 1 }
& ssh @ssh "root@$Ip" "sh /tmp/gl-probe.sh"
