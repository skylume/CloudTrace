#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import time
import asyncio
import logging
import threading
from collections import deque
from typing import Any, Dict, List, Optional

from core.compat import get_event_loop_policy
from service.events import (
    EventBus, EV_LOG, EV_PROGRESS, EV_FUNNEL,
    EV_SCAN_DONE, EV_SPEED_PROGRESS, EV_SPEED_DONE, EV_SPEED_ABORT,
    EV_STATE, EV_SETTINGS,
)


logger = logging.getLogger("CloudTrace")

STAGE_IDLE = "idle"
STAGE_SCANNING = "scanning"
STAGE_TESTING = "testing"

LOG_BUFFER_SIZE = 500


class TaskManager:
    """单例任务状态机：同一进程内 Qt 窗口与 Web 面板共享同一份任务状态。"""

    _instance = None

    @classmethod
    def get(cls) -> "TaskManager":
        if cls._instance is None:
            cls._instance = cls()
        return cls._instance

    def __init__(self):
        self.bus = EventBus()
        self._lock = threading.RLock()
        self.stage = STAGE_IDLE
        self.scan_results: List[Dict] = []
        self.speed_results: List[Dict] = []
        self.funnel: Dict[str, int] = {}
        self.log_buffer: deque = deque(maxlen=LOG_BUFFER_SIZE)
        self.last_progress = (0, 0, 0, 0.0)          # completed, total, success, speed
        self.last_speed_progress = (0, 0, 0)         # current, total, 0
        self._scan_thread: Optional[threading.Thread] = None
        self._speed_thread: Optional[threading.Thread] = None
        self._scanner = None
        self._speed_task = None
        self.last_error: Optional[str] = None

    # ---------------- 状态查询 ----------------
    @property
    def busy(self) -> bool:
        return self.stage != STAGE_IDLE

    def snapshot(self) -> Dict[str, Any]:
        with self._lock:
            return {
                "stage": self.stage,
                "busy": self.busy,
                "funnel": dict(self.funnel),
                "progress": list(self.last_progress),
                "speed_progress": list(self.last_speed_progress),
                "scan_results": list(self.scan_results),
                "speed_results": list(self.speed_results),
                "log": list(self.log_buffer),
                "last_error": self.last_error,
            }

    def set_scan_results(self, results: List[Dict]):
        """外部（如历史加载）写入扫描结果，保持双 UI 状态一致。"""
        with self._lock:
            self.scan_results = list(results or [])
        self._emit_state()

    def set_speed_results(self, results: List[Dict]):
        with self._lock:
            self.speed_results = list(results or [])
        self._emit_state()

    def _emit_state(self):
        """广播完整状态快照。

        订阅方（Web 面板 / Qt 桥）都把 EV_STATE 当作「完整快照」处理，
        因此这里必须发送 snapshot() 全量数据；只发 {"stage": ...} 会让
        Web 端把缺失字段当成空值，从而清空已扫描/已测速的结果。
        """
        snap = self.snapshot()
        snap["stage"] = self.stage
        self.bus.emit(EV_STATE, snap)

    def _set_stage(self, stage: str):
        with self._lock:
            self.stage = stage
        self._emit_state()

    def _log(self, msg: str):
        with self._lock:
            self.log_buffer.append(msg)
        self.bus.emit(EV_LOG, msg)

    def _set_funnel(self, funnel: Dict[str, int]):
        with self._lock:
            self.funnel = dict(funnel)
        self.bus.emit(EV_FUNNEL, dict(funnel))

    # ---------------- 扫描任务 ----------------
    def _join_finished(self, thread_attr: str) -> bool:
        """在锁外等待旧线程退出，返回是否可以继续启动新任务。"""
        with self._lock:
            old = getattr(self, thread_attr)
        if old and old.is_alive():
            old.join(timeout=5.0)
            if old.is_alive():
                self._log("上一个任务尚未完全退出，请稍候重试")
                return False
        return True

    def start_scan(self, scanner, ip_version: int) -> bool:
        with self._lock:
            if self.stage == STAGE_SCANNING:
                logger.warning("扫描任务已在运行")
                return False
            if self.stage == STAGE_TESTING:
                logger.warning("测速任务运行中，拒绝启动扫描")
                return False
        if not self._join_finished("_scan_thread"):
            return False
        with self._lock:
            if self.stage != STAGE_IDLE:
                return False
            self._scanner = scanner
            self.scan_results = []
            self.funnel = {}
            self.last_error = None
            self.last_progress = (0, 0, 0, 0.0)
            thread = threading.Thread(
                target=self._run_scan, args=(scanner, ip_version),
                name="CloudTrace-Scan", daemon=True,
            )
            self._scan_thread = thread
            self.stage = STAGE_SCANNING

        scanner.log_callback = self._log
        scanner.progress_callback = self._on_scan_progress
        scanner.funnel_callback = self._set_funnel

        self._emit_state()
        thread.start()
        return True

    def _on_scan_progress(self, completed: int, total: int, success: int, speed: float):
        self.last_progress = (completed, total, success, speed)
        self.bus.emit(EV_PROGRESS, (completed, total, success, speed))
        if self._scanner is not None:
            self._set_funnel(self._scanner.funnel)

    def _run_scan(self, scanner, ip_version: int):
        results = None
        try:
            asyncio.set_event_loop_policy(get_event_loop_policy())
            loop = asyncio.new_event_loop()
            asyncio.set_event_loop(loop)
            try:
                results = loop.run_until_complete(scanner.run_scan_async())
            finally:
                loop.close()
        except Exception as e:
            self.last_error = str(e)
            self._log(f"{scanner.ip_label}扫描线程异常: {e}")
            logger.exception("扫描线程异常")
            results = None

        if results is not None:
            with self._lock:
                self.scan_results = results
        with self._lock:
            self._scan_thread = None
        if self.stage == STAGE_SCANNING:
            self._set_stage(STAGE_IDLE)
        self.bus.emit(EV_SCAN_DONE, results)

    # ---------------- 测速任务 ----------------
    def start_speed_test(self, speed_task) -> bool:
        with self._lock:
            if self.stage == STAGE_TESTING:
                logger.warning("测速任务已在运行")
                return False
            if self.stage == STAGE_SCANNING:
                logger.warning("扫描任务运行中，拒绝启动测速")
                return False
        if not self._join_finished("_speed_thread"):
            return False
        with self._lock:
            if self.stage != STAGE_IDLE:
                return False
            self._speed_task = speed_task
            self.speed_results = []
            self.last_speed_progress = (0, 0, 0)
            thread = threading.Thread(
                target=self._run_speed, args=(speed_task,),
                name="CloudTrace-Speed", daemon=True,
            )
            self._speed_thread = thread
            self.stage = STAGE_TESTING

        speed_task.log_callback = self._log
        speed_task.progress_callback = self._on_speed_progress

        self._emit_state()
        thread.start()
        return True

    def _on_speed_progress(self, current: int, total: int, speed: int = 0):
        self.last_speed_progress = (current, total, speed)
        self.bus.emit(EV_SPEED_PROGRESS, (current, total, speed))

    def _run_speed(self, speed_task):
        results = None
        try:
            results = speed_task.run()
        except Exception as e:
            self.last_error = str(e)
            self._log(f"测速线程异常: {e}")
            logger.exception("测速线程异常")
            results = []
        with self._lock:
            if results is not None:
                self.speed_results = results
            self._speed_thread = None
        if self.stage == STAGE_TESTING:
            self._set_stage(STAGE_IDLE)
        # 中止（None）与「正常完成但无结果」（[]）必须区分：
        # 前者不能触发「完成」语义，否则会覆盖停止提示并写入半截历史。
        if results is None:
            self.bus.emit(EV_SPEED_ABORT, None)
        else:
            self.bus.emit(EV_SPEED_DONE, results)

    # ---------------- 设置广播 ----------------
    def broadcast_settings(self, settings: Dict[str, Any]):
        """设置变更后广播，供双 UI 重新回填表单（避免互相覆盖）。"""
        self.bus.emit(EV_SETTINGS, dict(settings or {}))

    # ---------------- 停止 / 等待 ----------------
    def stop(self, wait: bool = False, timeout: float = 3.0):
        """请求停止所有任务；wait=True 时同步等待线程退出。"""
        scanner = self._scanner
        speed_task = self._speed_task
        if scanner is not None:
            scanner.stop()
        if speed_task is not None:
            speed_task.stop()
        if wait:
            self.wait(timeout)

    def wait(self, timeout: float = 5.0) -> bool:
        """等待工作线程退出，返回是否全部已退出。"""
        deadline = time.time() + timeout
        ok = True
        for attr in ("_scan_thread", "_speed_thread"):
            thread = getattr(self, attr)
            if thread and thread.is_alive():
                remain = max(0.0, deadline - time.time())
                thread.join(timeout=remain)
                if thread.is_alive():
                    ok = False
        return ok


task_manager = TaskManager.get()
