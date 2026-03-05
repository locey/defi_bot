package strategy

import (
	"math/big"
	"testing"

	"github.com/defi-bot/backend/pkg/cache"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	weth = common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1")
	usdc = common.HexToAddress("0xaf88d065e77c8cC2239327C5EDb3A432268e5831")
	arb  = common.HexToAddress("0x912CE59144191C1D603B5E70EFF7Dd3B0Ad2E89F")
)

func makeCachePool(addr string, t0, t1 common.Address, dex, protocol string) *cache.PoolPrice {
	return &cache.PoolPrice{
		PoolAddress: addr,
		Token0:      t0,
		Token1:      t1,
		DexName:     dex,
		Protocol:    protocol,
		Reserve0:    big.NewInt(1e18),
		Reserve1:    big.NewInt(2e18),
		Price:       2.0,
		Decimals0:   18,
		Decimals1:   18,
	}
}

func TestBuildTokenGraph_BidirectionalEdges(t *testing.T) {
	detector := NewArbitrageDetector(nil, nil, nil)

	pools := []*cache.PoolPrice{
		makeCachePool("p1", weth, usdc, "Sushi", "uniswap_v2"),
	}

	graph := detector.buildTokenGraph(pools)

	// WETH should have edge to USDC
	edges, ok := graph[weth]
	require.True(t, ok)
	assert.Len(t, edges, 1)
	assert.Equal(t, usdc, edges[0].ToToken)

	// USDC should have edge to WETH
	edges2, ok := graph[usdc]
	require.True(t, ok)
	assert.Len(t, edges2, 1)
	assert.Equal(t, weth, edges2[0].ToToken)
}

func TestFindCycles_3Hop(t *testing.T) {
	detector := NewArbitrageDetector(nil, nil, nil)

	pools := []*cache.PoolPrice{
		makeCachePool("p1", weth, usdc, "Sushi", "uniswap_v2"),
		makeCachePool("p2", usdc, arb, "Uni", "uniswap_v3"),
		makeCachePool("p3", arb, weth, "Camelot", "uniswap_v2"),
	}

	graph := detector.buildTokenGraph(pools)
	cycles := detector.findCycles(graph, weth, 3)

	assert.True(t, len(cycles) > 0, "should find at least one 3-hop cycle: WETH→USDC→ARB→WETH")
	for _, c := range cycles {
		assert.Equal(t, weth, c.Tokens[0], "cycle should start at WETH")
		assert.Len(t, c.Pools, 3, "3-hop cycle should have 3 pools")
	}
}

func TestFindCycles_4Hop(t *testing.T) {
	// WETH→USDC→ARB→DAI→WETH (4-hop)
	dai := common.HexToAddress("0xDA10009cBd5D07dd0CeCc66161FC93D7c9000da1")
	detector := NewArbitrageDetector(nil, nil, &DetectorConfig{
		MinPathLength: 3,
		MaxPathLength: 4,
		ProfitThresholds: map[int]float64{
			3: 0.005,
			4: 0.008,
		},
		MaxConcurrentCalc:     10,
		OpportunityBufferSize: 100,
	})

	pools := []*cache.PoolPrice{
		makeCachePool("p1", weth, usdc, "Sushi", "uniswap_v2"),
		makeCachePool("p2", usdc, arb, "Uni", "uniswap_v3"),
		makeCachePool("p3", arb, dai, "Camelot", "uniswap_v2"),
		makeCachePool("p4", dai, weth, "Sushi", "uniswap_v2"),
	}

	graph := detector.buildTokenGraph(pools)
	cycles := detector.findCycles(graph, weth, 4)

	assert.True(t, len(cycles) > 0, "should find at least one 4-hop cycle")
	for _, c := range cycles {
		assert.Len(t, c.Pools, 4)
	}
}

func TestFindCycles_NoDuplicatePool(t *testing.T) {
	detector := NewArbitrageDetector(nil, nil, nil)

	pools := []*cache.PoolPrice{
		makeCachePool("p1", weth, usdc, "Sushi", "uniswap_v2"),
		makeCachePool("p2", usdc, arb, "Uni", "uniswap_v3"),
		makeCachePool("p3", arb, weth, "Camelot", "uniswap_v2"),
	}

	graph := detector.buildTokenGraph(pools)
	cycles := detector.findCycles(graph, weth, 3)

	for _, c := range cycles {
		poolSet := make(map[string]bool)
		for _, p := range c.Pools {
			assert.False(t, poolSet[p], "pool %s used twice in cycle", p)
			poolSet[p] = true
		}
	}
}

func TestPrecomputePaths_IndexByPool(t *testing.T) {
	pc := cache.NewPriceCache(0.01, 100)
	detector := NewArbitrageDetector(pc, nil, nil)

	pools := []*cache.PoolPrice{
		makeCachePool("p1", weth, usdc, "Sushi", "uniswap_v2"),
		makeCachePool("p2", usdc, arb, "Uni", "uniswap_v3"),
		makeCachePool("p3", arb, weth, "Camelot", "uniswap_v2"),
	}

	detector.PrecomputePaths(pools, []common.Address{weth})

	assert.True(t, len(detector.paths) > 0)
	// pathsByPool should index paths by pool address
	for _, path := range detector.paths {
		for _, poolAddr := range path.Pools {
			assert.Contains(t, detector.pathsByPool, poolAddr)
		}
	}
}

func TestCalculateSwapOutput_V2AMM(t *testing.T) {
	// x * y = k, amountOut = reserveOut * amountIn * (1-fee) / (reserveIn + amountIn * (1-fee))
	amountIn := big.NewFloat(1e18)      // 1 token
	reserveIn := big.NewFloat(100e18)   // 100 tokens
	reserveOut := big.NewFloat(200e18)  // 200 tokens

	out := calculateSwapOutput(amountIn, reserveIn, reserveOut, 30) // 0.3% fee
	require.NotNil(t, out)

	outFloat, _ := out.Float64()
	// Without fee: out = 200 * 1 / (100 + 1) ≈ 1.98
	// With 0.3% fee: slightly less
	assert.True(t, outFloat > 0)
	assert.True(t, outFloat < 200e18, "output must be less than reserveOut")
}

func TestCalculateOptimalAmount_V2Pool(t *testing.T) {
	reserve0, _ := new(big.Int).SetString("100000000000000000000", 10) // 100e18
	reserve1, _ := new(big.Int).SetString("250000000000", 10)          // 250000e6
	pool := &cache.PoolPrice{
		Token0:    weth,
		Token1:    usdc,
		Reserve0:  reserve0,
		Reserve1:  reserve1,
		Decimals0: 18,
		Decimals1: 6,
	}

	amount := calculateOptimalAmount([]*cache.PoolPrice{pool}, []common.Address{weth, usdc})
	assert.True(t, amount.Sign() > 0)

	// Should be ~1% of reserve
	expected := new(big.Int).Div(pool.Reserve0, big.NewInt(100))
	assert.Equal(t, expected.String(), amount.String())
}

func TestCalculatePathConfidence(t *testing.T) {
	path3 := &ArbitragePath{PathLength: 3}
	path4 := &ArbitragePath{PathLength: 4}

	c3 := calculatePathConfidence(path3, 0.02) // 2% profit
	c4 := calculatePathConfidence(path4, 0.02)

	assert.True(t, c3 > c4, "3-hop should have higher confidence than 4-hop")
	assert.True(t, c3 > 0 && c3 <= 1.0)
	assert.True(t, c4 > 0 && c4 <= 1.0)

	// Very high profit rate should lower confidence (suspicious)
	cHigh := calculatePathConfidence(path3, 0.6)
	assert.True(t, cHigh < c3, "60% profit rate should lower confidence")
}

func TestCalculatePathConfidence_WithHistory(t *testing.T) {
	path := &ArbitragePath{
		PathLength:   3,
		SuccessCount: 8,
		FailCount:    2,
	}
	c := calculatePathConfidence(path, 0.02)

	pathNoHistory := &ArbitragePath{PathLength: 3}
	cNoHistory := calculatePathConfidence(pathNoHistory, 0.02)

	// With 80% success rate, confidence should be boosted
	assert.True(t, c > cNoHistory*0.8, "good history should boost confidence")
}
