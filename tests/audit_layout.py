#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""桌面 UI 排版审计：找出「文字被裁切 / 列被挤没」的控件。

为什么需要单独一个脚本（而不是并进 verify_ui.py）：
  verify_ui.py 跑在 offscreen 平台，该后端的字体度量退化（ascent==字号、
  descent==0），算出来的「自然高度」偏小，按钮一类控件即使被压扁也测不出来。
  本脚本用**原生平台 + WA_DontShowOnScreen**（与 render_screenshots.py 同款），
  拿到真实字体度量，才能真正判定「内容盒被压扁 → 文字上下被裁切」。

判定口径：
  1) 控件自然高度 sizeHint()（换行 QLabel 用 heightForWidth(实际宽度)）
     > 实际高度  ⇒ 文字垂直裁切
  2) 表格某列 columnWidth() < sizeHintForColumn()  ⇒ 该列内容被省略号截断

用法（在项目根目录）：
    python tests/audit_layout.py
无图形界面时设 CLOUDTRACE_AUDIT_OFFSCREEN=1 走离屏（结论会偏乐观，仅供冒烟）。

退出码：发现问题 → 1；全部干净 → 0。
"""
import os
import sys

if os.environ.get("CLOUDTRACE_AUDIT_OFFSCREEN") == "1":
    os.environ["QT_QPA_PLATFORM"] = "offscreen"
else:
    os.environ.pop("QT_QPA_PLATFORM", None)
os.environ.setdefault("CLOUDTRACE_ALLOW_MULTI", "1")

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)

# settings.json 是仓库外的运行时文件，本脚本只读不写；但为了与其它测试一致，
# 仍做二进制备份 + atexit 还原，避免任何意外写入。
import atexit

SETTINGS_PATH = os.path.join(ROOT, "settings.json")
_ORIG_SETTINGS = None
if os.path.exists(SETTINGS_PATH):
    with open(SETTINGS_PATH, "rb") as f:
        _ORIG_SETTINGS = f.read()


def restore_settings_file():
    if _ORIG_SETTINGS is not None:
        with open(SETTINGS_PATH, "wb") as f:
            f.write(_ORIG_SETTINGS)


atexit.register(restore_settings_file)

from PySide6.QtWidgets import (
    QApplication, QLabel, QLineEdit, QComboBox, QPushButton, QAbstractSpinBox,
    QTableWidget, QStackedWidget,
)
from PySide6.QtGui import QFontDatabase, QFont
from PySide6.QtCore import Qt

from settings import get_settings
from ui.main_window import CloudflareScanUI

_FONT_CANDIDATES = [
    r"C:\Windows\Fonts\msyh.ttc",
    r"C:\Windows\Fonts\seguiemj.ttf",
    "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
    "/System/Library/Fonts/PingFang.ttc",
]

app = QApplication.instance() or QApplication(sys.argv)
if not QFontDatabase.families():
    for _path in _FONT_CANDIDATES:
        if os.path.exists(_path):
            _fid = QFontDatabase.addApplicationFont(_path)
            if _fid != -1:
                fams = QFontDatabase.applicationFontFamilies(_fid)
                if fams:
                    app.setFont(QFont(fams[0], 10))
                    break

print("平台: %s | 逻辑 DPI: %s" % (app.platformName(),
                                   app.primaryScreen().logicalDotsPerInch()))

SCAN_RESULTS = [
    {"ip": "104.16.132.229", "latency": 42.3, "latency_avg": 44.1, "jitter": 3.4, "loss": 0.0,
     "iata_code": "HKG", "chinese_name": "中国香港", "colo": "HKG", "loc": "HK", "port": 443,
     "use_tls": True, "scan_mode": "tcping", "scan_time": "2026-01-12 21:04:11", "ip_version": 4,
     "visit_scheme": "https", "http_version": "HTTP/2", "tls_version": "TLSv1.3"},
    {"ip": "162.159.36.5", "latency": 91.4, "latency_avg": 95.3, "jitter": 7.2, "loss": 0.0,
     "iata_code": "NRT", "chinese_name": "日本", "colo": "NRT", "loc": "JP", "port": 443,
     "use_tls": True, "scan_mode": "tcping", "scan_time": "2026-01-12 21:04:11", "ip_version": 4,
     "visit_scheme": "https", "http_version": "HTTP/2", "tls_version": "TLSv1.3"},
]
SPEED_RESULTS = [
    {"ip": "104.16.132.229", "latency": 42.3, "latency_avg": 44.1, "jitter": 3.4, "loss": 0.0,
     "download_speed": 28.64, "score": 24.9, "iata_code": "HKG", "chinese_name": "中国香港",
     "colo": "HKG", "loc": "HK", "port": 443, "use_tls": True, "http_version": "HTTP/2",
     "tls_version": "TLSv1.3", "visit_scheme": "https", "test_type": "完全测速", "verified": True},
]


def build_window(width: int = 1280, height: int = 900):
    win = CloudflareScanUI()
    win.setAttribute(Qt.WA_DontShowOnScreen, True)
    try:
        win.tray_icon.hide()
    except Exception:
        pass
    win.resize(width, height)
    win.show()
    for _ in range(3):
        app.processEvents()
    return win


def audit(win):
    win.result_page.set_results(SCAN_RESULTS, scan_mode="tcping")
    win.speed_page.set_results(SPEED_RESULTS)
    stack = win.findChild(QStackedWidget)
    clipped, crushed = [], []
    for i in range(stack.count()):
        win._set_page(i)
        for _ in range(2):
            app.processEvents()
        page = stack.widget(i)
        pname = type(page).__name__
        for w in page.findChildren(object):
            if not isinstance(w, (QLabel, QLineEdit, QComboBox, QPushButton, QAbstractSpinBox)):
                continue
            if w.isHidden() or w.height() <= 0:
                continue
            if isinstance(w, QLabel) and w.wordWrap() and w.width() > 0:
                need = w.heightForWidth(w.width())
            else:
                need = w.sizeHint().height()
            if need > w.height():
                text = w.text() if hasattr(w, "text") else ""
                clipped.append(f"{pname}/{type(w).__name__} {text[:16]!r} "
                               f"h={w.height()} need={need}")
        for t in page.findChildren(QTableWidget):
            if t.rowCount() == 0:
                continue
            for c in range(t.columnCount()):
                hint = t.sizeHintForColumn(c)
                if t.columnWidth(c) < hint:
                    head = t.horizontalHeaderItem(c).text() if t.horizontalHeaderItem(c) else ""
                    crushed.append(f"{pname}/col{c} {head!r} "
                                   f"w={t.columnWidth(c)} need={hint}")
    return clipped, crushed


def main() -> int:
    win = build_window()
    clipped, crushed = audit(win)
    print(f"\n文字被垂直裁切的控件：{len(clipped)} 处")
    for line in clipped:
        print("  " + line)
    print(f"\n列宽不足（内容会被截断）的表格列：{len(crushed)} 处")
    for line in crushed:
        print("  " + line)
    win.bridge.detach()
    win.close()
    if clipped or crushed:
        print("\n排版审计未通过 ✗")
        return 1
    print("\n排版审计通过 ✓")
    return 0


if __name__ == "__main__":
    sys.exit(main())
