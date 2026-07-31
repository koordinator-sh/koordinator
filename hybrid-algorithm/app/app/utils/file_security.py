# -*- coding: utf-8 -*-
"""
文件路径安全验证工具
"""

import os

from fastapi import HTTPException


def validate_safe_file_path(
    file_path: str,
    allowed_dirs: list[str],
) -> str:
    """
    验证文件路径是否安全，返回解析后的绝对路径

    Args:
        file_path: 文件路径（相对或绝对）
        allowed_dirs: 允许访问的根目录列表（绝对路径）

    Returns:
        解析后的安全绝对路径

    Raises:
        HTTPException 400: 文件不存在或类型不支持
        HTTPException 403: 路径超出允许范围
    """
    # 1. 解析绝对路径
    abs_path = os.path.abspath(file_path)

    # 2. 验证文件类型
    file_ext = os.path.splitext(abs_path)[1].lower()
    supported_extensions = {".csv", ".txt"}
    if file_ext not in supported_extensions:
        raise HTTPException(status_code=400, detail=f"不支持的文件类型: {file_ext}，仅支持 {', '.join(supported_extensions)}")

    # 3. 验证文件是否存在
    if not os.path.exists(abs_path):
        raise HTTPException(status_code=400, detail=f"文件不存在: {file_path}")

    if not os.path.isfile(abs_path):
        raise HTTPException(status_code=400, detail=f"路径不是文件: {file_path}")

    # 4. 验证路径是否在允许的目录内
    is_allowed = False
    for allowed_dir in allowed_dirs:
        allowed_abs = os.path.abspath(allowed_dir)
        # 使用 commonprefix 检查路径是否在允许的目录内
        common = os.path.commonprefix([abs_path, allowed_abs])
        if common == allowed_abs:
            is_allowed = True
            break

    if not is_allowed:
        raise HTTPException(status_code=403, detail=f"不允许访问该路径: {file_path}，仅允许访问指定目录下的文件")

    return abs_path
