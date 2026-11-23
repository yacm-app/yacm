$ErrorActionPreference = 'Stop'
$toolsDir = Split-Path -Parent $MyInvocation.MyCommand.Definition
$version = '1.1.0'

$arm = $env:PROCESSOR_ARCHITECTURE -eq 'ARM64' -or $env:PROCESSOR_ARCHITEW6432 -eq 'ARM64'
$arch = if ($arm) { 'arm64' } else { 'x64' }
$sums = @{
  x64   = '1e7b52e9444e4ac23030f732bcc037ac75397f44433f70e62383591cfeb838f0'
  arm64 = '37da834f1f38814c39ac324e3051ac4a31e0196ca735fb50027c86aaa3941a61'
}

Install-ChocolateyZipPackage -PackageName $env:ChocolateyPackageName `
  -UnzipLocation $toolsDir `
  -Url64bit "https://github.com/yacm-app/yacm/releases/download/v$version/yacm-$version-windows-$arch.zip" `
  -Checksum64 $sums[$arch] -ChecksumType64 'sha256'

# YACM is a window, not a console program: the shim starts it and returns.
$exe = Join-Path $toolsDir "yacm-$version-windows-$arch\YACM.exe"
New-Item "$exe.gui" -ItemType File -Force | Out-Null

$programs = [Environment]::GetFolderPath('CommonPrograms')
Install-ChocolateyShortcut -ShortcutFilePath (Join-Path $programs 'YACM.lnk') -TargetPath $exe
