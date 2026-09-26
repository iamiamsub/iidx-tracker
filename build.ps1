# Builds the release: tests, one executable per platform, tracker_link.dll (the game-side DLL,
# built with MSVC, see tracker_link\build.ps1), license notices, a leak scan, zips.
#
#   .\build.ps1                 # version = today's date
#   .\build.ps1 -Version 2.0.0
#
# Always build releases with this script: -trimpath keeps this PC's paths (and user name) out of
# the executables, and the leak scan stops the release if anything personal is still inside.
# The scanner is set per PC in build.local.ps1 (not in the repository):
#
#   $leakScan = "<path to a script that exits non-zero when it finds personal data>"
#   $forbidden = @("<text no released file may contain>", ...)
param([string]$Version = (Get-Date -Format "yyyyMMdd"))
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

$leakScan = $null
$forbidden = @()
if (Test-Path build.local.ps1) { . .\build.local.ps1 }
if (-not $leakScan) { Write-Warning "no leak scanner set in build.local.ps1: the release is not scanned" }

$targets = @(
    @{ os = "windows"; arch = "amd64"; exe = "iidx-tracker.exe" },
    @{ os = "linux";   arch = "amd64"; exe = "iidx-tracker" },
    @{ os = "linux";   arch = "arm64"; exe = "iidx-tracker" }
)

go vet ./...
if ($LASTEXITCODE -ne 0) { throw "go vet failed" }
go test -count=1 ./...
if ($LASTEXITCODE -ne 0) { throw "tests failed" }

if (Test-Path dist) { Remove-Item -Recurse -Force dist }
New-Item -ItemType Directory dist | Out-Null

# tracker_link.dll goes into every package: it runs on the game PC, whatever runs the tracker.
# Built in a child shell, since its build enters the Visual Studio environment.
powershell -NoProfile -ExecutionPolicy Bypass -File tracker_link\build.ps1
if ($LASTEXITCODE -ne 0) { throw "tracker_link build failed" }
$trackerLink = Join-Path $PSScriptRoot "tracker_link\build\tracker_link.dll"

# License notices of everything linked into the executable (read from the module cache).
$notices = [System.Text.StringBuilder]::new()
[void]$notices.AppendLine("IIDX Tracker $Version - third-party notices")
[void]$notices.AppendLine("")
[void]$notices.AppendLine("== kbinxml (binary XML format, ported to Go in internal/eamuse/kbin.go) ==")
[void]$notices.AppendLine((Get-Content -Raw third_party\kbinxml-LICENSE.txt))
[void]$notices.AppendLine("== Go standard library ==")
[void]$notices.AppendLine((Get-Content -Raw (Join-Path (go env GOROOT) "LICENSE")))
$mods = go list -deps -f '{{if .Module}}{{.Module.Path}}|{{.Module.Version}}|{{.Module.Dir}}{{end}}' . |
    Where-Object { $_ -and -not $_.StartsWith("iidx-tracker|") } | Sort-Object -Unique
foreach ($m in $mods) {
    $path, $ver, $dir = $m -split "\|"
    [void]$notices.AppendLine("== $path $ver ==")
    foreach ($f in Get-ChildItem $dir -File | Where-Object { $_.Name -match '^(LICENSE|COPYING|PATENTS)' } | Sort-Object Name) {
        [void]$notices.AppendLine("-- $($f.Name)")
        [void]$notices.AppendLine((Get-Content -Raw $f.FullName))
    }
}
# tracker_link.dll: parts taken from omnifix, and the libraries vcpkg linked in
[void]$notices.AppendLine("== tracker_link.dll: omnifix (memory, module and AVS helpers) ==")
[void]$notices.AppendLine((Get-Content -Raw tracker_link\LICENSE-omnifix.txt))
foreach ($port in "safetyhook", "zydis", "zycore") {
    [void]$notices.AppendLine("== tracker_link.dll: $port ==")
    [void]$notices.AppendLine((Get-Content -Raw "tracker_link\build\vcpkg_installed\x64-windows-static\share\$port\copyright"))
}

$env:CGO_ENABLED = "0"
foreach ($t in $targets) {
    $name = "iidx-tracker-$Version-$($t.os)-$($t.arch)"
    $out = Join-Path $PSScriptRoot "dist\$name"   # absolute: .NET calls ignore Set-Location
    New-Item -ItemType Directory $out | Out-Null
    $env:GOOS = $t.os
    $env:GOARCH = $t.arch
    go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$Version" -o (Join-Path $out $t.exe) .
    if ($LASTEXITCODE -ne 0) { throw "build failed: $name" }
    Copy-Item README.md, README.ja.md, LICENSE $out
    Copy-Item $trackerLink $out
    [System.IO.File]::WriteAllText((Join-Path $out "THIRD_PARTY_NOTICES.txt"), $notices.ToString())
}
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED

# Nothing personal may leave this PC: stop here if the scan finds the user name or a home path,
# or any text build.local.ps1 lists as forbidden.
if ($leakScan) {
    python $leakScan dist
    if ($LASTEXITCODE -ne 0) { throw "leak scan found personal data - release stopped" }
}
foreach ($f in Get-ChildItem dist -Recurse -File) {
    $text = [System.Text.Encoding]::GetEncoding(28591).GetString([System.IO.File]::ReadAllBytes($f.FullName))
    foreach ($word in $forbidden) {
        if ($text.Contains($word)) { throw "$($f.Name) contains forbidden text - release stopped" }
    }
}

foreach ($d in Get-ChildItem dist -Directory) {
    Compress-Archive -Path (Join-Path $d.FullName "*") -DestinationPath "$($d.FullName).zip"
}
if ($leakScan) {
    python $leakScan (Get-ChildItem dist -Filter *.zip).FullName
    if ($LASTEXITCODE -ne 0) { throw "leak scan found personal data in a zip - release stopped" }
}
Write-Host "done: $((Get-ChildItem dist -Filter *.zip).Name -join ', ')"

# The copy this PC runs (next to its config.json and data\). A running tracker locks it.
$local = Join-Path $PSScriptRoot "iidx-tracker.exe"
try {
    Copy-Item (Join-Path $PSScriptRoot "dist\iidx-tracker-$Version-windows-amd64\iidx-tracker.exe") $local -ErrorAction Stop
    Write-Host "updated: iidx-tracker.exe"
} catch {
    Write-Warning "iidx-tracker.exe is in use: stop the tracker, then run build.ps1 again"
}
