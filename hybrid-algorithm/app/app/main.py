# -*- coding: utf-8 -*-
"""
PSBC 项目主程序入口
提供 API 服务和命令行接口
"""

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from fastapi.openapi.docs import get_swagger_ui_html
from fastapi.responses import JSONResponse
from fastapi.staticfiles import StaticFiles
import uvicorn

from app.api.v1.v1 import router
from app.settings import settings
from app.utils.life_span import lifespan
from app.utils.logger import get_logger


# =====================================================
# 日志配置
# =====================================================

logger = get_logger(__name__)

# =====================================================
# FastAPI 应用初始化
# =====================================================

app = FastAPI(
    title="PSBC API",
    description="PSBC (Prometheus System-Based Container) 性能分析 API",
    version="1.0.0",
    docs_url="/docs",
    redoc_url="/redoc",
    lifespan=lifespan,
)

app.mount("/static", StaticFiles(directory="app/static"), name="static")


# CORS 中间件
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

app.include_router(router)

@app.get("/local_docs", include_in_schema=False)
async def custom_swagger_ui_html():
    return get_swagger_ui_html(
        openapi_url="/openapi.json",
        title="LHNP Backend API - Swagger UI",
        swagger_js_url="/static/swagger-ui-bundle.js",  # 自定义 JS 路径
        swagger_css_url="/static/swagger-ui.css",       # 自定义 CSS 路径
        swagger_ui_parameters={
            "swagger_ui": True,
        },
    )

# =====================================================
# 异常处理
# =====================================================


@app.exception_handler(Exception)
async def global_exception_handler(request, exc):
    """全局异常处理器"""
    logger.error(f"未处理的异常: {exc}", exc_info=True)
    return JSONResponse(
        status_code=500,
        content={"status": "error", "message": "服务器内部错误", "details": str(exc)},
    )


# =====================================================
# 主程序入口
# =====================================================

if __name__ == "__main__":
    uvicorn.run(
        "main:app",
        host=settings.API_HOST,
        port=settings.API_PORT,
        reload=settings.API_RELOAD,
    )
