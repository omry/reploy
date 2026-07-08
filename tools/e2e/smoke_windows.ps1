$ErrorActionPreference = 'Stop'

$ScriptDir = Split-Path -Parent $PSCommandPath
$RepoRoot = Split-Path -Parent (Split-Path -Parent $ScriptDir)
$ScriptArgs = $args

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

function Invoke-SmokePython {
    param(
        [Parameter(Mandatory = $true)]
        [string]$PythonPath,
        [string[]]$PrefixArgs = @()
    )

    $Smoke = Join-Path $RepoRoot 'tools\e2e\smoke'
    $SmokeArgs = @($Smoke) + $PrefixArgs + @('--reploy', $Reploy, '--color', '--runtime', '--persistent-install') + $ScriptArgs
    Write-Host ('+ ' + $PythonPath + ' ' + ($SmokeArgs -join ' '))
    $PreviousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    & $PythonPath @SmokeArgs 2>&1 | ForEach-Object { Write-Host $_ }
    $ExitCode = $LASTEXITCODE
    $ErrorActionPreference = $PreviousErrorActionPreference
    return $ExitCode
}

$Python = Get-Command python -ErrorAction SilentlyContinue
if ($null -ne $Python) {
    $ExitCode = Invoke-SmokePython -PythonPath $Python.Source
    if ($ExitCode -ne 0) {
        throw "smoke failed with exit code $ExitCode"
    }
    return
}

$Py = Get-Command py -ErrorAction SilentlyContinue
if ($null -ne $Py) {
    $ExitCode = Invoke-SmokePython -PythonPath $Py.Source -PrefixArgs @('-3')
    if ($ExitCode -ne 0) {
        throw "smoke failed with exit code $ExitCode"
    }
    return
}

throw 'python or py is required to run tools\e2e\smoke'
