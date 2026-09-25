# Builds ohmyjo: a single native Go binary, no frontend toolchain involved.
#
# The linker flags are not optional. Without -H=windowsgui Go emits a
# console-subsystem executable, and Windows then creates a console window for it
# The application then looks like it opened a second terminal next to itself, and
# the startup log lands in that window instead of the log file.

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path

Push-Location $root
try {
    # Windows takes the taskbar and Alt+Tab icon from the executable's resources,
    # so the binary needs rsrc.syso beside it. The generated file is committed so
    # an ordinary build does not depend on the rsrc tool being installed, but it
    # is regenerated whenever the source icon is newer: a stale resource is
    # invisible, ships the wrong mark, and is otherwise impossible to notice.
    $ico = Join-Path $root 'assets\ohmyjo.ico'
    $syso = Join-Path $root 'rsrc.syso'
    if (-not (Test-Path $ico)) {
        throw "icon is missing: $ico"
    }

    $stale = $true
    if (Test-Path $syso) {
        $stale = (Get-Item $ico).LastWriteTimeUtc -gt (Get-Item $syso).LastWriteTimeUtc
    }
    if ($stale) {
        $rsrc = (Get-Command rsrc -ErrorAction SilentlyContinue).Source
        if (-not $rsrc) {
            $rsrc = Join-Path (Join-Path (go env GOPATH) 'bin') 'rsrc.exe'
        }
        if (-not (Test-Path $rsrc)) {
            throw "rsrc is needed to rebuild rsrc.syso from $ico. Install it with: go install github.com/akavel/rsrc@latest"
        }
        Write-Host "regenerating rsrc.syso from $ico..."
        & $rsrc -ico $ico -o $syso
    }

    Write-Host 'building ohmyjo.exe...'
    go build -trimpath -ldflags '-H=windowsgui -s -w' -o ohmyjo.exe .
    Write-Host "done: $root\ohmyjo.exe"
} finally {
    Pop-Location
}
