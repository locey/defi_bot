package web3

// 这个文件包含 Uniswap V2 Pair 合约相关的辅助方法
// ABI 定义和主要实现已移至 contracts.go

// GetPairReserves 获取交易对储备量（调用 contracts.go 中的实现）
func (c *Client) GetPairReserves(pairAddress string) (*PairReserves, error) {
	return c.GetPairReservesFromContract(pairAddress)
}
