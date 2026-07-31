#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
模型四完整算法服务层（重构版）

参考 Model4.py 整合算法，处理已解压的 jsonl 文件，
提供完整的聚类分析流程和进度回调支持
"""

import json
import os
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional

from app.algorithm import Model4
from app.utils.logger import get_logger
from app.utils.tools import ensure_output_dir


logger = get_logger(__name__)


# =====================================================
# 目标 Prometheus 指标集合（来自 Model4.py）
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
# 1. 从文件读取并计算 Pod 级资源指标（参考 Model4.py）
# =====================================================
def load_pod_metrics_from_file(file_paths: List[str]) :
    """
    从解压后的文件读取 Prometheus 时序数据，
    按 (pod, namespace) 聚合所有容器，
    返回 DataFrame，包含列：pod, namespace, cpu, memory, io, network

    Args:
        file_paths: 解压后的文件路径列表

    Returns:
        dict: {
            'success': bool,
            'pod_df': pd.DataFrame or None,
            'error': str
        }
    """
    try:
        import pandas as pd

        pod_data = {}  # (pod, ns) -> { container: { metric_name: [(ts, val), ...] } }

        for file_path in file_paths:
            logger.info(f"读取文件: {file_path}")

            with open(file_path, "rt", encoding="utf-8", errors="ignore") as f:
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

            pod_records.append(
                {
                    "pod": pod,
                    "namespace": namespace,
                    "cpu": cpu_cores,
                    "memory": mem_util,  # 0~1
                    "io": io_throughput_mbps,
                    "network": net_throughput_mbps,
                }
            )

        pod_df = pd.DataFrame(pod_records)
        logger.info(f"提取到 {len(pod_df)} 个 Pod 的资源指标")

        return {"success": True, "pod_df": pod_df}

    except Exception as e:
        logger.error(f"读取 Pod 指标失败: {e}", exc_info=True)
        return {"success": False, "error": str(e), "pod_df": None}


# =====================================================
# 导入 Model4.py 的其他函数
# =====================================================
def _import_model4_functions():
    """动态导入 Model4.py 的处理函数"""
    try:
        from pathlib import Path
        import sys

        # 添加算法目录到 Python 路径
        algorithm_dir = Path(__file__).parent.parent / "algorithm"
        if str(algorithm_dir) not in sys.path:
            sys.path.insert(0, str(algorithm_dir))

        return {
            "aggregate_pod_features": Model4.aggregate_pod_features,
            "normalize_features": Model4.normalize_features,
            "cluster_pods": Model4.cluster_pods,
            "label_pods": Model4.label_pods,
            "visualize_and_save": Model4.visualize_and_save,
        }
    except ImportError as e:
        logger.error(f"导入 Model4.py 失败: {e}")
        raise


# 缓存导入的函数
_model4_functions = None


def get_model4_functions():
    """获取 Model4.py 的函数（带缓存）"""
    global _model4_functions
    if _model4_functions is None:
        _model4_functions = _import_model4_functions()
    return _model4_functions


# =====================================================
# 模型四完整算法执行
# =====================================================


def run_model4_algorithm(
    input_dir: str,
    output_dir: str,
    progress_callback: Optional[Callable[[int, str], None]] = None,
) -> Dict[str, Any]:
    """
    运行模型四完整算法（参考 Model4.py 整合算法）

    处理已解压的 jsonl 文件（不需要 .gz 压缩文件）

    Args:
        input_dir: 输入目录路径（包含已解压的文件）
        output_dir: 输出目录路径
        progress_callback: 进度回调函数，接收 (progress_percent, message) 参数

    Returns:
        算法执行结果:
        {
            'success': bool,
            'output_dir': str,
            'pod_count': int,
            'cluster_count': int,
            'cluster_info': dict,
            'csv_file': str,
            'png_file': str,
            'txt_file': str,
            'error': str  # 仅失败时
        }
    """
    try:
        logger.info(f"开始运行模型四算法: input_dir={input_dir}, output_dir={output_dir}")

        # 确保输出目录存在
        ensure_output_dir(output_dir)

        # =====================================================
        # 步骤 1: 从文件中提取 Pod 资源指标 (0-35%)
        # =====================================================
        if progress_callback:
            progress_callback(5, "正在从文件提取 Pod 资源指标...")

        logger.info("[STEP 1] 从文件中提取 Pod 资源指标...")

        # 查找输入文件
        import glob

        # 查找所有文件（无扩展名）
        all_files = []
        for file in os.listdir(input_dir):
            file_path = os.path.join(input_dir, file)
            all_files.append(file_path)

        if not all_files:
            return {
                "success": False,
                "error": f"未找到有效的输入文件: {input_dir}",
            }

        logger.info(f"找到 {len(all_files)} 个输入文件")

        # 加载 Pod 指标
        load_result = load_pod_metrics_from_file(all_files)
        if not load_result["success"]:
            return {
                "success": False,
                "error": f"Pod 指标加载失败: {load_result.get('error')}",
            }

        raw_df = load_result["pod_df"]

        if raw_df.empty:
            return {
                "success": False,
                "error": "没有提取到任何 Pod 数据，请检查输入文件内容",
            }

        logger.info(f"  -> 提取到 {len(raw_df)} 条 Pod 记录")

        if progress_callback:
            progress_callback(35, f"Pod 资源指标提取完成: {len(raw_df)} 条记录")

        # =====================================================
        # 步骤 2-6: 使用 Model4.py 的流程 (35-100%)
        # =====================================================
        # 获取 Model4 函数
        model4_funcs = get_model4_functions()

        # 步骤 2: 聚合 Pod 特征 (35-50%)
        logger.info("[STEP 2] 聚合 Pod 特征...")
        if progress_callback:
            progress_callback(40, "正在聚合 Pod 特征...")

        pod_df = model4_funcs["aggregate_pod_features"](raw_df)
        logger.info(f"  -> 聚合后 Pod 数量: {len(pod_df)}")

        if progress_callback:
            progress_callback(50, f"Pod 特征聚合完成: {len(pod_df)} 个 Pod")

        # 步骤 3: 特征预处理 (50-65%)
        logger.info("[STEP 3] 特征预处理（log 变换 + 比例计算）...")
        if progress_callback:
            progress_callback(55, "正在进行特征预处理...")

        pod_df = model4_funcs["normalize_features"](pod_df)

        if progress_callback:
            progress_callback(65, "特征预处理完成")

        # 步骤 4: KMeans 聚类 (65-80%)
        logger.info("[STEP 4] KMeans 聚类...")
        if progress_callback:
            progress_callback(70, "正在执行 KMeans 聚类...")

        pod_df, X_scaled, cluster_info = model4_funcs["cluster_pods"](pod_df)

        if progress_callback:
            cluster_count = pod_df["cluster"].nunique()
            progress_callback(80, f"KMeans 聚类完成: {cluster_count} 个聚类")

        # 步骤 5: Pod 类型标注 (80-90%)
        logger.info("[STEP 5] Pod 类型标注...")
        if progress_callback:
            progress_callback(85, "正在标注 Pod 类型...")

        pod_df = model4_funcs["label_pods"](pod_df)

        if progress_callback:
            progress_callback(90, "Pod 类型标注完成")

        # 步骤 6: 可视化与输出 (90-100%)
        logger.info("[STEP 6] 可视化与输出...")
        if progress_callback:
            progress_callback(95, "正在保存分析结果...")

        pod_df = model4_funcs["visualize_and_save"](pod_df, X_scaled, output_dir)

        # =====================================================
        # 构建结果
        # =====================================================
        csv_file = os.path.join(output_dir, "pod_clustering_results.csv")
        png_file = os.path.join(output_dir, "pod_clustering_tSNE.png")
        txt_file = os.path.join(output_dir, "clustering_statistics.txt")

        result = {
            "success": True,
            "status": "SUCCESS",
            "output_dir": output_dir,
            "pod_count": len(pod_df),
            "cluster_count": pod_df["cluster"].nunique(),
            "cluster_info": cluster_info,
            "csv_file": csv_file,
            "png_file": png_file,
            "txt_file": txt_file,
        }

        logger.info(f"模型四算法运行成功: pod_count={result['pod_count']}, cluster_count={result['cluster_count']}")

        if progress_callback:
            progress_callback(100, f"分析完成！共分析 {len(pod_df)} 个 Pod，识别 {pod_df['cluster'].nunique()} 种聚类类型")

        return result

    except Exception as e:
        logger.error(f"模型四算法运行异常: {e}", exc_info=True)
        return {
            "success": False,
            "error": str(e),
        }
