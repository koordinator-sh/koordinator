# -*- coding: utf-8 -*-
"""
API 路由主模块 (v1 版本)

此模块整合所有 v1 版本的 API 路由
采用模块化设计，各功能路由独立管理在 routes/ 目录下
"""

from fastapi import APIRouter, Depends

from app.api.v1.common import verify_token
from app.api.v1.routes import data_api, health_api, model4_api, model5_api, model6_api


protected_router = APIRouter(dependencies=[Depends(verify_token)])

# 创建主路由器
router = APIRouter(prefix="/v1")

# 注册各功能模块路由
router.include_router(health_api.router)
protected_router.include_router(model4_api.router)
protected_router.include_router(model5_api.router)
protected_router.include_router(model6_api.router)
protected_router.include_router(data_api.router)
router.include_router(protected_router)
