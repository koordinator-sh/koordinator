# -*- coding: utf-8 -*-
"""
Model4 算法路由模块

负责处理 Model4 算法执行、状态查询等 HTTP 请求
业务逻辑在 service 层实现
"""

from celery.result import AsyncResult
from fastapi import APIRouter, HTTPException
from fastapi.responses import FileResponse

from app.api.v1.common import handle_service_error
from app.celery.config import celery_app
from app.celery.tasks import model4_task as model4_task
from app.schema.task_status import Model4TaskStatusResponse
from app.service.result_query import result_query_service
from app.utils.logger import get_logger


logger = get_logger(__name__)

router = APIRouter(tags=["模型四"])


# =====================================================
# Model4 算法端点
# =====================================================


@router.post("/run-model4", response_model=Model4TaskStatusResponse)
async def run_algorithm4():
    """
    运行模型四聚类分析算法（完整流程）

    **功能说明:**
    执行 Model4 完整算法流程，包括：
    1. PerformanceData 提取（0-35%）：从 Prometheus JSON 提取容器性能数据
    2. 性能指标计算（35-70%）：计算 CPU、内存、IO、网络等指标
    3. 聚类分析（70-100%）：KMeans 聚类，识别 Pod 类型

    **返回说明:**
    - task_id: 任务 ID，用于查询执行状态
    - status: 任务状态（PENDING/PROGRESS/SUCCESS/FAILURE）
    - progress: 执行进度（0-100）
    - message: 状态消息

    **使用示例:**
    ```bash
    # 运行算法
    curl -X POST "http://localhost:8000/v1/run-model4"

    # 返回示例
    {
      "task_id": "abc-123-def",
      "status": "PENDING",
      "progress": 0,
      "message": "算法任务创建成功！"
    }

    # 查询任务状态
    curl "http://localhost:8000/v1/model4-status/{task_id}"
    ```

    **注意事项:**
    - 需要先通过 `/upload-file` 上传数据文件到模型四目录
    - 算法会自动使用 `DATA_DIR_MODEL4/` 目录下最新数据
    - 完成后自动清除聚类结果缓存
    """
    try:
        logger.info("创建 Model4 算法任务")

        # 创建 Celery 任务
        task = model4_task.run_model4.delay()  # type: ignore

        if not task.id:
            raise HTTPException(status_code=500, detail="创建任务失败")

        logger.info(f"Model4 算法任务已提交: task_id={task.id}")

        result = AsyncResult(task.id, app=celery_app)

        return {
            "task_id": task.id,
            "status": result.state,
            "progress": 0,
            "message": "算法任务创建成功！",
        }

    except Exception as e:
        logger.error(f"运行算法异常: {e}", exc_info=True)
        raise handle_service_error(e, "运行算法")


@router.get("/model4-status/{task_id}", response_model=Model4TaskStatusResponse)
async def get_model4_status(task_id: str):
    """
    查询模型四聚类分析任务状态

    **功能说明:**
    根据任务 ID 查询 Model4 算法的执行状态和进度

    **参数说明:**
    - task_id: 任务 ID（由 /run-model4 接口返回）

    **返回说明:**
    - task_id: 任务 ID
    - status: 任务状态（PENDING/PROGRESS/SUCCESS/FAILURE）
    - progress: 执行进度（0-100）
    - message: 状态消息
    - step: 当前执行步骤（可选）
    - pod_count: Pod 数量（完成后）
    - performance_data_file: PerformanceData 输出文件路径（完成后）
    - metrics_file: 性能指标输出文件路径（完成后）
    - cluster_output_dir: 聚类分析输出目录路径（完成后）
    - result: 任务完整结果（成功时），失败时为 None 或包含错误信息

    **使用示例:**
    ```bash
    # 查询任务状态
    curl "http://localhost:8000/v1/model4-status/abc-123-def"

    # 返回示例（进行中）
    {
      "task_id": "abc-123-def",
      "status": "PROGRESS",
      "progress": 50,
      "message": "正在计算性能指标...",
      "step": "性能指标计算"
    }

    # 返回示例（完成）
    {
      "task_id": "abc-123-def",
      "status": "SUCCESS",
      "progress": 100,
      "message": "算法运行成功！",
      "pod_count": 150,
      "performance_data_file": "/app/output/output-model-4/PerformanceData_20250417_143022.jsonl",
      "metrics_file": "/app/output/output-model-4/performance_with_resource_metrics_20250417_143022.jsonl",
      "cluster_output_dir": "/app/output/output-model-4/pod_clustering_20250417_143023",
      "result": {...}
    }
    ```

    **状态值说明:**
    - `PENDING`: 任务等待中
    - `PROGRESS`: 任务进行中
    - `SUCCESS`: 任务成功
    - `FAILURE`: 任务失败

    **进度说明:**
    - 0-35%: PerformanceData 提取
    - 35-70%: 性能指标计算
    - 70-100%: 聚类分析
    """
    try:
        result = AsyncResult(task_id, app=celery_app)

        # 获取任务元数据
        task_info = result.info if isinstance(result.info, dict) else {}

        # 提取进度和消息
        progress = task_info.get("progress", 0)
        message = task_info.get("message", "任务进行中...")

        # PENDING 状态时进度为 0
        if result.state == "PENDING":
            progress = 0
            message = "任务等待中..."

        # SUCCESS 状态时进度为 100，包含完整结果
        elif result.state == "SUCCESS":
            progress = 100
            message = "算法运行成功！"
            # 从任务结果中提取详细信息
            if isinstance(result.result, dict):
                task_info.update(result.result)

        # FAILURE 状态
        elif result.state == "FAILURE":
            message = task_info.get("error", "任务失败") if isinstance(task_info, dict) else str(task_info)

        if result.successful():
            _result = result.result["result"]
        elif result.failed():
            _result = {"status":"FAILURE","reason":str(result.result)}
        else:
            _result = None



        return {
            "task_id": task_id,
            "status": result.state,
            "progress": progress,
            "message": message,
            "step": task_info.get("step"),
            "pod_count": task_info.get("pod_count") if result.state == "SUCCESS" else None,
            "performance_data_file": task_info.get("performance_data_file") if result.state == "SUCCESS" else None,
            "metrics_file": task_info.get("metrics_file") if result.state == "SUCCESS" else None,
            "cluster_output_dir": task_info.get("cluster_output_dir") if result.state == "SUCCESS" else None,
            "result": _result,
        }

    except Exception as e:
        logger.error(f"查询 Model4 任务状态异常: {e}", exc_info=True)
        raise handle_service_error(e, "查询任务状态")


@router.get("/model4-results")
async def get_last_cluster_result(task_id: str | None = None):
    """
    获取聚类分析结果（JSON格式）

    **功能说明:**
    如果指定 task_id，返回指定任务的聚类结果数据；否则返回最新的聚类结果数据
    结果包含 Pod 的名称、命名空间、聚类标签和 Pod 类型

    **参数说明:**
    - task_id: 可选，Celery 任务 ID

    **返回说明:**
    返回聚类结果数组，每个元素包含：
    - name: Pod 名称
    - namespace: 命名空间
    - cluster: 聚类标签（如 "Cluster 0", "Cluster 1"）
    - pod_type: Pod 类型（如 "CPU密集型", "内存密集型"）

    **使用示例:**
    ```bash
    # 获取最新结果
    curl "http://localhost:8000/v1/model4-results"

    # 获取指定任务结果
    curl "http://localhost:8000/v1/model4-results?task_id=xxx-xxx-xxx"

    # 返回示例
    [
      {
        "name": "nginx-deployment-abc123",
        "namespace": "default",
        "cluster": "Cluster 0",
        "pod_type": "CPU密集型"
      },
      {
        "name": "redis-master-xyz789",
        "namespace": "database",
        "cluster": "Cluster 1",
        "pod_type": "内存密集型"
      }
    ]
    ```

    **注意事项:**
    - 指定 task_id 时，会从 Celery 任务结果中获取
    - 未指定 task_id 时，会从缓存中获取最新结果
    - 如果任务未完成，返回 202 状态码
    - 如果任务不存在，返回 404 状态码
    """
    try:
        # 根据 task_id 参数选择不同的获取方式
        if task_id:
            # 指定 task_id，从 Celery 任务结果获取
            result = result_query_service.get_cluster_csv_by_task_id(task_id)

            if not result["success"]:
                error_msg = result.get("error", "获取任务结果失败")
                if "不存在" in error_msg or "未找到" in error_msg:
                    raise HTTPException(status_code=404, detail=error_msg)
                elif "未完成" in error_msg:
                    raise HTTPException(status_code=202, detail=error_msg)
                else:
                    raise HTTPException(status_code=500, detail=error_msg)

            csv_data = result["csv_data"]
        else:
            # 未指定 task_id，获取最新结果
            result = result_query_service.get_last_cluster_result()

            if not result["success"]:
                error_msg = result.get("error", "获取聚类结果失败")
                if "不存在" in error_msg or "未找到" in error_msg:
                    raise HTTPException(status_code=404, detail=error_msg)
                else:
                    raise HTTPException(status_code=500, detail=error_msg)

            # 只提取 csv_data 部分
            cluster_result = result["result"]
            csv_data = [
                {"name": row.pod, "cluster": row.cluster, "pod_type": row.pod_type, "namespace": row.namespace} for row in cluster_result.csv_data
            ]

        # 返回只包含 csv_data 的响应
        return csv_data
    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"获取聚类结果异常: {e}", exc_info=True)
        raise handle_service_error(e, "获取聚类结果")


@router.get("/model4-csv")
async def download_cluster_csv(task_id: str | None = None):
    """
    下载聚类分析结果 CSV 文件

    **功能说明:**
    如果指定 task_id，下载指定任务的 CSV 文件；否则下载最新的 CSV 文件

    **参数说明:**
    - task_id: 可选，Celery 任务 ID

    **返回说明:**
    - Content-Type: text/csv
    - Content-Disposition: attachment; filename="pod_clustering_results.csv"
    - 响应体: CSV 文件内容

    **使用示例:**
    ```bash
    # 下载最新结果
    curl "http://localhost:8000/v1/model4-csv" -o results.csv

    # 下载指定任务结果
    curl "http://localhost:8000/v1/model4-csv?task_id=xxx-xxx-xxx" -o results.csv

    # 使用 wget
    wget "http://localhost:8000/v1/model4-csv" -O clustering.csv
    ```

    **CSV 文件格式:**
    - 包含列：pod, namespace, cluster, pod_type, cpu_usage, memory_usage 等
    - 按聚类标签分组
    - 包含每个 Pod 的性能指标和聚类信息

    **注意事项:**
    - 指定 task_id 时，文件名包含 task_id：`pod_clustering_results_{task_id}.csv`
    - 未指定 task_id 时，文件名：`pod_clustering_results.csv`
    - 如果任务未完成，返回 202 状态码
    - 如果任务不存在，返回 404 状态码
    """
    try:
        # 根据 task_id 参数选择不同的获取方式
        if task_id:
            # 指定 task_id，从 Celery 任务结果获取文件路径
            result = result_query_service.get_cluster_csv_file_by_task_id(task_id)

            if not result["success"]:
                error_msg = result.get("error", "获取任务结果失败")
                if "不存在" in error_msg or "未找到" in error_msg:
                    raise HTTPException(status_code=404, detail=error_msg)
                elif "未完成" in error_msg:
                    raise HTTPException(status_code=202, detail=error_msg)
                else:
                    raise HTTPException(status_code=500, detail=error_msg)

            csv_file_path = result["file_path"]
            filename = f"pod_clustering_results_{task_id}.csv"
        else:
            # 未指定 task_id，获取最新结果
            result = result_query_service.get_last_cluster_output_file("csv")

            if not result["success"]:
                error_msg = result.get("error", "获取 CSV 文件失败")
                if "不存在" in error_msg or "未找到" in error_msg:
                    raise HTTPException(status_code=404, detail=error_msg)
                else:
                    raise HTTPException(status_code=500, detail=error_msg)

            csv_file_path = result["file_path"]
            filename = "pod_clustering_results.csv"

        # 返回文件下载
        return FileResponse(path=csv_file_path, filename=filename, media_type="text/csv")

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"下载 CSV 文件异常: {e}", exc_info=True)
        raise handle_service_error(e, "下载 CSV 文件")
