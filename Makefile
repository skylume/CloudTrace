# CloudTrace 构建入口
#
# 本文件只是「薄封装」：真正的构建与打包逻辑都在 scripts/ 下的 PowerShell 脚本里，
# 这样本地与 CI 走的是同一套流程，不会出现「本地能打、CI 打不出来」的分歧。
#
# 若本机没有 make，可以直接调用等价脚本，例如：
#   pwsh -File scripts/release.ps1 -Version 1.0.0
#
# 需要 Windows + PowerShell 7（GitHub Actions 的 windows runner 自带）。
# 若本机只装了 Windows PowerShell，用 make SHELL=powershell 覆盖即可。
#
# 注意必须是 -Command 而不是 -File：配方行是命令，不是脚本文件路径。
SHELL := pwsh
.SHELLFLAGS := -NoProfile -Command

VERSION ?= dev
OUTDIR  ?= dist

# ---- 共享清单 ------------------------------------------------------------
#
# PACKAGES / GOFMT_PATHS / COVERAGE_PACKAGES 只在 scripts/packages.mk 里定义
# 一次：本文件 include 它，scripts/ci-check.ps1 解析同一份文件。以前两边各存
# 一份、注释写着「改动时两边要一起改」，现在只改那一份。
include ./scripts/packages.mk

# 清单被改坏时立刻停下：空清单会让 go test/vet 退化成「只测当前目录」，
# 那种失败比直接报错难查得多。
ifeq ($(strip $(PACKAGES)),)
$(error scripts/packages.mk 未提供 PACKAGES：包清单为空)
endif

.DEFAULT_GOAL := help
.PHONY: help pkgs dev web build build-panel build-desktop build-win7 release ci test lint fmt fmt-check clean

ci: ## 跑一遍 CI 的检查项（与 workflow 同一份命令）
	pwsh -NoProfile -File scripts/ci-check.ps1

pkgs: ## 打印当前的包清单（来自 scripts/packages.mk）
	@Write-Host "PACKAGES=$(PACKAGES)"
	@Write-Host "GOFMT_PATHS=$(GOFMT_PATHS)"
	@Write-Host "COVERAGE_PACKAGES=$(COVERAGE_PACKAGES)"

help: ## 显示所有可用目标
	@Write-Host 'CloudTrace 构建目标：'
	@Write-Host ''
	@Write-Host '  make dev            前端热更新 + 后端 go run（开发用）'
	@Write-Host '  make web            只构建前端产物 web/dist'
	@Write-Host '  make build          完整构建面板版（含前端 embed）'
	@Write-Host '  make build-panel    面板版（官方 Go）'
	@Write-Host '  make build-desktop  桌面版（官方 Go + wails）'
	@Write-Host '  make build-win7     Win7 线（社区分支工具链）'
	@Write-Host '  make release        产出全部分发包'
	@Write-Host '  make test           跑全量测试（含竞态检测）'
	@Write-Host '  make lint           格式 + 静态检查'
	@Write-Host '  make fmt            自动格式化'
	@Write-Host '  make clean          清理构建产物'
	@Write-Host '  make pkgs           打印当前包清单（scripts/packages.mk）'
	@Write-Host ''
	@Write-Host "可选变量：VERSION=$(VERSION)  OUTDIR=$(OUTDIR)"

dev: ## 前端热更新 + 后端 go run
	@Write-Host '==> 启动后端（面板版，热重载前端请另开一个终端跑 npm run dev）'
	go run -tags panel ./cmd/panel

web: ## 只构建前端
	@Push-Location web; npm ci; npm run build; Pop-Location

build: build-panel ## 完整构建（默认面板版）

build-panel: ## 面板版（官方 Go）
	@./scripts/build.ps1 -Target panel -Toolchain modern -Version $(VERSION) -OutDir $(OUTDIR)

build-desktop: ## 桌面版（官方 Go + wails）
	@./scripts/build.ps1 -Target desktop -Toolchain modern -Version $(VERSION) -OutDir $(OUTDIR)

build-win7: ## Win7 线（社区分支工具链）
	@./scripts/build.ps1 -Target panel -Toolchain win7 -Version $(VERSION) -OutDir $(OUTDIR)

release: ## 产出全部分发包
	@./scripts/release.ps1 -Version $(VERSION) -OutDir $(OUTDIR)

test: ## 全量测试（含竞态检测）
	go test $(PACKAGES) -race -count=1 -timeout 600s

lint: ## 格式 + 静态检查
	@Write-Host '==> gofmt'
	@$(MAKE) --no-print-directory fmt-check
	go vet $(PACKAGES)

# 注意 $$：make 会把 $o 当成变量名展开，写成 $out 传给 PowerShell 的其实是 ut。
fmt-check:
	@$$out = gofmt -l $(GOFMT_PATHS); if ($$out) { Write-Host $$out; exit 1 } else { Write-Host 'gofmt 通过' }

fmt: ## 自动格式化
	gofmt -w $(GOFMT_PATHS)

clean: ## 清理构建产物
	@Remove-Item -Recurse -Force $(OUTDIR) -ErrorAction SilentlyContinue
	@Remove-Item -Recurse -Force .toolchain -ErrorAction SilentlyContinue
	@Write-Host '已清理'
