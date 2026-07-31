# -*- coding: utf-8 -*-
"""
任务状态响应模型 for PSBC API
"""

from typing import Any, Optional

from pydantic import BaseModel, Field

from app.schema.common import ResponseStatus


class TaskStatusResponse(BaseModel):
    """任务状态响应"""

    task_id: str = Field(..., description="任务ID")
    status: ResponseStatus = Field(..., description="任务状态")
    progress: float = Field(..., description="进度百分比(0-100)")
    message: str = Field(..., description="状态消息")
    result: None | Any = None


class UploadTaskStatusResponse(TaskStatusResponse):
    """文件上传任务状态响应"""

    uploaded_bytes: Optional[int] = Field(None, description="上传字节数")


class Model4TaskStatusResponse(TaskStatusResponse):
    """Model4 算法任务状态响应"""

    ...

class Model5TaskStatusResponse(TaskStatusResponse):
    """Model5 算法任务状态响应"""

    ...

class Model6TaskStatusResponse(TaskStatusResponse):
    """Model6 算法任务状态响应"""

    ...
