#Requires -Version 5.1
<#
.SYNOPSIS
    Builds the EAMI Agent MSI installer using WiX Toolset v4.

.DESCRIPTION
    Compiles Product.wxs into a signed, self-contained MSI for Windows x64.
    WiX v4 is installed automatically via dotnet tool if not already present.

    Prerequisites:
      - .NET SDK 6 or later (https://dotnet.microsoft.com/download)
      - The agent binary (eami-agent-windows-amd64.exe) must exist.
        Build it first: GOOS=windows GOARCH=amd64 go build -o eami-agent-windows-amd64.exe ./cmd/agent/

.PARAMETER Version
    Semantic version string for the MSI (must match Major.Minor.Patch[.Build]).
    Example: 1.2.3  or  1.2.3.0
    Defaults to 0.0.0-dev (use only for local testing; not a valid release version).

.PARAMETER BinaryPath
    Path to eami-agent-windows-amd64.exe.
    Defaults to ..\eami-agent-windows-amd64.exe relative to this script.

.PARAMETER OutputDir
    Directory where the MSI will be written.
    Defaults to .\dist\ relative to this script.

.PARAMETER SkipWixInstall
    Skip the automatic WiX installation check (useful in CI where WiX is pre-installed).

.EXAMPLE
    # Build from repo root (binary already cross-compiled)
    .\eami-agent\installer\build.ps1 -Version 1.0.0

.EXAMPLE
    # Explicit paths
    .\eami-agent\installer\build.ps1 `
        -Version 1.2.3 `
        -BinaryPath "C:\builds\eami-agent-windows-amd64.exe" `
        -OutputDir  "C:\dist"

.NOTES
    The produced MSI is named: eami-agent-<Version>-windows-amd64.msi

    Silent install:
      msiexec /i eami-agent-1.0.0-windows-amd64.msi /qn `
          COLLECTOR_URL=https://collector.corp.internal:8888 `
          COLLECTOR_API_KEY=eami_k_abc123

    Silent uninstall:
      msiexec /x eami-agent-1.0.0-windows-amd64.msi /qn
#>
param(
    [string] $Version         = "0.0.0-dev",
    [string] $BinaryPath      = "",
    [string] $OutputDir       = "",
    [switch] $SkipWixInstall
)

$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path

# ── Resolve defaults ───────────────────────────────────────────────────────
if (-not $BinaryPath) {
    $BinaryPath = Join-Path $ScriptDir "..\eami-agent-windows-amd64.exe"
}
if (-not $OutputDir) {
    $OutputDir = Join-Path $ScriptDir "dist"
}

# Resolve and validate binary path
try {
    $BinaryPath = (Resolve-Path $BinaryPath).Path
} catch {
    Write-Error @"
Agent binary not found at: $BinaryPath

Build it first:
  cd eami-agent
  `$Env:GOOS='windows'; `$Env:GOARCH='amd64'; `$Env:CGO_ENABLED='0'
  go build -ldflags='-w -s' -o eami-agent-windows-amd64.exe ./cmd/agent/
"@
    exit 1
}

# ── Validate version format (WiX requires Major.Minor.Patch[.Build]) ───────
# Strip leading 'v' if present (common from git tags)
$Version = $Version -replace '^v', ''

# For dev builds that append a SHA suffix, truncate to dotted-decimal only
# e.g. "0.0.0-abc1234" → "0.0.0" for the MSI, but keep full string in filename
$MsiVersion  = ($Version -split '-')[0]   # "1.2.3" or "0.0.0"
$FileVersion = $Version                    # "1.2.3" or "0.0.0-dev"

if ($MsiVersion -notmatch '^\d+\.\d+\.\d+(\.\d+)?$') {
    Write-Error "Version '$MsiVersion' is not valid WiX version format (must be Major.Minor.Patch[.Build])."
    exit 1
}

# ── Ensure WiX v4 is installed ────────────────────────────────────────────
if (-not $SkipWixInstall) {
    if (-not (Get-Command "wix" -ErrorAction SilentlyContinue)) {
        Write-Host "WiX Toolset v4 not found. Installing via dotnet tool..." -ForegroundColor Yellow

        if (-not (Get-Command "dotnet" -ErrorAction SilentlyContinue)) {
            Write-Error ".NET SDK is required. Download from https://dotnet.microsoft.com/download"
            exit 1
        }

        dotnet tool install --global wix
        if ($LASTEXITCODE -ne 0) {
            Write-Error "Failed to install WiX Toolset. Check dotnet SDK version (requires 6+)."
            exit 1
        }

        # Refresh PATH so 'wix' is available in this session
        $Env:PATH = [System.Environment]::GetEnvironmentVariable("PATH", "Machine") + ";" +
                    [System.Environment]::GetEnvironmentVariable("PATH", "User")
    }
}

# ── Verify wix is available ───────────────────────────────────────────────
$WixVersion = & wix --version 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Error "wix command not found after install. Ensure ~/.dotnet/tools is in PATH."
    exit 1
}
Write-Host "WiX version: $WixVersion" -ForegroundColor DarkGray

# ── Build ─────────────────────────────────────────────────────────────────
$WxsFile   = Join-Path $ScriptDir "Product.wxs"
$OutputMsi = Join-Path $OutputDir "eami-agent-$FileVersion-windows-amd64.msi"

New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null

Write-Host ""
Write-Host "Building EAMI Agent MSI" -ForegroundColor Cyan
Write-Host "  WiX source : $WxsFile"
Write-Host "  Binary     : $BinaryPath"
Write-Host "  Version    : $MsiVersion (file: $FileVersion)"
Write-Host "  Output     : $OutputMsi"
Write-Host ""

wix build $WxsFile `
    -arch x64 `
    -d "Version=$MsiVersion" `
    -d "BinaryPath=$BinaryPath" `
    -o $OutputMsi

if ($LASTEXITCODE -ne 0) {
    Write-Error "WiX build failed (exit code $LASTEXITCODE)."
    exit $LASTEXITCODE
}

$MsiSize = [math]::Round((Get-Item $OutputMsi).Length / 1MB, 1)
Write-Host ""
Write-Host "MSI built successfully ($MsiSize MB)" -ForegroundColor Green
Write-Host "  $OutputMsi"
Write-Host ""
Write-Host "Silent install:" -ForegroundColor Cyan
Write-Host "  msiexec /i `"$OutputMsi`" /qn ``"
Write-Host "      COLLECTOR_URL=https://collector.corp.internal:8888 ``"
Write-Host "      COLLECTOR_API_KEY=eami_k_your_key_here"
Write-Host ""
Write-Host "Silent uninstall:"
Write-Host "  msiexec /x `"$OutputMsi`" /qn"
Write-Host ""
