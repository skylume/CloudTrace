#!/usr/bin/env python3
# -*- coding: utf-8 -*-

from typing import Callable, List

from PySide6.QtWidgets import (
    QWidget, QVBoxLayout, QHBoxLayout, QLabel, QCheckBox, QSpinBox,
    QDoubleSpinBox, QComboBox, QLineEdit, QPushButton, QScrollArea, QApplication,
)
from PySide6.QtCore import Qt, Signal
from PySide6.QtGui import QFont

from core.constants import FONT_FAMILY
from core.speed_url import (
    SPEED_URL_PRESETS, CUSTOM_SPEED_URL, CF_SPEED_URL,
    url_to_preset_value,
)
from core.utils import to_float, to_int
from settings import save_settings, reset_settings, DEFAULT_SETTINGS
from service.health import validate_settings
from ui.widgets import Card
from ui.styles import (
    FONT_SMALL, C_BLUE, C_BLUE_DARK, C_MUTED, C_MUTED_LIGHT, FIELD_STYLE,
    btn_stylesheet, ghost_btn_stylesheet,
)
from ui.dialogs import CustomMessageBox


def _hint(text: str) -> QLabel:
    lbl = QLabel(text)
    lbl.setProperty("class", "hintText")
    lbl.setWordWrap(True)
    return lbl


class SettingsPage(QWidget):
    """设置页：分组卡片表单 + 保存/恢复/体检。"""

    saved = Signal(dict)       # 保存设置：同步「设置页拥有」的字段
    restored = Signal(dict)    # 恢复默认：需全量回填各页表单

    def __init__(self, app_settings: dict, parent=None):
        super().__init__(parent)
        self.app_settings = app_settings
        self._providers: List[Callable[[], dict]] = []
        self._build()

    def set_param_provider(self, fn: Callable[[], dict]):
        self._providers.append(fn)

    # ---------------- UI ----------------
    @staticmethod
    def _row(label_text: str, widget, hint: str = "") -> QHBoxLayout:
        row = QHBoxLayout()
        row.setSpacing(10)
        box = QVBoxLayout()
        box.setSpacing(1)
        lbl = QLabel(label_text)
        lbl.setProperty("class", "fieldLabel")
        lbl.setFont(FONT_SMALL)
        box.addWidget(lbl)
        if hint:
            box.addWidget(_hint(hint))
        row.addLayout(box, 1)
        row.addWidget(widget)
        return row

    def _build(self):
        outer = QVBoxLayout(self)
        outer.setContentsMargins(20, 16, 20, 18)
        outer.setSpacing(14)

        scroll = QScrollArea()
        scroll.setWidgetResizable(True)
        scroll.setFrameShape(QScrollArea.NoFrame)
        scroll.setStyleSheet("QScrollArea { background: transparent; border: none; }")
        inner = QWidget()
        inner.setStyleSheet("background: transparent;")
        lay = QVBoxLayout(inner)
        lay.setContentsMargins(0, 0, 8, 0)
        lay.setSpacing(14)

        grid_wrap = QHBoxLayout()
        grid_wrap.setSpacing(14)
        left = QVBoxLayout()
        left.setSpacing(14)
        right = QVBoxLayout()
        right.setSpacing(14)

        s = self.app_settings

        # ---- 常规 ----
        card_general = Card("常规")
        self.chk_tray = QCheckBox("关闭窗口时最小化到系统托盘")
        self.chk_tray.setChecked(bool(s.get("tray_on_close", False)))
        card_general.body().addWidget(self.chk_tray)
        card_general.body().addWidget(
            _hint("勾选后点击关闭按钮不会退出程序，可从托盘菜单快速发起扫描"))
        left.addWidget(card_general)

        # ---- 扫描默认值 ----
        card_scan = Card("扫描默认值", "与「扫描」页共享同一份设置")
        self.combo_scan_mode = QComboBox()
        self.combo_scan_mode.addItems(["tcping", "httping"])
        self.combo_scan_mode.setCurrentText(s.get("scan_mode", "tcping"))
        self.combo_scan_mode.setFixedHeight(32)
        card_scan.body().addLayout(self._row(
            "扫描方式", self.combo_scan_mode,
            "tcping 测握手延迟；httping 测 TTFB，阈值自动换算"))

        self.spin_sample = QSpinBox()
        self.spin_sample.setRange(100, 50000)
        self.spin_sample.setSingleStep(500)
        self.spin_sample.setValue(to_int(s.get("sample_max"), 5000, 100, 50000))
        self.spin_sample.setFixedHeight(32)
        card_scan.body().addLayout(self._row(
            "采样上限", self.spin_sample, "单次扫描生成的 IP 数量上限"))
        left.addWidget(card_scan)

        # ---- 评分权重 ----
        card_score = Card("综合评分权重", "score = 速度权重 × MB/s ÷ (1 + 延迟权重 × 延迟秒)")
        self.spin_w_speed = QDoubleSpinBox()
        self.spin_w_speed.setRange(0, 50)
        self.spin_w_speed.setSingleStep(0.5)
        self.spin_w_speed.setValue(to_float(s.get("score_speed_weight"), 3.0, 0, 50))
        self.spin_w_speed.setFixedHeight(32)
        card_score.body().addLayout(self._row("速度权重", self.spin_w_speed))
        self.spin_w_latency = QDoubleSpinBox()
        self.spin_w_latency.setRange(0, 50)
        self.spin_w_latency.setSingleStep(0.5)
        self.spin_w_latency.setValue(to_float(s.get("score_latency_weight"), 3.0, 0, 50))
        self.spin_w_latency.setFixedHeight(32)
        card_score.body().addLayout(self._row(
            "延迟权重", self.spin_w_latency, "延迟作分母惩罚，权重越大越偏向低延迟节点"))
        left.addWidget(card_score)
        left.addStretch()

        # ---- 测速 ----
        card_speed = Card("测速", "下载实测参数")
        self.combo_speed_url = QComboBox()
        for label, value in SPEED_URL_PRESETS:
            self.combo_speed_url.addItem(label, value)
        self.combo_speed_url.setFixedHeight(32)
        self.combo_speed_url.setMinimumWidth(210)
        self.input_speed_url = QLineEdit()
        self.input_speed_url.setPlaceholderText("speed.cloudflare.com/__down?bytes=99999999")
        self.input_speed_url.setFixedHeight(32)
        url_row = QHBoxLayout()
        url_row.setSpacing(8)
        url_lbl = QLabel("测速地址")
        url_lbl.setProperty("class", "fieldLabel")
        url_row.addWidget(url_lbl)
        url_row.addWidget(self.combo_speed_url)
        url_row.addWidget(self.input_speed_url, 1)
        card_speed.body().addLayout(url_row)
        card_speed.body().addWidget(_hint(
            "auto 会先探测出口 ISP：识别到中国移动时从移动友好/移动专属源里随机取一个，"
            "否则用 Cloudflare 官方源。选「自定义…」可手填任意下载地址"))
        self._apply_speed_url_to_ui(s.get("speed_url", "auto"))
        self.combo_speed_url.currentIndexChanged.connect(self._on_speed_url_preset_changed)

        self.chk_verify = QCheckBox("测速前验证节点为 Cloudflare（防劫持/非 CF，稍慢更准）")
        self.chk_verify.setChecked(bool(s.get("verify_nodes", True)))
        card_speed.body().addWidget(self.chk_verify)

        self.spin_min_speed = QDoubleSpinBox()
        self.spin_min_speed.setRange(0, 200)
        self.spin_min_speed.setDecimals(1)
        self.spin_min_speed.setSuffix(" MB/s")
        self.spin_min_speed.setFixedHeight(32)
        self.spin_min_speed.setValue(to_float(s.get("min_speed"), 0.0, 0, 200))
        card_speed.body().addLayout(self._row(
            "合格阈值", self.spin_min_speed, "低于该速度的节点视为不合格（可用于筛掉假高速）"))

        self.spin_interval = QSpinBox()
        self.spin_interval.setRange(0, 15)
        self.spin_interval.setSuffix(" s")
        self.spin_interval.setFixedHeight(32)
        self.spin_interval.setValue(to_int(s.get("download_interval"), 3, 0, 15))
        card_speed.body().addLayout(self._row(
            "测速间隔", self.spin_interval, "每个 IP 之间的等待时间；间隔过小容易触发 Cloudflare 限速"))

        self.spin_speed_workers = QSpinBox()
        self.spin_speed_workers.setRange(1, 16)
        self.spin_speed_workers.setFixedHeight(32)
        self.spin_speed_workers.setValue(to_int(s.get("speed_workers"), 1, 1, 16))
        card_speed.body().addLayout(self._row(
            "测速并发", self.spin_speed_workers,
            "1 = 串行（最准）；2~5 可显著提速，但并发过高会互相抢带宽、让读数偏低"))

        self.spin_result_limit = QSpinBox()
        self.spin_result_limit.setRange(0, 1000)
        self.spin_result_limit.setFixedHeight(32)
        self.spin_result_limit.setSpecialValueText("不限")
        self.spin_result_limit.setValue(to_int(s.get("speed_result_limit"), 0, 0, 1000))
        card_speed.body().addLayout(self._row(
            "提前收敛", self.spin_result_limit, "收够 N 个结果即停止测速（0 = 不提前停止）"))

        self.spin_region_topn = QSpinBox()
        self.spin_region_topn.setRange(0, 100)
        self.spin_region_topn.setFixedHeight(32)
        self.spin_region_topn.setSpecialValueText("不限")
        self.spin_region_topn.setValue(to_int(s.get("per_region_topn"), 0, 0, 100))
        card_speed.body().addLayout(self._row(
            "分地区 TopN", self.spin_region_topn,
            "每个地区只取延迟最低的 N 个进测速队列（0 = 不限）；地区多时能省下大量测速时间"))
        right.addWidget(card_speed)

        # ---- HTTP 面板 ----
        card_http = Card("HTTP 服务面板", "浏览器访问同一套面板，与桌面端共享任务状态")
        self.chk_http = QCheckBox("启用 HTTP 服务")
        self.chk_http.setChecked(bool(s.get("http_enabled", True)))
        card_http.body().addWidget(self.chk_http)

        self.spin_port = QSpinBox()
        self.spin_port.setRange(1, 65535)
        self.spin_port.setFixedHeight(32)
        self.spin_port.setValue(to_int(s.get("http_port"), 17443, 1, 65535))
        card_http.body().addLayout(self._row("监听端口", self.spin_port))

        self.chk_lan = QCheckBox("允许局域网访问")
        self.chk_lan.setChecked(bool(s.get("allow_lan", False)))
        card_http.body().addWidget(self.chk_lan)

        self.input_token = QLineEdit(s.get("http_token", ""))
        self.input_token.setPlaceholderText("留空则不鉴权（仅建议本机使用）")
        self.input_token.setEchoMode(QLineEdit.Password)
        self.input_token.setFixedHeight(32)
        card_http.body().addLayout(self._row(
            "访问 Token", self.input_token, "启用局域网访问时强烈建议设置 Token"))

        self.lbl_http_hint = _hint("")
        card_http.body().addWidget(self.lbl_http_hint)
        self.btn_copy_addr = QPushButton("📋 复制面板地址")
        self.btn_copy_addr.setFixedHeight(30)
        self.btn_copy_addr.setFont(FONT_SMALL)
        self.btn_copy_addr.setCursor(Qt.PointingHandCursor)
        self.btn_copy_addr.setStyleSheet(ghost_btn_stylesheet())
        self.btn_copy_addr.clicked.connect(self._copy_address)
        card_http.body().addWidget(self.btn_copy_addr)
        right.addWidget(card_http)

        # ---- 数据 ----
        card_data = Card("数据")
        card_data.body().addWidget(_hint(
            "扫描/测速结果自动保存于 CloudTrace_history 目录，最多各保留 5 份，"
            "并同步更新 ipv4/ipv6 的 latest 副本。"))
        right.addWidget(card_data)
        right.addStretch()

        grid_wrap.addLayout(left, 1)
        grid_wrap.addLayout(right, 1)
        lay.addLayout(grid_wrap)

        # ---- 按钮 ----
        btns = QHBoxLayout()
        btns.setSpacing(10)
        self.btn_save = QPushButton("💾 保存设置")
        self.btn_restore = QPushButton("恢复默认")
        self.btn_health = QPushButton("体检配置")
        for b in (self.btn_save, self.btn_restore, self.btn_health):
            b.setFixedHeight(36)
            b.setFont(FONT_SMALL)
            b.setCursor(Qt.PointingHandCursor)
        self.btn_save.setStyleSheet(btn_stylesheet(C_BLUE, hover_color=C_BLUE_DARK))
        self.btn_restore.setStyleSheet(ghost_btn_stylesheet())
        self.btn_health.setStyleSheet(ghost_btn_stylesheet())
        self.btn_save.clicked.connect(self.save)
        self.btn_restore.clicked.connect(self.restore_defaults)
        self.btn_health.clicked.connect(self.show_health)
        btns.addWidget(self.btn_save)
        btns.addWidget(self.btn_restore)
        btns.addWidget(self.btn_health)
        btns.addStretch()
        lay.addLayout(btns)
        lay.addStretch()

        scroll.setWidget(inner)
        outer.addWidget(scroll)
        self.setStyleSheet(FIELD_STYLE)
        self._update_http_hint()

        # 勾选/改端口都要刷新提示（原实现漏了 chk_lan，勾选后地址提示不刷新）
        self.chk_http.stateChanged.connect(lambda _s: self._update_http_hint())
        self.spin_port.valueChanged.connect(lambda _v: self._update_http_hint())
        self.chk_lan.stateChanged.connect(lambda _s: self._update_http_hint())

    # ---------------- 测速地址预设 ----------------
    def _apply_speed_url_to_ui(self, url: str):
        """把已保存的地址回填到「预设下拉 + 自定义输入框」。"""
        preset = url_to_preset_value(url)
        index = self.combo_speed_url.findData(preset)
        self.combo_speed_url.setCurrentIndex(index if index >= 0 else 0)
        is_custom = (preset == CUSTOM_SPEED_URL)
        self.input_speed_url.setText("" if not is_custom else (url or ""))
        self.input_speed_url.setVisible(is_custom)

    def _on_speed_url_preset_changed(self, _index: int):
        value = self.combo_speed_url.currentData()
        is_custom = (value == CUSTOM_SPEED_URL)
        self.input_speed_url.setVisible(is_custom)
        if is_custom and not self.input_speed_url.text().strip():
            self.input_speed_url.setText(CF_SPEED_URL)

    def _current_speed_url(self) -> str:
        value = self.combo_speed_url.currentData()
        if value == CUSTOM_SPEED_URL:
            return self.input_speed_url.text().strip() or AUTO_SPEED_URL
        return value or AUTO_SPEED_URL

    def _copy_address(self):
        from service.http_server import http_server
        addr = http_server.address
        if not addr:
            host = "127.0.0.1" if not self.chk_lan.isChecked() else "<本机IP>"
            addr = f"http://{host}:{self.spin_port.value()}/"
        QApplication.clipboard().setText(addr)
        CustomMessageBox.information(self, "已复制", f"面板地址已复制：\n{addr}")

    def _update_http_hint(self):
        if self.chk_http.isChecked():
            host = "127.0.0.1" if not self.chk_lan.isChecked() else "<本机IP>"
            extra = "" if self.chk_lan.isChecked() else "  ·  仅本机可访问"
            self.lbl_http_hint.setText(
                f"面板地址: http://{host}:{self.spin_port.value()}/{extra}")
        else:
            self.lbl_http_hint.setText("HTTP 服务已关闭（仅桌面版可用）")

    # ---------------- 操作 ----------------
    def _collect(self) -> dict:
        s = dict(self.app_settings)
        s["tray_on_close"] = self.chk_tray.isChecked()
        s["scan_mode"] = self.combo_scan_mode.currentText()
        s["sample_max"] = self.spin_sample.value()
        s["score_speed_weight"] = self.spin_w_speed.value()
        s["score_latency_weight"] = self.spin_w_latency.value()
        s["speed_url"] = self._current_speed_url()
        s["verify_nodes"] = self.chk_verify.isChecked()
        s["min_speed"] = self.spin_min_speed.value()
        s["download_interval"] = self.spin_interval.value()
        s["speed_workers"] = self.spin_speed_workers.value()
        s["speed_result_limit"] = self.spin_result_limit.value()
        s["per_region_topn"] = self.spin_region_topn.value()
        s["http_enabled"] = self.chk_http.isChecked()
        s["http_port"] = self.spin_port.value()
        s["allow_lan"] = self.chk_lan.isChecked()
        s["http_token"] = self.input_token.text().strip()
        return s

    def _merged_for_health(self) -> dict:
        merged = self._collect()
        for fn in self._providers:
            try:
                merged.update(fn())
            except Exception:
                pass
        return merged

    def save(self):
        collected = self._collect()
        # 就地更新共享对象：Web 面板与桌面端因此始终是同一份设置
        self.app_settings.clear()
        self.app_settings.update(collected)
        save_settings(self.app_settings)
        warnings = validate_settings(self._merged_for_health())
        msg = "设置已保存"
        if warnings:
            msg += "\n\n⚠ 体检提醒:\n" + "\n".join(f"· {w}" for w in warnings)
        CustomMessageBox.information(self, "保存设置", msg)
        self.saved.emit(dict(self.app_settings))

    def restore_defaults(self):
        ans = CustomMessageBox.question(self, "恢复默认", "确定恢复全部默认设置吗？")
        if ans not in ("是", "确定", "Yes"):
            return
        # 走 settings.reset_settings()：保证共享对象被就地重置并原子落盘
        reset_settings()
        self.reload_from_settings()
        CustomMessageBox.information(self, "完成", "已恢复默认设置")
        self.restored.emit(dict(self.app_settings))

    def show_health(self):
        warnings = validate_settings(self._merged_for_health())
        if warnings:
            CustomMessageBox.warning(
                self, "配置体检",
                "发现以下可优化项:\n" + "\n".join(f"· {w}" for w in warnings)
            )
        else:
            CustomMessageBox.information(self, "配置体检", "未发现配置问题 ✓")

    def reload_from_settings(self):
        """把共享设置回填到设置页表单（字段齐全，避免「恢复默认」漏项）。"""
        s = self.app_settings
        self.chk_tray.setChecked(bool(s.get("tray_on_close", False)))
        self.combo_scan_mode.setCurrentText(s.get("scan_mode", "tcping"))
        self.spin_sample.setValue(to_int(s.get("sample_max"), 5000, 100, 50000))
        self.spin_w_speed.setValue(to_float(s.get("score_speed_weight"), 3.0, 0, 50))
        self.spin_w_latency.setValue(to_float(s.get("score_latency_weight"), 3.0, 0, 50))

        self._apply_speed_url_to_ui(s.get("speed_url", "auto"))

        self.chk_verify.setChecked(bool(s.get("verify_nodes", True)))
        self.spin_min_speed.setValue(to_float(s.get("min_speed"), 0.0, 0, 200))
        self.spin_interval.setValue(to_int(s.get("download_interval"), 3, 0, 15))
        self.spin_speed_workers.setValue(to_int(s.get("speed_workers"), 1, 1, 16))
        self.spin_result_limit.setValue(to_int(s.get("speed_result_limit"), 0, 0, 1000))
        self.spin_region_topn.setValue(to_int(s.get("per_region_topn"), 0, 0, 100))

        self.chk_http.setChecked(bool(s.get("http_enabled", True)))
        self.spin_port.setValue(to_int(s.get("http_port"), 17443, 1, 65535))
        self.chk_lan.setChecked(bool(s.get("allow_lan", False)))
        self.input_token.setText(s.get("http_token", ""))
        self._update_http_hint()
