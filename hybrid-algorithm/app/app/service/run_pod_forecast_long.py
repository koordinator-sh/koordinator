# -*- coding: utf-8 -*-
"""
Pod 长期预测服务模块

封装 pod_forecast_long 算法，提供可调用的服务接口
"""

import os
from typing import Any, Dict

from tqdm import tqdm

from app.algorithm.pod_forecast_long import (
    BACKTEST_WINDOWS,
    BUCKET_SEC,
    CPU_CRIT,
    CPU_WARN,
    IDX_CPU,
    IDX_IO,
    IDX_MEM,
    IDX_NET,
    IN_LEN,
    IO_CRIT,
    IO_WARN,
    MC_SAMPLES,
    MEM_CRIT,
    MEM_WARN,
    NET_CRIT,
    NET_WARN,
    OUT_LEN,
    backtest_workload,
    detect_severity,
    first_trigger_idx,
    mc_dropout_forecast,
    pass1_extract_limits,
    pass2_build_workload_series,
    recommend_pods,
    train_lstm_one_step,
    ts_to_local_str,
    write_alert_reco_csv,
    write_backtest_detail_csv,
    write_backtest_summary_csv,
    write_forecast_csv,
)
from app.settings import settings
from app.utils.logger import get_logger
from app.utils.tools import ensure_output_dir


logger = get_logger(__name__)


# =====================================================
# Pod 长期预测服务函数
# =====================================================


def run_pod_forecast_long(date_str: str, input_jsonl: str) -> Dict[str, Any]:
    """
    运行Pod负载长期预测

    Args:
        date_str: 日期字符串（用于组织输出目录）
        input_jsonl: 输入JSONL文件路径

    Returns:
        {
            'success': bool,
            'forecast_csv': str,          # 预测结果CSV
            'alert_csv': str,              # 告警+扩容建议CSV
            'backtest_detail_csv': str,    # 回测明细CSV
            'backtest_summary_csv': str,  # 回测汇总CSV
            'workload_count': int,         # workload数量
            'error': str                   # 仅失败时
        }
    """
    try:
        logger.info("[PodForecastLong] 开始Pod长期预测")

        # =====================================================
        # 路径配置
        # =====================================================
        output_dir = os.path.join(settings.OUTPUT_DIR_MODEL_5, date_str, "ForecastOutputWorkloadLSTM24h")

        # 确保输出目录存在
        ensure_output_dir(output_dir)

        # 验证输入文件
        if not os.path.exists(input_jsonl):
            error_msg = f"输入JSONL文件不存在: {input_jsonl}"
            logger.error(f"[PodForecastLong] {error_msg}")
            return {"success": False, "error": error_msg}

        bucket_min = BUCKET_SEC // 60
        logger.info(f"[PodForecastLong] 粒度: {BUCKET_SEC}s（{bucket_min} 分钟/桶）")
        logger.info(f"[PodForecastLong] 输入历史: {IN_LEN} 桶 = {IN_LEN * bucket_min // 60} 小时")
        logger.info(f"[PodForecastLong] 预测长度: {OUT_LEN} 桶 = {OUT_LEN * bucket_min // 60} 小时")

        # =====================================================
        # 数据准备
        # =====================================================
        logger.info("[PodForecastLong] Pass1: 提取限制信息")
        mem_limit, cpu_quota, cpu_period = pass1_extract_limits(input_jsonl)

        logger.info("[PodForecastLong] Pass2: 构造workload序列")
        wl_series, min_m, max_m, wl_pods, wl_namespace = pass2_build_workload_series(
            input_jsonl, mem_limit, cpu_quota, cpu_period
        )

        if min_m is None:
            error_msg = "没有可用的workload序列（缺少quota/period或内存限制信息）"
            logger.error(f"[PodForecastLong] {error_msg}")
            return {"success": False, "error": error_msg}

        start_future_bucket = max_m + 1
        total_buckets = max_m - min_m + 1
        logger.info(f"[PodForecastLong] 数据时间范围: 桶 {min_m} ~ {max_m}")
        logger.info(f"[PodForecastLong] 数据总桶数: {total_buckets} 桶 = {total_buckets * bucket_min / 60:.1f} 小时")
        logger.info(f"[PodForecastLong] 预测起始桶: {start_future_bucket}")
        logger.info(f"[PodForecastLong] workload数量: {len(wl_series)}")
        logger.info(f"[PodForecastLong] 预测起始时间: {ts_to_local_str(start_future_bucket * BUCKET_SEC)}")

        # =====================================================
        # 阶段一：实际预测
        # =====================================================
        logger.info("[PodForecastLong] 阶段一：对未来一天做实际预测")
        forecast_rows = []
        alert_rows = []

        def get_cur_pods(wl: str) -> int:
            """获取当前workload的pod数量"""
            recent_start = max(min_m, max_m - IN_LEN + 1)
            pods = set()
            for m in range(recent_start, max_m + 1):
                pods.update(wl_pods[wl].get(m, set()))
            return max(1, len(pods))

        processed_count = 0
        for wl, series_2d in tqdm(list(wl_series.items()), desc="[PodForecastLong] 阶段一：实际预测"):
            if len(series_2d) < (IN_LEN + OUT_LEN):
                logger.debug(f"[PodForecastLong] Workload {wl} 数据不足，跳过")
                continue

            # 训练LSTM模型
            model = train_lstm_one_step(series_2d)
            if model is None:
                logger.warning(f"[PodForecastLong] Workload {wl} 模型训练失败，跳过")
                continue

            processed_count += 1
            namespace = wl_namespace.get(wl, "")

            # MC Dropout预测
            mean, low, high = mc_dropout_forecast(model, series_2d, OUT_LEN, MC_SAMPLES)

            # 生成预测结果行（4个维度）
            for i in range(OUT_LEN):
                bucket = start_future_bucket + i
                ts_sec = bucket * BUCKET_SEC
                dt = ts_to_local_str(ts_sec)
                for dim_name, idx in [
                    ("cpu_util", IDX_CPU),
                    ("mem_util", IDX_MEM),
                    ("io_mbps", IDX_IO),
                    ("net_mbps", IDX_NET),
                ]:
                    forecast_rows.append(
                        {
                            "namespace": namespace,
                            "workload": wl,
                            "dimension": dim_name,
                            "minute_offset": i + 1,
                            "ts": ts_sec,
                            "dt": dt,
                            "mean": f"{mean[i][idx]:.4f}",
                            "low": f"{low[i][idx]:.4f}",
                            "high": f"{high[i][idx]:.4f}",
                        }
                    )

            # 生成告警和扩容建议（4个维度）
            cur_pods = get_cur_pods(wl)
            for dim, idx, warn, crit in [
                ("cpu_util", IDX_CPU, CPU_WARN, CPU_CRIT),
                ("mem_util", IDX_MEM, MEM_WARN, MEM_CRIT),
                ("io_mbps", IDX_IO, IO_WARN, IO_CRIT),
                ("net_mbps", IDX_NET, NET_WARN, NET_CRIT),
            ]:
                high_seq = [high[i][idx] for i in range(OUT_LEN)]
                peak = max(high_seq) if high_seq else 0.0
                sev = detect_severity(peak, warn, crit)
                if sev:
                    thr = crit if sev == "CRIT" else warn
                    fidx = first_trigger_idx(high_seq, thr) or 0
                    first_b = start_future_bucket + fidx
                    add, new = recommend_pods(cur_pods, peak, sev)
                    alert_rows.append(
                        {
                            "namespace": namespace,
                            "workload": wl,
                            "dimension": dim,
                            "severity": sev,
                            "reason": "ci_high_exceed",
                            "threshold": thr,
                            "peak_high": f"{peak:.4f}",
                            "cur_pods": cur_pods,
                            "add_pods": add,
                            "new_pods": new,
                            "first_dt": ts_to_local_str(first_b * BUCKET_SEC),
                        }
                    )

        logger.info(f"[PodForecastLong] 阶段一完成，处理了 {processed_count} 个 workload")
        logger.info(f"[PodForecastLong] 预测行数: {len(forecast_rows)}")
        logger.info(f"[PodForecastLong] 告警行数: {len(alert_rows)}")

        # =====================================================
        # 阶段二：滑窗回测
        # =====================================================
        all_backtest_rows = []
        if BACKTEST_WINDOWS > 0:
            logger.info(f"[PodForecastLong] 阶段二：滑窗回测（每个workload做{BACKTEST_WINDOWS}个窗口）")

            for wl, series_2d in tqdm(wl_series.items(), desc="[PodForecastLong] 阶段二：滑窗回测"):
                namespace = wl_namespace.get(wl, "")
                rows = backtest_workload(wl, namespace, series_2d, wl_pods, min_m, max_m, BACKTEST_WINDOWS)
                all_backtest_rows.extend(rows)

            logger.info(f"[PodForecastLong] 阶段二完成，回测行数: {len(all_backtest_rows)}")
        else:
            logger.info("[PodForecastLong] 阶段二：BACKTEST_WINDOWS=0，跳过回测")

        # =====================================================
        # 汇总统计
        # =====================================================
        total = len(all_backtest_rows)
        if total == 0:
            if BACKTEST_WINDOWS > 0:
                logger.warning("[PodForecastLong] 回测结果为空，数据不足")
                min_needed = IN_LEN + OUT_LEN * (BACKTEST_WINDOWS + 1)
                logger.warning(f"[PodForecastLong] 需要至少 {min_needed} 桶 = {min_needed * bucket_min / 60:.1f} 小时")
            backtest_summary = [
                ("警告", "数据不足或回测未执行"),
            ]
        else:

            def pct(n):
                return f"{n / total * 100:.2f}"

            cpu_alert_hits = sum(1 for r in all_backtest_rows if r["cpu_alert_match"] == "HIT")
            mem_alert_hits = sum(1 for r in all_backtest_rows if r["mem_alert_match"] == "HIT")
            io_alert_hits = sum(1 for r in all_backtest_rows if r["io_alert_match"] == "HIT")
            net_alert_hits = sum(1 for r in all_backtest_rows if r["net_alert_match"] == "HIT")
            cpu_scale_hits = sum(1 for r in all_backtest_rows if r["cpu_scale_hit"] == "HIT")
            mem_scale_hits = sum(1 for r in all_backtest_rows if r["mem_scale_hit"] == "HIT")
            avg_cpu_mae = sum(float(r["cpu_mae"]) for r in all_backtest_rows) / total
            avg_mem_mae = sum(float(r["mem_mae"]) for r in all_backtest_rows) / total
            avg_io_mae = sum(float(r["io_mae"]) for r in all_backtest_rows) / total
            avg_net_mae = sum(float(r["net_mae"]) for r in all_backtest_rows) / total

            backtest_summary = [
                ("回测窗口总数", total),
                ("涉及workload数", len(set(r["workload"] for r in all_backtest_rows))),
                ("", ""),
                ("── 利用率预测误差 ──", ""),
                ("平均CPU利用率MAE", f"{avg_cpu_mae:.4f}"),
                ("平均MEM利用率MAE", f"{avg_mem_mae:.4f}"),
                ("平均IO吞吐MAE(MB/s)", f"{avg_io_mae:.4f}"),
                ("平均NET吞吐MAE(MB/s)", f"{avg_net_mae:.4f}"),
                ("", ""),
                ("── 告警级别准确率 ──", ""),
                ("CPU告警命中数", cpu_alert_hits),
                ("CPU告警未命中数", total - cpu_alert_hits),
                ("CPU告警准确率(%)", pct(cpu_alert_hits)),
                ("MEM告警命中数", mem_alert_hits),
                ("MEM告警未命中数", total - mem_alert_hits),
                ("MEM告警准确率(%)", pct(mem_alert_hits)),
                ("IO告警命中数", io_alert_hits),
                ("IO告警未命中数", total - io_alert_hits),
                ("IO告警准确率(%)", pct(io_alert_hits)),
                ("NET告警命中数", net_alert_hits),
                ("NET告警未命中数", total - net_alert_hits),
                ("NET告警准确率(%)", pct(net_alert_hits)),
                ("", ""),
                ("── 扩容建议准确率 ──", ""),
                ("CPU扩容建议命中数", cpu_scale_hits),
                ("CPU扩容建议准确率(%)", pct(cpu_scale_hits)),
                ("MEM扩容建议命中数", mem_scale_hits),
                ("MEM扩容建议准确率(%)", pct(mem_scale_hits)),
            ]

        # =====================================================
        # 输出 CSV
        # =====================================================
        forecast_csv = os.path.join(output_dir, "workload_forecast_24h_lstm.csv")
        alert_csv = os.path.join(output_dir, "workload_alerts_and_scale_pods_24h_lstm.csv")
        backtest_detail_csv = os.path.join(output_dir, "workload_backtest_detail_24h_lstm.csv")
        backtest_summary_csv = os.path.join(output_dir, "workload_backtest_summary_24h_lstm.csv")

        write_forecast_csv(forecast_csv, forecast_rows)
        write_alert_reco_csv(alert_csv, alert_rows)
        if all_backtest_rows:
            write_backtest_detail_csv(backtest_detail_csv, all_backtest_rows)
        write_backtest_summary_csv(backtest_summary_csv, backtest_summary)

        logger.info("[PodForecastLong] 预测完成")
        logger.info(f"[PodForecastLong] 预测结果: {forecast_csv}")
        logger.info(f"[PodForecastLong] 告警+扩容建议: {alert_csv}")
        logger.info(f"[PodForecastLong] 回测明细: {backtest_detail_csv}")
        logger.info(f"[PodForecastLong] 回测汇总: {backtest_summary_csv}")

        return {
            "success": True,
            "forecast_csv": forecast_csv,
            "alert_csv": alert_csv,
            "backtest_detail_csv": backtest_detail_csv,
            "backtest_summary_csv": backtest_summary_csv,
            "workload_count": processed_count,
        }

    except Exception as e:
        logger.error(f"[PodForecastLong] Pod预测异常: {e}", exc_info=True)
        return {
            "success": False,
            "error": str(e),
        }
