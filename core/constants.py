#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import sys
import os
import platform
import json
import time
import logging
import requests
from typing import List, Optional, Dict


logger = logging.getLogger("CloudTrace")


def get_project_root() -> str:
    """获取项目根目录"""
    if getattr(sys, 'frozen', False):
        # 打包后，从可执行文件目录往上找
        return os.path.dirname(sys.executable)
    else:
        # 开发时，从当前文件 (core/constants.py) 往上两级
        return os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


PROJECT_ROOT = get_project_root()


def resource_path(relative_path: str) -> str:
    """获取资源文件的绝对路径"""
    base_path = None
    
    # 优先尝试从 _MEIPASS 获取（PyInstaller 打包后的临时目录）
    if getattr(sys, 'frozen', False) and hasattr(sys, '_MEIPASS'):
        base_path = sys._MEIPASS
        candidate = os.path.join(base_path, relative_path)
        if os.path.exists(candidate):
            return candidate
    
    # 其次尝试从项目根目录获取
    base_path = PROJECT_ROOT
    candidate = os.path.join(base_path, relative_path)
    if os.path.exists(candidate):
        return candidate
    
    # 最后返回相对路径作为后备
    return relative_path


def get_system_font():
    system = platform.system()
    if system == "Windows":
        return "Microsoft YaHei, SimHei, SimSun, Arial, sans-serif"
    elif system == "Darwin":
        return "PingFang SC, Helvetica, sans-serif"
    else:
        return "DejaVu Sans, sans-serif"


SYSTEM_FONT = get_system_font()
FONT_FAMILY = SYSTEM_FONT.split(',')[0].strip()

BTN_W = 120
BTN_H = 32
SPACING = 8


# 保持 APP_DIR 向后兼容
APP_DIR = PROJECT_ROOT


def get_version():
    version_file = resource_path("version.txt")
    try:
        with open(version_file, "r", encoding="utf-8") as f:
            return f.read().strip()
    except FileNotFoundError:
        return "DEV"


CF_OFFICIAL_IPV4_URL = "https://www.cloudflare.com/ips-v4/"
CF_OFFICIAL_IPV6_URL = "https://www.cloudflare.com/ips-v6/"

IP_CACHE_FILE = os.path.join(APP_DIR, "ip_cache.json")
IP_CACHE_UPDATE_INTERVAL = 30 * 24 * 3600


CF_IPV4_CIDRS = [
    "173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
    "141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
    "197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/12",
    "172.64.0.0/17", "172.64.128.0/18", "172.64.192.0/19", "172.64.224.0/22",
    "172.64.229.0/24", "172.64.230.0/23", "172.64.232.0/21", "172.64.240.0/21",
    "172.64.248.0/21", "172.65.0.0/16", "172.66.0.0/16", "172.67.0.0/16",
    "131.0.72.0/22"
]

CF_IPV6_CIDRS = [
    "2400:cb00:2049::/48", "2400:cb00:f00e::/48", "2606:4700::/32",
    "2606:4700:10::/48", "2606:4700:130::/48", "2606:4700:3000::/48",
    "2606:4700:3001::/48", "2606:4700:3002::/48", "2606:4700:3003::/48",
    "2606:4700:3004::/48", "2606:4700:3005::/48", "2606:4700:3006::/48",
    "2606:4700:3007::/48", "2606:4700:3008::/48", "2606:4700:3009::/48",
    "2606:4700:3010::/48", "2606:4700:3011::/48", "2606:4700:3012::/48",
    "2606:4700:3013::/48", "2606:4700:3014::/48", "2606:4700:3015::/48",
    "2606:4700:3016::/48", "2606:4700:3017::/48", "2606:4700:3018::/48",
    "2606:4700:3019::/48", "2606:4700:3020::/48", "2606:4700:3021::/48",
    "2606:4700:3022::/48", "2606:4700:3023::/48", "2606:4700:3024::/48",
    "2606:4700:3025::/48", "2606:4700:3026::/48", "2606:4700:3027::/48",
    "2606:4700:3028::/48", "2606:4700:3029::/48", "2606:4700:3030::/48",
    "2606:4700:3031::/48", "2606:4700:3032::/48", "2606:4700:3033::/48",
    "2606:4700:3034::/48", "2606:4700:3035::/48", "2606:4700:3036::/48",
    "2606:4700:3037::/48", "2606:4700:3038::/48", "2606:4700:3039::/48",
    "2606:4700:a0::/48", "2606:4700:a1::/48", "2606:4700:a8::/48",
    "2606:4700:a9::/48", "2606:4700:a::/48", "2606:4700:b::/48",
    "2606:4700:c::/48", "2606:4700:d0::/48", "2606:4700:d1::/48",
    "2606:4700:d::/48", "2606:4700:e0::/48", "2606:4700:e1::/48",
    "2606:4700:e2::/48", "2606:4700:e3::/48", "2606:4700:e4::/48",
    "2606:4700:e5::/48", "2606:4700:e6::/48", "2606:4700:e7::/48",
    "2606:4700:e::/48", "2606:4700:f1::/48", "2606:4700:f2::/48",
    "2606:4700:f3::/48", "2606:4700:f4::/48", "2606:4700:f5::/48",
    "2606:4700:f::/48", "2803:f800:50::/48", "2803:f800:51::/48",
    "2a06:98c1:3100::/48", "2a06:98c1:3101::/48", "2a06:98c1:3102::/48",
    "2a06:98c1:3103::/48", "2a06:98c1:3104::/48", "2a06:98c1:3105::/48",
    "2a06:98c1:3106::/48", "2a06:98c1:3107::/48", "2a06:98c1:3108::/48",
    "2a06:98c1:3109::/48", "2a06:98c1:310a::/48", "2a06:98c1:310b::/48",
    "2a06:98c1:310c::/48", "2a06:98c1:310d::/48", "2a06:98c1:310e::/48",
    "2a06:98c1:310f::/48", "2a06:98c1:3120::/48", "2a06:98c1:3121::/48",
    "2a06:98c1:3122::/48", "2a06:98c1:3123::/48", "2a06:98c1:3200::/48",
    "2a06:98c1:50::/48", "2a06:98c1:51::/48", "2a06:98c1:54::/48",
    "2a06:98c1:58::/48"
]

AIRPORT_CODES = {
    "HKG": "香港", "TPE": "台北", "KHH": "高雄", "MFM": "澳门",
    "NRT": "东京", "HND": "东京", "KIX": "大阪", "NGO": "名古屋",
    "FUK": "福冈", "CTS": "札幌", "OKA": "冲绳",
    "ICN": "首尔", "GMP": "首尔", "PUS": "釜山",
    "SIN": "新加坡", "BKK": "曼谷", "DMK": "曼谷",
    "KUL": "吉隆坡", "HKT": "普吉岛",
    "MNL": "马尼拉", "CEB": "宿务",
    "HAN": "河内", "SGN": "胡志明市",
    "JKT": "雅加达", "DPS": "巴厘岛",
    "DEL": "德里", "BOM": "孟买", "MAA": "金奈",
    "DXB": "迪拜", "AUH": "阿布扎比",
    "SJC": "圣何塞", "LAX": "洛杉矶", "SFO": "旧金山",
    "SEA": "西雅图", "PDX": "波特兰",
    "LAS": "拉斯维加斯", "PHX": "菲尼克斯",
    "DEN": "丹佛", "DFW": "达拉斯", "IAH": "休斯顿",
    "ORD": "芝加哥", "MSP": "明尼阿波利斯",
    "ATL": "亚特兰大", "MIA": "迈阿密", "MCO": "奥兰多",
    "JFK": "纽约", "EWR": "纽约", "LGA": "纽约",
    "BOS": "波士顿", "PHL": "费城", "IAD": "华盛顿",
    "YYZ": "多伦多", "YVR": "温哥华", "YUL": "蒙特利尔",
    "LHR": "伦敦", "LGW": "伦敦", "STN": "伦敦",
    "CDG": "巴黎", "ORY": "巴黎",
    "FRA": "法兰克福", "MUC": "慕尼黑", "TXL": "柏林",
    "AMS": "阿姆斯特丹", "EIN": "埃因霍温",
    "MAD": "马德里", "BCN": "巴塞罗那",
    "FCO": "罗马", "MXP": "米兰", "LIN": "米兰",
    "ZRH": "苏黎世", "GVA": "日内瓦",
    "VIE": "维也纳", "PRG": "布拉格",
    "WAW": "华沙", "KRK": "克拉科夫",
    "HEL": "赫尔辛基", "OSL": "奥斯陆", "ARN": "斯德哥尔摩",
    "CPH": "哥本哈根",
    "SYD": "悉尼", "MEL": "墨尔本", "BNE": "布里斯班",
    "PER": "珀斯", "ADL": "阿德莱德",
    "AKL": "奥克兰", "WLG": "惠灵顿",
    "GRU": "圣保罗", "GIG": "里约热内卢", "EZE": "布宜诺斯艾利斯",
    "SCL": "圣地亚哥", "LIM": "利马", "BOG": "波哥大",
    "JNB": "约翰内斯堡", "CPT": "开普敦", "CAI": "开罗",
}

PORT_OPTIONS = ["443", "2053", "2083", "2087", "2096", "8443"]


def fetch_official_cidrs(url: str):
    try:
        resp = requests.get(url, timeout=10)
        resp.raise_for_status()
        lines = resp.text.splitlines()
        return [line.strip() for line in lines if line.strip() and not line.startswith('#')]
    except Exception as e:
        logger.warning("获取官方列表失败 (%s): %s", url, e)
        return None


def load_or_update_ip_cache(ip_version: int):
    builtin = CF_IPV4_CIDRS if ip_version == 4 else CF_IPV6_CIDRS
    key = "ipv4" if ip_version == 4 else "ipv6"
    url = CF_OFFICIAL_IPV4_URL if ip_version == 4 else CF_OFFICIAL_IPV6_URL

    cache = {}
    try:
        if os.path.exists(IP_CACHE_FILE):
            with open(IP_CACHE_FILE, 'r', encoding='utf-8') as f:
                cache = json.load(f)
    except Exception:
        cache = {}

    current_time = time.time()
    # 每个 IP 版本独立记录更新时间，避免更新 v4 顺带给 v6 缓存“续期”
    ts_key = f"{key}_update_time"
    if cache.get(key) and (current_time - cache.get(ts_key, 0) < IP_CACHE_UPDATE_INTERVAL):
        return cache[key]

    official_list = fetch_official_cidrs(url)
    if official_list:
        cache[key] = official_list
        cache[ts_key] = current_time
        try:
            from core.utils import atomic_write_json
            atomic_write_json(IP_CACHE_FILE, cache)
            logger.info("已更新 %s 列表（%d 个 CIDR）", key, len(official_list))
        except Exception:
            pass
        return official_list

    if cache.get(key):
        logger.warning("无法更新，使用过期缓存（%d 个 CIDR）", len(cache[key]))
        return cache[key]

    logger.info("无缓存，使用内置 %s 列表", key)
    return builtin
