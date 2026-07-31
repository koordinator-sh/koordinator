import os
import json
import re
import math
from collections import defaultdict
from datetime import datetime, timezone, timedelta

from tqdm import tqdm

import torch
import torch.nn as nn
import torch.optim as optim

# ================== 参数 ==================
INPUT_JSONL = "/root/autodl-tmp/BurstDataLong/performance_with_resource_metrics_with_burst.jsonl"
OUT_DIR = "/root/autodl-tmp/BurstDataLong/ForecastOutputWorkloadLSTM"

BUCKET_SEC = 180         # 数据采样间隔是180秒
IN_LEN = 120             # 输入窗口：前120桶（= 6小时）历史
OUT_LEN = 480            # 预测窗口：未来480桶（= 1天）

# 优化训练参数
EPOCHS = 10              # 减少训练轮数
LR = 1e-3
HIDDEN = 8               # 减小隐藏层
NUM_LAYERS = 1
DROPOUT = 0.2
MC_SAMPLES = 5           # 减少MC采样次数

DEVICE = "cpu"
torch.set_num_threads(max(1, os.cpu_count() // 2))

CPU_WARN = 0.80
CPU_CRIT = 0.90
MEM_WARN = 0.80
MEM_CRIT = 0.90

TARGET_UTIL_WARN = 0.70
TARGET_UTIL_CRIT = 0.60
SAFETY_MARGIN = 1.10

BACKTEST_WINDOWS = 1     # 减少回测窗口数量
TZ = timezone(timedelta(hours=8))

# ============== 指标 ==============
METRIC_CPU_USAGE = "container_cpu_usage_seconds_total"
METRIC_CPU_QUOTA = "container_spec_cpu_quota"
METRIC_CPU_PERIOD = "container_spec_cpu_period"
METRIC_MEM_USAGE = "container_memory_usage_bytes"
METRIC_MEM_LIMIT = "container_spec_memory_limit_bytes"
METRIC_FS_READ   = "container_fs_reads_bytes_total"
METRIC_FS_WRITE  = "container_fs_writes_bytes_total"
METRIC_NET_RX    = "container_network_receive_bytes_total"
METRIC_NET_TX    = "container_network_transmit_bytes_total"

IO_WARN  = 50.0
IO_CRIT  = 200.0
NET_WARN = 100.0
NET_CRIT = 500.0

IDX_CPU = 0
IDX_MEM = 1
IDX_IO  = 2
IDX_NET = 3
N_DIM   = 4

# ========= workload 解析正则 ==========
DEPLOYMENT_RE = re.compile(r"^([a-z0-9][a-z0-9-]*)-([a-z0-9]{10})-([a-z0-9]{5})$")
JOB_RE = re.compile(r"^([a-z0-9][a-z0-9-]*)-(\d+)-([a-z0-9]{5})$")
STATEFULSET_RE = re.compile(r"^([a-z0-9][a-z0-9-]*)-(\d+)$")
DAEMONSET_RE = re.compile(r"^([a-z0-9][a-z0-9-]*)-([a-z0-9-]{5,})$")

def parse_workload(pod_name: str):
    if not pod_name:
        return None
    m = DEPLOYMENT_RE.match(pod_name)
    if m: return m.group(1)
    m = JOB_RE.match(pod_name)
    if m: return m.group(1)
    m = STATEFULSET_RE.match(pod_name)
    if m: return m.group(1)
    m = DAEMONSET_RE.match(pod_name)
    if m: return m.group(1)
    if "-" in pod_name:
        return pod_name.rsplit("-", 1)[0]
    return pod_name

def clamp01(x):
    if x is None or (isinstance(x, float) and math.isnan(x)):
        return 0.0
    return max(0.0, min(1.0, float(x)))

def ts_to_local_str(ts_sec: int):
    return datetime.fromtimestamp(ts_sec, tz=TZ).strftime("%Y-%m-%d %H:%M:%S")

def container_key(metric: dict):
    return metric.get("id") or metric.get("container") or metric.get("pod")

def parse_long_record(rec):
    tss = rec.get("timestamps", [])
    vals = rec.get("values", [])
    if not tss or not vals or len(tss) != len(vals):
        return []
    result = []
    for raw_ts, v in zip(tss, vals):
        try:
            if raw_ts > 1e12:
                ts_sec = int(raw_ts) // 1000
            else:
                ts_sec = int(raw_ts)
            fv = float(v)
            if math.isfinite(fv):
                result.append((ts_sec, fv))
        except (ValueError, TypeError):
            continue
    return result

def analyze_data_format(input_jsonl):
    print("\n========== 数据格式分析 ==========")
    sample_count = 0
    total_records = 0
    min_ts_global, max_ts_global = None, None
    metric_types = defaultdict(int)
    sample_lengths = []

    with open(input_jsonl, "r", encoding="utf-8") as f:
        for line in f:
            if not line.strip(): continue
            try:
                rec = json.loads(line)
            except:
                continue
            if not isinstance(rec, dict):
                continue
            total_records += 1
            metric = rec.get("metric", {})
            name = metric.get("__name__", "unknown")
            metric_types[name] += 1

            pairs = parse_long_record(rec)
            if not pairs: continue

            sample_lengths.append(len(pairs))
            first_ts, last_ts = pairs[0][0], pairs[-1][0]
            if min_ts_global is None or first_ts < min_ts_global:
                min_ts_global = first_ts
            if max_ts_global is None or last_ts > max_ts_global:
                max_ts_global = last_ts

            if sample_count < 5:
                pod = metric.get("pod", "N/A")
                print(f"[样例 {sample_count+1}] 指标: {name}, Pod: {pod}")
                print(f"       时间范围: {ts_to_local_str(first_ts)} -> {ts_to_local_str(last_ts)}")
                print(f"       采样点数: {len(pairs)}")
                print(f"       前3个原始时间戳: {rec.get('timestamps', [])[:3]}")
                sample_count += 1

    if total_records == 0:
        print("错误：文件为空或无有效JSON记录。")
        return

    print(f"\n总记录数: {total_records}")
    if min_ts_global and max_ts_global:
        span_min = (max_ts_global - min_ts_global) // 60
        print(f"全局时间范围: {ts_to_local_str(min_ts_global)} -> {ts_to_local_str(max_ts_global)}")
        print(f"覆盖分钟数: {span_min}（约 {span_min/60:.1f} 小时）")
        print(f"按 {BUCKET_SEC}s 分桶共: {(max_ts_global-min_ts_global)//BUCKET_SEC} 桶")
    if sample_lengths:
        print(f"每条记录平均采样点数: {sum(sample_lengths)/len(sample_lengths):.1f}")
    print("指标类型分布:")
    for name, cnt in metric_types.items():
        print(f"  {name}: {cnt}")
    print("===================================\n")

def pass1_extract_limits(input_jsonl):
    mem_limit = {}
    cpu_quota = {}
    cpu_period = {}

    with open(input_jsonl, "r", encoding="utf-8") as f:
        for line in tqdm(f, desc="Pass1: extract limits/quota/period"):
            if not line.strip(): continue
            try:
                rec = json.loads(line)
            except:
                continue
            if not isinstance(rec, dict):
                continue
            metric = rec.get("metric", {})
            name = metric.get("__name__")
            ck = container_key(metric)
            if not ck: continue

            pairs = parse_long_record(rec)
            if not pairs: continue
            last_val = pairs[-1][1]

            if name == METRIC_MEM_LIMIT and last_val > 0:
                mem_limit[ck] = last_val
            elif name == METRIC_CPU_QUOTA:
                cpu_quota[ck] = last_val
            elif name == METRIC_CPU_PERIOD and last_val > 0:
                cpu_period[ck] = last_val

    print(f"  mem_limit 覆盖容器数: {len(mem_limit)}")
    print(f"  cpu_quota 覆盖容器数: {len(cpu_quota)}  （0表示数据中无此指标，CPU维度将全为0）")
    return mem_limit, cpu_quota, cpu_period

def pass2_build_workload_series(input_jsonl, mem_limit, cpu_quota, cpu_period):
    container_cpu_raw = defaultdict(list)
    container_mem_raw = defaultdict(list)
    container_io_raw  = defaultdict(list)
    container_net_raw = defaultdict(list)
    container_meta = {}

    with open(input_jsonl, "r", encoding="utf-8") as f:
        for line in tqdm(f, desc="Pass2: collecting raw points"):
            if not line.strip(): continue
            try:
                rec = json.loads(line)
            except:
                continue
            if not isinstance(rec, dict):
                continue
            metric = rec.get("metric", {})
            name = metric.get("__name__")
            pod = metric.get("pod")
            namespace = metric.get("namespace", "")
            wl = parse_workload(pod) if pod else None
            if wl is None: continue

            ck = container_key(metric)
            if not ck: continue
            if ck not in container_meta:
                container_meta[ck] = (wl, pod, namespace)

            pairs = parse_long_record(rec)
            if not pairs: continue

            if name == METRIC_CPU_USAGE:
                container_cpu_raw[ck].extend(pairs)
            elif name == METRIC_MEM_USAGE:
                container_mem_raw[ck].extend(pairs)
            elif name in (METRIC_FS_READ, METRIC_FS_WRITE):
                container_io_raw[ck].extend(pairs)
            elif name in (METRIC_NET_RX, METRIC_NET_TX):
                container_net_raw[ck].extend(pairs)

    print("Pass2: 开始按容器聚合分钟桶...")

    wl_cpu = defaultdict(lambda: defaultdict(lambda: [0.0, 0.0]))
    wl_mem = defaultdict(lambda: defaultdict(lambda: [0.0, 0.0]))
    wl_io  = defaultdict(lambda: defaultdict(float))
    wl_net = defaultdict(lambda: defaultdict(float))
    wl_pods = defaultdict(lambda: defaultdict(set))
    wl_namespace = {}
    min_minute, max_minute = None, None

    cpu_skipped = 0
    for ck, raw_pairs in tqdm(container_cpu_raw.items(), desc="Processing CPU containers"):
        if len(raw_pairs) < 2: continue
        wl, pod, namespace = container_meta[ck]
        wl_namespace[wl] = namespace
        q = cpu_quota.get(ck)
        p = cpu_period.get(ck)
        if q is None or p is None or p <= 0 or q <= 0:
            cpu_skipped += 1
            continue
        limit_cores = q / p

        raw_pairs.sort(key=lambda x: x[0])
        for i in range(len(raw_pairs) - 1):
            t0, v0 = raw_pairs[i]
            t1, v1 = raw_pairs[i+1]
            if t1 <= t0: continue
            delta = v1 - v0
            if delta < 0: delta = v1
            duration = t1 - t0
            if duration <= 0: continue
            rate = delta / duration
            start_min = t0 // BUCKET_SEC
            end_min = t1 // BUCKET_SEC
            for m in range(start_min, end_min + 1):
                seg_start = max(t0, m * BUCKET_SEC)
                seg_end = min(t1, (m + 1) * BUCKET_SEC)
                seg_len = max(0, seg_end - seg_start)
                wl_cpu[wl][m][0] += rate * seg_len
                wl_cpu[wl][m][1] += limit_cores * (seg_len / BUCKET_SEC)
                if pod: wl_pods[wl][m].add(pod)
                if min_minute is None or m < min_minute: min_minute = m
                if max_minute is None or m > max_minute: max_minute = m

    if cpu_skipped > 0:
        print(f"  ⚠ CPU: {cpu_skipped} 个容器因缺少 quota/period 被跳过")

    mem_skipped = 0
    for ck, raw_pairs in tqdm(container_mem_raw.items(), desc="Processing MEM containers"):
        wl, pod, namespace = container_meta[ck]
        wl_namespace[wl] = namespace
        lim = mem_limit.get(ck)
        if lim is None or lim <= 0:
            mem_skipped += 1
            continue

        minute_last = {}
        for ts_sec, v in raw_pairs:
            m = ts_sec // BUCKET_SEC
            minute_last[m] = v
        for m, usage in minute_last.items():
            wl_mem[wl][m][0] += usage
            wl_mem[wl][m][1] += lim
            if pod: wl_pods[wl][m].add(pod)
            if min_minute is None or m < min_minute: min_minute = m
            if max_minute is None or m > max_minute: max_minute = m

    if mem_skipped > 0:
        print(f"  ⚠ MEM: {mem_skipped} 个容器因缺少 mem_limit 被跳过")

    def _agg_counter_mbps_outer(raw_dict, wl_target):
        nonlocal min_minute, max_minute
        for ck, raw_pairs in raw_dict.items():
            if ck not in container_meta: continue
            if len(raw_pairs) < 2: continue
            wl, pod, namespace = container_meta[ck]
            wl_namespace[wl] = namespace
            raw_pairs.sort(key=lambda x: x[0])
            for i in range(len(raw_pairs) - 1):
                t0, v0 = raw_pairs[i]
                t1, v1 = raw_pairs[i+1]
                if t1 <= t0: continue
                delta = max(v1 - v0, 0.0)
                duration = t1 - t0
                mbps = delta / duration / (1024 * 1024)
                m = t0 // BUCKET_SEC
                wl_target[wl][m] += mbps
                if pod: wl_pods[wl][m].add(pod)
                if min_minute is None or m < min_minute: min_minute = m
                if max_minute is None or m > max_minute: max_minute = m

    print("Processing IO containers...")
    _agg_counter_mbps_outer(container_io_raw, wl_io)
    print("Processing NET containers...")
    _agg_counter_mbps_outer(container_net_raw, wl_net)

    if min_minute is None or max_minute is None:
        return {}, None, None, {}, {}

    all_minutes = list(range(min_minute, max_minute + 1))
    workloads = sorted(set(wl_cpu.keys()) | set(wl_mem.keys()) | set(wl_io.keys()) | set(wl_net.keys()))
    print(f"  分桶范围: {min_minute} ~ {max_minute}，共 {len(all_minutes)} 桶（{len(all_minutes)*BUCKET_SEC//60} 分钟）")

    wl_series = {}
    for wl in workloads:
        cpu_vals, mem_vals, io_vals, net_vals = [], [], [], []
        last_cpu, last_mem, last_io, last_net = 0.0, 0.0, 0.0, 0.0
        for m in all_minutes:
            if m in wl_cpu[wl]:
                u, l = wl_cpu[wl][m]
                avg_usage = u / BUCKET_SEC
                avg_limit = l
                if avg_limit > 0:
                    last_cpu = clamp01(avg_usage / avg_limit)
            cpu_vals.append(last_cpu)

            if m in wl_mem[wl]:
                u, l = wl_mem[wl][m]
                if l > 0:
                    last_mem = clamp01(u / l)
            mem_vals.append(last_mem)

            if m in wl_io[wl]:
                last_io = max(0.0, wl_io[wl][m])
            io_vals.append(last_io)

            if m in wl_net[wl]:
                last_net = max(0.0, wl_net[wl][m])
            net_vals.append(last_net)

        wl_series[wl] = [
            [cpu_vals[i], mem_vals[i], io_vals[i], net_vals[i]]
            for i in range(len(all_minutes))
        ]

    return wl_series, min_minute, max_minute, wl_pods, wl_namespace

class OneStepLSTM(nn.Module):
    def __init__(self, input_dim=N_DIM, hidden=HIDDEN, num_layers=NUM_LAYERS, dropout=DROPOUT, out_dim=N_DIM):
        super().__init__()
        self.lstm = nn.LSTM(input_dim, hidden, num_layers=num_layers, batch_first=True,
                            dropout=dropout if num_layers > 1 else 0.0)
        self.dropout = nn.Dropout(dropout)
        self.fc = nn.Linear(hidden, out_dim)

    def forward(self, x):
        out, _ = self.lstm(x)
        return self.fc(self.dropout(out[:, -1, :]))

def train_lstm_one_step(series_2d, cutoff_idx=None, batch_size=64, epochs=EPOCHS, patience=3, max_samples=1000):
    data = series_2d[:cutoff_idx] if cutoff_idx is not None else series_2d
    X, y = [], []
    for t in range(IN_LEN, len(data)):
        X.append(data[t-IN_LEN:t])
        y.append(data[t])
    n = len(X)
    if n < 10:
        return None
    if n > max_samples:
        indices = torch.randperm(n)[:max_samples].tolist()
        X = [X[i] for i in indices]
        y = [y[i] for i in indices]
        n = max_samples
    batch_size = max(1, min(batch_size, n))
    X_t = torch.tensor(X, dtype=torch.float32, device=DEVICE)
    y_t = torch.tensor(y, dtype=torch.float32, device=DEVICE)
    model = OneStepLSTM().to(DEVICE)
    opt = optim.Adam(model.parameters(), lr=LR)
    loss_fn = nn.MSELoss()
    best_loss = float("inf")
    bad = 0
    model.train()
    pbar = tqdm(range(epochs), desc="   LSTM training", leave=False)
    for _ in pbar:
        idx = torch.randperm(n)
        epoch_loss = 0.0
        steps = 0
        for s in range(0, n, batch_size):
            b = idx[s:s+batch_size]
            opt.zero_grad()
            loss = loss_fn(model(X_t[b]), y_t[b])
            loss.backward()
            torch.nn.utils.clip_grad_norm_(model.parameters(), 1.0)
            opt.step()
            epoch_loss += float(loss.item())
            steps += 1
        epoch_loss /= max(1, steps)
        pbar.set_postfix({"loss": f"{epoch_loss:.4f}"})
        if epoch_loss + 1e-8 < best_loss:
            best_loss = epoch_loss
            bad = 0
        else:
            bad += 1
            if bad >= patience:
                break
    return model

def mc_dropout_forecast(model, history_2d, out_len, mc_samples):
    model.train()  # enable dropout
    x0 = torch.tensor([history_2d[-IN_LEN:]], dtype=torch.float32, device=DEVICE)
    all_samples = []
    with torch.no_grad():
        for _ in range(mc_samples):
            x = x0.clone()
            out_seq = []
            # 预分配列表，避免动态 cat
            for _ in range(out_len):
                raw = model(x).squeeze(0).cpu().numpy()
                y_next = [
                    clamp01(raw[IDX_CPU]),
                    clamp01(raw[IDX_MEM]),
                    max(0.0, raw[IDX_IO]),
                    max(0.0, raw[IDX_NET]),
                ]
                out_seq.append(y_next)
                # 更新输入：去掉第一个时间步，加上预测值
                new_x = torch.cat([x[:, 1:, :], torch.tensor([[y_next]], dtype=torch.float32, device=DEVICE)], dim=1)
                x = new_x
            all_samples.append(out_seq)
    s = torch.tensor(all_samples, dtype=torch.float32)
    return (s.mean(dim=0).numpy().tolist(),
            s.quantile(0.025, dim=0).numpy().tolist(),
            s.quantile(0.975, dim=0).numpy().tolist())

def detect_severity(peak, warn, crit):
    if peak >= crit: return "CRIT"
    if peak >= warn: return "WARN"
    return None

def recommend_pods(current_replicas, peak_util, severity):
    if current_replicas <= 0: current_replicas = 1
    target = TARGET_UTIL_CRIT if severity == "CRIT" else TARGET_UTIL_WARN
    new_total = max(current_replicas, int(math.ceil(current_replicas * (peak_util / max(target, 1e-6)) * SAFETY_MARGIN)))
    return new_total - current_replicas, new_total

def first_trigger_idx(seq, thr):
    for i, v in enumerate(seq):
        if v >= thr: return i
    return None

def get_pod_count_at(wl_pods, wl, minute, min_m):
    for m in range(minute, max(min_m-1, minute-5), -1):
        pods = wl_pods[wl].get(m)
        if pods: return len(pods)
    return 0

def backtest_workload(wl, namespace, series_2d, wl_pods, min_m, max_m, n_windows):
    T = len(series_2d)
    if T < IN_LEN + OUT_LEN * (n_windows + 1):
        return []
    backtest_rows = []
    for k in range(n_windows):
        pred_start_idx = T - OUT_LEN - k * OUT_LEN
        if pred_start_idx < IN_LEN: break
        model = train_lstm_one_step(series_2d, cutoff_idx=pred_start_idx)
        if model is None: continue
        history = series_2d[pred_start_idx-IN_LEN:pred_start_idx]
        mean, low, high = mc_dropout_forecast(model, history, OUT_LEN, MC_SAMPLES)
        # 提取各维度
        pred_cpu_high = [high[i][IDX_CPU] for i in range(OUT_LEN)]
        pred_mem_high = [high[i][IDX_MEM] for i in range(OUT_LEN)]
        pred_io_high  = [high[i][IDX_IO]  for i in range(OUT_LEN)]
        pred_net_high = [high[i][IDX_NET] for i in range(OUT_LEN)]

        gt_cpu = [series_2d[pred_start_idx+i][IDX_CPU] for i in range(OUT_LEN)]
        gt_mem = [series_2d[pred_start_idx+i][IDX_MEM] for i in range(OUT_LEN)]
        gt_io  = [series_2d[pred_start_idx+i][IDX_IO]  for i in range(OUT_LEN)]
        gt_net = [series_2d[pred_start_idx+i][IDX_NET] for i in range(OUT_LEN)]

        # MAE
        cpu_mae = sum(abs(mean[i][IDX_CPU] - gt_cpu[i]) for i in range(OUT_LEN))/OUT_LEN
        mem_mae = sum(abs(mean[i][IDX_MEM] - gt_mem[i]) for i in range(OUT_LEN))/OUT_LEN
        io_mae  = sum(abs(mean[i][IDX_IO]  - gt_io[i])  for i in range(OUT_LEN))/OUT_LEN
        net_mae = sum(abs(mean[i][IDX_NET] - gt_net[i]) for i in range(OUT_LEN))/OUT_LEN

        cpu_pred_peak = max(pred_cpu_high); mem_pred_peak = max(pred_mem_high)
        io_pred_peak  = max(pred_io_high);  net_pred_peak = max(pred_net_high)
        cpu_gt_peak = max(gt_cpu); mem_gt_peak = max(gt_mem)
        io_gt_peak  = max(gt_io);  net_gt_peak = max(gt_net)

        cpu_pred_sev = detect_severity(cpu_pred_peak, CPU_WARN, CPU_CRIT)
        mem_pred_sev = detect_severity(mem_pred_peak, MEM_WARN, MEM_CRIT)
        io_pred_sev  = detect_severity(io_pred_peak,  IO_WARN,  IO_CRIT)
        net_pred_sev = detect_severity(net_pred_peak, NET_WARN, NET_CRIT)
        cpu_gt_sev = detect_severity(cpu_gt_peak, CPU_WARN, CPU_CRIT)
        mem_gt_sev = detect_severity(mem_gt_peak, MEM_WARN, MEM_CRIT)
        io_gt_sev  = detect_severity(io_gt_peak,  IO_WARN,  IO_CRIT)
        net_gt_sev = detect_severity(net_gt_peak, NET_WARN, NET_CRIT)

        pred_start_minute = min_m + pred_start_idx
        pods_before = get_pod_count_at(wl_pods, wl, pred_start_minute-1, min_m)
        cur_replicas = max(1, pods_before)
        cpu_add_pred = 0
        if cpu_pred_sev: cpu_add_pred, _ = recommend_pods(cur_replicas, cpu_pred_peak, cpu_pred_sev)
        mem_add_pred = 0
        if mem_pred_sev: mem_add_pred, _ = recommend_pods(cur_replicas, mem_pred_peak, mem_pred_sev)
        max_pods_gt = pods_before
        for i in range(OUT_LEN):
            cnt = len(wl_pods[wl].get(pred_start_minute+i, set()))
            if cnt > max_pods_gt: max_pods_gt = cnt
        actual_inc = max(0, max_pods_gt - pods_before)
        def scale_hit(add_pred, actual_inc):
            return (add_pred > 0 and actual_inc > 0) or (add_pred == 0 and actual_inc == 0)

        backtest_rows.append({
            "namespace": namespace, "workload": wl, "window_index": k+1,
            "pred_start_minute": pred_start_minute,
            "window_start_dt": ts_to_local_str(pred_start_minute*BUCKET_SEC),
            "window_end_dt": ts_to_local_str((pred_start_minute+OUT_LEN-1)*BUCKET_SEC),
            "cpu_mae": f"{cpu_mae:.4f}", "mem_mae": f"{mem_mae:.4f}",
            "io_mae":  f"{io_mae:.4f}",  "net_mae": f"{net_mae:.4f}",
            "cpu_gt_peak": f"{cpu_gt_peak:.4f}", "cpu_pred_peak_high": f"{cpu_pred_peak:.4f}",
            "cpu_gt_sev": cpu_gt_sev or "NONE", "cpu_pred_sev": cpu_pred_sev or "NONE",
            "cpu_alert_match": "HIT" if cpu_pred_sev == cpu_gt_sev else "MISS",
            "mem_gt_peak": f"{mem_gt_peak:.4f}", "mem_pred_peak_high": f"{mem_pred_peak:.4f}",
            "mem_gt_sev": mem_gt_sev or "NONE", "mem_pred_sev": mem_pred_sev or "NONE",
            "mem_alert_match": "HIT" if mem_pred_sev == mem_gt_sev else "MISS",
            "io_gt_peak":  f"{io_gt_peak:.4f}",  "io_pred_peak_high":  f"{io_pred_peak:.4f}",
            "io_gt_sev":   io_gt_sev  or "NONE",  "io_pred_sev":   io_pred_sev  or "NONE",
            "io_alert_match":  "HIT" if io_pred_sev  == io_gt_sev  else "MISS",
            "net_gt_peak": f"{net_gt_peak:.4f}", "net_pred_peak_high": f"{net_pred_peak:.4f}",
            "net_gt_sev":  net_gt_sev or "NONE",  "net_pred_sev":  net_pred_sev  or "NONE",
            "net_alert_match": "HIT" if net_pred_sev == net_gt_sev else "MISS",
            "pods_before": pods_before, "actual_pods_increased": actual_inc,
            "cpu_add_pred": cpu_add_pred, "mem_add_pred": mem_add_pred,
            "cpu_scale_hit": "HIT" if scale_hit(cpu_add_pred, actual_inc) else "MISS",
            "mem_scale_hit": "HIT" if scale_hit(mem_add_pred, actual_inc) else "MISS"
        })
    return backtest_rows

def ensure_out_dir():
    os.makedirs(OUT_DIR, exist_ok=True)

def write_forecast_csv(path, rows):
    with open(path, "w", encoding="utf-8-sig") as w:
        w.write("namespace,工作负载,维度,预测发生桶数,未来时间戳,本地时间,预测均值,CI下界,CI上界\n")
        for r in rows:
            w.write(f"{r['namespace']},{r['workload']},{r['dimension']},{r['minute_offset']},{r['ts']},{r['dt']},{r['mean']},{r['low']},{r['high']}\n")

def write_alert_reco_csv(path, rows):
    with open(path, "w", encoding="utf-8-sig") as w:
        # w.write("namespace,工作负载,维度,告警级别,触发原因,阈值,预测峰值(CI上界),当前Pod数,建议新增Pod数,建议总Pod数,首次触发时间\n")
        w.write("namespace,name,维度,告警级别,触发原因,阈值,预测峰值(CI上界),current_replicas,recommend_replicas,total_replicas,predicted_at\n")
        for r in rows:
            w.write(f"{r['namespace']},{r['workload']},{r['dimension']},{r['severity']},{r['reason']},{r['threshold']},{r['peak_high']},{r['cur_pods']},{r['add_pods']},{r['new_pods']},{r['first_dt']}\n")

def write_backtest_detail_csv(path, rows):
    with open(path, "w", encoding="utf-8-sig") as w:
        w.write("namespace,工作负载,回测窗口序号,预测起始桶,窗口起始时间,窗口结束时间,"
                "CPU_MAE,MEM_MAE,IO_MAE(MB/s),NET_MAE(MB/s),"
                "CPU真实峰值,CPU预测峰值(CI上界),CPU告警_真实,CPU告警_预测,CPU告警命中,"
                "MEM真实峰值,MEM预测峰值(CI上界),MEM告警_真实,MEM告警_预测,MEM告警命中,"
                "IO真实峰值(MB/s),IO预测峰值(CI上界),IO告警_真实,IO告警_预测,IO告警命中,"
                "NET真实峰值(MB/s),NET预测峰值(CI上界),NET告警_真实,NET告警_预测,NET告警命中,"
                "扩容前Pod数,实际增加Pod数,CPU建议增加Pod数,MEM建议增加Pod数,CPU扩容命中,MEM扩容命中\n")
        for r in rows:
            w.write(f"{r['namespace']},{r['workload']},{r['window_index']},{r['pred_start_minute']},"
                    f"{r['window_start_dt']},{r['window_end_dt']},"
                    f"{r['cpu_mae']},{r['mem_mae']},{r['io_mae']},{r['net_mae']},"
                    f"{r['cpu_gt_peak']},{r['cpu_pred_peak_high']},{r['cpu_gt_sev']},{r['cpu_pred_sev']},{r['cpu_alert_match']},"
                    f"{r['mem_gt_peak']},{r['mem_pred_peak_high']},{r['mem_gt_sev']},{r['mem_pred_sev']},{r['mem_alert_match']},"
                    f"{r['io_gt_peak']},{r['io_pred_peak_high']},{r['io_gt_sev']},{r['io_pred_sev']},{r['io_alert_match']},"
                    f"{r['net_gt_peak']},{r['net_pred_peak_high']},{r['net_gt_sev']},{r['net_pred_sev']},{r['net_alert_match']},"
                    f"{r['pods_before']},{r['actual_pods_increased']},{r['cpu_add_pred']},{r['mem_add_pred']},"
                    f"{r['cpu_scale_hit']},{r['mem_scale_hit']}\n")

def write_backtest_summary_csv(path, rows):
    with open(path, "w", encoding="utf-8-sig") as w:
        w.write("指标,数值\n")
        for k, v in rows: w.write(f"{k},{v}\n")

def main():
    ensure_out_dir()
    if not os.path.exists(INPUT_JSONL):
        raise FileNotFoundError(f"文件不存在: {INPUT_JSONL}")

    analyze_data_format(INPUT_JSONL)

    mem_limit, cpu_quota, cpu_period = pass1_extract_limits(INPUT_JSONL)
    wl_series, min_m, max_m, wl_pods, wl_namespace = pass2_build_workload_series(
        INPUT_JSONL, mem_limit, cpu_quota, cpu_period)

    if min_m is None:
        print("未生成任何workload序列，请检查数据。")
        return

    start_future_bucket = max_m + 1
    print(f"\n数据时间范围: {ts_to_local_str(min_m * BUCKET_SEC)} ~ {ts_to_local_str(max_m * BUCKET_SEC)}")
    print(f"预测起始时间: {ts_to_local_str(start_future_bucket * BUCKET_SEC)}")
    print(f"workload 数量: {len(wl_series)}")
    min_needed = IN_LEN + OUT_LEN * (BACKTEST_WINDOWS + 1)
    print(f"回测所需最少桶数: {min_needed}（= {min_needed*BUCKET_SEC//60} 分钟）")

    # 阶段一：实际预测
    print("\n[阶段一] 对未来一天做实际预测...")
    forecast_rows = []
    alert_rows = []

    def get_cur_pods(wl):
        recent_start = max(min_m, max_m - IN_LEN + 1)
        pods = set()
        for m in range(recent_start, max_m + 1):
            pods.update(wl_pods[wl].get(m, set()))
        return max(1, len(pods))

    for idx, (wl, series_2d) in enumerate(tqdm(list(wl_series.items()), desc="[阶段一] 实际预测")):
        if len(series_2d) < (IN_LEN + OUT_LEN):
            continue
        model = train_lstm_one_step(series_2d)
        if model is None:
            continue
        namespace = wl_namespace.get(wl, "")
        mean, low, high = mc_dropout_forecast(model, series_2d, OUT_LEN, MC_SAMPLES)

        for i in range(OUT_LEN):
            bucket = start_future_bucket + i
            ts_sec = bucket * BUCKET_SEC
            dt = ts_to_local_str(ts_sec)
            for dim_name, idx in [("cpu_util", IDX_CPU), ("mem_util", IDX_MEM),
                                   ("io_mbps",  IDX_IO),  ("net_mbps", IDX_NET)]:
                forecast_rows.append({"namespace": namespace, "workload": wl, "dimension": dim_name,
                    "minute_offset": i+1, "ts": ts_sec, "dt": dt,
                    "mean": f"{mean[i][idx]:.4f}",
                    "low":  f"{low[i][idx]:.4f}",
                    "high": f"{high[i][idx]:.4f}"})

        cur_pods = get_cur_pods(wl)
        for dim, idx, warn, crit in [
            ("cpu_util", IDX_CPU, CPU_WARN,  CPU_CRIT),
            ("mem_util", IDX_MEM, MEM_WARN,  MEM_CRIT),
            ("io_mbps",  IDX_IO,  IO_WARN,   IO_CRIT),
            ("net_mbps", IDX_NET, NET_WARN,  NET_CRIT),
        ]:
            high_seq = [high[i][idx] for i in range(OUT_LEN)]
            peak = max(high_seq) if high_seq else 0.0
            sev = detect_severity(peak, warn, crit)
            if sev:
                thr = crit if sev == "CRIT" else warn
                fidx = first_trigger_idx(high_seq, thr) or 0
                first_b = start_future_bucket + fidx
                add, new = recommend_pods(cur_pods, peak, sev)
                alert_rows.append({"namespace": namespace, "workload": wl, "dimension": dim,
                    "severity": sev, "reason": "ci_high_exceed", "threshold": thr,
                    "peak_high": f"{peak:.4f}", "cur_pods": cur_pods,
                    "add_pods": add, "new_pods": new,
                    "first_dt": ts_to_local_str(first_b * BUCKET_SEC)})

    # 阶段二：回测（如果BACKTEST_WINDOWS > 0）
    all_backtest_rows = []
    if BACKTEST_WINDOWS > 0:
        print(f"\n[阶段二] 滑窗回测（每个workload做{BACKTEST_WINDOWS}个窗口）...")
        for wl, series_2d in tqdm(wl_series.items(), desc="[阶段二] 滑窗回测"):
            namespace = wl_namespace.get(wl, "")
            rows = backtest_workload(wl, namespace, series_2d, wl_pods, min_m, max_m, BACKTEST_WINDOWS)
            all_backtest_rows.extend(rows)

    total = len(all_backtest_rows)
    if total == 0:
        if BACKTEST_WINDOWS > 0:
            print(f"\n警告：回测结果为空。需要至少 {IN_LEN + OUT_LEN*(BACKTEST_WINDOWS+1)} 桶序列")
        backtest_summary = [("警告", "数据不足或回测未执行")]
    else:
        def pct(n): return f"{n/total*100:.2f}"
        cpu_alert_hits = sum(1 for r in all_backtest_rows if r["cpu_alert_match"] == "HIT")
        mem_alert_hits = sum(1 for r in all_backtest_rows if r["mem_alert_match"] == "HIT")
        io_alert_hits  = sum(1 for r in all_backtest_rows if r["io_alert_match"]  == "HIT")
        net_alert_hits = sum(1 for r in all_backtest_rows if r["net_alert_match"] == "HIT")
        cpu_scale_hits = sum(1 for r in all_backtest_rows if r["cpu_scale_hit"] == "HIT")
        mem_scale_hits = sum(1 for r in all_backtest_rows if r["mem_scale_hit"] == "HIT")
        avg_cpu_mae = sum(float(r["cpu_mae"]) for r in all_backtest_rows)/total
        avg_mem_mae = sum(float(r["mem_mae"]) for r in all_backtest_rows)/total
        avg_io_mae  = sum(float(r["io_mae"])  for r in all_backtest_rows)/total
        avg_net_mae = sum(float(r["net_mae"]) for r in all_backtest_rows)/total
        backtest_summary = [
            ("回测窗口总数", total),
            ("涉及workload数", len(set(r["workload"] for r in all_backtest_rows))),
            ("", ""), ("── 利用率预测误差 ──", ""),
            ("平均CPU利用率MAE",    f"{avg_cpu_mae:.4f}"),
            ("平均MEM利用率MAE",    f"{avg_mem_mae:.4f}"),
            ("平均IO吞吐MAE(MB/s)", f"{avg_io_mae:.4f}"),
            ("平均NET吞吐MAE(MB/s)",f"{avg_net_mae:.4f}"),
            ("", ""), ("── 告警级别准确率 ──", ""),
            ("CPU告警命中数", cpu_alert_hits), ("CPU告警准确率(%)", pct(cpu_alert_hits)),
            ("MEM告警命中数", mem_alert_hits), ("MEM告警准确率(%)", pct(mem_alert_hits)),
            ("IO告警命中数",  io_alert_hits),  ("IO告警准确率(%)",  pct(io_alert_hits)),
            ("NET告警命中数", net_alert_hits), ("NET告警准确率(%)", pct(net_alert_hits)),
            ("", ""), ("── 扩容建议准确率 ──", ""),
            ("CPU扩容建议命中数", cpu_scale_hits), ("CPU扩容建议准确率(%)", pct(cpu_scale_hits)),
            ("MEM扩容建议命中数", mem_scale_hits), ("MEM扩容建议准确率(%)", pct(mem_scale_hits))
        ]

    write_forecast_csv(os.path.join(OUT_DIR, "workload_forecast_1d_lstm.csv"), forecast_rows)
    write_alert_reco_csv(os.path.join(OUT_DIR, "workload_alerts_and_scale_pods_1d_lstm.csv"), alert_rows)
    if all_backtest_rows:
        write_backtest_detail_csv(os.path.join(OUT_DIR, "workload_backtest_detail_lstm.csv"), all_backtest_rows)
    write_backtest_summary_csv(os.path.join(OUT_DIR, "workload_backtest_summary_lstm.csv"), backtest_summary)

    print("\n===== DONE =====")
    print("实际预测结果:    ", os.path.join(OUT_DIR, "workload_forecast_1d_lstm.csv"))
    print("告警+扩容建议:   ", os.path.join(OUT_DIR, "workload_alerts_and_scale_pods_1d_lstm.csv"))
    if all_backtest_rows:
        print("回测明细:        ", os.path.join(OUT_DIR, "workload_backtest_detail_lstm.csv"))
    print("回测汇总:        ", os.path.join(OUT_DIR, "workload_backtest_summary_lstm.csv"))

    print("\n===== 滑窗回测准确率汇总 =====")
    for k, v in backtest_summary:
        if k: print(f"  {k}: {v}")

if __name__ == "__main__":
    main()