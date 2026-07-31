# -*- coding: utf-8 -*-
"""
Model5 算法路由模块

负责处理 Model5 算法执行、状态查询等 HTTP 请求
业务逻辑在 service 层实现
"""

import os

import aiofiles
from celery.result import AsyncResult
from fastapi import APIRouter, HTTPException, Query
from fastapi.responses import FileResponse, StreamingResponse

from app.api.v1.common import handle_service_error
from app.celery.config import celery_app
from app.celery.tasks import model5_task as model5_task
from app.schema.task_status import Model5TaskStatusResponse
from app.service.result_query import ResultQueryService5, result_query_service5
from app.settings import settings
from app.utils.file_utils import get_latest_dir
from app.utils.logger import get_logger


logger = get_logger(__name__)

router = APIRouter(tags=["模型五"])


# =====================================================
# Model5 算法端点
# =====================================================


@router.post("/run-model5-short", response_model=Model5TaskStatusResponse)
async def run_algorithm5_short(
    # date_str: str = Query(..., description="日期字符串（如 '1208'）"),
):
    """
    运行模型五 Pod 中期预测算法（90分钟预测）

    **功能说明:**
    执行 Model5 中期预测完整算法流程，包括：
    1. EmergencyShort 数据清洗（0-20%）：提取目标指标、统计 burst 秒、标记 burst 事件
    2. Pod 中期预测（20-100%）：LSTM 训练、多步预测、告警分析、扩容建议、回测评估

    **特性说明:**
    - 使用 LSTM 进行 Pod 负载预测
    - 时间桶：180秒（3分钟）一桶
    - 输入窗口：30 桶 = 90 分钟历史数据
    - 预测窗口：30 桶 = 90 分钟预测
    - 支持应急中期预测（Burst 检测）
    - 提供告警、扩容建议和回测评估

    **返回说明:**
    - task_id: 任务 ID，用于查询执行状态
    - status: 任务状态（PENDING/PROGRESS/SUCCESS/FAILURE）
    - progress: 执行进度（0-100）
    - message: 状态消息

    **使用示例:**
    ```bash
    # 运行算法（自动使用最新数据目录）
    curl -X POST "http://localhost:8000/v1/run-model5-short"

    # 返回示例
    {
      "task_id": "abc-123-def",
      "status": "PENDING",
      "progress": 0,
      "message": "中期预测任务创建成功！"
    }

    # 查询任务状态
    curl "http://localhost:8000/v1/model5-status/{task_id}"
    ```

    **注意事项:**
    - 自动使用 `DATA_DIR_MODEL5/` 目录下最新数据
    - 无需手动指定日期参数
    - 需要先通过 `/upload-file` 上传数据文件到模型五目录
    """
    try:
        date_str = get_latest_dir(settings.DATA_DIR_MODEL5)

        logger.info(f"创建 Model5 算法任务，日期: {date_str}")

        # 创建 Celery 任务
        task = model5_task.run_model5_short.delay(date_str)  # type: ignore

        if not task.id:
            raise HTTPException(status_code=500, detail="创建任务失败")

        logger.info(f"Model5 算法任务已提交: task_id={task.id}, date_str={date_str}")

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


@router.post("/run-model5-long", response_model=Model5TaskStatusResponse)
async def run_algorithm5_long(
    # date_str: str = Query(..., description="日期字符串（如 '1210'）"),
):
    """
    运行模型五 Pod 长期预测算法（24小时预测）

    **功能说明:**
    执行 Model5 长期预测完整算法流程，包括：
    1. EmergencyLong 数据清洗（0-20%）：处理单目录多文件，支持 .gz 自动解压，检测长期 Burst 事件
    2. Pod 长期预测（20-100%）：LSTM 训练、多步预测、告警分析、扩容建议、回测评估

    **特性说明:**
    - 使用 LSTM 进行 24 小时负载预测
    - 时间桶：180秒（3分钟）一桶
    - 输入窗口：120 桶 = 6 小时历史数据
    - 预测窗口：480 桶 = 24 小时预测
    - 使用 MC Dropout 进行不确定性估计（5 次采样，95% 置信区间）
    - 优化的训练参数：10 轮训练、8 个隐藏单元、1 层 LSTM
    - 单进程处理（移除并行处理以简化架构）
    - 包含告警分析和扩容建议（CPU、内存、IO、网络 4 维度）
    - 包含滑窗回测评估（1 个回测窗口）

    **数据要求:**
    - 支持单目录多文件输入（无需三天目录结构）
    - 自动检测并解压 .gz 压缩文件
    - 建议至少 30 小时以上的数据以保证预测质量
    - 数据应位于 `DATA_DIR_MODEL5/` 目录下

    **返回说明:**
    - task_id: 任务 ID，用于查询执行状态
    - status: 任务状态（PENDING/PROGRESS/SUCCESS/FAILURE）
    - progress: 执行进度（0-100）
    - message: 状态消息

    **使用示例:**
    ```bash
    # 运行算法（自动使用最新数据目录）
    curl -X POST "http://localhost:8000/v1/run-model5-long"

    # 返回示例
    {
      "task_id": "abc-123-def",
      "status": "PENDING",
      "progress": 0,
      "message": "长期预测任务创建成功！"
    }

    # 查询任务状态
    curl "http://localhost:8000/v1/model5-status/{task_id}"
    ```

    **注意事项:**
    - 自动使用 `DATA_DIR_MODEL5/` 目录下最新数据
    - 无需手动指定日期参数
    - 需要先通过 `/upload-file` 上传数据文件到模型五目录
    - 输出目录：`OUTPUT_DIR_MODEL_5/{date_str}/ForecastOutputWorkloadLSTM24h/`
    """
    try:
        date_str = get_latest_dir(settings.DATA_DIR_MODEL5)

        logger.info(f"创建 Model5 长期预测任务，最后一天: {date_str}")

        # 创建 Celery 任务
        task = model5_task.run_model5_long.delay(date_str)  # type: ignore

        if not task.id:
            raise HTTPException(status_code=500, detail="创建任务失败")

        logger.info(f"Model5 长期预测任务已提交: task_id={task.id}, date_str={date_str}")

        result = AsyncResult(task.id, app=celery_app)

        return {
            "task_id": task.id,
            "status": result.state,
            "progress": 0,
            "message": "长期预测任务创建成功！",
        }

    except Exception as e:
        logger.error(f"运行长期预测异常: {e}", exc_info=True)
        raise handle_service_error(e, "运行长期预测")


@router.get("/model5-status/{task_id}", response_model=Model5TaskStatusResponse)
async def get_model5_status(task_id: str):
    """
    查询模型五预测任务状态（中期/长期预测通用）

    **功能说明:**
    根据任务 ID 查询 Model5 算法的执行状态和进度

    **参数说明:**
    - task_id: 任务 ID（由 /run-model5-short 或 /run-model5-long 接口返回）

    **返回说明:**
    - task_id: 任务 ID
    - status: 任务状态（PENDING/PROGRESS/SUCCESS/FAILURE）
    - progress: 执行进度（0-100）
    - message: 状态消息
    - result: 任务完整结果（成功时），失败时为 None

    **使用示例:**
    ```bash
    # 查询任务状态
    curl "http://localhost:8000/v1/model5-status/abc-123-def"

    # 返回示例（进行中）
    {
      "task_id": "abc-123-def",
      "status": "PROGRESS",
      "progress": 50,
      "message": "正在进行 LSTM 训练...",
      "result": null
    }

    # 返回示例（完成）
    {
      "task_id": "abc-123-def",
      "status": "SUCCESS",
      "progress": 100,
      "message": "算法运行成功！",
      "result": {
        "burst_data_jsonl": "/app/temp/1208/BurstData/performance_with_resource_metrics_with_burst.jsonl",
        "forecast_csv": "/app/output/output-model-5/1208/ForecastOutputWorkloadLSTM/workload_forecast_90m_lstm.csv",
        "alert_csv": "/app/output/output-model-5/1208/ForecastOutputWorkloadLSTM/workload_alerts_and_scale_pods_90m_lstm.csv",
        "workload_count": 25
      }
    }
    ```

    **状态值说明:**
    - `PENDING`: 任务等待中
    - `PROGRESS`: 任务进行中
    - `SUCCESS`: 任务成功
    - `FAILURE`: 任务失败

    **进度说明（中期预测）：**
    - 0-20%: EmergencyShort 数据清洗
    - 20-100%: Pod 中期预测（LSTM训练、预测、回测）

    **进度说明（长期预测）：**
    - 0-20%: EmergencyLong 数据清洗（处理单目录多文件）
    - 20-100%: Pod 长期预测（LSTM训练、预测、回测）
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
            _result = {"status": "FAILURE", "reason": str(result.result)}
        else:
            _result = None

        return {
            "task_id": task_id,
            "status": result.state,
            "progress": progress,
            "message": message,
            "result": _result,
        }

    except Exception as e:
        logger.error(f"查询 Model5 任务状态异常: {e}", exc_info=True)
        raise handle_service_error(e, "查询任务状态")


# =====================================================
# Model5 结果下载端点
# =====================================================


@router.get("/model5-short-results")
async def get_model5_short_results(task_id: str | None = None):
    """
    获取中期预测（90分钟）告警和扩容建议结果

    **功能说明:**
    如果指定 task_id，返回指定任务的告警数据；否则返回最新的告警数据
    结果仅包含6个核心字段，减少数据传输量

    **参数说明:**
    - task_id: 可选，Celery 任务 ID

    **返回说明:**
    返回告警和扩容建议数组，每个元素包含：
    - namespace: 命名空间
    - name: Workload 名称
    - current_replicas: 当前副本数
    - recommend_replicas: 推荐副本数
    - total_replicas: 总副本数
    - predicted_at: 预测时间

    **使用示例:**
    ```bash
    # 获取最新结果
    curl "http://localhost:8000/v1/model5-short-results"

    # 获取指定任务结果
    curl "http://localhost:8000/v1/model5-short-results?task_id=xxx-xxx-xxx"

    # 返回示例
    [
      {
        "namespace": "default",
        "name": "nginx-deployment",
        "current_replicas": 3,
        "recommend_replicas": 5,
        "total_replicas": 8,
        "predicted_at": "2025-04-17 15:30:00"
      },
      {
        "namespace": "database",
        "name": "redis-master",
        "current_replicas": 1,
        "recommend_replicas": 1,
        "total_replicas": 2,
        "predicted_at": "2025-04-17 15:30:00"
      }
    ]
    ```

    **注意事项:**
    - 指定 task_id 时，会从 Celery 任务结果中获取
    - 未指定 task_id 时，会获取最新目录的结果
    - 如果任务未完成，返回 202 状态码
    - 如果任务不存在，返回 404 状态码
    - 结果文件：`workload_alerts_and_scale_pods_90m_lstm.csv`
    """
    try:
        if task_id:
            # 指定 task_id，从 Celery 任务结果获取文件路径
            result = result_query_service5.get_alert_csv_file_by_task_id(task_id, is_long=False)

            if not result["success"]:
                error_msg = result.get("error", "获取任务结果失败")
                if "不存在" in error_msg or "未找到" in error_msg:
                    raise HTTPException(status_code=404, detail=error_msg)
                elif "未完成" in error_msg:
                    raise HTTPException(status_code=202, detail=error_msg)
                else:
                    raise HTTPException(status_code=500, detail=error_msg)

            csv_file_path = result["file_path"]
        else:
            # 未指定 task_id，获取最新结果
            dir = get_latest_dir(settings.OUTPUT_DIR_MODEL_5)
            csv_file_path = os.path.join(
                settings.OUTPUT_DIR_MODEL_5, dir, "ForecastOutputWorkloadLSTM", "workload_alerts_and_scale_pods_90m_lstm.csv"
            )

            if not os.path.exists(csv_file_path):
                raise HTTPException(status_code=404, detail=f"文件不存在: {csv_file_path}")

        # 读取 CSV 文件并返回数据
        import csv

        csv_data = []
        with open(csv_file_path, "r", encoding="utf-8") as f:  # noqa: ASYNC230
            reader = csv.DictReader(f)
            for row in reader:
                csv_data.append(row)

        csv_data = [
            {
                "namespace":row["namespace"] if row.get("namespace") else row["\ufeffnamespace"]  ,
                "name": row['name'],
                "current_replicas": row['current_replicas'],
                "recommend_replicas": row['recommend_replicas'],
                "total_replicas": row['total_replicas'],
                "predicted_at": row['predicted_at'],
            }
            for row in csv_data
        ]
        return csv_data

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"获取短期预测告警结果异常: {e}", exc_info=True)
        raise handle_service_error(e, "获取告警结果")


@router.get("/model5-long-results")
async def get_model5_long_results(task_id: str | None = None):
    """
    获取长期预测（24小时）告警和扩容建议结果

    **功能说明:**
    如果指定 task_id，返回指定任务的告警数据；否则返回最新的告警数据
    结果仅包含6个核心字段，减少数据传输量

    **参数说明:**
    - task_id: 可选，Celery 任务 ID

    **返回说明:**
    返回告警和扩容建议数组，每个元素包含：
    - namespace: 命名空间
    - name: Workload 名称
    - current_replicas: 当前副本数
    - recommend_replicas: 推荐副本数
    - total_replicas: 总副本数
    - predicted_at: 预测时间

    **使用示例:**
    ```bash
    # 获取最新结果
    curl "http://localhost:8000/v1/model5-long-results"

    # 获取指定任务结果
    curl "http://localhost:8000/v1/model5-long-results?task_id=xxx-xxx-xxx"

    # 返回示例
    [
      {
        "namespace": "default",
        "name": "nginx-deployment",
        "current_replicas": 3,
        "recommend_replicas": 6,
        "total_replicas": 9,
        "predicted_at": "2025-04-18 15:30:00"
      }
    ]
    ```

    **注意事项:**
    - 指定 task_id 时，会从 Celery 任务结果中获取
    - 未指定 task_id 时，会获取最新目录的结果
    - 如果任务未完成，返回 202 状态码
    - 如果任务不存在，返回 404 状态码
    - 结果文件：`workload_alerts_and_scale_pods_24h_lstm.csv`
    - 输出目录：`OUTPUT_DIR_MODEL_5/{date_str}/ForecastOutputWorkloadLSTM24h/`
    """
    try:
        if task_id:
            # 指定 task_id，从 Celery 任务结果获取文件路径
            result = result_query_service5.get_alert_csv_file_by_task_id(task_id, is_long=True)

            if not result["success"]:
                error_msg = result.get("error", "获取任务结果失败")
                if "不存在" in error_msg or "未找到" in error_msg:
                    raise HTTPException(status_code=404, detail=error_msg)
                elif "未完成" in error_msg:
                    raise HTTPException(status_code=202, detail=error_msg)
                else:
                    raise HTTPException(status_code=500, detail=error_msg)

            csv_file_path = result["file_path"]
        else:
            # 未指定 task_id，获取最新结果
            dir = get_latest_dir(settings.OUTPUT_DIR_MODEL_5)
            csv_file_path = os.path.join(
                settings.OUTPUT_DIR_MODEL_5, dir, "ForecastOutputWorkloadLSTM24h", "workload_alerts_and_scale_pods_24h_lstm.csv"
            )

            if not os.path.exists(csv_file_path):
                raise HTTPException(status_code=404, detail=f"文件不存在: {csv_file_path}")

        # 读取 CSV 文件并返回数据
        import csv

        csv_data = []
        with open(csv_file_path, "r", encoding="utf-8") as f:  # noqa: ASYNC230
            reader = csv.DictReader(f)
            for row in reader:
                csv_data.append(row)
        csv_data = [
            {
                "namespace":row["namespace"] if row.get("namespace") else row["\ufeffnamespace"]  ,
                "name": row['name'],
                "current_replicas": row['current_replicas'],
                "recommend_replicas": row['recommend_replicas'],
                "total_replicas": row['total_replicas'],
                "predicted_at": row['predicted_at'],
            }
            for row in csv_data
        ]
        return csv_data

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"获取长期预测告警结果异常: {e}", exc_info=True)
        raise handle_service_error(e, "获取告警结果")


@router.get("/model5-short-csv")
async def get_model5_short_csv(task_id: str | None = None):
    """
    下载中期预测（90分钟）告警和扩容建议 CSV 文件

    **功能说明:**
    如果指定 task_id，下载指定任务的 CSV 文件；否则下载最新的 CSV 文件

    **参数说明:**
    - task_id: 可选，Celery 任务 ID

    **返回说明:**
    - Content-Type: text/csv
    - Content-Disposition: attachment; filename="workload_alerts_and_scale_pods_90m_lstm.csv"
    - 响应体: CSV 文件内容

    **使用示例:**
    ```bash
    # 下载最新结果
    curl "http://localhost:8000/v1/model5-short-csv" -o results.csv

    # 下载指定任务结果
    curl "http://localhost:8000/v1/model5-short-csv?task_id=xxx-xxx-xxx" -o results.csv

    # 使用 wget
    wget "http://localhost:8000/v1/model5-short-csv" -O alerts.csv
    ```

    **CSV 文件格式:**
    - 包含预测的告警信息和扩容建议
    - 包含字段：namespace, name, current_replicas, recommend_replicas, total_replicas, predicted_at 等
    - 按命名空间和 workload 名称排序

    **注意事项:**
    - 指定 task_id 时，文件名包含 task_id：`workload_alerts_and_scale_pods_90m_lstm_{task_id}.csv`
    - 未指定 task_id 时，文件名：`workload_alerts_and_scale_pods_90m_lstm.csv`
    - 如果任务未完成，返回 202 状态码
    - 如果任务不存在，返回 404 状态码
    """
    try:
        if task_id:
            # 指定 task_id，从 Celery 任务结果获取文件路径
            result = result_query_service5.get_alert_csv_file_by_task_id(task_id, is_long=False)

            if not result["success"]:
                error_msg = result.get("error", "获取任务结果失败")
                if "不存在" in error_msg or "未找到" in error_msg:
                    raise HTTPException(status_code=404, detail=error_msg)
                elif "未完成" in error_msg:
                    raise HTTPException(status_code=202, detail=error_msg)
                else:
                    raise HTTPException(status_code=500, detail=error_msg)

            csv_file_path = result["file_path"]
            filename = f"workload_alerts_and_scale_pods_90m_lstm_{task_id}.csv"
        else:
            # 返回最近的dir中的workload_alerts_and_scale_pods_90m_lstm.csv文件
            dir = get_latest_dir(settings.OUTPUT_DIR_MODEL_5)
            csv_file_path = os.path.join(
                settings.OUTPUT_DIR_MODEL_5, dir, "ForecastOutputWorkloadLSTM", "workload_alerts_and_scale_pods_90m_lstm.csv"
            )
            filename = "workload_alerts_and_scale_pods_90m_lstm.csv"

        if not os.path.exists(csv_file_path):
            raise HTTPException(status_code=404, detail=f"文件不存在: {csv_file_path}")

        # 返回文件下载
        return FileResponse(path=csv_file_path, filename=filename, media_type="text/csv")

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"下载中期预测 CSV 文件异常: {e}", exc_info=True)
        raise handle_service_error(e, "下载 CSV 文件")


@router.get("/model5-long-csv")
async def get_model5_long_csv(task_id: str | None = None):
    """
    下载长期预测（24小时）告警和扩容建议 CSV 文件

    **功能说明:**
    如果指定 task_id，下载指定任务的 CSV 文件；否则下载最新的 CSV 文件

    **参数说明:**
    - task_id: 可选，Celery 任务 ID

    **返回说明:**
    - Content-Type: text/csv
    - Content-Disposition: attachment; filename="workload_alerts_and_scale_pods_24h_lstm.csv"
    - 响应体: CSV 文件内容

    **使用示例:**
    ```bash
    # 下载最新结果
    curl "http://localhost:8000/v1/model5-long-csv" -o results.csv

    # 下载指定任务结果
    curl "http://localhost:8000/v1/model5-long-csv?task_id=xxx-xxx-xxx" -o results.csv

    # 使用 wget
    wget "http://localhost:8000/v1/model5-long-csv" -O alerts_24h.csv
    ```

    **CSV 文件格式:**
    - 包含预测的告警信息和扩容建议（24小时预测）
    - 包含字段：namespace, name, current_replicas, recommend_replicas, total_replicas, predicted_at 等
    - 按命名空间和 workload 名称排序

    **注意事项:**
    - 指定 task_id 时，文件名包含 task_id：`workload_alerts_and_scale_pods_24h_lstm_{task_id}.csv`
    - 未指定 task_id 时，文件名：`workload_alerts_and_scale_pods_24h_lstm.csv`
    - 如果任务未完成，返回 202 状态码
    - 如果任务不存在，返回 404 状态码
    - 输出目录：`OUTPUT_DIR_MODEL_5/{date_str}/ForecastOutputWorkloadLSTM24h/`
    """
    try:
        if task_id:
            # 指定 task_id，从 Celery 任务结果获取文件路径
            result = result_query_service5.get_alert_csv_file_by_task_id(task_id, is_long=True)

            if not result["success"]:
                error_msg = result.get("error", "获取任务结果失败")
                if "不存在" in error_msg or "未找到" in error_msg:
                    raise HTTPException(status_code=404, detail=error_msg)
                elif "未完成" in error_msg:
                    raise HTTPException(status_code=202, detail=error_msg)
                else:
                    raise HTTPException(status_code=500, detail=error_msg)

            csv_file_path = result["file_path"]
            filename = f"workload_alerts_and_scale_pods_24h_lstm_{task_id}.csv"
        else:
            # 返回最近的dir中的workload_alerts_and_scale_pods_24h_lstm.csv文件
            dir = get_latest_dir(settings.OUTPUT_DIR_MODEL_5)
            csv_file_path = os.path.join(
                settings.OUTPUT_DIR_MODEL_5, dir, "ForecastOutputWorkloadLSTM24h", "workload_alerts_and_scale_pods_24h_lstm.csv"
            )
            filename = "workload_alerts_and_scale_pods_24h_lstm.csv"

        if not os.path.exists(csv_file_path):
            raise HTTPException(status_code=404, detail=f"文件不存在: {csv_file_path}")

        # 返回文件下载
        return FileResponse(path=csv_file_path, filename=filename, media_type="text/csv")

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"下载长期预测 CSV 文件异常: {e}", exc_info=True)
        raise handle_service_error(e, "下载 CSV 文件")
