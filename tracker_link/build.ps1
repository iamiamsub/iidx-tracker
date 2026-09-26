# Builds tracker_link.dll with MSVC and vcpkg: the one in VCPKG_ROOT when it is set (the GitHub
# workflow clones its own), else the one that ships with Visual Studio (C++ workload):
#
#   .\build.ps1        # -> build\tracker_link.dll
#
# Dependencies (safetyhook, zydis) come from vcpkg.json and are linked statically, as is the
# C runtime, so the DLL needs nothing but the game's avs2-core.dll and Windows.
$ErrorActionPreference = "Stop"

$vswhere = Join-Path ${env:ProgramFiles(x86)} "Microsoft Visual Studio\Installer\vswhere.exe"
$vs = & $vswhere -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath
if (-not $vs) { throw "Visual Studio with the C++ tools was not found" }

# Read before entering the Visual Studio shell, which may set VCPKG_ROOT to its own copy.
$vcpkg = if ($env:VCPKG_ROOT) { $env:VCPKG_ROOT } else { Join-Path $vs "VC\vcpkg" }

Import-Module (Join-Path $vs "Common7\Tools\Microsoft.VisualStudio.DevShell.dll")
Enter-VsDevShell -VsInstallPath $vs -SkipAutomaticLocation -DevCmdArguments "-arch=x64 -host_arch=x64" | Out-Null

Set-Location $PSScriptRoot

cmake -B build -S . -G Ninja `
    -DCMAKE_BUILD_TYPE=Release `
    -DVCPKG_TARGET_TRIPLET=x64-windows-static `
    -DCMAKE_MSVC_RUNTIME_LIBRARY=MultiThreaded `
    -DCMAKE_TOOLCHAIN_FILE="$vcpkg\scripts\buildsystems\vcpkg.cmake"
if ($LASTEXITCODE -ne 0) { throw "configure failed ($LASTEXITCODE)" }

cmake --build build
if ($LASTEXITCODE -ne 0) { throw "build failed ($LASTEXITCODE)" }

$dll = Get-Item build\tracker_link.dll
Write-Output "BUILT: tracker_link.dll ($([math]::Round($dll.Length / 1kb)) KB)"
