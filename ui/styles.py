#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""样式与字体定义（桌面端）。

设计约定：
- 所有 QSS 片段集中在这里，页面只引用，避免各处硬编码颜色导致风格漂移。
- 颜色语义：涨/成功=绿(#16A34A)，警示=橙(#EA580C)，危险=红(#DC2626)。
  （本工具与股市无关，不涉及红涨绿跌约定。）
- 卡片阴影用 QGraphicsDropShadowEffect（QSS 不支持 box-shadow）。
"""

from PySide6.QtGui import QFont
from core.constants import FONT_FAMILY


FONT_TITLE = QFont(FONT_FAMILY, 20)
FONT_TITLE.setBold(True)
FONT_BTN = QFont(FONT_FAMILY, 11)
FONT_SMALL = FONT_BTN
FONT_STATUS = QFont(FONT_FAMILY, 10)
FONT_LABEL = QFont(FONT_FAMILY, 10)
FONT_H1 = QFont(FONT_FAMILY, 16)
FONT_H1.setBold(True)
FONT_METRIC = QFont(FONT_FAMILY, 19)
FONT_METRIC.setBold(True)

# ---------------- 调色板 ----------------
C_BLUE = "#2563EB"
C_BLUE_DARK = "#1D4ED8"
C_BLUE_SOFT = "#EFF6FF"
C_NAVY = "#0F2B44"
C_NAVY2 = "#17395A"
C_BG = "#F5F7FA"
C_CARD = "#FFFFFF"
C_BORDER = "#E6EAF0"
C_BORDER_STRONG = "#D3DAE3"
C_TEXT = "#0F172A"
C_TEXT_SOFT = "#334155"
C_MUTED = "#64748B"
C_MUTED_LIGHT = "#94A3B8"
C_GREEN = "#16A34A"
C_GREEN_DARK = "#15803D"
C_ORANGE = "#EA580C"
C_ORANGE_DARK = "#C2410C"
C_RED = "#DC2626"
C_RED_DARK = "#B91C1C"
C_PURPLE = "#7C3AED"
C_TEAL = "#0D9488"

# 状态色（浅底深字，用于胶囊/徽标）
STATUS_BG = {
    "idle": ("#F1F5F9", C_MUTED),
    "run": ("#DCFCE7", "#15803D"),
    "busy": ("#FEF3C7", "#B45309"),
    "error": ("#FEE2E2", "#B91C1C"),
}

SCROLLBAR_CSS = f"""
QScrollBar:vertical {{ background: transparent; width: 10px; margin: 2px; }}
QScrollBar::handle:vertical {{ background: #CBD5E1; min-height: 28px; border-radius: 5px; }}
QScrollBar::handle:vertical:hover {{ background: #94A3B8; }}
QScrollBar::add-line:vertical, QScrollBar::sub-line:vertical {{ height: 0px; }}
QScrollBar::add-page:vertical, QScrollBar::sub-page:vertical {{ background: none; }}
QScrollBar:horizontal {{ background: transparent; height: 10px; margin: 2px; }}
QScrollBar::handle:horizontal {{ background: #CBD5E1; min-width: 28px; border-radius: 5px; }}
QScrollBar::handle:horizontal:hover {{ background: #94A3B8; }}
QScrollBar::add-line:horizontal, QScrollBar::sub-line:horizontal {{ width: 0px; }}
QScrollBar::add-page:horizontal, QScrollBar::sub-page:horizontal {{ background: none; }}
"""

# 窗口级全局规则：字体/背景/基础文字色 + 滚动条 + 提示
APP_QSS = f"""
QWidget {{ font-family: "{FONT_FAMILY}", sans-serif; background: {C_BG}; color: {C_TEXT}; }}
QToolTip {{
    background: #0F172A; color: #F8FAFC; border: none; border-radius: 6px;
    padding: 5px 8px; font-family: "{FONT_FAMILY}"; font-size: 12px;
}}
{SCROLLBAR_CSS}
"""

# ---------------- 兼容旧版（深色表格，保留以防外部引用） ----------------
TABLE_STYLE = f"""
QTableWidget {{
    background: #0B3C5D; border-radius: 8px; color: white;
    gridline-color: #1E4D6B;
}}
QHeaderView::section {{
    background: #0F4C75; color: white; border: none; height: 32px;
    padding-left: 10px; font-family: "{FONT_FAMILY}";
}}
QTableWidget::item {{
    padding: 5px; border-bottom: 1px solid #1E4D6B;
    font-family: "{FONT_FAMILY}", sans-serif;
}}
{SCROLLBAR_CSS}
"""

LOG_STYLE = f"""
QTextEdit {{
    background: #0B3C5D; border: 1px solid #0F4C75; border-radius: 6px;
    padding: 10px; color: #ECF0F1; font-family: "{FONT_FAMILY}", sans-serif;
}}
{SCROLLBAR_CSS}
"""

# ---------------- 表格（浅色） ----------------
TABLE_LIGHT_STYLE = f"""
QTableWidget {{
    background: {C_CARD}; border: none; border-radius: 10px;
    gridline-color: transparent; color: {C_TEXT};
    font-family: "{FONT_FAMILY}"; font-size: 13px;
    selection-background-color: {C_BLUE_SOFT};
    selection-color: {C_TEXT};
    alternate-background-color: #FAFBFD;
    outline: none;
}}
QTableWidget::item {{ padding: 8px 10px; border-bottom: 1px solid #F1F5F9; }}
QTableWidget::item:hover {{ background: #F8FAFC; }}
QTableWidget::item:selected {{ background: {C_BLUE_SOFT}; color: {C_TEXT}; }}
QHeaderView {{ background: transparent; }}
QHeaderView::section {{
    background: #F8FAFC; color: {C_MUTED}; border: none;
    border-bottom: 1px solid {C_BORDER}; height: 36px;
    padding-left: 10px; font-family: "{FONT_FAMILY}"; font-size: 12px; font-weight: 600;
}}
QHeaderView::section:first {{ border-top-left-radius: 10px; }}
QHeaderView::section:last {{ border-top-right-radius: 10px; }}
QTableCornerButton::section {{ background: #F8FAFC; border: none; }}
{SCROLLBAR_CSS}
"""

TERMINAL_STYLE = f"""
QPlainTextEdit {{
    background: #0B1120; border: 1px solid #1E293B; border-radius: 10px;
    padding: 10px 12px; color: #CBD5E1;
    font-family: Consolas, "Courier New", monospace; font-size: 12px;
    selection-background-color: #1D4ED8;
}}
{SCROLLBAR_CSS}
"""

# ---------------- 侧边导航 ----------------
SIDEBAR_STYLE = f"""
QFrame#sidebar {{
    background: qlineargradient(x1:0, y1:0, x2:0, y2:1,
    stop:0 {C_NAVY}, stop:1 {C_NAVY2});
    border: none;
}}
QFrame#sidebar QLabel {{ background: transparent; border: none; }}
QPushButton[class="nav"] {{
    background: transparent; color: rgba(255,255,255,0.68);
    border: none; border-radius: 10px;
    font-family: "{FONT_FAMILY}"; font-size: 12px; padding: 9px 0;
    text-align: center;
}}
QPushButton[class="nav"]:hover {{ background: rgba(255,255,255,0.10); color: #FFFFFF; }}
QPushButton[class="nav"]:checked {{
    background: rgba(37,99,235,0.92); color: #FFFFFF;
    font-weight: 600;
}}
"""

# ---------------- 卡片 ----------------
CARD_STYLE = f"""
QFrame#card {{
    background: {C_CARD}; border: 1px solid {C_BORDER}; border-radius: 14px;
}}
QFrame#card QLabel {{ background: transparent; border: none; }}
QLabel#cardTitle {{
    color: {C_TEXT}; font-size: 13px; font-weight: 600;
    font-family: "{FONT_FAMILY}"; background: transparent; border: none;
}}
QLabel#cardSubtitle {{
    color: {C_MUTED}; font-size: 11px;
    font-family: "{FONT_FAMILY}"; background: transparent; border: none;
}}
QFrame#subCard {{
    background: #F8FAFC; border: 1px solid {C_BORDER}; border-radius: 10px;
}}
"""

# ---------------- 表单控件 ----------------
FIELD_STYLE = f"""
QLineEdit, QSpinBox, QDoubleSpinBox, QComboBox, QPlainTextEdit {{
    background: {C_CARD}; color: {C_TEXT};
    border: 1px solid {C_BORDER_STRONG}; border-radius: 8px;
    padding: 5px 9px; font-family: "{FONT_FAMILY}"; font-size: 13px;
    selection-background-color: {C_BLUE}; selection-color: white;
    min-height: 22px;
}}
QLineEdit:hover, QSpinBox:hover, QDoubleSpinBox:hover, QComboBox:hover, QPlainTextEdit:hover {{
    border: 1px solid #B6C2D2;
}}
QLineEdit:focus, QSpinBox:focus, QDoubleSpinBox:focus, QComboBox:focus, QPlainTextEdit:focus {{
    border: 1px solid {C_BLUE};
}}
QLineEdit:disabled, QSpinBox:disabled, QDoubleSpinBox:disabled, QComboBox:disabled {{
    background: #F1F5F9; color: {C_MUTED_LIGHT};
}}
QSpinBox::up-button, QDoubleSpinBox::up-button,
QSpinBox::down-button, QDoubleSpinBox::down-button {{
    width: 16px; background: transparent; border: none;
}}
QSpinBox::up-arrow, QDoubleSpinBox::up-arrow {{
    image: none; width: 0; height: 0;
    border-left: 4px solid transparent; border-right: 4px solid transparent;
    border-bottom: 5px solid {C_MUTED};
}}
QSpinBox::down-arrow, QDoubleSpinBox::down-arrow {{
    image: none; width: 0; height: 0;
    border-left: 4px solid transparent; border-right: 4px solid transparent;
    border-top: 5px solid {C_MUTED};
}}
QComboBox::drop-down {{ border: none; width: 24px; }}
QComboBox::down-arrow {{
    image: none; width: 0; height: 0; margin-right: 8px;
    border-left: 4px solid transparent; border-right: 4px solid transparent;
    border-top: 5px solid {C_MUTED};
}}
QComboBox QAbstractItemView {{
    background: {C_CARD}; border: 1px solid {C_BORDER_STRONG}; border-radius: 8px;
    selection-background-color: {C_BLUE_SOFT}; selection-color: {C_TEXT};
    padding: 4px; outline: none;
}}
QLabel.fieldLabel {{
    color: {C_TEXT_SOFT}; font-size: 12px; font-weight: 500;
    font-family: "{FONT_FAMILY}"; background: transparent; border: none;
}}
QLabel.hintText {{
    color: {C_MUTED_LIGHT}; font-size: 11px;
    font-family: "{FONT_FAMILY}"; background: transparent; border: none;
}}
QCheckBox {{
    color: {C_TEXT_SOFT}; font-size: 12px; font-family: "{FONT_FAMILY}";
    background: transparent; border: none; spacing: 7px;
}}
QCheckBox::indicator {{
    width: 16px; height: 16px; border-radius: 4px;
    border: 1px solid {C_BORDER_STRONG}; background: {C_CARD};
}}
QCheckBox::indicator:hover {{ border: 1px solid {C_BLUE}; }}
QCheckBox::indicator:checked {{ background: {C_BLUE}; border: 1px solid {C_BLUE}; }}
QRadioButton {{
    color: {C_TEXT_SOFT}; font-size: 13px; font-family: "{FONT_FAMILY}";
    background: transparent; spacing: 7px;
}}
QRadioButton::indicator {{
    width: 15px; height: 15px; border-radius: 8px;
    border: 1px solid {C_BORDER_STRONG}; background: {C_CARD};
}}
QRadioButton::indicator:checked {{ border: 5px solid {C_BLUE}; background: {C_CARD}; }}
"""

CHIP_STYLE = f"""
QPushButton[class="chip"] {{
    background: {C_CARD}; color: {C_TEXT_SOFT};
    border: 1px solid {C_BORDER}; border-radius: 999px;
    padding: 5px 13px; font-size: 12px; font-family: "{FONT_FAMILY}";
}}
QPushButton[class="chip"]:hover {{ border-color: {C_BLUE}; color: {C_BLUE}; background: {C_BLUE_SOFT}; }}
QPushButton[class="chip"]:checked {{
    background: {C_BLUE}; border: 1px solid {C_BLUE}; color: white; font-weight: 600;
}}
"""

SEG_STYLE = f"""
QFrame#segGroup {{ border: 1px solid {C_BORDER_STRONG}; border-radius: 9px; background: #F1F5F9; }}
QPushButton[class="seg"] {{
    background: transparent; color: {C_TEXT_SOFT}; border: none;
    padding: 6px 16px; font-family: "{FONT_FAMILY}"; font-size: 13px;
    border-radius: 7px;
}}
QPushButton[class="seg"]:hover {{ color: {C_BLUE}; }}
QPushButton[class="seg"]:checked {{ background: {C_CARD}; color: {C_BLUE}; font-weight: 600; }}
"""

PROGRESS_STYLE = f"""
QProgressBar {{ background: #E8EDF3; border: none; border-radius: 3px; }}
QProgressBar::chunk {{
    background: qlineargradient(x1:0, y1:0, x2:1, y2:0,
        stop:0 {C_BLUE}, stop:1 #38BDF8);
    border-radius: 3px;
}}
"""

PILL_STYLE = f"""
QLabel#statusPill {{
    background: #F1F5F9; color: {C_MUTED}; border-radius: 999px;
    padding: 4px 13px; font-size: 12px; font-family: "{FONT_FAMILY}"; font-weight: 600;
}}
QLabel#statusPill[status="run"] {{ background: #DCFCE7; color: #15803D; }}
QLabel#statusPill[status="busy"] {{ background: #FEF3C7; color: #B45309; }}
QLabel#statusPill[status="error"] {{ background: #FEE2E2; color: #B91C1C; }}
"""

# 顶栏「HTTP 面板」地址徽标（可点击复制）
ENDPOINT_BADGE_STYLE = f"""
QLabel#endpointBadge {{
    background: {C_BLUE_SOFT}; color: {C_BLUE}; border: 1px solid #BFDBFE;
    border-radius: 999px; padding: 4px 11px; font-size: 11px;
    font-family: "{FONT_FAMILY}";
}}
QLabel#endpointBadge[state="off"] {{
    background: #F1F5F9; color: {C_MUTED_LIGHT}; border: 1px solid {C_BORDER};
}}
QLabel#endpointBadge[state="error"] {{
    background: #FEF2F2; color: {C_RED}; border: 1px solid #FECACA;
}}
"""

# 统计小卡（结果页/测速页顶部指标）
STAT_CARD_STYLE = f"""
QFrame#statCard {{
    background: {C_CARD}; border: 1px solid {C_BORDER}; border-radius: 12px;
}}
QFrame#statCard QLabel {{ background: transparent; border: none; }}
QLabel#statLabel {{ color: {C_MUTED}; font-size: 11px; font-family: "{FONT_FAMILY}"; }}
QLabel#statValue {{ color: {C_TEXT}; font-size: 19px; font-weight: 700; font-family: "{FONT_FAMILY}"; }}
QLabel#statUnit {{ color: {C_MUTED_LIGHT}; font-size: 11px; font-family: "{FONT_FAMILY}"; }}
"""

BADGE_STYLE = f"""
QLabel[class="badge"] {{
    background: #F1F5F9; color: {C_MUTED}; border-radius: 999px;
    padding: 2px 9px; font-size: 11px; font-family: "{FONT_FAMILY}";
}}
QLabel[class="badgeGreen"] {{
    background: #DCFCE7; color: #15803D; border-radius: 999px;
    padding: 2px 9px; font-size: 11px; font-family: "{FONT_FAMILY}";
}}
"""

EMPTY_STATE_STYLE = f"""
QLabel#emptyState {{
    color: {C_MUTED_LIGHT}; font-size: 13px; font-family: "{FONT_FAMILY}";
    background: transparent; border: none;
}}
"""


def btn_stylesheet(color: str, text_color: str = "white", hover_color: str = None) -> str:
    if hover_color is None:
        hover_color = color
    return f"""
    QPushButton {{
        background: {color}; color: {text_color}; border-radius: 8px;
        font-family: "{FONT_FAMILY}"; font-size: 13px; font-weight: 600;
        border: none; padding: 7px 16px;
    }}
    QPushButton:disabled {{ background: #E8EDF3; color: {C_MUTED_LIGHT}; }}
    QPushButton:hover:!disabled {{ background: {hover_color}; }}
    QPushButton:pressed:!disabled {{ background: {hover_color}; }}
    """


def ghost_btn_stylesheet() -> str:
    return f"""
    QPushButton {{
        background: {C_CARD}; color: {C_TEXT_SOFT}; border: 1px solid {C_BORDER_STRONG};
        border-radius: 8px; font-family: "{FONT_FAMILY}"; font-size: 13px;
        padding: 7px 16px;
    }}
    QPushButton:disabled {{ color: {C_MUTED_LIGHT}; background: #F8FAFC; border-color: {C_BORDER}; }}
    QPushButton:hover:!disabled {{ background: #F1F5F9; border-color: #B6C2D2; }}
    """


def danger_btn_stylesheet() -> str:
    return f"""
    QPushButton {{
        background: {C_CARD}; color: {C_RED}; border: 1px solid #FECACA;
        border-radius: 8px; font-family: "{FONT_FAMILY}"; font-size: 13px;
        padding: 7px 16px;
    }}
    QPushButton:disabled {{ color: {C_MUTED_LIGHT}; background: #F8FAFC; border-color: {C_BORDER}; }}
    QPushButton:hover:!disabled {{ background: #FEF2F2; border-color: {C_RED}; }}
    """
