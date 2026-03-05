#!/bin/bash
# =============================================================
# ArbitrageX 全量测试脚本
# 用法: bash docs/scripts/run_all_tests.sh
# =============================================================

set -e
ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"

echo "============================================================"
echo "  ArbitrageX 全量测试 (124 tests)"
echo "  $(date '+%Y-%m-%d %H:%M:%S')"
echo "============================================================"
echo ""

PASS=0
FAIL=0

# ---- Phase 1: Go 单元测试 ----
echo "=== Phase 1: Go 单元测试 (94 tests) ==="
cd "$ROOT_DIR/backend"

if go test -race -count=1 ./internal/strategy/... ./internal/executor/... ./pkg/cache/... ./internal/cexdex/... 2>&1; then
    echo "  Go tests: PASS"
    PASS=$((PASS + 1))
else
    echo "  Go tests: FAIL"
    FAIL=$((FAIL + 1))
fi
echo ""

# ---- Phase 2: Solidity 合约测试 ----
echo "=== Phase 2: Solidity 合约测试 (30 tests) ==="
cd "$ROOT_DIR/contract"

if npx hardhat test test/SpotArbitrage.test.js 2>&1; then
    echo "  Solidity tests: PASS"
    PASS=$((PASS + 1))
else
    echo "  Solidity tests: FAIL"
    FAIL=$((FAIL + 1))
fi
echo ""

# ---- Phase 3: 编译检查 ----
echo "=== Phase 3: 编译检查 ==="
cd "$ROOT_DIR/backend"
if go build ./... 2>&1; then
    echo "  Go build: PASS"
    PASS=$((PASS + 1))
else
    echo "  Go build: FAIL"
    FAIL=$((FAIL + 1))
fi

cd "$ROOT_DIR/contract"
if npx hardhat compile 2>&1 | tail -1; then
    echo "  Solidity compile: PASS"
    PASS=$((PASS + 1))
else
    echo "  Solidity compile: FAIL"
    FAIL=$((FAIL + 1))
fi
echo ""

# ---- 结果 ----
echo "============================================================"
echo "  Results: $PASS passed, $FAIL failed"
echo "============================================================"

if [ $FAIL -gt 0 ]; then
    exit 1
else
    echo "  ALL PASS"
    exit 0
fi
