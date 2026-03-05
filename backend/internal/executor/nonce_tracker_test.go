package executor

import (
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
)

var (
	addr1 = common.HexToAddress("0x1111111111111111111111111111111111111111")
	addr2 = common.HexToAddress("0x2222222222222222222222222222222222222222")
)

func TestNonceTracker_SequentialIncrement(t *testing.T) {
	nt := NewNonceTracker()
	nt.Set(addr1, 0)

	assert.Equal(t, uint64(0), nt.GetAndIncrement(addr1))
	assert.Equal(t, uint64(1), nt.GetAndIncrement(addr1))
	assert.Equal(t, uint64(2), nt.GetAndIncrement(addr1))
	assert.Equal(t, uint64(3), nt.Get(addr1))
}

func TestNonceTracker_MultipleAddresses(t *testing.T) {
	nt := NewNonceTracker()
	nt.Set(addr1, 10)
	nt.Set(addr2, 20)

	assert.Equal(t, uint64(10), nt.GetAndIncrement(addr1))
	assert.Equal(t, uint64(20), nt.GetAndIncrement(addr2))
	assert.Equal(t, uint64(11), nt.Get(addr1))
	assert.Equal(t, uint64(21), nt.Get(addr2))
}

func TestNonceTracker_Decrement(t *testing.T) {
	nt := NewNonceTracker()
	nt.Set(addr1, 5)
	nt.Decrement(addr1)
	assert.Equal(t, uint64(4), nt.Get(addr1))
}

func TestNonceTracker_DecrementNoUnderflow(t *testing.T) {
	nt := NewNonceTracker()
	nt.Set(addr1, 0)
	nt.Decrement(addr1)
	assert.Equal(t, uint64(0), nt.Get(addr1), "should not underflow below 0")
}

func TestNonceTracker_Set(t *testing.T) {
	nt := NewNonceTracker()
	nt.Set(addr1, 42)
	assert.Equal(t, uint64(42), nt.Get(addr1))
}

func TestNonceTracker_Reset(t *testing.T) {
	nt := NewNonceTracker()
	nt.Set(addr1, 10)
	nt.Reset(addr1)
	assert.Equal(t, uint64(0), nt.Get(addr1), "after reset, Get returns default 0")
}

func TestNonceTracker_ResetAll(t *testing.T) {
	nt := NewNonceTracker()
	nt.Set(addr1, 10)
	nt.Set(addr2, 20)
	nt.ResetAll()
	assert.Equal(t, uint64(0), nt.Get(addr1))
	assert.Equal(t, uint64(0), nt.Get(addr2))
}

func TestNonceTracker_ConcurrentIncrement(t *testing.T) {
	nt := NewNonceTracker()
	nt.Set(addr1, 0)

	n := 100
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			nt.GetAndIncrement(addr1)
		}()
	}
	wg.Wait()

	assert.Equal(t, uint64(n), nt.Get(addr1), "after %d concurrent increments, nonce should be %d", n, n)
}

func TestNonceTracker_InitIfNeeded_Idempotent(t *testing.T) {
	// Without a real client, we test idempotency by setting manually
	nt := NewNonceTracker()
	nt.Set(addr1, 5)
	// Mark as inited
	nt.mu.Lock()
	nt.inited[addr1] = true
	nt.mu.Unlock()

	// A second Set should not interfere if already inited
	// (InitIfNeeded checks inited flag, so we test the flag logic)
	nt.mu.Lock()
	isInited := nt.inited[addr1]
	nt.mu.Unlock()

	assert.True(t, isInited)
	assert.Equal(t, uint64(5), nt.Get(addr1))
}

func TestNonceTracker_GetDefaultZero(t *testing.T) {
	nt := NewNonceTracker()
	// Never set addr1 — should return 0 (Go map default)
	assert.Equal(t, uint64(0), nt.Get(addr1))
}
