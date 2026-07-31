# -*- coding: utf-8 -*-
"""
文件处理 Celery 任务模块

提供文件解压、清理等异步任务实现
"""

import os
from typing import Any, Dict, List

from celery.exceptions import SoftTimeLimitExceeded

from app.celery.config import get_celery_app
from app.settings import settings
from app.utils.file_utils import (
    ArchiveCorruptedError,
    FileExtensionError,
    FileValidationError,
    cleanup_temp_files,
    extract_file,
    validate_upload_file,
)
from app.utils.logger import get_logger
from app.utils.tools import ensure_output_dir


logger = get_logger(__name__)

# 获取 Celery 应用实例
celery_app = get_celery_app()


# =====================================================
# 文件解压任务
# =====================================================


@celery_app.task(
    name="extract_file_task",
    bind=True,
    max_retries=3,
    default_retry_delay=60,
    soft_time_limit=3000,  # 50分钟软超时
    time_limit=3600,  # 60分钟硬超时
)
def extract_file_task(
    self,
    archive_path: str,
    extract_to: str,
    cleanup_archive: bool = True,
) -> Dict[str, Any]:
    """解压文件的 Celery 任务

    Args:
        self: Celery 任务实例（bind=True）
        archive_path: 压缩文件路径
        extract_to: 解压目标目录
        allowed_extensions: 允许的文件扩展名列表
        cleanup_archive: 是否在解压后删除原压缩文件

    Returns:
        解压结果字典:
        {
            'success': bool,
            'extract_dir': str,  # 解压目录
            'file_count': int,
            'total_size': int,
            'extracted_files': list,
            'error': str  # 仅失败时
        }

    Raises:
        FileValidationError: 文件验证失败
        ArchiveCorruptedError: 压缩文件损坏
        SoftTimeLimitExceeded: 任务超时
    """
    try:
        logger.info(f"[Celery] 开始解压任务: file={archive_path}")

        # 更新任务状态为开始解压
        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 10,
                "message": f"开始解压文件: {os.path.basename(archive_path)}",
            },
        )

        # 验证文件扩展名
        filename = os.path.basename(archive_path)
        try:
            validate_upload_file(filename, settings.FILE_UPLOAD_ALLOWED_EXTENSIONS)  # type: ignore
            logger.info(f"[Celery] 文件验证通过: {filename}")
        except FileExtensionError as e:
            logger.error(f"[Celery] 文件扩展名验证失败: {e}")
            raise

        # 确保目标目录存在
        ensure_output_dir(extract_to)

        # 更新进度
        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 30,
                "message": "正在解压文件...",
            },
        )

        # 执行解压（根据扩展名自动选择解压方法）
        extract_result = extract_file(archive_path, extract_to)

        if not extract_result["success"]:
            error_msg = extract_result.get("error", "未知错误")
            logger.error(f"[Celery] 解压失败: {error_msg}")
            return extract_result

        # 更新进度
        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 80,
                "message": f"解压完成: {extract_result['file_count']} 个文件",
            },
        )

        # 清理原压缩文件
        if cleanup_archive:
            try:
                cleanup_temp_files(archive_path)
                logger.info(f"[Celery] 已删除原压缩文件: {archive_path}")
            except Exception as e:
                logger.warning(f"[Celery] 删除原压缩文件失败（不影响结果）: {e}")

        # 任务成功完成
        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 100,
                "message": f"解压成功: {extract_result['file_count']} 个文件",
            },
        )

        logger.info(f"[Celery] 解压任务完成: files={extract_result['file_count']}, size={extract_result['total_size']}")

        return extract_result

    except ArchiveCorruptedError as e:
        logger.error(f"[Celery] 压缩文件损坏: {e}")
        raise

    except FileValidationError as e:
        logger.error(f"[Celery] 文件验证失败: {e}")
        raise

    except SoftTimeLimitExceeded:
        error_msg = "解压任务超时，请检查文件大小或尝试较小的文件"
        logger.error(f"[Celery] 任务超时: {error_msg}")
        raise

    except Exception as e:
        logger.error(f"[Celery] 解压任务异常: {e}", exc_info=True)

        # 如果异常可重试，则自动重试
        if self.request.retries < self.max_retries:
            logger.info(f"[Celery] 任务将重试: {self.request.retries + 1}/{self.max_retries}")
            raise self.retry(exc=e)

        raise


# =====================================================
# 导出任务名称
# =====================================================

__all__ = [
    "extract_file_task",
]
