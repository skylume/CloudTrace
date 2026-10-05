<#
.SYNOPSIS
  生成 Release 说明。

.DESCRIPTION
  从产物文件名推出「适用系统」与「这一版给谁用」，写成一份 Markdown。

  抽成脚本而不是写在 workflow 里，理由和检查序列一样：写在 workflow 里就只能
  等发版时才知道对不对。这里可以拿几个假文件名在本地跑一遍。

  两列都从文件名推出来而不是写死：写死的话，「加了新产物但忘了改说明」会变成
  常事——而发出去之后没人会回头核对那张表。

.PARAMETER Version
  版本号，只用于标题。

.PARAMETER ArtifactDir
  存放产物的目录，脚本会挑出里面的 *.zip。

.PARAMETER Out
  输出文件路径。
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Version,

    [Parameter(Mandatory = $true)]
    [string]$ArtifactDir,

    [Parameter(Mandatory = $true)]
    [string]$Out
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$files = Get-ChildItem -Path $ArtifactDir -Filter '*.zip' -ErrorAction SilentlyContinue |
    Sort-Object Name
if (-not $files) {
    throw "在 $ArtifactDir 里没找到任何 .zip 产物"
}

# 说明按文件名匹配。注意 PowerShell 的 switch 会执行**所有**匹配到的分支，
# 所以每条后面都带 break——不然「同时命中两条」时拿到的是最后一条的结果。
function Get-ArtifactNote([string]$name) {
    switch -Wildcard ($name) {
        '*panel*win-x64*' { '推荐绝大多数人：解压即用，浏览器访问面板'; break }
        '*panel*win7*' { 'Windows 7：需配 Chrome 109 / Firefox ESR 115'; break }
        '*desktop*win-x64*' { 'Windows 10/11：原生窗口，使用系统 WebView2'; break }
        '*desktop*win7*lite*' { 'Windows 7：需自行安装 WebView2 109'; break }
        '*desktop*win7*full*' { 'Windows 7：已捆绑 WebView2 109，解压即用'; break }
        default { '' }
    }
}

function Get-ArtifactSystem([string]$name) {
    if ($name -like '*win7*') { return 'Windows 7' }
    if ($name -like '*win-x64*') { return 'Windows 10+' }
    return ''
}

$lines = @()
$lines += "# CloudTrace $Version"
$lines += ''
$lines += '## 下载哪个'
$lines += ''
$lines += '| 文件 | 适用系统 | 说明 |'
$lines += '| :--- | :--- | :--- |'
foreach ($f in $files) {
    $system = Get-ArtifactSystem $f.Name
    $note = Get-ArtifactNote $f.Name
    $lines += "| ``$($f.Name)`` | $system | $note |"
}
$lines += ''
$lines += '## 说明'
$lines += ''
$lines += '- 绿色免安装：解压后直接运行，数据默认落在程序同级 ``data/`` 目录。'
# 只写分支号不写具体补丁号：补丁号随 fetch-webview2.ps1 变，写在这里迟早对不上。
$lines += '- Windows 7 桌面版固定使用 WebView2 ``109``（最后一个支持 Win7 的分支），该分支已停止安全更新，建议优先升级系统。'
$lines += '- Windows 7 支持由社区分支 Go 工具链提供，主构建使用官方 Go。'

Set-Content -Path $Out -Value ($lines -join "`n") -Encoding utf8
Write-Host "已写入 $Out："
Get-Content $Out
