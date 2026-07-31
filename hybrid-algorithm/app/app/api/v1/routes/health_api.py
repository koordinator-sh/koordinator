# -*- coding: utf-8 -*-
"""
健康检查路由模块
"""

import time

from fastapi import APIRouter

from app.schema.health import HealthResponse


router = APIRouter(tags=["系统"])

# 记录启动时间
start_time = time.time()


@router.get("/health", response_model=HealthResponse)
async def health_check():
    """
    健康检查接口

    **功能说明:**
    检查 API 服务及其各组件的运行状态

    **返回说明:**
    - status: 服务状态（healthy/unhealthy）
    - version: API 版本号
    - uptime: 服务运行时间（秒）
    - services: 各服务组件状态
      - performance_data: 性能数据提取服务状态
      - metrics_calculation: 性能指标计算服务状态
      - cluster_analysis: 聚类分析服务状态

    **使用示例:**
    ```bash
    # 检查服务健康状态
    curl "http://localhost:8000/v1/health"

    # 返回示例
    {
      "status": "healthy",
      "version": "1.0.0",
      "uptime": 3600.5,
      "services": {
        "performance_data": true,
        "metrics_calculation": true,
        "cluster_analysis": true
      }
    }
    ```

    **注意事项:**
    - 此接口用于健康检查和监控
    - 无需认证即可访问
    - 返回的服务组件状态为静态值，实际应用中可连接 Celery/Redis 进行真实检查
    """
    return HealthResponse(
        status="healthy",
        version="1.0.0",
        uptime=time.time() - start_time,
        services={
            "performance_data": True,
            "metrics_calculation": True,
            "cluster_analysis": True,
        },
    )
