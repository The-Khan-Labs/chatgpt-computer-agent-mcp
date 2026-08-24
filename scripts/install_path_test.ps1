param(
    [Parameter(Mandatory = $true)][string]$Installer
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

function Assert-Equal {
    param($Actual, $Expected, [string]$Message)

    if ($Actual -cne $Expected) {
        throw "$Message (actual='$Actual', expected='$Expected')"
    }
}

function Assert-True {
    param([bool]$Condition, [string]$Message)

    if (-not $Condition) {
        throw $Message
    }
}

$Tokens = $null
$ParseErrors = $null
$Ast = [System.Management.Automation.Language.Parser]::ParseFile($Installer, [ref]$Tokens, [ref]$ParseErrors)
if ($ParseErrors.Count -ne 0) {
    throw "install.ps1 has parse errors: $($ParseErrors -join '; ')"
}
$FunctionNames = @("ConvertTo-NormalizedPathEntry", "Add-DirectoryToPathValue")
$FunctionAsts = @($Ast.FindAll({
    param($Node)
    $Node -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $FunctionNames -contains $Node.Name
}, $true))
Assert-Equal $FunctionAsts.Count $FunctionNames.Count "installer PATH helper count"
foreach ($FunctionAst in $FunctionAsts) {
    Invoke-Expression $FunctionAst.Extent.Text
}

$Directory = "C:\Users\Beginner\AppData\Local\Programs\ChatGPTComputerAgentMCP"
$Unrelated = "C:\Windows;C:\Tools"

$Absent = Add-DirectoryToPathValue -PathValue $Unrelated -Directory $Directory
Assert-True $Absent.Added "absent install directory was not added"
Assert-Equal $Absent.Value "$Unrelated;$Directory" "unrelated entries were not preserved"

$Present = Add-DirectoryToPathValue -PathValue "$Unrelated;$Directory" -Directory $Directory
Assert-True (-not $Present.Added) "existing install directory was added again"
Assert-Equal $Present.Value "$Unrelated;$Directory" "existing PATH was rewritten"

$CaseEquivalent = Add-DirectoryToPathValue -PathValue "$Unrelated;$($Directory.ToUpperInvariant())" -Directory $Directory
Assert-True (-not $CaseEquivalent.Added) "case-insensitive equivalent was duplicated"

$TrailingEquivalent = Add-DirectoryToPathValue -PathValue "$Unrelated;$Directory\" -Directory $Directory
Assert-True (-not $TrailingEquivalent.Added) "trailing-slash equivalent was duplicated"

$ExistingDuplicates = "$Unrelated;$Directory;$($Directory.ToUpperInvariant())\"
$DuplicateResult = Add-DirectoryToPathValue -PathValue $ExistingDuplicates -Directory $Directory
Assert-True (-not $DuplicateResult.Added) "a PATH containing equivalents gained another duplicate"
Assert-Equal $DuplicateResult.Value $ExistingDuplicates "pre-existing PATH entries were rewritten"

$FirstInstall = Add-DirectoryToPathValue -PathValue $Unrelated -Directory $Directory
$SecondInstall = Add-DirectoryToPathValue -PathValue $FirstInstall.Value -Directory $Directory
Assert-True (-not $SecondInstall.Added) "repeated install was not idempotent"
Assert-Equal $SecondInstall.Value $FirstInstall.Value "repeated install changed PATH"

$SessionPath = Add-DirectoryToPathValue -PathValue "C:\SessionOnly" -Directory $Directory
Assert-True $SessionPath.Added "current-session PATH was not updated"
Assert-True ($SessionPath.Value.EndsWith(";$Directory")) "current-session PATH is missing install directory"

foreach ($EmptyPath in @($null, "")) {
    $EmptyResult = Add-DirectoryToPathValue -PathValue $EmptyPath -Directory $Directory
    Assert-True $EmptyResult.Added "empty or missing user PATH did not accept install directory"
    Assert-Equal $EmptyResult.Value $Directory "empty or missing user PATH produced the wrong value"
}

$ExpandedDirectory = Join-Path $env:LOCALAPPDATA "Programs\ChatGPTComputerAgentMCP"
$VariableEquivalent = Add-DirectoryToPathValue -PathValue "%LOCALAPPDATA%\Programs\ChatGPTComputerAgentMCP" -Directory $ExpandedDirectory
Assert-True (-not $VariableEquivalent.Added) "environment-variable equivalent was duplicated"

$Source = Get-Content -LiteralPath $Installer -Raw
Assert-True ($Source.Contains('[EnvironmentVariableTarget]::User')) "installer does not explicitly target user PATH"
Assert-True (-not $Source.Contains('[EnvironmentVariableTarget]::Machine')) "installer references machine PATH"
Assert-True ($Source.Contains('$env:Path = $ProcessPathUpdate.Value')) "installer does not refresh current-session PATH"
Assert-True ($Source.IndexOf('Move-Item -LiteralPath $TemporaryTarget', [StringComparison]::Ordinal) -lt $Source.IndexOf('$UserPath = [Environment]::GetEnvironmentVariable', [StringComparison]::Ordinal)) "PATH update moved ahead of verified atomic replacement"

$UninstallRoot = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
$ConfigRoot = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $UninstallRoot, $ConfigRoot | Out-Null
try {
    $Binary = Join-Path $UninstallRoot "computer-agent.exe"
    $Config = Join-Path $ConfigRoot "config.json"
    Set-Content -LiteralPath $Binary -Value "fixture"
    Set-Content -LiteralPath $Config -Value "preserve"
    $UserPathBefore = [Environment]::GetEnvironmentVariable("Path", [EnvironmentVariableTarget]::User)
    $MachinePathBefore = [Environment]::GetEnvironmentVariable("Path", [EnvironmentVariableTarget]::Machine)
    $PowerShell = (Get-Process -Id $PID).Path
    $Output = & $PowerShell -NoProfile -NonInteractive -File $Installer -InstallDir $UninstallRoot -Uninstall | Out-String
    Assert-True (-not (Test-Path -LiteralPath $Binary)) "uninstall left the binary behind"
    Assert-True (Test-Path -LiteralPath $Config) "uninstall removed configuration"
    Assert-Equal ([Environment]::GetEnvironmentVariable("Path", [EnvironmentVariableTarget]::User)) $UserPathBefore "uninstall changed user PATH"
    Assert-Equal ([Environment]::GetEnvironmentVariable("Path", [EnvironmentVariableTarget]::Machine)) $MachinePathBefore "uninstall changed machine PATH"
    Assert-True ($Output.Contains("configuration and user PATH were preserved")) "uninstall did not explain PATH preservation"
}
finally {
    Remove-Item -LiteralPath $UninstallRoot, $ConfigRoot -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Output "PowerShell installer PATH fixture passed."
