package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Resinat/Resin/internal/netutil"
	"github.com/Resinat/Resin/internal/node"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
)

var ErrOutboundNotReady = errors.New("outbound not ready")

// PoolAccessor provides read-only access to the node pool.
type PoolAccessor interface {
	GetEntry(hash node.Hash) (*node.NodeEntry, bool)
	RangeNodes(fn func(node.Hash, *node.NodeEntry) bool)
}

// ChainedOutboundResolver provides outbounds that dial through a relay node.
type ChainedOutboundResolver interface {
	EnsureChainedOutbound(landing, relay node.Hash) (adapter.Outbound, error)
}

// Relay tag encoding: detour tags are namespaced with the relay node hash so
// that DetourDialer can resolve the relay node's outbound lazily at dial time.
const relayTagPrefix = "@resin-relay/"

// RelayTag returns the detour tag used to reference a relay node's outbound.
func RelayTag(hash node.Hash) string {
	return relayTagPrefix + hash.Hex()
}

func parseRelayTag(tag string) (node.Hash, bool) {
	raw, ok := strings.CutPrefix(tag, relayTagPrefix)
	if !ok || raw == "" {
		return node.Zero, false
	}
	h, err := node.ParseHex(raw)
	if err != nil {
		return node.Zero, false
	}
	return h, true
}

// chainKey identifies one (landing, relay) outbound chain.
type chainKey struct {
	landing node.Hash
	relay   node.Hash
}

// closeOutbound closes an outbound if it implements io.Closer.
func closeOutbound(ob adapter.Outbound) {
	if c, ok := ob.(io.Closer); ok {
		_ = c.Close()
	}
}

func buildOutboundSafely(builder OutboundBuilder, rawOptions json.RawMessage) (ob adapter.Outbound, err error) {
	defer func() {
		if r := recover(); r != nil {
			if ob != nil {
				closeOutbound(ob)
			}
			ob = nil
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return builder.Build(rawOptions)
}

// OutboundManager manages outbound lifecycle and provides unified HTTP execution.
type OutboundManager struct {
	pool    PoolAccessor
	builder OutboundBuilder

	chainMu sync.Mutex
	chains  map[chainKey]adapter.Outbound
}

func NewOutboundManager(pool PoolAccessor, builder OutboundBuilder) *OutboundManager {
	return &OutboundManager{
		pool:    pool,
		builder: builder,
		chains:  make(map[chainKey]adapter.Outbound),
	}
}

func (m *OutboundManager) isLiveEntry(hash node.Hash, entry *node.NodeEntry) bool {
	current, ok := m.pool.GetEntry(hash)
	return ok && current == entry
}

// EnsureNodeOutbound idempotently creates and stores an outbound for a node.
// Uses CompareAndSwap(nil, &wrapped) to guarantee only one goroutine's build
// result is stored. Losers discard their result (stage 6 adds io.Closer release).
func (m *OutboundManager) EnsureNodeOutbound(hash node.Hash) {
	entry, ok := m.pool.GetEntry(hash)
	if !ok {
		return
	}
	// Fast path: already has outbound.
	if entry.Outbound.Load() != nil {
		return
	}

	ob, err := buildOutboundSafely(m.builder, entry.RawOptions)
	if err != nil {
		entry.SetLastError("outbound build: " + err.Error())
		return
	}

	// Build can race with node deletion/replacement. If this entry is no longer
	// the pool's live value for the hash, discard the build result.
	if !m.isLiveEntry(hash, entry) {
		closeOutbound(ob)
		return
	}

	if !entry.Outbound.CompareAndSwap(nil, &ob) {
		// Another goroutine won the race. Close the losing build result.
		closeOutbound(ob)
		return
	}

	// Close-and-clear if the node disappeared/replaced right after CAS.
	if !m.isLiveEntry(hash, entry) {
		old := entry.Outbound.Swap(nil)
		if old != nil {
			closeOutbound(*old)
		}
	}
}

// RemoveNodeOutbound clears a node's outbound reference and drops any chained
// outbounds that use the node as landing or relay.
// Accepts the entry directly because the node may already be deleted from the pool
// (RemoveNodeFromSub deletes before firing onNodeRemoved callback).
func (m *OutboundManager) RemoveNodeOutbound(entry *node.NodeEntry) {
	if entry == nil {
		return
	}
	old := entry.Outbound.Swap(nil)
	if old != nil {
		closeOutbound(*old)
	}
	m.removeChainsFor(entry.Hash)
}

// EnsureChainedOutbound returns (building once and caching) the landing node's
// outbound that dials through the relay node. The relay node's own outbound must
// be ready; this is attempted via EnsureNodeOutbound before failing.
func (m *OutboundManager) EnsureChainedOutbound(landing, relay node.Hash) (adapter.Outbound, error) {
	if landing == relay {
		return nil, errors.New("relay node must differ from landing node")
	}
	key := chainKey{landing: landing, relay: relay}

	m.chainMu.Lock()
	if ob, ok := m.chains[key]; ok {
		m.chainMu.Unlock()
		return ob, nil
	}
	m.chainMu.Unlock()

	landingEntry, ok := m.pool.GetEntry(landing)
	if !ok {
		return nil, errors.New("landing node not found")
	}
	relayEntry, ok := m.pool.GetEntry(relay)
	if !ok {
		return nil, errors.New("relay node not found")
	}
	if relayEntry.Outbound.Load() == nil {
		m.EnsureNodeOutbound(relay)
		if relayEntry.Outbound.Load() == nil {
			return nil, ErrOutboundNotReady
		}
	}

	chainedBuilder, ok := m.builder.(ChainedOutboundBuilder)
	if !ok {
		return nil, errors.New("outbound builder does not support relay chains")
	}
	ob, err := buildChainedOutboundSafely(chainedBuilder, landingEntry.RawOptions, RelayTag(relay))
	if err != nil {
		return nil, err
	}

	m.chainMu.Lock()
	if existing, ok := m.chains[key]; ok {
		m.chainMu.Unlock()
		closeOutbound(ob)
		return existing, nil
	}
	m.chains[key] = ob
	m.chainMu.Unlock()
	return ob, nil
}

func (m *OutboundManager) removeChainsFor(hash node.Hash) {
	m.chainMu.Lock()
	var closed []adapter.Outbound
	for key, ob := range m.chains {
		if key.landing == hash || key.relay == hash {
			delete(m.chains, key)
			closed = append(closed, ob)
		}
	}
	m.chainMu.Unlock()
	for _, ob := range closed {
		closeOutbound(ob)
	}
}

func buildChainedOutboundSafely(builder ChainedOutboundBuilder, rawOptions json.RawMessage, detourTag string) (ob adapter.Outbound, err error) {
	defer func() {
		if r := recover(); r != nil {
			if ob != nil {
				closeOutbound(ob)
			}
			ob = nil
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return builder.BuildChained(rawOptions, detourTag)
}

// WarmupAll iterates all nodes in the pool and ensures each has an outbound.
// Called once after bootstrap to avoid ErrOutboundNotReady on restart.
func (m *OutboundManager) WarmupAll() {
	m.pool.RangeNodes(func(h node.Hash, _ *node.NodeEntry) bool {
		m.EnsureNodeOutbound(h)
		return true
	})
}

// Fetch executes HTTP request using the node's outbound.
// Returns ErrOutboundNotReady if the node's outbound is not yet initialized.
// ctx controls timeout/cancellation.
func (m *OutboundManager) Fetch(ctx context.Context, hash node.Hash, url string) ([]byte, time.Duration, error) {
	return m.FetchWithUserAgent(ctx, hash, url, "")
}

// FetchWithUserAgent executes HTTP request using the node's outbound and
// applies the given User-Agent if non-empty.
func (m *OutboundManager) FetchWithUserAgent(
	ctx context.Context,
	hash node.Hash,
	url string,
	userAgent string,
) ([]byte, time.Duration, error) {
	entry, ok := m.pool.GetEntry(hash)
	if !ok {
		return nil, 0, errors.New("node not found")
	}
	outboundPtr := entry.Outbound.Load() // *adapter.Outbound
	if outboundPtr == nil {
		return nil, 0, ErrOutboundNotReady
	}
	return netutil.HTTPGetViaOutbound(ctx, *outboundPtr, url, netutil.OutboundHTTPOptions{
		RequireStatusOK: true,
		UserAgent:       userAgent,
	})
}

// ---------------------------------------------------------------------------
// adapter.OutboundManager — minimal implementation so sing-box DetourDialer
// can lazily resolve relay detour tags back to node outbounds at dial time.
// ---------------------------------------------------------------------------

var _ adapter.OutboundManager = (*OutboundManager)(nil)

func (m *OutboundManager) Outbounds() []adapter.Outbound {
	return nil
}

func (m *OutboundManager) Outbound(tag string) (adapter.Outbound, bool) {
	hash, ok := parseRelayTag(tag)
	if !ok {
		return nil, false
	}
	entry, ok := m.pool.GetEntry(hash)
	if !ok {
		return nil, false
	}
	ptr := entry.Outbound.Load()
	if ptr == nil {
		return nil, false
	}
	return *ptr, true
}

func (m *OutboundManager) Default() adapter.Outbound {
	return nil
}

func (m *OutboundManager) Remove(tag string) error {
	return fmt.Errorf("outbound manager: remove not supported for %q", tag)
}

func (m *OutboundManager) Create(_ context.Context, _ adapter.Router, _ log.ContextLogger, _ string, _ string, _ any) error {
	return errors.New("outbound manager: dynamic create not supported")
}

func (m *OutboundManager) Start(_ adapter.StartStage) error {
	return nil
}

func (m *OutboundManager) Close() error {
	m.chainMu.Lock()
	chains := m.chains
	m.chains = make(map[chainKey]adapter.Outbound)
	m.chainMu.Unlock()
	for _, ob := range chains {
		closeOutbound(ob)
	}
	return nil
}
