#!/usr/bin/env python3
# -*- coding: utf-8 -*-

from typing import List

from PySide6.QtWidgets import (
    QWidget, QVBoxLayout, QHBoxLayout, QLabel, QPushButton, QScrollArea,
    QFrame,
)
from PySide6.QtCore import Qt, Signal
from PySide6.QtGui import QFont

from core.constants import FONT_FAMILY
from settings import get_history_list
from ui.widgets import Card, Segmented, clear_layout
from ui.styles import (
    FONT_SMALL, C_BLUE, C_BLUE_DARK, C_RED, C_MUTED, C_MUTED_LIGHT,
    C_PURPLE, btn_stylesheet, ghost_btn_stylesheet, danger_btn_stylesheet,
)
from ui.dialogs import CustomMessageBox


class HistoryPage(QWidget):
    """历史页：扫描/测速记录的加载、导出、删除。"""

    load_requested = Signal(str, str)     # filepath, type('scan'|'speed')
    export_requested = Signal(str, str)   # filepath, type
    delete_requested = Signal(str)        # filepath

    def __init__(self, parent=None):
        super().__init__(parent)
        self.ip_version = 4
        self._build()

    def _build(self):
        outer = QVBoxLayout(self)
        outer.setContentsMargins(20, 16, 20, 18)
        outer.setSpacing(14)

        head = QHBoxLayout()
        lbl = QLabel("IP 版本")
        lbl.setProperty("class", "fieldLabel")
        lbl.setFont(FONT_SMALL)
        self.seg_version = Segmented(["IPv4", "IPv6"], 0)
        self.seg_version.indexChanged.connect(self._on_version_changed)
        head.addWidget(lbl)
        head.addWidget(self.seg_version)
        head.addStretch()
        self.btn_refresh = QPushButton("🔄 刷新")
        self.btn_refresh.setFixedHeight(30)
        self.btn_refresh.setFont(FONT_SMALL)
        self.btn_refresh.setCursor(Qt.PointingHandCursor)
        self.btn_refresh.setStyleSheet(ghost_btn_stylesheet())
        self.btn_refresh.clicked.connect(self.refresh)
        head.addWidget(self.btn_refresh)
        outer.addLayout(head)

        scroll = QScrollArea()
        scroll.setWidgetResizable(True)
        scroll.setFrameShape(QScrollArea.NoFrame)
        scroll.setStyleSheet("QScrollArea { background: transparent; border: none; }")
        inner = QWidget()
        inner.setStyleSheet("background: transparent;")
        self.inner_lay = QVBoxLayout(inner)
        self.inner_lay.setContentsMargins(0, 0, 8, 0)
        self.inner_lay.setSpacing(14)
        scroll.setWidget(inner)
        outer.addWidget(scroll, 1)

    def _on_version_changed(self, idx: int):
        self.ip_version = 4 if idx == 0 else 6
        self.refresh()

    def refresh(self):
        clear_layout(self.inner_lay)

        for type_label, type_key, icon in (("扫描记录", "scan", "📡"), ("测速记录", "speed", "🚀")):
            history = get_history_list(self.ip_version, type_key)
            card = Card(f"IPv{self.ip_version} {type_label}", f"共 {len(history)} 份 · 最多保留 5 份")
            if not history:
                empty = QLabel(
                    f"暂无{type_label}（执行一次{'扫描' if type_key == 'scan' else '测速'}后自动生成）")
                empty.setStyleSheet(
                    f"color: {C_MUTED_LIGHT}; font-size: 12px; border: none; background: transparent;")
                empty.setAlignment(Qt.AlignCenter)
                empty.setContentsMargins(0, 14, 0, 14)
                card.body().addWidget(empty)
            else:
                for h in history:
                    card.body().addWidget(self._make_row(h, type_key, icon))
            self.inner_lay.addWidget(card)
        self.inner_lay.addStretch()

    def _make_row(self, h: dict, type_key: str, icon: str) -> QFrame:
        row = QFrame()
        row.setStyleSheet(
            "QFrame { background: #F8FAFC; border: 1px solid #E6EAF0; border-radius: 10px; }")
        lay = QHBoxLayout(row)
        lay.setContentsMargins(14, 10, 14, 10)
        lay.setSpacing(12)

        icon_lbl = QLabel(icon)
        icon_lbl.setFont(QFont(FONT_FAMILY, 14))
        icon_lbl.setStyleSheet("border: none; background: transparent;")
        lay.addWidget(icon_lbl)

        text_box = QVBoxLayout()
        text_box.setSpacing(2)
        title = QLabel(h.get("save_time", "未知时间"))
        title.setFont(QFont(FONT_FAMILY, 10))
        title.setStyleSheet("color: #0F172A; border: none; background: transparent; font-weight: 600;")
        meta = QLabel(f"{h.get('count', 0)} 条 · {h.get('filename', '')}")
        meta.setFont(QFont(FONT_FAMILY, 8))
        meta.setStyleSheet(f"color: {C_MUTED}; border: none; background: transparent;")
        text_box.addWidget(title)
        text_box.addWidget(meta)
        lay.addLayout(text_box, 1)

        def op(text, style):
            b = QPushButton(text)
            b.setFixedSize(62, 28)
            b.setFont(QFont(FONT_FAMILY, 9))
            b.setCursor(Qt.PointingHandCursor)
            b.setStyleSheet(style)
            return b

        btn_load = op("加载", btn_stylesheet(C_BLUE, hover_color=C_BLUE_DARK))
        btn_export = op("导出", btn_stylesheet(C_PURPLE))
        btn_del = op("删除", danger_btn_stylesheet())
        filepath = h["filepath"]
        # 首参必须带默认值：PySide6 的 clicked 只有 0/1 参重载，
        # 2 个参数的 lambda 会匹配失败并静默不执行（详见 ui/widgets.py 注释）。
        btn_load.clicked.connect(lambda _c=False, fp=filepath: self.load_requested.emit(fp, type_key))
        btn_export.clicked.connect(lambda _c=False, fp=filepath: self.export_requested.emit(fp, type_key))
        btn_del.clicked.connect(lambda _c=False, fp=filepath: self._confirm_delete(fp))
        lay.addWidget(btn_load)
        lay.addWidget(btn_export)
        lay.addWidget(btn_del)
        return row

    def _confirm_delete(self, filepath: str):
        ans = CustomMessageBox.question(self, "确认删除", "确定要删除这条历史记录吗？\n该操作不可恢复。",
                                        ["删除", "取消"], "取消")
        if ans == "删除":
            self.delete_requested.emit(filepath)

    def showEvent(self, event):
        super().showEvent(event)
        self.refresh()
