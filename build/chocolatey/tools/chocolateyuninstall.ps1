$ErrorActionPreference = 'Stop'
$programs = [Environment]::GetFolderPath('CommonPrograms')
Remove-Item (Join-Path $programs 'YACM.lnk') -Force -ErrorAction SilentlyContinue
