# -*- coding: utf-8 -*-
"""
Workload 干扰分析服务模块

提供干扰分析算法的业务逻辑封装
"""

import os
import sys
from typing import Any, Dict


# 添加算法目录到 Python 路径
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "algorithm"))

from app.algorithm import Interference
from app.settings import settings
from app.utils.logger import get_logger


logger = get_logger(__name__)


def run_interference_analysis(
    input_gz_path: str,
    output_dir: str | None = None,
) -> Dict[str, Any]:
    """
    运行干扰分析算法

    Args:
        input_gz_path: 输入的 .gz 文件路径
        output_dir: 输出目录（默认使用 settings.OUTPUT_DIR_MODEL_6）

    Returns:
        算法执行结果字典:
        {
            'success': bool,
            'analysis_csv': str,           # 当前干扰分析CSV
            'forecast_csv': str,            # 未来干扰预测CSV
            'backtest_detail_csv': str,    # 回测明细CSV
            'backtest_summary_csv': str,   # 回测汇总CSV
            'workload_count': int,         # Workload数量
            'interference_count': int,     # 干扰workload数量
            'forecast_signal_count': int,  # 预测信号数量
            'error': str                   # 仅失败时
        }
    """
    try:
        logger.info(f"[服务层] 开始执行干扰分析，输入文件: {input_gz_path}")

        # 设置输出目录
        if output_dir is None:
            output_dir = settings.OUTPUT_DIR_MODEL_6
        os.makedirs(output_dir, exist_ok=True)


        # 修改算法模块的 INPUT_SOURCES 和 OUT_DIR
        Interference.INPUT_FILE = input_gz_path
        Interference.OUT_DIR = output_dir

        # 执行算法
        logger.info("[服务层] 开始调用 Interference.main()")
        Interference.main()

        # 构建输出文件路径
        analysis_csv = os.path.join(output_dir, "pod_interference_analysis.csv")
        forecast_csv = os.path.join(output_dir, "pod_interference_forecast.csv")

        # 验证输出文件是否存在
        for csv_file in [analysis_csv, forecast_csv]:
            if not os.path.exists(csv_file):
                raise FileNotFoundError(f"算法输出文件不存在: {csv_file}")

        # 读取统计信息（可选）
        workload_count = _count_workloads(analysis_csv)
        interference_count = _count_interferences(analysis_csv)
        forecast_signal_count = _count_forecasts(forecast_csv)

        logger.info("[服务层] 干扰分析完成")
        logger.info(f"[服务层] workload_count={workload_count}, interference_count={interference_count}")
        logger.info(f"[服务层] analysis_csv={analysis_csv}")
        logger.info(f"[服务层] forecast_csv={forecast_csv}")

        return {
            "success": True,
            "status":"SUCCESS",
            "analysis_csv": analysis_csv,
            "forecast_csv": forecast_csv,
            "workload_count": workload_count,
            "interference_count": interference_count,
            "forecast_signal_count": forecast_signal_count,
        }

    except Exception as e:
        logger.error(f"[服务层] 干扰分析失败: {e}", exc_info=True)
        return {
            "success": False,
            "error": str(e),
        }


def _count_workloads(analysis_csv: str) -> int:
    """统计 workload 数量"""
    try:
        with open(analysis_csv, "r", encoding="utf-8-sig") as f:
            return sum(1 for line in f if line.strip()) - 1  # 减去表头
    except Exception:
        return 0


def _count_interferences(analysis_csv: str) -> int:
    """统计干扰事件数量"""
    try:
        with open(analysis_csv, "r", encoding="utf-8-sig") as f:
            next(f)  # 跳过表头
            return sum(1 for line in f if line.strip())
    except Exception:
        return 0


def _count_forecasts(forecast_csv: str) -> int:
    """统计预测信号数量"""
    try:
        with open(forecast_csv, "r", encoding="utf-8-sig") as f:
            next(f)  # 跳过表头
            return sum(1 for line in f if line.strip())
    except Exception:
        return 0
