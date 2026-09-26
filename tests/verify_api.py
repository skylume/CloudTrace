#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""CloudTrace 服务层 + Web 面板接口验证（离线，不联网）。"""
import os
import io
import asyncio
import json
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)

# settings.json 是运行时文件：测试会写它（含测试用 Token），结束后原样还原
SETTINGS_PATH = os.path.join(ROOT, "settings.json")
_ORIG_SETTINGS = None
if os.path.exists(SETTINGS_PATH):
    with io.open(SETTINGS_PATH, "r", encoding="utf-8") as f:
        _ORIG_SETTINGS = f.read()


def restore_settings_file():
    if _ORIG_SETTINGS is not None:
        with io.open(SETTINGS_PATH, "w", encoding="utf-8") as f:
            f.write(_ORIG_SETTINGS)

from aiohttp.test_utils import TestClient, TestServer

from service.api import create_app
from service.task_manager import task_manager
from settings import get_settings, apply_settings, reset_settings

PASS, FAIL = [], []


def check(name, cond, detail=""):
    (PASS if cond else FAIL).append(name)
    print(("  [OK]   " if cond else "  [FAIL] ") + name + (("  -> " + str(detail)) if detail and not cond else ""))


async def main():
    reset_settings()
    app = create_app()
    client = TestClient(TestServer(app))
    await client.start_server()
    base = str(client.make_url("")).rstrip("/")

    print("\n== 1. /api/state 不回传 Token ==")
    r = await client.get("/api/state")
    st = await r.json()
    check("state 200", r.status == 200)
    check("state 不含明文 token", st["settings"].get("http_token") == "", st["settings"].get("http_token"))
    check("state 提供 http_token_set", st["settings"].get("http_token_set") is False)
    check("state 含 version", bool(st.get("version")))
    check("state 含 scan_results 数组", isinstance(st.get("scan_results"), list))

    print("\n== 2. 设置 PUT 立刻生效（共享对象） ==")
    r = await client.put("/api/settings", json={"http_port": 18080, "workers": 5000,
                                                "sample_max": 999999, "min_speed": -5,
                                                "latency_threshold": "abc"})
    body = await r.json()
    check("PUT 200", r.status == 200)
    s = get_settings()
    check("共享对象端口已更新", s["http_port"] == 18080, s["http_port"])
    check("workers 被钳制到 2000", s["workers"] == 2000, s["workers"])
    check("sample_max 被钳制到 200000", s["sample_max"] == 200000, s["sample_max"])
    check("min_speed 负数归零", s["min_speed"] == 0.0, s["min_speed"])
    check("latency_threshold 坏值回退 230", s["latency_threshold"] == 230, s["latency_threshold"])
    check("响应不回传 token", body["settings"].get("http_token") == "")

    print("\n== 3. 新增字段 speed_workers / speed_result_limit ==")
    r = await client.put("/api/settings", json={"speed_workers": 99, "speed_result_limit": 7})
    await r.json()
    check("speed_workers 钳制到 16", get_settings()["speed_workers"] == 16, get_settings()["speed_workers"])
    check("speed_result_limit 生效", get_settings()["speed_result_limit"] == 7)

    print("\n== 4. 只读字段不可覆盖 ==")
    await client.put("/api/settings", json={"http_token_set": True, "http_port": 17443})
    check("http_token_set 被忽略", get_settings().get("http_token_set") is None)

    print("\n== 5. reset 恢复默认 ==")
    r = await client.post("/api/settings/reset", json={})
    await r.json()
    check("reset 后端口回默认 17443", get_settings()["http_port"] == 17443, get_settings()["http_port"])
    check("reset 后 workers 回 200", get_settings()["workers"] == 200)

    print("\n== 6. Token 鉴权 ==")
    await client.put("/api/settings", json={"http_token": "s3cr3t"})
    r = await client.get("/api/state")
    check("无 Token -> 401", r.status == 401, r.status)
    r = await client.get("/api/state", headers={"X-Token": "wrong"})
    check("错误 Token -> 401", r.status == 401, r.status)
    r = await client.get("/api/state", headers={"X-Token": "s3cr3t"})
    check("正确 Token -> 200", r.status == 200, r.status)
    r = await client.get("/api/state?token=s3cr3t")
    check("query token 亦可 -> 200", r.status == 200, r.status)

    print("\n== 7. 导出：无结果 404 / 有结果 txt ==")
    task_manager.set_scan_results([])
    task_manager.set_speed_results([])
    r = await client.get("/api/export?type=scan&format=csv", headers={"X-Token": "s3cr3t"})
    check("空结果导出 -> 404", r.status == 404, r.status)
    r = await client.get("/api/export?type=scan&format=xml", headers={"X-Token": "s3cr3t"})
    check("非法格式 -> 400", r.status == 400, r.status)

    task_manager.set_scan_results([
        {"ip": "1.2.3.4", "latency": 88.5, "iata_code": "HKG", "chinese_name": "中国香港",
         "port": 443, "use_tls": True, "scan_mode": "tcping", "scan_time": "2026-01-01 00:00:00",
         "ip_version": 4},
        {"ip": "2606:4700::1111", "latency": 120.0, "iata_code": "NRT", "chinese_name": "日本",
         "port": 8443, "use_tls": True, "scan_mode": "tcping", "scan_time": "2026-01-01 00:00:00",
         "ip_version": 6},
    ])
    r = await client.get("/api/export?type=scan&format=csv", headers={"X-Token": "s3cr3t"})
    txt = await r.text()
    check("CSV 导出 200", r.status == 200, r.status)
    check("CSV 带 BOM（Excel 友好）", txt.startswith("\ufeff"), repr(txt[:4]))
    check("CSV 含表头 IP地址", "IP地址" in txt)

    r = await client.get("/api/export?type=scan&format=txt", headers={"X-Token": "s3cr3t"})
    body = await r.text()
    check("TXT 导出 200", r.status == 200, r.status)
    check("TXT 输出 ip:port", "1.2.3.4:443" in body, repr(body))
    check("TXT IPv6 自动加方括号", "[2606:4700::1111]:8443" in body, repr(body))

    r = await client.get("/api/export?type=scan&format=json&fields=ip,latency",
                         headers={"X-Token": "s3cr3t"})
    j = json.loads(await r.text())
    check("JSON 字段裁剪生效", j["fields"] == ["ip", "latency"], j["fields"])

    print("\n== 8. 测速启动校验 ==")
    r = await client.post("/api/speed/start", json={"scope": "bogus"}, headers={"X-Token": "s3cr3t"})
    check("非法 scope -> 400", r.status == 400, r.status)
    task_manager.set_scan_results([])
    r = await client.post("/api/speed/start", json={"scope": "all"}, headers={"X-Token": "s3cr3t"})
    check("无扫描结果测速 -> 409", r.status == 409, r.status)

    print("\n== 9. 扫描参数校验 ==")
    r = await client.post("/api/scan/start", json={"ip_version": 9}, headers={"X-Token": "s3cr3t"})
    check("ip_version=9 -> 400", r.status == 400, r.status)
    r = await client.post("/api/scan/start", json={"source_mode": "乱写"}, headers={"X-Token": "s3cr3t"})
    check("非法 source_mode -> 400", r.status == 400, r.status)
    r = await client.post("/api/scan/start", json={"source_mode": "仅自定义", "cidrs": []},
                          headers={"X-Token": "s3cr3t"})
    check("空 CIDR -> 400", r.status == 400, r.status)
    r = await client.post("/api/scan/start",
                          json={"source_mode": "非标列表", "import_text": "not-an-ip!!!"},
                          headers={"X-Token": "s3cr3t"})
    check("非法非标列表 -> 400", r.status == 400, r.status)
    r = await client.post("/api/scan/start", json={"source_mode": "仅自定义",
                                                   "cidrs": ["999.999.0.0/24"]},
                          headers={"X-Token": "s3cr3t"})
    check("非法 CIDR -> 400", r.status == 400, r.status)
    r = await client.post("/api/scan/start", json={"ip_version": 6, "source_mode": "仅自定义",
                                                   "cidrs": ["1.2.3.0/24"]},
                          headers={"X-Token": "s3cr3t"})
    check("IPv6 模式 + IPv4 CIDR -> 400", r.status == 400, r.status)

    print("\n== 9b. IP 来源多形态（CIDR / IP / IP:port / IP 段 / 版本不符） ==")
    r = await client.post("/api/scan/start",
                          json={"ip_version": 4, "source_mode": "仅自定义",
                                "source_text": "2606:4700::/32"},
                          headers={"X-Token": "s3cr3t"})
    txt = await r.text()
    check("IPv4 模式喂 IPv6 来源 -> 400", r.status == 400, r.status)
    check("提示与所选 IP 版本不符", "IP 版本" in txt, txt)

    r = await client.post("/api/scan/start",
                          json={"source_mode": "仅自定义", "source_text": "not-an-ip!!!"},
                          headers={"X-Token": "s3cr3t"})
    check("多形态框里的非法行 -> 400", r.status == 400, r.status)

    # 合法多形态：网段 + 单 IP + IP:port + IP 段（含自定义端口）应被接受并成功启动
    r = await client.post("/api/scan/start",
                          json={"source_mode": "仅自定义", "ip_version": 4,
                                "source_text": "127.0.0.0/30\n127.0.0.5\n127.0.0.6:1\n127.0.0.8-127.0.0.9",
                                "port": 1, "workers": 4, "sample_max": 100,
                                "threshold": 2000, "ping_times": 0},
                          headers={"X-Token": "s3cr3t"})
    check("多形态来源可启动扫描", r.status == 200, (r.status, await r.text()))
    await asyncio.sleep(0.2)
    task_manager.stop(wait=True, timeout=10.0)
    check("扫描结束回到 idle", task_manager.stage == "idle", task_manager.stage)

    # 旧版「非标列表」应平滑迁移为「仅自定义」，不再被判为非法 source_mode
    r = await client.post("/api/scan/start",
                          json={"source_mode": "非标列表", "ip_version": 4,
                                "import_text": "127.0.0.1", "port": 1,
                                "workers": 2, "sample_max": 10, "threshold": 2000},
                          headers={"X-Token": "s3cr3t"})
    check("旧版「非标列表」仍可用", r.status == 200, (r.status, await r.text()))
    await asyncio.sleep(0.2)
    task_manager.stop(wait=True, timeout=10.0)

    print("\n== 9c. /api/sources/preview（远程数据源拉取预览） ==")
    r = await client.post("/api/sources/preview", json={"remote_sources": []},
                          headers={"X-Token": "s3cr3t"})
    j = await r.json()
    check("空数据源 -> ok=False", r.status == 200 and j.get("ok") is False, j)

    r = await client.post("/api/sources/preview",
                          json={"remote_sources": [
                              {"name": "x", "url": "https://a.example/x", "enabled": False}]},
                          headers={"X-Token": "s3cr3t"})
    j = await r.json()
    check("全部禁用 -> ok=False 且说明原因",
          j.get("ok") is False and "启用" in (j.get("error") or ""), j)

    r = await client.post("/api/sources/preview",
                          json={"remote_sources": [
                              {"name": "bad", "url": "not-a-url", "enabled": True}]},
                          headers={"X-Token": "s3cr3t"})
    j = await r.json()
    check("非法 URL 被 sanitize 掉 -> ok=False", j.get("ok") is False, j)

    r = await client.post("/api/sources/preview",
                          json={"remote_sources": [
                              {"name": "local", "url": "http://127.0.0.1:9/none", "enabled": True}],
                              "port": 443, "source_timeout": 1.0,
                              "source_retries": 1, "source_retry_delay": 0},
                          headers={"X-Token": "s3cr3t"})
    j = await r.json()
    check("不可达源 -> 200 且返回 report",
          r.status == 200 and isinstance(j.get("report"), list), j)

    print("\n== 10. 历史接口 ==")
    r = await client.get("/api/history?type=bogus", headers={"X-Token": "s3cr3t"})
    check("非法 type -> 400", r.status == 400, r.status)
    r = await client.get("/api/history?ipver=9", headers={"X-Token": "s3cr3t"})
    check("非法 ipver -> 400", r.status == 400, r.status)
    r = await client.get("/api/history?type=scan&ipver=4", headers={"X-Token": "s3cr3t"})
    check("正常历史列表 200", r.status == 200, r.status)
    r = await client.post("/api/history/load", json={"filepath": "../../../etc/passwd"},
                          headers={"X-Token": "s3cr3t"})
    check("路径穿越被拒 -> 400", r.status == 400, r.status)

    print("\n== 11. SSE settings 事件不携带明文 Token ==")
    sse = await client.get("/api/events?token=s3cr3t")
    check("SSE 200", sse.status == 200)
    await sse.content.read(64)  # 消费首个 state 事件
    await client.put("/api/settings", json={"http_port": 17443}, headers={"X-Token": "s3cr3t"})
    got = b""
    for _ in range(40):
        chunk = await asyncio.wait_for(sse.content.read(256), timeout=2)
        if not chunk:
            break
        got += chunk
        if b"event: settings" in got:
            break
    sse.close()
    check("收到 settings 事件", b"event: settings" in got, got[-200:])
    check("settings 事件不含明文 token", b"s3cr3t" not in got, got[-300:])

    print("\n== 12. 静态资源 ==")
    r = await client.get("/")
    check("index.html 200", r.status == 200, r.status)
    r = await client.get("/static/app.js")
    check("app.js 200", r.status == 200, r.status)
    r = await client.get("/static/style.css")
    check("style.css 200", r.status == 200, r.status)

    await client.close()

    print("\n" + "=" * 56)
    print(f"通过 {len(PASS)} / 失败 {len(FAIL)}")
    restore_settings_file()
    if FAIL:
        print("失败项: " + ", ".join(FAIL))
        return 1
    print("全部通过 ✓")
    return 0


if __name__ == "__main__":
    raise SystemExit(asyncio.get_event_loop().run_until_complete(main()))
