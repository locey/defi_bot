#!/bin/bash
# =============================================================
# ArbitrageX Solidity 合约测试
# 用法: bash docs/scripts/run_contract_tests.sh
# =============================================================

set -e
cd "$(dirname "$0")/../../contract"

echo "============================================================"
echo "  ArbitrageX Solidity 合约测试 (30 tests)"
echo "  $(date '+%Y-%m-%d %H:%M:%S')"
echo "============================================================"
echo ""

# 编译
echo "--- Compiling contracts ---"
npx hardhat compile
echo ""

# 测试
echo "--- Running tests ---"
npx hardhat test test/SpotArbitrage.test.js

echo ""
echo "============================================================"
echo "  ALL PASS"
echo "============================================================"
