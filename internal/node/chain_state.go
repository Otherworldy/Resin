package node

import (
	"net/netip"
	"sync/atomic"
)

// ChainState holds probe-derived health state for one relay→landing chain
// (中转→落地). It mirrors the health-relevant fields of NodeEntry but is
// scoped to a chain, so platform relay configuration never pollutes the
// node's direct (global) state.
type ChainState struct {
	EgressIP                atomic.Pointer[netip.Addr]
	EgressRegion            atomic.Pointer[string]
	LastEgressUpdate        atomic.Int64 // unix-nano of last successful egress sample
	LastLatencyProbeAttempt atomic.Int64 // unix-nano, regardless of success
	FailureCount            atomic.Int32
	CircuitOpenSince        atomic.Int64 // unix-nano; 0 = not open
	LastLatencyMs           atomic.Int64 // unix-nano of last successful latency observation
	LastLatencyDomain       atomic.Value // string; domain of last successful latency observation
	LatencyTable            *LatencyTable
}

// NewChainState creates a ChainState with the given latency table capacity.
// Pass 0 to skip latency-table initialization (tests that don't need it).
func NewChainState(maxLatencyTableEntries int) *ChainState {
	s := &ChainState{}
	if maxLatencyTableEntries > 0 {
		s.LatencyTable = NewLatencyTable(maxLatencyTableEntries)
	}
	return s
}

// GetEgressIP returns the chain's egress IP, or the zero Addr if unknown.
func (s *ChainState) GetEgressIP() netip.Addr {
	ptr := s.EgressIP.Load()
	if ptr == nil {
		return netip.Addr{}
	}
	return *ptr
}

// SetEgressIP stores the chain's egress IP. Nil clears it.
func (s *ChainState) SetEgressIP(ip *netip.Addr) {
	if ip == nil {
		s.EgressIP.Store(nil)
		return
	}
	s.EgressIP.Store(ip)
}

// GetEgressRegion returns the chain's stored region, or empty if unknown.
func (s *ChainState) GetEgressRegion() string {
	ptr := s.EgressRegion.Load()
	if ptr == nil {
		return ""
	}
	return *ptr
}

// SetEgressRegion stores the chain's explicit probe region. Nil/empty clears.
func (s *ChainState) SetEgressRegion(region *string) {
	if region == nil || *region == "" {
		s.EgressRegion.Store(nil)
		return
	}
	s.EgressRegion.Store(region)
}

// IsCircuitOpen reports whether the chain is currently circuit-broken.
func (s *ChainState) IsCircuitOpen() bool {
	return s.CircuitOpenSince.Load() != 0
}

// HasLatency reports whether the chain has at least one latency record.
func (s *ChainState) HasLatency() bool {
	return s.LatencyTable != nil && s.LatencyTable.Size() > 0
}

// IsHealthy reports whether the chain can be treated as healthy for routing:
// circuit not open and has latency samples.
func (s *ChainState) IsHealthy() bool {
	return s != nil && !s.IsCircuitOpen() && s.HasLatency()
}
