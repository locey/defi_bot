# DeFi 套利机器人 Makefile

.PHONY: help build run test clean docker-up docker-down migrate seed

# 默认目标
.DEFAULT_GOAL := help

# 配置文件
CONFIG_FILE ?= configs/config.test.yaml

# 帮助信息
help:
	@echo "DeFi 套利机器人 - 可用命令："
	@echo ""
	@echo "  make build         - 编译项目"
	@echo "  make run           - 运行服务（使用测试配置）"
	@echo "  make test          - 运行测试"
	@echo "  make clean         - 清理编译文件"
	@echo ""
	@echo "  make docker-up     - 启动 Docker 服务（生产）"
	@echo "  make docker-dev    - 启动 Docker 服务（开发）"
	@echo "  make docker-down   - 停止 Docker 服务"
	@echo "  make docker-logs   - 查看 Docker 日志"
	@echo ""
	@echo "  make migrate       - 执行数据库迁移"
	@echo "  make seed          - 初始化种子数据"
	@echo "  make migrate-seed  - 迁移 + 种子数据"
	@echo ""
	@echo "  make db-connect    - 连接到数据库"
	@echo "  make redis-cli     - 连接到 Redis"
	@echo ""

# 编译项目
build:
	@echo "编译项目..."
	go build -o bin/server cmd/server/main.go
	@echo "✅ 编译完成: bin/server"

# 运行服务
run: build
	@echo "启动服务..."
	./bin/server -config $(CONFIG_FILE)

# 运行测试
test:
	@echo "运行测试..."
	go test -v ./...

# 清理编译文件
clean:
	@echo "清理编译文件..."
	rm -rf bin/
	go clean
	@echo "✅ 清理完成"

# Docker - 启动生产环境
docker-up:
	@echo "启动 Docker 服务（生产）..."
	docker-compose up -d
	@echo "✅ 服务已启动"
	@echo "查看日志: make docker-logs"

# Docker - 启动开发环境
docker-dev:
	@echo "启动 Docker 服务（开发）..."
	docker-compose -f docker-compose.dev.yml up -d
	@echo "✅ 开发环境已启动"
	@echo "数据库: localhost:5432"
	@echo "Redis: localhost:6379"
	@echo "pgAdmin: http://localhost:5050"
	@echo "Redis Commander: http://localhost:8081"

# Docker - 停止服务
docker-down:
	@echo "停止 Docker 服务..."
	docker-compose down
	docker-compose -f docker-compose.dev.yml down
	@echo "✅ 服务已停止"

# Docker - 查看日志
docker-logs:
	docker-compose logs -f

# 数据库迁移
migrate: build
	@echo "执行数据库迁移..."
	./bin/server -config $(CONFIG_FILE) -migrate
	@echo "✅ 迁移完成"

# 初始化种子数据
seed: build
	@echo "初始化种子数据..."
	./bin/server -config $(CONFIG_FILE) -seed
	@echo "✅ 种子数据初始化完成"

# 迁移 + 种子数据
migrate-seed: build
	@echo "执行迁移和种子数据初始化..."
	./bin/server -config $(CONFIG_FILE) -migrate -seed
	@echo "✅ 完成"

# 连接到数据库
db-connect:
	@echo "连接到 PostgreSQL..."
	docker-compose exec postgres psql -U defi_user -d defi_arbitrage

# 连接到 Redis
redis-cli:
	@echo "连接到 Redis..."
	docker-compose exec redis redis-cli

# 查看 Docker 服务状态
docker-ps:
	docker-compose ps

# 重启 Docker 服务
docker-restart:
	@echo "重启 Docker 服务..."
	docker-compose restart
	@echo "✅ 服务已重启"

# 查看数据库大小
db-size:
	docker-compose exec postgres psql -U defi_user -d defi_arbitrage -c "SELECT pg_size_pretty(pg_database_size('defi_arbitrage'));"

# 清空 Redis 缓存
redis-flush:
	@echo "清空 Redis 缓存..."
	docker-compose exec redis redis-cli FLUSHALL
	@echo "✅ 缓存已清空"

# 备份数据库
db-backup:
	@echo "备份数据库..."
	docker-compose exec postgres pg_dump -U defi_user defi_arbitrage > backup_$(shell date +%Y%m%d_%H%M%S).sql
	@echo "✅ 备份完成"

# 开发环境完整初始化
dev-init: docker-dev migrate-seed
	@echo "✅ 开发环境初始化完成！"
	@echo ""
	@echo "现在可以运行: make run"
