#!/bin/bash
# =============================================================
# ArbitrageX 集成测试
# 用法: bash docs/scripts/run_integration_tests.sh
# 前置: 需要网络连接 (Arbitrum RPC)
# =============================================================

set -e
cd "$(dirname "$0")/../../backend"

echo "============================================================"
echo "  ArbitrageX 集成测试"
echo "  $(date '+%Y-%m-%d %H:%M:%S')"
echo "============================================================"
echo ""

RESULTS=()
PASS=0
FAIL=0

run_test() {
    local name="$1"
    local cmd="$2"
    local timeout_sec="${3:-60}"

    echo "--- [$name] ---"
    if timeout "$timeout_sec" bash -c "$cmd" > /tmp/integ_$$.log 2>&1; then
        echo "  PASS"
        RESULTS+=("PASS: $name")
        PASS=$((PASS + 1))
    else
        echo "  FAIL (exit code: $?)"
        tail -5 /tmp/integ_$$.log 2>/dev/null
        RESULTS+=("FAIL: $name")
        FAIL=$((FAIL + 1))
    fi
    rm -f /tmp/integ_$$.log
    echo ""
}

# 1. 编译检查
run_test "Go 编译检查" "go build ./..." 120

# 2. 执行器模块测试 (calldata + ABI)
run_test "执行器模块 (test-executor)" "go run cmd/test-executor/main.go" 60

# 3. 策略模块测试 (链上价格)
run_test "策略模块 (test-strategy)" "go run cmd/test-strategy/main.go" 120

# 4. E2E 测试
run_test "E2E 测试" \
    "go run cmd/e2e/main.go -config configs/config.yaml -skip-pairs -skip-depth" 120

echo "============================================================"
echo "  集成测试结果: $PASS passed, $FAIL failed"
echo "------------------------------------------------------------"
for r in "${RESULTS[@]}"; do
    echo "  $r"
done
echo "============================================================"

if [ $FAIL -gt 0 ]; then
    exit 1
fi
