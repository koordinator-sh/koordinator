# =====================================================
# 辅助函数
# =====================================================

import os
import time


def expand_path(path: str) -> str:
    """扩展路径中的波浪线和环境变量"""
    return os.path.expanduser(os.path.expandvars(path))


def ensure_output_dir(output_dir: str) -> None:
    """确保输出目录存在"""
    os.makedirs(output_dir, exist_ok=True)


def get_output_path(base_dir: str, prefix: str, extension: str = "jsonl") -> str:
    """生成输出文件路径"""
    ensure_output_dir(base_dir)
    timestamp = time.strftime("%Y%m%d_%H%M%S")
    filename = f"{prefix}_{timestamp}.{extension}"
    return os.path.join(base_dir, filename)
