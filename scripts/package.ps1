param(
    [ValidatePattern('^[A-Za-z0-9][A-Za-z0-9.+_-]*$')][string]$Version = 'dev',
    [string]$Go = 'go'
)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Push-Location $projectRoot
try {
    foreach ($arch in @('amd64', 'arm64')) {
        & "$PSScriptRoot/build.ps1" -Arch $arch -Version $Version -Go $Go
        $stage = Join-Path $projectRoot "dist/Samuel-$Version-windows-$arch"
        New-Item -ItemType Directory -Force -Path $stage | Out-Null
        Copy-Item -LiteralPath "dist/Samuel-windows-$arch.exe" -Destination (Join-Path $stage 'Samuel.exe')
        Copy-Item -LiteralPath README.md, LICENSE -Destination $stage
        Copy-Item -LiteralPath docs -Destination $stage -Recurse -Force
        Copy-Item -LiteralPath assets/Samuel.ico -Destination $stage -Force
        $previewAssets = Join-Path $stage 'assets'
        New-Item -ItemType Directory -Force -Path $previewAssets | Out-Null
        Get-ChildItem -LiteralPath assets -Filter 'fluent-*.png' | Copy-Item -Destination $previewAssets -Force
        Get-ChildItem -LiteralPath assets -Filter 'capitalization-*.png' | Copy-Item -Destination $previewAssets -Force
        Get-ChildItem -LiteralPath assets -Filter 'valuation-*.png' | Copy-Item -Destination $previewAssets -Force
        $zip = "$stage.zip"
        Compress-Archive -Path "$stage/*" -DestinationPath $zip -Force
        Get-FileHash -LiteralPath $zip -Algorithm SHA256 | Select-Object Path, Hash
    }
    $sourceStage = Join-Path $projectRoot "dist/Samuel-$Version-source"
    New-Item -ItemType Directory -Force -Path $sourceStage | Out-Null
    foreach ($sourcePath in @('.gitattributes', '.gitignore', 'go.mod', 'LICENSE', 'README.md', '.github', 'assets', 'cmd', 'internal', 'scripts', 'docs')) {
        Copy-Item -LiteralPath $sourcePath -Destination $sourceStage -Recurse -Force
    }
    $sourceZip = "$sourceStage.zip"
    Compress-Archive -Path "$sourceStage/*" -DestinationPath $sourceZip -Force
    Get-FileHash -LiteralPath $sourceZip -Algorithm SHA256 | Select-Object Path, Hash
} finally {
    Pop-Location
}
