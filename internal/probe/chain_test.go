package probe

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/Resinat/Resin/internal/node"
)

// fakeChainRecorder records chain probe write-backs.
type fakeChainRecorder struct {
	mu           sync.Mutex
	egressCalls  int
	latencyCalls int
	success      bool
	egressIP     netip.Addr
	updated      int
	egressDue    bool
	latencyDue   bool
}

func (f *fakeChainRecorder) RecordChainResult(_ node.Hash, _ node.Hash, success bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.success = success
}

func (f *fakeChainRecorder) RecordChainLatency(_ node.Hash, _ node.Hash, _ string, _ *time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latencyCalls++
}

func (f *fakeChainRecorder) SetChainEgressIP(_ node.Hash, _ node.Hash, ip *netip.Addr, _ *string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.egressCalls++
	if ip != nil {
		f.egressIP = *ip
	}
}

func (f *fakeChainRecorder) ChainEgressDue(_ node.Hash, _ node.Hash, _ time.Duration) bool {
	return f.egressDue
}

func (f *fakeChainRecorder) ChainLatencyDue(_ node.Hash, _ node.Hash, _ time.Duration) bool {
	return f.latencyDue
}

func (f *fakeChainRecorder) ChainStateUpdated(_ node.Hash, _ node.Hash) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updated++
}

func newChainProbeManager(rec *fakeChainRecorder) *ProbeManager {
	return NewProbeManager(ProbeConfig{
		Concurrency: 2,
		RelayChainFetcher: func(relay, landing node.Hash, url string) ([]byte, time.Duration, error) {
			if url == egressTraceURL {
				return []byte("fl=123\nip=203.0.113.7\nloc=SG\nts=123"), 10 * time.Millisecond, nil
			}
			return []byte("ok"), 12 * time.Millisecond, nil
		},
		RelayChainRecorder: rec,
	})
}

// TestPerformRelayEgressProbe_RecordsChainState verifies a chain egress probe
// writes chain state (and never touches the node).
func TestPerformRelayEgressProbe_RecordsChainState(t *testing.T) {
	rec := &fakeChainRecorder{}
	m := newChainProbeManager(rec)
	relay := node.Hash{1}
	landing := node.Hash{2}

	m.performRelayEgressProbe(relay, landing)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !rec.success {
		t.Fatal("expected chain probe success")
	}
	if rec.egressCalls != 1 || rec.egressIP != netip.MustParseAddr("203.0.113.7") {
		t.Fatalf("unexpected egress write-back: calls=%d ip=%s", rec.egressCalls, rec.egressIP)
	}
	if rec.latencyCalls != 1 {
		t.Fatalf("expected 1 latency record, got %d", rec.latencyCalls)
	}
	if rec.updated != 1 {
		t.Fatalf("expected 1 ChainStateUpdated, got %d", rec.updated)
	}
}

func TestPerformRelayEgressProbe_FailureRecordsFailure(t *testing.T) {
	rec := &fakeChainRecorder{}
	m := NewProbeManager(ProbeConfig{
		Concurrency: 2,
		RelayChainFetcher: func(_, _ node.Hash, _ string) ([]byte, time.Duration, error) {
			return nil, 0, context.DeadlineExceeded
		},
		RelayChainRecorder: rec,
	})

	m.performRelayEgressProbe(node.Hash{1}, node.Hash{2})
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.success {
		t.Fatal("expected chain probe failure")
	}
	if rec.egressCalls != 1 || rec.egressIP != (netip.Addr{}) {
		t.Fatalf("expected egress IP cleared on failure, calls=%d ip=%s", rec.egressCalls, rec.egressIP)
	}
}

func TestPerformRelayLatencyProbe_RecordsChainState(t *testing.T) {
	rec := &fakeChainRecorder{}
	m := newChainProbeManager(rec)

	m.performRelayLatencyProbe(node.Hash{1}, node.Hash{2})
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !rec.success || rec.latencyCalls != 1 {
		t.Fatalf("unexpected latency probe result: success=%v latencyCalls=%d", rec.success, rec.latencyCalls)
	}
}
