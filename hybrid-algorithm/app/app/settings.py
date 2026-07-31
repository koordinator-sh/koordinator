"""配置文件。

使用pydantic_settings加载环境变量，包含PSBC项目所有配置。
"""

import os
from typing import Any, List

from dotenv import find_dotenv
from pydantic import field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


class Setting(BaseSettings):
    """应用配置类。"""

    # 加载环境变量
    model_config = SettingsConfigDict(
        env_file=find_dotenv(),
        env_file_encoding="utf-8",
        # 忽略空的环境变量
        env_ignore_empty=True,
        extra="ignore",
        validate_default=False,
    )

    # =====================================================
    # API 配置
    # =====================================================
    API_HOST: str = "0.0.0.0"
    API_PORT: int = 8000
    API_RELOAD: bool = True
    API_DEBUG: bool = True

    # =====================================================
    # 路径配置
    # =====================================================
    DATA_DIR_MODEL4: str = "./data/data-model-4/"
    DATA_DIR_MODEL5: str = "./data/data-model-5/"
    DATA_DIR_MODEL6: str = "./data/data-model-6/"
    OUTPUT_DIR_MODEL_4: str = "./output/output-model-4/"
    OUTPUT_DIR_MODEL_5: str = "./output/output-model-5/"
    OUTPUT_DIR_MODEL_6: str = "./output/output-model-6/"
    TEMP_DIR: str = "./temp"
    UPLOAD_DIR: str = "./temp"
    DEFAULT_INPUT_PATH: str = "~/autodl-tmp"

    # =====================================================
    # 算法配置
    # =====================================================

    # PerformanceData 算法配置
    PERF_DATA_DEFAULT_PREFIX: str = "PerformanceData"
    PERF_DATA_SUPPORTED_METRICS: str = (
        "container_cpu_usage_seconds_total,"
        "container_spec_cpu_quota,"
        "container_spec_cpu_period,"
        "container_memory_usage_bytes,"
        "container_spec_memory_limit_bytes,"
        "container_fs_reads_bytes_total,"
        "container_fs_writes_bytes_total,"
        "container_fs_io_time_seconds_total,"
        "container_network_receive_bytes_total,"
        "container_network_transmit_bytes_total"
    )

    # 性能指标计算配置
    METRICS_DEFAULT_PREFIX: str = "performance_with_resource_metrics"
    METRICS_ENABLE_CACHE: bool = True

    # 聚类分析配置
    CLUSTER_DEFAULT_PREFIX: str = "pod_clustering"
    CLUSTER_MAX_K: int = 10
    CLUSTER_RANDOM_STATE: int = 42
    CLUSTER_ENABLE_VISUALIZATION: bool = True
    CLUSTER_VISUALIZATION_FORMAT: str = "png"

    # =====================================================
    # 日志配置
    # =====================================================
    LOG_LEVEL: str = "INFO"
    LOG_FORMAT: str = "%(asctime)s - %(name)s - %(levelname)s - %(message)s"
    LOG_FILE: str = "./logs/api.log"
    LOG_DIR: str = "./logs"
    MODE: str = "dev"
    ENABLE_COLORED: bool = True
    ENABLE_FILE: bool = False

    # =====================================================
    # 文件上传配置
    # =====================================================
    FILE_UPLOAD_ALLOWED_EXTENSIONS: str | list[str] = [".7z", ".gz" , ".tar"]
    FILE_UPLOAD_MAX_SIZE: int = 0  # 0 表示不限制
    FILE_UPLOAD_KEEP_ARCHIVE: bool = False
    FILE_UPLOAD_CHUNK_SIZE: int = 8388608  # 8MB
    FILE_UPLOAD_ENABLE_PROGRESS: bool = True

    # =====================================================
    # Redis 配置（Celery Broker + Backend）
    # =====================================================
    REDIS_HOST: str = "localhost"
    REDIS_PORT: int = 6379
    REDIS_PASSWORD: str = ""
    REDIS_DB: int = 0

    # =====================================================
    # Flower 监控配置
    # =====================================================
    FLOWER_PORT: int = 5555

    # =====================================================
    # celery
    # =====================================================
    CELERY_WORKER_NAME: str = "matching_worker"
    CELERY_CONCURRENCY: int = os.cpu_count() or 2  # 默认使用所有 CPU 核心
    CELERY_TASK_TIME_LIMIT: int = 7200  # 单个任务超时（秒）
    CELERY_TASK_SOFTTIME_LIMIT: int = 6600  # 软超时时间（秒）
    CELERY_RESULT_EXPIRES: int = 3600 * 24  # 结果过期时间（秒）
    CELERY_PREFETCH_MULTIPLIER: int = 1  # 预取任务数量
    CELERY_TASK_ACKS_LATE: bool = True  # 完成后才确认
    CELERY_REJECT_ON_WORKER_LOST: bool = True  # Worker 丢失时拒绝任务

    @field_validator("MODE")
    @classmethod
    def validate_mode(cls, v: str) -> str:
        """验证 MODE 参数。"""
        if v not in ["dev", "prod"]:
            raise ValueError("MODE 参数必须是 dev 或 prod")
        return v

    @field_validator("PERF_DATA_SUPPORTED_METRICS")
    @classmethod
    def parse_metrics(cls, v: str) -> List[str]:
        """解析指标列表。"""
        return [m.strip() for m in v.split(",") if m.strip()]

    @field_validator("FILE_UPLOAD_ALLOWED_EXTENSIONS", mode="before")
    @classmethod
    def parse_extensions(cls, v: str | list[str]) -> List[str]:
        """解析文件扩展名列表。"""
        if isinstance(v, list):
            return v
        return [ext.strip() for ext in v.split(",") if ext.strip()]

    @property
    def redis_url(self) -> str:
        """生成 Redis 连接 URL（用于 Celery broker 和 backend）。"""
        if self.REDIS_PASSWORD:
            return f"redis://:{self.REDIS_PASSWORD}@{self.REDIS_HOST}:{self.REDIS_PORT}/{self.REDIS_DB}"
        return f"redis://{self.REDIS_HOST}:{self.REDIS_PORT}/{self.REDIS_DB}"

    def model_post_init(self, context: Any) -> None:
        """模型初始化后的处理。"""
        pass


settings = Setting()  # type: ignore
