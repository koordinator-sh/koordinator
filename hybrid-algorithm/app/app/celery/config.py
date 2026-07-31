"""Celery应用配置。

定义Celery应用实例，供API和Worker共同使用。
配置从app.core.settings加载。
"""

from celery import Celery
from celery.schedules import crontab

from app.settings import settings


# 创建Celery应用实例
celery_app = Celery(
    settings.CELERY_WORKER_NAME,
    broker=settings.redis_url,
    backend=settings.redis_url,
    include=[
        "app.celery.tasks.files_task",
        "app.celery.tasks.model4_task",
        "app.celery.tasks.model5_task",
        "app.celery.tasks.model6_task",
    ],  # 自动发现任务模块
    task_serializer="json",
    accept_content=["json"],
    result_serializer="json",
    timezone="Asia/Shanghai",
    enable_utc=True,
)

# Celery配置
celery_app.conf.update(
    # 并发控制
    worker_concurrency=settings.CELERY_CONCURRENCY,
    worker_prefetch_multiplier=settings.CELERY_PREFETCH_MULTIPLIER,  # 不预取任务
    # 任务结果过期时间（1小时）
    result_expires=settings.CELERY_RESULT_EXPIRES,
    # 任务重试配置
    task_acks_late=settings.CELERY_TASK_ACKS_LATE,
    task_reject_on_worker_lost=settings.CELERY_REJECT_ON_WORKER_LOST,
    task_time_limit=settings.CELERY_TASK_TIME_LIMIT,
    task_soft_time_limit=settings.CELERY_TASK_SOFTTIME_LIMIT,
    # 序列化
    task_serializer="json",
    result_serializer="json",
    accept_content=["json"],
)

# 定时任务配置（可选）
celery_app.conf.beat_schedule = {
    # 每天凌晨清理一次临时数据
    "cleanup-temp-data-everyday": {
        "task": "app.celery.tasks.cleanup_temp_data",
        "schedule": crontab(hour=2, minute=0),
    },
}


def get_celery_app():
    """获取Celery应用实例。

    Returns:
        Celery应用实例
    """
    return celery_app


# 导出主要的配置供外部使用
__all__ = ["celery_app", "get_celery_app"]
