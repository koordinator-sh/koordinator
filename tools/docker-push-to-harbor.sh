#!/bin/bash

# Docker镜像推送脚本 - 将tar包中的镜像推送到镜像仓库
# 功能：从tar包加载镜像并推送到指定的镜像仓库，只保留最后的镜像名和tag

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 显示帮助信息
show_help() {
    echo "用法: $0 <镜像仓库地址> <tar包路径>"
    echo ""
    echo "参数说明:"
    echo "  镜像仓库地址    镜像仓库地址，例如: hub.example.com"
    echo "  tar包路径       Docker镜像tar包路径"
    echo ""
    echo "示例:"
    echo "  $0 hub.example.com ./docker-images-20240101.tar"
    echo "  $0 192.168.1.100:5000 ./images.tar"
    echo ""
    echo "环境变量:"
    echo "  REGISTRY_USER   仓库用户名（可选，如果不提供则会提示输入）"
    echo "  REGISTRY_PASS   仓库密码（可选，如果不提供则会提示输入）"
}

# 检查参数
if [ "$1" = "-h" ] || [ "$1" = "--help" ]; then
    show_help
    exit 0
fi

if [ $# -lt 2 ]; then
    echo -e "${RED}错误: 缺少必要参数${NC}"
    show_help
    exit 1
fi

# 获取参数
REGISTRY_ADDR="$1"
TAR_FILE="$2"

# 检查tar包是否存在
if [ ! -f "$TAR_FILE" ]; then
    echo -e "${RED}错误: tar包不存在: $TAR_FILE${NC}"
    exit 1
fi

echo -e "${BLUE}================================${NC}"
echo -e "${BLUE}   Docker镜像推送工具${NC}"
echo -e "${BLUE}================================${NC}"
echo -e "${YELLOW}镜像仓库地址:${NC} $REGISTRY_ADDR"
echo -e "${YELLOW}Tar包文件:${NC} $TAR_FILE"
echo ""

# 检查Docker是否运行
if ! docker info >/dev/null 2>&1; then
    echo -e "${RED}❌ Docker未运行，请启动Docker${NC}"
    exit 1
fi

# 登录镜像仓库（如果需要）
if [ -n "$REGISTRY_USER" ] || [ -n "$REGISTRY_PASS" ]; then
    echo -e "${YELLOW}登录到 $REGISTRY_ADDR...${NC}"
    
    # 如果环境变量中没有用户名密码，则提示输入
    if [ -z "$REGISTRY_USER" ]; then
        read -p "请输入用户名: " REGISTRY_USER
    fi
    if [ -z "$REGISTRY_PASS" ]; then
        read -s -p "请输入密码: " REGISTRY_PASS
        echo ""
    fi
    
    # 登录仓库
    if echo "$REGISTRY_PASS" | docker login "$REGISTRY_ADDR" -u "$REGISTRY_USER" --password-stdin; then
        echo -e "${GREEN}✅ 登录成功${NC}"
    else
        echo -e "${RED}❌ 登录失败${NC}"
        exit 1
    fi
else
    echo -e "${YELLOW}未提供认证信息，假设仓库无需认证或已登录${NC}"
fi

# 创建临时目录
TEMP_DIR=$(mktemp -d)
echo -e "${YELLOW}创建临时目录: $TEMP_DIR${NC}"

# 复制tar包到临时目录
cp "$TAR_FILE" "$TEMP_DIR/"
TAR_NAME=$(basename "$TAR_FILE")

echo ""
echo -e "${YELLOW}步骤1: 查看tar包中的镜像信息${NC}"

# 查看tar包中的镜像列表（不加载）
echo -e "${BLUE}tar包中的原始镜像:${NC}"
if command -v python3 &>/dev/null; then
    tar -xf "$TAR_FILE" manifest.json -O 2>/dev/null | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    for item in data:
        if 'RepoTags' in item and item['RepoTags']:
            for tag in item['RepoTags']:
                print(f'  - {tag}')
        else:
            # 如果没有标签，显示Repository和Tag信息
            repo = item.get('Repository', 'unknown')
            tag = item.get('Tag', 'latest')
            print(f'  - {repo}:{tag}')
except Exception as e:
    print(f'  无法解析manifest.json: {e}')
" || echo "  无法解析镜像信息，将继续加载过程"
else
    echo "  提示: 安装python3可以查看更详细的镜像信息"
fi

echo ""
echo -e "${YELLOW}步骤2: 加载Docker镜像${NC}"

# 获取加载前的镜像列表
BEFORE_IMAGES=$(docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null | sort)

# 加载镜像
cd "$TEMP_DIR"
echo -e "正在加载镜像: $TAR_NAME"
if docker load -i "$TAR_NAME"; then
    echo -e "${GREEN}✅ 镜像加载成功${NC}"
else
    echo -e "${RED}❌ 镜像加载失败${NC}"
    cd - >/dev/null
    rm -rf "$TEMP_DIR"
    exit 1
fi
cd - >/dev/null

# 获取新加载的镜像
AFTER_IMAGES=$(docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null | sort)

# 找出新加载的镜像
LOADED_IMAGES=""
if command -v comm &>/dev/null; then
    LOADED_IMAGES=$(comm -13 <(echo "$BEFORE_IMAGES") <(echo "$AFTER_IMAGES") 2>/dev/null)
else
    # 如果没有comm命令，使用grep方式
    while IFS= read -r img; do
        if ! echo "$BEFORE_IMAGES" | grep -q "^$img$"; then
            LOADED_IMAGES="${LOADED_IMAGES}${img}"$'\n'
        fi
    done <<< "$AFTER_IMAGES"
fi

# 如果还是没找到，使用更直接的方式：从manifest.json读取
if [ -z "$LOADED_IMAGES" ]; then
    echo -e "${YELLOW}尝试从tar包中直接读取镜像信息...${NC}"
    # 使用Python解析manifest.json获取镜像信息
    if command -v python3 &>/dev/null; then
        LOADED_IMAGES=$(tar -xf "$TAR_FILE" manifest.json -O 2>/dev/null | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    for item in data:
        if 'RepoTags' in item and item['RepoTags']:
            for tag in item['RepoTags']:
                print(tag)
        else:
            # 尝试从config获取信息
            if 'Config' in item and 'config' in item['Config']:
                config = item['Config']['config']
                if 'Image' in config:
                    print(config['Image'])
except:
    pass
")
    fi
fi

# 函数：提取最后的镜像名和tag
extract_image_name() {
    local full_name="$1"
    # 去掉仓库地址（直到最后一个/之前的内容）
    # 例如：hub.example.com/myproject/nginx:latest -> nginx:latest
    # 例如：myapp-backend:v1.0 -> myapp-backend:v1.0
    # 例如：docker.io/library/redis:7.0 -> redis:7.0
    
    # 提取最后一个/之后的内容
    local base_name="${full_name##*/}"
    
    # 如果没有/，则使用原名称
    if [ -z "$base_name" ]; then
        base_name="$full_name"
    fi
    
    echo "$base_name"
}

# 将镜像列表转换为数组并去重
IMAGES=()
while IFS= read -r img; do
    # 去除可能的空白字符
    img=$(echo "$img" | xargs)
    # 检查是否为空或<none>镜像
    if [ -n "$img" ] && [ "$img" != "<none>:<none>" ]; then
        # 提取最后的镜像名（去掉仓库地址）
        simple_name=$(extract_image_name "$img")
        
        # 检查是否已存在（手动去重）
        found=0
        for existing in "${IMAGES[@]}"; do
            if [ "$existing" = "$simple_name" ]; then
                found=1
                break
            fi
        done
        if [ $found -eq 0 ]; then
            IMAGES+=("$simple_name")
            echo -e "${GREEN}  发现镜像: $img -> 简化名: $simple_name${NC}"
        fi
    fi
done <<< "$LOADED_IMAGES"

# 如果还没有找到镜像，尝试直接读取docker images中所有非none镜像
if [ ${#IMAGES[@]} -eq 0 ]; then
    echo -e "${YELLOW}使用当前Docker中的所有镜像${NC}"
    while IFS= read -r img; do
        if [ -n "$img" ] && [[ "$img" != *"<none>"* ]]; then
            # 提取最后的镜像名（去掉仓库地址）
            simple_name=$(extract_image_name "$img")
            
            # 检查是否已存在
            found=0
            for existing in "${IMAGES[@]}"; do
                if [ "$existing" = "$simple_name" ]; then
                    found=1
                    break
                fi
            done
            if [ $found -eq 0 ]; then
                IMAGES+=("$simple_name")
                echo -e "${GREEN}  发现镜像: $img -> 简化名: $simple_name${NC}"
            fi
        fi
    done <<< "$(docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null)"
fi

if [ ${#IMAGES[@]} -eq 0 ]; then
    echo -e "${RED}错误: 没有找到可推送的镜像${NC}"
    rm -rf "$TEMP_DIR"
    exit 1
fi

echo ""
echo -e "${GREEN}简化后的镜像列表（将推送到仓库）:${NC}"
for i in "${!IMAGES[@]}"; do
    echo "  $((i+1)). ${IMAGES[$i]}"
done

echo ""
echo -e "${YELLOW}步骤3: 推送到镜像仓库${NC}"
echo -e "${BLUE}目标仓库: $REGISTRY_ADDR/${NC}"
echo -e "${BLUE}推送策略: 只保留最后的镜像名和tag${NC}"
echo ""

# 推送计数器
SUCCESS_COUNT=0
FAILED_COUNT=0
FAILED_IMAGES=()
SUCCESS_IMAGES=()

for simple_name in "${IMAGES[@]}"; do
    # 跳过空行
    [ -z "$simple_name" ] && continue
    
    # 构建完整的镜像标签
    NEW_TAG="$REGISTRY_ADDR/$simple_name"
    
    echo ""
    echo -e "${YELLOW}处理镜像: $simple_name${NC}"
    echo -e "  将推送到: $NEW_TAG"
    
    # 需要找到原始镜像的完整名称来打标签
    # 在已加载的镜像中查找匹配的简化名
    ORIGINAL_IMAGE=""
    while IFS= read -r img; do
        img=$(echo "$img" | xargs)
        extracted=$(extract_image_name "$img")
        if [ "$extracted" = "$simple_name" ]; then
            ORIGINAL_IMAGE="$img"
            break
        fi
    done <<< "$LOADED_IMAGES"
    
    # 如果没有找到，尝试从所有镜像中查找
    if [ -z "$ORIGINAL_IMAGE" ]; then
        ALL_IMAGES=$(docker images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null)
        while IFS= read -r img; do
            img=$(echo "$img" | xargs)
            extracted=$(extract_image_name "$img")
            if [ "$extracted" = "$simple_name" ]; then
                ORIGINAL_IMAGE="$img"
                break
            fi
        done <<< "$ALL_IMAGES"
    fi
    
    if [ -z "$ORIGINAL_IMAGE" ]; then
        echo -e "  ${RED}❌ 找不到原始镜像: $simple_name${NC}"
        FAILED_COUNT=$((FAILED_COUNT + 1))
        FAILED_IMAGES+=("$simple_name")
        continue
    fi
    
    echo -e "  原始镜像: $ORIGINAL_IMAGE"
    
    # 重新打标签
    if docker tag "$ORIGINAL_IMAGE" "$NEW_TAG" 2>/dev/null; then
        echo -e "  ${GREEN}✅ 标签创建成功: $NEW_TAG${NC}"
    else
        echo -e "  ${RED}❌ 标签创建失败${NC}"
        FAILED_COUNT=$((FAILED_COUNT + 1))
        FAILED_IMAGES+=("$simple_name")
        continue
    fi
    
    # 推送镜像
    echo -e "  正在推送..."
    if docker push "$NEW_TAG"; then
        echo -e "  ${GREEN}✅ 推送成功${NC}"
        SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
        SUCCESS_IMAGES+=("$NEW_TAG")
    else
        echo -e "  ${RED}❌ 推送失败${NC}"
        FAILED_COUNT=$((FAILED_COUNT + 1))
        FAILED_IMAGES+=("$simple_name")
    fi
done

echo ""
echo -e "${BLUE}================================${NC}"
echo -e "${BLUE}   推送结果汇总${NC}"
echo -e "${BLUE}================================${NC}"
echo -e "${GREEN}✅ 成功: $SUCCESS_COUNT 个镜像${NC}"
if [ $SUCCESS_COUNT -gt 0 ]; then
    echo -e "${GREEN}成功推送的镜像:${NC}"
    for img in "${SUCCESS_IMAGES[@]}"; do
        echo "  - $img"
    done
fi

if [ $FAILED_COUNT -gt 0 ]; then
    echo -e "${RED}❌ 失败: $FAILED_COUNT 个镜像${NC}"
    echo -e "${RED}失败的镜像:${NC}"
    for img in "${FAILED_IMAGES[@]}"; do
        echo "  - $img"
    done
fi

# 清理临时文件和临时标签
echo ""
read -p "是否清理临时文件和临时标签？(y/n) " -n 1 -r
echo ""
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo -e "${YELLOW}清理临时文件...${NC}"
    rm -rf "$TEMP_DIR"
    
    echo -e "${YELLOW}清理临时标签...${NC}"
    for img in "${SUCCESS_IMAGES[@]}"; do
        docker rmi "$img" 2>/dev/null || true
    done
    echo -e "${GREEN}✅ 清理完成${NC}"
else
    echo -e "${YELLOW}保留临时文件和标签${NC}"
fi

# 显示拉取这些镜像的命令
if [ $SUCCESS_COUNT -gt 0 ]; then
    echo ""
    echo -e "${BLUE}================================${NC}"
    echo -e "${BLUE}   在其他机器拉取这些镜像${NC}"
    echo -e "${BLUE}================================${NC}"
    echo -e "如果需要从仓库拉取这些镜像，可以使用以下命令："
    for img in "${SUCCESS_IMAGES[@]}"; do
        echo "  docker pull $img"
    done
fi

echo ""
if [ $FAILED_COUNT -eq 0 ]; then
    echo -e "${GREEN}✅ 所有镜像推送成功！${NC}"
    exit 0
else
    echo -e "${RED}❌ 部分镜像推送失败，请检查错误信息${NC}"
    exit 1
fi
