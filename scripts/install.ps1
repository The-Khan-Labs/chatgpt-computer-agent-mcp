[CmdletBinding()]
param(
    [string]$Repository = $env:COMPUTER_AGENT_REPOSITORY,
    [string]$Version = $env:COMPUTER_AGENT_VERSION,
    [string]$InstallDir = $env:COMPUTER_AGENT_INSTALL_DIR,
    [switch]$Uninstall
)

$ErrorActionPreference = "Stop"

function ConvertTo-NormalizedPathEntry {
    param([string]$Entry)

    if ([string]::IsNullOrWhiteSpace($Entry)) {
        return ""
    }
    $Normalized = [Environment]::ExpandEnvironmentVariables($Entry.Trim())
    while ($Normalized.Length -gt 3 -and ($Normalized.EndsWith("\") -or $Normalized.EndsWith("/"))) {
        $Normalized = $Normalized.Substring(0, $Normalized.Length - 1)
    }
    return $Normalized
}

function Add-DirectoryToPathValue {
    param(
        [AllowNull()][string]$PathValue,
        [Parameter(Mandatory = $true)][string]$Directory
    )

    $NormalizedDirectory = ConvertTo-NormalizedPathEntry $Directory
    foreach ($Entry in @($PathValue -split ";")) {
        if ((ConvertTo-NormalizedPathEntry $Entry) -ieq $NormalizedDirectory) {
            return [pscustomobject]@{ Value = $PathValue; Added = $false }
        }
    }
    $Updated = if ([string]::IsNullOrEmpty($PathValue)) {
        $Directory
    }
    elseif ($PathValue.EndsWith(";")) {
        $PathValue + $Directory
    }
    else {
        $PathValue + ";" + $Directory
    }
    return [pscustomobject]@{ Value = $Updated; Added = $true }
}

if ([string]::IsNullOrWhiteSpace($InstallDir)) {
    $InstallDir = Join-Path $env:LOCALAPPDATA "Programs\ChatGPTComputerAgentMCP"
}
$Target = Join-Path $InstallDir "computer-agent.exe"
if ($Uninstall) {
    Remove-Item -LiteralPath $Target -Force -ErrorAction SilentlyContinue
    Write-Output "Removed $Target; configuration and user PATH were preserved."
    exit 0
}
if ([string]::IsNullOrWhiteSpace($Repository)) {
    throw "COMPUTER_AGENT_REPOSITORY is unset; set it to the published owner/repository before installing."
}
if ([string]::IsNullOrWhiteSpace($Version)) {
    throw "COMPUTER_AGENT_VERSION is required (for example, v1.0.0)."
}

$Architecture = switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { "amd64" }
    "ARM64" { "arm64" }
    default { throw "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}
$Name = "chatgpt-computer-agent-mcp-windows-$Architecture.exe"
$BaseUrl = "https://github.com/$Repository/releases/download/$Version"
$DownloadDir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $DownloadDir | Out-Null
try {
    $Binary = Join-Path $DownloadDir $Name
    $Checksums = Join-Path $DownloadDir "SHA256SUMS"
    Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/$Name" -OutFile $Binary
    Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/SHA256SUMS" -OutFile $Checksums
    $Matches = @(Get-Content -LiteralPath $Checksums | Where-Object { $_ -match "^([0-9a-fA-F]{64})\s+$([regex]::Escape($Name))$" })
    if ($Matches.Count -ne 1) {
        throw "SHA256SUMS does not contain exactly one valid checksum for $Name"
    }
    $Expected = $Matches[0].Split([char[]]" `t", [System.StringSplitOptions]::RemoveEmptyEntries)[0]
    $Actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $Binary).Hash
    if ($Actual -ne $Expected) {
        throw "SHA-256 verification failed for $Name"
    }
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $TemporaryTarget = Join-Path $InstallDir (".computer-agent." + [System.IO.Path]::GetRandomFileName())
    Copy-Item -LiteralPath $Binary -Destination $TemporaryTarget
    Move-Item -LiteralPath $TemporaryTarget -Destination $Target -Force
    Write-Output "Installed $Target"
}
finally {
    Remove-Item -LiteralPath $DownloadDir -Recurse -Force -ErrorAction SilentlyContinue
}

$UserPath = [Environment]::GetEnvironmentVariable("Path", [EnvironmentVariableTarget]::User)
$UserPathUpdate = Add-DirectoryToPathValue -PathValue $UserPath -Directory $InstallDir
if ($UserPathUpdate.Added) {
    [Environment]::SetEnvironmentVariable("Path", $UserPathUpdate.Value, [EnvironmentVariableTarget]::User)
    Write-Output "Added $InstallDir to your user PATH."
}
$ProcessPathUpdate = Add-DirectoryToPathValue -PathValue $env:Path -Directory $InstallDir
if ($ProcessPathUpdate.Added) {
    $env:Path = $ProcessPathUpdate.Value
}
Write-Output "You can now run: computer-agent"
