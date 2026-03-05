#!/bin/bash
# =============================================================
# ArbitrageX Go 单元测试
# 用法: bash docs/scripts/run_unit_tests.sh
# =============================================================

set -e
cd "$(dirname "$0")/../../backend"

echo "============================================================"
echo "  ArbitrageX Go 单元测试 (94 tests, 4 packages)"
echo "  $(date '+%Y-%m-%d %H:%M:%S')"
echo "============================================================"
echo ""

PACKAGES=(
    "./internal/strategy/..."
    "./internal/executor/..."
    "./pkg/cache/..."
    "./internal/cexdex/..."
)

TOTAL_PASS=0
TOTAL_FAIL=0

for pkg in "${PACKAGES[@]}"; do
    echo "--- Testing: $pkg ---"
    if go test -race -count=1 -v "$pkg" 2>&1 | tee /tmp/test_output_$$.log; then
        PASS_COUNT=$(grep -c "^--- PASS" /tmp/test_output_$$.log || true)
        TOTAL_PASS=$((TOTAL_PASS + PASS_COUNT))
        echo "  PASS: $PASS_COUNT tests"
    else
        FAIL_COUNT=$(grep -c "^--- FAIL" /tmp/test_output_$$.log || true)
        TOTAL_FAIL=$((TOTAL_FAIL + FAIL_COUNT))
        echo "  FAIL: $FAIL_COUNT tests"
    fi
    echo ""
done

rm -f /tmp/test_output_$$.log

echo "============================================================"
echo "  Results: $TOTAL_PASS passed, $TOTAL_FAIL failed"
echo "============================================================"

if [ $TOTAL_FAIL -gt 0 ]; then
    echo "  FAILED"
    exit 1
else
    echo "  ALL PASS"
    exit 0
fi
