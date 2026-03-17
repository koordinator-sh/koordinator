#!/bin/bash

# Docker镜像打包脚本 - 从文件读取镜像列表
# 功能：从文件读取镜像列表，检查镜像是否存在，然后将这些镜像打包成tar包

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 显示帮助信息
show_help() {
    echo "用法: $0 [镜像列表文件] [输出文件名]"
    echo ""
    echo "参数说明:"
    echo "  镜像列表文件    包含镜像名称的文件，每行一个镜像（可选，默认为images.txt）"
    echo "  输出文件名      打包输出的文件名（可选，默认为docker-images-日期时间.tar）"
    echo ""
    echo "示例:"
    echo "  $0                                 # 使用默认的images.txt文件"
    echo "  $0 my-images.txt                    # 指定镜像列表文件"
    echo "  $0 images.txt my-package.tar        # 指定镜像列表文件和输出文件名"
    echo ""
    echo "镜像列表文件格式示例（每行一个镜像）:"
    echo "  registry.cn-beijing.aliyuncs.com/koordinator-sh/koordlet:v1.7.0"
    echo "  ghcr.io/koordinator-sh/koord-descheduler:release-1.7-23f05842"
    echo "  nginx:latest"
    echo "  redis:7.0"
}

# 检查参数
if [ "$1" = "-h" ] || [ "$1" = "--help" ]; then
    show_help
    exit 0
fi

echo -e "${BLUE}================================${NC}"
echo -e "${BLUE}   Docker镜像打包工具${NC}"
echo -e "${BLUE}================================${NC}"

# 设置默认文件
IMAGE_LIST_FILE="${1:-images.txt}"
OUTPUT_FILE="${2:-docker-images-$(date +%Y%m%d-%H%M%S).tar}"

# 检查镜像列表文件是否存在
if [ ! -f "$IMAGE_LIST_FILE" ]; then
    echo -e "${RED}❌ 镜像列表文件不存在: $IMAGE_LIST_FILE${NC}"
    echo ""
    echo -e "${YELLOW}请创建镜像列表文件，每行一个镜像名称，例如：${NC}"
    echo "  registry.cn-beijing.aliyuncs.com/koordinator-sh/koordlet:v1.7.0"
    echo "  ghcr.io/koordinator-sh/koord-descheduler:release-1.7-23f05842"
    echo "  nginx:latest"
    exit 1
fi

echo -e "${YELLOW}镜像列表文件:${NC} $IMAGE_LIST_FILE"
echo -e "${YELLOW}输出文件:${NC} $OUTPUT_FILE"
echo ""

# 检查Docker是否运行
if ! docker info >/dev/null 2>&1; then
    echo -e "${RED}❌ Docker未运行，请启动Docker${NC}"
    exit 1
fi

# 从文件读取镜像列表
echo -e "${YELLOW}从文件读取镜像列表...${NC}"
IMAGES=()
while IFS= read -r line || [ -n "$line" ]; do
    # 跳过空行和注释行（以#开头的行）
    if [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]]; then
        continue
    fi
    # 去除首尾空格
    line=$(echo "$line" | xargs)
    if [ -n "$line" ]; then
        IMAGES+=("$line")
    fi
done < "$IMAGE_LIST_FILE"

# 检查是否读取到镜像
if [ ${#IMAGES[@]} -eq 0 ]; then
    echo -e "${RED}❌ 镜像列表文件中没有有效的镜像名称${NC}"
    exit 1
fi

echo -e "${GREEN}从文件读取到 ${#IMAGES[@]} 个镜像${NC}"
echo ""

# 检查镜像是否存在
echo -e "${YELLOW}检查镜像是否存在...${NC}"
MISSING_IMAGES=()
EXISTING_IMAGES=()
TOTAL_SIZE=0

for image in "${IMAGES[@]}"; do
    echo -n "检查: $image ... "
    if docker image inspect "$image" >/dev/null 2>&1; then
        echo -e "${GREEN}✅ 存在${NC}"
        EXISTING_IMAGES+=("$image")
        
        # 获取镜像大小
        size=$(docker image inspect "$image" --format='{{.Size}}' 2>/dev/null || echo "0")
        TOTAL_SIZE=$((TOTAL_SIZE + size))
    else
        echo -e "${RED}❌ 缺失${NC}"
        MISSING_IMAGES+=("$image")
    fi
done

# 如果有缺失的镜像，询问是否继续
if [ ${#MISSING_IMAGES[@]} -ne 0 ]; then
    echo ""
    echo -e "${RED}发现缺失的镜像 (${#MISSING_IMAGES[@]}个)：${NC}"
    for img in "${MISSING_IMAGES[@]}"; do
        echo "  - $img"
    done
    echo ""
    
    if [ ${#EXISTING_IMAGES[@]} -eq 0 ]; then
        echo -e "${RED}没有可打包的镜像，退出${NC}"
        exit 1
    fi
    
    read -p "是否继续打包已存在的 ${#EXISTING_IMAGES[@]} 个镜像？(y/n) " -n 1 -r
    echo ""
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo -e "${RED}打包已取消${NC}"
        exit 1
    fi
    
    # 使用存在的镜像
    IMAGES=("${EXISTING_IMAGES[@]}")
fi

# 如果没有镜像可打包，退出
if [ ${#IMAGES[@]} -eq 0 ]; then
    echo -e "${RED}❌ 没有可打包的镜像${NC}"
    exit 1
fi

echo ""
echo -e "${YELLOW}准备打包以下 ${#IMAGES[@]} 个镜像：${NC}"
for image in "${IMAGES[@]}"; do
    echo "  - $image"
done

echo ""
echo -e "${YELLOW}打包到文件：$OUTPUT_FILE${NC}"

# 显示打包进度
echo -e "${BLUE}开始打包...${NC}"

# 执行打包
if docker save "${IMAGES[@]}" -o "$OUTPUT_FILE"; then
    echo -e "${GREEN}✅ 打包成功！${NC}"
    
    # 显示文件信息
    file_size=$(du -h "$OUTPUT_FILE" | cut -f1)
    echo "文件名称：$OUTPUT_FILE"
    echo "文件大小：$file_size"
    
    # 显示总大小
    if [ $TOTAL_SIZE -gt 0 ]; then
        echo "镜像总大小：$(numfmt --to=iec-i --suffix=B $TOTAL_SIZE 2>/dev/null || echo "$TOTAL_SIZE bytes")"
    fi
    
    # 验证tar包
    echo ""
    echo -e "${YELLOW}验证tar包内容：${NC}"
    
    # 获取tar包中的镜像数量
    if command -v python3 &>/dev/null; then
        IMAGE_COUNT=$(tar -xf "$OUTPUT_FILE" manifest.json -O 2>/dev/null | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    print(len(data))
except:
    print('unknown')
" 2>/dev/null || echo "unknown")
        echo "tar包中包含 $IMAGE_COUNT 个镜像"
    fi
    
    # 显示前几层
    echo "tar包内容预览（前10行）："
    tar tvf "$OUTPUT_FILE" 2>/dev/null | head -10 || echo "无法读取tar包内容"
    
    # 创建镜像列表文件（记录打包的镜像）
    LIST_FILE="${OUTPUT_FILE%.tar}-list.txt"
    printf "%s\n" "${IMAGES[@]}" > "$LIST_FILE"
    echo -e "${GREEN}镜像列表已保存到: $LIST_FILE${NC}"
    
    echo ""
    echo -e "${GREEN}打包完成！${NC}"
    echo "可以使用以下命令查看tar包内容："
    echo "  tar tvf $OUTPUT_FILE"
    echo "可以使用以下命令加载镜像："
    echo "  docker load -i $OUTPUT_FILE"
else
    echo -e "${RED}❌ 打包失败！${NC}"
    exit 1
fi
