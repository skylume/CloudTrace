#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import os
import logging
from datetime import datetime
from typing import Dict, List, Optional

from PySide6.QtWidgets import (
    QWidget, QVBoxLayout, QHBoxLayout, QLabel, QPushButton, QProgressBar,
    QStackedWidget, QFileDialog, QApplication, QStyle, QSystemTrayIcon, QMenu,
    QDialog,
)
from PySide6.QtCore import Qt
from PySide6.QtGui import QFont, QIcon

from core.constants import FONT_FAMILY, resource_path, get_version
from core.factory import create_scanner, create_speed_task
from core.export import write_export
from core.analytics import region_stats
from settings import (
    get_settings, ensure_save_dir, save_custom_cidrs,
    save_results_to_file, load_results_from_file, delete_history,
)
from ui.styles import (
    APP_QSS, PILL_STYLE, PROGRESS_STYLE, ENDPOINT_BADGE_STYLE,
    C_BLUE, C_BLUE_DARK, C_ORANGE, C_ORANGE_DARK, C_GREEN, C_GREEN_DARK,
    C_RED, C_MUTED,
    btn_stylesheet, ghost_btn_stylesheet,
)
from ui.widgets import SideNav
from ui.dialogs import CustomMessageBox, ExportDialog
from ui.pages.scan_page import ScanPage
from ui.pages.result_page import ResultPage
from ui.pages.speed_page import SpeedPage
from ui.pages.history_page import HistoryPage
from ui.pages.settings_page import SettingsPage
from service.task_manager import task_manager, STAGE_TESTING
from service.http_server import http_server
from ui.bridge import WorkerBridge

logger = logging.getLogger("CloudTrace")

PAGE_SCAN = 0
PAGE_RESULT = 1
PAGE_SPEED = 2
PAGE_HISTORY = 3
PAGE_SETTINGS = 4

PAGE_TITLES = ["扫描", "扫描结果", "测速结果", "历史记录", "设置"]
PAGE_SUBTITLES = [
    "配置参数并扫描 Cloudflare IP 段",
    "筛选可用 IP，并选择测速范围",
    "下载测速与综合评分排名",
    "回看与复用历史扫描 / 测速结果",
    "默认参数、评分权重与 HTTP 面板",
]
CTA_STYLES = {
    PAGE_SCAN: ("▶ 开始扫描", C_BLUE, C_BLUE_DARK),
    PAGE_RESULT: ("🚀 批量测速", C_ORANGE, C_ORANGE_DARK),
    PAGE_SPEED: ("⬇ 导出结果", C_GREEN, C_GREEN_DARK),
    PAGE_HISTORY: ("🔄 前往扫描", C_BLUE, C_BLUE_DARK),
    PAGE_SETTINGS: ("💾 保存设置", C_BLUE, C_BLUE_DARK),
}


class CloudflareScanUI(QWidget):
    """主窗口薄壳：左侧导航 + 顶栏状态 + 页面栈，编排任务与页面。"""

    def __init__(self):
        super().__init__()
        self.setWindowTitle(f"CloudTrace 云迹 V{get_version()}")
        self.resize(1180, 820)
        self.setMinimumSize(1000, 660)

        self._setup_window_icon()
        self.setStyleSheet(APP_QSS)

        self.scanning = False
        self.speed_testing = False
        self.scan_results: List[Dict] = []
        self.speed_results: List[Dict] = []
        self.current_scan_port = 443
        self.current_ip_version = 4
        self.current_scan_mode = "tcping"
        self.current_funnel: Dict[str, int] = {}
        self.current_page = PAGE_SCAN
        self._settings_echo_guard = False

        ensure_save_dir()
        # 共享设置对象（与 Web 面板同一份，见 settings/settings.py）
        self.app_settings = get_settings()

        self._setup_bridge()
        self._build_ui()
        self._init_tray()
        self._set_page(PAGE_SCAN)
        self._sync_http_server()

    # ================= 桥接 =================
    def _setup_bridge(self):
        self.bridge = WorkerBridge(self)
        self.bridge.progress_update.connect(self._on_progress)
        self.bridge.status_message.connect(self._on_log)
        self.bridge.funnel_updated.connect(self._on_funnel)
        self.bridge.scan_completed.connect(self._scan_finished)
        self.bridge.scan_aborted.connect(self._scan_aborted)
        self.bridge.speed_progress.connect(self._on_speed_progress)
        self.bridge.speed_completed.connect(self._speed_finished)
        self.bridge.speed_aborted.connect(self._speed_aborted)
        self.bridge.state_changed.connect(self._on_state_snapshot)
        self.bridge.settings_changed.connect(self._on_settings_changed)

    def _on_state_snapshot(self, snap: dict):
        """任务状态快照变化 → 同步另一侧 UI（Web 面板/历史加载）写入的结果。

        只在本窗口持有的结果与快照「不是同一批数据」时才刷新，避免每次
        stage 变化都重建表格、丢失勾选状态。
        """
        scan = snap.get("scan_results")
        if self._results_differ(scan, self.scan_results):
            self.scan_results = list(scan)
            self.current_scan_port = (self.scan_results[0].get("port", 443)
                                      if self.scan_results else self.current_scan_port)
            self.current_ip_version = (self.scan_results[0].get("ip_version", self.current_ip_version)
                                       if self.scan_results else self.current_ip_version)
            self.current_scan_mode = (self.scan_results[0].get("scan_mode", self.current_scan_mode)
                                      if self.scan_results else self.current_scan_mode)
            if hasattr(self, "result_page"):
                self.result_page.set_results(self.scan_results, dict(self.current_funnel),
                                             self.current_scan_mode)

        speed = snap.get("speed_results")
        if self._results_differ(speed, self.speed_results):
            self.speed_results = list(speed)
            if hasattr(self, "speed_page"):
                self.speed_page.set_results(self.speed_results)

    @staticmethod
    def _results_differ(new, current) -> bool:
        if new is None:
            return False
        if len(new) != len(current):
            return True
        if not new:
            return False
        # snapshot() 复制的是列表本身而非元素，用首元素身份判断是否同一批数据
        return new[0] is not current[0]

    def _on_log(self, msg: str):
        # 按当前阶段分流日志，避免同一句话在扫描/测速两个终端各刷一遍
        if task_manager.stage == STAGE_TESTING:
            self.speed_page.log(msg)
        else:
            self.scan_page.log(msg)

    def _on_funnel(self, funnel: dict):
        self.current_funnel = dict(funnel)
        steps = []
        if funnel.get("generated"):
            steps = [
                ("生成", funnel.get("generated")),
                ("延迟达标", funnel.get("latency_ok")),
                ("地区解析", funnel.get("with_iata")),
            ]
        self.scan_page.set_funnel(steps)

    def _on_settings_changed(self, snap: dict):
        """设置被另一侧（Web 面板）改动 → 回填本端表单并同步服务。

        这是「Web 改端口不生效」与「桌面旧表单覆盖 Web 修改」两个问题的修复点：
        设置是同一个共享对象，收到广播后各页只做「读并回填」，不写回。
        """
        if self._settings_echo_guard:
            return
        self._settings_echo_guard = True
        try:
            self.settings_page.reload_from_settings()
            self.scan_page.reload_from_settings()
            self.speed_page.reload_from_settings()
            self._sync_http_server()
        finally:
            self._settings_echo_guard = False

    # ================= UI 构建 =================
    def _build_ui(self):
        root = QHBoxLayout(self)
        root.setContentsMargins(0, 0, 0, 0)
        root.setSpacing(0)

        self.sidebar = SideNav([
            ("📡", "扫描"), ("📋", "结果"), ("🚀", "测速"),
            ("🕘", "历史"), ("⚙️", "设置"),
        ])
        self.sidebar.pageChanged.connect(self._set_page)
        root.addWidget(self.sidebar)

        main = QVBoxLayout()
        main.setContentsMargins(0, 0, 0, 0)
        main.setSpacing(0)

        # ---- 顶栏 ----
        topbar = QHBoxLayout()
        topbar.setContentsMargins(22, 12, 22, 10)
        topbar.setSpacing(12)

        title_box = QVBoxLayout()
        title_box.setSpacing(1)
        self.lbl_title = QLabel(PAGE_TITLES[0])
        self.lbl_title.setFont(QFont(FONT_FAMILY, 15))
        self.lbl_title.setStyleSheet("font-weight: bold; background: transparent; border: none;")
        self.lbl_subtitle = QLabel(PAGE_SUBTITLES[0])
        self.lbl_subtitle.setStyleSheet(
            f"color: {C_MUTED}; font-size: 11px; background: transparent; border: none;")
        title_box.addWidget(self.lbl_title)
        title_box.addWidget(self.lbl_subtitle)
        topbar.addLayout(title_box)

        self.lbl_pill = QLabel("就绪")
        self.lbl_pill.setObjectName("statusPill")
        self.lbl_pill.setProperty("status", "idle")
        topbar.addWidget(self.lbl_pill, 0, Qt.AlignVCenter)

        self.lbl_meta = QLabel("")
        self.lbl_meta.setStyleSheet(
            f"color: {C_MUTED}; font-size: 12px; background: transparent; border: none;")
        topbar.addWidget(self.lbl_meta, 0, Qt.AlignVCenter)

        topbar.addStretch()

        self.lbl_endpoint = QLabel("面板已关闭")
        self.lbl_endpoint.setObjectName("endpointBadge")
        self.lbl_endpoint.setProperty("state", "off")
        self.lbl_endpoint.setCursor(Qt.PointingHandCursor)
        self.lbl_endpoint.setToolTip("点击复制面板地址")
        self.lbl_endpoint.mousePressEvent = self._copy_endpoint  # type: ignore
        topbar.addWidget(self.lbl_endpoint, 0, Qt.AlignVCenter)

        self.lbl_speed = QLabel("速度: 0 IP/s")
        self.lbl_speed.setStyleSheet(
            f"color: {C_MUTED}; font-size: 12px; background: transparent; border: none;")
        topbar.addWidget(self.lbl_speed, 0, Qt.AlignVCenter)

        self.btn_stop = QPushButton("⏹ 停止")
        self.btn_stop.setFixedHeight(34)
        self.btn_stop.setCursor(Qt.PointingHandCursor)
        self.btn_stop.setEnabled(False)
        self.btn_stop.setStyleSheet(ghost_btn_stylesheet())
        self.btn_stop.clicked.connect(self._confirm_stop)
        topbar.addWidget(self.btn_stop)

        self.btn_cta = QPushButton(CTA_STYLES[PAGE_SCAN][0])
        self.btn_cta.setFixedHeight(34)
        self.btn_cta.setMinimumWidth(128)
        self.btn_cta.setCursor(Qt.PointingHandCursor)
        self.btn_cta.clicked.connect(self._on_cta)
        topbar.addWidget(self.btn_cta)

        main.addLayout(topbar)

        # ---- 进度条 ----
        self.progress_bar = QProgressBar()
        self.progress_bar.setFixedHeight(3)
        self.progress_bar.setTextVisible(False)
        self.progress_bar.setStyleSheet(PROGRESS_STYLE)
        main.addWidget(self.progress_bar)

        # ---- 页面栈 ----
        self.stack = QStackedWidget()
        self.scan_page = ScanPage(self.app_settings)
        self.result_page = ResultPage()
        self.speed_page = SpeedPage(self.app_settings)
        self.history_page = HistoryPage()
        self.settings_page = SettingsPage(self.app_settings)

        for page in (self.scan_page, self.result_page, self.speed_page,
                     self.history_page, self.settings_page):
            self.stack.addWidget(page)
        main.addWidget(self.stack, 1)

        root.addLayout(main, 1)
        # 注意：setStyleSheet 是「覆盖」而非「追加」，必须把全局窗口规则一起带上。
        self.setStyleSheet(APP_QSS + PILL_STYLE + ENDPOINT_BADGE_STYLE)

        # ---- 页面信号 ----
        self.scan_page.start_requested.connect(self._start_scan_from_page)

        self.result_page.single_speed_requested.connect(self._on_single_speed)
        self.result_page.region_speed_requested.connect(self._on_region_speed)
        self.result_page.full_speed_requested.connect(self._start_full_speed)
        self.result_page.export_requested.connect(lambda: self._export(initial="scan"))

        self.speed_page.start_region_requested.connect(self._start_region_speed_text)
        self.speed_page.start_full_requested.connect(self._start_full_speed)
        self.speed_page.export_requested.connect(lambda: self._export(initial="speed"))

        self.history_page.load_requested.connect(self._load_history)
        self.history_page.export_requested.connect(self._export_history)
        self.history_page.delete_requested.connect(self._delete_history)

        self.settings_page.set_param_provider(self.scan_page.health_snapshot)
        self.settings_page.set_param_provider(self.speed_page.health_snapshot)
        self.settings_page.saved.connect(self._on_settings_saved)
        self.settings_page.restored.connect(self._on_settings_restored)

    def _on_settings_saved(self, *_args):
        """保存设置后：回填「设置页拥有」的字段到扫描/测速页，同步 HTTP 服务并广播给面板。"""
        self.scan_page.reload_from_settings()
        self.speed_page.reload_from_settings()
        self._sync_http_server()
        task_manager.broadcast_settings(self.app_settings)

    def _on_settings_restored(self, *_args):
        """恢复默认后：全量回填扫描/测速页表单，并同步 HTTP 服务。"""
        self.scan_page.reload_from_settings(full=True)
        self.speed_page.reload_from_settings(full=True)
        self._sync_http_server()
        task_manager.broadcast_settings(self.app_settings)

    def _set_endpoint_badge(self, text: str, state: str):
        self.lbl_endpoint.setText(text)
        self.lbl_endpoint.setProperty("state", state)
        self.lbl_endpoint.style().unpolish(self.lbl_endpoint)
        self.lbl_endpoint.style().polish(self.lbl_endpoint)

    def _copy_endpoint(self, _event):
        addr = http_server.address
        if addr:
            QApplication.clipboard().setText(addr)
            self.scan_page.log(f"已复制面板地址: {addr}")

    def _sync_http_server(self, *_args):
        """按设置启动/停止 HTTP 面板服务。"""
        s = self.app_settings
        if s.get("http_enabled", True):
            host = "0.0.0.0" if s.get("allow_lan") else "127.0.0.1"
            port = int(s.get("http_port", 17443))
            ok = http_server.start(host, port)
            if ok:
                msg = f"HTTP 面板已就绪: {http_server.address}"
                if s.get("http_token"):
                    msg += " (需要 Token)"
                self._set_endpoint_badge(f"🌐 {http_server.address}", "on")
            else:
                msg = f"HTTP 服务启动失败: {http_server.error}"
                self._set_endpoint_badge("⚠ 面板启动失败", "error")
        else:
            http_server.stop()
            msg = "HTTP 面板已关闭"
            self._set_endpoint_badge("面板已关闭", "off")
        self.scan_page.log(msg)

    # ================= 页面导航 =================
    def _set_page(self, idx: int):
        if not (0 <= idx < self.stack.count()):
            return
        self.current_page = idx
        self.stack.setCurrentIndex(idx)
        self.sidebar.set_active(idx)
        self.lbl_title.setText(PAGE_TITLES[idx])
        self.lbl_subtitle.setText(PAGE_SUBTITLES[idx])
        text, color, hover = CTA_STYLES[idx]
        self.btn_cta.setText(text)
        self.btn_cta.setStyleSheet(btn_stylesheet(color, hover_color=hover))

    def _on_cta(self):
        idx = self.current_page
        if idx == PAGE_SCAN:
            self._start_scan_from_page()
        elif idx == PAGE_RESULT:
            codes = self.result_page.chips.selected_codes()
            if codes:
                self._on_region_speed(codes)
            else:
                self._start_full_speed()
        elif idx == PAGE_SPEED:
            self._export(initial="speed" if self.speed_results else "scan")
        elif idx == PAGE_HISTORY:
            self._set_page(PAGE_SCAN)
        elif idx == PAGE_SETTINGS:
            self.settings_page.save()

    # ================= 状态 =================
    def _set_status(self, text: str, mode: str = "idle"):
        self.lbl_pill.setText(text)
        self.lbl_pill.setProperty("status", mode)
        self.lbl_pill.style().unpolish(self.lbl_pill)
        self.lbl_pill.style().polish(self.lbl_pill)

    def _set_busy(self, busy: bool):
        self.btn_stop.setEnabled(busy)
        self.btn_cta.setEnabled(not busy)
        self.scan_page.set_busy(busy)
        self.result_page.set_busy(busy)
        self.speed_page.set_busy(busy)

    def _on_progress(self, completed: int, total: int, success: int, speed: float):
        if total > 0:
            self.progress_bar.setValue(int(completed / total * 100))
        self.lbl_speed.setText(f"速度: {speed:.0f} IP/s | 成功: {success}")
        self._set_status(f"扫描 {completed}/{total}", "run")

    def _on_speed_progress(self, current: int, total: int, success: int = 0):
        if total > 0:
            self.progress_bar.setValue(int(current / total * 100))
        self.lbl_speed.setText(f"已完成 {current}/{total} · 有效 {success}")
        self._set_status(f"测速 {current}/{total}", "busy")

    # ================= 扫描 =================
    def _start_scan_from_page(self):
        if task_manager.busy:
            CustomMessageBox.warning(self, "提示", "已有任务正在运行")
            return
        params = self.scan_page.collect()
        if params is None:
            return
        self.scan_page.persist_scan_params()
        if params["source_mode"] in ("仅自定义", "官方+自定义"):
            save_custom_cidrs(params["cidr_text"])

        # 远程数据源在扫描线程里拉取，避免阻塞 UI
        from core.sources import make_fetcher
        fetcher = make_fetcher(self.app_settings, params["port"])
        if fetcher is not None:
            params["remote_fetch"] = fetcher

        scanner = create_scanner(params)
        if not task_manager.start_scan(scanner, scanner.ip_version):
            CustomMessageBox.warning(self, "提示", "任务启动失败：已有任务正在运行")
            return

        self.scanning = True
        self.scan_results = []
        self.speed_results = []
        self.current_funnel = {}
        self.current_ip_version = scanner.ip_version
        self.current_scan_port = params["port"]
        self.current_scan_mode = params["scan_mode"]

        self.result_page.set_empty()
        self.speed_page.set_results([])
        self.scan_page.set_funnel([])
        self.progress_bar.setValue(0)
        self.lbl_speed.setText("速度: 0 IP/s")
        self.lbl_meta.setText(
            f"{scanner.ip_label} · 端口 {params['port']} · "
            f"并发 {params['workers']} · {'HTTPing' if params['scan_mode'] == 'httping' else 'TCPing'}"
        )
        self._set_status("扫描中…", "run")
        self._set_busy(True)

    @staticmethod
    def _history_ip_version(results: List[Dict], fallback: int = 4) -> int:
        """按结果内容判定归档用的 IP 版本（非标混合列表时以 v4 归档）。"""
        versions = {r.get("ip_version") for r in (results or []) if r.get("ip_version")}
        if versions == {6}:
            return 6
        if versions:
            return 4
        return fallback

    def _scan_finished(self, results: List[Dict]):
        self.scanning = False
        self.scan_results = results or []
        self.progress_bar.setValue(100)

        if results:
            ip_version = self._history_ip_version(results, self.current_ip_version)
            self.current_ip_version = ip_version
            save_results_to_file(results, ip_version, "scan")
            scan_mode = results[0].get("scan_mode", "tcping")
            self.current_scan_mode = scan_mode
            self.current_scan_port = results[0].get("port", self.current_scan_port)
            self.result_page.set_results(results, dict(self.current_funnel), scan_mode)
            self.scan_page.log(f"✅ 扫描完成: {len(results)} 个可用IP，已存入历史")
            for line in self._region_lines(results)[:10]:
                self.scan_page.log(line)
            self._set_status(f"完成 · {len(results)} IP", "run")
            self._set_page(PAGE_RESULT)
            if not self.isVisible():
                self.tray_icon.showMessage(
                    "CloudTrace 扫描完成",
                    f"找到 {len(results)} 个可用 IP",
                    QSystemTrayIcon.Information, 3000,
                )
        else:
            self.scan_page.log("扫描完成: 未找到可用IP")
            self._set_status("完成（无结果）", "idle")

        self._set_busy(False)

    def _scan_aborted(self):
        self.scanning = False
        self.progress_bar.setValue(0)
        self._set_status("已停止", "idle")
        self._set_busy(False)

    def _region_lines(self, results: List[Dict]) -> List[str]:
        return [f"  {s['code']}  {s['name']}: {s['count']}" for s in region_stats(results)]

    # ================= 测速 =================
    def _start_speed_test(self, region_code: Optional[str] = None,
                          selected_ips: Optional[List[Dict]] = None,
                          label: Optional[str] = None):
        if task_manager.busy:
            CustomMessageBox.warning(self, "提示", "已有任务正在运行")
            return
        if not self.scan_results:
            CustomMessageBox.warning(self, "提示", "请先扫描或加载扫描结果")
            return

        opts = self.speed_page.collect()
        opts["current_port"] = self.current_scan_port
        opts["region_code"] = region_code
        opts["selected_ips"] = selected_ips
        opts["label"] = label
        task = create_speed_task(self.scan_results, opts, self.app_settings)
        if not task_manager.start_speed_test(task):
            CustomMessageBox.warning(self, "提示", "任务启动失败：已有任务正在运行")
            return

        self.speed_testing = True
        self.progress_bar.setValue(0)
        self._set_status("测速中…", "busy")
        self._set_busy(True)
        self._set_page(PAGE_SPEED)

    def _on_single_speed(self, info):
        if isinstance(info, dict) and "_multiple" in info:
            self._start_speed_test(selected_ips=info["_multiple"], label="自选测速")
        else:
            self._start_speed_test(selected_ips=[info], label="单点测速")

    def _on_region_speed(self, codes: List[str]):
        codes_u = {c.upper() for c in codes}
        matched = [r for r in self.scan_results
                   if (r.get("iata_code") or "").upper() in codes_u]
        if not matched:
            CustomMessageBox.warning(self, "提示", "所选地区没有匹配的 IP")
            return
        self._start_speed_test(selected_ips=matched, label="地区测速")

    def _start_region_speed_text(self):
        region = self.speed_page.collect()["region"]
        matched = [r for r in self.scan_results
                   if (r.get("iata_code") or "").upper() == region]
        if not matched:
            available = sorted({(r.get("iata_code") or "").upper()
                                for r in self.scan_results if r.get("iata_code")})
            CustomMessageBox.warning(
                self, "提示",
                f"未找到地区码 {region} 的IP\n可用地区码: {', '.join(available[:30])}"
            )
            return
        # 与「结果页地区芯片」保持同一语义：测该地区全部匹配 IP
        self._start_speed_test(selected_ips=matched, label="地区测速")

    def _start_full_speed(self):
        self._start_speed_test()

    def _speed_finished(self, results: List[Dict]):
        self.speed_testing = False
        self.speed_results = results or []
        self.progress_bar.setValue(100)
        self.speed_page.set_results(self.speed_results)

        if results:
            ip_version = self._history_ip_version(results, self.current_ip_version)
            save_results_to_file(results, ip_version, "speed")
            best = results[0]
            self._set_status(f"完成 · 最快 {best.get('download_speed', 0)} MB/s", "run")
            self.speed_page.log(
                f"✅ 测速完成: {len(results)} 个结果，最优 {best.get('ip')} "
                f"({best.get('download_speed')} MB/s, 评分 {best.get('score')})"
            )
            if not self.isVisible():
                self.tray_icon.showMessage(
                    "CloudTrace 测速完成",
                    f"最快 {best.get('download_speed', 0)} MB/s（{best.get('chinese_name', '')}）",
                    QSystemTrayIcon.Information, 3000,
                )
        else:
            self._set_status("测速完成（无结果）", "idle")

        self._set_busy(False)
        self._set_page(PAGE_SPEED)

    def _speed_aborted(self):
        """用户中止测速：结果不写入历史，也不显示「完成」。"""
        self.speed_testing = False
        self.progress_bar.setValue(0)
        self._set_status("已停止", "idle")
        self._set_busy(False)

    # ================= 停止 =================
    def _confirm_stop(self):
        if not task_manager.busy:
            return
        ans = CustomMessageBox.question(
            self, "确认停止",
            "确定要停止当前正在运行的任务吗？\n未完成的进度将会丢失。",
            ["停止", "取消"], "取消",
        )
        if ans == "停止":
            self.stop_all_tasks()

    def stop_all_tasks(self):
        task_manager.stop()
        self.scanning = False
        self.speed_testing = False
        self._set_status("已停止", "idle")
        self._set_busy(False)
        self.scan_page.log("⚠️ 任务已停止")

    # ================= 导出 =================
    def _export(self, initial: str = None):
        has_scan = bool(self.scan_results)
        has_speed = bool(self.speed_results)
        if not has_scan and not has_speed:
            CustomMessageBox.warning(self, "提示", "没有可导出的结果")
            return

        dlg = ExportDialog(has_scan, has_speed, self, initial_choice=initial)
        if dlg.exec() != QDialog.Accepted or not dlg.choice:
            return

        timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
        filters = "CSV文件 (*.csv);;JSON文件 (*.json);;TXT文件 (*.txt);;所有文件 (*)"
        saved = []
        try:
            if dlg.choice in ("scan", "both") and has_scan:
                path, _ = QFileDialog.getSaveFileName(
                    self, "保存扫描结果", f"cf_scan_{timestamp}.csv", filters)
                if path:
                    write_export(path, self.scan_results, "scan", fields=dlg.fields,
                                 fmt=dlg.format)
                    saved.append(path)
            if dlg.choice in ("speed", "both") and has_speed:
                path, _ = QFileDialog.getSaveFileName(
                    self, "保存测速结果", f"cf_speed_{timestamp}.csv", filters)
                if path:
                    write_export(path, self.speed_results, "speed", fields=dlg.fields,
                                 qualified_only=dlg.qualified_only, min_speed=dlg.min_speed,
                                 fmt=dlg.format)
                    saved.append(path)
            if saved:
                msg = "已导出:\n" + "\n".join(saved)
                self.scan_page.log(f"✅ {msg}")
                CustomMessageBox.information(self, "导出成功", msg)
        except Exception as e:
            logger.exception("导出失败")
            CustomMessageBox.critical(self, "错误", f"导出失败: {e}")

    # ================= 历史 =================
    def _load_history(self, filepath: str, type_key: str):
        data = load_results_from_file(filepath)
        if data is None or not data.get("results"):
            CustomMessageBox.warning(self, "错误", "加载失败：文件损坏或结果为空")
            return
        results = data["results"]
        save_time = data.get("save_time", "未知")

        if type_key == "scan":
            self.scan_results = results
            self.current_ip_version = data.get("ip_version", self.current_ip_version)
            self.current_scan_port = results[0].get("port", 443)
            scan_mode = results[0].get("scan_mode", "tcping")
            self.current_scan_mode = scan_mode
            self.current_funnel = {}
            # 同步到全局任务状态，保证 Web 面板看到的是同一份数据
            task_manager.set_scan_results(results)
            self.result_page.set_results(results, {}, scan_mode)
            self.scan_page.log(f"✅ 已加载扫描记录 ({save_time})，共 {len(results)} 个IP")
            self._set_status(f"已加载 {len(results)} IP", "idle")
            self._set_page(PAGE_RESULT)
        else:
            self.speed_results = results
            self.current_ip_version = data.get("ip_version", self.current_ip_version)
            task_manager.set_speed_results(results)
            self.speed_page.set_results(results)
            self.scan_page.log(f"✅ 已加载测速记录 ({save_time})，共 {len(results)} 条")
            self._set_status(f"已加载 {len(results)} 条测速", "idle")
            self._set_page(PAGE_SPEED)

    def _export_history(self, filepath: str, type_key: str):
        data = load_results_from_file(filepath)
        if data is None or not data.get("results"):
            CustomMessageBox.warning(self, "错误", "加载失败：文件损坏或结果为空")
            return
        timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
        default_name = f"cf_{type_key}_{timestamp}.csv"
        path, _ = QFileDialog.getSaveFileName(
            self, "导出历史记录", default_name,
            "CSV文件 (*.csv);;JSON文件 (*.json);;TXT文件 (*.txt);;所有文件 (*)",
        )
        if not path:
            return
        try:
            write_export(path, data["results"], type_key)
            CustomMessageBox.information(self, "导出成功", f"已导出:\n{path}")
        except Exception as e:
            CustomMessageBox.critical(self, "错误", f"导出失败: {e}")

    def _delete_history(self, filepath: str):
        if delete_history(filepath):
            self.history_page.refresh()
            CustomMessageBox.information(self, "完成", "已删除")
        else:
            CustomMessageBox.warning(self, "错误", "删除失败")

    # ================= 托盘 / 关闭 =================
    def _setup_window_icon(self):
        try:
            icon_path = resource_path("favicon.ico")
            if os.path.exists(icon_path):
                icon = QIcon(icon_path)
                if not icon.isNull():
                    self.setWindowIcon(icon)
                    return
        except Exception as e:
            logging.warning(f"加载窗口图标失败: {e}")
        default_icon = QApplication.style().standardIcon(QStyle.SP_ComputerIcon)
        self.setWindowIcon(default_icon)

    def _init_tray(self):
        self.tray_icon = QSystemTrayIcon(self)
        self._setup_tray_icon()
        self.tray_icon.setToolTip(f"CloudTrace 云迹 V{get_version()}")

        tray_menu = QMenu()
        action_show = tray_menu.addAction("显示主窗口")
        action_show.triggered.connect(self._tray_show_window)
        tray_menu.addSeparator()

        self.action_ipv4_scan = tray_menu.addAction("开始 IPv4 扫描")
        self.action_ipv4_scan.triggered.connect(lambda: self._tray_start_scan(4))
        self.action_ipv6_scan = tray_menu.addAction("开始 IPv6 扫描")
        self.action_ipv6_scan.triggered.connect(lambda: self._tray_start_scan(6))
        tray_menu.addSeparator()

        action_quit = tray_menu.addAction("退出")
        action_quit.triggered.connect(self._quit_application)

        self.tray_icon.setContextMenu(tray_menu)
        self.tray_icon.activated.connect(self._on_tray_activated)
        self.tray_icon.show()

    def _setup_tray_icon(self):
        try:
            icon_path = resource_path("favicon.ico")
            if os.path.exists(icon_path):
                icon = QIcon(icon_path)
                if not icon.isNull():
                    self.tray_icon.setIcon(icon)
                    return
        except Exception as e:
            logging.warning(f"加载托盘图标失败: {e}")
        default_icon = QApplication.style().standardIcon(QStyle.SP_ComputerIcon)
        self.tray_icon.setIcon(default_icon)

    def _on_tray_activated(self, reason):
        if reason in (QSystemTrayIcon.Trigger, QSystemTrayIcon.DoubleClick):
            self._tray_show_window()

    def _tray_show_window(self):
        self.show()
        self.activateWindow()
        self.raise_()

    def _tray_start_scan(self, ip_version: int):
        if task_manager.busy:
            CustomMessageBox.warning(self, "提示", "已有任务正在运行")
            return
        self._tray_show_window()
        self._set_page(PAGE_SCAN)
        self.scan_page.seg_version.set_index(0 if ip_version == 4 else 1)
        self._start_scan_from_page()

    def _quit_application(self):
        logging.info("用户请求退出应用程序")
        if hasattr(self, "bridge"):
            self.bridge.detach()
        task_manager.stop(wait=True, timeout=3.0)
        http_server.stop()
        if hasattr(self, "tray_icon"):
            self.tray_icon.hide()
        QApplication.quit()

    def closeEvent(self, event):
        # 勾选「关闭到托盘」但系统没有托盘时，隐藏窗口会让程序无法再被唤出，
        # 因此这种情况下按正常关闭处理。
        tray_available = QSystemTrayIcon.isSystemTrayAvailable()
        if self.app_settings.get("tray_on_close", False) and tray_available:
            event.ignore()
            self.hide()
            self.tray_icon.showMessage(
                "CloudTrace 云迹",
                "程序已最小化到系统托盘",
                QSystemTrayIcon.Information, 2000,
            )
        else:
            self._quit_application()
            event.accept()
