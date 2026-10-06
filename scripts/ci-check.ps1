<#
.SYNOPSIS
  跑一遍 CI 的检查项：格式、静态检查、构建、测试、覆盖率门禁。

.DESCRIPTION
  与 .github/workflows/ci.yml 里的检查步骤**同一份命令**。抽出来是因为那两次
  CI 失败都源于同一件事：改了 workflow 却没法在本地验证——只能等推上去才发现。

  本地跑一遍：
      pwsh -File scripts/ci-check.ps1 -SkipRace

  -SkipRace 只该在本机性能不够时用；CI 上必须带竞态检测。

.PARAMETER SkipRace
  跳过竞态检测。本机跑不动 -race 时用，**不要**在 CI 上用它。

.PARAMETER CoverageThreshold
  覆盖率门禁阈值，0 表示不做门禁。
#>
[CmdletBinding()]
param(
    [switch]$SkipRace,
    [double]$CoverageThreshold = 70
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# 显式列出包路径，而不是 `./...`：web/node_modules 里有个 npm 包夹带了 Go 源码
# （flatted），`./...` 会把它当成本模块的包一起编译、一起算覆盖率——它和这个
# 项目毫无关系，哪天它编译不过就会连带把 CI 弄红。
#
# **新增顶层目录时记得加进这里。**
#
# 注意这里是 PowerShell 数组，不是「一串用空格隔开的模式」。写成字符串再直接
# 传给 go 是不行的：PowerShell 不按空格拆环境变量，go 会收到一个带空格的整体
# 模式，结果是 `matched no packages`——而 go 在没有包可查时返回非零，整步就红了。
$Packages = @('./assets/...', './cmd/...', './internal/...')

$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    Write-Host '==> gofmt'
    $unformatted = gofmt -l ./cmd ./internal ./web
    if ($unformatted) {
        Write-Host '以下文件未格式化：'
        $unformatted | ForEach-Object { Write-Host "    $_" }
        throw 'gofmt 未通过'
    }
    Write-Host '    gofmt 通过'

    Write-Host '==> go vet'
    go vet @Packages
    if ($LASTEXITCODE -ne 0) { throw 'go vet 未通过' }
    Write-Host '    vet 通过'

    # staticcheck 比 vet 查得深（无效代码、可疑比较、无用分支等）。
    #
    # 本地没装就跳过并说一声，CI 上必装（workflow 里 `go install`）——它是门禁
    # 的一部分，不该因为「我这台机器上没有」就静默不跑。
    Write-Host '==> staticcheck'
    $staticcheck = Get-Command staticcheck -ErrorAction SilentlyContinue
    if ($null -eq $staticcheck) {
        Write-Host '    未安装 staticcheck，已跳过（本地可 go install honnef.co/go/tools/cmd/staticcheck@2025.1.1）'
    }
    else {
        staticcheck @Packages
        if ($LASTEXITCODE -ne 0) { throw 'staticcheck 未通过' }
        Write-Host '    staticcheck 通过'
    }

    Write-Host '==> go build（panel）'
    go build -tags panel @Packages
    if ($LASTEXITCODE -ne 0) { throw 'panel 构建失败' }

    Write-Host '==> go build（desktop）'
    go build -tags desktop @Packages
    if ($LASTEXITCODE -ne 0) { throw 'desktop 构建失败' }
    Write-Host '    两个构建标签都编过'

    Write-Host '==> go test'
    $profile = Join-Path $root 'coverage.out'
    Remove-Item -Force $profile -ErrorAction SilentlyContinue

    $testArgs = @()
    if ($SkipRace) {
        Write-Host '    已跳过竞态检测（本机模式）'
    }
    else {
        $testArgs += '-race'
    }
    $testArgs += @('-count=1', '-timeout', '600s', '-covermode=atomic', "-coverprofile=$profile")

    $env:CGO_ENABLED = '1'
    go test @Packages @testArgs
    if ($LASTEXITCODE -ne 0) { throw '测试未通过' }

    if (-not (Test-Path $profile)) {
        throw "测试跑完了却没有产出覆盖率文件：$profile"
    }
    Write-Host "    覆盖率文件：$profile（$((Get-Item $profile).Length) 字节）"

    if ($CoverageThreshold -gt 0) {
        Write-Host '==> 覆盖率门禁'
        & (Join-Path $PSScriptRoot 'check-coverage.ps1') `
            -CoverProfile $profile `
            -Threshold $CoverageThreshold `
            -Package cloudtrace/internal/probe, cloudtrace/internal/source, cloudtrace/internal/scan, cloudtrace/internal/speed, cloudtrace/internal/history, cloudtrace/internal/score, cloudtrace/internal/geo
        if ($LASTEXITCODE -ne 0) { throw '覆盖率门禁未通过' }
    }

    # ---- 前端 ----
    #
    # 这几步以前只在本地跑，CI 里一步都没有：改了 store 或工具函数，CI 全绿也
    # 可能带着坏掉的前端逻辑合并。前端产物是 go:embed 进二进制的，它坏掉就是
    # 整个界面坏掉。
    Write-Host '==> 前端检查'
    Push-Location (Join-Path $root 'web')
    try {
        if (-not (Test-Path 'node_modules')) {
            Write-Host '    web/node_modules 不存在，先 npm ci'
            npm ci
            if ($LASTEXITCODE -ne 0) { throw 'npm ci 失败' }
        }

        Write-Host '    lint'
        npm run lint
        if ($LASTEXITCODE -ne 0) { throw '前端 lint 未通过' }

        Write-Host '    type-check'
        npm run typecheck
        if ($LASTEXITCODE -ne 0) { throw '前端类型检查未通过' }

        Write-Host '    test'
        npm test
        if ($LASTEXITCODE -ne 0) { throw '前端测试未通过' }
    }
    finally {
        Pop-Location
    }

    Write-Host ''
    Write-Host '检查全部通过。'
}
finally {
    Pop-Location
}
