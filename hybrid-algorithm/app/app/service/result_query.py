# -*- coding: utf-8 -*-
"""
结果查询服务模块

负责聚类结果的查询和处理业务逻辑
"""

import csv
from dataclasses import asdict, dataclass
from datetime import datetime
import json
import os
import re
from typing import Any, Dict, List, Optional

import redis

from app.settings import settings
from app.utils.logger import get_logger
from app.utils.tools import expand_path


logger = get_logger(__name__)

# =====================================================
# 缓存配置
# =====================================================
CACHE_KEY_PREFIX = "cluster_result:"
CACHE_KEY_LATEST = f"{CACHE_KEY_PREFIX}latest"
CACHE_TTL = 600  # 缓存过期时间（秒），默认 10 分钟


@dataclass
class ClusterCsvRow:
    """聚类结果 CSV 行数据"""

    pod: str
    cluster: int
    pod_type: str
    namespace: str


@dataclass
class ClusterResult:
    """聚类结果数据类"""

    output_dir: str
    statistics: str
    csv_data: List[ClusterCsvRow]
    created_at: str
    csv_file_path: str
    txt_file_path: str
    pod_count: int


class ResultQueryService4:
    """结果查询服务类"""

    def __init__(self):
        """初始化服务"""
        self.output_dir = expand_path(settings.OUTPUT_DIR_MODEL_4)
        # 初始化 Redis 客户端用于缓存
        self.redis_client = redis.from_url(settings.redis_url, decode_responses=True)

    def _get_cache(self, key: str) -> Optional[Dict[str, Any]]:
        """从 Redis 获取缓存

        Args:
            key: 缓存键

        Returns:
            缓存的数据，如果不存在或解析失败则返回 None
        """
        try:
            cached_data = self.redis_client.get(key)
            if cached_data:
                logger.debug(f"从缓存获取数据: {key}")
                return json.loads(cached_data)  # type: ignore
            return None
        except json.JSONDecodeError as e:
            logger.warning(f"缓存数据 JSON 解析失败: {e}")
            return None
        except Exception as e:
            logger.error(f"获取缓存失败: {e}", exc_info=True)
            return None

    def _set_cache(self, key: str, value: Any, ttl: int = CACHE_TTL) -> bool:
        """设置 Redis 缓存

        Args:
            key: 缓存键
            value: 要缓存的数据
            ttl: 过期时间（秒）

        Returns:
            是否设置成功
        """
        try:
            # 将 ClusterResult 对象转换为字典
            if isinstance(value, ClusterResult):
                value_dict = asdict(value)
            else:
                value_dict = value

            serialized_data = json.dumps(value_dict, ensure_ascii=False)
            self.redis_client.setex(key, ttl, serialized_data)
            logger.debug(f"设置缓存: {key}, TTL: {ttl}秒")
            return True
        except Exception as e:
            logger.error(f"设置缓存失败: {e}", exc_info=True)
            return False

    def invalidate_cache(self) -> bool:
        """清除聚类结果缓存

        Returns:
            是否清除成功
        """
        try:
            # 清除最新结果缓存
            deleted = self.redis_client.delete(CACHE_KEY_LATEST)
            logger.info(f"清除聚类结果缓存: 删除 {deleted} 个键")
            return True
        except Exception as e:
            logger.error(f"清除缓存失败: {e}", exc_info=True)
            return False

    def get_directory_metadata(self, directory: str) -> Dict[str, Any]:
        """获取目录元数据

        Args:
            directory: 目录路径

        Returns:
            元数据:
            {
                'success': bool,
                'created_at': str,  # 创建时间
                'error': str  # 仅失败时
            }
        """
        try:
            if not os.path.exists(directory):
                return {"success": False, "error": f"目录不存在: {directory}"}

            created_time = datetime.fromtimestamp(os.path.getmtime(directory))
            created_at_str = created_time.strftime("%Y-%m-%d %H:%M:%S")

            return {"success": True, "created_at": created_at_str}

        except Exception as e:
            logger.error(f"获取目录元数据失败: {e}", exc_info=True)
            return {"success": False, "error": str(e)}

    # =====================================================
    # 模型四结果获取
    # =====================================================

    def find_latest_cluster_directory(self) -> Optional[str]:
        """查找最新的聚类结果目录

        Returns:
            最新的聚类结果目录路径，如果不存在则返回 None
        """
        if not os.path.exists(self.output_dir):
            return None

        # 查找所有聚类结果目录 (pod_clustering_*)
        cluster_dirs = []
        cluster_pattern = re.compile(r"^pod_clustering_\d{8}_\d{6}$")

        for item in os.listdir(self.output_dir):
            item_path = os.path.join(self.output_dir, item)
            if os.path.isdir(item_path) and cluster_pattern.match(item):
                cluster_dirs.append(item_path)

        if not cluster_dirs:
            return None

        # 按修改时间排序，获取最新的
        cluster_dirs.sort(key=lambda x: os.path.getmtime(x), reverse=True)
        return cluster_dirs[0]

    def read_clustering_statistics_file(self, directory: str) -> Dict[str, Any]:
        """读取聚类统计信息文件

        Args:
            directory: 聚类结果目录路径

        Returns:
            读取结果:
            {
                'success': bool,
                'content': str,  # 文件内容
                'error': str  # 仅失败时
            }
        """
        try:
            stats_file = os.path.join(directory, "clustering_statistics.txt")

            if not os.path.exists(stats_file):
                return {"success": False, "error": f"聚类统计文件不存在: {stats_file}"}

            with open(stats_file, "r", encoding="utf-8") as f:
                content = f.read()

            return {"success": True, "content": content}

        except Exception as e:
            logger.error(f"读取统计文件失败: {e}", exc_info=True)
            return {"success": False, "error": str(e)}

    def read_pod_clustering_csv_file(self, directory: str) -> Dict[str, Any]:
        """读取聚类结果 CSV 文件

        Args:
            directory: 聚类结果目录路径

        Returns:
            读取结果:
            {
                'success': bool,
                'csv_data': List[ClusterCsvRow],  # CSV 前三列数据
                'pod_count': int,  # Pod 总数
                'error': str  # 仅失败时
            }
        """
        try:
            csv_file = os.path.join(directory, "pod_clustering_results.csv")

            if not os.path.exists(csv_file):
                return {"success": False, "error": f"聚类结果文件不存在: {csv_file}"}

            csv_data: List[ClusterCsvRow] = []
            pod_count = 0

            with open(csv_file, "r", encoding="utf-8") as f:
                csv_reader = csv.DictReader(f)
                for row in csv_reader:
                    pod_count += 1
                    csv_data.append(
                        ClusterCsvRow(
                            pod=row.get("pod", ""),
                            cluster=int(row.get("cluster", 0)),
                            pod_type=row.get("pod_type", ""),
                            namespace=row.get("namespace", ""),
                        )
                    )

            return {"success": True, "csv_data": csv_data, "pod_count": pod_count}

        except Exception as e:
            logger.error(f"读取 CSV 文件失败: {e}", exc_info=True)
            return {"success": False, "error": str(e)}

    def get_last_cluster_result(self) -> Dict[str, Any]:
        """获取最后的聚类分析结果

        Returns:
            聚类结果:
            {
                'success': bool,
                'result': ClusterResult,  # 聚类结果对象
                'error': str  # 仅失败时
            }
        """
        try:
            # 1. 尝试从缓存获取
            cached_data = self._get_cache(CACHE_KEY_LATEST)
            if cached_data:
                logger.info("从缓存获取聚类结果")
                # 从字典重建 ClusterResult 对象
                csv_data = [ClusterCsvRow(**item) for item in cached_data["csv_data"]]
                cluster_result = ClusterResult(
                    output_dir=cached_data["output_dir"],
                    statistics=cached_data["statistics"],
                    csv_data=csv_data,
                    created_at=cached_data["created_at"],
                    csv_file_path=cached_data["csv_file_path"],
                    txt_file_path=cached_data["txt_file_path"],
                    pod_count=cached_data["pod_count"],
                )
                return {"success": True, "result": cluster_result}

            # 2. 缓存未命中，从文件系统读取
            logger.info("缓存未命中，从文件系统读取聚类结果")

            # 查找最新的聚类结果目录
            latest_dir = self.find_latest_cluster_directory()

            if not latest_dir:
                return {"success": False, "error": "未找到聚类分析结果"}

            # 读取统计信息
            stats_result = self.read_clustering_statistics_file(latest_dir)
            if not stats_result["success"]:
                return {"success": False, "error": stats_result["error"]}

            # 读取 CSV 数据
            csv_result = self.read_pod_clustering_csv_file(latest_dir)
            if not csv_result["success"]:
                return {"success": False, "error": csv_result["error"]}

            # 获取元数据
            metadata_result = self.get_directory_metadata(latest_dir)
            if not metadata_result["success"]:
                return {"success": False, "error": metadata_result["error"]}

            # 构建结果对象
            cluster_result = ClusterResult(
                output_dir=latest_dir,
                statistics=stats_result["content"],
                csv_data=csv_result["csv_data"],
                created_at=metadata_result["created_at"],
                csv_file_path=os.path.join(latest_dir, "pod_clustering_results.csv"),
                txt_file_path=os.path.join(latest_dir, "clustering_statistics.txt"),
                pod_count=csv_result["pod_count"],
            )

            # 3. 写入缓存
            self._set_cache(CACHE_KEY_LATEST, cluster_result, CACHE_TTL)

            return {"success": True, "result": cluster_result}

        except Exception as e:
            logger.error(f"获取聚类结果异常: {e}", exc_info=True)
            return {"success": False, "error": str(e)}

    def get_last_cluster_output_file(self, file_type: str) -> Dict[str, Any]:
        """获取指定类型的文件路径

        Args:
            file_type: 文件类型 ('csv' 或 'txt')

        Returns:
            文件路径结果:
            {
                'success': bool,
                'file_path': str,  # 文件路径
                'error': str  # 仅失败时
            }
        """
        try:
            # 查找最新的聚类结果目录
            latest_dir = self.find_latest_cluster_directory()

            if not latest_dir:
                return {"success": False, "error": "未找到聚类分析结果"}

            # 根据文件类型获取路径
            if file_type == "csv":
                file_path = os.path.join(latest_dir, "pod_clustering_results.csv")
            elif file_type == "txt":
                file_path = os.path.join(latest_dir, "clustering_statistics.txt")
            else:
                return {"success": False, "error": f"不支持的文件类型: {file_type}"}

            if not os.path.exists(file_path):
                return {"success": False, "error": f"文件不存在: {file_path}"}

            return {"success": True, "file_path": file_path}

        except Exception as e:
            logger.error(f"获取文件路径异常: {e}", exc_info=True)
            return {"success": False, "error": str(e)}

    def get_cluster_csv_by_task_id(self, task_id: str) -> Dict[str, Any]:
        """根据 task_id 获取聚类 CSV 数据

        Args:
            task_id: Celery 任务 ID

        Returns:
            CSV 数据:
            {
                'success': bool,
                'csv_data': List[Dict[str, Any]],  # CSV 数据
                'error': str  # 仅失败时
            }
        """
        try:
            from app.celery.config import get_celery_app

            # 获取 Celery 应用
            celery_app = get_celery_app()

            # 从 Celery 获取任务结果
            task_result = celery_app.AsyncResult(task_id)

            if task_result.state == 'PENDING':
                return {"success": False, "error": f"任务 {task_id} 不存在或未开始"}

            if task_result.state == 'FAILURE':
                return {"success": False, "error": f"任务 {task_id} 执行失败"}

            if task_result.state != 'SUCCESS':
                return {"success": False, "error": f"任务 {task_id} 未完成，状态: {task_result.state}"}

            # 获取任务结果
            result = task_result.result["result"]

            if not isinstance(result, dict):
                return {"success": False, "error": "任务结果格式错误"}

            if not result.get("success"):
                return {"success": False, "error": result.get("error", "任务执行失败")}

            # 获取聚类输出目录
            cluster_output_dir = result.get("output_dir")
            if not cluster_output_dir:
                return {"success": False, "error": "任务结果中未找到聚类输出目录"}

            # 验证目录是否存在
            if not os.path.exists(cluster_output_dir):
                return {"success": False, "error": f"聚类输出目录不存在: {cluster_output_dir}"}

            # 读取 CSV 数据
            csv_result = self.read_pod_clustering_csv_file(cluster_output_dir)
            if not csv_result["success"]:
                return {"success": False, "error": csv_result["error"]}

            # 转换 CSV 数据为字典列表
            csv_data = [
                {
                    "pod": row.pod,
                    "cluster": row.cluster,
                    "pod_type": row.pod_type,
                    "namespace": row.namespace
                }
                for row in csv_result["csv_data"]
            ]

            logger.info(f"成功获取任务 {task_id} 的 CSV 数据，包含 {len(csv_data)} 条记录")

            return {"success": True, "csv_data": csv_data}

        except Exception as e:
            logger.error(f"根据 task_id 获取 CSV 数据异常: {e}", exc_info=True)
            return {"success": False, "error": str(e)}

    def get_cluster_csv_file_by_task_id(self, task_id: str) -> Dict[str, Any]:
        """根据 task_id 获取聚类 CSV 文件路径

        Args:
            task_id: Celery 任务 ID

        Returns:
            文件路径信息:
            {
                'success': bool,
                'file_path': str,  # CSV 文件路径
                'error': str  # 仅失败时
            }
        """
        try:
            from app.celery.config import get_celery_app

            # 获取 Celery 应用
            celery_app = get_celery_app()

            # 从 Celery 获取任务结果
            task_result = celery_app.AsyncResult(task_id)

            if task_result.state == 'PENDING':
                return {"success": False, "error": f"任务 {task_id} 不存在或未开始"}

            if task_result.state == 'FAILURE':
                return {"success": False, "error": f"任务 {task_id} 执行失败"}

            if task_result.state != 'SUCCESS':
                return {"success": False, "error": f"任务 {task_id} 未完成，状态: {task_result.state}"}

            # 获取任务结果
            result = task_result.result["result"]

            if not isinstance(result, dict):
                return {"success": False, "error": "任务结果格式错误"}

            if not result.get("success"):
                return {"success": False, "error": result.get("error", "任务执行失败")}

            # 获取聚类输出目录
            cluster_output_dir = result.get("cluster_output_dir")
            if not cluster_output_dir:
                return {"success": False, "error": "任务结果中未找到聚类输出目录"}

            # 验证目录是否存在
            if not os.path.exists(cluster_output_dir):
                return {"success": False, "error": f"聚类输出目录不存在: {cluster_output_dir}"}

            # 构建 CSV 文件路径
            csv_file_path = os.path.join(cluster_output_dir, "pod_clustering_results.csv")

            # 验证文件是否存在
            if not os.path.exists(csv_file_path):
                return {"success": False, "error": f"CSV 文件不存在: {csv_file_path}"}

            logger.info(f"成功获取任务 {task_id} 的 CSV 文件路径: {csv_file_path}")

            return {"success": True, "file_path": csv_file_path}

        except Exception as e:
            logger.error(f"根据 task_id 获取 CSV 文件路径异常: {e}", exc_info=True)
            return {"success": False, "error": str(e)}

    def list_all_cluster_results(self) -> Dict[str, Any]:
        """列出所有聚类结果

        Returns:
            聚类结果列表:
            {
                'success': bool,
                'results': List[Dict],  # 结果列表
                'error': str  # 仅失败时
            }
        """
        try:
            if not os.path.exists(self.output_dir):
                return {"success": True, "results": []}

            cluster_dirs = []
            cluster_pattern = re.compile(r"^pod_clustering_\d{8}_\d{6}$")

            for item in os.listdir(self.output_dir):
                item_path = os.path.join(self.output_dir, item)
                if os.path.isdir(item_path) and cluster_pattern.match(item):
                    cluster_dirs.append(item_path)

            # 按修改时间排序
            cluster_dirs.sort(key=lambda x: os.path.getmtime(x), reverse=True)

            results = []
            for directory in cluster_dirs:
                created_time = datetime.fromtimestamp(os.path.getmtime(directory))
                results.append(
                    {"directory": directory, "created_at": created_time.strftime("%Y-%m-%d %H:%M:%S"), "name": os.path.basename(directory)}
                )

            return {"success": True, "results": results}

        except Exception as e:
            logger.error(f"列出聚类结果异常: {e}", exc_info=True)
            return {"success": False, "error": str(e)}


class ResultQueryService5:
    """模型五结果查询服务"""

    def __init__(self):
        """初始化服务"""
        self.output_dir = expand_path(settings.OUTPUT_DIR_MODEL_5)

    def get_alert_csv_file_by_task_id(self, task_id: str, is_long: bool = False) -> Dict[str, Any]:
        """根据 task_id 获取告警 CSV 文件路径

        Args:
            task_id: Celery 任务 ID
            is_long: 是否为长期预测（默认 False，短期预测）

        Returns:
            文件路径信息:
            {
                'success': bool,
                'file_path': str,  # CSV 文件路径
                'error': str  # 仅失败时
            }
        """
        try:
            from app.celery.config import get_celery_app

            # 获取 Celery 应用
            celery_app = get_celery_app()

            # 从 Celery 获取任务结果
            task_result = celery_app.AsyncResult(task_id)

            if task_result.state == 'PENDING':
                return {"success": False, "error": f"任务 {task_id} 不存在或未开始"}

            if task_result.state == 'FAILURE':
                return {"success": False, "error": f"任务 {task_id} 执行失败"}

            if task_result.state != 'SUCCESS':
                return {"success": False, "error": f"任务 {task_id} 未完成，状态: {task_result.state}"}

            # 获取任务结果
            result = task_result.result["result"]

            if not isinstance(result, dict):
                return {"success": False, "error": "任务结果格式错误"}

            if not result.get("success"):
                return {"success": False, "error": result.get("error", "任务执行失败")}

            # 获取 alert_csv 文件路径
            alert_csv = result.get("alert_csv")
            if not alert_csv:
                return {"success": False, "error": "任务结果中未找到 alert_csv"}

            # 验证文件是否存在
            if not os.path.exists(alert_csv):
                return {"success": False, "error": f"CSV 文件不存在: {alert_csv}"}

            logger.info(f"成功获取任务 {task_id} 的告警 CSV 文件路径: {alert_csv}")

            return {"success": True, "file_path": alert_csv}

        except Exception as e:
            logger.error(f"根据 task_id 获取告警 CSV 文件路径异常: {e}", exc_info=True)
            return {"success": False, "error": str(e)}


class ResultQueryService6:
    """模型六结果查询服务"""

    def __init__(self):
        """初始化服务"""
        self.output_dir = expand_path(settings.OUTPUT_DIR_MODEL_6)

    def get_interference_csv_file_by_task_id(self, task_id: str) -> Dict[str, Any]:
        """根据 task_id 获取干扰分析 CSV 文件路径

        Args:
            task_id: Celery 任务 ID

        Returns:
            文件路径信息:
            {
                'success': bool,
                'analysis_csv': str,  # 当前干扰分析CSV文件路径
                'forecast_csv': str,  # 干扰预测CSV文件路径
                'backtest_detail_csv': str,  # 回测明细CSV文件路径
                'backtest_summary_csv': str,  # 回测汇总CSV文件路径
                'error': str  # 仅失败时
            }
        """
        try:
            from app.celery.config import get_celery_app

            # 获取 Celery 应用
            celery_app = get_celery_app()

            # 从 Celery 获取任务结果
            task_result = celery_app.AsyncResult(task_id)

            if task_result.state == 'PENDING':
                return {"success": False, "error": f"任务 {task_id} 不存在或未开始"}

            if task_result.state == 'FAILURE':
                return {"success": False, "error": f"任务 {task_id} 执行失败"}

            if task_result.state != 'SUCCESS':
                return {"success": False, "error": f"任务 {task_id} 未完成，状态: {task_result.state}"}

            # 获取任务结果
            result = task_result.result["result"]

            if not isinstance(result, dict):
                return {"success": False, "error": "任务结果格式错误"}

            if not result.get("success"):
                return {"success": False, "error": result.get("error", "任务执行失败")}

            # 获取各个 CSV 文件路径
            analysis_csv = result.get("analysis_csv")
            forecast_csv = result.get("forecast_csv")
            backtest_detail_csv = result.get("backtest_detail_csv")
            backtest_summary_csv = result.get("backtest_summary_csv")

            if not analysis_csv:
                return {"success": False, "error": "任务结果中未找到 analysis_csv"}

            # 验证文件是否存在
            if not os.path.exists(analysis_csv):
                return {"success": False, "error": f"当前干扰分析CSV不存在: {analysis_csv}"}

            if forecast_csv and not os.path.exists(forecast_csv):
                logger.warning(f"干扰预测CSV不存在: {forecast_csv}")
                forecast_csv = None

            if backtest_detail_csv and not os.path.exists(backtest_detail_csv):
                logger.warning(f"回测明细CSV不存在: {backtest_detail_csv}")
                backtest_detail_csv = None

            if backtest_summary_csv and not os.path.exists(backtest_summary_csv):
                logger.warning(f"回测汇总CSV不存在: {backtest_summary_csv}")
                backtest_summary_csv = None

            logger.info(f"成功获取任务 {task_id} 的干扰分析CSV文件路径")

            return {
                "success": True,
                "analysis_csv": analysis_csv,
                "forecast_csv": forecast_csv,
                "backtest_detail_csv": backtest_detail_csv,
                "backtest_summary_csv": backtest_summary_csv,
            }

        except Exception as e:
            logger.error(f"根据 task_id 获取干扰分析CSV文件路径异常: {e}", exc_info=True)
            return {"success": False, "error": str(e)}

# 创建服务实例
result_query_service = ResultQueryService4()
result_query_service5 = ResultQueryService5()
result_query_service6 = ResultQueryService6()
