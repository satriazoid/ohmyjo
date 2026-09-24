# Builds ohmyjo: the React frontend first, then the Go binary.
#
# The linker flags are not optional. Without -H=windowsgui Go emits a
# console-subsystem executable, and Windows then creates a console window for it
# — the application looks like it opened a second terminal next to itself, and
# the startup log lands in that window instead of the log file.

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path

Push-Location $root
try {
    $web = Join-Path $root 'web'
    if (-not (Test-Path (Join-Path $web 'node_modules'))) {
        Write-Host 'installing web dependencies...'
        npm --prefix $web install --no-fund --no-audit
    }
    Write-Host 'building web assets...'
    npm --prefix $web run build

    # Windows takes the taskbar/Alt+Tab icon from the exe's resources, so the
    # .syso has to be present or the binary ships without a mark. It is built
    # from assets/ohmyjo.ico by `rsrc -ico assets/ohmyjo.ico -o rsrc.syso` and
    # committed, because that generation should not run on every build.
    if (-not (Test-Path (Join-Path $root 'rsrc.syso'))) {
        throw 'rsrc.syso is missing: regenerate it with rsrc -ico assets/ohmyjo.ico -o rsrc.syso (see scripts/make-icon.mjs)'
    }

    Write-Host 'building ohmyjo.exe...'
    go build -trimpath -ldflags '-H=windowsgui -s -w' -o ohmyjo.exe .
    Write-Host "done: $root\ohmyjo.exe"
} finally {
    Pop-Location
}
