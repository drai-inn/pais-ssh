# Installs pais-ssh from its GitHub Releases on Windows, into %LOCALAPPDATA%\Programs\pais-ssh, and adds that folder
# to your user PATH. Run again to upgrade.
#
#   irm https://raw.githubusercontent.com/drai-inn/pais-ssh/main/install.ps1 | iex
#   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/drai-inn/pais-ssh/main/install.ps1))) v0.2.2
#
# Invoke-WebRequest doesn't mark the download as coming from the internet, so SmartScreen doesn't stop the unsigned
# binary.
param([string]$Version)

# A function, so that `irm | iex`, which runs in your session, doesn't leave its settings and variables there.
function Install-PaisSsh([string]$Version) {
    $ErrorActionPreference = 'Stop'
    # The progress bar makes Invoke-WebRequest very slow in Windows PowerShell.
    $ProgressPreference = 'SilentlyContinue'

    $repo = 'https://github.com/drai-inn/pais-ssh'
    $dir = Join-Path $env:LOCALAPPDATA 'Programs\pais-ssh'
    $exe = Join-Path $dir 'pais-ssh.exe'

    $arch = switch ($env:PROCESSOR_ARCHITECTURE) {
        'AMD64' { 'amd64' }
        'ARM64' { 'arm64' }
        default { throw "pais-ssh install: unsupported processor $env:PROCESSOR_ARCHITECTURE" }
    }
    $url = if ($Version) { "$repo/releases/download/$Version" } else { "$repo/releases/latest/download" }
    $archive = "pais-ssh_windows_$arch.zip"

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("pais-ssh-" + [guid]::NewGuid())
    New-Item -ItemType Directory $tmp | Out-Null
    try {
        Write-Host "Downloading $url/$archive"
        Invoke-WebRequest -UseBasicParsing "$url/$archive" -OutFile "$tmp\$archive"
        Invoke-WebRequest -UseBasicParsing "$url/checksums.txt" -OutFile "$tmp\checksums.txt"

        $want = Get-Content "$tmp\checksums.txt" | ForEach-Object {
            $sum, $name = $_ -split '\s+'
            if ($name -eq $archive) { $sum }
        }
        $got = (Get-FileHash -Algorithm SHA256 "$tmp\$archive").Hash.ToLower()
        if (-not $want -or $want -ne $got) { throw "pais-ssh install: checksum mismatch for $archive" }

        Expand-Archive "$tmp\$archive" -DestinationPath "$tmp\x"
        New-Item -ItemType Directory -Force $dir | Out-Null
        # A running pais-ssh.exe can't be replaced, but it can be renamed out of the way.
        if (Test-Path $exe) { Move-Item -Force $exe "$exe.old" }
        Move-Item "$tmp\x\pais-ssh.exe" $exe
        Remove-Item -Force "$exe.old" -ErrorAction SilentlyContinue
    } finally {
        Remove-Item -Recurse -Force $tmp
    }
    Write-Host "Installed pais-ssh $(& $exe version) in $dir"

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (($userPath -split ';') -notcontains $dir) {
        [Environment]::SetEnvironmentVariable('Path', $(if ($userPath) { "$userPath;$dir" } else { $dir }), 'User')
        $env:Path += ";$dir"
        Write-Host "Added $dir to your user PATH (new terminals pick it up)."
    }
    $other = (Get-Command pais-ssh -ErrorAction SilentlyContinue).Source
    if ($other -and $other -ne $exe) {
        Write-Host "Note: $other comes first on your PATH. Remove it, or ~/.ssh/config may keep using it."
    }

    Write-Host ""
    Write-Host "Next: pais-ssh setup   (run it again after an upgrade, to rewrite its ~/.ssh/config block)"
}

Install-PaisSsh $Version
