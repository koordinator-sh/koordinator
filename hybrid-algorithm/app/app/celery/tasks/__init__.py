# -*- coding: utf-8 -*-
"""
Celery 任务模块

导出所有 Celery 任务
"""

# 导入文件处理任务
from app.celery.tasks.files_task import extract_file_task
from app.celery.tasks.model4_task import run_model4
from app.celery.tasks.model5_task import run_emergency_long, run_model5_short


__all__ = ["extract_file_task", "run_model4", "run_model5_short", "run_emergency_long"]
