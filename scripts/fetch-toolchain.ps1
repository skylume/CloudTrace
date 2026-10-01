#Requires -Version 5.1
<#
.SYNOPSIS
    下载并解包 Win7 线的 Go 工具链（社区分支 go-legacy-win7）。

.DESCRIPTION
    官方 Go 自 1.21 起终止 Win7 支持，社区分支把支持补了回来。
    它必须与主构建使用的官方 Go 完全隔离成两个 GOROOT，否则会互相污染。

    下载后自动定位 bin/go.exe 所在目录，并把 GOROOT 写入 GOLEGACY_ROOT 环境变量：
      - 在 GitHub Actions 里会追加到 $GITHUB_ENV / $GITHUB_PATH，供后续步骤使用；
      - 在本地只打印路径，由调用方自行设置。

.PARAMETER Version
    发布版本号（对应 tag v<Version>）。缺省 1.27.1-1，与设计锁定的 go1.27.1 对应。

.PARAMETER Dest
    解包目标目录。缺省用 $env:RUNNER_TEMP（CI）或仓库同级 .toolchain（本地）。

.PARAMETER Sha256
    可选的 zip 校验值；提供时会校验，不匹配即失败。
#>
[CmdletBinding()]
param(
    [string]$Version = '1.27.1-1',

    [string]$Dest,

    [string]$Sha256 = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$assetName = "go-legacy-win7-$Version.windows_amd64.zip"
$url = "https://github.com/thongtech/go-legacy-win7/releases/download/v$Version/$assetName"

if ([string]::IsNullOrWhiteSpace($Dest)) {
    if ($env:RUNNER_TEMP) {
        $Dest = Join-Path $env:RUNNER_TEMP "go-legacy-win7-$Version"
    }
    else {
        $Dest = Join-Path (Split-Path -Parent $PSScriptRoot) ".toolchain/go-legacy-win7-$Version"
    }
}

Write-Host "==> 下载 Win7 工具链：$url"
New-Item -ItemType Directory -Force -Path $Dest | Out-Null
$zipPath = Join-Path $Dest $assetName

# 大文件下载：进度条会拖慢速度，关掉。
$prevProgress = $ProgressPreference
$ProgressPreference = 'SilentlyContinue'
try {
    Invoke-WebRequest -Uri $url -OutFile $zipPath -UseBasicParsing
}
finally {
    $ProgressPreference = $prevProgress
}

if ($Sha256) {
    $actual = (Get-FileHash -Path $zipPath -Algorithm SHA256).Hash
    if ($actual -ne $Sha256.ToUpperInvariant()) {
        throw "校验失败：期望 $Sha256，实际 $actual"
    }
    Write-Host '==> 校验通过'
}

Write-Host "==> 解包到：$Dest"
Expand-Archive -Path $zipPath -DestinationPath $Dest -Force
Remove-Item -Path $zipPath -Force

# 压缩包内部层级不固定，直接按 go.exe 的位置反推 GOROOT。
$goExe = Get-ChildItem -Path $Dest -Recurse -Filter 'go.exe' -ErrorAction SilentlyContinue |
    Where-Object { $_.DirectoryName -like '*\bin' } |
    Select-Object -First 1

if (-not $goExe) {
    throw "解包后未找到 bin/go.exe，目录结构可能已变化：$Dest"
}

$goRoot = Split-Path -Parent (Split-Path -Parent $goExe.FullName)
Write-Host "==> GOROOT：$goRoot"

# 自检：能报出 vendor 的 go 版本才算可用。
$reported = & $goExe.FullName version
if ($LASTEXITCODE -ne 0) {
    throw "工具链不可用：$($goExe.FullName)"
}
Write-Host "==> $reported"

if ($env:GITHUB_ENV) {
    Add-Content -Path $env:GITHUB_ENV -Value "GOLEGACY_ROOT=$goRoot"
    Add-Content -Path $env:GITHUB_ENV -Value "GOLEGACY_BIN=$(Split-Path -Parent $goExe.FullName)"
}
if ($env:GITHUB_PATH) {
    Add-Content -Path $env:GITHUB_PATH -Value (Split-Path -Parent $goExe.FullName)
}

Write-Output $goRoot
