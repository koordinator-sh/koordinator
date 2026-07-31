# -*- coding: utf-8 -*-
"""
文件处理工具模块
提供文件上传、验证、解压等功能
"""

import gzip
import os
from pathlib import Path
import shutil
import tarfile
from typing import Any, Callable, Dict, List, Optional

import py7zr
from py7zr.callbacks import ExtractCallback

from app.utils.logger import get_logger
from app.utils.tools import ensure_output_dir


logger = get_logger(__name__)


# =====================================================
# 异常定义
# =====================================================


class FileValidationError(Exception):
    """文件验证异常基类"""

    pass


class FileExtensionError(FileValidationError):
    """文件扩展名错误"""

    pass


class ArchiveCorruptedError(FileValidationError):
    """压缩文件损坏"""

    pass


# =====================================================
# py7zr 回调类
# =====================================================


class SimpleExtractCallback(ExtractCallback):
    """简单的提取回调类,用于满足 py7zr 的要求"""

    def __init__(self):
        self.files_done = 0

    def report_start(self, processing_file_path: str, processing_bytes: str) -> None:
        """开始处理文件时调用"""
        pass

    def report_end(self, processing_file_path: str, wrote_bytes: str) -> None:
        """处理文件结束时调用"""
        self.files_done += 1

    def report_post_extract(self) -> None:
        """提取完成后调用"""
        pass

    def report_start_preparation(self) -> None:
        """开始准备时调用"""
        pass

    def report_update(self, decompressed_bytes: str) -> None:
        """更新进度时调用"""
        pass

    def report_warning(self, message: str) -> None:
        """警告时调用"""
        pass

    def report_postprocess(self) -> None:
        """后处理后调用"""
        pass


# =====================================================
# 文件验证函数
# =====================================================


def validate_upload_file(file_filename: str, allowed_extensions: List[str]) -> None:
    """验证上传文件

    Args:
        file_filename: 文件名
        allowed_extensions: 允许的扩展名列表 (如 ['.7z', '.zip'])

    Raises:
        FileExtensionError: 扩展名不允许
    """
    file_ext = os.path.splitext(file_filename)[1].lower()

    if file_ext not in allowed_extensions:
        raise FileExtensionError(f"文件扩展名 '{file_ext}' 不允许。仅支持: {', '.join(allowed_extensions)}")

    logger.info(f"文件验证通过: {file_filename}, 扩展名: {file_ext}")


def secure_filename(filename: str) -> str:
    """生成安全的文件名,防止路径遍历攻击

    Args:
        filename: 原始文件名

    Returns:
        安全的文件名
    """
    # 去除路径分隔符
    filename = os.path.basename(filename)

    # 移除危险字符
    dangerous_chars = ["..", "~", "\\", "/", "\x00"]
    for char in dangerous_chars:
        filename = filename.replace(char, "_")

    # 保留文件名的基本字符
    filename = filename.strip()

    if not filename:
        filename = "unnamed_file"

    logger.debug(f"安全文件名: {filename} <- {filename}")
    return filename


def parse_filename(filename: str) -> str:
    return filename.split("_")[2]


# =====================================================
# 7z 文件解压函数
# =====================================================


def extract_file(archive_path: str, extract_to: str) -> Dict[str, Any]:
    ext = archive_path.split(".")[-1]
    match ext:
        case "7z":
            result = extract_7z_file(archive_path, extract_to)
        case "tar":
            result = extract_tar_file(archive_path, extract_to)
        case "gz":
            result = extract_gz_file(archive_path, extract_to)
        case _:
            raise ValueError("文件扩展名验证失败,需是 gz / 7z / tar")

    return result


def extract_7z_file(archive_path: str, extract_to: str) -> Dict[str, Any]:
    """解压 7z 文件

    Args:
        archive_path: 压缩文件路径
        extract_to: 解压目标目录

    Returns:
        解压结果字典:
        {
            'success': bool,
            'extract_dir': str,  # 解压目录
            'extracted_files': list,
            'file_count': int,
            'total_size': int,
            'error': str  # 仅失败时
        }

    Raises:
        ArchiveCorruptedError: 压缩文件损坏
        Exception: 其他异常
    """
    try:
        logger.info(f"开始解压 7z 文件: {archive_path} -> {extract_to}")

        # 确保目标目录存在
        os.makedirs(extract_to, exist_ok=True)

        # 打开压缩文件
        with py7zr.SevenZipFile(archive_path, mode="r") as archive:
            # 获取文件列表
            file_list = archive.getnames()
            total_files = len(file_list)

            logger.info(f"压缩包包含 {total_files} 个文件")

            # 解压文件(使用简单的回调)
            callback = SimpleExtractCallback()
            archive.extractall(path=extract_to, callback=callback)

        # 收集解压后的文件信息
        result = _collect_file_info(extract_to)

        logger.info(f"解压成功: {result['file_count']} 个文件, 总大小: {result['total_size']} 字节")

        return {
            "success": True,
            "extract_dir": extract_to,
            "extracted_files": result["extracted_files"],
            "file_count": result["file_count"],
            "total_size": result["total_size"],
        }

    except Exception as e:
        logger.error(f"解压失败: {e}", exc_info=True)
        return {"success": False, "error": str(e)}

def extract_tar_file(archive_path: str, extract_to: str) -> Dict[str, Any]:
    """解压 tar 文件

    Args:
        archive_path: 压缩文件路径
        extract_to: 解压目标目录

    Returns:
        解压结果字典:
        {
            'success': bool,
            'extract_dir': str,
            'extracted_files': list,
            'file_count': int,
            'total_size': int,
            'error': str  # 仅失败时
        }
    """
    try:
        logger.info(f"开始解压 tar 文件: {archive_path} -> {extract_to}")

        # 确保目标目录存在
        os.makedirs(extract_to, exist_ok=True)

        # 打开并解压 tar 文件
        with tarfile.open(archive_path, mode="r:*") as archive:
            # 获取文件列表
            file_list = archive.getnames()
            total_files = len(file_list)

            logger.info(f"压缩包包含 {total_files} 个文件")

            # 解压所有文件
            archive.extractall(path=extract_to)

        # 收集解压后的文件信息
        result = _collect_file_info(extract_to)

        logger.info(f"解压成功: {result['file_count']} 个文件, 总大小: {result['total_size']} 字节")

        return {
            "success": True,
            "extract_dir": extract_to,
            "extracted_files": result["extracted_files"],
            "file_count": result["file_count"],
            "total_size": result["total_size"],
        }

    except tarfile.TarError as e:
        logger.error(f"tar 文件损坏或格式错误: {e}")
        raise ArchiveCorruptedError(f"压缩文件损坏: {str(e)}")

    except Exception as e:
        logger.error(f"解压失败: {e}", exc_info=True)
        return {"success": False, "error": str(e)}


def extract_gz_file(archive_path: str, extract_to: str) -> Dict[str, Any]:
    """解压 gz 文件

    注意：gz 文件通常只包含单个文件，直接解压到目标目录

    Args:
        archive_path: 压缩文件路径
        extract_to: 解压目标目录

    Returns:
        解压结果字典:
        {
            'success': bool,
            'extract_dir': str,
            'extracted_files': list,
            'file_count': int,
            'total_size': int,
            'error': str  # 仅失败时
        }
    """
    try:
        logger.info(f"开始解压 gz 文件: {archive_path} -> {extract_to}")

        # 确保目标目录存在
        os.makedirs(extract_to, exist_ok=True)

        # 确定输出文件名（去除 .gz 扩展名）
        original_filename = os.path.basename(archive_path)
        if original_filename.endswith('.gz'):
            output_filename = original_filename[:-3]  # 去掉 .gz
        else:
            output_filename = original_filename

        output_path = os.path.join(extract_to, output_filename)

        # 解压 gz 文件
        with gzip.open(archive_path, 'rb') as f_in:
            with open(output_path, 'wb') as f_out:
                shutil.copyfileobj(f_in, f_out)

        # 获取文件大小
        file_size = os.path.getsize(output_path)

        # 计算相对路径
        rel_path = os.path.relpath(output_path, extract_to)

        extracted_files = [{
            "file_path": output_path,
            "relative_path": rel_path,
            "file_size": file_size,
            "is_compressed": False,
        }]

        logger.info(f"解压成功: 1 个文件, 大小: {file_size} 字节")

        return {
            "success": True,
            "extract_dir": extract_to,
            "extracted_files": extracted_files,
            "file_count": 1,
            "total_size": file_size,
        }

    except (gzip.BadGzipFile, OSError) as e:
        logger.error(f"gz 文件损坏或格式错误: {e}")
        raise ArchiveCorruptedError(f"压缩文件损坏: {str(e)}")

    except Exception as e:
        logger.error(f"解压失败: {e}", exc_info=True)
        return {"success": False, "error": str(e)}


def _collect_file_info(extract_to: str) -> Dict[str, Any]:
    """收集解压后的文件信息

    Args:
        extract_to: 解压目录

    Returns:
        文件信息字典
    """
    try:
        total_size = 0
        extracted_files = []
        file_count = 0

        # 遍历解压后的文件
        for root, dirs, files in os.walk(extract_to):
            for file in files:
                file_path = os.path.join(root, file)

                if os.path.isfile(file_path):
                    file_size = os.path.getsize(file_path)
                    total_size += file_size
                    file_count += 1

                    # 计算相对路径
                    rel_path = os.path.relpath(file_path, extract_to)

                    extracted_files.append({
                        "file_path": file_path,
                        "relative_path": rel_path,
                        "file_size": file_size,
                        "is_compressed": file.endswith('.gz'),
                    })

        return {
            "success": True,
            "extracted_files": extracted_files,
            "file_count": file_count,
            "total_size": total_size,
        }

    except Exception as e:
        logger.error(f"收集文件信息失败: {e}", exc_info=True)
        return {"success": False, "error": str(e)}



# =====================================================
# 文件列表函数
# =====================================================


def list_extracted_files(directory: str, base_dir: Optional[str] = None) -> List[Dict[str, Any]]:
    """列出解压后的文件信息

    Args:
        directory: 目录路径
        base_dir: 基础目录(用于计算相对路径)

    Returns:
        文件信息列表:
        [
            {
                'file_path': '相对路径',
                'file_size': 大小,
                'is_compressed': bool
            }
        ]
    """
    files_info = []

    if not os.path.exists(directory):
        logger.warning(f"目录不存在: {directory}")
        return files_info

    for root, dirs, files in os.walk(directory):
        for file in files:
            file_path = os.path.join(root, file)

            if os.path.isfile(file_path):
                file_size = os.path.getsize(file_path)
                rel_path = os.path.relpath(file_path, base_dir) if base_dir else file_path

                files_info.append({"file_path": rel_path, "file_size": file_size, "is_compressed": file.endswith(".gz")})

    logger.info(f"列出文件: {len(files_info)} 个文件")
    return files_info


# =====================================================
# 文件清理函数
# =====================================================


def cleanup_temp_files(*file_paths: str) -> None:
    """清理临时文件

    Args:
        *file_paths: 要删除的文件路径列表
    """
    for file_path in file_paths:
        try:
            if os.path.exists(file_path):
                if os.path.isfile(file_path):
                    os.remove(file_path)
                    logger.info(f"已删除文件: {file_path}")
                elif os.path.isdir(file_path):
                    shutil.rmtree(file_path)
                    logger.info(f"已删除目录: {file_path}")
        except Exception as e:
            logger.error(f"删除文件失败 {file_path}: {e}")


# =====================================================
# 磁盘空间检查
# =====================================================


def check_disk_space(required_bytes: int, path: str = ".") -> bool:
    """检查磁盘剩余空间

    Args:
        required_bytes: 需要的字节数
        path: 检查路径

    Returns:
        是否有足够空间
    """
    try:
        stat = os.statvfs(path)
        free_space = stat.f_bavail * stat.f_frsize

        has_space = free_space >= required_bytes

        if not has_space:
            logger.warning(f"磁盘空间不足: 需要 {required_bytes} 字节, 可用 {free_space} 字节")

        return has_space

    except Exception as e:
        logger.error(f"检查磁盘空间失败: {e}")
        return True  # 检查失败时跳过,继续执行


def calculate_directory_size(directory: str) -> int:
    """计算目录总大小

    Args:
        directory: 目录路径

    Returns:
        总大小(字节)
    """
    total_size = 0

    if not os.path.exists(directory):
        return 0

    for root, dirs, files in os.walk(directory):
        for file in files:
            file_path = os.path.join(root, file)
            if os.path.isfile(file_path):
                total_size += os.path.getsize(file_path)

    return total_size


def save_uploaded_file_stream(
    source_file,
    target_path: str,
    chunk_size: int = 8 * 1024 * 1024,
    progress_callback: Optional[Callable[[int], None]] = None,
) -> int:
    """流式保存上传文件(适用于大文件)

    Args:
        source_file: 源文件对象(需实现 read() 方法)
        target_path: 目标文件路径
        chunk_size: 分块大小(默认 8MB)
        progress_callback: 进度回调函数 callback(uploaded_bytes)

    Returns:
        保存的字节数
    """
    ensure_output_dir(os.path.dirname(target_path))

    uploaded_bytes = 0

    with open(target_path, "wb") as f:
        while True:
            chunk = source_file.read(chunk_size)
            if not chunk:
                break

            f.write(chunk)
            uploaded_bytes += len(chunk)

            if progress_callback:
                progress_callback(uploaded_bytes)

    logger.info(f"流式保存文件完成: {target_path}, 大小: {uploaded_bytes} 字节")
    return uploaded_bytes



def get_latest_dir(dir:str) -> str:
    dirs = sorted(os.listdir(dir))
    if not dirs:
        raise ValueError("请先上传文件")
    return dirs[-1]
