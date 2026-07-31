# -*- coding: utf-8 -*-
"""
Pod 短期预测服务模块

封装 pod_forecast_short 算法，提供可调用的服务接口
"""

import os
from typing import Any, Dict

from app.algorithm.pod_forecast_short import (
    BACKTEST_WINDOWS,
    BUCKET_SEC,
    CPU_CRIT,
    CPU_WARN,
    EPOCHS,
    IN_LEN,
    MC_SAMPLES,
    MEM_CRIT,
    MEM_WARN,
    OUT_LEN,
    SAFETY_MARGIN,
    TARGET_UTIL_CRIT,
    TARGET_UTIL_WARN,
    backtest_workload,
    detect_severity,
    first_trigger_idx,
    get_pod_count_at,
    mc_dropout_forecast,
    pass1_extract_limits,
    pass2_build_workload_series,
    recommend_pods,
    train_lstm_one_step,
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
# Pod 短期预测服务函数
# =====================================================


def run_pod_forecast_short(date_str: str, input_jsonl: str) -> Dict[str, Any]:
    """
    运行Pod负载短期预测

    Args:
        date_str: 日期字符串（如 "1208"）
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
        logger.info(f"[PodForecastShort] 开始Pod预测，日期: {date_str}")

        # =====================================================
        # 路径配置
        # =====================================================
        forecast_output_dir = os.path.join(settings.OUTPUT_DIR_MODEL_5, date_str, "ForecastOutputWorkloadLSTM")

        # 确保输出目录存在
        ensure_output_dir(forecast_output_dir)

        # 验证输入文件
        if not os.path.exists(input_jsonl):
            error_msg = f"输入JSONL文件不存在: {input_jsonl}"
            logger.error(f"[PodForecastShort] {error_msg}")
            return {"success": False, "error": error_msg}

        # =====================================================
        # 数据准备
        # =====================================================
        logger.info("[PodForecastShort] Pass1: 提取限制信息")
        mem_limit, cpu_quota, cpu_period = pass1_extract_limits(input_jsonl)

        logger.info("[PodForecastShort] Pass2: 构造workload序列")
        wl_series, min_m, max_m, wl_pods, wl_namespace = pass2_build_workload_series(input_jsonl, mem_limit, cpu_quota, cpu_period)

        if min_m is None:
            error_msg = "没有可用的workload序列（缺少quota/period或内存限制信息）"
            logger.error(f"[PodForecastShort] {error_msg}")
            return {"success": False, "error": error_msg}

        start_future_minute = max_m + 1  # type: ignore
        logger.info(f"[PodForecastShort] 数据时间范围: 分钟 {min_m} ~ {max_m}")
        logger.info(f"[PodForecastShort] 预测起始分钟: {start_future_minute}")
        logger.info(f"[PodForecastShort] workload数量: {len(wl_series)}")

        # =====================================================
        # 阶段一：对未来90分钟做实际预测（线上预测，用全量历史）
        # =====================================================
        logger.info("[PodForecastShort] 阶段一：对未来90分钟做实际预测")
        forecast_rows = []
        alert_rows = []

        def get_cur_pods(wl):
            """用最近 IN_LEN 分钟内的去重 pod 数作为当前副本数估计"""
            recent_start = max(min_m, max_m - IN_LEN + 1)  # type: ignore
            pods = set()
            for m in range(recent_start, max_m + 1):  # type: ignore
                pods.update(wl_pods[wl].get(m, set()))
            return max(1, len(pods))

        # 导入时间转换函数
        from app.algorithm.pod_forecast_short import ts_to_local_str

        processed_count = 0
        for wl, series_2d in wl_series.items():
            if len(series_2d) < (IN_LEN + OUT_LEN):
                logger.debug(f"[PodForecastShort] workload '{wl}' 序列长度不足，跳过")
                continue

            logger.debug(f"[PodForecastShort] 处理 workload: {wl}")

            # 获取 namespace
            namespace = wl_namespace.get(wl, "")

            # 训练模型
            model = train_lstm_one_step(series_2d)  # 全量训练，无 cutoff
            if model is None:
                logger.warning(f"[PodForecastShort] workload '{wl}' 训练失败，跳过")
                continue

            # 多步预测
            mean, low, high = mc_dropout_forecast(model, series_2d, OUT_LEN, MC_SAMPLES)

            # 记录预测结果（包含 namespace）
            for i in range(OUT_LEN):
                minute = start_future_minute + i
                ts_sec = minute * BUCKET_SEC
                dt_str = ts_to_local_str(ts_sec)
                forecast_rows.append(
                    {
                        "namespace": namespace,
                        "workload": wl,
                        "dimension": "cpu_util",
                        "minute_offset": i + 1,
                        "ts": ts_sec,
                        "dt": dt_str,
                        "mean": f"{mean[i][0]:.4f}",
                        "low": f"{low[i][0]:.4f}",
                        "high": f"{high[i][0]:.4f}",
                    }
                )
                forecast_rows.append(
                    {
                        "namespace": namespace,
                        "workload": wl,
                        "dimension": "mem_util",
                        "minute_offset": i + 1,
                        "ts": ts_sec,
                        "dt": dt_str,
                        "mean": f"{mean[i][1]:.4f}",
                        "low": f"{low[i][1]:.4f}",
                        "high": f"{high[i][1]:.4f}",
                    }
                )

            # 告警和扩容建议（包含 namespace）
            cpu_high_seq = [high[i][0] for i in range(OUT_LEN)]
            mem_high_seq = [high[i][1] for i in range(OUT_LEN)]
            cur_pods = get_cur_pods(wl)

            for dim, high_seq, warn, crit in [
                ("cpu_util", cpu_high_seq, CPU_WARN, CPU_CRIT),
                ("mem_util", mem_high_seq, MEM_WARN, MEM_CRIT),
            ]:
                peak = max(high_seq) if high_seq else 0.0
                sev = detect_severity(peak, warn, crit)
                if sev:
                    thr = crit if sev == "CRIT" else warn
                    fidx = first_trigger_idx(high_seq, thr) or 0
                    first_min = start_future_minute + fidx
                    first_ts_sec = first_min * BUCKET_SEC
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
                            "first_dt": ts_to_local_str(first_ts_sec),
                        }
                    )

            processed_count += 1

        logger.info(f"[PodForecastShort] 阶段一完成，处理了 {processed_count} 个 workload")

        # =====================================================
        # 阶段二：滑窗回测评估（用历史已有的 ground truth 验证）
        # =====================================================
        logger.info(f"[PodForecastShort] 阶段二：滑窗回测（每个 workload 做 {BACKTEST_WINDOWS} 个窗口）")
        all_backtest_rows = []

        for wl, series_2d in wl_series.items():
            namespace = wl_namespace.get(wl, "")
            rows = backtest_workload(wl, namespace, series_2d, wl_pods, min_m, max_m, BACKTEST_WINDOWS)
            all_backtest_rows.extend(rows)

        logger.info(f"[PodForecastShort] 回测完成，共 {len(all_backtest_rows)} 个窗口")

        # =====================================================
        # 汇总统计
        # =====================================================
        total = len(all_backtest_rows)
        if total == 0:
            logger.warning("[PodForecastShort] 回测结果为空，数据不足")
            backtest_summary = [("警告", f"数据不足，至少需要 {IN_LEN + OUT_LEN * (BACKTEST_WINDOWS + 1)} 分钟序列")]
        else:

            def pct(n):
                return f"{n / total * 100:.2f}"

            cpu_alert_hits = sum(1 for r in all_backtest_rows if r["cpu_alert_match"] == "HIT")
            mem_alert_hits = sum(1 for r in all_backtest_rows if r["mem_alert_match"] == "HIT")
            cpu_scale_hits = sum(1 for r in all_backtest_rows if r["cpu_scale_hit"] == "HIT")
            mem_scale_hits = sum(1 for r in all_backtest_rows if r["mem_scale_hit"] == "HIT")
            avg_cpu_mae = sum(float(r["cpu_mae"]) for r in all_backtest_rows) / total
            avg_mem_mae = sum(float(r["mem_mae"]) for r in all_backtest_rows) / total

            backtest_summary = [
                ("回测窗口总数", total),
                ("涉及workload数", len(set(r["workload"] for r in all_backtest_rows))),
                ("", ""),
                ("── 利用率预测误差 ──", ""),
                ("平均CPU利用率MAE", f"{avg_cpu_mae:.4f}"),
                ("平均MEM利用率MAE", f"{avg_mem_mae:.4f}"),
                ("", ""),
                ("── 告警级别准确率 ──", ""),
                ("CPU告警命中数", cpu_alert_hits),
                ("CPU告警未命中数", total - cpu_alert_hits),
                ("CPU告警准确率(%)", pct(cpu_alert_hits)),
                ("MEM告警命中数", mem_alert_hits),
                ("MEM告警未命中数", total - mem_alert_hits),
                ("MEM告警准确率(%)", pct(mem_alert_hits)),
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
        forecast_csv = os.path.join(forecast_output_dir, "workload_forecast_90m_lstm.csv")
        alert_csv = os.path.join(forecast_output_dir, "workload_alerts_and_scale_pods_90m_lstm.csv")
        backtest_detail_csv = os.path.join(forecast_output_dir, "workload_backtest_detail_lstm.csv")
        backtest_summary_csv = os.path.join(forecast_output_dir, "workload_backtest_summary_lstm.csv")

        write_forecast_csv(forecast_csv, forecast_rows)
        write_alert_reco_csv(alert_csv, alert_rows)
        write_backtest_detail_csv(backtest_detail_csv, all_backtest_rows)
        write_backtest_summary_csv(backtest_summary_csv, backtest_summary)

        logger.info("[PodForecastShort] 预测完成")
        logger.info(f"[PodForecastShort] 预测结果: {forecast_csv}")
        logger.info(f"[PodForecastShort] 告警+扩容建议: {alert_csv}")
        logger.info(f"[PodForecastShort] 回测明细: {backtest_detail_csv}")
        logger.info(f"[PodForecastShort] 回测汇总: {backtest_summary_csv}")

        return {
            "success": True,
            "forecast_csv": forecast_csv,
            "alert_csv": alert_csv,
            "backtest_detail_csv": backtest_detail_csv,
            "backtest_summary_csv": backtest_summary_csv,
            "workload_count": processed_count,
        }

    except Exception as e:
        logger.error(f"[PodForecastShort] Pod预测异常: {e}", exc_info=True)
        return {
            "success": False,
            "error": str(e),
        }
