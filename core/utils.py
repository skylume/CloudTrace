#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""通用工具：安全类型转换 + 原子写文件。

为什么需要这个模块：
- `settings.json` 是用户可手工编辑的文件，一旦写入 `null` / 字符串，
  `int(None)` 会让主窗口 `_build_ui` 阶段直接抛 `TypeError` 而启动失败。
  所有读配置的地方都应经过 `to_int` / `to_float` 兜底。
- 直接 `open(path, 'w')` 覆写，进程在写入中途崩溃会留下半截 JSON，
  历史页再读取即报「文件损坏」。原子写（临时文件 + os.replace）保证
  要么是完整旧内容、要么是完整新内容。
"""

import os
import json
import tempfile
from typing import Any, Optional


__all__ = [
    "to_int", "to_float", "to_bool",
    "atomic_write_bytes", "atomic_write_text", "atomic_write_json",
    "safe_json_load",
]


def to_int(value: Any, default: int = 0,
           minimum: Optional[int] = None, maximum: Optional[int] = None) -> int:
    """尽力把 value 转成 int；失败时返回 default，并可选钳制到 [minimum, maximum]。"""
    try:
        if value is None or (isinstance(value, str) and not value.strip()):
            raise ValueError("empty")
        result = int(float(value))
    except (TypeError, ValueError, OverflowError):
        result = default
    if minimum is not None and result < minimum:
        result = minimum
    if maximum is not None and result > maximum:
        result = maximum
    return result


def to_float(value: Any, default: float = 0.0,
             minimum: Optional[float] = None, maximum: Optional[float] = None) -> float:
    """尽力把 value 转成 float；失败时返回 default，并可选钳制到 [minimum, maximum]。"""
    try:
        if value is None or (isinstance(value, str) and not value.strip()):
            raise ValueError("empty")
        result = float(value)
        # NaN / ±Inf 都不是可用配置：Python 的 json.load 默认接受 Infinity/NaN
        # 这类非标准字面量，若不拦下来，min_speed=inf 会把所有节点筛掉。
        if result != result or result in (float("inf"), float("-inf")):
            raise ValueError("non-finite")
    except (TypeError, ValueError, OverflowError):
        result = default
    if minimum is not None and result < minimum:
        result = minimum
    if maximum is not None and result > maximum:
        result = maximum
    return result


def to_bool(value: Any, default: bool = False) -> bool:
    """把常见真值表示转成 bool（兼容 JSON 里的字符串 "true"/"0"）。"""
    if isinstance(value, bool):
        return value
    if value is None:
        return default
    if isinstance(value, (int, float)):
        return value != 0
    if isinstance(value, str):
        text = value.strip().lower()
        if text in ("1", "true", "yes", "on", "y", "t"):
            return True
        if text in ("0", "false", "no", "off", "n", "f", ""):
            return False
    return default


def atomic_write_bytes(path: str, data: bytes) -> None:
    """原子写二进制：写临时文件 → fsync → os.replace 覆盖目标。"""
    target = os.path.abspath(path)
    directory = os.path.dirname(target) or "."
    os.makedirs(directory, exist_ok=True)
    fd, tmp_path = tempfile.mkstemp(prefix=".ct_tmp_", suffix=".part", dir=directory)
    try:
        with os.fdopen(fd, "wb") as f:
            f.write(data)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp_path, target)
    except BaseException:
        try:
            os.remove(tmp_path)
        except OSError:
            pass
        raise


def atomic_write_text(path: str, text: str, encoding: str = "utf-8") -> None:
    atomic_write_bytes(path, text.encode(encoding))


def atomic_write_json(path: str, data: Any, indent: int = 2) -> None:
    """原子写 JSON（ensure_ascii=False，便于人工查看中文）。"""
    payload = json.dumps(data, ensure_ascii=False, indent=indent, default=str)
    atomic_write_text(path, payload)


def safe_json_load(path: str) -> Optional[Any]:
    """读取 JSON；文件缺失或损坏时返回 None，不抛异常。"""
    try:
        with open(path, "r", encoding="utf-8") as f:
            return json.load(f)
    except Exception:
        return None
