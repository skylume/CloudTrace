#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import logging
import threading
from typing import Any, Callable, Dict, List


logger = logging.getLogger("CloudTrace")

# 事件类型常量
EV_LOG = "log"                    # payload: str
EV_PROGRESS = "progress"          # payload: (completed, total, success, ips_per_second)
EV_FUNNEL = "funnel"              # payload: dict
EV_SCAN_DONE = "scan_done"        # payload: list | None (None=中止/出错)
EV_SPEED_PROGRESS = "speed_progress"  # payload: (current, total, success)
EV_SPEED_DONE = "speed_done"      # payload: list（正常完成）
EV_SPEED_ABORT = "speed_abort"    # payload: None（用户中止，结果不写入历史）
EV_STATE = "state"                # payload: dict (状态快照变化)
EV_SETTINGS = "settings"          # payload: dict (设置变更，双 UI 需重新回填表单)


class EventBus:
    """线程安全的事件总线：核心工作线程 emit → Qt 桥接器 / SSE 订阅。"""

    def __init__(self):
        self._lock = threading.RLock()
        self._subscribers: Dict[str, List[Callable[[Any], None]]] = {}

    def subscribe(self, event: str, callback: Callable[[Any], None]) -> Callable[[], None]:
        """订阅事件，返回取消订阅函数。"""
        with self._lock:
            self._subscribers.setdefault(event, []).append(callback)

        def unsubscribe():
            with self._lock:
                subs = self._subscribers.get(event, [])
                if callback in subs:
                    subs.remove(callback)

        return unsubscribe

    def emit(self, event: str, payload: Any = None):
        with self._lock:
            subs = list(self._subscribers.get(event, []))
        for cb in subs:
            try:
                cb(payload)
            except Exception:
                logger.exception("事件 %s 订阅者执行失败", event)
