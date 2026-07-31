# -*- coding: utf-8 -*-
"""
文件管理服务模块

负责文件清理、目录管理等业务逻辑
"""

import os
import shutil
from typing import Any, Dict, Optional

from app.schema.file_management import DataDirEnum
from app.settings import settings
from app.utils.logger import get_logger
from app.utils.tools import expand_path


logger = get_logger(__name__)


class FileManagementService:
    """文件管理服务类"""

    def __init__(self):
        """初始化服务"""
        self.data_dir = expand_path(settings.DATA_DIR_MODEL4)
        self.output_dir = expand_path(settings.OUTPUT_DIR_MODEL_4)

    def get_directory_size(self, dir_path: str) -> int:
        """计算目录大小

        Args:
            dir_path: 目录路径

        Returns:
            目录大小（字节）
        """
        total_size = 0
        for dirpath, dirnames, filenames in os.walk(dir_path):
            for filename in filenames:
                filepath = os.path.join(dirpath, filename)
                if os.path.exists(filepath):
                    total_size += os.path.getsize(filepath)
        return total_size

    def clean_directory(self, dir_path: str) -> Dict[str, Any]:
        """清理指定目录

        Args:
            dir_path: 目录路径

        Returns:
            清理结果:
            {
                'success': bool,
                'cleaned': bool,
                'size_freed': int,  # 释放的空间（字节）
                'error': str  # 仅失败时
            }
        """
        try:
            if not os.path.exists(dir_path):
                return {"success": True, "cleaned": False, "size_freed": 0}

            # 计算目录大小
            size_freed = self.get_directory_size(dir_path)

            # 删除目录内容
            for item in os.listdir(dir_path):
                item_path = os.path.join(dir_path, item)
                if os.path.isdir(item_path):
                    shutil.rmtree(item_path)
                else:
                    os.remove(item_path)

            logger.info(f"已清理目录: {dir_path}, 释放空间: {size_freed} 字节")

            return {"success": True, "cleaned": True, "size_freed": size_freed}

        except Exception as e:
            logger.error(f"清理目录失败: {dir_path}, 错误: {e}", exc_info=True)
            return {"success": False, "cleaned": False, "size_freed": 0, "error": str(e)}

    def clean_data_and_output_directories(
        self,
        clean_data: Optional[DataDirEnum] = None,
        clean_output: Optional[DataDirEnum] = None,
    ) -> Dict[str, Any]:
        """清理 data 和 output 目录

        Args:
            clean_data: 清理哪个模型的 data 目录（None=不清理, MODEL4=清理模型4, MODEL5=清理模型5）
            clean_output: 清理哪个模型的 output 目录（None=不清理, MODEL4=清理模型4, MODEL5=清理模型5）

        Returns:
            清理结果:
            {
                'success': bool,
                'cleaned_data': bool,
                'cleaned_output': bool,
                'data_size': int,  # data 目录释放的空间
                'output_size': int,  # output 目录释放的空间
                'error': str  # 仅失败时
            }
        """
        try:
            result = {"success": True, "cleaned_data": False, "cleaned_output": False, "data_size": 0, "output_size": 0}

            # 清理 data 目录
            if clean_data is not None:
                if clean_data == DataDirEnum.MODEL4:
                    data_dir = expand_path(settings.DATA_DIR_MODEL4)
                elif clean_data == DataDirEnum.MODEL5:
                    data_dir = expand_path(settings.DATA_DIR_MODEL5)
                elif clean_data == DataDirEnum.MODEL6:
                    data_dir = expand_path(settings.DATA_DIR_MODEL6)
                else:
                    return {"success": False, "error": f"不支持的 clean_data 值: {clean_data}"}

                data_result = self.clean_directory(data_dir)
                if not data_result["success"]:
                    return {"success": False, "error": f"清理 data 目录失败: {data_result.get('error', '未知错误')}"}
                result["cleaned_data"] = data_result["cleaned"]
                result["data_size"] = data_result["size_freed"]

            # 清理 output 目录
            if clean_output is not None:
                if clean_output == DataDirEnum.MODEL4:
                    output_dir = expand_path(settings.OUTPUT_DIR_MODEL_4)
                elif clean_output == DataDirEnum.MODEL5:
                    output_dir = expand_path(settings.OUTPUT_DIR_MODEL_5)
                elif clean_output == DataDirEnum.MODEL6:
                    output_dir = expand_path(settings.OUTPUT_DIR_MODEL_6)
                else:
                    return {"success": False, "error": f"不支持的 clean_output 值: {clean_output}"}

                output_result = self.clean_directory(output_dir)
                if not output_result["success"]:
                    return {"success": False, "error": f"清理 output 目录失败: {output_result.get('error', '未知错误')}"}
                result["cleaned_output"] = output_result["cleaned"]
                result["output_size"] = output_result["size_freed"]

            return result

        except Exception as e:
            logger.error(f"清理目录异常: {e}", exc_info=True)
            return {"success": False, "error": str(e)}

    def get_directory_info(self, dir_path: str) -> Dict[str, Any]:
        """获取目录信息

        Args:
            dir_path: 目录路径

        Returns:
            目录信息:
            {
                'exists': bool,
                'size': int,  # 目录大小（字节）
                'file_count': int,  # 文件数量
                'dir_count': int  # 子目录数量
            }
        """
        try:
            if not os.path.exists(dir_path):
                return {"exists": False, "size": 0, "file_count": 0, "dir_count": 0}

            size = 0
            file_count = 0
            dir_count = 0

            for dirpath, dirnames, filenames in os.walk(dir_path):
                dir_count += len(dirnames)
                for filename in filenames:
                    filepath = os.path.join(dirpath, filename)
                    if os.path.exists(filepath):
                        size += os.path.getsize(filepath)
                        file_count += 1

            return {"exists": True, "size": size, "file_count": file_count, "dir_count": dir_count}

        except Exception as e:
            logger.error(f"获取目录信息失败: {dir_path}, 错误: {e}", exc_info=True)
            return {"exists": False, "size": 0, "file_count": 0, "dir_count": 0, "error": str(e)}


# 创建服务实例
file_management_service = FileManagementService()
