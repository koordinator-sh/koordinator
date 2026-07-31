# -*- coding: utf-8 -*-
"""
Model5 Celery 任务模块

提供模型5（Pod预测）的异步任务实现
"""

import time
from typing import Any, Dict

from celery.exceptions import SoftTimeLimitExceeded

from app.celery.config import get_celery_app
from app.service.run_emergency_long import run_emergency_long
from app.service.run_emergency_short import run_emergency_short
from app.service.run_pod_forecast_long import run_pod_forecast_long
from app.service.run_pod_forecast_short import run_pod_forecast_short
from app.settings import settings
from app.utils.logger import get_logger


logger = get_logger(__name__)

# 获取 Celery 应用实例
celery_app = get_celery_app()


# =====================================================
# Model5 完整流程任务
# =====================================================


@celery_app.task(
    name="run_model5_short",
    bind=True,
    max_retries=3,
    default_retry_delay=60,
    soft_time_limit=10800,  # 60分钟软超时
    time_limit=20800,  # 70分钟硬超时
)
def run_model5_short(
    self,
    date_str: str,
) -> Dict[str, Any]:
    """
    运行 Model5 完整流程（数据清洗 -> Pod预测）

    Args:
        self: Celery 任务实例（bind=True）
        date_str: 日期字符串（如 "1208"）

    Returns:
        完整流程结果字典:
        {
            'success': bool,
            'burst_data_jsonl': str,
            'burst_data_csv': str,
            'forecast_csv': str,
            'alert_csv': str,
            'backtest_detail_csv': str,
            'backtest_summary_csv': str,
            'workload_count': int,
            'error': str  # 仅失败时
        }

    Raises:
        SoftTimeLimitExceeded: 任务超时
    """
    try:
        logger.info(f"[Celery] 开始运行模型5（完整流程），日期: {date_str}")

        # 更新任务状态：开始
        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 0,
                "message": "开始运行模型5...",
                "date_str": date_str,
            },
        )

        # =====================================================
        # 步骤 1: EmergencyShort 数据清洗
        # =====================================================
        logger.info("[Celery] 步骤 1/2: EmergencyShort 数据清洗")
        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 5,
                "message": "正在进行数据清洗...",
                "step": "emergency_short",
                "date_str": date_str,
            },
        )

        clean_result = run_emergency_short(date_str)

        # 检查清洗结果
        if not clean_result.get("success"):
            error_msg = clean_result.get("error", "数据清洗失败")
            logger.error(f"[Celery] 步骤 1 失败: {error_msg}")
            raise Exception(f"数据清洗失败: {error_msg}")

        burst_data_jsonl = clean_result["output_jsonl"]
        burst_data_csv = clean_result["output_csv"]

        logger.info("[Celery] 步骤 1 完成")
        logger.info(f"[Celery] Burst数据JSONL: {burst_data_jsonl}")
        logger.info(f"[Celery] Burst时间点CSV: {burst_data_csv}")

        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 20,
                "message": "数据清洗完成",
                "step": "emergency_short",
                "burst_data_jsonl": burst_data_jsonl,
                "burst_data_csv": burst_data_csv,
            },
        )

        # =====================================================
        # 步骤 2: Pod 短期预测
        # =====================================================
        logger.info("[Celery] 步骤 2/2: Pod 短期预测")
        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 25,
                "message": "正在进行Pod预测（LSTM训练）...",
                "step": "pod_forecast",
            },
        )

        forecast_result = run_pod_forecast_short(date_str, burst_data_jsonl)

        # 检查预测结果
        if not forecast_result.get("success"):
            error_msg = forecast_result.get("error", "Pod预测失败")
            logger.error(f"[Celery] 步骤 2 失败: {error_msg}")
            raise Exception(f"Pod预测失败: {error_msg}")
            return {
                "success": False,
                "error": f"Pod预测失败: {error_msg}",
                "step": "pod_forecast",
            }

        forecast_csv = forecast_result["forecast_csv"]
        alert_csv = forecast_result["alert_csv"]
        backtest_detail_csv = forecast_result["backtest_detail_csv"]
        backtest_summary_csv = forecast_result["backtest_summary_csv"]
        workload_count = forecast_result["workload_count"]

        logger.info("[Celery] 步骤 2 完成")
        logger.info(f"[Celery] 预测结果CSV: {forecast_csv}")
        logger.info(f"[Celery] 告警+扩容建议CSV: {alert_csv}")
        logger.info(f"[Celery] 回测明细CSV: {backtest_detail_csv}")
        logger.info(f"[Celery] 回测汇总CSV: {backtest_summary_csv}")
        logger.info(f"[Celery] 处理的workload数量: {workload_count}")

        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 95,
                "message": "预测完成",
                "step": "pod_forecast",
                "workload_count": workload_count,
            },
        )

        # =====================================================
        # 构建完整流程结果
        # =====================================================
        full_pipeline_result = {
            "success": True,
            "status":"SUCCESS",
            "burst_data_jsonl": burst_data_jsonl,
            "burst_data_csv": burst_data_csv,
            "forecast_csv": forecast_csv,
            "alert_csv": alert_csv,
            "backtest_detail_csv": backtest_detail_csv,
            "backtest_summary_csv": backtest_summary_csv,
            "workload_count": workload_count,
        }

        # 更新任务状态：完成
        self.update_state(
            state="SUCCESS",
            meta={
                "progress": 100,
                "message": "模型5 运行成功！",
                "result": full_pipeline_result,
            },
        )

        logger.info(f"[Celery] 模型5 运行成功: workload_count={workload_count}")

        return full_pipeline_result

    except SoftTimeLimitExceeded:
        error_msg = "模型5 运行超时，请检查数据量或增加超时时间"
        logger.error(f"[Celery] 任务超时: {error_msg}")
        raise TimeoutError(error_msg)
        return {
            "success": False,
            "error": error_msg,
        }

    except Exception as e:
        logger.error(f"[Celery] 模型5 运行异常: {e}", exc_info=True)

        # 如果异常可重试，则自动重试
        if self.request.retries < self.max_retries:
            logger.info(f"[Celery] 任务将重试: {self.request.retries + 1}/{self.max_retries}")
            raise self.retry(exc=e)
        raise Exception(str(e))

        return {
            "success": False,
            "error": str(e),
        }


@celery_app.task(
    name="run_model5_long",
    bind=True,
    max_retries=3,
    default_retry_delay=60,
    soft_time_limit=259200,  # 3天时间
    time_limit=300000,  # 3天多一点时间
)
def run_model5_long(
    self,
    date_str: str,
) -> Dict[str, Any]:
    """
    运行 Model5 长期预测完整流程（数据清洗 -> Pod预测）

    Args:
        self: Celery 任务实例（bind=True）
        date_str: 最后一天的日期字符串（如 "1210"）

    Returns:
        完整流程结果字典:
        {
            'success': bool,
            'burst_data_jsonl': str,
            'burst_data_csv': str,
            'forecast_csv': str,
            'alert_csv': str,
            'backtest_detail_csv': str,
            'backtest_summary_csv': str,
            'workload_count': int,
            'error': str  # 仅失败时
        }

    Raises:
        SoftTimeLimitExceeded: 任务超时
    """
    try:
        logger.info(f"[Celery] 开始运行模型5长期预测，最后一天: {date_str}")

        # 更新任务状态：开始
        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 0,
                "message": "开始运行模型5长期预测...",
                "date_str": date_str,
            },
        )

        # =====================================================
        # 步骤 1: EmergencyLong 数据清洗
        # =====================================================
        logger.info("[Celery] 步骤 1/2: EmergencyLong 数据清洗（处理单目录多文件）")
        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 5,
                "message": "正在进行数据清洗（处理单目录多文件）...",
                "step": "emergency_long",
                "date_str": date_str,
            },
        )

        clean_result = run_emergency_long(date_str)

        # 检查清洗结果
        if not clean_result.get("success"):
            error_msg = clean_result.get("error", "数据清洗失败")
            logger.error(f"[Celery] 步骤 1 失败: {error_msg}")
            raise Exception(f"数据清洗失败: {error_msg}")


        burst_data_jsonl = clean_result["output_jsonl"]
        burst_data_csv = clean_result["output_csv"]

        logger.info("[Celery] 步骤 1 完成")
        logger.info(f"[Celery] Burst数据JSONL: {burst_data_jsonl}")
        logger.info(f"[Celery] Burst时间点CSV: {burst_data_csv}")

        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 20,
                "message": "数据清洗完成",
                "step": "emergency_long",
                "burst_data_jsonl": burst_data_jsonl,
                "burst_data_csv": burst_data_csv,
            },
        )

        # =====================================================
        # 步骤 2: Pod 长期预测
        # =====================================================
        logger.info("[Celery] 步骤 2/2: Pod 长期预测（24小时预测，输入6小时）")
        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 25,
                "message": "正在进行Pod长期预测（LSTM训练）...",
                "step": "pod_forecast_long",
            },
        )

        forecast_result = run_pod_forecast_long(date_str,burst_data_jsonl)

        # 检查预测结果
        if not forecast_result.get("success"):
            error_msg = forecast_result.get("error", "Pod长期预测失败")
            logger.error(f"[Celery] 步骤 2 失败: {error_msg}")
            raise Exception(f"Pod长期预测失败: {error_msg}")

        forecast_csv = forecast_result["forecast_csv"]
        alert_csv = forecast_result["alert_csv"]
        backtest_detail_csv = forecast_result["backtest_detail_csv"]
        backtest_summary_csv = forecast_result["backtest_summary_csv"]
        workload_count = forecast_result["workload_count"]

        logger.info("[Celery] 步骤 2 完成")
        logger.info(f"[Celery] 预测结果CSV: {forecast_csv}")
        logger.info(f"[Celery] 告警+扩容建议CSV: {alert_csv}")
        logger.info(f"[Celery] 回测明细CSV: {backtest_detail_csv}")
        logger.info(f"[Celery] 回测汇总CSV: {backtest_summary_csv}")
        logger.info(f"[Celery] 处理的workload数量: {workload_count}")

        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 95,
                "message": "预测完成",
                "step": "pod_forecast_long",
                "workload_count": workload_count,
            },
        )

        # =====================================================
        # 构建完整流程结果
        # =====================================================
        full_pipeline_result = {
            "success": True,
            "status":"SUCCESS",
            "burst_data_jsonl": burst_data_jsonl,
            "burst_data_csv": burst_data_csv,
            "forecast_csv": forecast_csv,
            "alert_csv": alert_csv,
            "backtest_detail_csv": backtest_detail_csv,
            "backtest_summary_csv": backtest_summary_csv,
            "workload_count": workload_count,
        }

        # 更新任务状态：完成
        self.update_state(
            state="SUCCESS",
            meta={
                "progress": 100,
                "message": "模型5长期预测运行成功！",
                "result": full_pipeline_result,
            },
        )

        logger.info(f"[Celery] 模型5长期预测运行成功: workload_count={workload_count}")

        return full_pipeline_result

    except SoftTimeLimitExceeded:
        error_msg = "模型5长期预测运行超时，请检查数据量或增加超时时间"
        logger.error(f"[Celery] 任务超时: {error_msg}")
        raise TimeoutError(error_msg)

    except Exception as e:
        logger.error(f"[Celery] 模型5长期预测运行异常: {e}", exc_info=True)

        # 如果异常可重试，则自动重试
        if self.request.retries < self.max_retries:
            logger.info(f"[Celery] 任务将重试: {self.request.retries + 1}/{self.max_retries}")
            raise self.retry(exc=e)
        raise Exception(str(e))


# =====================================================
# 导出任务名称
# =====================================================

__all__ = [
    "run_model5_short",
    "run_model5_long",
]
