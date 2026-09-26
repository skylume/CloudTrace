#!/usr/bin/env python3
# -*- coding: utf-8 -*-

from PySide6.QtCore import QObject, Signal

from service.task_manager import task_manager
from service.events import (
    EV_LOG, EV_PROGRESS, EV_FUNNEL, EV_STATE,
    EV_SCAN_DONE, EV_SPEED_PROGRESS, EV_SPEED_DONE, EV_SPEED_ABORT, EV_SETTINGS,
)


class WorkerBridge(QObject):
    """事件总线 → Qt 信号桥。

    工作线程 emit → 本对象以 QueuedConnection 转发到主线程槽函数。

    重要：**每一个 subscribe() 的返回值都必须登记到 _unsubs**。
    只登记一部分会让窗口关闭后仍有订阅持有已析构 QObject 的 bound method，
    工作线程再 emit 时会调用已删除的 C++ 对象（异常被事件总线吞掉，表现为
    日志刷屏 + 对象无法回收）。
    """

    progress_update = Signal(int, int, int, float)
    status_message = Signal(str)
    funnel_updated = Signal(dict)
    scan_completed = Signal(list)
    scan_aborted = Signal()
    speed_progress = Signal(int, int, float)
    speed_completed = Signal(list)
    speed_aborted = Signal()
    state_changed = Signal(dict)
    settings_changed = Signal(dict)

    def __init__(self, parent=None):
        super().__init__(parent)
        self._unsubs = []
        bus = task_manager.bus
        subscriptions = (
            bus.subscribe(EV_LOG, self.status_message.emit),
            bus.subscribe(EV_PROGRESS, lambda p: self.progress_update.emit(*p)),
            bus.subscribe(EV_FUNNEL, self.funnel_updated.emit),
            bus.subscribe(EV_SPEED_PROGRESS, lambda p: self.speed_progress.emit(*p)),
            bus.subscribe(EV_SCAN_DONE, self._on_scan_done),
            bus.subscribe(EV_SPEED_DONE, self.speed_completed.emit),
            bus.subscribe(EV_SPEED_ABORT, self._on_speed_abort),
            # EV_STATE / EV_SETTINGS 载荷为完整快照：供主窗口同步「另一侧 UI」写入的数据
            bus.subscribe(EV_STATE, self.state_changed.emit),
            bus.subscribe(EV_SETTINGS, self.settings_changed.emit),
        )
        self._unsubs.extend(subscriptions)

    def detach(self):
        """断开事件总线订阅（窗口销毁前调用，避免向已析构对象发信号）。"""
        for unsub in self._unsubs:
            try:
                unsub()
            except Exception:
                pass
        self._unsubs.clear()

    def _on_scan_done(self, results):
        if results is not None:
            self.scan_completed.emit(results)
        else:
            self.scan_aborted.emit()

    def _on_speed_abort(self, _payload=None):
        self.speed_aborted.emit()
