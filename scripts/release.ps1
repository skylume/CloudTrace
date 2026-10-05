#Requires -Version 5.1
<#
.SYNOPSIS
    产出 CloudTrace 的发布分发包。

.DESCRIPTION
    本脚本是「产物清单」的唯一事实来源：产物怎么命名、Win7 桌面版的 lite / full
    怎么区分，都只在这里定义一次。CI 通过 -Only 让每个 job 只负责一个产物，
    本地则直接跑全部。

    产物清单（一次发版全部产出，挂在同一个 Release）：
      1. cloudtrace-panel-<ver>-win-x64.zip          官方 Go
      2. cloudtrace-desktop-<ver>-win-x64.zip        官方 Go + wails
      3. cloudtrace-panel-<ver>-win7-x64.zip         Win7 工具链
      4. cloudtrace-desktop-<ver>-win7-x64-lite.zip  Win7 工具链，不含 WebView2
      5. cloudtrace-desktop-<ver>-win7-x64-full.zip  Win7 工具链，含固定版 WebView2

    lite 与 full 共用同一个 exe，只差是否附带 webview2/ 目录。full 附带的固定版
    WebView2 默认从 nuget.org 上的 WebView2.Runtime.X64 取（微软官方的地址是 GUID
    形式的临时链接、会过期）；也可以设 WEBVIEW2_CAB_URL 改从官方 .cab 取。

.PARAMETER Only
    只产出指定产物；缺省 all。

.PARAMETER Version
    版本号，写入二进制并参与产物命名。

.PARAMETER OutDir
    产物输出目录，相对仓库根；缺省 dist。

.PARAMETER SkipWeb
    跳过前端构建（由调用方保证 web/dist 已就绪）。CI 中前端只构建一次，
    因此除 build-web job 外都应带上本开关。

.PARAMETER StageOnly
    只准备各包的暂存目录（dist/pkg/<包名>/），不压 zip。CI 的构建 job 用它上传
    artifact，zip 由汇总发布那一步统一生成。

.PARAMETER KeepIntermediate
    保留未压缩的 exe，便于排查；缺省清理。
#>
[CmdletBinding()]
param(
    [ValidateSet('all', 'panel-modern', 'desktop-modern', 'panel-win7', 'desktop-win7')]
    [string[]]$Only = @('all'),

    [string]$Version = 'dev',

    [string]$OutDir = 'dist',

    [switch]$SkipWeb,

    [switch]$StageOnly,

    [switch]$KeepIntermediate
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$LASTEXITCODE = 0

$repoRoot = Split-Path -Parent $PSScriptRoot
$buildScript = Join-Path $PSScriptRoot 'build.ps1'
$packageScript = Join-Path $PSScriptRoot 'package.ps1'
$toolchainScript = Join-Path $PSScriptRoot 'fetch-toolchain.ps1'
$webviewScript = Join-Path $PSScriptRoot 'fetch-webview2.ps1'

$outDirAbs = Join-Path $repoRoot $OutDir
New-Item -ItemType Directory -Force -Path $outDirAbs | Out-Null

# 各包的暂存目录都放这里。CI 上传的就是它（而不是 zip），见 package.ps1 的说明。
# 目录本身由 package.ps1 按需创建，这里不预先建：非 StageOnly 的那条路上不该
# 留下一个空的 dist/pkg/。
$pkgDirAbs = Join-Path $outDirAbs 'pkg'

function Test-Wanted {
    param([string]$Name)
    return ($Only -contains 'all') -or ($Only -contains $Name)
}

function Invoke-Build {
    param([string]$Target, [string]$Toolchain)
    # 注意：不要用 $args 作变量名，它是 PowerShell 的自动变量。
    $buildArgs = @{
        Target    = $Target
        Toolchain = $Toolchain
        Version   = $Version
        OutDir    = $OutDir
    }
    if ($SkipWeb) { $buildArgs['SkipWeb'] = $true }

    # 子脚本的输出经 Write-Host 直通控制台（保留可见性），但不进入管道：
    # 否则 npm / go 的日志会被当成返回值，混进产物路径里。
    & $buildScript @buildArgs | ForEach-Object { Write-Host $_ }

    $exe = Join-Path $outDirAbs "cloudtrace-$Target-$Version.exe"
    if (-not (Test-Path $exe)) {
        throw "构建 $Target（$Toolchain）未产出预期文件：$exe"
    }
    return $exe
}

function New-Package {
    param([string]$Name, [string[]]$Items)
    $dest = Join-Path $outDirAbs "$Name.zip"
    $pkgArgs = @{
        Path        = $Items
        Destination = $dest
        Root        = $repoRoot
    }
    # 暂存目录只在 CI 那条路上留着：本地跑一遍没必要多出一份几百兆的解包内容。
    if ($StageOnly) {
        $pkgArgs['StageDir'] = $pkgDirAbs
        $pkgArgs['StageOnly'] = $true
    }

    # 子脚本的输出经 Write-Host 直通控制台（保留可见性），但不进入管道：
    # 否则包路径会被当成返回值混进来。
    & $packageScript @pkgArgs | ForEach-Object { Write-Host $_ }

    if ($StageOnly) {
        $stage = Join-Path $pkgDirAbs $Name
        if (-not (Test-Path $stage)) {
            throw "暂存 $Name 未产出预期目录：$stage"
        }
        return $stage
    }
    if (-not (Test-Path $dest)) {
        throw "打包 $Name 未产出预期文件：$dest"
    }
    return $dest
}

$produced = @()

Push-Location $repoRoot
try {
    # ---- Win7 线需要独立的工具链 --------------------------------------
    # 与主构建的官方 Go 隔离成两个 GOROOT，避免互相污染。
    $needsWin7 = (Test-Wanted 'panel-win7') -or (Test-Wanted 'desktop-win7')
    if ($needsWin7 -and [string]::IsNullOrWhiteSpace($env:GOLEGACY_ROOT)) {
        # 脚本把 GOROOT 作为唯一的标准输出返回；进度信息走 Write-Host，不受影响。
        $goRoot = & $toolchainScript | Select-Object -Last 1
        if ($goRoot) { $env:GOLEGACY_ROOT = "$goRoot".Trim() }
        if ([string]::IsNullOrWhiteSpace($env:GOLEGACY_ROOT)) {
            throw '未能准备 Win7 工具链（GOLEGACY_ROOT 仍为空）'
        }
        Write-Host "==> Win7 工具链就绪：$env:GOLEGACY_ROOT"
    }

    # ---- 1. 面板版（官方 Go）------------------------------------------
    if (Test-Wanted 'panel-modern') {
        Write-Host ''
        Write-Host '########## 产物 1/5：面板版（Win10+）##########'
        $exe = Invoke-Build -Target 'panel' -Toolchain 'modern'
        $produced += (New-Package -Name "cloudtrace-panel-$Version-win-x64" -Items @($exe))
    }

    # ---- 2. 桌面版（官方 Go + wails）----------------------------------
    if (Test-Wanted 'desktop-modern') {
        Write-Host ''
        Write-Host '########## 产物 2/5：桌面版（Win10+）##########'
        $exe = Invoke-Build -Target 'desktop' -Toolchain 'modern'
        $produced += (New-Package -Name "cloudtrace-desktop-$Version-win-x64" -Items @($exe))
    }

    # ---- 3. 面板版（Win7）---------------------------------------------
    if (Test-Wanted 'panel-win7') {
        Write-Host ''
        Write-Host '########## 产物 3/5：面板版（Win7）##########'
        $exe = Invoke-Build -Target 'panel' -Toolchain 'win7'
        $produced += (New-Package -Name "cloudtrace-panel-$Version-win7-x64" -Items @($exe))
    }

    # ---- 4/5. 桌面版（Win7）lite 与 full -------------------------------
    if (Test-Wanted 'desktop-win7') {
        Write-Host ''
        Write-Host '########## 产物 4/5：桌面版（Win7）lite ##########'
        $exe = Invoke-Build -Target 'desktop' -Toolchain 'win7'
        $produced += (New-Package -Name "cloudtrace-desktop-$Version-win7-x64-lite" -Items @($exe))

        Write-Host ''
        Write-Host '########## 产物 5/5：桌面版（Win7）full ##########'
        # full 与 lite 共用同一个 exe，只多带一个固定版本 WebView2 运行时目录。
        #
        # 默认从 nuget 取那份固定版运行时，因为微软官方的下载地址是 GUID 形式的
        # 临时链接、会过期——按「没配变量就跳过」处理的话，full 会在某次发版时
        # 无声消失。设了 WEBVIEW2_CAB_URL 才改用官方 .cab。
        $webviewArgs = @{ Dest = (Join-Path $outDirAbs 'webview2') }
        $cabUrl = $env:WEBVIEW2_CAB_URL
        if (-not [string]::IsNullOrWhiteSpace($cabUrl)) {
            $webviewArgs['CabUrl'] = $cabUrl
            Write-Host "==> 使用 WEBVIEW2_CAB_URL 指定的官方 .cab"
        }
        $webviewDest = & $webviewScript @webviewArgs | Select-Object -Last 1
        if ([string]::IsNullOrWhiteSpace($webviewDest)) { throw '准备 WebView2 运行时失败' }
        $webviewDest = "$webviewDest".Trim()
        $produced += (New-Package -Name "cloudtrace-desktop-$Version-win7-x64-full" -Items @($exe, $webviewDest))
    }

    # ---- 收尾 ----------------------------------------------------------
    # 只清中间产物。dist/pkg/ 是各包的内容本身（CI 要上传它），不能当临时文件删掉。
    if (-not $KeepIntermediate) {
        Get-ChildItem -Path $outDirAbs -Filter '*.exe' -File -ErrorAction SilentlyContinue |
            Remove-Item -Force -ErrorAction SilentlyContinue
        Remove-Item -Path (Join-Path $outDirAbs 'webview2') -Recurse -Force -ErrorAction SilentlyContinue
    }

    Write-Host ''
    Write-Host "==> 本次产出 $($produced.Count) 个分发包："
    foreach ($p in $produced) { Write-Host "    $p" }
}
finally {
    Pop-Location
}
