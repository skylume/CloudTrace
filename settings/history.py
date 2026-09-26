#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import os
import json
import logging
from datetime import datetime
from typing import List, Dict, Optional

from settings.settings import SAVE_DIR
from core.utils import atomic_write_json, atomic_write_bytes


logger = logging.getLogger("CloudTrace")

MAX_HISTORY = 5

IPV4_SCAN_FILE = os.path.join(SAVE_DIR, "ipv4_scan_latest.json")
IPV6_SCAN_FILE = os.path.join(SAVE_DIR, "ipv6_scan_latest.json")
IPV4_SPEED_FILE = os.path.join(SAVE_DIR, "ipv4_speed_latest.json")
IPV6_SPEED_FILE = os.path.join(SAVE_DIR, "ipv6_speed_latest.json")


def ensure_save_dir():
    os.makedirs(SAVE_DIR, exist_ok=True)
    _cleanup_legacy_files()
    _cleanup_all_types()


def save_results_to_file(results: List[Dict], ip_version: int, result_type: str) -> bool:
    ensure_save_dir()
    now = datetime.now()
    timestamp = now.strftime("%Y%m%d_%H%M%S")
    ip_label = "ipv4" if ip_version == 4 else "ipv6"
    type_label = result_type

    history_file = os.path.join(SAVE_DIR, f"{ip_label}_{type_label}_{timestamp}.json")

    save_data = {
        'save_time': now.strftime("%Y-%m-%d %H:%M:%S"),
        'ip_version': ip_version,
        'result_type': result_type,
        'count': len(results),
        'results': results
    }

    try:
        # 原子写：进程崩溃也不会留下半截 JSON 导致历史页报「文件损坏」
        atomic_write_json(history_file, save_data)

        if result_type == "scan":
            latest_file = IPV4_SCAN_FILE if ip_version == 4 else IPV6_SCAN_FILE
        else:
            latest_file = IPV4_SPEED_FILE if ip_version == 4 else IPV6_SPEED_FILE
        with open(history_file, 'rb') as f:
            atomic_write_bytes(latest_file, f.read())

        _cleanup_by_prefix(ip_label, type_label)
        return True
    except Exception as e:
        logger.error("保存失败: %s", e)
        return False


def _cleanup_by_prefix(ip_label: str, type_label: str):
    prefix = f"{ip_label}_{type_label}_"
    timestamped_files = []

    try:
        all_files = os.listdir(SAVE_DIR)
    except Exception as e:
        logger.warning("无法列出目录 %s: %s", SAVE_DIR, e)
        return

    for f in all_files:
        if f.startswith(prefix) and f.endswith(".json") and "latest" not in f:
            full_path = os.path.join(SAVE_DIR, f)
            if os.path.isfile(full_path):
                timestamped_files.append(full_path)

    total = len(timestamped_files)
    if total <= MAX_HISTORY:
        return

    timestamped_files.sort(key=lambda x: os.path.basename(x), reverse=True)
    to_delete = timestamped_files[MAX_HISTORY:]

    deleted = 0
    for old_file in to_delete:
        try:
            os.remove(old_file)
            deleted += 1
            logger.debug("已删除: %s", os.path.basename(old_file))
        except Exception as e:
            logger.warning("删除失败 %s: %s", os.path.basename(old_file), e)

    logger.info("%s_%s: 共%d份, 保留%d份, 删除%d份", ip_label, type_label, total, MAX_HISTORY, deleted)


def _cleanup_all_types():
    for ip_label in ("ipv4", "ipv6"):
        for type_label in ("scan", "speed"):
            _cleanup_by_prefix(ip_label, type_label)


def _cleanup_legacy_files():
    try:
        all_files = os.listdir(SAVE_DIR)
    except Exception as e:
        logger.warning("无法列出目录: %s", e)
        return

    for f in all_files:
        is_legacy = False
        for ip in ("ipv4", "ipv6"):
            if f.startswith(f"{ip}_") and "scan" not in f and "speed" not in f:
                is_legacy = True
                break

        if is_legacy and f.endswith(".json"):
            full_path = os.path.join(SAVE_DIR, f)
            try:
                if os.path.isfile(full_path):
                    os.remove(full_path)
                    logger.debug("已删除旧格式: %s", f)
            except Exception as e:
                logger.warning("删除旧格式失败 %s: %s", f, e)


def delete_history(filepath: str) -> bool:
    """删除一条历史记录（限制在 SAVE_DIR 内，防止误删任意文件）。"""
    try:
        real = os.path.realpath(filepath)
        base = os.path.realpath(SAVE_DIR)
        if not real.startswith(base + os.sep):
            logger.warning("拒绝删除保存目录之外的文件: %s", filepath)
            return False
        if not os.path.isfile(real):
            return False
        os.remove(real)
        logger.info("已删除历史记录: %s", os.path.basename(real))
        return True
    except Exception as e:
        logger.error("删除历史记录失败: %s", e)
        return False


def load_results_from_file(filepath: str) -> Optional[Dict]:
    try:
        if not os.path.exists(filepath):
            return None
        with open(filepath, 'r', encoding='utf-8') as f:
            data = json.load(f)
        if 'results' in data and isinstance(data['results'], list):
            return data
        return None
    except Exception as e:
        logger.error("加载失败: %s", e)
        return None


def get_history_list(ip_version: int, result_type: str) -> List[Dict]:
    ensure_save_dir()
    ip_label = "ipv4" if ip_version == 4 else "ipv6"
    type_label = result_type
    prefix = f"{ip_label}_{type_label}_"
    history = []

    try:
        all_files = os.listdir(SAVE_DIR)
    except Exception:
        return history

    for f in sorted(all_files, reverse=True):
        if f.startswith(prefix) and f.endswith(".json") and "latest" not in f:
            filepath = os.path.join(SAVE_DIR, f)
            try:
                with open(filepath, 'r', encoding='utf-8') as fp:
                    data = json.load(fp)
                history.append({
                    'filename': f,
                    'filepath': filepath,
                    'save_time': data.get('save_time', '未知'),
                    'count': data.get('count', 0),
                })
            except Exception:
                continue

    return history
