#!/bin/bash
# =============================================================
# ArbitrageX Phase 3 主网监控脚本
# 用法: bash docs/scripts/monitor_phase3.sh [log_file]
# =============================================================

LOG_FILE="${1:-/tmp/defi-phase3.log}"

if [ ! -f "$LOG_FILE" ]; then
    echo "日志文件不存在: $LOG_FILE"
    exit 1
fi

echo "============================================================"
echo "  ArbitrageX Phase 3 监控报告"
echo "  日志: $LOG_FILE"
echo "  $(date '+%Y-%m-%d %H:%M:%S')"
echo "============================================================"
echo ""

echo "--- Pipeline 指标 ---"
echo "ArbitrageDetector 机会: $(grep -c 'ArbitrageDetector: Found opportunity' "$LOG_FILE" 2>/dev/null || echo 0)"
echo "eth_call 尝试:          $(grep -c 'eth_call' "$LOG_FILE" 2>/dev/null || echo 0)"
echo "eth_call reverted:      $(grep -c 'eth_call reverted' "$LOG_FILE" 2>/dev/null || echo 0)"
echo "eth_call passed:        $(grep -c 'eth_call.*pass\|simulation' "$LOG_FILE" 2>/dev/null || echo 0)"
echo "交易发送:               $(grep -c 'SendTransaction\|tx.*submitted' "$LOG_FILE" 2>/dev/null || echo 0)"
echo "SpreadScanner:          $(grep -c 'SpreadScanner.*Auto' "$LOG_FILE" 2>/dev/null || echo 0)"
echo "CEX-DEX 比较:           $(grep -c '价格比较' "$LOG_FILE" 2>/dev/null || echo 0)"
echo ""

echo "--- 安全指标 ---"
echo "Errors:                 $(grep -c 'ERR' "$LOG_FILE" 2>/dev/null || echo 0)"
echo "Panics:                 $(grep -c 'panic' "$LOG_FILE" 2>/dev/null || echo 0)"
echo "Nonce 错误:             $(grep -c 'nonce' "$LOG_FILE" 2>/dev/null || echo 0)"
echo "Revert:                 $(grep -c 'revert\|REVERT' "$LOG_FILE" 2>/dev/null || echo 0)"
echo ""

echo "--- 最近 eth_call 结果 ---"
grep 'eth_call' "$LOG_FILE" | tail -5
echo ""

echo "--- 最近交易 ---"
grep -i 'tx.*hash\|交易.*发送\|execute.*success\|execute.*fail' "$LOG_FILE" | tail -5
echo ""

echo "============================================================"
