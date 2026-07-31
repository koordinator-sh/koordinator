# -*- coding: utf-8 -*-
"""
API 通用工具和依赖模块
"""

import os

from fastapi import Header, HTTPException

from app.utils.logger import get_logger


logger = get_logger(__name__)


# =====================================================
# 错误处理工具
# =====================================================
# 固定的 token 值（实际项目中建议从环境变量或配置文件读取）
EXPECTED_TOKEN = "aGVsbG8scHl0aG9ulQ=="


def verify_token(x_token: str = Header(...)):
    """
    验证请求头中的 X-Token 是否匹配预期值
    """
    if x_token != EXPECTED_TOKEN:
        raise HTTPException(status_code=401, detail="Invalid or missing token")

def handle_service_error(error: Exception, operation: str) -> HTTPException:
    """统一处理服务错误"""
    logger.error(f"{operation}异常: {error}", exc_info=True)
    return HTTPException(status_code=500, detail=f"服务器内部错误: {str(error)}")


def validate_file_exists(file_path: str, operation: str = "操作") -> None:
    """验证文件是否存在"""
    if not os.path.exists(file_path):
        raise HTTPException(status_code=400, detail=f"{operation}的输入文件不存在: {file_path}")


def validate_dir_exists(dir_path: str, operation: str = "操作") -> None:
    """验证目录是否存在"""
    if not os.path.exists(dir_path):
        raise HTTPException(status_code=400, detail=f"{operation}的输入目录不存在: {dir_path}")
