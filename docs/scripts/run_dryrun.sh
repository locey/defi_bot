#!/bin/bash
# =============================================================
# ArbitrageX Dry-Run 测试脚本
# 用法: bash docs/scripts/run_dryrun.sh [duration_seconds]
# 默认运行 300 秒 (5 分钟), 然后输出统计报告
# =============================================================

cd "$(dirname "$0")/../../backend"

DURATION=${1:-300}
LOG_FILE="/tmp/defi-dryrun-$(date +%Y%m%d_%H%M%S).log"

echo "============================================================"
echo "  ArbitrageX Dry-Run 测试"
echo "  时长: ${DURATION}s"
echo "  日志: $LOG_FILE"
echo "  $(date '+%Y-%m-%d %H:%M:%S')"
echo "============================================================"
echo ""

# 检查 config 是否是 dry-run 模式
if ! grep -q "dry_run: true" configs/config.yaml; then
    echo "WARNING: config.yaml 中 dry_run 不是 true!"
    echo "请先修改 configs/config.yaml:"
    echo "  enable_execution: false"
    echo "  dry_run: true"
    exit 1
fi

# 编译
echo "编译中..."
go build -o bin/server cmd/server/main.go || { echo "编译失败"; exit 1; }

# 启动服务器
echo "启动 dry-run 服务器..."
nohup ./bin/server -config configs/config.yaml > "$LOG_FILE" 2>&1 &
PID=$!
echo "PID: $PID"
echo ""

# 等待指定时长
echo "运行中 (${DURATION}s)..."
sleep "$DURATION"

# 停止服务器
kill $PID 2>/dev/null
sleep 2

# 生成统计报告
echo ""
echo "============================================================"
echo "  Dry-Run 统计报告"
echo "============================================================"

echo "ArbitrageDetector 发现机会: $(grep -c 'ArbitrageDetector: Found opportunity' "$LOG_FILE" 2>/dev/null || echo 0)"
echo "Scheduler 级别机会:        $(grep -c 'Opportunity found' "$LOG_FILE" 2>/dev/null || echo 0)"
echo "SpreadScanner 跨DEX发现:   $(grep -c 'SpreadScanner: Auto-discovered' "$LOG_FILE" 2>/dev/null || echo 0)"
echo "CEX-DEX 价格比较:          $(grep -c '价格比较' "$LOG_FILE" 2>/dev/null || echo 0)"
echo "eth_call 模拟尝试:         $(grep -c 'eth_call' "$LOG_FILE" 2>/dev/null || echo 0)"
echo "Errors:                    $(grep -c 'ERR' "$LOG_FILE" 2>/dev/null || echo 0)"
echo "Panics:                    $(grep -c 'panic' "$LOG_FILE" 2>/dev/null || echo 0)"
echo ""
echo "日志文件: $LOG_FILE"
echo "============================================================"

# 判断成功标准
ERRORS=$(grep -c 'panic' "$LOG_FILE" 2>/dev/null || echo 0)
POOLS=$(grep -c 'PriceCache' "$LOG_FILE" 2>/dev/null || echo 0)

if [ "$ERRORS" -gt 0 ]; then
    echo "  FAIL: 检测到 panic"
    exit 1
elif [ "$POOLS" -eq 0 ]; then
    echo "  FAIL: PriceCache 未初始化"
    exit 1
else
    echo "  PASS: Dry-run 正常完成"
    exit 0
fi
