#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""CloudTrace 纯 HTTP 面板入口（无 Qt 依赖）。

用法:
    python serve.py                 # 按 settings.json 启动
    python serve.py --port 18080    # 指定端口
    python serve.py --host 0.0.0.0  # 允许局域网访问
    python serve.py --token abc123  # 设置访问 Token
"""

import sys
import time
import argparse
import logging

logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
)

from core.compat import IS_WIN7  # noqa: F401  (触发 Win7 环境适配)

if IS_WIN7:
    try:
        import asyncio
        asyncio.set_event_loop_policy(asyncio.WindowsSelectorEventLoopPolicy())
    except AttributeError:
        pass

from core.constants import get_version
from settings import load_settings, save_settings
from service.task_manager import task_manager
from service.http_server import http_server


def main() -> int:
    parser = argparse.ArgumentParser(description="CloudTrace HTTP 面板")
    parser.add_argument("--host", default=None, help="监听地址（默认 127.0.0.1，allow_lan 时 0.0.0.0）")
    parser.add_argument("--port", type=int, default=None, help="监听端口（默认 17443）")
    parser.add_argument("--token", default=None, help="访问 Token（写入设置）")
    args = parser.parse_args()

    # 单实例：避免与桌面版/另一个面板进程抢端口与历史目录
    from core.single_instance import acquire_single_instance
    if not acquire_single_instance():
        print("[错误] 检测到另一个 CloudTrace 实例正在运行；"
              "如需强制多开，请设置环境变量 CLOUDTRACE_ALLOW_MULTI=1。")
        return 1

    settings = load_settings()
    dirty = False
    if args.token is not None and args.token != settings.get("http_token", ""):
        settings["http_token"] = args.token
        dirty = True
    if not settings.get("http_enabled", True):
        settings["http_enabled"] = True
        dirty = True

    host = args.host or ("0.0.0.0" if settings.get("allow_lan") else "127.0.0.1")
    if host != "127.0.0.1":
        settings["allow_lan"] = host in ("0.0.0.0", "::")
        dirty = True
    port = args.port or int(settings.get("http_port", 17443))
    if port != settings.get("http_port", 17443):
        settings["http_port"] = port
        dirty = True
    if dirty:
        save_settings(settings)

    print(f"CloudTrace 云迹 v{get_version()} · HTTP 面板")
    if not http_server.start(host, port):
        print(f"[错误] 启动失败: {http_server.error}")
        return 1

    shown = "127.0.0.1" if host in ("0.0.0.0", "::") else host
    print(f"  面板地址: http://{shown}:{port}/")
    if settings.get("http_token"):
        print(f"  访问 Token: {settings['http_token']}")
    else:
        print("  未设置 Token（仅建议本机使用，可用 --token 设置）")
    print("  Ctrl+C 退出")

    try:
        while True:
            time.sleep(3600)
    except KeyboardInterrupt:
        print("\n正在退出...")
    finally:
        task_manager.stop(wait=True, timeout=3.0)
        http_server.stop()
    return 0


if __name__ == "__main__":
    sys.exit(main())
