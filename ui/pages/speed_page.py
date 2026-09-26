#!/usr/bin/env python3
# -*- coding: utf-8 -*-

from typing import Dict, List

from PySide6.QtWidgets import (
    QWidget, QVBoxLayout, QHBoxLayout, QLabel, QTableWidget, QTableWidgetItem,
    QHeaderView, QCheckBox, QSpinBox, QDoubleSpinBox, QComboBox, QLineEdit,
    QPushButton, QApplication, QAbstractItemView,
)
from PySide6.QtCore import Qt, Signal
from PySide6.QtGui import QFont, QColor

from core.constants import FONT_FAMILY, AIRPORT_CODES
from core.speed_url import (
    SPEED_URL_PRESETS, CUSTOM_SPEED_URL, CF_SPEED_URL, url_to_preset_value,
)
from core.utils import to_float
from ui.widgets import Card, LogTerminal, StatCard, EmptyState
from ui.styles import (
    FONT_SMALL, TABLE_LIGHT_STYLE, C_BLUE, C_BLUE_DARK, C_ORANGE, C_ORANGE_DARK,
    C_GREEN, C_GREEN_DARK, C_RED, C_MUTED, C_MUTED_LIGHT,
    btn_stylesheet, ghost_btn_stylesheet,
)
from ui.dialogs import CustomMessageBox


class SpeedPage(QWidget):
    """测速页：统计卡 + 控制工具栏 + 排名表格 + 日志。"""

    start_region_requested = Signal()
    start_full_requested = Signal()
    export_requested = Signal()

    def __init__(self, app_settings: dict, parent=None):
        super().__init__(parent)
        self.app_settings = app_settings
        self.all_results: List[Dict] = []
        self._build()

    def _build(self):
        outer = QVBoxLayout(self)
        outer.setContentsMargins(20, 16, 20, 18)
        outer.setSpacing(14)

        # ---- 统计卡 ----
        stats_row = QHBoxLayout()
        stats_row.setSpacing(12)
        self.stat_best = StatCard("最快下载", "—", "MB/s")
        self.stat_avg = StatCard("平均下载", "—", "MB/s")
        self.stat_pass = StatCard("合格节点", "0")
        self.stat_count = StatCard("结果总数", "0")
        for c in (self.stat_best, self.stat_avg, self.stat_pass, self.stat_count):
            stats_row.addWidget(c)
        outer.addLayout(stats_row)

        # ---- 工具栏 ----
        card = Card("测速控制", "基于扫描结果下载实测；测速间隔越小越快，但更易触发限速")
        bar = QHBoxLayout()
        bar.setSpacing(10)

        def labeled(text, widget):
            box = QHBoxLayout()
            box.setSpacing(5)
            lbl = QLabel(text)
            lbl.setProperty("class", "fieldLabel")
            lbl.setFont(FONT_SMALL)
            box.addWidget(lbl)
            box.addWidget(widget)
            return box

        self.input_region = QLineEdit()
        self.input_region.setFixedWidth(72)
        self.input_region.setFixedHeight(30)
        self.input_region.setAlignment(Qt.AlignCenter)
        self.input_region.setPlaceholderText("HKG")
        self.input_region.textChanged.connect(self._auto_uppercase)
        bar.addLayout(labeled("地区码", self.input_region))

        self.spin_count = QSpinBox()
        self.spin_count.setRange(1, 50)
        self.spin_count.setValue(10)
        self.spin_count.setFixedHeight(30)
        self.spin_count.setFixedWidth(68)
        bar.addLayout(labeled("数量", self.spin_count))

        self.combo_speed_url = QComboBox()
        for label, value in SPEED_URL_PRESETS:
            self.combo_speed_url.addItem(label, value)
        self.combo_speed_url.setFixedHeight(30)
        self.combo_speed_url.setMinimumWidth(200)
        bar.addLayout(labeled("测速地址", self.combo_speed_url))

        self.input_speed_url = QLineEdit()
        self.input_speed_url.setFixedHeight(30)
        self.input_speed_url.setMinimumWidth(240)
        self.input_speed_url.setPlaceholderText("speed.cloudflare.com/__down?bytes=99999999")
        bar.addWidget(self.input_speed_url)
        self.combo_speed_url.currentIndexChanged.connect(self._on_speed_url_preset_changed)
        self._apply_speed_url_to_ui(self.app_settings.get("speed_url", "auto"))

        self.chk_min_speed = QCheckBox("隐藏低于")
        self.spin_min_speed = QDoubleSpinBox()
        self.spin_min_speed.setRange(0, 200)
        self.spin_min_speed.setDecimals(1)
        self.spin_min_speed.setSuffix(" MB/s")
        self.spin_min_speed.setFixedHeight(30)
        self.spin_min_speed.setFixedWidth(116)
        bar.addWidget(self.chk_min_speed)
        bar.addWidget(self.spin_min_speed)

        bar.addStretch()

        self.btn_region = QPushButton("🌸 地区测速")
        self.btn_full = QPushButton("⬆ 完全测速")
        self.btn_export = QPushButton("⬇ 导出结果")
        for b in (self.btn_region, self.btn_full, self.btn_export):
            b.setFixedHeight(32)
            b.setFont(FONT_SMALL)
            b.setCursor(Qt.PointingHandCursor)
        self.btn_region.setStyleSheet(btn_stylesheet(C_ORANGE, hover_color=C_ORANGE_DARK))
        self.btn_full.setStyleSheet(btn_stylesheet(C_BLUE, hover_color=C_BLUE_DARK))
        self.btn_export.setStyleSheet(btn_stylesheet(C_GREEN, hover_color=C_GREEN_DARK))
        self.btn_region.clicked.connect(self._on_region)
        self.btn_full.clicked.connect(self.start_full_requested.emit)
        self.btn_export.clicked.connect(self.export_requested.emit)
        bar.addWidget(self.btn_region)
        bar.addWidget(self.btn_full)
        bar.addWidget(self.btn_export)

        card.body().addLayout(bar)
        outer.addWidget(card)

        self.chk_min_speed.stateChanged.connect(self._refresh_table)
        self.spin_min_speed.valueChanged.connect(self._refresh_table)

        # ---- 表格 ----
        table_card = Card("测速结果", "双击任意单元格可复制内容")
        self.table = QTableWidget()
        self.table.setColumnCount(8)
        self.table.setHorizontalHeaderLabels(
            ["排名", "IP 地址", "地区", "延迟", "下载速度", "综合评分", "端口", "测速类型"]
        )
        self.table.setAlternatingRowColors(True)
        self.table.verticalHeader().setVisible(False)
        self.table.verticalHeader().setDefaultSectionSize(36)
        self.table.setEditTriggers(QAbstractItemView.NoEditTriggers)
        self.table.setSelectionBehavior(QAbstractItemView.SelectRows)
        self.table.setShowGrid(False)
        self.table.setWordWrap(False)
        self.table.setStyleSheet(TABLE_LIGHT_STYLE)
        for i in range(7):
            self.table.horizontalHeader().setSectionResizeMode(i, QHeaderView.ResizeToContents)
        self.table.horizontalHeader().setSectionResizeMode(7, QHeaderView.Stretch)
        self.table.doubleClicked.connect(self._copy_cell)
        self.empty = EmptyState("暂无测速结果，请先在「结果」页选择范围并开始测速", "🚀")
        table_card.body().addWidget(self.empty)
        table_card.body().addWidget(self.table, 1)
        outer.addWidget(table_card, 1)

        # ---- 日志 ----
        self.terminal = LogTerminal("测速日志")
        outer.addWidget(self.terminal)

        self.setStyleSheet(
            "QLineEdit, QSpinBox, QDoubleSpinBox, QComboBox { background: white;"
            " color: #0F172A; border: 1px solid #D3DAE3; border-radius: 8px;"
            f" padding: 4px 8px; font-family: '{FONT_FAMILY}'; }}"
            "QLineEdit:focus, QSpinBox:focus, QDoubleSpinBox:focus, QComboBox:focus"
            " { border: 1px solid #2563EB; }"
            "QComboBox::drop-down { border: none; width: 22px; }"
            "QLabel { color: #334155; font-size: 12px; }"
            "QCheckBox { color: #334155; font-size: 12px; spacing: 6px; }"
            "QCheckBox::indicator { width: 15px; height: 15px; border-radius: 4px;"
            " border: 1px solid #D3DAE3; background: white; }"
            "QCheckBox::indicator:checked { background: #2563EB; border: 1px solid #2563EB; }"
        )

    # ---------------- 交互 ----------------
    def _apply_speed_url_to_ui(self, url: str):
        preset = url_to_preset_value(url)
        index = self.combo_speed_url.findData(preset)
        self.combo_speed_url.setCurrentIndex(index if index >= 0 else 0)
        is_custom = (preset == CUSTOM_SPEED_URL)
        self.input_speed_url.setText(url if is_custom else "")
        self.input_speed_url.setVisible(is_custom)

    def _on_speed_url_preset_changed(self, _index: int):
        is_custom = (self.combo_speed_url.currentData() == CUSTOM_SPEED_URL)
        self.input_speed_url.setVisible(is_custom)
        if is_custom and not self.input_speed_url.text().strip():
            self.input_speed_url.setText(CF_SPEED_URL)

    def _auto_uppercase(self, text):
        if text != text.upper():
            self.input_region.blockSignals(True)
            pos = self.input_region.cursorPosition()
            self.input_region.setText(text.upper())
            self.input_region.setCursorPosition(pos)
            self.input_region.blockSignals(False)

    def _on_region(self):
        region = self.input_region.text().strip().upper()
        if not region:
            CustomMessageBox.warning(self, "提示", "请输入地区码（如 HKG, NRT, SIN）")
            return
        self.start_region_requested.emit()

    # ---------------- 参数 ----------------
    def collect(self) -> dict:
        value = self.combo_speed_url.currentData()
        if value == CUSTOM_SPEED_URL:
            url = self.input_speed_url.text().strip() or "auto"
        else:
            url = value or "auto"
        return {
            "region": self.input_region.text().strip().upper(),
            "count": self.spin_count.value(),
            "speed_url": url,
            "min_speed": self.spin_min_speed.value() if self.chk_min_speed.isChecked() else 0,
        }

    def health_snapshot(self) -> dict:
        return {"min_speed": self.collect()["min_speed"], "speed_url": self.collect()["speed_url"]}

    def reload_from_settings(self, full: bool = False):
        """把设置回填到测速页表单（speed_url 始终同步；full 时含 min_speed）。"""
        s = self.app_settings
        self._apply_speed_url_to_ui(s.get("speed_url", "auto"))
        if full:
            value = to_float(s.get("min_speed"), 0.0, 0.0, 200.0)
            self.spin_min_speed.setValue(value)
            if value > 0:
                self.chk_min_speed.setChecked(True)

    # ---------------- 数据 ----------------
    def set_results(self, results: List[Dict]):
        self.all_results = list(results or [])
        self._refresh_table()

    def _visible_results(self) -> List[Dict]:
        data = self.all_results
        if self.chk_min_speed.isChecked():
            limit = self.spin_min_speed.value()
            data = [r for r in data if (r.get("download_speed") or 0) >= limit]
        return data

    def _refresh_table(self):
        data = self._visible_results()
        self.table.setRowCount(len(data))
        rank_colors = {0: QColor("#D4A017"), 1: QColor("#94A3B8"), 2: QColor("#B87333")}

        for i, r in enumerate(data):
            rank_item = QTableWidgetItem(str(i + 1))
            rank_item.setTextAlignment(Qt.AlignCenter)
            if i in rank_colors:
                rank_item.setForeground(rank_colors[i])
                font = rank_item.font()
                font.setBold(True)
                rank_item.setFont(font)
            self.table.setItem(i, 0, rank_item)

            ip_item = QTableWidgetItem(r.get("ip", ""))
            ip_item.setFont(QFont("Consolas", 10))
            self.table.setItem(i, 1, ip_item)

            code = r.get("iata_code", "") or ""
            name = r.get("chinese_name", AIRPORT_CODES.get(code, "未知"))
            verify_mark = ""
            if r.get("verified") is True:
                verify_mark = "✓ "
            elif r.get("verified") is False:
                verify_mark = "✗ "
            region_item = QTableWidgetItem(f"{verify_mark}{name}({code})")
            region_item.setTextAlignment(Qt.AlignCenter)
            if r.get("verified") is False:
                region_item.setForeground(QColor(C_RED))
            self.table.setItem(i, 2, region_item)

            latency = r.get("latency", 0)
            lat_item = QTableWidgetItem(f"{latency:.1f} ms")
            lat_item.setTextAlignment(Qt.AlignCenter)
            if latency < 100:
                lat_item.setForeground(QColor(C_GREEN))
            elif latency < 200:
                lat_item.setForeground(QColor(C_ORANGE))
            else:
                lat_item.setForeground(QColor(C_RED))
            self.table.setItem(i, 3, lat_item)

            speed = r.get("download_speed", 0)
            speed_item = QTableWidgetItem(f"{speed:.2f} MB/s")
            speed_item.setTextAlignment(Qt.AlignCenter)
            if speed >= 10:
                speed_item.setForeground(QColor(C_GREEN))
            elif speed >= 5:
                speed_item.setForeground(QColor(C_ORANGE))
            else:
                speed_item.setForeground(QColor(C_RED))
            self.table.setItem(i, 4, speed_item)

            score = r.get("score", 0)
            score_item = QTableWidgetItem(f"{score:.1f}")
            score_item.setTextAlignment(Qt.AlignCenter)
            if i in rank_colors:
                font = score_item.font()
                font.setBold(True)
                score_item.setFont(font)
            self.table.setItem(i, 5, score_item)

            port_item = QTableWidgetItem(str(r.get("port", "")))
            port_item.setTextAlignment(Qt.AlignCenter)
            self.table.setItem(i, 6, port_item)

            type_item = QTableWidgetItem(r.get("test_type", ""))
            type_item.setTextAlignment(Qt.AlignCenter)
            type_item.setForeground(QColor(C_MUTED))
            self.table.setItem(i, 7, type_item)

        self._update_stats(data)
        self._sync_empty_state()

    def _update_stats(self, visible: List[Dict]):
        self.stat_count.set_value(len(self.all_results))
        if not self.all_results:
            self.stat_best.set_value("—")
            self.stat_avg.set_value("—")
            self.stat_pass.set_value(0)
            return
        speeds = [(r.get("download_speed") or 0) for r in self.all_results]
        best = max(speeds)
        avg = sum(speeds) / len(speeds)
        self.stat_best.set_value(f"{best:.2f}", color=C_GREEN)
        self.stat_avg.set_value(
            f"{avg:.2f}", color=C_GREEN if avg >= 10 else (C_ORANGE if avg >= 5 else C_RED))
        self.stat_pass.set_value(len(visible))

    def _sync_empty_state(self):
        has_data = bool(self.all_results)
        self.table.setVisible(has_data)
        self.empty.setVisible(not has_data)

    def _copy_cell(self, index):
        item = self.table.item(index.row(), index.column())
        if item and item.text():
            QApplication.clipboard().setText(item.text())

    def set_busy(self, busy: bool):
        for b in (self.btn_region, self.btn_full, self.btn_export):
            b.setEnabled(not busy)

    def log(self, msg: str):
        self.terminal.append_line(msg)
