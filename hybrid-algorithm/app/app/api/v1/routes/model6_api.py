# -*- coding: utf-8 -*-
"""
Model6 算法路由模块

负责处理 Model6 算法执行、状态查询等 HTTP 请求
业务逻辑在 service 层实现
"""

import os
from typing import cast

import aiofiles
from celery.result import AsyncResult
from fastapi import APIRouter, File, HTTPException, UploadFile
from fastapi.responses import FileResponse

from app.api.v1.common import handle_service_error
from app.celery.config import celery_app
from app.celery.tasks import model6_task as model6_task
from app.schema.task_status import Model6TaskStatusResponse
from app.service.result_query import result_query_service6
from app.settings import settings
from app.utils.file_utils import (
    get_latest_dir,
)
from app.utils.logger import get_logger
from app.utils.tools import ensure_output_dir


logger = get_logger(__name__)

router = APIRouter(tags=["模型六"])


# =====================================================
# Model6 文件上传和算法执行端点
# =====================================================


@router.post("/run-model6", response_model=Model6TaskStatusResponse)
async def upload_model6_file(
    # file: UploadFile = File(..., description="要上传的 .gz 压缩文件"),
):
    """
    运行模型六 Pod 维度干扰分析算法

    **功能说明:**
    执行 Model6 Pod 维度干扰分析完整算法流程，包括：
    1. 节点背景压力分析（0-20%）：汇总节点 CPU/MEM/IO/NET，CPU 利用率基于固定 64 核
    2. Pod CPI 异常检测（20-40%）：基于 koordlet CPI 数据检测 CPI 异常
    3. Pod PSI 异常检测（40-60%）：基于 koordlet PSI 数据检测 PSI 异常（CPU/内存/I/O）
    4. 当前干扰分析（60-80%）：融合节点背景和Pod自身压力，进行双路径判定
    5. 未来干扰预测（80-90%）：使用线性趋势预测（最小二乘法）预测未来 30 分钟干扰
    6. 回测评估（90-100%）：评估预测准确率（TP/TN/FP/FN、准确率、精确率、召回率、F1）

    **特性说明:**
    - **Pod 维度分析**：融合节点背景压力（CPU/MEM/IO/NET）与Pod自身性能压力
    - 基于 CPI（每周期指令数）和 PSI（压力 stall 指标）进行干扰分析
    - 双路径干扰判定：
      - 路径B：CPI 异常（CPI 峰值 >= 1.0，分为 LIGHT/MEDIUM/SEVERE）
      - 路径C：PSI 异常（CPU/内存/I/O 的 some 或 full 超过告警阈值）
    - 节点背景压力阈值：CPU 70%/90%，MEM 32GB/64GB，IO 200/500 MB/s，NET 200/500 MB/s
    - 使用线性趋势预测（最小二乘法）预测未来 30 分钟的干扰
    - 置信度评估：R² >= 0.05 时趋势可信，否则不输出预测
    - 包含回测功能评估预测准确率
    - 提供优化建议和根因分析

    **返回说明:**
    - task_id: 任务 ID，用于查询执行状态
    - status: 任务状态（PENDING/PROGRESS/SUCCESS/FAILURE）
    - progress: 执行进度（0-100）
    - message: 状态消息

    **使用示例:**
    ```bash
    # 运行算法（自动使用最新数据目录）
    curl -X POST "http://localhost:8000/v1/run-model6"

    # 返回示例
    {
      "task_id": "abc-123-def",
      "status": "PENDING",
      "progress": 0,
      "message": "干扰分析任务创建成功！",
      "input_file": "/app/data/data-model-6/0417/hybrid_export.gz"
    }

    # 查询任务状态
    curl "http://localhost:8000/v1/model6-status/{task_id}"
    ```

    **注意事项:**
    - 自动使用 `DATA_DIR_MODEL6/` 目录下最新数据
    - 无需手动上传文件，直接使用最新目录的数据
    - 输入格式：混合导出 .gz 文件（koordlet_container_cpi + koordlet_container_psi）
    - 输出文件：`pod_interference_analysis.csv`, `pod_interference_forecast.csv` 等
    """
    try:
        date_str = get_latest_dir(settings.DATA_DIR_MODEL6)

        # 读取文件内容
        # file_content = await file.read()
        # file_size = len(file_content)

        # 确保 file.filename 不为 None
        # filename = file.filename if file.filename else "unknown.gz"

        # logger.info(f"开始处理模型6上传文件: {filename}, 大小: {file_size} 字节")

        # 1. 验证文件扩展名（必须为 .gz）
        # validate_upload_file(filename, [".gz"])

        # 2. 生成安全的文件名
        # safe_filename = secure_filename(filename)

        # 3. 保存上传文件到临时目录
        # ensure_output_dir(settings.UPLOAD_DIR)
        upload_file_path = os.path.join(settings.DATA_DIR_MODEL6, date_str)

        # 使用 aiofiles 异步写入文件
        # async with aiofiles.open(upload_file_path, "wb") as f:
        #     await f.write(file_content)

        # logger.info(f"文件已保存: {upload_file_path}")

        # 4. 创建 Celery 任务（直接执行干扰分析）
        dirpath, dirnames, filenames = next(os.walk(upload_file_path))

        task = model6_task.run_model6.delay(date_str, os.path.join(upload_file_path, filenames[0]))  # type: ignore

        if not task.id:
            raise HTTPException(status_code=500, detail="创建任务失败")

        logger.info(f"模型6干扰分析任务已提交: task_id={task.id}")

        result = AsyncResult(task.id, app=celery_app)

        return {
            "task_id": task.id,
            "status": result.state,
            "progress": 0,
            "message": "干扰分析任务创建成功！",
            "input_file": upload_file_path,
        }

    except Exception as e:
        logger.error(f"上传模型6文件异常: {e}", exc_info=True)
        raise handle_service_error(e, "上传模型6文件")


@router.get("/model6-status/{task_id}", response_model=Model6TaskStatusResponse)
async def get_model6_status(task_id: str):
    """
    查询模型六 Pod 干扰分析任务状态

    **功能说明:**
    根据任务 ID 查询模型六 Pod 干扰分析的执行状态和进度

    **参数说明:**
    - task_id: 任务 ID（由 /run-model6 接口返回）

    **返回说明:**
    - task_id: 任务 ID
    - status: 任务状态（PENDING/PROGRESS/SUCCESS/FAILURE）
    - progress: 执行进度（0-100）
    - message: 状态消息
    - step: 当前执行步骤（可选）
    - input_file: 输入文件路径
    - analysis_csv: Pod 干扰分析 CSV 文件路径（完成后）
    - forecast_csv: 未来干扰预测 CSV 文件路径（完成后）
    - backtest_detail_csv: 回测明细 CSV 文件路径（完成后）
    - backtest_summary_csv: 回测汇总 CSV 文件路径（完成后）
    - workload_count: Workload 数量（完成后）
    - interference_count: 检测到干扰的 workload 数量（完成后）
    - forecast_signal_count: 预测信号数量（完成后）
    - result: 任务完整结果（成功时），失败时为 None

    **使用示例:**
    ```bash
    # 查询任务状态
    curl "http://localhost:8000/v1/model6-status/abc-123-def"

    # 返回示例（进行中）
    {
      "task_id": "abc-123-def",
      "status": "PROGRESS",
      "progress": 50,
      "message": "正在进行 PSI 异常检测...",
      "step": "Pod PSI 异常检测"
    }

    # 返回示例（完成）
    {
      "task_id": "abc-123-def",
      "status": "SUCCESS",
      "progress": 100,
      "message": "干扰分析运行成功！",
      "analysis_csv": "/app/output/output-model-6/0417/InterferenceOutputWorkloadLSTM/pod_interference_analysis.csv",
      "forecast_csv": "/app/output/output-model-6/0417/InterferenceOutputWorkloadLSTM/pod_interference_forecast.csv",
      "workload_count": 45,
      "interference_count": 8
    }
    ```

    **状态值说明:**
    - `PENDING`: 任务等待中
    - `PROGRESS`: 任务进行中
    - `SUCCESS`: 任务成功
    - `FAILURE`: 任务失败

    **进度说明:**
    - 0-20%: 节点背景压力分析
    - 20-40%: Pod CPI 异常检测
    - 40-60%: Pod PSI 异常检测
    - 60-80%: 当前干扰分析
    - 80-90%: 未来干扰预测
    - 90-100%: 回测评估
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
            message = "干扰分析运行成功！"
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
            "input_file": task_info.get("input_file"),
            "analysis_csv": task_info.get("analysis_csv") if result.state == "SUCCESS" else None,
            "forecast_csv": task_info.get("forecast_csv") if result.state == "SUCCESS" else None,
            "backtest_detail_csv": task_info.get("backtest_detail_csv") if result.state == "SUCCESS" else None,
            "backtest_summary_csv": task_info.get("backtest_summary_csv") if result.state == "SUCCESS" else None,
            "workload_count": task_info.get("workload_count") if result.state == "SUCCESS" else None,
            "interference_count": task_info.get("interference_count") if result.state == "SUCCESS" else None,
            "forecast_signal_count": task_info.get("forecast_signal_count") if result.state == "SUCCESS" else None,
            "result": _result,
        }

    except Exception as e:
        logger.error(f"查询模型6任务状态异常: {e}", exc_info=True)
        raise handle_service_error(e, "查询任务状态")


@router.get("/model6-results")
async def get_model6_results(task_id: str | None = None):
    """
    获取 Pod 干扰分析结果（JSON格式）

    **功能说明:**
    如果指定 task_id，返回指定任务的干扰分析数据；否则返回最新的干扰分析数据
    结果仅包含6个核心字段，减少数据传输量

    **参数说明:**
    - task_id: 可选，Celery 任务 ID

    **返回说明:**
    返回 Pod 干扰分析结果数组，每个元素包含：
    - name: Pod 名称
    - namespace: 命名空间
    - interference_level: 干扰等级（NONE/LIGHT/MEDIUM/SEVERE）
    - interference_signal: 干扰信号（NO_INTERFERENCE/CPI_HIGH/PSI_HIGH/BOTH_HIGH）
    - interference_reason: 干扰原因（CPU/MEM/IO 指标异常）
    - recommend_action: 推荐操作（优化建议）

    **使用示例:**
    ```bash
    # 获取最新结果
    curl "http://localhost:8000/v1/model6-results"

    # 获取指定任务结果
    curl "http://localhost:8000/v1/model6-results?task_id=xxx-xxx-xxx"

    # 返回示例
    [
      {
        "name": "nginx-deployment-abc123",
        "namespace": "default",
        "interference_level": "MEDIUM",
        "interference_signal": "CPI_HIGH",
        "interference_reason": "CPI 异常（峰值 2.5），可能存在 CPU 争抢",
        "recommend_action": "考虑增加 CPU 资源限制或调整调度策略"
      },
      {
        "name": "redis-master-xyz789",
        "namespace": "database",
        "interference_level": "NONE",
        "interference_signal": "NO_INTERFERENCE",
        "interference_reason": "无干扰",
        "recommend_action": "保持当前配置"
      }
    ]
    ```

    **注意事项:**
    - 指定 task_id 时，会从 Celery 任务结果中获取
    - 未指定 task_id 时，会获取最新目录的结果
    - 如果任务未完成，返回 202 状态码
    - 如果任务不存在，返回 404 状态码
    - 结果文件：`pod_interference_analysis.csv`
    """
    try:
        if task_id:
            # 指定 task_id，从 Celery 任务结果获取文件路径
            result = result_query_service6.get_interference_csv_file_by_task_id(task_id)

            if not result["success"]:
                error_msg = result.get("error", "获取任务结果失败")
                if "不存在" in error_msg or "未找到" in error_msg:
                    raise HTTPException(status_code=404, detail=error_msg)
                elif "未完成" in error_msg:
                    raise HTTPException(status_code=202, detail=error_msg)
                else:
                    raise HTTPException(status_code=500, detail=error_msg)

            # 优先返回回测明细数据（最完整的结果）
            csv_file_path = cast(str,result.get("analysis_csv"))
        else:
            # 未指定 task_id，获取最新结果
            dir = get_latest_dir(settings.OUTPUT_DIR_MODEL_6)
            csv_file_path = os.path.join(
                settings.OUTPUT_DIR_MODEL_6, dir, "pod_interference_analysis.csv"
            )

            if not os.path.exists(csv_file_path):
                # 如果回测明细不存在，尝试返回干扰分析结果
                csv_file_path = os.path.join(settings.OUTPUT_DIR_MODEL_6, dir, "pod_interference_analysis.csv")

            if not os.path.exists(csv_file_path):
                raise HTTPException(status_code=404, detail=f"文件不存在: {csv_file_path}")

        # 读取 CSV 文件并返回数据
        import csv

        csv_data = []
        with open(csv_file_path, "r", encoding="utf-8") as f:
            reader = csv.DictReader(f)
            for row in reader:
                csv_data.append(row)

        csv_data = [
            {
                "name":row["name"] if row.get("name") else row["\ufeffname"]  ,
                "namespace": row['namespace'],
                "interference_level": row['interference_level'],
                "interference_signal": row['interference_signal'],
                "interference_reason": row['interference_reason'],
                "recommend_action": row['recommend_action'],
            }
            for row in csv_data
        ]

        return csv_data

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"获取干扰分析结果异常: {e}", exc_info=True)
        raise handle_service_error(e, "获取干扰分析结果")


@router.get("/model6-csv")
async def get_model6_csv(task_id: str | None = None):
    """
    下载 Pod 干扰分析 CSV 文件

    **功能说明:**
    如果指定 task_id，下载指定任务的 CSV 文件；否则下载最新的 CSV 文件

    **参数说明:**
    - task_id: 可选，Celery 任务 ID

    **返回说明:**
    - Content-Type: text/csv
    - Content-Disposition: attachment; filename="pod_interference_analysis.csv"
    - 响应体: CSV 文件内容

    **使用示例:**
    ```bash
    # 下载最新结果
    curl "http://localhost:8000/v1/model6-csv" -o results.csv

    # 下载指定任务结果
    curl "http://localhost:8000/v1/model6-csv?task_id=xxx-xxx-xxx" -o results.csv

    # 使用 wget
    wget "http://localhost:8000/v1/model6-csv" -O interference.csv
    ```

    **CSV 文件格式:**
    - 包含 Pod 维度的干扰状态分析
    - 包含字段：name, namespace, interference_level, interference_signal, interference_reason, recommend_action 等
    - 融合节点背景压力和 Pod 自身性能压力

    **注意事项:**
    - 指定 task_id 时，文件名保持原始名称
    - 未指定 task_id 时，文件名：`pod_interference_analysis.csv`
    - 如果任务未完成，返回 202 状态码
    - 如果任务不存在，返回 404 状态码
    - 输出目录：`OUTPUT_DIR_MODEL_6/{date_str}/InterferenceOutputWorkloadLSTM/`
    """
    try:
        if task_id:
            # 指定 task_id，从 Celery 任务结果获取文件路径
            result = result_query_service6.get_interference_csv_file_by_task_id(task_id)

            if not result["success"]:
                error_msg = result.get("error", "获取任务结果失败")
                if "不存在" in error_msg or "未找到" in error_msg:
                    raise HTTPException(status_code=404, detail=error_msg)
                elif "未完成" in error_msg:
                    raise HTTPException(status_code=202, detail=error_msg)
                else:
                    raise HTTPException(status_code=500, detail=error_msg)

            # 优先返回回测明细文件（最完整的结果）
            csv_file_path = cast(str,result.get("analysis_csv"))
            filename = os.path.basename(csv_file_path) if csv_file_path else "pod_interference_analysis.csv"
        else:
            # 返回最近的dir中的回测明细CSV文件
            dir = get_latest_dir(settings.OUTPUT_DIR_MODEL_6)
            csv_file_path = os.path.join(
                settings.OUTPUT_DIR_MODEL_6, dir, "pod_interference_analysis.csv"
            )

            # 如果回测明细不存在，尝试返回干扰分析结果
            if not os.path.exists(csv_file_path):
                csv_file_path = os.path.join(settings.OUTPUT_DIR_MODEL_6, dir, "node_interference_analysis.csv")
                filename = "pod_interference_analysis.csv"
            else:
                filename = "pod_interference_analysis.csv"

            if not os.path.exists(csv_file_path):
                raise HTTPException(status_code=404, detail=f"文件不存在: {csv_file_path}")

        # 返回文件下载
        return FileResponse(path=csv_file_path, filename=filename, media_type="text/csv")

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"下载干扰分析CSV文件异常: {e}", exc_info=True)
        raise handle_service_error(e, "下载CSV文件")
