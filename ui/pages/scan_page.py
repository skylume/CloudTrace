#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import threading
from typing import List, Optional

from PySide6.QtWidgets import (
    QWidget, QVBoxLayout, QHBoxLayout, QLabel, QLineEdit, QSpinBox,
    QComboBox, QPlainTextEdit, QPushButton, QFileDialog, QScrollArea,
    QGridLayout, QSizePolicy, QDoubleSpinBox,
)
from PySide6.QtCore import Qt, Signal
from PySide6.QtGui import QFont

from core.constants import FONT_FAMILY, PORT_OPTIONS
from core.importer import (
    parse_source_text, load_source_text_from_file, describe_source_stats,
)
from core.sources import (
    sanitize_sources, format_sources_text, fetch_sources, DEFAULT_SOURCES,
)
from core.utils import to_float, to_int
from settings import load_custom_cidrs, save_settings, CIDR_MODES
from ui.widgets import Card, Segmented, FunnelBar, LogTerminal
from ui.styles import (
    FONT_SMALL, FIELD_STYLE, C_BLUE, C_BLUE_DARK, C_MUTED, C_MUTED_LIGHT,
    btn_stylesheet, ghost_btn_stylesheet,
)
from ui.dialogs import CustomMessageBox


SOURCE_PLACEHOLDER = (
    "每行一条，自动识别类型（可混着写）：\n"
    "1.2.3.0/24                  # CIDR → 按采样密度抽 IP\n"
    "2606:4700::/32              # IPv6 CIDR\n"
    "1.2.3.4                     # 单个 IP → 逐条直测\n"
    "1.2.3.4:8443                # IP + 端口\n"
    "1.2.3.4-1.2.3.20            # IP 段（≤1024 个直接展开）\n"
    "[2606:4700::1111]:8443     # IPv6 + 端口\n"
    "example.com                 # 域名（自动解析，最多 50 个 IP）\n"
    "https://example.com:8443/   # 带 scheme → 决定是否走 TLS"
)

SOURCE_SAMPLE = (
    "# CIDR（采样）\n"
    "104.16.0.0/24\n"
    "# 单个 IP / 端口\n"
    "1.1.1.1\n"
    "1.0.0.1:8443\n"
    "# IP 段\n"
    "104.16.0.10-104.16.0.40\n"
    "# 域名\n"
    "cloudflare.com\n"
)

SOURCES_PLACEHOLDER = (
    "每行一个数据源：名称 | URL\n"
    "以 # 开头的行表示禁用该源。例如：\n"
    "cfnb 聚合列表 | https://zip.cm.edu.kg/all.txt\n"
    "# 备用源 | https://example.com/all.txt"
)


class ScanPage(QWidget):
    """扫描页：参数 + IP 来源 + 远程数据源 + 漏斗 + 日志终端。"""

    start_requested = Signal()
    sources_preview_ready = Signal(str)

    def __init__(self, app_settings: dict, parent=None):
        super().__init__(parent)
        self.app_settings = app_settings
        self._suppress_persist = False
        self._build()

    # ---------------- UI ----------------
    def _build(self):
        outer = QVBoxLayout(self)
        outer.setContentsMargins(20, 16, 20, 18)
        outer.setSpacing(0)

        scroll = QScrollArea()
        scroll.setWidgetResizable(True)
        scroll.setFrameShape(QScrollArea.NoFrame)
        scroll.setStyleSheet("QScrollArea { background: transparent; border: none; }")
        inner = QWidget()
        inner.setStyleSheet("background: transparent;")
        lay = QVBoxLayout(inner)
        lay.setContentsMargins(0, 0, 8, 0)
        lay.setSpacing(14)

        # ---- 上排：扫描参数 + 本次扫描 ----
        top_row = QHBoxLayout()
        top_row.setSpacing(14)

        param_card = Card("扫描参数", "决定扫描速度与筛选严格度")
        grid = QGridLayout()
        grid.setHorizontalSpacing(18)
        grid.setVerticalSpacing(12)

        def field(label_text, widget, hint: str = ""):
            wrap = QVBoxLayout()
            wrap.setSpacing(5)
            lbl = QLabel(label_text)
            lbl.setProperty("class", "fieldLabel")
            lbl.setFont(FONT_SMALL)
            wrap.addWidget(lbl)
            wrap.addWidget(widget)
            if hint:
                h = QLabel(hint)
                h.setProperty("class", "hintText")
                h.setWordWrap(True)
                wrap.addWidget(h)
            return wrap

        self.seg_version = Segmented(["IPv4", "IPv6"], 0)
        grid.addLayout(field("IP 版本", self.seg_version), 0, 0)

        self.combo_port = QComboBox()
        for port in PORT_OPTIONS:
            self.combo_port.addItem(port)
        self.combo_port.setCurrentText("443")
        self.combo_port.setFixedHeight(32)
        grid.addLayout(field("端口", self.combo_port), 0, 1)

        self.spin_workers = QSpinBox()
        self.spin_workers.setRange(10, 500)
        self.spin_workers.setValue(to_int(self.app_settings.get("workers"), 200, 10, 500))
        self.spin_workers.setSingleStep(50)
        self.spin_workers.setFixedHeight(32)
        grid.addLayout(field("并发数", self.spin_workers), 0, 2)

        self.spin_threshold = QSpinBox()
        self.spin_threshold.setRange(50, 999)
        self.spin_threshold.setValue(to_int(self.app_settings.get("latency_threshold"), 230, 50, 999))
        self.spin_threshold.setSingleStep(10)
        self.spin_threshold.setSuffix(" ms")
        self.spin_threshold.setFixedHeight(32)
        grid.addLayout(field("延迟阈值", self.spin_threshold), 1, 0)

        self.spin_sample = QSpinBox()
        self.spin_sample.setRange(100, 50000)
        self.spin_sample.setSingleStep(500)
        self.spin_sample.setValue(to_int(self.app_settings.get("sample_max"), 5000, 100, 50000))
        self.spin_sample.setFixedHeight(32)
        grid.addLayout(field("采样上限", self.spin_sample), 1, 1)

        self.spin_ping = QSpinBox()
        self.spin_ping.setRange(0, 10)
        self.spin_ping.setValue(to_int(self.app_settings.get("ping_times"), 0, 0, 10))
        self.spin_ping.setSpecialValueText("自动")
        self.spin_ping.setFixedHeight(32)
        grid.addLayout(field("探测次数", self.spin_ping, "0 = 自动（IPv4 三次 / IPv6 两次）"), 1, 2)

        self.seg_mode = Segmented(["TCPing", "HTTPing"],
                                  0 if self.app_settings.get("scan_mode", "tcping") == "tcping" else 1)
        grid.addLayout(field("扫描方式", self.seg_mode), 2, 0, 1, 2)

        self.input_prefilter = QLineEdit(str(self.app_settings.get("pre_filter_ports") or ""))
        self.input_prefilter.setPlaceholderText("如 443,8443")
        self.input_prefilter.setFixedHeight(32)
        grid.addLayout(field("端口过滤", self.input_prefilter, "留空=不过滤；在测速前先剔除其它端口"), 2, 2)

        mode_hint = QLabel("TCPing 测握手延迟；HTTPing 测 TTFB（阈值自动 ×1.3/×4.0 换算），两者数据不可比")
        mode_hint.setProperty("class", "hintText")
        mode_hint.setWordWrap(True)
        grid.addWidget(mode_hint, 3, 0, 1, 3)
        grid.setColumnStretch(0, 1)
        grid.setColumnStretch(1, 1)
        grid.setColumnStretch(2, 1)

        param_card.body().addLayout(grid)
        # AlignTop：两张卡片各自按内容高度显示，不让「本次扫描」被拉伸到
        # 与「扫描参数」等高，否则漏斗下方会留出一大片空白。
        top_row.addWidget(param_card, 3, Qt.AlignTop)

        # ---- 本次扫描 ----
        funnel_card = Card("本次扫描", "漏斗实时反映筛选情况")
        self.funnel_bar = FunnelBar()
        funnel_card.body().addWidget(self.funnel_bar)
        top_row.addWidget(funnel_card, 2, Qt.AlignTop)

        lay.addLayout(top_row)

        # ---- IP 来源 ----
        source_card = Card("IP 来源", "自定义框同时支持 CIDR / 单个 IP / IP 段 / 域名，自动分类")
        source_top = QHBoxLayout()
        source_top.setSpacing(10)
        lbl = QLabel("来源模式")
        lbl.setProperty("class", "fieldLabel")
        lbl.setFont(FONT_SMALL)
        self.combo_source = QComboBox()
        self.combo_source.addItems(list(CIDR_MODES))
        self.combo_source.setCurrentText(self.app_settings.get("cidr_mode", "仅官方"))
        self.combo_source.setFixedHeight(32)
        self.combo_source.setMinimumWidth(150)
        source_top.addWidget(lbl)
        source_top.addWidget(self.combo_source)
        source_top.addStretch()

        self.btn_import_file = QPushButton("📂 导入文件")
        self.btn_import_sample = QPushButton("填入示例")
        self.btn_import_clear = QPushButton("清空")
        for b in (self.btn_import_file, self.btn_import_sample, self.btn_import_clear):
            b.setCursor(Qt.PointingHandCursor)
            b.setFont(FONT_SMALL)
            b.setFixedHeight(30)
            b.setStyleSheet(ghost_btn_stylesheet())
        self.btn_import_file.clicked.connect(self._import_file)
        self.btn_import_sample.clicked.connect(
            lambda: self.text_source.setPlainText(SOURCE_SAMPLE))
        self.btn_import_clear.clicked.connect(lambda: self.text_source.setPlainText(""))
        source_top.addWidget(self.btn_import_file)
        source_top.addWidget(self.btn_import_sample)
        source_top.addWidget(self.btn_import_clear)
        source_card.body().addLayout(source_top)

        self.text_source = QPlainTextEdit()
        self.text_source.setFont(FONT_SMALL)
        self.text_source.setPlaceholderText(SOURCE_PLACEHOLDER)
        self.text_source.setMinimumHeight(132)
        self.text_source.textChanged.connect(self._update_source_preview)
        source_card.body().addWidget(self.text_source)

        self.lbl_source_preview = QLabel("")
        self.lbl_source_preview.setProperty("class", "hintText")
        self.lbl_source_preview.setWordWrap(True)
        source_card.body().addWidget(self.lbl_source_preview)

        self.lbl_source_official = QLabel(
            "将扫描 Cloudflare 官方 IPv4/IPv6 网段（自动更新，无需填写）。"
            "如需指定网段或节点，请把来源模式切换为「仅自定义」或「官方+自定义」。")
        self.lbl_source_official.setProperty("class", "hintText")
        self.lbl_source_official.setWordWrap(True)
        source_card.body().addWidget(self.lbl_source_official)

        lay.addWidget(source_card)

        # ---- 远程数据源 ----
        remote_card = Card("数据源", "扫描前从远程列表拉取节点，合并进本次扫描（参考 cfnb ADDITIONAL_SOURCES）")
        self.chk_remote = QComboBox()
        self.chk_remote.addItems(["不启用", "启用"])
        self.chk_remote.setFixedHeight(30)
        self.chk_remote.setMinimumWidth(110)
        self.chk_remote.setCurrentText(
            "启用" if self.app_settings.get("use_remote_sources") else "不启用")
        remote_row = QHBoxLayout()
        remote_row.setSpacing(10)
        remote_lbl = QLabel("远程数据源")
        remote_lbl.setProperty("class", "fieldLabel")
        remote_lbl.setFont(FONT_SMALL)
        remote_row.addWidget(remote_lbl)
        remote_row.addWidget(self.chk_remote)
        self.lbl_remote_state = QLabel("")
        self.lbl_remote_state.setProperty("class", "hintText")
        remote_row.addWidget(self.lbl_remote_state)
        remote_row.addStretch()
        remote_card.body().addLayout(remote_row)

        self.text_sources = QPlainTextEdit()
        self.text_sources.setFont(FONT_SMALL)
        self.text_sources.setPlaceholderText(SOURCES_PLACEHOLDER)
        self.text_sources.setMinimumHeight(76)
        self.text_sources.setPlainText(
            format_sources_text(self.app_settings.get("remote_sources") or DEFAULT_SOURCES))
        remote_card.body().addWidget(self.text_sources)

        remote_params = QHBoxLayout()
        remote_params.setSpacing(12)

        def _spin(minimum, maximum, value, suffix=""):
            box = QSpinBox()
            box.setRange(minimum, maximum)
            box.setValue(value)
            if suffix:
                box.setSuffix(suffix)
            box.setFixedHeight(30)
            box.setFixedWidth(96)
            return box

        self.spin_retries = _spin(1, 10, to_int(self.app_settings.get("source_retries"), 3, 1, 10), " 次")
        self.spin_retry_delay = QDoubleSpinBox()
        self.spin_retry_delay.setRange(0, 60)
        self.spin_retry_delay.setDecimals(1)
        self.spin_retry_delay.setSuffix(" s")
        self.spin_retry_delay.setFixedHeight(30)
        self.spin_retry_delay.setFixedWidth(96)
        self.spin_retry_delay.setValue(
            to_float(self.app_settings.get("source_retry_delay"), 3.0, 0, 60))
        self.spin_timeout = QDoubleSpinBox()
        self.spin_timeout.setRange(1, 120)
        self.spin_timeout.setDecimals(1)
        self.spin_timeout.setSuffix(" s")
        self.spin_timeout.setFixedHeight(30)
        self.spin_timeout.setFixedWidth(96)
        self.spin_timeout.setValue(to_float(self.app_settings.get("source_timeout"), 8.0, 1, 120))

        for text, widget in (("重试次数", self.spin_retries),
                             ("重试间隔", self.spin_retry_delay),
                             ("超时", self.spin_timeout)):
            tag = QLabel(text)
            tag.setProperty("class", "fieldLabel")
            tag.setFont(FONT_SMALL)
            remote_params.addWidget(tag)
            remote_params.addWidget(widget)
        remote_params.addStretch()

        self.btn_preview_sources = QPushButton("🔍 拉取预览")
        self.btn_preview_sources.setCursor(Qt.PointingHandCursor)
        self.btn_preview_sources.setFont(FONT_SMALL)
        self.btn_preview_sources.setFixedHeight(30)
        self.btn_preview_sources.setStyleSheet(ghost_btn_stylesheet())
        self.btn_preview_sources.clicked.connect(self._preview_sources)
        remote_params.addWidget(self.btn_preview_sources)
        remote_card.body().addLayout(remote_params)

        self.lbl_sources_preview = QLabel("尚未拉取")
        self.lbl_sources_preview.setProperty("class", "hintText")
        self.lbl_sources_preview.setWordWrap(True)
        remote_card.body().addWidget(self.lbl_sources_preview)

        lay.addWidget(remote_card)

        # ---- 日志 ----
        self.terminal = LogTerminal("运行日志")
        lay.addWidget(self.terminal)

        # ---- 主按钮 ----
        btn_row = QHBoxLayout()
        self.btn_start = QPushButton("▶ 开始扫描")
        self.btn_start.setFixedSize(168, 42)
        self.btn_start.setFont(QFont(FONT_FAMILY, 12))
        self.btn_start.setCursor(Qt.PointingHandCursor)
        self.btn_start.setStyleSheet(btn_stylesheet(C_BLUE, hover_color=C_BLUE_DARK))
        self.btn_start.clicked.connect(self.start_requested.emit)
        btn_row.addStretch()
        btn_row.addWidget(self.btn_start)
        btn_row.addStretch()
        lay.addLayout(btn_row)

        lay.addStretch()
        scroll.setWidget(inner)
        outer.addWidget(scroll)

        self.setStyleSheet(FIELD_STYLE)
        self.sources_preview_ready.connect(self._on_preview_ready)
        self.combo_source.currentTextChanged.connect(self._on_source_changed)
        self.chk_remote.currentTextChanged.connect(self._on_remote_toggled)
        self._on_source_changed(self.combo_source.currentText())
        self._on_remote_toggled(self.chk_remote.currentText())

        saved_cidrs = load_custom_cidrs()
        if saved_cidrs:
            self.text_source.setPlainText(saved_cidrs)
        self._update_source_preview()

    # ---------------- 来源模式 ----------------
    def _on_source_changed(self, mode: str):
        is_custom = mode in ("仅自定义", "官方+自定义")
        for w in (self.text_source, self.lbl_source_preview,
                  self.btn_import_file, self.btn_import_sample, self.btn_import_clear):
            w.setVisible(is_custom)
        self.lbl_source_official.setVisible(not is_custom)
        if is_custom:
            self._update_source_preview()
        # 来源模式需要持久化，否则重启后总是回到「仅官方」（旧版行为回归）
        if self._suppress_persist:
            return
        if self.app_settings.get("cidr_mode") != mode:
            self.app_settings["cidr_mode"] = mode
            save_settings(self.app_settings)

    def _on_remote_toggled(self, text: str):
        enabled = (text == "启用")
        self.lbl_remote_state.setText(
            "扫描开始时先拉取列表，再与自定义来源合并" if enabled else "不发起任何网络请求")
        if self._suppress_persist:
            return
        if bool(self.app_settings.get("use_remote_sources")) != enabled:
            self.app_settings["use_remote_sources"] = enabled
            save_settings(self.app_settings)

    def _current_source_text(self) -> str:
        return self.text_source.toPlainText()

    def _update_source_preview(self):
        """实时预览解析结果（不做 DNS，避免逐键卡顿）。"""
        if not self.text_source.isVisible():
            return
        text = self._current_source_text().strip()
        if not text:
            self.lbl_source_preview.setText("等待输入：CIDR / 单个 IP / IP 段 / 域名")
            return
        ip_version = 4 if self.seg_version.index() == 0 else 6
        cidrs, entries, errors, stats = parse_source_text(
            text, int(self.combo_port.currentText()), ip_version, resolve_domains=False)
        summary = describe_source_stats(stats)
        self.lbl_source_preview.setText(f"解析预览：{summary}")
        if errors:
            self.lbl_source_preview.setText(
                f"解析预览：{summary}　⚠ {len(errors)} 行有问题：{errors[0]}")

    # ---------------- 远程数据源 ----------------
    def _collect_sources(self):
        return sanitize_sources(self.text_sources.toPlainText())

    def _preview_sources(self):
        sources = [s for s in self._collect_sources() if s.get("enabled")]
        if not sources:
            CustomMessageBox.warning(self, "提示", "没有启用中的数据源（每行一个 URL，# 开头表示禁用）")
            return
        port = int(self.combo_port.currentText())
        retries = self.spin_retries.value()
        delay = self.spin_retry_delay.value()
        timeout = self.spin_timeout.value()
        self.btn_preview_sources.setEnabled(False)
        self.lbl_sources_preview.setText("正在拉取，请稍候…")

        def worker():
            try:
                entries, report = fetch_sources(sources, port, timeout, retries, delay)
                lines = list(report)
                if entries:
                    sample = ", ".join(f"{e['ip']}:{e['port']}" for e in entries[:5])
                    lines.append(f"合计 {len(entries)} 条；示例: {sample}")
                msg = "\n".join(lines) or "未返回任何内容"
            except Exception as e:
                msg = f"拉取失败: {e}"
            self.sources_preview_ready.emit(msg)

        threading.Thread(target=worker, daemon=True, name="ct-source-preview").start()

    def _on_preview_ready(self, msg: str):
        self.btn_preview_sources.setEnabled(True)
        self.lbl_sources_preview.setText(msg)

    def reload_from_settings(self, full: bool = False):
        """把设置回填到扫描页表单，保证界面与实际设置一致。

        full=False 只同步「设置页拥有」的字段（scan_mode / sample_max），
        避免保存设置时覆盖用户在扫描页尚未持久化的改动；
        full=True 用于「恢复默认」，把全部参数一并回填。
        """
        s = self.app_settings
        self._suppress_persist = True
        try:
            self.spin_sample.setValue(to_int(s.get("sample_max"), 5000, 100, 50000))
            self.seg_mode.set_index(0 if s.get("scan_mode", "tcping") == "tcping" else 1)
            if full:
                self.spin_workers.setValue(to_int(s.get("workers"), 200, 10, 500))
                self.spin_threshold.setValue(to_int(s.get("latency_threshold"), 230, 50, 999))
                self.spin_ping.setValue(to_int(s.get("ping_times"), 0, 0, 10))
                self.combo_source.setCurrentText(s.get("cidr_mode", "仅官方"))
                self.input_prefilter.setText(str(s.get("pre_filter_ports") or ""))
                self.chk_remote.setCurrentText("启用" if s.get("use_remote_sources") else "不启用")
                self.text_sources.setPlainText(
                    format_sources_text(s.get("remote_sources") or DEFAULT_SOURCES))
                self.spin_retries.setValue(to_int(s.get("source_retries"), 3, 1, 10))
                self.spin_retry_delay.setValue(
                    to_float(s.get("source_retry_delay"), 3.0, 0, 60))
                self.spin_timeout.setValue(to_float(s.get("source_timeout"), 8.0, 1, 120))
        finally:
            self._suppress_persist = False
        self._on_source_changed(self.combo_source.currentText())
        self._on_remote_toggled(self.chk_remote.currentText())
        self._update_source_preview()

    def persist_scan_params(self):
        """把扫描页参数写回设置，保证下次启动沿用（不覆盖设置页拥有的 sample_max/scan_mode）。"""
        changed = False
        payload = {
            "workers": self.spin_workers.value(),
            "latency_threshold": self.spin_threshold.value(),
            "ping_times": self.spin_ping.value(),
            "pre_filter_ports": self.input_prefilter.text().strip(),
            "use_remote_sources": self.chk_remote.currentText() == "启用",
            "remote_sources": self._collect_sources(),
            "source_retries": self.spin_retries.value(),
            "source_retry_delay": self.spin_retry_delay.value(),
            "source_timeout": self.spin_timeout.value(),
        }
        for key, value in payload.items():
            if self.app_settings.get(key) != value:
                self.app_settings[key] = value
                changed = True
        if changed:
            save_settings(self.app_settings)

    def _import_file(self):
        path, _ = QFileDialog.getOpenFileName(
            self, "导入来源列表", "", "文本文件 (*.txt *.csv);;所有文件 (*)"
        )
        if not path:
            return
        text, error = load_source_text_from_file(path)
        if error:
            CustomMessageBox.warning(self, "导入失败", error)
            return
        self.text_source.setPlainText(text)
        self._update_source_preview()
        self.scan_page_log(f"已导入来源文件: {path}")

    def scan_page_log(self, msg: str):
        self.terminal.append_line(msg)

    # ---------------- 数据收集 ----------------
    def collect(self) -> Optional[dict]:
        """校验并返回扫描参数；失败时弹窗并返回 None。"""
        source_mode = self.combo_source.currentText()
        ip_version = 4 if self.seg_version.index() == 0 else 6
        port = int(self.combo_port.currentText())
        use_remote = (self.chk_remote.currentText() == "启用")

        cidrs: List[str] = []
        entries: List[dict] = []
        source_text = ""

        if source_mode != "仅官方":
            source_text = self.text_source.toPlainText().strip()
            stats = {}
            if source_text:
                cidrs, entries, errors, stats = parse_source_text(
                    source_text, port, ip_version, resolve_domains=True)
                if errors:
                    msg = errors[:10]
                    if len(errors) > 10:
                        msg.append(f"... 共 {len(errors)} 行有问题")
                    CustomMessageBox.warning(self, "来源格式错误", "\n".join(msg))
                    return None
            if not cidrs and not entries and not use_remote:
                if stats.get("skipped"):
                    CustomMessageBox.warning(
                        self, "提示",
                        f"来源中的条目与所选 IP 版本(IPv{ip_version})不符。\n"
                        "请检查「IP 版本」或来源内容。")
                else:
                    CustomMessageBox.warning(
                        self, "提示",
                        "自定义来源为空：请填写 CIDR / 单个 IP / IP 段 / 域名，或启用远程数据源")
                return None

        return {
            "ip_version": ip_version,
            "source_mode": source_mode,
            "cidrs": cidrs,
            "entries": entries,
            "port": port,
            "workers": self.spin_workers.value(),
            "threshold": self.spin_threshold.value(),
            "sample_max": self.spin_sample.value(),
            "ping_times": self.spin_ping.value(),
            "scan_mode": "tcping" if self.seg_mode.index() == 0 else "httping",
            "cidr_text": source_text,
            "pre_filter_ports": self.input_prefilter.text().strip(),
            "use_remote_sources": use_remote,
            "remote_sources": self._collect_sources(),
            "source_retries": self.spin_retries.value(),
            "source_retry_delay": self.spin_retry_delay.value(),
            "source_timeout": self.spin_timeout.value(),
        }

    def health_snapshot(self) -> dict:
        """给配置体检用的当前扫描参数。"""
        return {
            "latency_threshold": self.spin_threshold.value(),
            "workers": self.spin_workers.value(),
            "sample_max": self.spin_sample.value(),
            "scan_mode": "tcping" if self.seg_mode.index() == 0 else "httping",
            "pre_filter_ports": self.input_prefilter.text().strip(),
            "use_remote_sources": self.chk_remote.currentText() == "启用",
            "remote_sources": self._collect_sources(),
            "source_retries": self.spin_retries.value(),
            "source_retry_delay": self.spin_retry_delay.value(),
            "source_timeout": self.spin_timeout.value(),
        }

    # ---------------- 状态 ----------------
    def set_funnel(self, steps: list):
        self.funnel_bar.set_steps(steps)

    def set_busy(self, busy: bool):
        self.btn_start.setEnabled(not busy)
        self.btn_start.setText("扫描中…" if busy else "▶ 开始扫描")
        self.btn_preview_sources.setEnabled(not busy)

    def log(self, msg: str):
        self.terminal.append_line(msg)
