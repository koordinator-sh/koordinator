"""
Author: hxy
Date: 2026/03/13
Description:
修复导入相关bug，设置python的工作路径，可以找到自定义的包
"""

import os
import sys

from dotenv import load_dotenv


load_dotenv()
PYTHONPATH = os.getenv("PYTHONPATH")
if PYTHONPATH:
    sys.path.insert(0, PYTHONPATH)
