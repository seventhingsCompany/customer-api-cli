# Install the seventhings CLI on Windows.
#
#   irm https://raw.githubusercontent.com/SeventhingsCompany/customer-api-cli/main/install.ps1 | iex
#
# Environment variables:
#   SEVENTHINGS_VERSION       release tag, default: latest
#   SEVENTHINGS_INSTALL_DIR   default: %LOCALAPPDATA%\Programs\seventhings
#   SEVENTHINGS_DOWNLOAD_URL  overrides the releases URL (mirrors, testing)
#
# The download is checked against checksums.txt. The install directory is
# added to the user PATH.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue' # much faster Invoke-WebRequest

$repoUrl = 'https://github.com/SeventhingsCompany/customer-api-cli'
$baseUrl = if ($env:SEVENTHINGS_DOWNLOAD_URL) { $env:SEVENTHINGS_DOWNLOAD_URL } else { "$repoUrl/releases" }
$version = if ($env:SEVENTHINGS_VERSION) { $env:SEVENTHINGS_VERSION } else { 'latest' }
$installDir = if ($env:SEVENTHINGS_INSTALL_DIR) { $env:SEVENTHINGS_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\seventhings' }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { throw "Unsupported architecture $($env:PROCESSOR_ARCHITECTURE) (amd64 and arm64 are available)" }
}

if ($version -eq 'latest') {
    $releaseUrl = "$baseUrl/latest/download"
} else {
    if (-not $version.StartsWith('v')) { $version = "v$version" }
    $releaseUrl = "$baseUrl/download/$version"
}

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("seventhings-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Host "Downloading checksums from $releaseUrl ..."
    $checksums = Join-Path $tmp 'checksums.txt'
    Invoke-WebRequest -UseBasicParsing -Uri "$releaseUrl/checksums.txt" -OutFile $checksums

    # The checksum list names the archive, which carries the version number.
    $suffix = "_windows_$arch.zip"
    $entry = Get-Content $checksums | ForEach-Object {
        $parts = $_ -split '\s+'
        if ($parts.Count -ge 2 -and $parts[1].StartsWith('seventhings_') -and $parts[1].EndsWith($suffix)) {
            [pscustomobject]@{ Hash = $parts[0]; File = $parts[1] }
        }
    } | Select-Object -First 1
    if (-not $entry) { throw "No archive for windows/$arch in this release" }

    Write-Host "Downloading $($entry.File) ..."
    $archive = Join-Path $tmp $entry.File
    Invoke-WebRequest -UseBasicParsing -Uri "$releaseUrl/$($entry.File)" -OutFile $archive
    $actual = (Get-FileHash -Algorithm SHA256 $archive).Hash.ToLowerInvariant()
    if ($actual -ne $entry.Hash.ToLowerInvariant()) {
        throw "Checksum mismatch for $($entry.File) (expected $($entry.Hash), got $actual)"
    }

    $extract = Join-Path $tmp 'x'
    Expand-Archive -Path $archive -DestinationPath $extract
    New-Item -ItemType Directory -Force -Path $installDir | Out-Null
    Copy-Item -Force (Join-Path $extract 'seventhings.exe') (Join-Path $installDir 'seventhings.exe')
    $completions = Join-Path $extract 'completions\seventhings.ps1'
    if (Test-Path $completions) { Copy-Item -Force $completions (Join-Path $installDir 'seventhings-completion.ps1') }
    Write-Host "Installed $(Join-Path $installDir 'seventhings.exe')"

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not (($userPath -split ';') -contains $installDir)) {
        [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ";$installDir").TrimStart(';'), 'User')
        $env:Path = "$env:Path;$installDir"
        Write-Host "Added $installDir to your user PATH (open a new terminal to use it)."
    }
    Write-Host ""
    Write-Host "Shell completion: add this line to your PowerShell profile (`$PROFILE):"
    Write-Host "  . '$(Join-Path $installDir 'seventhings-completion.ps1')'"
    Write-Host "Get started: seventhings auth login --url https://<tenant>.seventhings.com"
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
