Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

if ($MyInvocation.InvocationName -eq ".") {
    Write-Error "Do not dot-source this script; run it as .\scripts\start_codex_with_moonbridge.ps1 to avoid polluting your shell."
    return
}

param(
    [Parameter(Mandatory = $true)]
    [string]$ProjectDirectory,

    [Parameter(Mandatory = $false)]
    [string]$CodexHome = $(if ($env:CODEX_HOME) { $env:CODEX_HOME } else { Join-Path $HOME ".codex" })
)

$RootDir = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$ConfigFile = if ($env:MOONBRIDGE_CONFIG) { $env:MOONBRIDGE_CONFIG } else { Join-Path $RootDir "config.yml" }
$CodexHomeDir = $CodexHome
$ServerBin = Join-Path $RootDir ".cache\start-codex\moonbridge.exe"
$LogFile = Join-Path $RootDir "logs\moonbridge-codex.log"
$CodexProcess = $null
$CodexWatcherProcess = $null

New-Item -ItemType Directory -Force -Path (Split-Path -Parent $LogFile) | Out-Null
Set-Content -LiteralPath $LogFile -Value "" -NoNewline

function Write-Log {
    param([Parameter(Mandatory = $true)][string]$Message)

    Write-Host $Message
    Add-Content -LiteralPath $LogFile -Value $Message
}

function Write-LogError {
    param([Parameter(Mandatory = $true)][string]$Message)

    [Console]::Error.WriteLine($Message)
    Add-Content -LiteralPath $LogFile -Value $Message
}

function Require-Command {
    param([Parameter(Mandatory = $true)][string]$Name)

    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        Write-LogError "missing required command: $Name"
        exit 1
    }
}

function ConvertTo-NativeArgument {
    param([AllowNull()][string]$Argument)

    if ($null -eq $Argument) {
        return '""'
    }
    if ($Argument -notmatch '[\s"]') {
        return $Argument
    }

    $result = '"'
    $backslashes = 0
    foreach ($char in $Argument.ToCharArray()) {
        if ($char -eq '\') {
            $backslashes++
            continue
        }
        if ($char -eq '"') {
            $result += ('\' * ($backslashes * 2 + 1))
            $result += '"'
            $backslashes = 0
            continue
        }
        if ($backslashes -gt 0) {
            $result += ('\' * $backslashes)
            $backslashes = 0
        }
        $result += $char
    }
    if ($backslashes -gt 0) {
        $result += ('\' * ($backslashes * 2))
    }
    $result += '"'
    return $result
}

function ConvertTo-PowerShellLiteral {
    param([AllowNull()][string]$Value)

    if ($null -eq $Value) {
        return "''"
    }
    return "'" + $Value.Replace("'", "''") + "'"
}

function Invoke-NativeCapture {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments
    )

    $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $FilePath
    $startInfo.Arguments = (($Arguments | ForEach-Object { ConvertTo-NativeArgument $_ }) -join " ")
    $startInfo.WorkingDirectory = $RootDir
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true

    $process = [System.Diagnostics.Process]::new()
    $process.StartInfo = $startInfo
    if (-not $process.Start()) {
        throw "failed to start $FilePath"
    }

    $stdout = $process.StandardOutput.ReadToEnd()
    $stderr = $process.StandardError.ReadToEnd()
    $process.WaitForExit()

    if (-not [string]::IsNullOrWhiteSpace($stderr)) {
        Add-Content -LiteralPath $LogFile -Value ($stderr.TrimEnd() -split "`r?`n")
    }

    return [pscustomobject]@{
        ExitCode = $process.ExitCode
        Stdout = $stdout
        Stderr = $stderr
    }
}

function Parse-Addr {
    param([Parameter(Mandatory = $true)][string]$Addr)

    if ($Addr.StartsWith(":")) {
        $port = $Addr.Substring(1)
        return [pscustomobject]@{
            Host = "127.0.0.1"
            Port = [int]$port
            BaseAddr = "127.0.0.1:$port"
        }
    }

    $lastColon = $Addr.LastIndexOf(":")
    if ($lastColon -lt 0) {
        throw "invalid server address: $Addr"
    }

    return [pscustomobject]@{
        Host = $Addr.Substring(0, $lastColon)
        Port = [int]$Addr.Substring($lastColon + 1)
        BaseAddr = $Addr
    }
}

function Test-TcpPort {
    param(
        [Parameter(Mandatory = $true)][string]$HostName,
        [Parameter(Mandatory = $true)][int]$Port
    )

    $client = [System.Net.Sockets.TcpClient]::new()
    try {
        $connect = $client.BeginConnect($HostName, $Port, $null, $null)
        if (-not $connect.AsyncWaitHandle.WaitOne(200)) {
            return $false
        }
        $client.EndConnect($connect)
        return $true
    } catch {
        return $false
    } finally {
        $client.Close()
    }
}

function Stop-StaleMoonBridgeOnPort {
    param(
        [Parameter(Mandatory = $true)][string]$HostName,
        [Parameter(Mandatory = $true)][int]$Port
    )

    if (-not (Get-Command Get-NetTCPConnection -ErrorAction SilentlyContinue)) {
        return
    }

    $serverBinPath = (Resolve-Path -LiteralPath $ServerBin -ErrorAction SilentlyContinue).Path
    if (-not $serverBinPath) {
        return
    }

    $connections = Get-NetTCPConnection -LocalAddress $HostName -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
    foreach ($connection in $connections) {
        $process = Get-Process -Id $connection.OwningProcess -ErrorAction SilentlyContinue
        if (-not $process) {
            continue
        }

        $processPath = $null
        try {
            $processPath = $process.Path
        } catch {
            $processPath = $null
        }

        if ($processPath -eq $serverBinPath) {
            Write-Log "Stopping stale Moon Bridge process $($process.Id) on ${HostName}:$Port"
            Stop-Process -Id $process.Id -ErrorAction SilentlyContinue
            $process.WaitForExit(5000) | Out-Null
            Write-Log "Stale Moon Bridge process $($process.Id) stopped"
        }
    }
}

function Ensure-PortFree {
    param(
        [Parameter(Mandatory = $true)][string]$Addr,
        [Parameter(Mandatory = $true)][string]$HostName,
        [Parameter(Mandatory = $true)][int]$Port
    )

    if (Test-TcpPort -HostName $HostName -Port $Port) {
        Write-LogError "port already in use: $Addr"
        Write-LogError "change server.addr in config.yml, or stop the process using $Addr"
        Write-LogError "Moon Bridge log: $LogFile"
        exit 1
    }
}


function Get-CodexLaunchCommand {
    $command = Get-Command codex -ErrorAction Stop
    $source = $command.Source

    if ([string]::IsNullOrWhiteSpace($source)) {
        return [pscustomobject]@{
            FilePath = "codex"
            PrefixArguments = @()
        }
    }

    if ([System.IO.Path]::GetExtension($source) -ieq ".ps1") {
        $cmdShim = [System.IO.Path]::ChangeExtension($source, ".cmd")
        if (Test-Path -LiteralPath $cmdShim -PathType Leaf) {
            return [pscustomobject]@{
                FilePath = $cmdShim
                PrefixArguments = @()
            }
        }

        return [pscustomobject]@{
            FilePath = (Get-Command powershell.exe -ErrorAction Stop).Source
            PrefixArguments = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $source)
        }
    }

    return [pscustomobject]@{
        FilePath = $source
        PrefixArguments = @()
    }
}

function Stop-ProcessTree {
    param([Parameter(Mandatory = $true)][int]$ProcessId)

    $children = Get-CimInstance Win32_Process -Filter "ParentProcessId = $ProcessId" -ErrorAction SilentlyContinue
    foreach ($child in $children) {
        Stop-ProcessTree -ProcessId $child.ProcessId
    }

    $process = Get-Process -Id $ProcessId -ErrorAction SilentlyContinue
    if ($process) {
        Stop-Process -Id $ProcessId -ErrorAction SilentlyContinue
    }
}

function Stop-CodexTerminal {
    if (-not $script:CodexProcess) {
        return
    }

    $process = Get-Process -Id $script:CodexProcess.Id -ErrorAction SilentlyContinue
    if (-not $process) {
        return
    }

    Write-Log "Stopping Codex terminal process $($script:CodexProcess.Id)"
    Stop-ProcessTree -ProcessId $script:CodexProcess.Id
    Write-Log "Codex terminal process $($script:CodexProcess.Id) stopped"
}

function Start-CodexCleanupWatcher {
    param([Parameter(Mandatory = $true)][System.Diagnostics.Process]$Process)

    $escapedLogFile = $LogFile.Replace("'", "''")
    $watcherScript = @"
`$ParentPid = $PID
`$CodexPid = $($Process.Id)
`$LogFile = '$escapedLogFile'

function Add-LogLine {
    param([string]`$Message)
    try {
        Add-Content -LiteralPath `$LogFile -Value `$Message
    } catch {
    }
}

function Stop-Tree {
    param([int]`$ProcessId)
    `$children = Get-CimInstance Win32_Process -Filter "ParentProcessId = `$ProcessId" -ErrorAction SilentlyContinue
    foreach (`$child in `$children) {
        Stop-Tree -ProcessId `$child.ProcessId
    }
    `$process = Get-Process -Id `$ProcessId -ErrorAction SilentlyContinue
    if (`$process) {
        Stop-Process -Id `$ProcessId -ErrorAction SilentlyContinue
    }
}

while (`$true) {
    `$parent = Get-Process -Id `$ParentPid -ErrorAction SilentlyContinue
    `$codex = Get-Process -Id `$CodexPid -ErrorAction SilentlyContinue

    if (-not `$codex) {
        break
    }

    if (-not `$parent) {
        Add-LogLine ("Moon Bridge launcher exited; stopping Codex terminal process " + `$CodexPid)
        Stop-Tree -ProcessId `$CodexPid
        Add-LogLine ("Codex terminal process " + `$CodexPid + " stopped by watcher")
        break
    }

    Start-Sleep -Seconds 1
}
"@

    $encodedScript = [Convert]::ToBase64String([System.Text.Encoding]::Unicode.GetBytes($watcherScript))
    return Start-Process `
        -FilePath (Get-Command powershell.exe -ErrorAction Stop).Source `
        -ArgumentList @("-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-EncodedCommand", $encodedScript) `
        -WindowStyle Hidden `
        -PassThru
}

function Start-CodexTerminal {
    param(
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$HostName,
        [Parameter(Mandatory = $true)][int]$Port
    )

    $launch = Get-CodexLaunchCommand
    $allArguments = @($launch.PrefixArguments) + $Arguments
    $argumentLiteralList = ($allArguments | ForEach-Object { ConvertTo-PowerShellLiteral $_ }) -join ", "
    $clientApiKey = if ($env:MOONBRIDGE_CLIENT_API_KEY) { $env:MOONBRIDGE_CLIENT_API_KEY } else { "local-dev" }
    $logFileLiteral = ConvertTo-PowerShellLiteral $LogFile

    $codexScript = @"
`$env:CODEX_HOME = $(ConvertTo-PowerShellLiteral $CodexHomeDir)
`$env:MOONBRIDGE_CLIENT_API_KEY = $(ConvertTo-PowerShellLiteral $clientApiKey)
Set-Location -LiteralPath $(ConvertTo-PowerShellLiteral $RootDir)
`$logFile = $logFileLiteral
function Add-LauncherLog {
    param([string]`$Message)
    try {
        Add-Content -LiteralPath `$logFile -Value `$Message
    } catch {
    }
}
Write-Host "Starting Codex with CODEX_HOME=$CodexHomeDir"
Write-Host 'Workspace: $RootDir'
Write-Host 'Mode: $Mode'
Write-Host 'Model: $ModelAlias'
Add-LauncherLog 'Codex terminal opened'
Write-Host 'Waiting for Moon Bridge at ${HostName}:$Port'
`$deadline = (Get-Date).AddSeconds(30)
while ((Get-Date) -lt `$deadline) {
    `$client = [System.Net.Sockets.TcpClient]::new()
    try {
        `$connect = `$client.BeginConnect($(ConvertTo-PowerShellLiteral $HostName), $Port, `$null, `$null)
        if (`$connect.AsyncWaitHandle.WaitOne(200)) {
            `$client.EndConnect(`$connect)
            break
        }
    } catch {
    } finally {
        `$client.Close()
    }
    Start-Sleep -Milliseconds 200
}
`$codexArgs = @($argumentLiteralList)
& $(ConvertTo-PowerShellLiteral $launch.FilePath) @codexArgs
`$codexStatus = `$LASTEXITCODE
Write-Host "Codex exited with status `$codexStatus"
Add-LauncherLog "Codex exited with status `$codexStatus"
exit `$codexStatus
"@

    $encodedScript = [Convert]::ToBase64String([System.Text.Encoding]::Unicode.GetBytes($codexScript))
    return Start-Process `
        -FilePath (Get-Command powershell.exe -ErrorAction Stop).Source `
        -ArgumentList @("-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-EncodedCommand", $encodedScript) `
        -WorkingDirectory $RootDir `
        -PassThru
}

Require-Command go
Require-Command codex

if (-not (Test-Path -LiteralPath $ConfigFile -PathType Leaf)) {
    Write-LogError "missing config file: $ConfigFile"
    Write-LogError "copy config.example.yml to config.yml and fill provider settings"
    exit 1
}

New-Item -ItemType Directory -Force -Path $CodexHomeDir | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $RootDir ".cache\go-build") | Out-Null
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $ServerBin) | Out-Null

# Config is passed via --config flag, not env var
if (-not $env:CGO_ENABLED) {
    $env:CGO_ENABLED = "0"
}
if (-not $env:GOCACHE) {
    $env:GOCACHE = Join-Path $RootDir ".cache\go-build"
}

Write-Log "Building Moon Bridge"
Push-Location $RootDir
try {
    & go build -buildvcs=false -o $ServerBin ./cmd/moonbridge 2>&1 | Tee-Object -FilePath $LogFile -Append
} finally {
    Pop-Location
}
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

$modeResult = Invoke-NativeCapture -FilePath $ServerBin -Arguments @("--config", $ConfigFile, "--print-mode")
if ($modeResult.ExitCode -ne 0) {
    exit $modeResult.ExitCode
}
$Mode = $modeResult.Stdout.Trim()
if ($Mode -notin @("Transform", "CaptureResponse")) {
    Write-LogError "config.yml mode must be Transform or CaptureResponse for Codex, got: $Mode"
    exit 1
}

$modelResult = Invoke-NativeCapture -FilePath $ServerBin -Arguments @("--config", $ConfigFile, "--print-codex-model")
if ($modelResult.ExitCode -ne 0) {
    exit $modelResult.ExitCode
}
$ModelAlias = $modelResult.Stdout.Trim()
if ([string]::IsNullOrWhiteSpace($ModelAlias)) {
    Write-LogError "provider.default_model or developer.proxy.response.model is required for Codex"
    exit 1
}

$addrResult = Invoke-NativeCapture -FilePath $ServerBin -Arguments @("--config", $ConfigFile, "--print-addr")
if ($addrResult.ExitCode -ne 0) {
    exit $addrResult.ExitCode
}
$Addr = $addrResult.Stdout.Trim()
$ParsedAddr = Parse-Addr -Addr $Addr
Stop-StaleMoonBridgeOnPort -HostName $ParsedAddr.Host -Port $ParsedAddr.Port
Ensure-PortFree -Addr $Addr -HostName $ParsedAddr.Host -Port $ParsedAddr.Port

$codexConfigResult = Invoke-NativeCapture -FilePath $ServerBin -Arguments @(
    "--config", $ConfigFile,
    "--print-codex-config", $ModelAlias,
    "--codex-base-url", "http://$($ParsedAddr.BaseAddr)/v1",
    "--codex-home", $CodexHomeDir
)
if ($codexConfigResult.ExitCode -ne 0) {
    exit $codexConfigResult.ExitCode
}
Set-Content -LiteralPath (Join-Path $CodexHomeDir "config.toml") -Value $codexConfigResult.Stdout -NoNewline

$codexArgs = @(
    "--sandbox", "workspace-write",
    "--ask-for-approval", "on-request",
    "--cd", $ProjectDirectory
)
Write-Log "Starting Codex in a new PowerShell window"
$CodexProcess = Start-CodexTerminal -Arguments $codexArgs -HostName $ParsedAddr.Host -Port $ParsedAddr.Port
$CodexWatcherProcess = Start-CodexCleanupWatcher -Process $CodexProcess

Write-Log "Starting Moon Bridge on $Addr in this terminal"
Write-Log "Moon Bridge log: $LogFile"
Write-Log "Press Ctrl+C in this terminal to stop Moon Bridge and the launched Codex terminal."

Push-Location $RootDir
$MoonBridgeStatus = 0
try {
    & $ServerBin $ServerBin --config $ConfigFile
    $MoonBridgeStatus = $LASTEXITCODE
} finally {
    Stop-CodexTerminal
    if ($CodexWatcherProcess -and -not $CodexWatcherProcess.HasExited) {
        Stop-Process -Id $CodexWatcherProcess.Id -ErrorAction SilentlyContinue
    }
    Pop-Location
}
exit $MoonBridgeStatus
