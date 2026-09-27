#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import sys
import logging

# 配置日志
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
)

from core.compat import IS_WIN7

if IS_WIN7:
    try:
        import asyncio
        asyncio.set_event_loop_policy(asyncio.WindowsSelectorEventLoopPolicy())
    except AttributeError:
        pass

from PySide6.QtWidgets import QApplication
from PySide6.QtGui import QFont
from PySide6.QtCore import Qt
from core.constants import FONT_FAMILY
from ui.main_window import CloudflareScanUI


if __name__ == '__main__':
    if sys.version_info < (3, 8):
        print("错误: 此程序需要 Python 3.8 或更高版本")
        sys.exit(1)

    # 必须在 QApplication 之前设置：高分屏缩放策略显式声明为 PassThrough
    # （Qt6 默认值），避免被外部环境变量悄悄改成 Round/Floor 导致布局与字号错位。
    QApplication.setHighDpiScaleFactorRoundingPolicy(
        Qt.HighDpiScaleFactorRoundingPolicy.PassThrough)

    app = QApplication(sys.argv)
    app.setApplicationName("CloudTrace 云迹")

    # 统一默认字体为 QSS 使用的同一字体族。
    # Windows 的系统默认字体是 "Microsoft YaHei UI"，而 QSS 里写的是
    # "Microsoft YaHei"；两者字宽/字重不同，混用会让未被 QSS 覆盖的控件
    # （对话框、表格项等）看起来比其它文字更细更虚。这里统一成 13px 整数像素，
    # 避免点值换算出小数像素导致的栅格化发虚。
    _app_font = QFont(FONT_FAMILY)
    _app_font.setPixelSize(13)
    app.setFont(_app_font)

    # 确保最后一个窗口关闭时应用程序也能正确退出
    app.setQuitOnLastWindowClosed(False)

    # 单实例：双开会抢同一个 HTTP 端口与 CloudTrace_history 目录
    from core.single_instance import acquire_single_instance
    if not acquire_single_instance():
        from ui.dialogs import CustomMessageBox
        CustomMessageBox.warning(
            None, "CloudTrace 已在运行",
            "检测到另一个 CloudTrace 实例正在运行。\n"
            "同时运行会导致 HTTP 面板端口冲突与历史文件互相覆盖。\n\n"
            "如需强制多开，请设置环境变量 CLOUDTRACE_ALLOW_MULTI=1。",
        )
        sys.exit(0)

    window = CloudflareScanUI()
    window.show()

    exit_code = app.exec()
    logging.info(f"应用程序退出，代码: {exit_code}")
    sys.exit(exit_code)
