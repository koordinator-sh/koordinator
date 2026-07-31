#!/bin/bash

# ============================================
# PSBC 服务启动脚本 (Git Bash)
# ============================================

# 禁用 Git Bash 的路径转换
export MSYS_NO_PATHCONV=1

echo "========================================="
echo "PSBC 服务启动脚本"
echo "========================================="

# 1. 创建网络和卷
echo "[1/7] 创建网络和数据卷..."
docker network create psbc-network 2>/dev/null || true
docker volume create rabbitmq-data 2>/dev/null || true
docker volume create redis-data 2>/dev/null || true
docker volume create psbc-shared-data 2>/dev/null || true
docker volume create psbc-shared-output 2>/dev/null || true
docker volume create psbc-shared-temp 2>/dev/null || true
docker volume create psbc-shared-logs 2>/dev/null || true

# 2. 清理旧容器
echo "[2/7] 清理旧容器..."
docker stop rabbitmq redis psbc-api psbc-worker psbc-flower 2>/dev/null
docker rm rabbitmq redis psbc-api psbc-worker psbc-flower 2>/dev/null

# 3. 启动 RabbitMQ
echo "[3/7] 启动 RabbitMQ..."
docker run -d \
  --name rabbitmq \
  --network psbc-network \
  -p 5672:5672 \
  -p 15672:15672 \
  -e RABBITMQ_DEFAULT_USER=guest \
  -e RABBITMQ_DEFAULT_PASS=guest \
  -v rabbitmq-data:/var/lib/rabbitmq \
  --restart unless-stopped \
  rabbitmq:3.13-management

echo "等待 RabbitMQ 启动..."
sleep 10

# 4. 启动 Redis
echo "[4/7] 启动 Redis..."
docker run -d \
  --name redis \
  --network psbc-network \
  -p 6379:6379 \
  -v redis-data:/data \
  --restart unless-stopped \
  redis:7-alpine redis-server --appendonly yes

sleep 5

# 5. 启动 API
echo "[5/7] 启动 API 服务..."
docker run -d \
  --name psbc-api \
  --network psbc-network \
  -p 8000:8000 \
  -e PYTHONPATH=/app \
  -e RABBITMQ_HOST=rabbitmq \
  -e RABBITMQ_PORT=5672 \
  -e RABBITMQ_USER=guest \
  -e RABBITMQ_PASSWORD=guest \
  -e RABBITMQ_VHOST=/ \
  -e REDIS_HOST=redis \
  -e REDIS_PORT=6379 \
  -e REDIS_PASSWORD= \
  -e REDIS_DB=0 \
  -v psbc-shared-data:/app/data \
  -v psbc-shared-output:/app/output \
  -v psbc-shared-temp:/app/temp \
  -v psbc-shared-logs:/app/logs \
  --restart unless-stopped \
  psbc-backend:latest

# 6. 启动 Worker
echo "[6/7] 启动 Worker..."
docker run -d \
  --name psbc-worker \
  --network psbc-network \
  -e PYTHONPATH=/app \
  -e RABBITMQ_HOST=rabbitmq \
  -e RABBITMQ_PORT=5672 \
  -e RABBITMQ_USER=guest \
  -e RABBITMQ_PASSWORD=guest \
  -e RABBITMQ_VHOST=/ \
  -e REDIS_HOST=redis \
  -e REDIS_PORT=6379 \
  -e REDIS_PASSWORD= \
  -e REDIS_DB=0 \
  -v psbc-shared-data:/app/data \
  -v psbc-shared-output:/app/output \
  -v psbc-shared-temp:/app/temp \
  --restart unless-stopped \
  psbc-backend:latest \
  celery -A app.celery.config worker --concurrency=8

# 7. 启动 Flower
echo "[7/7] 启动 Flower 监控..."
docker run -d \
  --name psbc-flower \
  --network psbc-network \
  -p 5555:5555 \
  -e CELERY_BROKER_URL=amqp://guest:guest@rabbitmq:5672// \
  -e CELERY_RESULT_BACKEND=redis://redis:6379/0 \
  --restart unless-stopped \
  mher/flower:latest

echo "========================================="
echo "所有服务启动完成！"
echo "========================================="

sleep 5

echo ""
echo "服务状态:"
docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}" | grep -E "NAMES|rabbitmq|redis|psbc-api|psbc-worker|psbc-flower"

echo ""
echo "服务访问地址:"
echo "  API 服务: http://localhost:8000"
echo "  API 文档: http://localhost:8000/docs"
echo "  RabbitMQ 管理: http://localhost:15672 (账号: guest/guest)"
echo "  Flower 监控: http://localhost:5555"

echo ""
echo "常用命令:"
echo "  查看 API 日志: docker logs -f psbc-api"
echo "  查看 Worker 日志: docker logs -f psbc-worker"
echo "  停止所有服务: docker stop psbc-api psbc-worker psbc-flower rabbitmq redis"
echo "  重启所有服务: docker restart psbc-api psbc-worker psbc-flower rabbitmq redis"

echo ""
echo "API 服务启动日志:"
sleep 2
docker logs --tail 20 psbc-api