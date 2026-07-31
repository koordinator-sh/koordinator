# -*- coding: utf-8 -*-
"""
聚类分析相关模型 for PSBC API
"""

from typing import Any, Dict, Optional

from pydantic import BaseModel, Field



class ClusterCsvRow(BaseModel):
    """聚类结果 CSV 行数据"""

    pod: str = Field(..., description="Pod 名称")
    cluster: int = Field(..., description="聚类簇 ID")
    pod_type: str = Field(..., description="Pod 类型")
    namespace: str = Field(..., description="namespace")


class LastClusterResult(BaseModel):
    """最后聚类结果"""

    output_dir: str = Field(..., description="结果输出目录")
    statistics: str = Field(..., description="聚类统计信息文本内容")
    csv_data: list[ClusterCsvRow] = Field(default_factory=list, description="CSV 前三列数据")
    created_at: str = Field(..., description="创建时间")
    csv_file_path: str = Field(..., description="CSV 文件路径（用于下载）")
    pod_count: int = Field(default=0, description="Pod 总数")
