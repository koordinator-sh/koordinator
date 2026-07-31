#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Pod资源聚类与类型标注系统（Model4目录专用）
无交互终端版本 — 输入输出路径 /root/autodl-tmp/Model4/
"""

import argparse
import gzip
import json
import os
import glob
import sys
from datetime import datetime
from pathlib import Path

import numpy as np
import pandas as pd
import matplotlib.pyplot as plt
import warnings
from sklearn.preprocessing import RobustScaler
from sklearn.cluster import KMeans
from sklearn.metrics import silhouette_score
from sklearn.manifold import TSNE

warnings.filterwarnings("ignore")



INPUT = "/root/autodl-tmp/Model4/*.gz"
OUTPUT = "/root/autodl-tmp/Model4/pod_cluster_results"


# =====================================================
# 目标 Prometheus 指标集合
# =====================================================
TARGET_METRICS = {
    "container_cpu_usage_seconds_total",
    "container_memory_usage_bytes",
    "container_spec_memory_limit_bytes",
    "container_fs_reads_bytes_total",
    "container_fs_writes_bytes_total",
    "container_network_receive_bytes_total",
    "container_network_transmit_bytes_total",
}


# =====================================================
# 1. 从 gz 读取并计算 Pod 级资源指标
# =====================================================
def load_pod_metrics_from_gz(gz_paths):
    """
    逐行读取 gz 压缩的 Prometheus 时序数据，
    按 (pod, namespace) 聚合所有容器，
    返回 DataFrame，包含列：pod, namespace, cpu, memory, io, network
    """
    pod_data = {}  # (pod, ns) -> { container: { metric_name: [(ts, val), ...] } }

    for gz_path in gz_paths:
        print(f"[INFO] 读取文件: {gz_path}")
        with gzip.open(gz_path, "rt", encoding="utf-8", errors="ignore") as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                try:
                    record = json.loads(line)
                except json.JSONDecodeError:
                    continue

                metric = record.get("metric", {})
                metric_name = metric.get("__name__", "")
                if metric_name not in TARGET_METRICS:
                    continue

                pod = metric.get("pod")
                namespace = metric.get("namespace", "default")
                if not pod:
                    continue

                container = metric.get("container", "main")
                pod_key = (pod, namespace)

                timestamps = record.get("timestamps", [])
                values = record.get("values", [])
                if not timestamps or not values or len(timestamps) != len(values):
                    continue

                # 按时间排序
                points = sorted(zip(timestamps, values), key=lambda x: x[0])

                # 初始化嵌套字典
                if pod_key not in pod_data:
                    pod_data[pod_key] = {}
                if container not in pod_data[pod_key]:
                    pod_data[pod_key][container] = {}
                pod_data[pod_key][container][metric_name] = points

    # 聚合每个 Pod 的资源指标
    pod_records = []
    for (pod, namespace), containers in pod_data.items():
        all_timestamps = []
        total_cpu_delta = 0.0
        total_memory_usage = 0.0
        total_memory_limit = 0.0
        total_read_delta = 0.0
        total_write_delta = 0.0
        total_rx_delta = 0.0
        total_tx_delta = 0.0

        for container, metrics in containers.items():
            # CPU (累计时间)
            if "container_cpu_usage_seconds_total" in metrics:
                points = metrics["container_cpu_usage_seconds_total"]
                ts, vals = zip(*points)
                all_timestamps.extend(ts)
                total_cpu_delta += vals[-1] - vals[0]

            # Memory usage
            if "container_memory_usage_bytes" in metrics:
                points = metrics["container_memory_usage_bytes"]
                total_memory_usage += points[-1][1]

            # Memory limit
            if "container_spec_memory_limit_bytes" in metrics:
                points = metrics["container_spec_memory_limit_bytes"]
                total_memory_limit += points[-1][1]  # 取最后时间点

            # IO reads
            if "container_fs_reads_bytes_total" in metrics:
                points = metrics["container_fs_reads_bytes_total"]
                ts, vals = zip(*points)
                all_timestamps.extend(ts)
                total_read_delta += vals[-1] - vals[0]

            # IO writes
            if "container_fs_writes_bytes_total" in metrics:
                points = metrics["container_fs_writes_bytes_total"]
                ts, vals = zip(*points)
                all_timestamps.extend(ts)
                total_write_delta += vals[-1] - vals[0]

            # Network receive
            if "container_network_receive_bytes_total" in metrics:
                points = metrics["container_network_receive_bytes_total"]
                ts, vals = zip(*points)
                all_timestamps.extend(ts)
                total_rx_delta += vals[-1] - vals[0]

            # Network transmit
            if "container_network_transmit_bytes_total" in metrics:
                points = metrics["container_network_transmit_bytes_total"]
                ts, vals = zip(*points)
                all_timestamps.extend(ts)
                total_tx_delta += vals[-1] - vals[0]

        # 时间窗口
        if not all_timestamps:
            continue
        min_ts = min(all_timestamps)
        max_ts = max(all_timestamps)
        time_window_sec = (max_ts - min_ts) / 1000.0
        if time_window_sec <= 0:
            continue

        cpu_cores = total_cpu_delta / time_window_sec
        mem_util = min(total_memory_usage / total_memory_limit, 1.0) if total_memory_limit > 0 else 0.0
        io_throughput_mbps = ((total_read_delta + total_write_delta) / time_window_sec) / (1024 * 1024)
        net_throughput_mbps = ((total_rx_delta + total_tx_delta) / time_window_sec) / (1024 * 1024)

        pod_records.append({
            "pod": pod,
            "namespace": namespace,
            "cpu": cpu_cores,
            "memory": mem_util,       # 0~1
            "io": io_throughput_mbps,
            "network": net_throughput_mbps,
        })

    return pd.DataFrame(pod_records)


# =====================================================
# 2. Pod 级特征聚合（mean, max, p90）
# =====================================================
def aggregate_pod_features(df):
    def safe_percentile(x, p=90):
        clean = x.dropna()
        return np.percentile(clean, p) if len(clean) > 0 else 0

    agg_funcs = {
        "cpu": ["mean", "max", lambda x: safe_percentile(x, 90)],
        "memory": ["mean", "max", lambda x: safe_percentile(x, 90)],
        "io": ["mean", "max", lambda x: safe_percentile(x, 90)],
        "network": ["mean", "max", lambda x: safe_percentile(x, 90)],
    }
    pod_df = df.groupby(["pod", "namespace"]).agg(agg_funcs)
    pod_df.columns = [
        "cpu_mean", "cpu_max", "cpu_p90",
        "memory_mean", "memory_max", "memory_p90",
        "io_mean", "io_max", "io_p90",
        "network_mean", "network_max", "network_p90"
    ]
    pod_df = pod_df.fillna(0).reset_index()
    return pod_df


# =====================================================
# 3. 特征预处理（微小IO处理 + log1p + 比例）
# =====================================================
def normalize_features(pod_df):
    df = pod_df.copy()

    # 微小IO值置零
    io_threshold = 1e-4
    df["io_mean_processed"] = df["io_mean"].copy()
    tiny_mask = (df["io_mean"] > 0) & (df["io_mean"] < io_threshold)
    df.loc[tiny_mask, "io_mean_processed"] = 0.0
    print(f"[INFO] 处理微小 IO 值（<{io_threshold}）: {tiny_mask.sum()} 个 Pod 被置零")

    # log1p 变换
    df["cpu_mean_log"] = np.log1p(df["cpu_mean"])
    df["memory_mean_log"] = np.log1p(df["memory_mean"])
    df["io_mean_log"] = np.log1p(df["io_mean_processed"])
    df["network_mean_log"] = np.log1p(df["network_mean"])

    # 资源比例
    total_res = (df["cpu_mean_log"] + df["memory_mean_log"] +
                 df["io_mean_log"] + df["network_mean_log"] + 1e-10)
    df["cpu_proportion"] = df["cpu_mean_log"] / total_res
    df["memory_proportion"] = df["memory_mean_log"] / total_res
    df["io_proportion"] = df["io_mean_log"] / total_res
    df["network_proportion"] = df["network_mean_log"] / total_res

    # 消除 NaN/Inf（防止全为0时比例计算产生 NaN）
    df = df.replace([np.inf, -np.inf], np.nan).fillna(0)
    return df


# =====================================================
# 4. KMeans 聚类
# =====================================================
def cluster_pods(pod_df):
    # 安全处理 NaN
    feature_cols = [
        "cpu_mean_log", "memory_mean_log", "io_mean_log", "network_mean_log",
        "cpu_proportion", "memory_proportion", "io_proportion", "network_proportion"
    ]
    existing_features = [c for c in feature_cols if c in pod_df.columns]
    print(f"[INFO] 使用 {len(existing_features)} 个特征进行聚类")

    # 提取特征矩阵前确保无 NaN
    X = pod_df[existing_features].values
    X = np.nan_to_num(X, nan=0.0, posinf=0.0, neginf=0.0)

    scaler = RobustScaler()
    X_scaled = scaler.fit_transform(X)

    best_k, best_score, best_labels = 4, -1, None
    for k in range(2, min(11, len(X_scaled))):
        kmeans = KMeans(n_clusters=k, random_state=42, n_init=10)
        labels = kmeans.fit_predict(X_scaled)
        if len(set(labels)) > 1:
            score = silhouette_score(X_scaled, labels)
            print(f"  K={k}, 轮廓系数={score:.3f}")
            if score > best_score:
                best_k, best_score, best_labels = k, score, labels

    print(f"[INFO] 最佳 K = {best_k}, 轮廓系数 = {best_score:.3f}")
    pod_df["cluster"] = best_labels
    return pod_df, X_scaled, {"method": "KMeans", "n_clusters": best_k, "silhouette": best_score}


# =====================================================
# 5. Pod 类型标注
# =====================================================
def assign_pod_type(row):
    resources = {
        "cpu": row.get("cpu_proportion", 0),
        "memory": row.get("memory_proportion", 0),
        "io": row.get("io_proportion", 0),
        "network": row.get("network_proportion", 0),
    }
    threshold = 0.10

    memory_high = resources["memory"] >= threshold
    io_high = resources["io"] >= threshold
    if memory_high and io_high:
        return "memory-io-intensive"

    dominant = [r for r, v in resources.items() if v >= threshold]
    if not dominant:
        max_r = max(resources, key=resources.get)
        return f"{max_r}-intensive" if resources[max_r] > 0 else "unknown"
    elif len(dominant) == 1:
        return f"{dominant[0]}-intensive"
    elif len(dominant) == 2:
        r1, r2 = sorted(dominant, key=lambda x: ["cpu", "memory", "io", "network"].index(x))
        return f"{r1}-{r2}-intensive"
    else:
        return "multi-intensive"


def label_pods(pod_df):
    pod_df = pod_df.copy()
    pod_df["pod_type"] = pod_df.apply(assign_pod_type, axis=1)
    print("\n[INFO] Pod 类型分布:")
    for typ, cnt in pod_df["pod_type"].value_counts().items():
        print(f"  {typ:25s}: {cnt:4d}  ({cnt/len(pod_df)*100:.1f}%)")
    return pod_df


# =====================================================
# 6. 可视化与保存
# =====================================================
def visualize_and_save(pod_df, X_scaled, output_dir):
    # output_dir = Path(output_dir)
    # output_dir.mkdir(parents=True, exist_ok=True)

    # tsne = TSNE(n_components=2, random_state=42, perplexity=min(30, len(X_scaled)-1),
    #             max_iter=1000, learning_rate=200)
    # X_tsne = tsne.fit_transform(X_scaled)
    # pod_df["tsne1"] = X_tsne[:, 0]
    # pod_df["tsne2"] = X_tsne[:, 1]

    # # 可视化
    # fig, axes = plt.subplots(1, 2, figsize=(20, 8))
    # # 聚类视图
    # ax1 = axes[0]
    # clusters = sorted(pod_df["cluster"].unique())
    # colors = plt.cm.tab20(np.linspace(0, 1, len(clusters)))
    # for i, cid in enumerate(clusters):
    #     d = pod_df[pod_df["cluster"] == cid]
    #     ax1.scatter(d["tsne1"], d["tsne2"], label=f"Cluster {cid} ({len(d)})",
    #                 c=[colors[i]], alpha=0.7, s=60, edgecolors='k', linewidth=0.5)
    # ax1.legend(fontsize=10)
    # ax1.set_title("Pod Clustering (t-SNE)", fontsize=16)
    # ax1.grid(alpha=0.2)

    # # 类型视图
    # ax2 = axes[1]
    # pod_types = sorted(pod_df["pod_type"].unique())
    # colors2 = plt.cm.Set3(np.linspace(0, 1, len(pod_types)))
    # for i, ptype in enumerate(pod_types):
    #     d = pod_df[pod_df["pod_type"] == ptype]
    #     ax2.scatter(d["tsne1"], d["tsne2"], label=f"{ptype} ({len(d)})",
    #                 c=[colors2[i]], alpha=0.7, s=60, edgecolors='k', linewidth=0.5)
    # ax2.legend(fontsize=10)
    # ax2.set_title("Pod Distribution by Type (t-SNE)", fontsize=16)
    # ax2.grid(alpha=0.2)

    # plt.tight_layout()
    # png_path = output_dir / "pod_clustering_tSNE.png"
    # plt.savefig(png_path, dpi=300, bbox_inches="tight")
    # plt.close()
    # print(f"[INFO] 可视化图已保存: {png_path}")

    # 保存 CSV
    save_cols = ["pod", "namespace", "cluster", "pod_type", "io_mean_processed",
                 "cpu_mean", "memory_mean", "io_mean", "network_mean",
                 "cpu_proportion", "memory_proportion", "io_proportion", "network_proportion"]
    csv_path = output_dir +"/"+ "pod_clustering_results.csv"
    pod_df.sort_values(["cluster", "pod_type", "pod"])[save_cols].to_csv(csv_path, index=False, encoding="utf-8")
    print(f"[INFO] 聚类结果 CSV: {csv_path}")

    # 统计文本
    stats_path = output_dir+"/"+"clustering_statistics.txt"
    with open(stats_path, "w", encoding="utf-8") as f:
        sil_score = silhouette_score(X_scaled, pod_df["cluster"]) if len(set(pod_df["cluster"])) > 1 else 0
        f.write(f"Pod 资源聚类分析统计\n")
        f.write(f"分析时间: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}\n")
        f.write(f"Pod 总数: {len(pod_df)}\n")
        f.write(f"最佳 K: {pod_df['cluster'].nunique()}  轮廓系数: {sil_score:.3f}\n\n")
        f.write("类型分布:\n")
        for typ, cnt in pod_df["pod_type"].value_counts().items():
            f.write(f"  {typ:25s}: {cnt:4d}  ({cnt/len(pod_df)*100:.1f}%)\n")
    print(f"[INFO] 统计信息: {stats_path}")
    return pod_df




# =====================================================
# 主函数
# =====================================================
def main():

    # 解析输入文件（支持通配符）
    gz_files = glob.glob(INPUT)
    if not gz_files:
        print(f"[ERROR] 未找到输入文件: {INPUT}")
        sys.exit(1)

    print("=" * 60)
    print("[STEP 1] 从 gz 文件中提取 Pod 资源指标...")
    raw_df = load_pod_metrics_from_gz(gz_files)
    if raw_df.empty:
        print("[ERROR] 没有提取到任何 Pod 数据，请检查输入文件内容。")
        sys.exit(1)
    print(f"  -> 提取到 {len(raw_df)} 条 Pod 记录。")

    print("\n[STEP 2] 聚合 Pod 特征...")
    pod_df = aggregate_pod_features(raw_df)
    print(f"  -> 聚合后 Pod 数量: {len(pod_df)}")

    print("\n[STEP 3] 特征预处理（log 变换 + 比例计算）...")
    pod_df = normalize_features(pod_df)

    print("\n[STEP 4] KMeans 聚类...")
    pod_df, X_scaled, cluster_info = cluster_pods(pod_df)

    print("\n[STEP 5] Pod 类型标注...")
    pod_df = label_pods(pod_df)

    print("\n[STEP 6] 可视化与输出...")
    pod_df = visualize_and_save(pod_df, X_scaled, OUTPUT)

    print("\n" + "=" * 60)
    print("分析完成！结果保存在:", os.path.abspath(OUTPUT))
    print("=" * 60)


if __name__ == "__main__":
    main()