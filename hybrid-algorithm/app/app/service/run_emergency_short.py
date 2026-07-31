# -*- coding: utf-8 -*-
"""
EmergencyShort 服务模块

封装 EmergencyShort 数据清洗算法，提供可调用的服务接口
"""

import os
import tempfile
from typing import Any, Dict

from app.algorithm.EmergencyShort import (
    build_burst_seconds_from_jsonl,
    extract_to_temp_jsonl,
    list_hour_files,
    parse_workload,
    tag_and_write_with_array_and_csv,
)
from app.settings import settings
from app.utils.logger import get_logger
from app.utils.tools import ensure_output_dir


logger = get_logger(__name__)

# =====================================================
# EmergencyShort 服务函数
# =====================================================


def run_emergency_short(date_str: str) -> Dict[str, Any]:
    """
    运行应急短期预测数据清洗

    Args:
        date_str: 日期字符串（如 "1208"）

    Returns:
        {
            'success': bool,
            'output_jsonl': str,  # 输出的JSONL文件路径
            'output_csv': str,    # 输出的CSV文件路径
            'error': str          # 仅失败时
        }
    """
    try:
        logger.info(f"[EmergencyShort] 开始数据清洗，日期: {date_str}")

        # =====================================================
        # 路径配置
        # =====================================================
        input_dir = os.path.join(settings.DATA_DIR_MODEL5, date_str)
        burst_data_dir = os.path.join(settings.TEMP_DIR, date_str, "BurstData")

        output_jsonl = os.path.join(burst_data_dir, "performance_with_resource_metrics_with_burst.jsonl")
        output_csv = os.path.join(burst_data_dir, "pod_burst_timestamps.csv")

        # 确保输出目录存在
        ensure_output_dir(burst_data_dir)

        # 验证输入目录
        if not os.path.exists(input_dir):
            error_msg = f"输入数据目录不存在: {input_dir}"
            logger.error(f"[EmergencyShort] {error_msg}")
            return {"success": False, "error": error_msg}

        # =====================================================
        # Pass0：提取目标指标 -> 写入临时 JSONL
        # =====================================================
        file_list = list_hour_files(input_dir, 48)  # 48个文件（4小时数据）
        logger.info(f"[EmergencyShort] 找到 {len(file_list)} 个文件")

        if not file_list:
            error_msg = f"输入目录中没有找到数据文件: {input_dir}"
            logger.error(f"[EmergencyShort] {error_msg}")
            return {"success": False, "error": error_msg}

        temp_jsonl = extract_to_temp_jsonl(input_dir, file_list)

        # =====================================================
        # Pass1：统计 burst 秒
        # =====================================================
        logger.info("[EmergencyShort] 开始统计 burst 秒")
        burst_seconds = build_burst_seconds_from_jsonl(temp_jsonl)

        # =====================================================
        # Pass2：标记 burst_event 数组并写出最终 JSONL + CSV
        # =====================================================
        logger.info("[EmergencyShort] 开始标记 burst 事件并输出")
        tag_and_write_with_array_and_csv(temp_jsonl, output_jsonl, output_csv, burst_seconds)

        # 清理临时文件
        try:
            os.remove(temp_jsonl)
            logger.debug(f"[EmergencyShort] 已删除临时文件: {temp_jsonl}")
        except Exception as e:
            logger.warning(f"[EmergencyShort] 删除临时文件失败: {e}")

        logger.info("[EmergencyShort] 数据清洗完成")
        logger.info(f"[EmergencyShort] 输出 JSONL: {output_jsonl}")
        logger.info(f"[EmergencyShort] 输出 CSV: {output_csv}")

        return {
            "success": True,
            "output_jsonl": output_jsonl,
            "output_csv": output_csv,
        }

    except Exception as e:
        logger.error(f"[EmergencyShort] 数据清洗异常: {e}", exc_info=True)
        return {
            "success": False,
            "error": str(e),
        }
