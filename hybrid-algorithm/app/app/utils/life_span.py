# =====================================================
# 启动事件
# =====================================================
from concurrent.futures import ThreadPoolExecutor
from contextlib import asynccontextmanager

from fastapi import FastAPI

from app.settings import settings
from app.utils.logger import get_logger
from app.utils.tools import expand_path


# 线程池执行器
executor = ThreadPoolExecutor(max_workers=3)
logger = get_logger(__name__)


@asynccontextmanager
async def lifespan(app: FastAPI):
    logger.info("=" * 80)
    logger.info("PSBC API 服务启动")
    logger.info("版本: 1.0.0")
    logger.info(f"模型4 输出目录: {expand_path(settings.OUTPUT_DIR_MODEL_4)}")
    logger.info(f"模型5 输出目录: {expand_path(settings.OUTPUT_DIR_MODEL_5)}")
    logger.info("=" * 80)

    yield  # 分界线：yield 之前是启动，yield 之后是停止

    logger.info("PSBC API 服务关闭")
    executor.shutdown(wait=True)
