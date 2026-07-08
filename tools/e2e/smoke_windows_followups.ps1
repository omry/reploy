param(
    [ValidateSet('PathSpaces', 'PortConflict', 'DockerUnavailable')]
    [string[]]$Case = @('PathSpaces', 'PortConflict'),

    [string]$WorkRoot = $env:TEMP,

    [switch]$KeepWorkdir,

    [switch]$Color
)

$ErrorActionPreference = 'Stop'

$ScriptDir = Split-Path -Parent $PSCommandPath
$RepoRoot = Split-Path -Parent (Split-Path -Parent $ScriptDir)

$Arch = $env:PROCESSOR_ARCHITECTURE
if ([string]::IsNullOrWhiteSpace($Arch)) {
    $Arch = $env:PROCESSOR_ARCHITEW6432
}

switch ($Arch.ToUpperInvariant()) {
    'AMD64' { $GoArch = 'amd64' }
    'X64' { $GoArch = 'amd64' }
    'ARM64' { $GoArch = 'arm64' }
    default {
        throw "unsupported smoke host architecture: $Arch"
    }
}

$Target = "windows-$GoArch"
$Reploy = Join-Path $RepoRoot "dist\$Target\reploy.exe"
if (-not (Test-Path -LiteralPath $Reploy -PathType Leaf)) {
    throw "missing built reploy binary: $Reploy; build it first with: python tools/build_reploy --target $Target"
}

$env:PYTHONIOENCODING = 'utf-8:replace'

function Get-SmokePython {
    $Python = Get-Command python -ErrorAction SilentlyContinue
    if ($null -ne $Python) {
        return @($Python.Source)
    }

    $Py = Get-Command py -ErrorAction SilentlyContinue
    if ($null -ne $Py) {
        return @($Py.Source, '-3')
    }

    throw 'python or py is required to run tools\e2e\smoke'
}

function Write-SmokeHostInfo {
    Write-Host "[windows-followups] host: $([System.Environment]::OSVersion.VersionString)"
    Write-Host "[windows-followups] arch: $Arch"
    Write-Host "[windows-followups] reploy: $Reploy"
    $Docker = Get-Command docker -ErrorAction SilentlyContinue
    if ($null -eq $Docker) {
        Write-Host '[windows-followups] docker: not found'
        return
    }
    Write-Host "[windows-followups] docker: $($Docker.Source)"
    docker version --format '{{json .Server}}' 2>$null
    if ($LASTEXITCODE -ne 0) {
        Write-Host '[windows-followups] docker server: unavailable'
        return
    }
    docker context show 2>$null
}

function Invoke-SmokeCase {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Name,
        [Parameter(Mandatory = $true)]
        [string[]]$SmokeArgs
    )

    $Python = @(Get-SmokePython)
    $Smoke = Join-Path $RepoRoot 'tools\e2e\smoke'
    $Stamp = [Guid]::NewGuid().ToString('N').Substring(0, 8)
    $Workdir = Join-Path $WorkRoot "Reploy Smoke $Name $Stamp"
    $ArgsForPython = @()
    if ($Python.Length -gt 1) {
        $ArgsForPython += $Python[1..($Python.Length - 1)]
    }
    $ArgsForPython += @(
        $Smoke,
        '--reploy', $Reploy,
        '--workdir', $Workdir
    )
    if ($Color) {
        $ArgsForPython += '--color'
    }
    $ArgsForPython += $SmokeArgs
    if ($KeepWorkdir) {
        $ArgsForPython += '--keep-workdir'
    }

    $PythonExe = $Python[0]
    Write-Host "[windows-followups] case: $Name"
    Write-Host "[windows-followups] workdir: $Workdir"
    Write-Host ('+ ' + $PythonExe + ' ' + ($ArgsForPython -join ' '))
    $PreviousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    & $PythonExe @ArgsForPython 2>&1 | ForEach-Object { Write-Host $_ }
    $ExitCode = $LASTEXITCODE
    $ErrorActionPreference = $PreviousErrorActionPreference
    if ($ExitCode -ne 0) {
        throw "smoke follow-up case failed: $Name"
    }
}

Write-SmokeHostInfo

foreach ($SelectedCase in $Case) {
    switch ($SelectedCase) {
        'PathSpaces' {
            Invoke-SmokeCase -Name 'Path Spaces' -SmokeArgs @('--runtime', '--persistent-install')
        }
        'PortConflict' {
            Invoke-SmokeCase -Name 'Port Conflict' -SmokeArgs @('--port-conflict-probe')
        }
        'DockerUnavailable' {
            Invoke-SmokeCase -Name 'Docker Unavailable' -SmokeArgs @('--docker-unavailable-probe')
        }
    }
}
