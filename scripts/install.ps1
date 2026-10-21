# Install nrp-mcp on Windows (PowerShell 5.1 or PowerShell 7).
#   irm https://raw.githubusercontent.com/UCR-Research-Computing/nrp-mcp/main/scripts/install.ps1 | iex
# Installs into %LOCALAPPDATA%\Programs\nrp-mcp (no admin rights), verifies SHA256SUMS,
# unblocks the binary, and adds the install folder to the User PATH via
# [Environment]::SetEnvironmentVariable("Path", ..., "User") (never setx).
# Override with $env:NRP_MCP_VERSION, $env:NRP_MCP_BIN, or $env:NRP_MCP_BASE.

$ErrorActionPreference = "Stop"
$Repo = "UCR-Research-Computing/nrp-mcp"
$Bin = if ($env:NRP_MCP_BIN) { $env:NRP_MCP_BIN } else { Join-Path $env:LOCALAPPDATA "Programs\nrp-mcp" }

$archRaw = $env:PROCESSOR_ARCHITECTURE
if ($env:PROCESSOR_ARCHITEW6432) { $archRaw = $env:PROCESSOR_ARCHITEW6432 }
switch -Regex ($archRaw) {
    "^(AMD64|X86_64)$" { $Arch = "amd64" }
    "^ARM64$"          { $Arch = "arm64" }
    default            { throw "Unsupported CPU architecture: $archRaw" }
}

$Ver = $env:NRP_MCP_VERSION
if (-not $Ver) {
    try {
        $resp = Invoke-WebRequest -Uri "https://github.com/$Repo/releases/latest" -MaximumRedirection 0 -ErrorAction SilentlyContinue
        if ($resp.Headers.Location -and "$($resp.Headers.Location)" -match "/tag/([^/?#]+)") {
            $Ver = $Matches[1]
        }
    } catch {
        if ($_.Exception.Response -and $_.Exception.Response.Headers -and $_.Exception.Response.Headers["Location"]) {
            $loc = "$($_.Exception.Response.Headers["Location"])"
            if ($loc -match "/tag/([^/?#]+)") { $Ver = $Matches[1] }
        }
    }
}
if (-not $Ver) {
    $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers @{ "User-Agent" = "nrp-mcp-install" }
    $Ver = $rel.tag_name
}
if (-not $Ver) { throw "Could not find the latest nrp-mcp release" }

$VerNum = $Ver -replace "^v", ""
$Name = "nrp-mcp_${VerNum}_windows_${Arch}"
$Base = if ($env:NRP_MCP_BASE) { $env:NRP_MCP_BASE } else { "https://github.com/$Repo/releases/download/$Ver" }

$Tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("nrp-mcp-" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $Tmp | Out-Null
try {
    $ZipPath = Join-Path $Tmp "$Name.zip"
    $SumPath = Join-Path $Tmp "SHA256SUMS"
    Write-Host "Downloading nrp-mcp $Ver for windows/$Arch ..."
    if ($Base -match "^https?://") {
        Invoke-WebRequest -Uri "$Base/$Name.zip" -OutFile $ZipPath -UseBasicParsing
        Invoke-WebRequest -Uri "$Base/SHA256SUMS" -OutFile $SumPath -UseBasicParsing
    } else {
        Copy-Item -Path (Join-Path $Base "$Name.zip") -Destination $ZipPath -Force
        Copy-Item -Path (Join-Path $Base "SHA256SUMS") -Destination $SumPath -Force
    }

    $Want = $null
    foreach ($line in (Get-Content -Path $SumPath)) {
        if ($line -match "^([0-9a-fA-F]{64})\s+\*?$([Regex]::Escape("$Name.zip"))\s*$") {
            $Want = $Matches[1].ToLowerInvariant()
            break
        }
    }
    $Got = (Get-FileHash -Path $ZipPath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ((-not $Want) -or ($Want -ne $Got)) {
        throw "SHA256 mismatch for $Name.zip (got $Got, want $Want); not installing"
    }

    Expand-Archive -Path $ZipPath -DestinationPath $Tmp -Force
    New-Item -ItemType Directory -Force -Path $Bin | Out-Null
    $SrcExe = Join-Path (Join-Path $Tmp $Name) "nrp-mcp.exe"
    if (-not (Test-Path $SrcExe)) {
        $SrcExe = Join-Path $Tmp "nrp-mcp.exe"
    }
    $DstExe = Join-Path $Bin "nrp-mcp.exe"
    Copy-Item -Path $SrcExe -Destination $DstExe -Force
    Unblock-File -Path $DstExe -ErrorAction SilentlyContinue
} finally {
    Remove-Item -Recurse -Force -Path $Tmp -ErrorAction SilentlyContinue
}

$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
$Parts = @()
if ($UserPath) {
    $Parts = $UserPath -split ";" | Where-Object { $_ -ne "" }
}
$Already = $false
foreach ($p in $Parts) {
    if ($p.TrimEnd("\") -ieq $Bin.TrimEnd("\")) {
        $Already = $true
        break
    }
}
if (-not $Already) {
    $NewUserPath = if ($UserPath) { "$UserPath;$Bin" } else { $Bin }
    [Environment]::SetEnvironmentVariable("Path", $NewUserPath, "User")
    Write-Host "Added $Bin to your User PATH."
}
if (($env:Path -split ";") -notcontains $Bin) {
    $env:Path = "$Bin;$env:Path"
}

$VerOut = & $DstExe version
Write-Host "Installed $DstExe ($VerOut)"
Write-Host "Binary path: $DstExe"
Write-Host "Next: download your NRP config at https://nrp.ai/config, then run: `"$DstExe`" setup"
