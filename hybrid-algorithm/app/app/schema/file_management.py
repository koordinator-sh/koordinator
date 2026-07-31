# -*- coding: utf-8 -*-
"""
文件管理相关模型 for PSBC API
"""

from enum import StrEnum
from typing import Optional

from pydantic import BaseModel, Field

from app.schema.common import BaseResponse


class CleanDirectoriesResponse(BaseResponse):
    """清理文件夹响应"""

    cleaned_data: Optional[bool] = Field(None, description="是否清理了 data 目录")
    cleaned_output: Optional[bool] = Field(None, description="是否清理了 output 目录")
    data_size: Optional[int] = Field(None, description="data 目录释放的空间(字节)")
    output_size: Optional[int] = Field(None, description="output 目录释放的空间(字节)")


class DataDirEnum(StrEnum):
    MODEL4 = "MODEL4"
    MODEL5 = "MODEL5"
    MODEL6 = "MODEL6"
