#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""CloudTrace 核心逻辑单元测试（纯离线，不发起任何网络请求）。"""
import os
import sys
import json
import shutil
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)

PASS, FAIL = [], []


def check(name, cond, detail=""):
    (PASS if cond else FAIL).append(name)
    print(("  [OK]   " if cond else "  [FAIL] ") + name + (("  -> " + str(detail)) if detail and not cond else ""))


print("== 1. 类型兜底 to_int / to_float / to_bool（缺陷 3.8） ==")
from core.utils import to_int, to_float, to_bool, atomic_write_json, atomic_write_text, safe_json_load

check("to_int(None) -> 默认", to_int(None, 7) == 7, to_int(None, 7))
check("to_int('abc') -> 默认", to_int("abc", 7) == 7, to_int("abc", 7))
check("to_int('42') -> 42", to_int("42", 0) == 42, to_int("42", 0))
check("to_int(5000, max=2000) -> 2000", to_int(5000, 0, None, 2000) == 2000, to_int(5000, 0, None, 2000))
check("to_int(-5, min=0) -> 0", to_int(-5, 0, 0, None) == 0, to_int(-5, 0, 0, None))
check("to_int(float('nan')) -> 默认", to_int(float("nan"), 9) == 9, to_int(float("nan"), 9))
check("to_int([], 3) -> 3（不抛异常）", to_int([], 3) == 3)
check("to_float('abc', 1.5) -> 1.5", to_float("abc", 1.5) == 1.5, to_float("abc", 1.5))
check("to_float(-3, 0, min=0) -> 0.0", to_float(-3, 0, 0.0, None) == 0.0, to_float(-3, 0, 0.0, None))
check("to_float('inf', 1.0) -> 1.0", to_float("inf", 1.0) == 1.0, to_float("inf", 1.0))
check("to_float('-inf', 2.0) -> 2.0", to_float("-inf", 2.0) == 2.0, to_float("-inf", 2.0))
check("to_float(float('inf'), 3.0) -> 3.0", to_float(float("inf"), 3.0) == 3.0, to_float(float("inf"), 3.0))
check("to_bool('false') -> False", to_bool("false", True) is False)
check("to_bool('true') -> True", to_bool("true", False) is True)
check("to_bool(0) -> False", to_bool(0, True) is False)
check("to_bool(None, True) -> True", to_bool(None, True) is True)

print("\n== 2. 原子写 / 安全读 ==")
tmpdir = tempfile.mkdtemp(prefix="ct_")
try:
    p = os.path.join(tmpdir, "a.json")
    atomic_write_json(p, {"中文": "值", "n": 1})
    check("原子写 JSON 可读回", safe_json_load(p) == {"中文": "值", "n": 1}, safe_json_load(p))
    check("写入后无残留临时文件", not [f for f in os.listdir(tmpdir) if f != "a.json"], os.listdir(tmpdir))
    tp = os.path.join(tmpdir, "b.txt")
    atomic_write_text(tp, "hello 世界")
    check("原子写文本", open(tp, encoding="utf-8").read() == "hello 世界")
    bad = os.path.join(tmpdir, "bad.json")
    open(bad, "w", encoding="utf-8").write("{ not json")
    check("损坏 JSON -> None（不抛）", safe_json_load(bad) is None)
    check("不存在文件 -> None", safe_json_load(os.path.join(tmpdir, "nope.json")) is None)
finally:
    shutil.rmtree(tmpdir, ignore_errors=True)

print("\n== 3. parse_speed_url 端口/TLS 推断 ==")
from core.speed_url import parse_speed_url
h, path, tls = parse_speed_url("speed.cloudflare.com/__down?bytes=99999999")
check("无 scheme 默认 TLS", tls is True, tls)
check("host 解析正确", h == "speed.cloudflare.com", h)
check("path 保留 query", path == "/__down?bytes=99999999", path)
h, path, tls = parse_speed_url("http://example.com:8080/x")
check("http:// -> 非 TLS", tls is False, tls)
check("显式端口 8080", h == "example.com:8080", h)
h, path, tls = parse_speed_url("https://a.b.c/")
check("https:// -> TLS", tls is True, tls)
check("根路径自动补 bytes= 参数", path.startswith("/") and "bytes=" in path, path)
h, path, tls = parse_speed_url("")
check("空串回退默认 host", bool(h), h)
check("空串 path 以 / 开头", path.startswith("/"), path)

print("\n== 4. parse_ip_list（非标导入，缺陷 3.7） ==")
from core.importer import parse_ip_list
entries, errs = parse_ip_list("1.2.3.4\n5.6.7.8 8443\n", default_port=443, resolve_domains=False)
check("两行均解析成功", len(entries) == 2 and not errs, (entries, errs))
check("默认端口生效", entries[0]["port"] == 443, entries[0])
check("空格端口生效", entries[1]["port"] == 8443, entries[1])
check("无 scheme 默认 use_tls=False（非标默认非 TLS）",
      entries[0].get("use_tls") in (False, None), entries[0])

entries, errs = parse_ip_list("[2606:4700::1111]:8443", default_port=443, resolve_domains=False)
check("[v6]:port 解析", len(entries) == 1 and entries[0]["port"] == 8443, (entries, errs))
check("IPv6 地址正确", entries[0]["ip"] == "2606:4700::1111", entries[0])

entries, errs = parse_ip_list("1.2.3.4 # 注释\n# 整行注释\n\n", default_port=443, resolve_domains=False)
check("# 行尾注释与空行被忽略", len(entries) == 1 and not errs, (entries, errs))

entries, errs = parse_ip_list("https://example.com:8443/path", default_port=443, resolve_domains=False)
check("https:// + 路径 + 端口 -> 归入错误（需 DNS，未开启）", len(entries) == 0 and len(errs) == 1, (entries, errs))

entries, errs = parse_ip_list("999.1.1.1", default_port=443, resolve_domains=False)
check("非法 IP 报错而非静默丢弃", len(errs) == 1 and not entries, (entries, errs))

entries, errs = parse_ip_list("1.2.3.4\n1.2.3.4", default_port=443, resolve_domains=False)
check("重复项去重", len(entries) == 1, entries)

print("\n== 5. parse_cidr_lines / 端口 TLS 判定 ==")
from core.factory import parse_cidr_lines
from core.network import port_uses_tls, HTTPS_PORTS
valid, bad = parse_cidr_lines(["1.2.3.0/24", "2606:4700::/32", "垃圾"], 4)
check("IPv4 模式只保留 v4 CIDR", valid == ["1.2.3.0/24"], valid)
check("仅非 CIDR 计入错误（版本不符静默过滤）", len(bad) == 1 and bad[0][1] == "垃圾", bad)
valid, bad = parse_cidr_lines(["2606:4700::/32"], 6)
check("IPv6 模式接受 v6 CIDR", len(valid) == 1 and not bad, (valid, bad))
check("443 走 TLS", port_uses_tls(443) is True)
check("8443 走 TLS", port_uses_tls(8443) is True)
check("80 不走 TLS", port_uses_tls(80) is False)
check("HTTPS_PORTS 含 2053/2087/2096", {2053, 2087, 2096} <= set(HTTPS_PORTS), HTTPS_PORTS)

print("\n== 6. 导出渲染（CSV / JSON / TXT） ==")
from core.export import render_export, render_txt, write_export, EXPORT_FORMATS
scan = [
    {"ip": "1.2.3.4", "latency": 88.5, "iata_code": "HKG", "chinese_name": "中国香港",
     "port": 443, "use_tls": True, "scan_mode": "tcping", "scan_time": "2026-01-01 00:00:00",
     "ip_version": 4},
    {"ip": "2606:4700::1111", "latency": 120.0, "iata_code": "NRT", "chinese_name": "日本",
     "port": 8443, "use_tls": True, "scan_mode": "tcping", "scan_time": "2026-01-01 00:00:00",
     "ip_version": 6},
]
speed = [
    {"ip": "1.2.3.4", "latency": 88.5, "download_speed": 12.0, "score": 9.0,
     "iata_code": "HKG", "chinese_name": "中国香港", "port": 443, "test_type": "完全测速", "verified": True},
    {"ip": "5.6.7.8", "latency": 150.0, "download_speed": 2.0, "score": 1.0,
     "iata_code": "NRT", "chinese_name": "日本", "port": 443, "test_type": "完全测速", "verified": True},
]
check("EXPORT_FORMATS 含三种", set(EXPORT_FORMATS) == {"csv", "json", "txt"}, EXPORT_FORMATS)
csv_txt = render_export(scan, "scan", fmt="csv")
check("CSV 含表头与数据行", "IP地址" in csv_txt and "1.2.3.4" in csv_txt)
json_txt = render_export(scan, "scan", fields=["ip", "latency"], fmt="json")
j = json.loads(json_txt)
check("JSON 字段裁剪", j["fields"] == ["ip", "latency"], j["fields"])
check("JSON count 正确", j["count"] == 2, j["count"])
txt = render_txt(scan, "scan")
check("TXT ip:port", "1.2.3.4:443" in txt, txt)
check("TXT IPv6 加方括号", "[2606:4700::1111]:8443" in txt, txt)
txt_q = render_export(speed, "speed", qualified_only=True, min_speed=5.0, fmt="txt")
check("qualified_only 只留合格节点", "1.2.3.4:443" in txt_q and "5.6.7.8" not in txt_q, txt_q)

tmpdir = tempfile.mkdtemp(prefix="ct_exp_")
try:
    out = os.path.join(tmpdir, "x.txt")
    n = write_export(out, scan, "scan", fmt="txt")
    check("write_export 返回条数", n == 2, n)
    check("显式 fmt 覆盖扩展名（txt 内容）", "1.2.3.4:443" in open(out, encoding="utf-8").read())
    out2 = os.path.join(tmpdir, "y.json")
    write_export(out2, scan, "scan")
    check("无 fmt 时按 .json 后缀推断", json.loads(open(out2, encoding="utf-8").read())["result_type"] == "scan")
finally:
    shutil.rmtree(tmpdir, ignore_errors=True)

print("\n== 7. region_stats 聚合 ==")
from core.analytics import region_stats
stats = region_stats(scan)
check("返回列表", isinstance(stats, list), type(stats))
codes = {s["code"] for s in stats}
check("含 HKG / NRT", {"HKG", "NRT"} <= codes, codes)
check("计数正确", all(s["count"] == 1 for s in stats), stats)

print("\n== 8. 设置规范化 sanitize_settings ==")
from settings import sanitize_settings, DEFAULT_SETTINGS
s = sanitize_settings({"workers": "9999", "sample_max": None, "scan_mode": "乱写",
                       "cidr_mode": "不存在", "min_speed": -1, "speed_workers": 100,
                       "speed_result_limit": -5, "http_port": 70000, "unknown_key": "保留"})
check("workers 钳制 2000", s["workers"] == 2000, s["workers"])
check("sample_max 坏值回默认", s["sample_max"] == 5000, s["sample_max"])
check("scan_mode 非法回 tcping", s["scan_mode"] == "tcping", s["scan_mode"])
check("cidr_mode 非法回仅官方", s["cidr_mode"] == "仅官方", s["cidr_mode"])
check("min_speed 负数归零", s["min_speed"] == 0.0, s["min_speed"])
check("speed_workers 钳制 16", s["speed_workers"] == 16, s["speed_workers"])
check("speed_result_limit 负数归零", s["speed_result_limit"] == 0, s["speed_result_limit"])
check("http_port 越界被钳制到 65535", s["http_port"] == 65535, s["http_port"])
check("未知键保留（向后兼容）", s.get("unknown_key") == "保留")
check("默认端口为 17443（缺陷 3.11）", DEFAULT_SETTINGS["http_port"] == 17443, DEFAULT_SETTINGS["http_port"])
check("默认含 speed_workers", "speed_workers" in DEFAULT_SETTINGS)
check("默认含 speed_result_limit", "speed_result_limit" in DEFAULT_SETTINGS)

print("\n== 9. 配置体检 validate_settings ==")
from service.health import validate_settings
w = validate_settings({"workers": 999, "latency_threshold": 20, "sample_max": 100000,
                       "score_speed_weight": 0, "score_latency_weight": 0,
                       "speed_workers": 9, "http_enabled": True, "allow_lan": True,
                       "http_token": "", "http_port": 17443, "min_speed": 0})
joined = " | ".join(w)
check("检出并发过高", "并发数" in joined, joined)
check("检出阈值过低", "延迟阈值" in joined, joined)
check("检出采样上限过大", "采样上限" in joined, joined)
check("检出权重全 0", "评分权重" in joined, joined)
check("检出测速并发过高", "测速并发" in joined, joined)
check("检出局域网无 Token", "Token" in joined, joined)
w2 = validate_settings(dict(DEFAULT_SETTINGS))
check("全默认配置无警告", w2 == [], w2)
check("坏类型不抛异常", isinstance(validate_settings({"workers": None, "http_port": "x"}), list))

print("\n== 10. 历史保存 / 读取 / 列表 / 删除 往返 ==")
from settings import history as hist
from settings.settings import SAVE_DIR
os.makedirs(SAVE_DIR, exist_ok=True)
before = set(os.listdir(SAVE_DIR))
saved_path = None
try:
    hist.save_results_to_file(scan, 4, "scan")
    after = set(os.listdir(SAVE_DIR))
    new_files = sorted(after - before)
    check("保存后新增文件", len(new_files) == 1, new_files)
    saved_path = os.path.join(SAVE_DIR, new_files[0])
    data = hist.load_results_from_file(saved_path)
    check("读回结果条数一致", data and len(data["results"]) == 2, data and len(data.get("results")))
    check("读回含 save_time", bool(data.get("save_time")))
    listing = hist.get_history_list(4, "scan")
    check("列表包含新文件", any(h["filename"] == new_files[0] for h in listing), listing)
    check("列表项含 count", all("count" in h for h in listing))
finally:
    if saved_path and os.path.exists(saved_path):
        ok = hist.delete_history(saved_path)
        check("删除历史成功", ok is True)
        check("删除后文件不存在", not os.path.exists(saved_path))
    check("历史目录已复原", set(os.listdir(SAVE_DIR)) == before, set(os.listdir(SAVE_DIR)) - before)

print("\n== 11. SpeedTestTask 中止语义（缺陷 3.2 核心） ==")
from core.factory import create_speed_task
from core.scanner import SpeedTestTask
task = create_speed_task(scan, {"region_code": None, "selected_ips": None, "count": 10,
                                "current_port": 443, "speed_url": "auto", "min_speed": 0.0,
                                "label": None}, {})
check("create_speed_task 返回 SpeedTestTask", isinstance(task, SpeedTestTask), type(task))
check("默认串行（speed_workers=1）", getattr(task, "speed_workers", None) == 1, getattr(task, "speed_workers", None))
task.stop()
check("stop() 置 running=False", task.running is False, task.running)
res = task.run()
check("中止后 run() 返回 None（而非 []）", res is None, res)
check("中止后 aborted 标志为真", task.aborted is True, task.aborted)

print("\n== 12. 创建扫描器参数兜底 ==")
from core.factory import create_scanner
sc = create_scanner({"ip_version": 4, "source_mode": "仅官方", "cidrs": [], "entries": None,
                     "port": 443, "workers": "abc", "threshold": None, "sample_max": 1e9,
                     "ping_times": -3, "scan_mode": "tcping"})
check("workers 坏值回退 200", sc.max_workers == 200, sc.max_workers)
check("threshold 坏值回退 230", sc.latency_threshold == 230, sc.latency_threshold)
check("sample_max 钳制 200000", sc.sample_max == 200000, sc.sample_max)
check("ping_times 负数 -> 0（即自动，回落到默认 3）", sc.ping_times == 3, sc.ping_times)

print("\n== 13. parse_source_text：自定义来源多形态分类 ==")
from core.importer import parse_source_text, parse_port_list, describe_source_stats

MIXED = "\n".join([
    "# 混合来源",
    "104.16.0.0/24            # CIDR -> 采样",
    "1.1.1.1                  # 单个 IP -> 直测",
    "1.0.0.1:8443             # IP + 端口",
    "104.16.0.10-104.16.0.14  # IP 段（小，展开为直测）",
    "2606:4700::/32           # IPv6 CIDR（IPv4 模式下应被忽略）",
    "[2606:4700::1111]:8443   # IPv6 + 端口（同上）",
    "http://example.com:8080/ # 带 scheme",
    "",
])
cidrs, entries, errors, stats = parse_source_text(MIXED, 443, 4, resolve_domains=False)
check("CIDR 被识别为网段", cidrs == ["104.16.0.0/24"], cidrs)
check("单 IP 进入直测列表", ("1.1.1.1", 443) in [(e["ip"], e["port"]) for e in entries], entries)
check("IP:端口 保留端口", ("1.0.0.1", 8443) in [(e["ip"], e["port"]) for e in entries], entries)
check("小 IP 段展开为 5 条直测",
      len([e for e in entries if e["ip"].startswith("104.16.0.1")]) == 5, entries)
check("IPv6 网段不报错但计入 skipped", stats["skipped"] >= 1, stats)
check("预览模式域名不报错且被计数", not errors and stats["domain"] >= 1, (errors, stats))
check("统计含网段 / 节点 / 段 / 域名四项",
      stats["cidr"] == 1 and stats["entry"] == 7 and stats["range"] == 1 and stats["domain"] == 1, stats)
check("describe_source_stats 输出人话", "网段" in describe_source_stats(stats),
      describe_source_stats(stats))

cidrs6, entries6, errors6, stats6 = parse_source_text(
    "2606:4700::/32\n[2606:4700::1111]:8443", 443, 6, resolve_domains=False)
check("IPv6 模式下识别 IPv6 网段", cidrs6 == ["2606:4700::/32"], cidrs6)
check("IPv6 模式下识别 IPv6 直测节点",
      [(e["ip"], e["port"]) for e in entries6] == [("2606:4700::1111", 8443)], entries6)

# 边界：正好 MAX_RANGE_EXPAND(1024) 个地址 -> 展开为逐条直测（更精确）
edge_cidrs, edge_entries, _e, _s = parse_source_text("10.0.0.0-10.0.3.255", 443, 4)
check("段大小 == MAX_RANGE_EXPAND 时展开为直测节点",
      not edge_cidrs and len(edge_entries) == 1024, (edge_cidrs, len(edge_entries)))

# 超过 MAX_RANGE_EXPAND -> 折算为 CIDR，交给采样
big_cidrs, big_entries, _e, big_stats = parse_source_text("10.0.0.0-10.0.4.255", 443, 4)
check("大 IP 段折算为 CIDR（不展开）", bool(big_cidrs) and not big_entries, (big_cidrs, big_entries))
check("折算后的 CIDR 能覆盖原段（含 10.0.0.0/22）", "10.0.0.0/22" in big_cidrs, big_cidrs)

_bad_cidrs, _bad_entries, bad_errors, _s = parse_source_text("not-an-ip!!!", 443, 4, resolve_domains=False)
check("非法行进入 errors（会阻断扫描）", bool(bad_errors), bad_errors)

dedup_cidrs, dedup_entries, _e2, dedup_stats = parse_source_text(
    "1.1.1.1\n1.1.1.1\n1.1.1.1:443", 443, 4, resolve_domains=False)
check("重复节点被去重", len(dedup_entries) == 1, dedup_entries)
check("去重计入 ignored", dedup_stats["ignored"] >= 1, dedup_stats)

check("parse_port_list 去重并过滤非法",
      parse_port_list("443, 8443 2053;443 99999 abc") == [443, 8443, 2053],
      parse_port_list("443, 8443 2053;443 99999 abc"))
check("parse_port_list 空值 -> 空列表", parse_port_list("") == [] and parse_port_list(None) == [])

print("\n== 14. 测速地址预设（对齐 CFData-WEB speed_url.go） ==")
from core.speed_url import (
    SPEED_URL_PRESETS, CUSTOM_SPEED_URL, CF_SPEED_URL, CM_SPEED_URL,
    MOBILE_DEDICATED_SPEED_URL, is_auto_speed_url, url_to_preset_value,
    preset_value_to_url, DEFAULT_SPEED_BYTES,
)
values = [v for _l, v in SPEED_URL_PRESETS]
check("预设含 auto / CF / 移动友好 / 移动专属 / 自定义",
      values == ["auto", CF_SPEED_URL, CM_SPEED_URL, MOBILE_DEDICATED_SPEED_URL, CUSTOM_SPEED_URL],
      values)
check("CF 预设字节数与参考一致（99999999）",
      str(DEFAULT_SPEED_BYTES) == "99999999" and "99999999" in CF_SPEED_URL, CF_SPEED_URL)
check("移动友好源与参考一致", CM_SPEED_URL == "cf.090227.xyz/__down?bytes=99999999", CM_SPEED_URL)
check("移动专属源与参考一致", MOBILE_DEDICATED_SPEED_URL == "speed.okl.abrdns.com",
      MOBILE_DEDICATED_SPEED_URL)
check("is_auto_speed_url('自动选择') 为真", is_auto_speed_url("自动选择") is True)
check("is_auto_speed_url('') 为真", is_auto_speed_url("") is True)
check("is_auto_speed_url(CF 地址) 为假", is_auto_speed_url(CF_SPEED_URL) is False)
check("预设地址反查回预设值", url_to_preset_value(CF_SPEED_URL) == CF_SPEED_URL)
check("非预设地址反查为「自定义」", url_to_preset_value("my.host/dl") == CUSTOM_SPEED_URL)
check("自定义值转 URL 为空串（交给输入框）", preset_value_to_url(CUSTOM_SPEED_URL) == "")
h, p, tls = parse_speed_url("speed.okl.abrdns.com")
check("裸主机名补 CF 标准端点 + bytes",
      h == "speed.okl.abrdns.com" and p == "/__down?bytes=99999999", (h, p))
h, p, tls = parse_speed_url("//example.com/p")
check("// 前缀按 https 处理", tls is True and h == "example.com", (h, tls))

print("\n== 15. 远程数据源自适应解析（core/sources.py） ==")
from core.sources import (
    parse_adaptive, sanitize_sources, format_sources_text, parse_source_lines, DEFAULT_SOURCES,
)
check("内置默认源非空", len(DEFAULT_SOURCES) >= 1, DEFAULT_SOURCES)
check("内置源均为 http(s)", all(s["url"].startswith("http") for s in DEFAULT_SOURCES))

messy = "\n".join([
    "🇭🇰 HKG 1.2.3.4:8443",
    "🇯🇵 NRT 5.6.7.8 2053",
    "[2606:4700::1111]:8443 中国香港",
    "9.9.9.9#备注",
    "# 整行注释",
    "完全没有 IP 的一行",
])
parsed, perrors = parse_adaptive(messy)
got = [(e["ip"], e["port"]) for e in parsed]
check("emoji + 地区码 + IP:PORT 可解析", ("1.2.3.4", 8443) in got, got)
check("空格分隔端口可解析", ("5.6.7.8", 2053) in got, got)
check("IPv6 + 方括号端口可解析", ("2606:4700::1111", 8443) in got, got)
check("IP#备注 使用默认端口", ("9.9.9.9", 443) in got, got)
check("解析结果条数正确（4 条）", len(parsed) == 4, got)

json_parsed, _je = parse_adaptive(
    '{"data":[{"ip":"1.1.1.1","port":2053},{"address":"2.2.2.2"}]}')
check("JSON 数组可解析", [(e["ip"], e["port"]) for e in json_parsed] ==
      [("1.1.1.1", 2053), ("2.2.2.2", 443)], json_parsed)
check("空内容返回错误", parse_adaptive("")[1] != [])
check("无 IP 内容返回错误", parse_adaptive("hello world")[1] != [])

src_text = format_sources_text([
    {"name": "A", "url": "https://a.example/x", "enabled": True},
    {"name": "B", "url": "https://b.example/y", "enabled": False},
])
check("源列表 -> 文本：禁用项加 #", src_text.splitlines()[1].startswith("# "), src_text)
round_trip = sanitize_sources(src_text)
check("文本 -> 源列表 往返一致",
      round_trip == [{"name": "A", "url": "https://a.example/x", "enabled": True},
                     {"name": "B", "url": "https://b.example/y", "enabled": False}],
      round_trip)
check("非法 URL 被过滤", sanitize_sources(["ftp://x", "not-a-url", "https://ok/x"]) ==
      [{"name": "https://ok/x", "url": "https://ok/x", "enabled": True}],
      sanitize_sources(["ftp://x", "not-a-url", "https://ok/x"]))
check("非列表输入返回空", sanitize_sources(None) == [] and sanitize_sources(123) == [])

print("\n== 16. 扫描器：直测节点 / 前置端口过滤 / 来源描述 ==")
mixed = create_scanner({
    "ip_version": 4, "source_mode": "仅自定义",
    "cidrs": ["104.16.0.0/24"],
    "entries": [{"ip": "1.1.1.1", "port": 443, "ip_version": 4},
                {"ip": "1.0.0.1", "port": 8443, "ip_version": 4},
                {"ip": "2606:4700::1111", "port": 443, "ip_version": 6}],
    "port": 443, "workers": 50, "threshold": 230, "sample_max": 10,
    "ping_times": 1, "scan_mode": "tcping", "pre_filter_ports": [],
})
check("自定义来源构建 IPv4Scanner", mixed.ip_version == 4)
check("直测节点只保留当前 IP 版本",
      sorted(mixed.custom_entry_ips()) == ["1.0.0.1", "1.1.1.1"], mixed.custom_entry_ips())
gen = mixed.generate_ips_from_cidrs()
check("生成结果包含直测节点", "1.1.1.1" in gen and "1.0.0.1" in gen, gen[:5])
check("生成结果不含 IPv6 节点", "2606:4700::1111" not in gen)
check("来源描述含「指定节点」", "指定节点" in mixed.describe_source(), mixed.describe_source())

filtered = create_scanner({
    "ip_version": 4, "source_mode": "仅自定义", "cidrs": [],
    "entries": [{"ip": "1.1.1.1", "port": 443, "ip_version": 4},
                {"ip": "2.2.2.2", "port": 8080, "ip_version": 4}],
    "port": 443, "workers": 50, "threshold": 230, "sample_max": 10,
    "ping_times": 1, "scan_mode": "tcping", "pre_filter_ports": "443",
})
check("前置端口过滤剔除 8080", filtered.custom_entry_ips() == ["1.1.1.1"],
      filtered.custom_entry_ips())
check("过滤计数正确", filtered.filtered_out == 1, filtered.filtered_out)

pure = create_scanner({
    "ip_version": 4, "source_mode": "仅自定义", "cidrs": [],
    "entries": [{"ip": "1.1.1.1", "port": 443, "ip_version": 4},
                {"ip": "2606:4700::1111", "port": 443, "ip_version": 6}],
    "port": 443, "workers": 50, "threshold": 230, "sample_max": 10,
    "ping_times": 1, "scan_mode": "tcping",
})
check("纯直测列表允许 v4/v6 混合", pure.ip_label == "非标(混合)", pure.ip_label)
check("纯直测列表不做采样",
      set(pure.generate_ips_from_cidrs()) == {"1.1.1.1", "2606:4700::1111"},
      pure.generate_ips_from_cidrs())

print("\n== 17. 分地区 TopN（SpeedTestTask） ==")
region_rows = [
    {"ip": "1.1.1.1", "latency": 10, "iata_code": "HKG"},
    {"ip": "1.1.1.2", "latency": 20, "iata_code": "HKG"},
    {"ip": "1.1.1.3", "latency": 30, "iata_code": "HKG"},
    {"ip": "2.2.2.1", "latency": 15, "iata_code": "NRT"},
    {"ip": "2.2.2.2", "latency": 25, "iata_code": "NRT"},
    {"ip": "3.3.3.1", "latency": 12, "iata_code": "SIN"},
]
topn_task = create_speed_task(region_rows, {"count": 50, "current_port": 443,
                                            "speed_url": "auto", "min_speed": 0},
                              {"per_region_topn": 1})
check("per_region_topn 生效", topn_task.per_region_topn == 1, topn_task.per_region_topn)
picked = topn_task._pick_targets()
check("每个地区只保留 1 个（共 3 个）", len(picked) == 3, picked)
check("保留的是各地区延迟最低的",
      sorted(r["ip"] for r in picked) == ["1.1.1.1", "2.2.2.1", "3.3.3.1"],
      [r["ip"] for r in picked])

off_task = create_speed_task(region_rows, {"count": 50, "current_port": 443,
                                           "speed_url": "auto", "min_speed": 0}, {})
check("默认 per_region_topn = 0（不限）", off_task.per_region_topn == 0)
check("不限时按延迟排序取前 N", len(off_task._pick_targets()) == 6)

print("\n" + "=" * 56)
print(f"通过 {len(PASS)} / 失败 {len(FAIL)}")
if FAIL:
    print("失败项: " + ", ".join(FAIL))
    sys.exit(1)
print("全部通过 ✓")
