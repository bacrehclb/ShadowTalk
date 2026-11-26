# Runs only on an ephemeral Windows CI runner; changes its user PATH temporarily.
$ErrorActionPreference = 'Stop'
$installer = (Get-Item "$PSScriptRoot\..\..\dist\installers\*_setup.exe").FullName
$version = (Get-Content "$PSScriptRoot\..\..\dist\metadata.json" | ConvertFrom-Json).version
$directory = Join-Path $env:RUNNER_TEMP 'ShadowTalk тест install'
$originalPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$profile = Join-Path $env:APPDATA 'shadowtalk'
$sentinel = Join-Path $profile 'packaging-sentinel.txt'
New-Item -ItemType Directory -Path $profile -Force | Out-Null
Set-Content -Path $sentinel -Value 'keep profile data'

function Invoke-Installer {
    $process = Start-Process -FilePath $installer -ArgumentList @(
        '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/TASKS=addtopath',
        "/DIR=`"$directory`""
    ) -Wait -PassThru
    if ($process.ExitCode -ne 0) { throw "Setup failed: $($process.ExitCode)" }
}

try {
    # Include a trailing separator to catch false 'already in PATH' decisions.
    $basePath = "$originalPath;C:\ShadowTalk smoke unrelated;"
    [Environment]::SetEnvironmentVariable('Path', $basePath, 'User')
    Invoke-Installer
    $expectedNotices = Get-FileHash "$PSScriptRoot\..\..\dist\THIRD_PARTY_NOTICES.txt" -Algorithm SHA256
    $installedNotices = Get-FileHash "$directory\THIRD_PARTY_NOTICES.txt" -Algorithm SHA256
    if ($expectedNotices.Hash -cne $installedNotices.Hash) { throw 'Installed third-party notices mismatch' }
    $installedPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (@($installedPath.Split(';') | Where-Object { $_ -eq $directory }).Count -ne 1) {
        throw 'PATH entry missing or duplicated'
    }
    Invoke-Installer
    if ([Environment]::GetEnvironmentVariable('Path', 'User') -cne $installedPath) {
        throw 'Reinstallation changed PATH'
    }
    $env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + $installedPath
    Push-Location $env:RUNNER_TEMP
    try {
        $output = & shadowtalk --version
        if ($LASTEXITCODE -ne 0 -or $output -notlike "shadowtalk $version *") {
            throw "Installed version mismatch: $output"
        }
        & shadowtalk --help
        if ($LASTEXITCODE -ne 0) { throw 'Installed help failed' }
        & "$directory\bin\tor-runtime\windows-amd64\tor\tor.exe" --version
        if ($LASTEXITCODE -ne 0) { throw 'Bundled Tor failed' }
        & "$directory\bin\tor-runtime\windows-amd64\tor\pluggable_transports\lyrebird.exe" -version
        if ($LASTEXITCODE -ne 0) { throw 'Bundled Lyrebird failed' }
    } finally { Pop-Location }
    $laterPath = ';C:\Another tool installed later'
    [Environment]::SetEnvironmentVariable('Path', $installedPath + $laterPath, 'User')
    $process = Start-Process -FilePath "$directory\unins000.exe" `
        -ArgumentList '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART' -Wait -PassThru
    if ($process.ExitCode -ne 0) { throw 'Uninstall failed' }
    if (Test-Path "$directory\shadowtalk.exe") { throw 'Uninstall left the binary' }
    if ([Environment]::GetEnvironmentVariable('Path', 'User') -cne ($basePath + $laterPath)) {
        throw 'Uninstall changed unrelated PATH entries'
    }
    if (!(Test-Path $sentinel)) { throw 'Uninstall removed profile data' }
} finally {
    [Environment]::SetEnvironmentVariable('Path', $originalPath, 'User')
    Remove-Item $sentinel -ErrorAction SilentlyContinue
}
