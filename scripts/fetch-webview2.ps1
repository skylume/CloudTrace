#Requires -Version 5.1
<#
.SYNOPSIS
    准备固定版本的 WebView2 运行时，用于 Win7 桌面版的 full 变体。

.DESCRIPTION
    Win7 桌面版要求固定版本 WebView2（109 是最后一个支持 Win7 的分支），因为它
    不再随系统更新，行为可预测。

    来源有两个：

      - 默认走 nuget.org 上的 WebView2.Runtime.X64 包。微软官方只提供 GUID 形式的
        临时下载地址，那串地址会过期，放进 CI 等于让 full 变体随机消失；nuget 上的
        地址带版本号、长期有效，因此是 CI 的默认来源。代价是这份运行时由社区打包，
        不是微软官方发布——所以下面仍然校验 msedgewebview2.exe 的版本号。
      - -CabUrl 改为从微软官方的固定版 .cab 获取（需要自己从下载页拿，地址会过期）。

.PARAMETER Version
    固定版本号。默认 109.0.1518.78，即 nuget 上最新的 109 分支版本。

.PARAMETER CabUrl
    微软官方固定版 .cab 的下载地址；给了它就不走 nuget。

.PARAMETER Dest
    解包目标目录（脚本会把运行时文件平铺在它下面）。

.PARAMETER KeepArchive
    保留下载到的压缩包，便于排查。
#>
[CmdletBinding()]
param(
    [string]$Version = '109.0.1518.78',

    [string]$CabUrl,

    [Parameter(Mandatory = $true)]
    [string]$Dest,

    [switch]$KeepArchive
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$LASTEXITCODE = 0

# nupkg 里运行时所在的固定前缀。包本身是普通 zip，只有这一棵子树是运行时。
$nupkgPrefix = 'contentFiles/any/any/WebView2/'

function Get-RemoteFile {
    param([string]$Url, [string]$OutFile)
    Write-Host "==> 下载：$Url"
    $prevProgress = $ProgressPreference
    $ProgressPreference = 'SilentlyContinue'
    try {
        Invoke-WebRequest -Uri $Url -OutFile $OutFile -UseBasicParsing
    }
    finally {
        $ProgressPreference = $prevProgress
    }
    if (-not (Test-Path $OutFile)) {
        throw "下载失败：$Url"
    }
    Write-Host ("==> 已下载 {0} MB" -f [math]::Round((Get-Item $OutFile).Length / 1MB, 2))
}

# 从 nupkg 里只解出运行时那一棵子树，平铺到 Dest。
#
# 用 ZipFile 逐条解而不是 Expand-Archive：包里有 440MB 运行时加一堆 nuspec 元数据，
# 整包解完再挑目录要多写一倍磁盘，而 CI 的磁盘和时间都有限。
function Expand-RuntimePackage {
    param([string]$PackagePath, [string]$TargetDir)

    Add-Type -AssemblyName System.IO.Compression.FileSystem

    $zip = [System.IO.Compression.ZipFile]::OpenRead($PackagePath)
    try {
        $count = 0
        foreach ($entry in $zip.Entries) {
            if (-not $entry.FullName.StartsWith($nupkgPrefix, [System.StringComparison]::Ordinal)) {
                continue
            }
            $relative = $entry.FullName.Substring($nupkgPrefix.Length)
            if ([string]::IsNullOrWhiteSpace($relative)) { continue }
            # 目录条目（以 / 结尾）在 Entries 里也存在，跳过它们。
            if ($entry.Name -eq '') { continue }

            # 注意变量名：PowerShell 的变量名不区分大小写，这里若写成 $target
            # 会把上面的参数 $TargetDir 之外的同名量覆盖掉，第二次循环就拼错路径。
            $targetFile = Join-Path $TargetDir ($relative -replace '/', '\')
            $parent = Split-Path -Parent $targetFile
            if ($parent -and -not (Test-Path $parent)) {
                New-Item -ItemType Directory -Force -Path $parent | Out-Null
            }
            [System.IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $targetFile, $true)
            $count++
        }
        if ($count -eq 0) {
            throw "包内没有 $nupkgPrefix 这棵子树，包结构可能已变化：$PackagePath"
        }
        Write-Host "==> 已解出 $count 个文件"
    }
    finally {
        $zip.Dispose()
    }
}

New-Item -ItemType Directory -Force -Path $Dest | Out-Null

$tempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("cloudtrace-webview2-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $tempDir | Out-Null

try {
    if ([string]::IsNullOrWhiteSpace($CabUrl)) {
        $archive = Join-Path $tempDir "webview2.runtime.x64.$Version.nupkg"
        $url = "https://api.nuget.org/v3-flatcontainer/webview2.runtime.x64/$Version/webview2.runtime.x64.$Version.nupkg"
        Get-RemoteFile -Url $url -OutFile $archive
        Write-Host "==> 解包运行时到：$Dest"
        Expand-RuntimePackage -PackagePath $archive -TargetDir $Dest
    }
    else {
        $archive = Join-Path $tempDir "webview2-$Version.cab"
        Get-RemoteFile -Url $CabUrl -OutFile $archive
        Write-Host "==> 解包 .cab 到：$Dest"
        # expand.exe 是 Windows 自带工具；-F:* 表示解出包内全部文件。
        & expand.exe "$archive" -F:* "$Dest" | Out-Null
        if ($LASTEXITCODE -ne 0) {
            throw "expand.exe 解包失败（退出码 $LASTEXITCODE）"
        }
    }
}
finally {
    if ($KeepArchive) {
        Write-Host "==> 压缩包保留在：$tempDir"
    }
    else {
        Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}

# 校验：解出来的确实是 WebView2 运行时，且版本号对得上。
#
# 版本号这一条是这次「改用第三方包」的兜底：包名里的版本和里面装的东西未必
# 一致，而 Win7 只认 109 分支——拿错版本的表现是「装上了但窗口起不来」。
$runtimeExe = Get-ChildItem -Path $Dest -Recurse -Filter 'msedgewebview2.exe' -ErrorAction SilentlyContinue |
    Select-Object -First 1
if (-not $runtimeExe) {
    throw "解包后未找到 msedgewebview2.exe，包结构可能已变化：$Dest"
}

$fileVersion = (Get-Item $runtimeExe.FullName).VersionInfo.FileVersion
Write-Host "==> 运行时版本：$fileVersion"
if ($fileVersion -and ($fileVersion -notlike "$Version*")) {
    throw "版本不符：期望 $Version，实际 $fileVersion"
}

Write-Output $Dest
