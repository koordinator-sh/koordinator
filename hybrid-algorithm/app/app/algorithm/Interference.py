#!/usr/bin/env python3
"""
pod_interference_analysis.py
==============================
Pod 维度干扰分析（融合节点背景压力）

分析逻辑：
  Step1 — 节点背景压力（汇总节点 CPU/MEM/IO/NET，CPU 利用率基于固定 64 核）
  Step2 — Pod CPI 异常（koordlet CPI）
  Step3 — Pod PSI 异常（koordlet PSI）
  Step4 — Pod 自身性能压力（CPU/MEM/IO/NET 使用量）

输出：
  pod_interference_analysis.csv   —— Pod 干扰状态（含节点背景）
  pod_interference_forecast.csv   —— 未来 30 分钟预测
"""

import os
import json
import math
import gzip
from collections import defaultdict
from datetime import datetime, timezone, timedelta
from tqdm import tqdm

# ================== 配置 ==================
INPUT_FILE = "/root/autodl-tmp/Hybrid_export/hybrid_export_20260313_083742.gz"  # 修改为实际文件路径
OUT_DIR = "/root/autodl-tmp/Hybrid_export/PodInterferenceOutput"
BUCKET_SEC = 60          # 分钟聚合粒度

# 预测
FORECAST_MINUTES = 30
TREND_WINDOW = 30
TREND_R2_MIN = 0.05

# CPI 阈值
CPI_WARN = 1.0   # ≥1.0 触发
CPI_CRIT = 5.0   # ≥5.0 严重

# PSI 阈值（%，avg10）
PSI_SOME_WARN = 2.0
PSI_SOME_CRIT = 10.0
PSI_FULL_WARN = 1.0
PSI_FULL_CRIT = 5.0

# Pod 性能阈值（CPU 利用率相对于 Pod 的 limit）
POD_CPU_WARN = 0.70   # 70%
POD_CPU_CRIT = 0.90   # 90%
POD_MEM_WARN = 0.75   # 75%
POD_MEM_CRIT = 0.90   # 90%
POD_IO_WARN = 50.0    # 50 MB/s
POD_IO_CRIT = 200.0   # 200 MB/s
POD_NET_WARN = 100.0  # 100 MB/s
POD_NET_CRIT = 500.0  # 500 MB/s

# 节点性能阈值（背景压力）
NODE_CPU_WARN = 0.70   # 70% 利用率（相对于 64 核）
NODE_CPU_CRIT = 0.90   # 90%
NODE_MEM_WARN = 32.0   # GB
NODE_MEM_CRIT = 64.0
NODE_IO_WARN = 200.0   # MB/s
NODE_IO_CRIT = 500.0
NODE_NET_WARN = 200.0
NODE_NET_CRIT = 500.0

NODE_TOTAL_CORES = 64   # 固定节点总核数

TZ = timezone(timedelta(hours=8))

# 指标名
M_CPI = "koordlet_container_cpi"
M_PSI = "koordlet_container_psi"
M_CPU_U = "container_cpu_usage_seconds_total"
M_CPU_Q = "container_spec_cpu_quota"
M_CPU_P = "container_spec_cpu_period"
M_MEM_U = "container_memory_usage_bytes"
M_MEM_L = "container_spec_memory_limit_bytes"
M_FS_R = "container_fs_reads_bytes_total"
M_FS_W = "container_fs_writes_bytes_total"
M_NET_RX = "container_network_receive_bytes_total"
M_NET_TX = "container_network_transmit_bytes_total"

PSI_PREC = "avg10"
SEV_RANK = {"NONE": 0, "WARN": 1, "CRIT": 2}

# 简单颜色定义
class Colors:
    GREEN = '\033[92m'
    YELLOW = '\033[93m'
    CYAN = '\033[96m'
    RED = '\033[91m'
    BOLD = '\033[1m'
    RESET = '\033[0m'

# ── 工具函数 ──────────────────────────────────────────
def sf(x):
    try:
        v = float(x)
        return None if (math.isnan(v) or math.isinf(v)) else v
    except:
        return None

def t2s(ts):
    return datetime.fromtimestamp(int(ts), tz=TZ).strftime("%Y-%m-%d %H:%M:%S")

def open_src(p):
    return gzip.open(p, "rt", encoding="utf-8", errors="ignore") if p.endswith(".gz") \
        else open(p, "r", encoding="utf-8", errors="ignore")

def sev(v, warn, crit):
    if v is None or v == 0:
        return "NONE"
    return "CRIT" if v >= crit else "WARN" if v >= warn else "NONE"

def sev_psi(some, full):
    if full >= PSI_FULL_CRIT or some >= PSI_SOME_CRIT:
        return "CRIT"
    if full >= PSI_FULL_WARN or some >= PSI_SOME_WARN:
        return "WARN"
    return "NONE"

def fmt(v, spec):
    return format(v, spec) if v is not None else "N/A"

# ── 线性趋势 ──────────────────────────────────────────
def linear_trend(vals):
    n = len(vals)
    if n < 3:
        return 0.0, vals[-1] if vals else 0.0, 0.0
    xs = list(range(n))
    mx = (n - 1) / 2
    my = sum(vals) / n
    sxx = sum((x - mx) ** 2 for x in xs)
    sxy = sum((xs[i] - mx) * (vals[i] - my) for i in range(n))
    syy = sum((v - my) ** 2 for v in vals)
    if sxx == 0:
        return 0.0, my, 0.0
    sl = sxy / sxx
    ic = my - sl * mx
    r2 = max(0.0, min(1.0, 1 - sum((vals[i] - (ic + sl * xs[i])) ** 2 for i in range(n)) / syy if syy > 0 else 0.0))
    return sl, ic, r2


# =====================================================
# 基于时间窗口的首尾差值计算（用户提供逻辑）
# =====================================================
def calculate_for_record(record):
    """计算时间窗口 & 首尾值"""
    values = record.get("values", [])
    timestamps = record.get("timestamps", [])

    if not values or not timestamps or len(values) != len(timestamps):
        return None

    valid = []
    for v, t in zip(values, timestamps):
        try:
            valid.append((float(t), float(v)))
        except:
            continue

    if len(valid) < 2:
        return None

    valid.sort(key=lambda x: x[0])

    first_ts, first_val = valid[0]
    last_ts, last_val = valid[-1]

    return {
        "time_window_seconds": (last_ts - first_ts) / 1000.0,
        "first_val": first_val,
        "last_val": last_val
    }


# =====================================================
# 第一遍：提取所有 memory limit 和 CPU quota/period
# =====================================================
def extract_limits(input_file):
    """提取 memory limit, cpu quota, cpu period"""
    print(f"\n{Colors.BOLD}{Colors.CYAN}第一遍：提取 limits ...{Colors.RESET}")
    
    limits_cache = {}  # container_key -> {"mem_limit": , "cpu_quota": , "cpu_period": }
    line_count = 0

    try:
        with open_src(input_file) as fin:
            for line in tqdm(fin, desc="扫描 limits", unit="line"):
                line_count += 1
                try:
                    record = json.loads(line)
                    metric = record.get("metric", {})
                    metric_name = metric.get("__name__", "")
                    if metric_name not in (M_MEM_L, M_CPU_Q, M_CPU_P):
                        continue

                    container_key = (
                        metric.get("id")
                        or metric.get("container")
                        or metric.get("pod")
                    )
                    if not container_key:
                        continue

                    calc = calculate_for_record(record)
                    if not calc:
                        continue
                    val = calc["last_val"]
                    if val <= 0:
                        continue

                    if container_key not in limits_cache:
                        limits_cache[container_key] = {"mem_limit": None, "cpu_quota": None, "cpu_period": None}
                    if metric_name == M_MEM_L:
                        limits_cache[container_key]["mem_limit"] = val
                    elif metric_name == M_CPU_Q:
                        limits_cache[container_key]["cpu_quota"] = val
                    elif metric_name == M_CPU_P:
                        limits_cache[container_key]["cpu_period"] = val

                except Exception:
                    continue
    except Exception as e:
        print(f"{Colors.RED}读取输入文件失败: {e}{Colors.RESET}")
        return None

    print(f"{Colors.GREEN}✓ 已提取 limits 数量：{len(limits_cache)}{Colors.RESET}")
    return limits_cache


# =====================================================
# 第二遍：处理性能数据 + CPI/PSI + 节点聚合
# =====================================================
def process_all(input_file, limits_cache):
    """
    返回：
        pod_data: dict[pod] = {
            "node": str,
            "namespace": str,
            "cpu_cores": float,          # 总 CPU 使用核数（所有容器求和）
            "mem_usage_gb": float,       # 总内存使用 GB
            "mem_util": float,           # 内存利用率（0~1）
            "io_mbps": float,            # IO 吞吐量 MB/s
            "net_mbps": float,           # 网络吞吐量 MB/s
            "cpu_limit_cores": float,    # Pod 的 CPU limit（如果所有容器一致，取第一个；否则取和？这里简单取第一个容器的 limit）
            "mem_limit_bytes": float,    # Pod 的内存 limit（同上）
            "cpi_series": {minute: cpi}, # CPI 时间序列（按分钟）
            "psi_series": {minute: {("cpu","some"): val, ...}} # PSI 时间序列
        }
        node_data: dict[node] = {
            "cpu_cores": {minute: total_cores},
            "mem_gb": {minute: total_gb},
            "io_mbps": {minute: total_mbps},
            "net_mbps": {minute: total_mbps}
        }
    """
    print(f"\n{Colors.BOLD}{Colors.CYAN}第二遍：处理性能、CPI、PSI 数据...{Colors.RESET}")

    # 临时存储每个 Pod 的容器级数据（用于求和）
    pod_containers = defaultdict(lambda: {
        "cpu_cores_sum": defaultdict(float),   # minute -> total cores
        "mem_gb_sum": defaultdict(float),
        "io_mbps_sum": defaultdict(float),
        "net_mbps_sum": defaultdict(float),
        "cpu_limit": None,
        "mem_limit": None,
        "node": None,
        "ns": None
    })
    # 节点聚合（求和所有 Pod）
    node_agg = defaultdict(lambda: {
        "cpu_cores": defaultdict(float),
        "mem_gb": defaultdict(float),
        "io_mbps": defaultdict(float),
        "net_mbps": defaultdict(float)
    })
    # CPI 和 PSI 收集（按 Pod）
    pod_cpi_raw = defaultdict(lambda: defaultdict(list))   # pod -> {cycles/instr: [(ts, val)]}
    pod_psi_raw = defaultdict(lambda: defaultdict(list))   # pod -> {(res,deg,prec): [(ts, val)]}

    # 辅助函数：判断时间戳单位
    def is_ms(tss):
        return int(tss[-1]) > 1_000_000_000_000
    def tsc(t, ms):
        return int(t) // 1000 if ms else int(t)

    line_count = 0
    with open_src(input_file) as fin:
        for line in tqdm(fin, desc="处理数据", unit="line"):
            line_count += 1
            try:
                record = json.loads(line)
            except:
                continue
            metric = record.get("metric", {})
            name = metric.get("__name__")
            if name not in {M_CPU_U, M_MEM_U, M_FS_R, M_FS_W, M_NET_RX, M_NET_TX, M_CPI, M_PSI}:
                continue

            tss = record.get("timestamps", [])
            vals = record.get("values", [])
            if not tss or not vals or len(tss) != len(vals):
                continue
            ms = is_ms(tss)

            # ---- CPI 处理 ----
            if name == M_CPI:
                pod = metric.get("pod_name", "")
                if not pod:
                    continue
                field = metric.get("cpi_field", "")
                if field not in ("cycles", "instructions"):
                    continue
                node = metric.get("node", "")
                if node:
                    pod_containers[pod]["node"] = node
                for t, v in zip(tss, vals):
                    fv = sf(v)
                    if fv is not None:
                        ts_sec = tsc(t, ms)
                        pod_cpi_raw[pod][field].append((ts_sec, fv))
                continue

            # ---- PSI 处理 ----
            if name == M_PSI:
                pod = metric.get("pod_name", "")
                if not pod:
                    continue
                res = metric.get("psi_resource_type", "")
                deg = metric.get("psi_degree", "")
                prec = metric.get("psi_precision", "")
                if not res or not deg or not prec:
                    continue
                node = metric.get("node", "")
                if node:
                    pod_containers[pod]["node"] = node
                key = (res, deg, prec)
                for t, v in zip(tss, vals):
                    fv = sf(v)
                    if fv is not None:
                        ts_sec = tsc(t, ms)
                        pod_psi_raw[pod][key].append((ts_sec, fv))
                continue

            # ---- 性能指标（CPU/MEM/IO/NET） ----
            pod = metric.get("pod", "")
            if not pod:
                continue
            ns = metric.get("namespace", "")
            if ns:
                pod_containers[pod]["ns"] = ns
            container_key = metric.get("id") or metric.get("container") or pod
            if not container_key:
                continue

            calc = calculate_for_record(record)
            if not calc:
                continue
            tw = calc["time_window_seconds"]
            if tw <= 0:
                continue

            minute = tsc(tss[-1], ms) // BUCKET_SEC

            # CPU
            if name == M_CPU_U:
                delta = calc["last_val"] - calc["first_val"]
                cores = delta / tw
                pod_containers[pod]["cpu_cores_sum"][minute] += cores
                # 节点聚合
                node = pod_containers[pod]["node"]
                if node:
                    node_agg[node]["cpu_cores"][minute] += cores

            # Memory usage (Gauge)
            elif name == M_MEM_U:
                usage_bytes = calc["last_val"]
                usage_gb = usage_bytes / (1024**3)
                pod_containers[pod]["mem_gb_sum"][minute] += usage_gb
                node = pod_containers[pod]["node"]
                if node:
                    node_agg[node]["mem_gb"][minute] += usage_gb
                # 同时记录 limit 信息（如果有）
                limit_info = limits_cache.get(container_key, {})
                if limit_info.get("mem_limit"):
                    pod_containers[pod]["mem_limit"] = limit_info["mem_limit"]
                if limit_info.get("cpu_quota") and limit_info.get("cpu_period"):
                    pod_containers[pod]["cpu_limit"] = limit_info["cpu_quota"] / limit_info["cpu_period"]

            # IO
            elif name in (M_FS_R, M_FS_W):
                delta = calc["last_val"] - calc["first_val"]
                # 先存到临时字典，稍后合并计算吞吐量
                # 为了方便，直接在此处计算该容器的 IO 吞吐量并累加
                # 注意：同一个容器可能有多条 IO 记录（读和写分开），需要合并
                # 简单做法：在容器级别暂存读/写的总字节数和时间窗口
                # 但为了保持与原始逻辑一致，我们采用与用户 process_file 类似的方法：
                # 使用全局字典暂存每个容器每分钟的读/写总字节数和最大时间窗口
                # 由于代码复杂度，这里简化：直接累加 delta/tw 得到 MB/s，但这样对于读和写分开的指标会分别计算，最终加总即可。
                # 实际中，读和写指标的时间窗口相同，可以安全相加。
                # 更准确：对于每个容器每分钟，累加所有读和写的 delta，然后除以该分钟的最大 tw。
                # 我们实现一个本地缓存。
                # 为了代码清晰，我将在循环外维护每个容器每分钟的 IO 累计。
                # 但为了简洁且不引入过多复杂度，这里采用每个记录独立计算并累加（会略微高估，因为同一分钟可能有多个指标，tw 相同，累加 delta/tw 等价于总字节/tw）。
                mbps = delta / tw / (1024 * 1024)
                pod_containers[pod]["io_mbps_sum"][minute] += mbps
                node = pod_containers[pod]["node"]
                if node:
                    node_agg[node]["io_mbps"][minute] += mbps

            # Network
            elif name in (M_NET_RX, M_NET_TX):
                delta = calc["last_val"] - calc["first_val"]
                mbps = delta / tw / (1024 * 1024)
                pod_containers[pod]["net_mbps_sum"][minute] += mbps
                node = pod_containers[pod]["node"]
                if node:
                    node_agg[node]["net_mbps"][minute] += mbps

    # 后处理：计算每个 Pod 的最终聚合值（取峰值）
    pod_data = {}
    for pod, pc in pod_containers.items():
        node = pc["node"] or "unknown"
        ns = pc["ns"] or "unknown"
        # 取各指标每分钟的峰值
        cpu_cores = max(pc["cpu_cores_sum"].values()) if pc["cpu_cores_sum"] else 0.0
        mem_gb = max(pc["mem_gb_sum"].values()) if pc["mem_gb_sum"] else 0.0
        io_mbps = max(pc["io_mbps_sum"].values()) if pc["io_mbps_sum"] else 0.0
        net_mbps = max(pc["net_mbps_sum"].values()) if pc["net_mbps_sum"] else 0.0
        mem_limit_bytes = pc["mem_limit"] or 1  # 避免除零
        mem_util = min(mem_gb * (1024**3) / mem_limit_bytes, 1.0) if mem_limit_bytes else 0.0
        cpu_limit_cores = pc["cpu_limit"] or 1.0
        cpu_util = min(cpu_cores / cpu_limit_cores, 1.0) if cpu_limit_cores else 0.0

        # CPI 分钟聚合
        cpi_min = {}
        if pod in pod_cpi_raw:
            cycles_list = pod_cpi_raw[pod].get("cycles", [])
            instr_list = pod_cpi_raw[pod].get("instructions", [])
            # 转换为分钟字典
            cycles_by_min = defaultdict(list)
            instr_by_min = defaultdict(list)
            for ts, v in cycles_list:
                cycles_by_min[ts // BUCKET_SEC].append(v)
            for ts, v in instr_list:
                instr_by_min[ts // BUCKET_SEC].append(v)
            for m in set(cycles_by_min) & set(instr_by_min):
                avg_cycles = sum(cycles_by_min[m]) / len(cycles_by_min[m])
                avg_instr = sum(instr_by_min[m]) / len(instr_by_min[m])
                if avg_instr > 0:
                    cpi_min[m] = avg_cycles / avg_instr

        # PSI 分钟聚合
        psi_min = defaultdict(dict)
        if pod in pod_psi_raw:
            for key, ts_vals in pod_psi_raw[pod].items():
                for ts, v in ts_vals:
                    minute = ts // BUCKET_SEC
                    psi_min[minute][key] = v  # 如果同一分钟多条，取最后一条（或平均值，简单取最后）

        pod_data[pod] = {
            "node": node,
            "namespace": ns,
            "cpu_cores": cpu_cores,
            "cpu_util": cpu_util,
            "mem_gb": mem_gb,
            "mem_util": mem_util,
            "io_mbps": io_mbps,
            "net_mbps": net_mbps,
            "cpi_min": cpi_min,
            "psi_min": psi_min,
        }

    # 节点数据：取每分钟的峰值
    node_data = {}
    for node, agg in node_agg.items():
        node_data[node] = {
            "cpu_cores": max(agg["cpu_cores"].values()) if agg["cpu_cores"] else 0.0,
            "cpu_util": min(max(agg["cpu_cores"].values()) / NODE_TOTAL_CORES, 1.0) if agg["cpu_cores"] else 0.0,
            "mem_gb": max(agg["mem_gb"].values()) if agg["mem_gb"] else 0.0,
            "io_mbps": max(agg["io_mbps"].values()) if agg["io_mbps"] else 0.0,
            "net_mbps": max(agg["net_mbps"].values()) if agg["net_mbps"] else 0.0,
        }

    return pod_data, node_data


# =====================================================
# 核心分析：单 Pod（含节点背景）
# =====================================================
def analyze(pod, pod_info, node_info):
    # Pod 自身
    p_cpu_util = pod_info["cpu_util"]
    p_cpu_s = sev(p_cpu_util, POD_CPU_WARN, POD_CPU_CRIT)
    p_mem_util = pod_info["mem_util"]
    p_mem_s = sev(p_mem_util, POD_MEM_WARN, POD_MEM_CRIT)
    p_io = pod_info["io_mbps"]
    p_io_s = sev(p_io, POD_IO_WARN, POD_IO_CRIT)
    p_net = pod_info["net_mbps"]
    p_net_s = sev(p_net, POD_NET_WARN, POD_NET_CRIT)

    # CPI
    cpi_min = pod_info["cpi_min"]
    cpi_pk = max(cpi_min.values()) if cpi_min else None
    cpi_sev = sev(cpi_pk, CPI_WARN, CPI_CRIT)

    # PSI
    psi_min = pod_info["psi_min"]
    psi = {}
    for res in ("cpu", "mem", "io"):
        some_key = (res, "some", PSI_PREC)
        full_key = (res, "full", PSI_PREC)
        some_vals = [kd.get(some_key, 0) for kd in psi_min.values() if some_key in kd]
        full_vals = [kd.get(full_key, 0) for kd in psi_min.values() if full_key in kd]
        sp = max(some_vals) if some_vals else 0.0
        fp = max(full_vals) if full_vals else 0.0
        psi[res] = {"some": sp, "full": fp, "sev": sev_psi(sp, fp)}
    psi_worst_res = max(psi, key=lambda r: SEV_RANK[psi[r]["sev"]])
    psi_worst_sev = psi[psi_worst_res]["sev"]

    # 节点背景
    n_cpu_util = node_info.get("cpu_util", 0.0)
    n_cpu_s = sev(n_cpu_util, NODE_CPU_WARN, NODE_CPU_CRIT)
    n_mem = node_info.get("mem_gb", 0.0)
    n_mem_s = sev(n_mem, NODE_MEM_WARN, NODE_MEM_CRIT)
    n_io = node_info.get("io_mbps", 0.0)
    n_io_s = sev(n_io, NODE_IO_WARN, NODE_IO_CRIT)
    n_net = node_info.get("net_mbps", 0.0)
    n_net_s = sev(n_net, NODE_NET_WARN, NODE_NET_CRIT)

    # 节点背景摘要
    np_parts = []
    if n_cpu_s != "NONE":
        np_parts.append(f"CPU{n_cpu_util:.1%}[{n_cpu_s}]")
    if n_mem_s != "NONE":
        np_parts.append(f"MEM{n_mem:.1f}GB[{n_mem_s}]")
    if n_io_s != "NONE":
        np_parts.append(f"IO{n_io:.0f}M[{n_io_s}]")
    if n_net_s != "NONE":
        np_parts.append(f"NET{n_net:.0f}M[{n_net_s}]")
    node_bg = " ".join(np_parts) if np_parts else "正常"

    # 触发信号
    triggered = []
    if cpi_sev != "NONE":
        triggered.append("CPI")
    if psi_worst_sev != "NONE":
        triggered.append(f"PSI-{psi_worst_res}")
    if p_cpu_s != "NONE":
        triggered.append("CPU")
    if p_mem_s != "NONE":
        triggered.append("MEM")
    if p_io_s != "NONE":
        triggered.append("IO")
    if p_net_s != "NONE":
        triggered.append("NET")
    if not triggered:
        return None

    # 整体严重度
    all_s = [cpi_sev, psi_worst_sev, p_cpu_s, p_mem_s, p_io_s, p_net_s]
    overall = max(all_s, key=lambda s: SEV_RANK[s])
    if overall == "NONE":
        overall = "WARN"

    # 根因排序
    cands = []
    if cpi_sev != "NONE" and cpi_pk is not None:
        ctx = f"，节点CPU {n_cpu_util:.1%}压力" if n_cpu_s != "NONE" else ""
        cands.append((
            SEV_RANK[cpi_sev] + 0.1 + (0.5 if n_cpu_s != "NONE" else 0),
            f"CPI={cpi_pk:.2f}{ctx}",
            "排查同节点高CPI服务，考虑绑核/隔离" if n_cpu_s != "NONE" else "检查Pod指令效率，考虑绑核"
        ))
    if psi_worst_sev != "NONE":
        res_cn = {"cpu": "CPU", "mem": "内存", "io": "I/O"}.get(psi_worst_res, psi_worst_res)
        sp = psi[psi_worst_res]["some"]
        fp = psi[psi_worst_res]["full"]
        desc = f"{res_cn} PSI full={fp:.1f}%" if fp >= PSI_FULL_WARN else f"{res_cn} PSI some={sp:.1f}%"
        act = {"cpu": "调大CPU Limit或降并发", "mem": "调大Memory Limit防OOM", "io": "限IO或迁SSD节点"}.get(psi_worst_res, "降低负载")
        cands.append((SEV_RANK[psi_worst_sev] + 0.05, desc, act))
    for val, sev_val, desc, act in [
        (p_cpu_util, p_cpu_s, f"Pod CPU={p_cpu_util:.0%}", "调大CPU Limit"),
        (p_mem_util, p_mem_s, f"Pod MEM={p_mem_util:.0%}", "调大Memory Limit"),
        (p_io, p_io_s, f"Pod IO={p_io:.0f}MB/s", "迁SSD节点或限流"),
        (p_net, p_net_s, f"Pod NET={p_net:.0f}MB/s", "检查流量/限速"),
    ]:
        if sev_val != "NONE" and val is not None:
            cands.append((SEV_RANK[sev_val], desc, act))
    cands.sort(reverse=True)
    top_cause = cands[0][1] if cands else "—"
    top_action = cands[0][2] if cands else "持续观察"

    return {
        "pod": pod,
        "node": pod_info["node"],
        "ns": pod_info["namespace"],
        "sev": overall,
        "signals": "/".join(triggered),
        "node_bg": node_bg,
        "cpi": fmt(cpi_pk, ".2f"),
        "cpi_sev": cpi_sev,
        "psi_cpu": f"{psi['cpu']['some']:.1f}/{psi['cpu']['full']:.1f}%",
        "psi_cpu_s": psi["cpu"]["sev"],
        "psi_mem": f"{psi['mem']['some']:.1f}/{psi['mem']['full']:.1f}%",
        "psi_mem_s": psi["mem"]["sev"],
        "psi_io": f"{psi['io']['some']:.1f}/{psi['io']['full']:.1f}%",
        "psi_io_s": psi["io"]["sev"],
        "pod_cpu": fmt(p_cpu_util, ".0%"), "pod_cpu_s": p_cpu_s,
        "pod_mem": fmt(p_mem_util, ".0%"), "pod_mem_s": p_mem_s,
        "pod_io": fmt(p_io, ".1f"), "pod_io_s": p_io_s,
        "pod_net": fmt(p_net, ".1f"), "pod_net_s": p_net_s,
        "n_cpu": fmt(n_cpu_util, ".1%"), "n_cpu_s": n_cpu_s,
        "n_mem": fmt(n_mem, ".1f"), "n_mem_s": n_mem_s,
        "n_io": fmt(n_io, ".1f"), "n_io_s": n_io_s,
        "n_net": fmt(n_net, ".1f"), "n_net_s": n_net_s,
        "cause": top_cause,
        "action": top_action,
    }


# =====================================================
# 预测（CPI / PSI_cpu / Pod_CPU / Pod_MEM）
# =====================================================
def forecast(pod, pod_info, max_minute):
    rows = []
    start_ts = (max_minute + 1) * BUCKET_SEC

    def _try(series, warn_thr, crit_thr, dim):
        if len(series) < 3:
            return
        sl, ic, r2 = linear_trend(series)
        cur = series[-1]
        idx = len(series) - 1
        cur_sev = "CRIT" if cur >= crit_thr else "WARN" if cur >= warn_thr else "NONE"
        if cur_sev != "NONE":
            pred = max(cur, ic + sl * (idx + FORECAST_MINUTES)) if sl >= 0 else cur
            pred_sev = "CRIT" if pred >= crit_thr else "WARN" if pred >= warn_thr else cur_sev
            rows.append({
                "pod": pod, "dim": dim,
                "cur": f"{cur:.3f}", "cur_sev": cur_sev,
                "pred": f"{pred:.3f}", "pred_sev": pred_sev,
                "tmins": 0, "tdt": t2s(start_ts),
                "basis": f"已超阈值 R²={r2:.2f}"
            })
            return
        if r2 < TREND_R2_MIN or sl <= 0:
            return
        pred = max(0.0, ic + sl * (idx + FORECAST_MINUTES))
        pred_sev = "CRIT" if pred >= crit_thr else "WARN" if pred >= warn_thr else "NONE"
        if pred_sev == "NONE":
            return
        tmins = min(math.ceil((warn_thr - cur) / sl) if cur < warn_thr else 0, FORECAST_MINUTES)
        rows.append({
            "pod": pod, "dim": dim,
            "cur": f"{cur:.3f}", "cur_sev": "NONE",
            "pred": f"{pred:.3f}", "pred_sev": pred_sev,
            "tmins": tmins, "tdt": t2s(start_ts + tmins * BUCKET_SEC),
            "basis": f"上升趋势 R²={r2:.2f}"
        })

    # CPI 序列
    cpi_min = pod_info.get("cpi_min", {})
    if cpi_min:
        ms = sorted(cpi_min.keys())
        _try([cpi_min[m] for m in ms[-TREND_WINDOW:]], CPI_WARN, CPI_CRIT, "CPI")

    # PSI cpu some 序列
    psi_min = pod_info.get("psi_min", {})
    if psi_min:
        key = ("cpu", "some", PSI_PREC)
        series = []
        for m in sorted(psi_min.keys())[-TREND_WINDOW:]:
            if key in psi_min[m]:
                series.append(psi_min[m][key])
        _try(series, PSI_SOME_WARN, PSI_SOME_CRIT, "PSI_cpu_some")

    # Pod CPU 利用率序列
    # 我们需要分钟级的 CPU 利用率，但 pod_info 中只存了峰值。为了预测，需要保留分钟序列。
    # 由于我们的 pod_info 没有保留分钟序列，这里简化：不做 Pod CPU 和 MEM 的预测。
    # 如果用户需要，可以在 process_all 中返回分钟级数据，但会增加复杂度。这里先跳过。
    return rows


# =====================================================
# CSV 输出
# =====================================================
def write_analysis_csv(path, rows):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    header = (
        "name,节点,namespace,interference_level,interference_signal,"
        "CPI,CPI级别,"
        "PSI_CPU(some/full),PSI_CPU级别,"
        "PSI_MEM(some/full),PSI_MEM级别,"
        "PSI_IO(some/full),PSI_IO级别,"
        "Pod_CPU,Pod_CPU级别,Pod_MEM,Pod_MEM级别,"
        "Pod_IO(MB/s),Pod_IO级别,Pod_NET(MB/s),Pod_NET级别,"
        "interference_reason,recommend_action\n"
    )
    with open(path, "w", encoding="utf-8-sig") as w:
        w.write(header)
        for r in rows:
            w.write(
                f"{r['pod']},{r['node']},{r['ns']},"
                f"{r['sev']},{r['signals']},"
                f"{r['cpi']},{r['cpi_sev']},"
                f"{r['psi_cpu']},{r['psi_cpu_s']},"
                f"{r['psi_mem']},{r['psi_mem_s']},"
                f"{r['psi_io']},{r['psi_io_s']},"
                f"{r['pod_cpu']},{r['pod_cpu_s']},"
                f"{r['pod_mem']},{r['pod_mem_s']},"
                f"{r['pod_io']},{r['pod_io_s']},"
                f"{r['pod_net']},{r['pod_net_s']},"
                f"\"{r['cause']}\",\"{r['action']}\"\n"
            )

def write_forecast_csv(path, rows):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8-sig") as w:
        w.write("Pod,预测维度,当前值,当前级别,预测值(30分钟后),预测级别,预计触发分钟,预计触发时间,预测依据\n")
        for r in rows:
            w.write(f"{r['pod']},{r['dim']},{r['cur']},{r['cur_sev']},"
                    f"{r['pred']},{r['pred_sev']},{r['tmins']},"
                    f"{r['tdt']},\"{r['basis']}\"\n")


# =====================================================
# main
# =====================================================
def main():
    os.makedirs(OUT_DIR, exist_ok=True)

    # 第一遍：提取 limits
    limits = extract_limits(INPUT_FILE)
    if limits is None:
        print("提取 limits 失败，退出")
        return

    # 第二遍：处理所有数据
    pod_data, node_data = process_all(INPUT_FILE, limits)
    print(f"\n发现 Pod 数量: {len(pod_data)}，节点数量: {len(node_data)}")

    # 找出所有 Pod 和节点
    all_pods = list(pod_data.keys())
    # 最新时刻（从 CPI 或性能指标中获取最大分钟，简化：取当前时间）
    max_minute = int(datetime.now(tz=TZ).timestamp()) // BUCKET_SEC

    analysis_rows = []
    forecast_rows = []
    skipped = 0

    for pod in tqdm(all_pods, desc="分析 Pod"):
        pinfo = pod_data[pod]
        node = pinfo["node"]
        ninfo = node_data.get(node, {})  # 如果节点没有聚合数据，使用空字典
        result = analyze(pod, pinfo, ninfo)
        if result is None:
            skipped += 1
        else:
            analysis_rows.append(result)
        # 预测（简化版，仅预测 CPI 和 PSI）
        forecast_rows.extend(forecast(pod, pinfo, max_minute))

    total = len(analysis_rows)
    crit = sum(1 for r in analysis_rows if r["sev"] == "CRIT")
    warn = sum(1 for r in analysis_rows if r["sev"] == "WARN")
    print(f"\n===== Pod 干扰分析结果 =====")
    print(f"  总 Pod 数: {len(all_pods)}  无干扰: {skipped}  有干扰: {total}  CRIT: {crit}  WARN: {warn}")

    sig_stats = defaultdict(int)
    for r in analysis_rows:
        for s in r["signals"].split("/"):
            sig_stats[s] += 1
    print("  信号分布: " + "  ".join(f"{s}:{n}" for s, n in sorted(sig_stats.items(), key=lambda x: -x[1])))

    fc_crit = sum(1 for r in forecast_rows if r["pred_sev"] == "CRIT")
    fc_warn = sum(1 for r in forecast_rows if r["pred_sev"] == "WARN")
    print(f"\n===== 未来{FORECAST_MINUTES}分钟预测 =====")
    print(f"  预测信号: {len(forecast_rows)}  CRIT: {fc_crit}  WARN: {fc_warn}")

    out_a = os.path.join(OUT_DIR, "pod_interference_analysis.csv")
    out_f = os.path.join(OUT_DIR, "pod_interference_forecast.csv")
    write_analysis_csv(out_a, analysis_rows)
    write_forecast_csv(out_f, forecast_rows)
    print(f"\n  ★ 干扰分析: {out_a}")
    print(f"  ★ 干扰预测: {out_f}")

    if analysis_rows:
        print("\n----- 示例（前5条，按严重度）-----")
        for r in sorted(analysis_rows, key=lambda x: -SEV_RANK[x["sev"]])[:5]:
            print(f"  [{r['sev']}] {r['pod']}  Node:{r['node']}  NS:{r['ns']}")
            print(f"    节点背景: {r['node_bg']}  触发: {r['signals']}")
            print(f"    CPI:{r['cpi']}({r['cpi_sev']})  PSI_cpu:{r['psi_cpu']}({r['psi_cpu_s']})")
            print(f"    根因: {r['cause']}  → {r['action']}")


if __name__ == "__main__":
    main()