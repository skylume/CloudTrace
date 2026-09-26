#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""单实例锁。

为什么需要：双开会让两个进程抢同一个 HTTP 端口（17443/18543）与
`CloudTrace_history/` 目录，导致端口占用报错、历史文件互相覆盖。

实现：`.run.lock` 文件锁（Windows 用 msvcrt.locking 非阻塞独占锁，
类 Unix 用 fcntl.flock）。锁随进程退出自动释放，并额外用 atexit 兜底。
设置环境变量 `CLOUDTRACE_ALLOW_MULTI=1` 可跳过检查（供调试/测试使用）。
"""

import os
import sys
import atexit
import logging
from typing import Optional

from core.constants import APP_DIR


logger = logging.getLogger("CloudTrace")

LOCK_FILE = os.path.join(APP_DIR, ".run.lock")
ALLOW_MULTI_ENV = "CLOUDTRACE_ALLOW_MULTI"

_handle = None


def _try_lock(handle) -> bool:
    """尝试获取非阻塞独占锁；成功返回 True。"""
    try:
        if sys.platform == "win32":
            import msvcrt
            handle.seek(0)
            msvcrt.locking(handle.fileno(), msvcrt.LK_NBLCK, 1)
        else:
            import fcntl
            fcntl.flock(handle.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        return True
    except OSError:
        return False
    except Exception:
        # 平台能力缺失时不阻塞启动
        logger.debug("单实例锁不可用，跳过", exc_info=True)
        return True


def _release():
    global _handle
    handle = _handle
    _handle = None
    if handle is None:
        return
    try:
        if sys.platform == "win32":
            import msvcrt
            handle.seek(0)
            msvcrt.locking(handle.fileno(), msvcrt.LK_UNLCK, 1)
        else:
            import fcntl
            fcntl.flock(handle.fileno(), fcntl.LOCK_UN)
    except Exception:
        pass
    finally:
        try:
            handle.close()
        except Exception:
            pass


def acquire_single_instance() -> bool:
    """获取单实例锁。

    返回 True 表示可以继续启动；False 表示已有实例在运行。
    """
    global _handle
    if os.environ.get(ALLOW_MULTI_ENV) == "1":
        logger.info("已设置 %s=1，跳过单实例检查", ALLOW_MULTI_ENV)
        return True

    try:
        handle = open(LOCK_FILE, "a+")
    except Exception:
        logger.warning("无法创建锁文件 %s，跳过单实例检查", LOCK_FILE)
        return True

    if not _try_lock(handle):
        try:
            handle.close()
        except Exception:
            pass
        return False

    # 写入 pid 便于排查（内容非锁本身，失败不影响）
    try:
        handle.seek(0)
        handle.truncate()
        handle.write(str(os.getpid()))
        handle.flush()
    except Exception:
        pass

    _handle = handle
    atexit.register(_release)
    return True


def release_single_instance():
    _release()
