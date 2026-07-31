# -*- coding: utf-8 -*-
"""
模型四 Celery 任务模块（重构版）

使用 Model4.py 整合算法，提供完整流程的异步任务实现
"""

import time
from typing import Any, Dict

from celery.exceptions import SoftTimeLimitExceeded

from app.celery.config import get_celery_app
from app.service.result_query import result_query_service
from app.service.run_model4 import run_model4_algorithm
from app.settings import settings
from app.utils.file_utils import get_latest_dir
from app.utils.logger import get_logger


logger = get_logger(__name__)

# 获取 Celery 应用实例
celery_app = get_celery_app()


# =====================================================
# 模型四完整算法任务
# =====================================================


@celery_app.task(
    name="run_model4",
    bind=True,
    max_retries=3,
    default_retry_delay=60,
    soft_time_limit=3000,  # 50分钟软超时
    time_limit=3600,  # 60分钟硬超时
)
def run_model4(
    self,
) -> Dict[str, Any]:
    """运行模型四完整算法（整合版）

    使用 Model4.py 整合算法，一步完成所有分析流程：
    - 数据提取 (0-35%): 从 Prometheus .gz 文件提取 Pod 性能数据
    - 特征聚合 (35-50%): 聚合 Pod 级资源指标
    - 特征预处理 (50-65%): log 变换和比例计算
    - KMeans 聚类 (65-80%): 识别 Pod 类型
    - 类型标注 (80-90%): 标注 Pod 资源类型
    - 结果输出 (90-100%): 生成 CSV、PNG 和统计文件

    Args:
        self: Celery 任务实例（bind=True）

    Returns:
        完整流程结果字典:
        {
            'success': bool,
            'status': str,
            'output_dir': str,
            'pod_count': int,
            'cluster_count': int,
            'cluster_info': dict,
            'csv_file': str,
            'png_file': str,
            'txt_file': str,
            'error': str  # 仅失败时
        }

    Raises:
        SoftTimeLimitExceeded: 任务超时
    """
    try:
        logger.info("[Celery] 开始运行模型四完整算法（整合版）")

        # 更新任务状态：开始
        self.update_state(
            state="PROGRESS",
            meta={
                "progress": 0,
                "message": "开始运行模型四算法...",
            },
        )

        # =====================================================
        # 获取输入输出目录
        # =====================================================
        # 自动获取最新数据目录
        input_dir = get_latest_dir(settings.DATA_DIR_MODEL4)
        if not input_dir:
            raise Exception(f"未找到数据目录: {settings.DATA_DIR_MODEL4}")

        logger.info(f"使用数据目录: {input_dir}")

        # 创建输出目录（带时间戳）
        timestamp = time.strftime("%Y%m%d_%H%M%S")
        output_dir = f"{settings.OUTPUT_DIR_MODEL_4}/pod_clustering_{timestamp}"
        logger.info(f"输出目录: {output_dir}")

        # =====================================================
        # 定义进度回调函数
        # =====================================================
        def update_progress(progress: int, message: str):
            """更新 Celery 任务进度"""
            logger.info(f"[Progress] {progress}%: {message}")
            self.update_state(
                state="PROGRESS",
                meta={
                    "progress": progress,
                    "message": message,
                },
            )

        # =====================================================
        # 运行模型四算法（完整流程）
        # =====================================================
        logger.info("[Celery] 调用模型四算法服务...")
        result = run_model4_algorithm(
            input_dir=settings.DATA_DIR_MODEL4 +"/"+ input_dir,
            output_dir=output_dir,
            progress_callback=update_progress,
        )

        # =====================================================
        # 检查算法执行结果
        # =====================================================
        if not result.get("success"):
            error_msg = result.get("error", "模型四算法执行失败")
            logger.error(f"[Celery] 算法执行失败: {error_msg}")
            raise Exception(f"模型四算法执行失败: {error_msg}")

        logger.info(
            f"[Celery] 算法执行成功: "
            f"pod_count={result.get('pod_count', 0)}, "
            f"cluster_count={result.get('cluster_count', 0)}, "
            f"output_dir={result.get('output_dir')}"
        )

        # =====================================================
        # 清除结果查询缓存
        # =====================================================
        try:
            result_query_service.invalidate_cache()
            logger.info("[Celery] 已清除结果查询缓存")
        except Exception as e:
            logger.warning(f"[Celery] 清除缓存失败（不影响任务结果）: {e}")

        # =====================================================
        # 更新任务状态：完成
        # =====================================================
        self.update_state(
            state="SUCCESS",
            meta={
                "progress": 100,
                "message": f"模型四算法运行成功！共分析 {result.get('pod_count', 0)} 个 Pod，识别 {result.get('cluster_count', 0)} 种聚类类型",
                "result": result,
            },
        )

        logger.info(f"[Celery] 模型四任务完成: {result}")

        return result

    except SoftTimeLimitExceeded:
        error_msg = "模型四算法运行超时（超过50分钟），请检查数据量或增加超时时间"
        logger.error(f"[Celery] 任务超时: {error_msg}")
        raise TimeoutError(error_msg)

    except Exception as e:
        logger.error(f"[Celery] 模型四算法运行异常: {e}", exc_info=True)

        # 如果异常可重试，则自动重试
        if self.request.retries < self.max_retries:
            logger.info(f"[Celery] 任务将重试: {self.request.retries + 1}/{self.max_retries}")
            raise self.retry(exc=e)

        # 重试次数用完，重新抛出异常
        raise


# =====================================================
# 导出任务名称
# =====================================================

__all__ = [
    "run_model4",
]
