# gl-show.ps1: the visual GPU test, over Wi-Fi. Watch the tablet: for 6 seconds the GPU shows
# Margin's screen moved down, then the screen is red for 4 seconds. Restart the tablet after.
#   gl-show.cmd [ip]
param([string]$Ip = "")
$ipFile = "$HOME\.margin\tablet-ip"
$ipOld = "$HOME\.condor\tablet-ip" # where it was kept before the rename to Margin
if ($Ip -eq "") { foreach ($f in $ipFile, $ipOld) { if (Test-Path $f) { $Ip = (Get-Content $f -Raw).Trim(); break } } }
if ($Ip -notmatch '^\d+\.\d+\.\d+\.\d+$') { Write-Host "Which IP? gl-test.cmd 192.168.x.y" -ForegroundColor Red; exit 1 }
$ssh = @('-o', 'ConnectTimeout=8', '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=accept-new')
$bin = Join-Path $PSScriptRoot 'os\margin-gl\glanim'
$ErrorActionPreference = 'Continue'
# The latest test first (GitHub is often slow to answer from this PC: a few tries).
Set-Location $PSScriptRoot
for ($i = 1; $i -le 10; $i++) {
    git pull --no-edit 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { break }
    Write-Host "git pull failed, trying again ($i)" -ForegroundColor Yellow
    Start-Sleep 5
}
& scp @ssh $bin (Join-Path $PSScriptRoot 'os\margin-gl\gl-show.sh') "root@${Ip}:/tmp/"
if ($LASTEXITCODE -ne 0) { Write-Host "copy failed: is the tablet on Wi-Fi at ${Ip}?" -ForegroundColor Red; exit 1 }
Write-Host "Watch the tablet now: 6 s of Margin's screen moved down, then 4 s of red." -ForegroundColor Cyan
& ssh @ssh "root@$Ip" "sh /tmp/gl-show.sh"
