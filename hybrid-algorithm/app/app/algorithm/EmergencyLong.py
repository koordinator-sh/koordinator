import os
import gzip
import json
import re
import tempfile
from collections import defaultdict
from tqdm import tqdm

# ================== 参数 ==================
INPUT_DIR = "/root/autodl-tmp/LongTest"  # 原始数据目录（多文件，每行一个JSON）
CHECK_FILE_COUNT = 1  

OUTPUT_JSONL = "/root/autodl-tmp/BurstDataLong/performance_with_resource_metrics_with_burst.jsonl"
OUTPUT_CSV = "/root/autodl-tmp/BurstDataLong/pod_burst_timestamps.csv"
# ==========================================

# ============== 目标指标 ==============
TARGET_METRICS = {
    # CPU
    "container_cpu_usage_seconds_total",
    "container_spec_cpu_quota",
    "container_spec_cpu_period",
    # memory
    "container_memory_usage_bytes",
    "container_spec_memory_limit_bytes",
    # IO
    "container_fs_reads_bytes_total",
    "container_fs_writes_bytes_total",
    "container_fs_io_time_seconds_total",
    # network
    "container_network_receive_bytes_total",
    "container_network_transmit_bytes_total",
}

# ========= Pod命名规则正则（按优先级从高到低排列）=========
# Deployment:  <name>-<10位alphanum hash>-<5位随机串>
DEPLOYMENT_RE = re.compile(r"^([a-z0-9][a-z0-9-]*)-([a-z0-9]{10})-([a-z0-9]{5})$")
# Job（并行）: <name>-<数字索引>-<5位随机串>
JOB_RE = re.compile(r"^([a-z0-9][a-z0-9-]*)-(\d+)-([a-z0-9]{5})$")
# StatefulSet: <name>-<数字序号>
STATEFULSET_RE = re.compile(r"^([a-z0-9][a-z0-9-]*)-(\d+)$")
# DaemonSet:   <name>-<节点标识（字母数字连字符，5位以上）>
DAEMONSET_RE = re.compile(r"^([a-z0-9][a-z0-9-]*)-([a-z0-9-]{5,})$")


def parse_workload(pod_name: str):
    """
    根据 pod 命名规则解析出 workload 名称。
    匹配顺序：Deployment -> Job -> StatefulSet -> DaemonSet -> 降级处理
    """
    if not pod_name:
        return None

    # Deployment（最常见，优先匹配）
    m = DEPLOYMENT_RE.match(pod_name)
    if m:
        return m.group(1)

    # Job（比 StatefulSet 更具体，先匹配）
    m = JOB_RE.match(pod_name)
    if m:
        return m.group(1)

    # StatefulSet
    m = STATEFULSET_RE.match(pod_name)
    if m:
        return m.group(1)

    # DaemonSet（节点标识不固定，兜底匹配）
    m = DAEMONSET_RE.match(pod_name)
    if m:
        return m.group(1)

    # 完全无法匹配时降级：去掉最后一段作为 workload 名，避免丢弃数据
    if "-" in pod_name:
        return pod_name.rsplit("-", 1)[0]

    return pod_name


def list_hour_files(input_dir: str, n_files: int):
    files = sorted(
        [f for f in os.listdir(input_dir) if os.path.isfile(os.path.join(input_dir, f))]
    )
    return files[:n_files]


# =====================================================
# Pass0：过滤指标 -> 写入临时 JSONL（保持原结构）
# =====================================================
def extract_to_temp_jsonl(input_dir: str, file_list):
    tmp = tempfile.NamedTemporaryFile(
        mode="w", suffix=".jsonl", delete=False, encoding="utf-8"
    )
    tmp_path = tmp.name

    stats = {"files": 0, "lines": 0, "matched": 0}

    with tmp as w:
        for fname in tqdm(file_list, desc="Pass0: 提取目标指标", unit="file"):
            path = os.path.join(input_dir, fname)

            try:
                if os.path.getsize(path) == 0:
                    continue
            except Exception:
                continue

            stats["files"] += 1

            # 自动判断是否为 gz 压缩文件
            opener = gzip.open if path.endswith(".gz") else open

            with opener(path, "rt", encoding="utf-8", errors="ignore") as f:
                for line in f:
                    line = line.strip()
                    if not line:
                        continue
                    stats["lines"] += 1

                    try:
                        rec = json.loads(line)
                    except Exception:
                        continue

                    if not isinstance(rec, dict):
                        continue

                    metric = rec.get("metric", {})
                    metric_name = metric.get("__name__")
                    if metric_name in TARGET_METRICS:
                        w.write(json.dumps(rec, ensure_ascii=False) + "\n")
                        stats["matched"] += 1

    print("\n===== Pass0 完成：指标过滤 =====")
    print(f"处理文件数: {stats['files']}")
    print(f"读取行数: {stats['lines']}")
    print(f"匹配写出行数: {stats['matched']}")
    print(f"临时提取文件: {tmp_path}")

    return tmp_path


# =====================================================
# Pass1：统计 burst 秒（workload 内 pod 数增加）
# =====================================================
def build_burst_seconds_from_jsonl(input_jsonl: str):
    """
    遍历 JSONL，统计每个 (namespace, workload) 每秒的 pod 集合。
    当某一秒的 pod 数量 > 上一秒，则该秒为 burst 秒。
    使用 (namespace, workload) 复合 key，避免不同命名空间下同名 workload 互相干扰。
    """
    # key: (namespace, workload) -> {sec -> set(pod)}
    nswl_sec_pods = defaultdict(lambda: defaultdict(set))

    with open(input_jsonl, "r", encoding="utf-8") as f:
        for line in tqdm(f, desc="Pass1: 统计每秒Pod集合", unit="line"):
            if not line.strip():
                continue
            try:
                rec = json.loads(line)
            except Exception:
                continue

            if not isinstance(rec, dict):
                continue

            metric = rec.get("metric", {})
            pod = metric.get("pod")
            if not pod:
                continue

            namespace = metric.get("namespace", "")
            workload = parse_workload(pod)
            if workload is None:
                continue

            timestamps = rec.get("timestamps", [])
            if not timestamps:
                continue

            key = (namespace, workload)
            for ts in timestamps:
                sec = int(ts // 1000)
                nswl_sec_pods[key][sec].add(pod)

    # burst_seconds: (namespace, workload) -> set of burst seconds
    burst_seconds = defaultdict(set)
    for key, sec_dict in nswl_sec_pods.items():
        prev_count = 0
        for sec in sorted(sec_dict.keys()):
            cur_count = len(sec_dict[sec])
            if cur_count > prev_count:
                burst_seconds[key].add(sec)
            prev_count = cur_count

    nswl_sec_pods.clear()
    return burst_seconds


# =====================================================
# Pass2：逐时间点标记 burst_event 数组并写出最终 JSONL
# 同时输出 CSV：每个 pod 的突发事件时间点（去重）
# =====================================================
def tag_and_write_with_array_and_csv(
    input_jsonl: str, output_jsonl: str, output_csv: str, burst_seconds
):
    os.makedirs(os.path.dirname(output_jsonl), exist_ok=True)
    os.makedirs(os.path.dirname(output_csv), exist_ok=True)

    printed_has_1 = False
    printed_all_0 = False

    total_out = 0
    seen_pod_ts = set()

    with open(output_csv, "w", encoding="utf-8") as csvw:
        csvw.write("namespace,pod,workload,timestamp_ms,timestamp_sec\n")

        with (
            open(input_jsonl, "r", encoding="utf-8") as f,
            open(output_jsonl, "w", encoding="utf-8") as w,
        ):
            for line in tqdm(f, desc="Pass2: 打标写JSONL + 写CSV", unit="line"):
                if not line.strip():
                    continue

                try:
                    rec = json.loads(line)
                except Exception:
                    continue

                if not isinstance(rec, dict):
                    continue

                metric = rec.get("metric", {})
                pod = metric.get("pod")
                namespace = metric.get("namespace", "")
                workload = parse_workload(pod) if pod else None

                timestamps = rec.get("timestamps", [])
                burst_list = [0] * len(timestamps)

                if workload and timestamps:
                    nswl_key = (namespace, workload)
                    burst_secs = burst_seconds.get(nswl_key, set())
                    for i, ts in enumerate(timestamps):
                        sec = int(ts // 1000)
                        if sec in burst_secs:
                            burst_list[i] = 1

                            key = (pod, ts)
                            if pod and key not in seen_pod_ts:
                                seen_pod_ts.add(key)
                                csvw.write(
                                    f"{namespace},{pod},{workload},{int(ts)},{sec}\n"
                                )

                rec["burst_event"] = burst_list

                out_line = json.dumps(rec, ensure_ascii=False)
                w.write(out_line + "\n")
                total_out += 1

                if (not printed_has_1) and any(burst_list):
                    print("\n===== 示例：burst_event 含 1 的一行 =====")
                    print(out_line)
                    printed_has_1 = True

                if (
                    (not printed_all_0)
                    and (len(burst_list) > 0)
                    and (not any(burst_list))
                ):
                    print("\n===== 示例：burst_event 全 0 的一行 =====")
                    print(out_line)
                    printed_all_0 = True

    print("\n===== 完成 =====")
    print(f"JSONL 输出行数: {total_out}")
    print(f"最终 JSONL 输出文件: {output_jsonl}")
    print(f"Pod 突发时间点 CSV 输出文件: {output_csv}")


def main():
    file_list = list_hour_files(INPUT_DIR, CHECK_FILE_COUNT)
    print(f"分析文件数量: {len(file_list)}")

    temp_jsonl = extract_to_temp_jsonl(INPUT_DIR, file_list)

    try:
        burst_seconds = build_burst_seconds_from_jsonl(temp_jsonl)
        tag_and_write_with_array_and_csv(
            temp_jsonl, OUTPUT_JSONL, OUTPUT_CSV, burst_seconds
        )
    finally:
        try:
            os.remove(temp_jsonl)
        except Exception:
            pass


if __name__ == "__main__":
    main()