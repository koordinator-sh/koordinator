# -*- coding: utf-8 -*-
"""
健康检查相关模型 for PSBC API
"""

from typing import Dict

from pydantic import BaseModel, Field


class HealthResponse(BaseModel):
    """健康检查响应"""

    status: str = Field(..., description="服务状态")
    version: str = Field(..., description="API 版本")
    uptime: float = Field(..., description="运行时间(秒)")
    services: Dict[str, bool] = Field(..., description="各服务组件状态")
