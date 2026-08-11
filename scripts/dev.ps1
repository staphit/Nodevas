<#
.SYNOPSIS
  Rebuilds the app and, optionally, restarts it.

.DESCRIPTION
  The binary embeds web/dist at compile time, so a frontend change is only
  visible after both builds run in this order:

      npm run build   (tsc + vite → web/dist)
      go build        (embeds web/dist into nodevas.exe)

  Forgetting the second step is why a fixed UI can still look broken: the
  running server is serving the bundle from whenever it was last compiled.

.EXAMPLE
  ./scripts/dev.ps1                                      # build web, no repo exe
  ./scripts/dev.ps1 -Run -Project "$HOME\NodevasWorkspace" -Port 5666 # build and run via go run
  ./scripts/dev.ps1 -BuildBinary -Run                   # explicitly build nodevas.exe
  ./scripts/dev.ps1 -SkipWeb                            # Go-only change
  ./scripts/dev.ps1 -Test                               # also run the test suites
#>
param(
    # Skip the frontend build (Go-only changes).
    [switch]$SkipWeb,
    # Stop any running instance and start a fresh one when the build succeeds.
    [switch]$Run,
    # Explicitly create nodevas.exe. By default -Run uses go run and leaves no
    # executable in the repository.
    [switch]$BuildBinary,
    # Run go test / vitest / playwright before building.
    [switch]$Test,
    # Workspace directory to serve. Defaults to the last one the app opened.
    [string]$Project = "",
    [int]$Port = 5666
)

$ErrorActionPreference = "Stop"
$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo

function Step($message) {
    Write-Host "==> $message" -ForegroundColor Cyan
}

# Stop any running Nodevas server instance.
$serverNames = @("nodevas")

function Stop-Server {
    $running = Get-Process -Name $serverNames -ErrorAction SilentlyContinue
    if ($running) {
        Step "stopping $($running.Count) running instance(s)"
        $running | Stop-Process -Force
        # Windows keeps the image locked for a moment after the process exits.
        Start-Sleep -Milliseconds 400
    }
}

function Stop-PortOwner([int]$TargetPort) {
    $listeners = @(Get-NetTCPConnection -State Listen -LocalPort $TargetPort -ErrorAction SilentlyContinue)
    $pids = @($listeners | Select-Object -ExpandProperty OwningProcess -Unique)
    foreach ($processId in $pids) {
        $process = Get-Process -Id $processId -ErrorAction SilentlyContinue
        if (-not $process) { continue }
        if ($serverNames -notcontains $process.ProcessName) {
            throw "port $TargetPort is already used by PID $processId ($($process.ProcessName)); stop it or choose -Port"
        }
        Step "stopping $($process.ProcessName) PID $processId on port $TargetPort"
        Stop-Process -Id $processId -Force
    }

    for ($attempt = 0; $attempt -lt 20; $attempt++) {
        if (-not (Get-NetTCPConnection -State Listen -LocalPort $TargetPort -ErrorAction SilentlyContinue)) {
            return
        }
        Start-Sleep -Milliseconds 100
    }
    throw "port $TargetPort is still occupied after stopping the server"
}

if ($Test) {
    Step "go test"
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw "go test failed" }

    Step "vitest"
    npm --prefix web run test
    if ($LASTEXITCODE -ne 0) { throw "vitest failed" }
}

if (-not $SkipWeb) {
    Step "building web (tsc + vite)"
    npm --prefix web run build
    if ($LASTEXITCODE -ne 0) { throw "web build failed" }
}

$arguments = @("serve", "--port", $Port)
if ($Project) { $arguments += @("--project", $Project) }

if ($Run) {
    Stop-PortOwner $Port
} elseif ($BuildBinary) {
    # A running server can hold the standalone binary lock.
    Stop-Server
}

if ($BuildBinary) {
    Step "building nodevas.exe (embeds web/dist)"
    go build -tags nomsgpack -o nodevas.exe ./cmd/nodevas
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }

    $built = Get-Item (Join-Path $repo "nodevas.exe")
    Write-Host "    nodevas.exe  $([math]::Round($built.Length / 1MB, 1)) MB  $($built.LastWriteTime.ToString('HH:mm:ss'))"

    if ($Run) {
        Step "serving nodevas.exe on http://127.0.0.1:$Port  (Ctrl+C to stop)"
        Write-Host "    hard-reload the page (Ctrl+Shift+R) so the browser drops the old bundle" -ForegroundColor DarkGray
        & (Join-Path $repo "nodevas.exe") @arguments
    } else {
        Write-Host "==> done. nodevas.exe built; start it with: .\nodevas.exe $($arguments -join ' ')" -ForegroundColor Green
    }
} elseif ($Run) {
    Step "serving via go run on http://127.0.0.1:$Port  (Ctrl+C to stop)"
    Write-Host "    no nodevas.exe is created in the repository" -ForegroundColor DarkGray
    Write-Host "    hard-reload the page (Ctrl+Shift+R) so the browser drops the old bundle" -ForegroundColor DarkGray
    go run ./cmd/nodevas @arguments
} else {
    Write-Host "==> web build done. No nodevas.exe was created." -ForegroundColor Green
}
