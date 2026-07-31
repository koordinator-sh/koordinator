"""
Author: hxy
Date: 2026/01/14
Description: 日志记录模块（符合最佳实践版本）

提供统一的日志记录接口，支持：
- 开发环境：彩色控制台输出
- 生产环境：JSON 格式结构化日志
- 日志文件轮转（防止磁盘占满）
- 完整的类型注解和文档字符串
"""

from datetime import datetime
import json
import logging
import logging.handlers
import os
from pathlib import Path
import sys
from typing import Any, Optional


# ==================== 常量定义 ====================

LOG_DIR = os.getenv("LOG_DIR", "./logs")
MODE = os.getenv("MODE", "dev")
ENABLE_COLORED = bool(os.getenv("ENABLE_COLORED", True))
ENABLE_FILE = bool(os.getenv("ENABLE_FILE", False))

# 对mode进行检查，必须是"dev"或"prod"
if MODE != "dev" and MODE != "prod":
    raise ValueError("环境变量MODE参数必须是dev或者prod")


# ANSI 颜色代码
class LogColor:
    """日志颜色常量。"""

    DEBUG = "\033[36m"  # 青色
    INFO = "\033[32m"  # 绿色
    WARNING = "\033[33m"  # 黄色
    ERROR = "\033[31m"  # 红色
    CRITICAL = "\033[35m"  # 紫色
    RESET = "\033[0m"  # 重置


# ==================== 格式化器 ====================


class ColoredFormatter(logging.Formatter):
    """彩色日志格式化器（开发环境使用）。

    为不同日志级别添加颜色，提高终端可读性。
    """

    # 颜色映射
    COLORS = {
        "DEBUG": LogColor.DEBUG,
        "INFO": LogColor.INFO,
        "WARNING": LogColor.WARNING,
        "ERROR": LogColor.ERROR,
        "CRITICAL": LogColor.CRITICAL,
    }

    def format(self, record: logging.LogRecord) -> str:
        """格式化日志记录并添加颜色。

        Args:
            record: 日志记录

        Returns:
            格式化后的日志字符串
        """
        # 创建副本或使用临时变量
        colored_record = logging.LogRecord(
            name=record.name,
            level=record.levelno,
            pathname=record.pathname,
            lineno=record.lineno,
            msg=record.msg,
            args=record.args,
            exc_info=record.exc_info,
        )

        # 修改副本
        levelname = record.levelname
        if levelname in self.COLORS:
            color = self.COLORS[levelname]
            colored_record.levelname = f"{color}{levelname}{LogColor.RESET}"
            if isinstance(record.msg, str):
                colored_record.msg = f"{color}{record.msg}{LogColor.RESET}"

        # 格式化副本
        return super().format(colored_record)


class JSONFormatter(logging.Formatter):
    """JSON 格式化器（生产环境使用）。

    生成结构化的 JSON 日志，便于日志聚合工具解析。
    """

    def format(self, record: logging.LogRecord) -> str:
        """格式化日志记录为 JSON。

        Args:
            record: 日志记录

        Returns:
            JSON 格式的日志字符串
        """
        log_entry: dict[str, Any] = {
            "timestamp": datetime.fromtimestamp(record.created).isoformat(),
            "level": record.levelname,
            "logger": record.name,
            "message": record.getMessage(),
            "module": record.module,
            "function": record.funcName,
            "line": record.lineno,
            "process": record.process,
            "thread": record.thread,
        }

        # 添加异常信息
        if record.exc_info:
            log_entry["exception"] = self.formatException(record.exc_info)

        # 添加上下文信息（通过 extra 传递）
        if hasattr(record, "user_id"):
            log_entry["user_id"] = record.user_id  # type: ignore
        if hasattr(record, "request_id"):
            log_entry["request_id"] = record.request_id  # type: ignore

        return json.dumps(log_entry, ensure_ascii=False)


# ==================== 日志配置 ====================


def get_log_dir() -> Path:
    """获取日志目录路径。

    Returns:
        日志目录路径（自动创建）
    """
    log_dir = Path(LOG_DIR)
    try:
        log_dir.mkdir(parents=True, exist_ok=True)
    except Exception:
        # 如果无法创建 logs 目录，使用 /tmp/logs
        log_dir = Path("/tmp/logs")
        log_dir.mkdir(parents=True, exist_ok=True)
    return log_dir


# ==================== 主日志函数 ====================


def _get_logger(
    name: str,
    log_level: str = "INFO",
    enable_file: bool = True,
    enable_colored: bool = True,
) -> logging.Logger:
    """获取日志记录器。

    Args:
        name: 日志记录器名称，通常使用 __name__
        log_level: 日志级别（DEBUG、INFO、WARNING、ERROR、CRITICAL）
        enable_file: 是否启用文件日志
        enable_colored: 是否启用彩色输出（仅终端）

    Returns:
        配置好的日志记录器

    Examples:
        ```python
        # 基础使用
        from core.logger import get_logger

        logger = get_logger(__name__)
        logger.info("应用启动成功")

        # 带上下文信息
        logger.info("用户登录", extra={"user_id": 123, "ip": "192.168.1.1"})

        # 异常日志
        try:
            result = risky_operation()
        except Exception as e:
            logger.error(f"操作失败: {e}", exc_info=True)
        ```
    """
    logger = logging.getLogger(name)

    # 避免重复添加 handler
    if logger.handlers:
        return logger

    # 设置日志级别
    logger.setLevel(getattr(logging, log_level.upper()))

    # ==================== 控制台处理器 ====================
    console_handler = logging.StreamHandler(sys.stdout)
    console_handler.setLevel(getattr(logging, log_level.upper()))

    # 根据环境选择格式化器
    if enable_colored and sys.stdout.isatty():
        # 开发环境：彩色格式
        console_formatter = ColoredFormatter(
            "%(asctime)s | %(levelname)-8s | %(name)s:%(lineno)d | %(message)s",
            datefmt="%Y-%m-%d %H:%M:%S",
        )
    else:
        # 生产环境或非终端：纯文本格式
        console_formatter = logging.Formatter(
            "%(asctime)s | %(levelname)-8s | %(name)s:%(lineno)d | %(message)s",
            datefmt="%Y-%m-%d %H:%M:%S",
        )

    console_handler.setFormatter(console_formatter)
    logger.addHandler(console_handler)

    # ==================== 文件处理器（带轮转）====================
    if enable_file:
        try:
            log_dir = get_log_dir()
            log_file = log_dir / "app.jsonl"

            # 使用 RotatingFileHandler 进行日志轮转
            file_handler = logging.handlers.RotatingFileHandler(
                log_file,
                maxBytes=10 * 1024 * 1024,  # 10 MB
                backupCount=5,
                encoding="utf-8",
            )
            file_handler.setLevel(getattr(logging, log_level.upper()))

            # 文件使用 JSON 格式（生产环境友好）
            file_formatter = JSONFormatter()
            file_handler.setFormatter(file_formatter)
            logger.addHandler(file_handler)

        except Exception as e:
            # 如果文件日志初始化失败，只输出到控制台
            logger.warning(f"无法初始化文件日志: {e}")

    return logger


# ==================== 便捷函数 ====================


def get_logger(name: str) -> logging.Logger:
    """获取开发环境日志记录器（彩色输出）。

    Args:
        name: 日志记录器名称

    Returns:
        配置好的日志记录器
    """
    return _get_logger(
        name,
        log_level="DEBUG" if MODE == "dev" else "INFO",
        enable_colored=ENABLE_COLORED,
        enable_file=ENABLE_FILE,
    )


# ==================== 模块初始化 ====================

# 创建根日志记录器
rootLogger = get_logger(__name__)

# 记录初始化成功
rootLogger.info("日志系统初始化成功")


# ==================== 测试代码 ====================

if __name__ == "__main__":
    """测试日志系统。"""
    test_logger = get_logger(__name__)

    # 测试不同级别的日志
    test_logger.debug("这是调试信息")
    test_logger.info("这是普通信息")
    test_logger.warning("这是警告信息")
    test_logger.error("这是错误信息")
    test_logger.critical("这是严重错误")

    # 测试带上下文的日志
    test_logger.info(
        "用户操作",
        extra={
            "user_id": 123,
            "username": "zhangsan",
            "action": "login",
            "ip": "192.168.1.1",
        },
    )

    # 测试异常日志
    try:
        result = 1 / 0
    except Exception as e:
        test_logger.error(f"发生错误: {e}", exc_info=True)

    print("\n查看日志文件：")
    log_dir = get_log_dir()
    log_file = log_dir / "app.log"
    if log_file.exists():
        print(f"日志文件: {log_file}")
        print(f"文件大小: {log_file.stat().st_size} bytes")
    else:
        print("日志文件尚未创建")
