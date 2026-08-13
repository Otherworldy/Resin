package probe

import (
	"testing"
	"time"

	"github.com/Resinat/Resin/internal/node"
	"github.com/Resinat/Resin/internal/testutil"
	"github.com/Resinat/Resin/internal/topology"
)

// TestScanLatency_PlatformOverride verifies that a platform-level probe
// override (disabled / shorter interval) drives the periodic latency scan.
func TestScanLatency_PlatformOverride(t *testing.T) {
	pool := topology.NewGlobalNodePool(topology.PoolConfig{
		MaxLatencyTableEntries: 16,
		MaxConsecutiveFailures: func() int { return 3 },
	})

	// Node A: override disables probing -> must never be enqueued.
	hashA := node.HashFromRawOptions([]byte(`{"type":"a"}`))
	pool.AddNodeFromSub(hashA, []byte(`{"type":"a"}`), "sub1")
	entryA, _ := pool.GetEntry(hashA)
	storeOutbound(entryA)

	// Node B: override shrinks the interval to 1h; last attempt 2h ago -> due.
	hashB := node.HashFromRawOptions([]byte(`{"type":"b"}`))
	pool.AddNodeFromSub(hashB, []byte(`{"type":"b"}`), "sub1")
	entryB, _ := pool.GetEntry(hashB)
	storeOutbound(entryB)
	entryB.LastLatencyProbeAttempt.Store(time.Now().Add(-2 * time.Hour).UnixNano())

	// Node C: no override; last attempt 30m ago with global interval 1h -> not due.
	hashC := node.HashFromRawOptions([]byte(`{"type":"c"}`))
	pool.AddNodeFromSub(hashC, []byte(`{"type":"c"}`), "sub1")
	entryC, _ := pool.GetEntry(hashC)
	storeOutbound(entryC)
	entryC.LastLatencyProbeAttempt.Store(time.Now().Add(-30 * time.Minute).UnixNano())

	probed := map[node.Hash]bool{}
	mgr := NewProbeManager(ProbeConfig{
		Pool:        pool,
		Fetcher:     func(h node.Hash, _ string) ([]byte, time.Duration, error) { return nil, 0, nil },
		Concurrency: 1,
		MaxLatencyTestInterval: func() time.Duration { return time.Hour },
		ResolvePlatformProbe: func(h node.Hash) PlatformProbeConfig {
			switch h {
			case hashA:
				return PlatformProbeConfig{Disabled: true}
			case hashB:
				return PlatformProbeConfig{LatencyInterval: time.Hour}
			default:
				return PlatformProbeConfig{}
			}
		},
	})
	mgr.taskQueue = newProbeTaskQueue(64, 64, nil)
	mgr.scanLatency()
	// Drain without blocking: Dequeue waits forever on an empty queue.
	for mgr.taskQueue.high.len() > 0 || mgr.taskQueue.normal.len() > 0 {
		task, ok := mgr.taskQueue.Dequeue()
		if !ok {
			break
		}
		probed[task.key.hash] = true
		if len(probed) > 10 {
			t.Fatal("too many probes enqueued")
		}
	}

	if probed[hashA] {
		t.Fatal("disabled node A should not be probed")
	}
	if !probed[hashB] {
		t.Fatal("node B with shorter override interval should be probed")
	}
	if probed[hashC] {
		t.Fatal("node C without override should not be due yet")
	}
	_ = testutil.NewNoopOutbound
}
