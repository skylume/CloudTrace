#!/usr/bin/env python3
# -*- coding: utf-8 -*-

from typing import List, Dict, Optional

from PySide6.QtWidgets import (
    QDialog, QLabel, QPushButton, QFrame,
    QVBoxLayout, QHBoxLayout, QTableWidget, QTableWidgetItem,
    QHeaderView, QRadioButton, QCheckBox, QDoubleSpinBox, QButtonGroup,
    QSizePolicy,
)
from PySide6.QtCore import Qt
from PySide6.QtGui import QFont

from core.constants import FONT_FAMILY
from core.export import SCAN_FIELDS, SPEED_FIELDS
from ui.styles import FONT_BTN, FONT_SMALL, C_MUTED, C_MUTED_LIGHT


def _primary_btn(text: str) -> QPushButton:
    b = QPushButton(text)
    b.setFixedHeight(34)
    b.setFont(FONT_BTN)
    b.setCursor(Qt.PointingHandCursor)
    b.setStyleSheet(
        "QPushButton { background: #2563EB; color: white; border: none; border-radius: 8px;"
        f" font-family: '{FONT_FAMILY}'; font-size: 13px; font-weight: 600; padding: 0 18px; }}"
        "QPushButton:hover { background: #1D4ED8; }"
    )
    return b


def _ghost_btn(text: str) -> QPushButton:
    b = QPushButton(text)
    b.setFixedHeight(34)
    b.setFont(FONT_BTN)
    b.setCursor(Qt.PointingHandCursor)
    b.setStyleSheet(
        "QPushButton { background: #FFFFFF; color: #334155; border: 1px solid #D3DAE3;"
        f" border-radius: 8px; font-family: '{FONT_FAMILY}'; font-size: 13px; padding: 0 18px; }}"
        "QPushButton:hover { background: #F1F5F9; }"
    )
    return b


class CustomMessageBox(QDialog):
    TYPE_INFO = "info"
    TYPE_WARNING = "warning"
    TYPE_ERROR = "error"
    TYPE_QUESTION = "question"

    ICONS = {
        TYPE_INFO: "ℹ️",
        TYPE_WARNING: "⚠️",
        TYPE_ERROR: "❌",
        TYPE_QUESTION: "❓",
    }

    @classmethod
    def show(cls, parent, title: str, text: str, msg_type: str = TYPE_INFO,
             buttons: List[str] = None, default_button: str = None) -> Optional[str]:
        dlg = cls(parent, title, text, msg_type, buttons, default_button)
        if dlg.exec() == QDialog.Accepted:
            return dlg.clicked_button
        return None

    @classmethod
    def information(cls, parent, title: str, text: str):
        cls(parent, title, text, cls.TYPE_INFO, ["确定"]).exec()

    @classmethod
    def warning(cls, parent, title: str, text: str):
        cls(parent, title, text, cls.TYPE_WARNING, ["确定"]).exec()

    @classmethod
    def critical(cls, parent, title: str, text: str):
        cls(parent, title, text, cls.TYPE_ERROR, ["确定"]).exec()

    @classmethod
    def question(cls, parent, title: str, text: str,
                 buttons: List[str] = None, default_button: str = None) -> Optional[str]:
        if buttons is None:
            buttons = ["是", "否"]
        dlg = cls(parent, title, text, cls.TYPE_QUESTION, buttons, default_button)
        if dlg.exec() == QDialog.Accepted:
            return dlg.clicked_button
        return None

    def __init__(self, parent, title: str, text: str, msg_type: str = TYPE_INFO,
                 buttons: List[str] = None, default_button: str = None):
        super().__init__(parent)
        self.clicked_button = None
        self.setWindowTitle(title)
        self.setModal(True)
        # 自适应尺寸：固定尺寸会把长文本（如「可用地区码: …」）裁掉
        self.setMinimumWidth(400)
        self.setMaximumWidth(620)

        if buttons is None:
            buttons = ["确定"]

        accent = {"warning": "#F59E0B", "error": "#DC2626", "info": "#2563EB"}.get(msg_type, "#2563EB")

        self.setStyleSheet(
            "QDialog { background: #FFFFFF; border-radius: 14px;"
            f" font-family: '{FONT_FAMILY}'; }}"
        )

        layout = QVBoxLayout(self)
        layout.setContentsMargins(0, 0, 0, 0)
        layout.setSpacing(0)

        header = QFrame()
        header.setFixedHeight(4)
        header.setStyleSheet(
            f"background: {accent}; border-top-left-radius: 14px; border-top-right-radius: 14px;")
        layout.addWidget(header)

        content = QFrame()
        content.setStyleSheet("QFrame { background: transparent; border: none; }")
        content_layout = QVBoxLayout(content)
        content_layout.setContentsMargins(24, 18, 24, 18)
        content_layout.setSpacing(16)

        header_row = QHBoxLayout()
        header_row.setSpacing(12)

        icon_label = QLabel(self.ICONS.get(msg_type, "ℹ️"))
        icon_label.setFont(QFont(FONT_FAMILY, 26))
        icon_label.setStyleSheet("background: transparent; border: none;")
        icon_label.setAlignment(Qt.AlignTop)
        header_row.addWidget(icon_label)

        text_label = QLabel(text)
        text_label.setFont(QFont(FONT_FAMILY, 10))
        text_label.setStyleSheet("color: #334155; background: transparent; border: none;")
        text_label.setWordWrap(True)
        text_label.setTextInteractionFlags(Qt.TextSelectableByMouse)
        text_label.setAlignment(Qt.AlignLeft | Qt.AlignVCenter)
        text_label.setMaximumWidth(470)
        text_label.setSizePolicy(QSizePolicy.Preferred, QSizePolicy.Minimum)
        header_row.addWidget(text_label, 1)

        content_layout.addLayout(header_row)

        btn_row = QHBoxLayout()
        btn_row.setSpacing(10)
        btn_row.addStretch()

        for btn_text in reversed(buttons):
            is_danger = btn_text in ("是", "停止", "删除")
            is_primary = (btn_text == default_button) or (btn_text == "确定" and len(buttons) == 1)

            if is_danger:
                btn = QPushButton(btn_text)
                btn.setFixedSize(88, 34)
                btn.setFont(FONT_BTN)
                btn.setCursor(Qt.PointingHandCursor)
                btn.setStyleSheet(
                    "QPushButton { background: #DC2626; color: white; border: none; border-radius: 8px;"
                    f" font-family: '{FONT_FAMILY}'; font-size: 13px; }}"
                    "QPushButton:hover { background: #B91C1C; }"
                )
            elif is_primary:
                btn = _primary_btn(btn_text)
                btn.setFixedWidth(88)
            else:
                btn = _ghost_btn(btn_text)
                btn.setFixedWidth(88)

            def make_handler(btn_text=btn_text):
                def handler():
                    self.clicked_button = btn_text
                    self.accept()
                return handler

            btn.clicked.connect(make_handler(btn_text))
            btn_row.addWidget(btn)

            if btn_text == default_button:
                btn.setDefault(True)
                btn.setFocus()

        content_layout.addLayout(btn_row)
        layout.addWidget(content)
        self.adjustSize()


class HistorySelectDialog(QDialog):
    """历史记录选择对话框（供外部扩展使用，保留兼容）。"""

    def __init__(self, ip_label: str, type_label: str, history: List[Dict], parent=None):
        super().__init__(parent)
        self.setWindowTitle(f"选择{ip_label}{type_label}历史记录")
        self.setMinimumWidth(520)
        self.setMinimumHeight(360)
        self.selected_filepath = None
        self.history = history

        self.setStyleSheet(f"QDialog {{ background: #F5F7FA; font-family: '{FONT_FAMILY}', sans-serif; }}")

        layout = QVBoxLayout(self)
        layout.setSpacing(14)
        layout.setContentsMargins(24, 20, 24, 20)

        title_frame = QFrame()
        title_frame.setStyleSheet(
            "background: qlineargradient(x1:0, y1:0, x2:1, y2:0, stop:0 #0F2B44, stop:1 #2563EB);"
            " border-radius: 10px;"
        )
        title_layout = QVBoxLayout(title_frame)
        title_layout.setContentsMargins(14, 10, 14, 10)
        title_layout.setSpacing(2)

        title_text = QLabel(f"📋 {ip_label}{type_label}历史记录")
        title_text.setFont(QFont(FONT_FAMILY, 13))
        title_text.setStyleSheet("color: white; font-weight: bold; border: none; background: transparent;")
        title_layout.addWidget(title_text)

        subtitle = QLabel(f"共 {len(history)} 份记录，请选择要加载的版本")
        subtitle.setFont(FONT_SMALL)
        subtitle.setStyleSheet("color: rgba(255,255,255,180); border: none; background: transparent;")
        title_layout.addWidget(subtitle)

        layout.addWidget(title_frame)

        self.table = QTableWidget()
        self.table.setColumnCount(3)
        self.table.setHorizontalHeaderLabels(["保存时间", "IP数量", "文件"])
        self.table.horizontalHeader().setSectionResizeMode(0, QHeaderView.ResizeToContents)
        self.table.horizontalHeader().setSectionResizeMode(1, QHeaderView.ResizeToContents)
        self.table.horizontalHeader().setSectionResizeMode(2, QHeaderView.Stretch)
        self.table.verticalHeader().setVisible(False)
        self.table.setEditTriggers(QTableWidget.NoEditTriggers)
        self.table.setSelectionBehavior(QTableWidget.SelectRows)
        self.table.setSelectionMode(QTableWidget.SingleSelection)
        self.table.setAlternatingRowColors(True)
        self.table.setRowCount(len(history))
        self.table.setStyleSheet(
            "QTableWidget { background: white; border: 1px solid #E6EAF0; border-radius: 10px;"
            " gridline-color: transparent; font-family: 'Microsoft YaHei', sans-serif;"
            " selection-background-color: #EFF6FF; selection-color: #0F172A;"
            " alternate-background-color: #FAFBFD; }"
            "QHeaderView::section { background: #F8FAFC; color: #64748B; border: none; height: 34px;"
            " padding-left: 10px; font-weight: 600; border-bottom: 1px solid #E6EAF0; }"
            "QTableWidget::item { padding: 7px; border-bottom: 1px solid #F1F5F9; }"
        )

        for i, h in enumerate(history):
            time_item = QTableWidgetItem(h['save_time'])
            time_item.setTextAlignment(Qt.AlignCenter)
            self.table.setItem(i, 0, time_item)
            count_item = QTableWidgetItem(f"{h['count']} 个")
            count_item.setTextAlignment(Qt.AlignCenter)
            self.table.setItem(i, 1, count_item)
            self.table.setItem(i, 2, QTableWidgetItem(h['filename']))

        if history:
            self.table.selectRow(0)
        # cellDoubleClicked(int,int) 没有 0 参重载，必须用 *args 接收
        self.table.cellDoubleClicked.connect(lambda *_: self._on_accept())

        layout.addWidget(self.table, 1)

        btn_layout = QHBoxLayout()
        btn_layout.addStretch()
        cancel_btn = _ghost_btn("取消")
        cancel_btn.setFixedWidth(90)
        cancel_btn.clicked.connect(self.reject)
        btn_layout.addWidget(cancel_btn)
        btn_layout.addSpacing(12)
        select_btn = _primary_btn("加载")
        select_btn.setFixedWidth(90)
        select_btn.clicked.connect(self._on_accept)
        btn_layout.addWidget(select_btn)
        layout.addLayout(btn_layout)

    def _on_accept(self):
        row = self.table.currentRow()
        if 0 <= row < len(self.history):
            self.selected_filepath = self.history[row]['filepath']
            self.accept()


class ExportDialog(QDialog):
    """导出对话框：内容选择 + 格式 + 字段勾选 + 合格筛选。"""

    def __init__(self, has_scan: bool, has_speed: bool, parent=None,
                 initial_choice: str = None):
        super().__init__(parent)
        self.setWindowTitle("导出结果")
        self.setMinimumWidth(470)
        self.choice = None            # 'scan' | 'speed' | 'both'
        self.fields: list = None      # None = 全部字段
        self.format = "csv"
        self.qualified_only = False
        self.min_speed = 0.0

        self.setStyleSheet(
            f"QDialog {{ background: #F5F7FA; font-family: '{FONT_FAMILY}', sans-serif; }}"
        )
        layout = QVBoxLayout(self)
        layout.setContentsMargins(22, 18, 22, 18)
        layout.setSpacing(12)

        title = QLabel("导出结果")
        title.setFont(QFont(FONT_FAMILY, 13))
        title.setStyleSheet("color: #0F172A; font-weight: bold;")
        layout.addWidget(title)

        box_style = "QFrame { background: white; border: 1px solid #E6EAF0; border-radius: 10px; }"
        sec_title_style = f"color: {C_MUTED}; font-size: 12px; border: none; background: transparent;"

        # ---- 内容 ----
        content_box = QFrame()
        content_box.setStyleSheet(box_style)
        content_lay = QVBoxLayout(content_box)
        content_lay.setContentsMargins(14, 10, 14, 10)
        content_lay.setSpacing(6)
        sec_content = QLabel("导出内容"); sec_content.setStyleSheet(sec_title_style)
        content_lay.addWidget(sec_content)

        self._choice_group = QButtonGroup(self)
        self._radio_scan = QRadioButton("扫描结果")
        self._radio_speed = QRadioButton("测速结果")
        self._radio_both = QRadioButton("扫描 + 测速（分别保存）")
        radios = []
        if has_scan:
            radios.append(self._radio_scan)
        if has_speed:
            radios.append(self._radio_speed)
        if has_scan and has_speed:
            radios.append(self._radio_both)
        for r in radios:
            r.setFont(QFont(FONT_FAMILY, 10))
            content_lay.addWidget(r)
            self._choice_group.addButton(r)
        if initial_choice == "scan" and has_scan:
            self._radio_scan.setChecked(True)
        elif initial_choice == "speed" and has_speed:
            self._radio_speed.setChecked(True)
        elif radios:
            radios[0].setChecked(True)
        if has_scan and has_speed and initial_choice is None:
            self._radio_both.setChecked(True)
        layout.addWidget(content_box)

        # ---- 格式 ----
        fmt_box = QFrame()
        fmt_box.setStyleSheet(box_style)
        fmt_lay = QVBoxLayout(fmt_box)
        fmt_lay.setContentsMargins(14, 10, 14, 10)
        fmt_lay.setSpacing(6)
        sec_fmt = QLabel("导出格式"); sec_fmt.setStyleSheet(sec_title_style)
        fmt_lay.addWidget(sec_fmt)
        fmt_row = QHBoxLayout()
        fmt_row.setSpacing(16)
        self._fmt_group = QButtonGroup(self)
        for value, label in (("csv", "CSV（Excel 可打开）"),
                             ("json", "JSON（程序处理）"),
                             ("txt", "TXT（ip:port 列表）")):
            rb = QRadioButton(label)
            rb.setFont(QFont(FONT_FAMILY, 10))
            rb.setProperty("fmt", value)
            if value == "csv":
                rb.setChecked(True)
            self._fmt_group.addButton(rb)
            fmt_row.addWidget(rb)
        fmt_row.addStretch()
        fmt_lay.addLayout(fmt_row)
        fmt_hint = QLabel("TXT 每行输出 ip:port，可直接粘贴到代理客户端（忽略字段勾选）")
        fmt_hint.setStyleSheet(f"color: {C_MUTED_LIGHT}; font-size: 11px; border: none; background: transparent;")
        fmt_lay.addWidget(fmt_hint)
        layout.addWidget(fmt_box)

        # ---- 字段 ----
        fields_box = QFrame()
        fields_box.setStyleSheet(box_style)
        fields_lay = QVBoxLayout(fields_box)
        fields_lay.setContentsMargins(14, 10, 14, 10)
        fields_lay.setSpacing(6)

        fields_head = QHBoxLayout()
        sec_fields = QLabel("导出字段"); sec_fields.setStyleSheet(sec_title_style)
        fields_head.addWidget(sec_fields)
        fields_head.addStretch()
        self._btn_all = QPushButton("全不选")
        self._btn_all.setFixedHeight(24)
        self._btn_all.setCursor(Qt.PointingHandCursor)
        self._btn_all.setStyleSheet(
            "QPushButton { background: white; border: 1px solid #D3DAE3; border-radius: 6px;"
            " color: #64748B; font-size: 11px; padding: 2px 10px; }"
            "QPushButton:hover { background: #F1F5F9; }"
        )
        fields_head.addWidget(self._btn_all)
        fields_lay.addLayout(fields_head)

        merged = dict(SCAN_FIELDS)
        merged.update(SPEED_FIELDS)
        self._field_checks: dict = {}
        row_lay = None
        for i, (key, label) in enumerate(merged.items()):
            if i % 3 == 0:
                row_lay = QHBoxLayout()
                row_lay.setSpacing(10)
                fields_lay.addLayout(row_lay)
            chk = QCheckBox(label)
            chk.setFont(QFont(FONT_FAMILY, 9))
            if key == "ip":
                chk.setChecked(True)
                chk.setEnabled(False)  # IP 列必选
            else:
                chk.setChecked(True)
            row_lay.addWidget(chk)
            self._field_checks[key] = chk
        self._btn_all.clicked.connect(self._toggle_fields)
        layout.addWidget(fields_box)

        # ---- 合格筛选 ----
        q_box = QFrame()
        q_box.setStyleSheet(box_style)
        q_lay = QHBoxLayout(q_box)
        q_lay.setContentsMargins(14, 10, 14, 10)
        q_lay.setSpacing(8)
        self.chk_qualified = QCheckBox("测速仅导出合格结果，低于")
        self.spin_min = QDoubleSpinBox()
        self.spin_min.setRange(0, 200)
        self.spin_min.setDecimals(1)
        self.spin_min.setSuffix(" MB/s")
        self.spin_min.setFixedHeight(28)
        self.spin_min.setEnabled(False)
        self.chk_qualified.stateChanged.connect(self.spin_min.setEnabled)
        q_lay.addWidget(self.chk_qualified)
        q_lay.addWidget(self.spin_min)
        q_lay.addStretch()
        layout.addWidget(q_box)

        # ---- 按钮 ----
        btn_row = QHBoxLayout()
        btn_row.addStretch()
        cancel = _ghost_btn("取消")
        cancel.setFixedWidth(90)
        cancel.clicked.connect(self.reject)
        ok = _primary_btn("导出")
        ok.setFixedWidth(90)
        ok.setDefault(True)
        ok.clicked.connect(self._accept)
        btn_row.addWidget(cancel)
        btn_row.addWidget(ok)
        layout.addLayout(btn_row)

    def _toggle_fields(self):
        will_uncheck = self._btn_all.text() == "全不选"
        for chk in self._field_checks.values():
            if chk.isEnabled():
                chk.setChecked(not will_uncheck)
        self._btn_all.setText("全选" if will_uncheck else "全不选")

    def _accept(self):
        if self._radio_scan.isChecked():
            self.choice = "scan"
        elif self._radio_speed.isChecked():
            self.choice = "speed"
        else:
            self.choice = "both"
        selected = [k for k, c in self._field_checks.items() if c.isChecked()]
        all_keys = list(self._field_checks.keys())
        self.fields = selected if len(selected) < len(all_keys) else None
        checked_fmt = self._fmt_group.checkedButton()
        self.format = checked_fmt.property("fmt") if checked_fmt else "csv"
        self.qualified_only = self.chk_qualified.isChecked()
        self.min_speed = self.spin_min.value()
        self.accept()
