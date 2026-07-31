# -*- coding: utf-8 -*-
"""
Model6 Celery 任务模块

提供模型6（Workload 干扰分析）的异步任务实现
"""

import os
from typing import Any, Dict

from celery.exceptions import SoftTimeLimitExceeded

from app.celery.config import get_celery_app
from app.service.run_interference import run_interference_analysis
from app.settings import settings
from app.utils.logger import get_logger


logger = get_logger(__name__)

# 获取 Celery 应用实例
celery_app = get_celery_app()


# =====================================================
# Model6 干扰分析任务
# =====================================================


@celery_app.task(
    name="run_model6",
    bind=True,
    max_retries=3,
    default_retry_delay=60,
    soft_time_limit=3600,  # 60分钟软超时
    time_limit=4200,  # 70分钟硬超时
)
def run_model6(
    self,
    data_dir:str,
    input_gz_path: str,
) -> Dict[str, Any]:
    """
    运行 Model6 干扰分析完整流程

    Args:
        self: Celery 任务实例（bind=True）
        input_gz_path: 输入的 .gz 文件路径

    Returns:
        完整流程结果字典:
        {
            'success': bool,
            'analysis_csv': str,
            'forecast_csv': str,
            'backtest_detail_csv': str,
            'backtest_summary_csv': str,
            'workload_count': int,
            'interference_count': int,
            'forecast_signal_count': int,
            'error': str  # 仅失败时
        }

    Raises:
        SoftTimeLimitExceeded: 任务超时
    """
    try:
        logger.info(f"[Celery] 开始运行模型6（干扰分析），输入文件: {input_gz_path}")

        # 更新任务状态：开始
        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 0,
                "message": "开始运行模型6干扰分析...",
                "input_file": input_gz_path,
            },
        )

        # =====================================================
        # 步骤 1: CPI + PSI 数据提取（0-30%）
        # =====================================================
        logger.info("[Celery] 步骤 1/5: CPI + PSI 数据提取")
        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 5,
                "message": "正在提取 CPI + PSI 数据...",
                "step": "extract_data",
                "input_file": input_gz_path,
            },
        )

        # 数据提取在算法内部完成，这里只是进度更新
        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 30,
                "message": "数据提取完成",
                "step": "extract_data",
            },
        )

        # =====================================================
        # 步骤 2-5: 执行干扰分析（30-100%）
        # =====================================================
        logger.info("[Celery] 步骤 2/5: 执行干扰分析")
        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 35,
                "message": "正在进行干扰分析...",
                "step": "interference_analysis",
            },
        )

        # 调用服务层执行算法
        analysis_result = run_interference_analysis(input_gz_path,os.path.join(settings.OUTPUT_DIR_MODEL_6,data_dir))

        # 检查分析结果
        if not analysis_result.get("success"):
            error_msg = analysis_result.get("error", "干扰分析失败")
            logger.error(f"[Celery] 干扰分析失败: {error_msg}")
            raise Exception(f"干扰分析失败: {error_msg}")

        # 提取结果
        analysis_csv = analysis_result["analysis_csv"]
        forecast_csv = analysis_result["forecast_csv"]
        workload_count = analysis_result["workload_count"]
        interference_count = analysis_result["interference_count"]
        forecast_signal_count = analysis_result["forecast_signal_count"]

        logger.info("[Celery] 干扰分析完成")
        logger.info(f"[Celery] 当前干扰分析CSV: {analysis_csv}")
        logger.info(f"[Celery] 干扰预测CSV: {forecast_csv}")
        logger.info(f"[Celery] workload数量: {workload_count}")
        logger.info(f"[Celery] 干扰workload数量: {interference_count}")
        logger.info(f"[Celery] 预测信号数量: {forecast_signal_count}")

        # 更新任务状态：完成
        self.update_state(
            state="SUCCESS",
            meta={
                "progress": 100,
                "message": "模型6干扰分析运行成功！",
                "result": analysis_result,
            },
        )

        return analysis_result

    except SoftTimeLimitExceeded:
        error_msg = "模型6运行超时，请检查数据量或增加超时时间"
        logger.error(f"[Celery] 任务超时: {error_msg}")
        raise TimeoutError(error_msg)


    except Exception as e:
        logger.error(f"[Celery] 模型6运行异常: {e}", exc_info=True)

        # 如果异常可重试，则自动重试
        if self.request.retries < self.max_retries:
            logger.info(f"[Celery] 任务将重试: {self.request.retries + 1}/{self.max_retries}")
            raise self.retry(exc=e)

        raise Exception( str(e))


# =====================================================
# 导出任务名称
# =====================================================

__all__ = [
    "run_model6",
]
