[CmdletBinding()]
param(
    [string]$To = $env:REPLOY_INSTALL_DIR,
    [string]$Version = $env:REPLOY_VERSION,
    [switch]$AddToPath,
    [switch]$NoPathUpdate,
    [switch]$Help
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$Repo = "omry/reploy"

function Write-Usage {
    @"
Usage: install.ps1 [-To DIR] [-Version VERSION] [-AddToPath] [-NoPathUpdate]

Downloads the Reploy Windows binary from GitHub Releases and installs it to:
  %LOCALAPPDATA%\Programs\Reploy\bin\reploy.exe

Options:
  -To DIR         Install into DIR instead of the default user bin directory
  -Version VALUE  Install VERSION instead of the repo VERSION
  -AddToPath      Add the install directory to the current user's PATH
  -NoPathUpdate   Do not print PATH update guidance
  -Help           Show this help
"@
}

function Get-TargetArch {
    $windowsArch = [Environment]::GetEnvironmentVariable("PROCESSOR_ARCHITEW6432")
    if ([string]::IsNullOrWhiteSpace($windowsArch)) {
        $windowsArch = [Environment]::GetEnvironmentVariable("PROCESSOR_ARCHITECTURE")
    }
    if ([string]::IsNullOrWhiteSpace($windowsArch)) {
        throw "could not determine Windows architecture"
    }
    switch ($windowsArch.Trim().ToUpperInvariant()) {
        "AMD64" { return "amd64" }
        "X64" { return "amd64" }
        "ARM64" { return "arm64" }
        default {
            throw "unsupported Windows architecture: $windowsArch"
        }
    }
}

function Test-NativeWindows {
    return [System.Environment]::OSVersion.Platform -eq [System.PlatformID]::Win32NT
}

function Get-ReployVersion {
    param([string]$RequestedVersion)

    if (-not [string]::IsNullOrWhiteSpace($RequestedVersion)) {
        return $RequestedVersion.Trim()
    }
    $versionUrl = "https://raw.githubusercontent.com/$Repo/main/VERSION"
    return (Invoke-RestMethod -Uri $versionUrl -UseBasicParsing).Trim()
}

function Get-Tag {
    param([string]$ResolvedVersion)

    if ($ResolvedVersion.StartsWith("v")) {
        return $ResolvedVersion
    }
    return "v$ResolvedVersion"
}

function Save-ReleaseAsset {
    param(
        [string]$Url,
        [string]$OutFile,
        [string]$Tag,
        [string]$Asset
    )

    try {
        Invoke-WebRequest -Uri $Url -OutFile $OutFile -UseBasicParsing -ErrorAction Stop
    } catch {
        $statusCode = $null
        if ($null -ne $_.Exception.Response) {
            try {
                $statusCode = [int]$_.Exception.Response.StatusCode
            } catch {
                $statusCode = $null
            }
        }
        if ($statusCode -eq 404) {
            throw "Reploy release asset was not found: $Asset in $Tag. This release may not include this Windows target yet: $Url"
        }
        throw "failed to download Reploy release asset: $Url. $($_.Exception.Message)"
    }
}

function Get-CanonicalPath {
    param([string]$Path)

    try {
        return [System.IO.Path]::GetFullPath($Path).TrimEnd("\", "/").ToLowerInvariant()
    } catch {
        return $Path.TrimEnd("\", "/").ToLowerInvariant()
    }
}

function Format-PowerShellLiteral {
    param([string]$Value)

    return "'" + $Value.Replace("'", "''") + "'"
}

function Get-DefaultScriptInstallPath {
    if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        return ""
    }
    return Join-Path (Join-Path $env:LOCALAPPDATA "Programs\Reploy\bin") "reploy.exe"
}

function Write-UninstallCommand {
    param([string]$Path)

    Write-Output "     Remove-Item -LiteralPath $(Format-PowerShellLiteral $Path) -Force"
}

function Write-UninstallHint {
    param([string]$Path)

    Write-Output "To uninstall this Reploy command:"
    Write-UninstallCommand -Path $Path
}

function Write-PathCommand {
    param([string]$Directory)

    Write-Output "     `$env:Path = $(Format-PowerShellLiteral "$Directory;") + `$env:Path"
}

function Get-ReployInstallMode {
    param(
        [string]$Path,
        [string]$DefaultScriptInstallPath
    )

    $canonical = Get-CanonicalPath $Path
    if (
        -not [string]::IsNullOrWhiteSpace($DefaultScriptInstallPath) -and
        $canonical -eq (Get-CanonicalPath $DefaultScriptInstallPath)
    ) {
        return "script install default ($DefaultScriptInstallPath)"
    }
    if ($canonical -match "\\\.venv\\scripts\\reploy(\.exe)?$" -or $canonical -match "\\venv\\scripts\\reploy(\.exe)?$") {
        return "Python virtual environment (inferred from path)"
    }
    if ($canonical -match "\\pipx\\venvs\\.*\\scripts\\reploy(\.exe)?$") {
        return "pipx environment (inferred from path)"
    }
    return ""
}

function Write-ReployDetails {
    param(
        [string]$Path,
        [string]$DefaultScriptInstallPath
    )

    $mode = Get-ReployInstallMode -Path $Path -DefaultScriptInstallPath $DefaultScriptInstallPath
    if (-not [string]::IsNullOrWhiteSpace($mode)) {
        Write-Output "Found first installation mode:"
        Write-Output "  $mode"
    }
    Write-Output "Inspect first command manually:"
    Write-Output "     & $(Format-PowerShellLiteral $Path) --version"
}

function Test-PathEntry {
    param(
        [string]$PathValue,
        [string]$Entry
    )

    if ([string]::IsNullOrWhiteSpace($PathValue)) {
        return $false
    }
    $wanted = Get-CanonicalPath $Entry
    foreach ($candidate in $PathValue -split [System.IO.Path]::PathSeparator) {
        if ([string]::IsNullOrWhiteSpace($candidate)) {
            continue
        }
        if ((Get-CanonicalPath $candidate) -eq $wanted) {
            return $true
        }
    }
    return $false
}

function Add-UserPathEntry {
    param([string]$Entry)

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if (-not (Test-PathEntry -PathValue $userPath -Entry $Entry)) {
        $separator = [System.IO.Path]::PathSeparator
        if ([string]::IsNullOrWhiteSpace($userPath)) {
            $newUserPath = $Entry
        } else {
            $newUserPath = "$userPath$separator$Entry"
        }
        [Environment]::SetEnvironmentVariable("Path", $newUserPath, "User")
    }

    if (-not (Test-PathEntry -PathValue $env:Path -Entry $Entry)) {
        $env:Path = "$env:Path$([System.IO.Path]::PathSeparator)$Entry"
    }
}

function Test-InstallDirectoryWritable {
    param([string]$Directory)

    $probe = Join-Path $Directory ".reploy-write-test-$([Guid]::NewGuid())"
    try {
        Set-Content -Path $probe -Value "" -NoNewline
    } finally {
        Remove-Item -LiteralPath $probe -Force -ErrorAction SilentlyContinue
    }
}

if ($Help) {
    Write-Usage
    exit 0
}

if ($AddToPath -and $NoPathUpdate) {
    throw "-AddToPath and -NoPathUpdate cannot be used together"
}

if (-not (Test-NativeWindows)) {
    throw "install.ps1 supports native Windows hosts only"
}

if ([string]::IsNullOrWhiteSpace($To)) {
    if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        throw "LOCALAPPDATA is not set; pass -To with a user-writable install directory"
    }
    $To = Join-Path $env:LOCALAPPDATA "Programs\Reploy\bin"
}

$defaultScriptInstallPath = Get-DefaultScriptInstallPath
$arch = Get-TargetArch
$target = "windows-$arch"
$resolvedVersion = Get-ReployVersion -RequestedVersion $Version
if ([string]::IsNullOrWhiteSpace($resolvedVersion)) {
    throw "could not resolve Reploy version"
}
$tag = Get-Tag -ResolvedVersion $resolvedVersion
$asset = "reploy-$target.exe"
$sourceUrl = "https://github.com/$Repo/releases/download/$tag/$asset"
$targetPath = Join-Path $To "reploy.exe"
$tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) "reploy-install-$([Guid]::NewGuid())"
$tmpFile = Join-Path $tmpDir "reploy.exe"

Write-Output "Installing Reploy"
Write-Output "Version: $tag"
Write-Output "Platform: $target"
Write-Output "Source: $sourceUrl"
Write-Output "Target: $targetPath"
Write-Output ""

New-Item -ItemType Directory -Path $To -Force | Out-Null
Test-InstallDirectoryWritable -Directory $To
New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null

try {
    Save-ReleaseAsset -Url $sourceUrl -OutFile $tmpFile -Tag $tag -Asset $asset
    Move-Item -LiteralPath $tmpFile -Destination $targetPath -Force
} finally {
    Remove-Item -LiteralPath $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Output ""
Write-Output "Installed:"
Write-Output "  $targetPath"
Write-Output ""

try {
    & $targetPath --version
} catch {
    Write-Warning "installed binary did not report a version: $_"
}

Write-Output ""
Write-UninstallHint -Path $targetPath

if ($AddToPath) {
    Add-UserPathEntry -Entry $To
    Write-Output ""
    Write-Output "Added install directory to the current user's PATH:"
    Write-Output "  $To"
    Write-Output "Restart already-open shells to pick up the user PATH update."
}

if (-not $NoPathUpdate) {
    $resolved = Get-Command "reploy.exe" -ErrorAction SilentlyContinue
    if ($null -eq $resolved) {
        Write-Output ""
        Write-Output "reploy.exe is not on PATH."
        Write-Output "Options:"
        Write-Output "  1. Correct PATH so this install is found:"
        Write-Output "     Rerun with -AddToPath, or add this directory to the current user's PATH:"
        Write-Output "     $To"
        Write-Output "  2. Uninstall the command installed by this script:"
        Write-UninstallCommand -Path $targetPath
    } elseif ((Get-CanonicalPath $resolved.Source) -ne (Get-CanonicalPath $targetPath)) {
        Write-Output ""
        Write-Output "The installed reploy.exe is not the first reploy.exe on PATH."
        Write-Output "Installed:"
        Write-Output "  $targetPath"
        Write-Output "Found first on PATH:"
        Write-Output "  $($resolved.Source)"
        Write-ReployDetails -Path $resolved.Source -DefaultScriptInstallPath $defaultScriptInstallPath
        Write-Output "Options:"
        Write-Output "  1. Uninstall the command installed by this script:"
        Write-UninstallCommand -Path $targetPath
        Write-Output "  2. Correct PATH so this install is used first:"
        Write-Output "     Move this directory earlier in the current user's PATH:"
        Write-Output "     $To"
        Write-Output "     Or update this shell now:"
        Write-PathCommand -Directory $To
    }
}
