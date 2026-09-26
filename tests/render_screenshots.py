#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""离屏渲染桌面 UI 截图（用于更新 README 的界面预览）。

用法（在项目根目录）：
    python tests/render_screenshots.py

输出：
    Screenshots/1.png  扫描页（README 主界面预览）
    Screenshots/2.png  测速结果页
    Screenshots/preview_*.png  其余页面（扫描/结果/历史/设置）
"""
import os
import sys

# 用「原生平台 + WA_DontShowOnScreen」渲染：既拿到系统真实字体（含 emoji），
# 又不会在屏幕上弹出窗口。
#
# 为什么不直接用 QT_QPA_PLATFORM=offscreen：该插件的字体库默认为空，
# 即便手动 addApplicationFont 载入 msyh/seguiemj，emoji 仍会被栅格化成
# 「豆腐块」——这是 offscreen 后端的固有限制，不是应用的问题。
# 需要强制离屏时设 CLOUDTRACE_SHOT_OFFSCREEN=1（文字可读，emoji 会是方块）。
if os.environ.get("CLOUDTRACE_SHOT_OFFSCREEN") == "1":
    os.environ["QT_QPA_PLATFORM"] = "offscreen"
else:
    os.environ.pop("QT_QPA_PLATFORM", None)
os.environ.setdefault("CLOUDTRACE_ALLOW_MULTI", "1")

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)

from PySide6.QtWidgets import QApplication
from PySide6.QtGui import QFontDatabase, QFont
from PySide6.QtCore import Qt

from settings import get_settings
from ui.main_window import CloudflareScanUI

# 仅在离屏平台下需要：其字体库为空，需手动注册系统字体
_FONT_CANDIDATES = [
    r"C:\Windows\Fonts\msyh.ttc",       # Microsoft YaHei 常规
    r"C:\Windows\Fonts\msyhbd.ttc",     # Microsoft YaHei 粗体
    r"C:\Windows\Fonts\simhei.ttf",     # SimHei
    r"C:\Windows\Fonts\simsun.ttc",     # SimSun
    r"C:\Windows\Fonts\seguiemj.ttf",   # Segoe UI Emoji
    r"C:\Windows\Fonts\seguisym.ttf",   # Segoe UI Symbol
    "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
    "/System/Library/Fonts/PingFang.ttc",
]


def register_fonts():
    """离屏平台字体库为空时，手动注册系统中文字体（否则全是豆腐块）。"""
    if QFontDatabase.families():
        return []
    loaded = []
    for path in _FONT_CANDIDATES:
        if os.path.exists(path):
            fid = QFontDatabase.addApplicationFont(path)
            if fid != -1:
                loaded.extend(QFontDatabase.applicationFontFamilies(fid))
    if loaded:
        app.setFont(QFont(loaded[0], 10))
    return loaded

OUT_DIR = os.path.join(ROOT, "Screenshots")
os.makedirs(OUT_DIR, exist_ok=True)

app = QApplication.instance() or QApplication(sys.argv)
_fonts = register_fonts()
print("平台: %s | 注册字体: %s" % (app.platformName(), ", ".join(_fonts) if _fonts else "(使用系统字体)"))

SCAN_RESULTS = [
    {"ip": "104.16.132.229", "latency": 42.3, "iata_code": "HKG", "chinese_name": "中国香港",
     "port": 443, "use_tls": True, "scan_mode": "tcping", "scan_time": "2026-01-12 21:04:11",
     "ip_version": 4},
    {"ip": "172.66.147.243", "latency": 58.7, "iata_code": "HKG", "chinese_name": "中国香港",
     "port": 443, "use_tls": True, "scan_mode": "tcping", "scan_time": "2026-01-12 21:04:11",
     "ip_version": 4},
    {"ip": "104.17.24.18", "latency": 76.1, "iata_code": "NRT", "chinese_name": "日本",
     "port": 443, "use_tls": True, "scan_mode": "tcping", "scan_time": "2026-01-12 21:04:11",
     "ip_version": 4},
    {"ip": "162.159.36.5", "latency": 91.4, "iata_code": "NRT", "chinese_name": "日本",
     "port": 443, "use_tls": True, "scan_mode": "tcping", "scan_time": "2026-01-12 21:04:11",
     "ip_version": 4},
    {"ip": "104.18.6.99", "latency": 112.8, "iata_code": "SIN", "chinese_name": "新加坡",
     "port": 443, "use_tls": True, "scan_mode": "tcping", "scan_time": "2026-01-12 21:04:11",
     "ip_version": 4},
    {"ip": "188.114.97.7", "latency": 134.5, "iata_code": "LAX", "chinese_name": "美国",
     "port": 443, "use_tls": True, "scan_mode": "tcping", "scan_time": "2026-01-12 21:04:11",
     "ip_version": 4},
    {"ip": "198.41.200.13", "latency": 158.2, "iata_code": "SIN", "chinese_name": "新加坡",
     "port": 443, "use_tls": True, "scan_mode": "tcping", "scan_time": "2026-01-12 21:04:11",
     "ip_version": 4},
    {"ip": "104.19.176.5", "latency": 187.9, "iata_code": "FRA", "chinese_name": "德国",
     "port": 443, "use_tls": True, "scan_mode": "tcping", "scan_time": "2026-01-12 21:04:11",
     "ip_version": 4},
]
SPEED_RESULTS = [
    {"ip": "104.16.132.229", "latency": 42.3, "download_speed": 28.64, "score": 24.9,
     "iata_code": "HKG", "chinese_name": "中国香港", "port": 443,
     "test_type": "完全测速", "verified": True},
    {"ip": "172.66.147.243", "latency": 58.7, "download_speed": 21.35, "score": 17.6,
     "iata_code": "HKG", "chinese_name": "中国香港", "port": 443,
     "test_type": "完全测速", "verified": True},
    {"ip": "104.17.24.18", "latency": 76.1, "download_speed": 16.82, "score": 12.7,
     "iata_code": "NRT", "chinese_name": "日本", "port": 443,
     "test_type": "完全测速", "verified": True},
    {"ip": "162.159.36.5", "latency": 91.4, "download_speed": 12.07, "score": 8.6,
     "iata_code": "NRT", "chinese_name": "日本", "port": 443,
     "test_type": "完全测速", "verified": True},
    {"ip": "104.18.6.99", "latency": 112.8, "download_speed": 8.41, "score": 5.4,
     "iata_code": "SIN", "chinese_name": "新加坡", "port": 443,
     "test_type": "完全测速", "verified": True},
    {"ip": "188.114.97.7", "latency": 134.5, "download_speed": 5.93, "score": 3.5,
     "iata_code": "LAX", "chinese_name": "美国", "port": 443,
     "test_type": "完全测速", "verified": True},
]
FUNNEL = {"generated": 5000, "latency_ok": 342, "with_iata": 318}
LOG_LINES = [
    "开始扫描 Cloudflare IPv4 官方网段（TCPing 模式，端口 443）",
    "并发 200 · 延迟阈值 230ms · 采样上限 5000",
    "已生成 5000 个候选 IP，开始并发探测…",
    "进度 1200/5000 · 成功 84 · 速度 412 IP/s",
    "进度 3200/5000 · 成功 226 · 速度 436 IP/s",
    "延迟筛选：226 → 342 通过（阈值 230ms）",
    "地区解析完成：318 个 IP 命中 IATA 码",
    "扫描完成，共 318 个可用 IP，已存入历史",
]


def build_window():
    win = CloudflareScanUI()
    # 让 Qt 完成布局与渲染，但不把窗口真正贴到屏幕上
    win.setAttribute(Qt.WA_DontShowOnScreen, True)
    # 托盘图标在无交互会话里可能创建失败并拖垮进程，渲染时不需要它
    try:
        win.tray_icon.hide()
    except Exception:
        pass
    win.resize(1280, 830)
    win.show()
    for _ in range(3):
        app.processEvents()
    return win


def snapshot(win, page_idx: int, filename: str, size=None):
    if size:
        win.resize(*size)
    win._set_page(page_idx)
    for _ in range(3):
        app.processEvents()
    pix = win.grab()
    path = os.path.join(OUT_DIR, filename)
    pix.save(path, "PNG")
    print(f"  saved {filename}  ({pix.width()}x{pix.height()})")
    return path


# 扫描页演示内容：多形态来源 + 实时解析预览 + 远程数据源预览
SOURCE_DEMO = """1.2.3.0/24          # CIDR → 按采样密度抽 IP
2606:4700::/32      # IPv6 CIDR
1.2.3.4             # 单个 IP
1.2.3.4:8443        # IP + 端口
1.2.3.4-1.2.3.20    # IP 段（≤1024 直接展开）
[2606:4700::1111]:8443   # IPv6 + 端口
example.com         # 域名（自动解析，最多 50 个 IP）
https://example.com:8443/  # 带 scheme → 决定是否走 TLS"""


def main():
    win = build_window()

    # 填充演示数据
    win.result_page.set_results(SCAN_RESULTS, funnel=FUNNEL, scan_mode="tcping")
    win.speed_page.set_results(SPEED_RESULTS)
    win.scan_page.set_funnel([("生成", FUNNEL["generated"]),
                              ("延迟达标", FUNNEL["latency_ok"]),
                              ("地区解析", FUNNEL["with_iata"]),
                              ("可用", len(SCAN_RESULTS))])
    for line in LOG_LINES:
        win.scan_page.log(line)

    # 扫描页：展示「一个框吃所有格式」的解析预览 + 远程数据源卡片
    win.scan_page.combo_source.setCurrentText("官方+自定义")
    win.scan_page.text_source.setPlainText(SOURCE_DEMO)
    win.scan_page.input_prefilter.setText("443, 8443")
    win.scan_page.chk_remote.setCurrentText("启用")
    win.scan_page.lbl_sources_preview.setText(
        "拉取完成：2 个源共 1284 条候选节点（cfnb 聚合 1024 · countrymerge 260）")
    app.processEvents()

    print("渲染截图:")
    # 扫描页内容较长：README 主图用标准高度（与测速图等宽等高），
    # 另出一张全页长图 preview_scan.png，把「数据源」卡片也纳入
    snapshot(win, 0, "1.png", size=(1280, 920))
    snapshot(win, 0, "preview_scan.png", size=(1280, 1500))
    snapshot(win, 1, "preview_result.png", size=(1280, 830))
    snapshot(win, 2, "preview_speed.png", size=(1280, 830))
    snapshot(win, 3, "preview_history.png", size=(1280, 830))
    snapshot(win, 4, "preview_settings.png", size=(1280, 830))

    # README 用的第二张主图
    import shutil
    shutil.copyfile(os.path.join(OUT_DIR, "preview_speed.png"), os.path.join(OUT_DIR, "2.png"))
    print("  已更新 README 预览: 1.png（扫描页） / 2.png（测速页）")

    win.bridge.detach()
    win.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
