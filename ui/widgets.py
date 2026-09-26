#!/usr/bin/env python3
# -*- coding: utf-8 -*-

from datetime import datetime
from typing import List, Tuple

from PySide6.QtWidgets import (
    QLayout, QFrame, QLabel, QPushButton, QPlainTextEdit,
    QHBoxLayout, QVBoxLayout, QWidget, QButtonGroup, QSizePolicy,
    QGraphicsDropShadowEffect,
)
from PySide6.QtCore import Qt, QRect, QSize, QPoint, Signal
from PySide6.QtGui import QFont, QColor

from core.constants import FONT_FAMILY
from ui.styles import (
    SIDEBAR_STYLE, CARD_STYLE, TERMINAL_STYLE, CHIP_STYLE, SEG_STYLE,
    STAT_CARD_STYLE, EMPTY_STATE_STYLE, BADGE_STYLE,
    FONT_SMALL, FONT_BTN, FONT_METRIC,
    C_BLUE, C_MUTED, C_GREEN, C_ORANGE, C_RED, C_MUTED_LIGHT, C_TEXT,
)


def apply_shadow(widget: QWidget, blur: int = 18, alpha: int = 26, dy: int = 2):
    """给控件加轻微投影（QSS 不支持 box-shadow，只能用图形效果）。"""
    effect = QGraphicsDropShadowEffect(widget)
    effect.setBlurRadius(blur)
    effect.setOffset(0, dy)
    effect.setColor(QColor(15, 23, 42, alpha))
    widget.setGraphicsEffect(effect)
    return effect


class FlowLayout(QLayout):
    """简单流式布局：控件从左到右排列，放不下自动换行。"""

    def __init__(self, parent=None, h_spacing=8, v_spacing=8):
        super().__init__(parent)
        self._items: List = []
        self._h_space = h_spacing
        self._v_space = v_spacing
        self.setContentsMargins(0, 0, 0, 0)

    def addItem(self, item):
        self._items.append(item)

    def count(self):
        return len(self._items)

    def itemAt(self, index):
        return self._items[index] if 0 <= index < len(self._items) else None

    def takeAt(self, index):
        return self._items.pop(index) if 0 <= index < len(self._items) else None

    def expandingDirections(self):
        return Qt.Orientations(0)

    def hasHeightForWidth(self):
        return True

    def heightForWidth(self, width):
        return self._do_layout(QRect(0, 0, width, 0), test_only=True)

    def setGeometry(self, rect):
        super().setGeometry(rect)
        self._do_layout(rect, test_only=False)

    def sizeHint(self):
        return self.minimumSize()

    def minimumSize(self):
        size = QSize()
        for item in self._items:
            size = size.expandedTo(item.minimumSize())
        m = self.contentsMargins()
        size += QSize(m.left() + m.right(), m.top() + m.bottom())
        return size

    def _do_layout(self, rect: QRect, test_only: bool) -> int:
        m = self.contentsMargins()
        x = rect.x() + m.left()
        y = rect.y() + m.top()
        line_height = 0
        right = rect.right() - m.right()

        for item in self._items:
            space_x = self._h_space
            space_y = self._v_space
            next_x = x + item.sizeHint().width() + space_x
            if next_x - space_x > right and line_height > 0:
                x = rect.x() + m.left()
                y = y + line_height + space_y
                next_x = x + item.sizeHint().width() + space_x
                line_height = 0
            if not test_only:
                item.setGeometry(QRect(QPoint(x, y), item.sizeHint()))
            x = next_x
            line_height = max(line_height, item.sizeHint().height())
        return y + line_height - rect.y() + m.bottom()


def clear_layout(layout: QLayout):
    """安全清空布局：先脱离父级再延迟销毁。

    只 `takeAt` + `deleteLater` 会让控件在事件循环处理销毁前仍挂在原位置，
    连续刷新时叠出「重影」。必须先 setParent(None) 把它从父级摘掉。
    """
    while layout.count():
        item = layout.takeAt(0)
        w = item.widget()
        if w is not None:
            w.setParent(None)
            w.deleteLater()
            continue
        child = item.layout()
        if child is not None:
            clear_layout(child)
            child.deleteLater()


class Segmented(QWidget):
    """分段选择器（容器边框 + 内部按钮）。"""

    indexChanged = Signal(int)

    def __init__(self, items: List[str], current: int = 0, parent=None):
        super().__init__(parent)
        self._buttons: List[QPushButton] = []
        self._group = QButtonGroup(self)
        self._group.setExclusive(True)

        frame = QFrame()
        frame.setObjectName("segGroup")
        lay = QHBoxLayout(frame)
        lay.setContentsMargins(3, 3, 3, 3)
        lay.setSpacing(2)
        for i, text in enumerate(items):
            btn = QPushButton(text)
            btn.setCheckable(True)
            btn.setCursor(Qt.PointingHandCursor)
            btn.setFont(FONT_SMALL)
            btn.setProperty("class", "seg")
            lay.addWidget(btn)
            self._group.addButton(btn, i)
            self._buttons.append(btn)
            # 注意：PySide6 的 clicked 只有 clicked()/clicked(bool) 两个重载。
            # 带 2 个参数的 lambda 匹配不到重载，会退化为无参调用并抛
            # TypeError(<lambda>() missing 1 required positional argument: '_c')，
            # 且回调完全不执行。因此首参必须带默认值。
            btn.clicked.connect(lambda _c=False, idx=i: self.indexChanged.emit(idx))

        outer = QHBoxLayout(self)
        outer.setContentsMargins(0, 0, 0, 0)
        outer.addWidget(frame)
        self.setStyleSheet(SEG_STYLE)
        self.set_index(current)

    def set_index(self, i: int):
        if 0 <= i < len(self._buttons):
            self._buttons[i].setChecked(True)

    def index(self) -> int:
        return self._group.checkedId()

    def current_text(self) -> str:
        i = self.index()
        return self._buttons[i].text() if 0 <= i < len(self._buttons) else ""


class Card(QFrame):
    """白色圆角卡片容器，支持标题 / 副标题 / 右侧操作区。"""

    def __init__(self, title: str = "", subtitle: str = "", parent=None):
        super().__init__(parent)
        self.setObjectName("card")
        self.setStyleSheet(CARD_STYLE)
        apply_shadow(self)

        self._layout = QVBoxLayout(self)
        self._layout.setContentsMargins(18, 16, 18, 16)
        self._layout.setSpacing(12)

        self._header = QHBoxLayout()
        self._header.setSpacing(10)
        self._title_box = QVBoxLayout()
        self._title_box.setSpacing(2)
        self._title: QLabel = None
        self._subtitle: QLabel = None
        if title:
            self._title = QLabel(title)
            self._title.setObjectName("cardTitle")
            self._title_box.addWidget(self._title)
        if subtitle:
            self._subtitle = QLabel(subtitle)
            self._subtitle.setObjectName("cardSubtitle")
            self._subtitle.setWordWrap(True)
            self._title_box.addWidget(self._subtitle)
        if title or subtitle:
            self._header.addLayout(self._title_box)
            self._header.addStretch()
            self._layout.addLayout(self._header)

    def body(self) -> QVBoxLayout:
        return self._layout

    def add_action(self, widget: QWidget):
        """把控件放到标题行右侧（例如「全选 / 清空」按钮）。"""
        self._header.addWidget(widget)

    def set_subtitle(self, text: str):
        if self._subtitle is None:
            self._subtitle = QLabel(text)
            self._subtitle.setObjectName("cardSubtitle")
            self._title_box.addWidget(self._subtitle)
        self._subtitle.setText(text)


class StatCard(QFrame):
    """指标小卡：大号数值 + 单位 + 说明文字。"""

    def __init__(self, label: str, value: str = "—", unit: str = "", parent=None):
        super().__init__(parent)
        self.setObjectName("statCard")
        self.setStyleSheet(STAT_CARD_STYLE)
        self.setSizePolicy(QSizePolicy.Expanding, QSizePolicy.Fixed)

        lay = QVBoxLayout(self)
        lay.setContentsMargins(14, 11, 14, 11)
        lay.setSpacing(2)

        self._label = QLabel(label)
        self._label.setObjectName("statLabel")
        lay.addWidget(self._label)

        row = QHBoxLayout()
        row.setSpacing(4)
        row.setContentsMargins(0, 0, 0, 0)
        self._value = QLabel(value)
        self._value.setObjectName("statValue")
        self._value.setFont(FONT_METRIC)
        row.addWidget(self._value)
        self._unit = QLabel(unit)
        self._unit.setObjectName("statUnit")
        row.addWidget(self._unit, 0, Qt.AlignBottom)
        row.addStretch()
        lay.addLayout(row)

    def set_value(self, value, unit: str = None, color: str = None):
        self._value.setText(str(value))
        if unit is not None:
            self._unit.setText(unit)
        self._value.setStyleSheet(
            f"color: {color};" if color else f"color: {C_TEXT};"
        )


class EmptyState(QWidget):
    """统一空状态：图标 + 文案（+ 可选操作按钮）。"""

    def __init__(self, text: str, icon: str = "📭", parent=None):
        super().__init__(parent)
        lay = QVBoxLayout(self)
        lay.setContentsMargins(0, 22, 0, 22)
        lay.setSpacing(6)
        ico = QLabel(icon)
        ico.setAlignment(Qt.AlignCenter)
        ico.setFont(QFont(FONT_FAMILY, 24))
        ico.setStyleSheet("background: transparent; border: none;")
        lay.addWidget(ico)
        lbl = QLabel(text)
        lbl.setObjectName("emptyState")
        lbl.setAlignment(Qt.AlignCenter)
        lbl.setWordWrap(True)
        lay.addWidget(lbl)

    def add_widget(self, widget: QWidget):
        self.layout().addWidget(widget, 0, Qt.AlignCenter)


class RegionChips(QWidget):
    """地区计数芯片（点选过滤）。"""

    selection_changed = Signal(list)  # 选中的地区码列表

    def __init__(self, parent=None):
        super().__init__(parent)
        self._flow = FlowLayout(self, h_spacing=8, v_spacing=8)
        self.setLayout(self._flow)
        self._selected: set = set()
        self._codes: List[str] = []
        self._buttons: dict = {}

    def set_stats(self, stats: List[dict]):
        """stats: [{'code','name','count'}]"""
        # 先脱离父级再延迟销毁，否则旧芯片会叠出新芯片（重影）
        clear_layout(self._flow)
        self._buttons.clear()
        self._codes = [s['code'] for s in stats]
        # 清掉已不存在的选中项
        self._selected &= set(self._codes)

        for s in stats:
            btn = QPushButton(f"{s['name']} {s['code']} · {s['count']}")
            btn.setCheckable(True)
            btn.setProperty("class", "chip")
            btn.setCursor(Qt.PointingHandCursor)
            btn.setFont(QFont(FONT_FAMILY, 9))
            btn.setStyleSheet(CHIP_STYLE)
            btn.setChecked(s['code'] in self._selected)
            btn.clicked.connect(lambda _c=False, code=s['code']: self._toggle(code))
            self._flow.addWidget(btn)
            self._buttons[s['code']] = btn

    def _toggle(self, code: str):
        if code in self._selected:
            self._selected.discard(code)
        else:
            self._selected.add(code)
        self.selection_changed.emit(self.selected_codes())

    def selected_codes(self) -> List[str]:
        return [c for c in self._codes if c in self._selected]

    def clear_selection(self):
        self._selected.clear()
        self._sync_checked()
        self.selection_changed.emit([])

    def select_all(self):
        self._selected = set(self._codes)
        self._sync_checked()
        self.selection_changed.emit(self.selected_codes())

    def _sync_checked(self):
        for code, btn in self._buttons.items():
            btn.setChecked(code in self._selected)


class FunnelBar(QWidget):
    """漏斗统计条：生成 → 延迟达标 → 地区解析 → 可用。"""

    def __init__(self, parent=None):
        super().__init__(parent)
        self._layout = QHBoxLayout(self)
        self._layout.setContentsMargins(0, 0, 0, 0)
        self._layout.setSpacing(8)
        self._widgets: List[QWidget] = []
        self.set_steps([])   # 初始就渲染占位，避免卡片空着

    def _make_step(self, text: str) -> QLabel:
        lab = QLabel(text)
        lab.setFont(QFont(FONT_FAMILY, 10))
        lab.setStyleSheet(
            "background: #EFF6FF; border: 1px solid #BFDBFE; color: #1E40AF;"
            "border-radius: 9px; padding: 6px 12px; font-family: '%s';" % FONT_FAMILY
        )
        return lab

    def set_steps(self, steps: List[Tuple[str, object]]):
        """steps: [(label, value)]；value 为 None 时不显示数值。

        注意：clear_layout 会连末尾的 stretch 一并移除，所以这里必须
        全部用 addWidget 顺序追加、最后再补一个 stretch。
        早期版本用 `insertWidget(self._layout.count() - 1, ...)`，
        在 stretch 被移除后索引算错，会把步骤插成
        「延迟达标 → 地区解析 → 可用 生成」这种错乱顺序。
        """
        clear_layout(self._layout)
        self._widgets.clear()

        if not steps:
            lab = self._make_step("等待开始")
            self._layout.addWidget(lab)
            self._widgets.append(lab)
        else:
            for i, (label, value) in enumerate(steps):
                if i > 0:
                    arr = QLabel("→")
                    arr.setStyleSheet(
                        f"color: {C_MUTED_LIGHT}; font-size: 13px; border: none; background: transparent;")
                    self._layout.addWidget(arr)
                    self._widgets.append(arr)
                text = label if value is None else f"{label} {value}"
                lab = self._make_step(text)
                self._layout.addWidget(lab)
                self._widgets.append(lab)

        # 末尾补 stretch，让步骤整体靠左
        self._layout.addStretch()

    def clear(self):
        self.set_steps([])


class LogTerminal(QWidget):
    """深色终端日志（可折叠 / 可清空 / 可复制全部）。"""

    def __init__(self, title: str = "运行日志", parent=None):
        super().__init__(parent)
        self._max_lines = 2000

        head = QHBoxLayout()
        head.setSpacing(8)
        self._title = QLabel(title)
        self._title.setObjectName("cardTitle")
        self._count = QLabel("0 行")
        self._count.setObjectName("cardSubtitle")
        self._copy_btn = QPushButton("复制")
        self._clear_btn = QPushButton("清空")
        self._toggle_btn = QPushButton("收起")
        btn_style = (
            "QPushButton { background: white; border: 1px solid #D3DAE3; border-radius: 7px;"
            " color: #475569; font-size: 11px; padding: 2px 9px; }"
            "QPushButton:hover { background: #F1F5F9; }"
        )
        for b in (self._copy_btn, self._clear_btn, self._toggle_btn):
            b.setFixedHeight(24)
            b.setCursor(Qt.PointingHandCursor)
            b.setFont(QFont(FONT_FAMILY, 9))
            b.setStyleSheet(btn_style)
        head.addWidget(self._title)
        head.addWidget(self._count)
        head.addStretch()
        head.addWidget(self._copy_btn)
        head.addWidget(self._clear_btn)
        head.addWidget(self._toggle_btn)

        self.output = QPlainTextEdit()
        self.output.setReadOnly(True)
        self.output.setStyleSheet(TERMINAL_STYLE)
        self.output.setMinimumHeight(96)
        self.output.setLineWrapMode(QPlainTextEdit.NoWrap)

        lay = QVBoxLayout(self)
        lay.setContentsMargins(0, 0, 0, 0)
        lay.setSpacing(7)
        lay.addLayout(head)
        lay.addWidget(self.output)

        self._toggle_btn.clicked.connect(self._toggle)
        self._clear_btn.clicked.connect(self.clear)
        self._copy_btn.clicked.connect(self._copy_all)

    def _toggle(self):
        collapsed = self.output.isVisible()
        self.output.setVisible(not collapsed)
        self._toggle_btn.setText("展开" if collapsed else "收起")

    def _copy_all(self):
        from PySide6.QtWidgets import QApplication
        QApplication.clipboard().setText(self.output.toPlainText())

    def clear(self):
        self.output.clear()
        self._update_count()

    def _update_count(self):
        self._count.setText(f"{self.output.document().blockCount()} 行")

    def append_line(self, msg: str):
        sb = self.output.verticalScrollBar()
        at_bottom = sb.value() >= sb.maximum() - 4
        stamp = datetime.now().strftime("[%H:%M:%S] ")
        self.output.appendPlainText(stamp + str(msg))
        # 超出上限裁剪最旧行
        doc = self.output.document()
        if doc.blockCount() > self._max_lines:
            self.output.setPlainText(
                "\n".join(self.output.toPlainText().splitlines()[-self._max_lines:])
            )
        if at_bottom:
            sb.setValue(sb.maximum())
        self._update_count()


class SideNav(QFrame):
    """左侧导航栏。"""

    pageChanged = Signal(int)

    def __init__(self, items: List[Tuple[str, str]], parent=None):
        """items: [(icon_text, label)]"""
        super().__init__(parent)
        self.setObjectName("sidebar")
        self.setFixedWidth(118)
        self.setStyleSheet(SIDEBAR_STYLE)

        lay = QVBoxLayout(self)
        lay.setContentsMargins(11, 18, 11, 14)
        lay.setSpacing(5)

        logo = QLabel("☁\nCloudTrace")
        logo.setAlignment(Qt.AlignCenter)
        logo.setFont(QFont(FONT_FAMILY, 13))
        logo.setStyleSheet(
            "color: white; background: transparent; border: none; font-weight: bold; line-height: 1.25;")
        lay.addWidget(logo)
        sub = QLabel("云迹 · 扫描面板")
        sub.setAlignment(Qt.AlignCenter)
        sub.setStyleSheet("color: rgba(255,255,255,120); font-size: 10px; background: transparent; border: none;")
        lay.addWidget(sub)
        lay.addSpacing(14)

        self._buttons: List[QPushButton] = []
        self._group = QButtonGroup(self)
        self._group.setExclusive(True)
        for i, (icon, label) in enumerate(items):
            btn = QPushButton(f"{icon}\n{label}")
            btn.setCheckable(True)
            btn.setFixedHeight(58)
            btn.setCursor(Qt.PointingHandCursor)
            btn.setFont(QFont(FONT_FAMILY, 9))
            btn.setProperty("class", "nav")
            lay.addWidget(btn)
            self._group.addButton(btn, i)
            self._buttons.append(btn)
            btn.clicked.connect(lambda _c=False, idx=i: self.pageChanged.emit(idx))

        lay.addStretch()
        footer = QLabel("v" + _safe_version())
        footer.setAlignment(Qt.AlignCenter)
        footer.setStyleSheet("color: rgba(255,255,255,110); font-size: 10px; background: transparent; border: none;")
        lay.addWidget(footer)

        self.set_active(0)

    def set_active(self, i: int):
        if 0 <= i < len(self._buttons):
            self._buttons[i].setChecked(True)


def _safe_version() -> str:
    try:
        from core.constants import get_version
        return get_version()
    except Exception:
        return "?"
