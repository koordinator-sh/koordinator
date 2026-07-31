# -*- coding: utf-8 -*-
"""
通用响应模型 for PSBC API
"""

from datetime import datetime
from enum import Enum
from typing import Any, Dict, Optional

from pydantic import BaseModel, Field


class ResponseStatus(str, Enum):
    """响应状态枚举"""

    PENDING = "PENDING"
    SUCCESS = "SUCCESS"
    FAILURE = "FAILURE"
    PROGRESS = "PROGRESS"
    RETRY="RETRY"


class BaseResponse(BaseModel):
    """基础响应模型"""

    status: ResponseStatus = Field(..., description="响应状态")
    message: str = Field(..., description="响应消息")
    timestamp: datetime = Field(default_factory=datetime.now, description="响应时间戳")


class ErrorResponse(BaseResponse):
    """错误响应模型"""

    error_code: Optional[str] = Field(None, description="错误代码")
    details: Optional[Dict[str, Any]] = Field(None, description="错误详情")
