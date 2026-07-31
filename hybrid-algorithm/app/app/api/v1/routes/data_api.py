# -*- coding: utf-8 -*-
"""
文件管理路由模块

负责处理文件上传、清理等 HTTP 请求
业务逻辑在 service 层实现
"""

import os

import aiofiles
from celery.result import AsyncResult
from fastapi import APIRouter, File, HTTPException, Query, UploadFile
from fastapi.responses import StreamingResponse

from app.api.v1.common import handle_service_error
from app.celery.config import celery_app
from app.celery.tasks import files_task as file_task
from app.schema.common import ResponseStatus
from app.schema.file_management import CleanDirectoriesResponse, DataDirEnum
from app.schema.task_status import UploadTaskStatusResponse
from app.service.file_management import file_management_service
from app.settings import settings
from app.utils.file_security import validate_safe_file_path
from app.utils.file_utils import parse_filename, secure_filename, validate_upload_file
from app.utils.logger import get_logger
from app.utils.tools import ensure_output_dir


logger = get_logger(__name__)

router = APIRouter(tags=["数据管理"])


# =====================================================
# 文件上传端点
# =====================================================


@router.post("/upload-file", response_model=UploadTaskStatusResponse)
async def upload_file(
    extract_to: DataDirEnum,
    file: UploadFile = File(..., description="要上传的压缩文件（.7z,.tar,.gz）"),
    cleanup_archive: bool = True,
):
    """
    上传并解压压缩文件

    **功能说明:**
    1. 接收用户上传的压缩文件（.7z/.tar/.gz）
    2. 验证文件扩展名和安全性
    3. 保存文件到临时目录
    4. 创建 Celery 任务异步解压文件
    5. 返回任务 ID 用于查询解压进度

    **参数说明:**
    - extract_to: 目标模型（MODEL4/MODEL5/MODEL6），指定解压目录
    - file: 要上传的压缩文件（支持 .7z, .tar, .gz 格式）
    - cleanup_archive: 是否在解压后删除压缩文件（默认 True）

    **返回说明:**
    - task_id: 任务 ID，用于查询解压进度
    - status: 任务状态（PENDING/PROGRESS/SUCCESS/FAILURE）
    - progress: 解压进度（0-100）
    - message: 状态消息
    - uploaded_bytes: 上传文件大小（字节）

    **使用示例:**
    ```bash
    # 上传文件到模型四目录
    curl -X POST "http://localhost:8000/v1/upload-file?extract_to=MODEL4" \
         -F "file=@data.7z"

    # 上传文件到模型五目录（不解压删除原文件）
    curl -X POST "http://localhost:8000/v1/upload-file?extract_to=MODEL5&cleanup_archive=false" \
         -F "file=@data.tar"

    # 查询解压进度
    curl "http://localhost:8000/v1/upload-status/{task_id}"
    ```

    **文件名解析:**
    - 文件名按 `_` 分割，第三部分作为日期/标识符
    - 例如：`prometheus_data_1208.7z` → 解压到 `DATA_DIR_MODELX/1208/`

    **支持的文件格式:**
    - `.7z` - 7z 压缩文件（模型四、模型五）
    - `.tar` - tar 压缩文件（模型四、模型五、模型六）
    - `.gz` - gzip 压缩文件（模型六，混合导出的 CPI/PSI 数据）
    """
    try:
        # 读取文件内容
        file_content = await file.read()
        file_size = len(file_content)

        # 确保 file.filename 不为 None
        filename = file.filename if file.filename else "unknown.7z"

        # 保存文件
        logger.info(f"开始处理上传文件: {filename}, 大小: {file_size} 字节")

        # 1. 验证文件扩展名
        validate_upload_file(filename, settings.FILE_UPLOAD_ALLOWED_EXTENSIONS)  # type: ignore

        # 2. 生成安全的文件名
        safe_filename = secure_filename(filename)

        # 2.1 解析文件名
        parsed_filename = parse_filename(safe_filename)

        # 3. 保存上传文件到临时目录
        ensure_output_dir(settings.UPLOAD_DIR)

        upload_file_path = os.path.join(settings.UPLOAD_DIR, safe_filename)
        # 使用 aiofiles 异步写入文件
        async with aiofiles.open(upload_file_path, "wb") as f:
            await f.write(file_content)

        logger.info(f"文件已保存: {upload_file_path}")

        if extract_to == DataDirEnum.MODEL5:
            extract_dir = os.path.join(settings.DATA_DIR_MODEL5, parsed_filename)
        elif extract_to == DataDirEnum.MODEL4:
            extract_dir = os.path.join(settings.DATA_DIR_MODEL4, parsed_filename)
        elif extract_to == DataDirEnum.MODEL6:
            extract_dir = os.path.join(settings.DATA_DIR_MODEL6, parsed_filename)

        # 创建celery任务
        task = file_task.extract_file_task.delay(  # type: ignore
            archive_path=upload_file_path,
            extract_to=extract_dir,
            cleanup_archive=cleanup_archive,
        )
        if not task.id:
            raise HTTPException(status_code=500, detail="创建任务失败")
        logger.info(f"任务已提交: task_id={task.id}")

        result = AsyncResult(task.id, app=celery_app)

        return {
            "task_id": task.id,
            "status": result.state,
            "progress": 0,
            "message": "上传任务创建成功！",
            "uploaded_bytes": file_size,
        }

    except Exception as e:
        logger.error(f"文件上传异常: {e}", exc_info=True)
        raise handle_service_error(e, "文件上传")


@router.get("/upload-status/{task_id}", response_model=UploadTaskStatusResponse)
async def get_upload_status(task_id: str):
    """
    查询文件上传和解压任务状态

    **功能说明:**
    根据任务 ID 查询文件上传和解压任务的执行状态和进度

    **参数说明:**
    - task_id: 文件上传任务 ID（由 /upload-file 接口返回）

    **返回说明:**
    - task_id: 任务 ID
    - status: 任务状态（PENDING/PROGRESS/SUCCESS/FAILURE）
    - progress: 解压进度（0-100）
    - message: 状态消息
    - result: 任务结果（完成后），包含：
      - success: 是否成功
      - extract_dir: 解压目录路径
      - extracted_files: 解压的文件列表
      - file_count: 文件数量
      - total_size: 总大小（字节）

    **使用示例:**
    ```bash
    # 查询任务状态
    curl "http://localhost:8000/v1/upload-status/abc-123-def"

    # 返回示例（进行中）
    {
      "task_id": "abc-123-def",
      "status": "PROGRESS",
      "progress": 65,
      "message": "正在解压文件...",
      "result": null
    }

    # 返回示例（完成）
    {
      "task_id": "abc-123-def",
      "status": "SUCCESS",
      "progress": 100,
      "message": "解压完成！",
      "result": {
        "success": true,
        "extract_dir": "/app/data/data-model-4/1208",
        "extracted_files": ["performance_data.jsonl"],
        "file_count": 1,
        "total_size": 1048576
      }
    }
    ```

    **状态值说明:**
    - `PENDING`: 任务等待中
    - `PROGRESS`: 解压进行中
    - `SUCCESS`: 解压成功
    - `FAILURE`: 解压失败
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

        # SUCCESS 状态时进度为 100
        elif result.state == "SUCCESS":
            progress = 100
            message = "任务完成！"

        # FAILURE 状态
        elif result.state == "FAILURE":
            message = task_info.get("error", "任务失败") if isinstance(task_info, dict) else str(task_info)

        return {
            "task_id": task_id,
            "status": result.state,
            "progress": progress,
            "message": message,
            "result": result.result if result.successful() else None,
        }

    except Exception as e:
        logger.error(f"查询任务状态异常: {e}", exc_info=True)
        raise handle_service_error(e, "查询任务状态")


# =====================================================
# 清理文件夹端点
# =====================================================


@router.delete("/clean-directories", response_model=CleanDirectoriesResponse)
async def clean_directories(
    clean_data: DataDirEnum | None = Query(None, description="是否清理 data 目录"),
    clean_output: DataDirEnum | None = Query(None, description="是否清理 output 目录"),
):
    """
    清理 data 和 output 文件夹

    **功能说明:**
    删除指定目录中的所有文件和子目录，释放磁盘空间

    **参数说明:**
    - clean_data: 是否清理 ./data 目录 (默认 True)
    - clean_output: 是否清理 ./output 目录 (默认 True)

    **返回说明:**
    - cleaned_data: 是否清理了 data 目录
    - cleaned_output: 是否清理了 output 目录
    - data_size: data 目录释放的空间 (字节)
    - output_size: output 目录释放的空间 (字节)

    **使用示例:**
    ```bash
    # 清理所有目录
    curl -X DELETE "http://localhost:8000/v1/clean-directories"

    # 仅清理 data 目录
    curl -X DELETE "http://localhost:8000/v1/clean-directories?clean_output=false"

    # 仅清理 output 目录
    curl -X DELETE "http://localhost:8000/v1/clean-directories?clean_data=false"
    ```
    """
    try:
        # 调用服务层处理业务逻辑
        result = file_management_service.clean_data_and_output_directories(clean_data=clean_data, clean_output=clean_output)

        if not result["success"]:
            raise HTTPException(status_code=500, detail=result.get("error", "清理目录失败"))

        return CleanDirectoriesResponse(
            status=ResponseStatus.SUCCESS,
            message="目录清理完成",
            cleaned_data=result.get("cleaned_data", False),
            cleaned_output=result.get("cleaned_output", False),
            data_size=result.get("data_size") if result.get("cleaned_data") else None,
            output_size=(result.get("output_size") if result.get("cleaned_output") else None),
        )

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"清理目录异常: {e}", exc_info=True)
        raise handle_service_error(e, "清理目录")


# =====================================================
# 文件结果获取端点
# =====================================================


@router.get("/get-file")
async def get_file_result(file_path: str = Query(..., description="文件路径")):
    """
    获取文件结果（CSV/TXT）

    **功能说明:**
    返回指定路径的 CSV 或 TXT 文件内容

    **参数说明:**
    - file_path: 文件路径，支持相对路径或绝对路径

    **安全限制:**
    - 仅允许访问 ./output/ 和 ./data/ 目录下的文件
    - 仅支持 .csv 和 .txt 文件

    **返回说明:**
    - Content-Type: text/csv 或 text/plain
    - 响应体: 文件原始内容
    """
    try:
        # 1. 验证路径安全性
        safe_path = validate_safe_file_path(
            file_path,
            allowed_dirs=[
                os.path.abspath(settings.OUTPUT_DIR_MODEL_4),
                os.path.abspath(settings.OUTPUT_DIR_MODEL_5),
                os.path.abspath(settings.OUTPUT_DIR_MODEL_6),
                os.path.abspath(settings.DATA_DIR_MODEL4),
                os.path.abspath(settings.DATA_DIR_MODEL5),
                os.path.abspath(settings.DATA_DIR_MODEL6),
            ],
        )

        # 2. 根据文件扩展名设置 Content-Type
        file_ext = os.path.splitext(safe_path)[1].lower()
        media_type = "text/csv" if file_ext == ".csv" else "text/plain"

        # 3. 返回文件内容（使用 StreamingResponse 支持大文件）
        async def file_iterator():
            async with aiofiles.open(safe_path, mode="rb") as f:
                while chunk := await f.read(64 * 1024):  # 64KB chunks
                    yield chunk

        return StreamingResponse(
            file_iterator(),
            media_type=media_type,
            headers={"Content-Disposition": f'attachment; filename="{os.path.basename(safe_path)}"'},
        )

    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"获取文件结果异常: {e}", exc_info=True)
        raise handle_service_error(e, "获取文件结果")
