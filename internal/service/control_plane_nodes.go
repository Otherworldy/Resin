package service

import (
	"strings"
	"sync"
	"time"

	"github.com/Resinat/Resin/internal/node"
	"github.com/Resinat/Resin/internal/platform"
	"github.com/Resinat/Resin/internal/probe"
	"github.com/Resinat/Resin/internal/subscription"
)

// ------------------------------------------------------------------
// Nodes
// ------------------------------------------------------------------

// NodeFilters holds query filters for listing nodes.
type NodeFilters struct {
	PlatformID     *string
	SubscriptionID *string
	Enabled        *bool
	Region         *string
	CircuitOpen    *bool
	HasOutbound    *bool
	EgressIP       *string
	ProbedSince    *time.Time
	TagKeyword     *string
}

// ListNodes returns nodes from the pool with optional filters.
func (s *ControlPlaneService) ListNodes(filters NodeFilters) ([]NodeSummary, error) {
	var subLookup node.SubLookupFunc
	if s != nil && s.Pool != nil {
		subLookup = s.Pool.MakeSubLookup()
	}

	// If platform_id filter, get the platform view.
	var platformView map[node.Hash]struct{}
	if filters.PlatformID != nil {
		plat, ok := s.Pool.GetPlatform(*filters.PlatformID)
		if !ok {
			return nil, notFound("platform not found")
		}
		platformView = make(map[node.Hash]struct{}, plat.View().Size())
		plat.View().Range(func(h node.Hash) bool {
			platformView[h] = struct{}{}
			return true
		})
	}

	var subNodes map[node.Hash]struct{}
	if filters.SubscriptionID != nil {
		sub := s.SubMgr.Lookup(*filters.SubscriptionID)
		if sub == nil {
			return nil, notFound("subscription not found")
		}
		subNodes = make(map[node.Hash]struct{})
		sub.ManagedNodes().RangeNodes(func(h node.Hash, managed subscription.ManagedNode) bool {
			if managed.Evicted {
				return true
			}
			subNodes[h] = struct{}{}
			return true
		})
	}

	var result []NodeSummary
	appendIfMatched := func(h node.Hash, entry *node.NodeEntry) {
		if !s.nodeEntryMatchesFilters(entry, filters, subLookup) {
			return
		}
		result = append(result, s.nodeEntryToSummary(h, entry))
	}

	appendIfMatchedHash := func(h node.Hash) {
		entry, ok := s.Pool.GetEntry(h)
		if !ok {
			return
		}
		appendIfMatched(h, entry)
	}

	switch {
	case platformView != nil && subNodes != nil:
		// Iterate the smaller candidate set, then intersect by membership.
		if len(platformView) <= len(subNodes) {
			for h := range platformView {
				if _, ok := subNodes[h]; !ok {
					continue
				}
				appendIfMatchedHash(h)
			}
		} else {
			for h := range subNodes {
				if _, ok := platformView[h]; !ok {
					continue
				}
				appendIfMatchedHash(h)
			}
		}
	case platformView != nil:
		for h := range platformView {
			appendIfMatchedHash(h)
		}
	case subNodes != nil:
		for h := range subNodes {
			appendIfMatchedHash(h)
		}
	default:
		s.Pool.Range(func(h node.Hash, entry *node.NodeEntry) bool {
			appendIfMatched(h, entry)
			return true
		})
	}

	if result == nil {
		result = []NodeSummary{}
	}
	return result, nil
}

func (s *ControlPlaneService) nodeEntryMatchesFilters(
	entry *node.NodeEntry,
	filters NodeFilters,
	subLookup node.SubLookupFunc,
) bool {
	// Enabled/disabled filter.
	if filters.Enabled != nil {
		enabled := true
		if subLookup != nil {
			enabled = entry.HasEnabledSubscription(subLookup)
		}
		if enabled != *filters.Enabled {
			return false
		}
	}

	// Node tag fuzzy search filter.
	if filters.TagKeyword != nil {
		keyword := strings.ToLower(strings.TrimSpace(*filters.TagKeyword))
		if keyword != "" {
			matched := false
			for _, subID := range entry.SubscriptionIDs() {
				sub := s.SubMgr.Lookup(subID)
				if sub == nil {
					continue
				}
				managed, ok := sub.ManagedNodes().LoadNode(entry.Hash)
				if !ok {
					continue
				}
				tags := managed.Tags
				for _, tag := range tags {
					displayTag := sub.Name() + "/" + tag
					if strings.Contains(strings.ToLower(displayTag), keyword) {
						matched = true
						break
					}
				}
				if matched {
					break
				}
			}
			if !matched {
				return false
			}
		}
	}

	// Region filter.
	if filters.Region != nil {
		region := entry.GetRegion(nil)
		if s.GeoIP != nil {
			region = entry.GetRegion(s.GeoIP.Lookup)
		}
		if region == "" || region != *filters.Region {
			return false
		}
	}
	// Circuit open filter.
	if filters.CircuitOpen != nil {
		if entry.IsCircuitOpen() != *filters.CircuitOpen {
			return false
		}
	}
	// Has outbound filter.
	if filters.HasOutbound != nil {
		if entry.HasOutbound() != *filters.HasOutbound {
			return false
		}
	}
	// Egress IP filter.
	if filters.EgressIP != nil {
		egressIP := entry.GetEgressIP()
		if !egressIP.IsValid() || egressIP.String() != *filters.EgressIP {
			return false
		}
	}
	// Probed since filter.
	if filters.ProbedSince != nil {
		lastUpdate := entry.LastLatencyProbeAttempt.Load()
		if lastUpdate < filters.ProbedSince.UnixNano() {
			return false
		}
	}
	return true
}

// GetNode returns a single node by hash.
func (s *ControlPlaneService) GetNode(hashStr string) (*NodeSummary, error) {
	h, err := node.ParseHex(hashStr)
	if err != nil {
		return nil, invalidArg("node_hash: invalid format")
	}
	entry, ok := s.Pool.GetEntry(h)
	if !ok {
		return nil, notFound("node not found")
	}
	ns := s.nodeEntryToSummary(h, entry)
	return &ns, nil
}

// ProbeEgress triggers a synchronous egress probe and returns results.
func (s *ControlPlaneService) ProbeEgress(hashStr string) (*probe.EgressProbeResult, error) {
	h, err := node.ParseHex(hashStr)
	if err != nil {
		return nil, invalidArg("node_hash: invalid format")
	}
	entry, ok := s.Pool.GetEntry(h)
	if !ok {
		return nil, notFound("node not found")
	}
	result, err := s.ProbeMgr.ProbeEgressSync(h)
	if err != nil {
		return nil, internal("egress probe failed", err)
	}
	result.Region = entry.GetRegion(nil)
	if s.GeoIP != nil {
		result.Region = entry.GetRegion(s.GeoIP.Lookup)
	}
	return result, nil
}

// ProbeLatency triggers a synchronous latency probe and returns results.
func (s *ControlPlaneService) ProbeLatency(hashStr string, platformID string) (*probe.LatencyProbeResult, error) {
	h, err := node.ParseHex(hashStr)
	if err != nil {
		return nil, invalidArg("node_hash: invalid format")
	}
	if _, ok := s.Pool.GetEntry(h); !ok {
		return nil, notFound("node not found")
	}
	// Explicit platform context pins the probe to that platform's test URL.
	testURL := ""
	if platformID != "" {
		plat, ok := s.Pool.GetPlatform(platformID)
		if !ok {
			return nil, notFound("platform not found")
		}
		if plat.ProbeOverride != nil && plat.ProbeOverride.LatencyTestURL != "" {
			testURL = plat.ProbeOverride.LatencyTestURL
		}
	}
	result, err := s.ProbeMgr.ProbeLatencySyncWithURL(h, testURL)
	if err != nil {
		return nil, internal("latency probe failed", err)
	}
	return result, nil
}

// ResolvePlatformProbe aggregates probe overrides of every platform the node
// belongs to: probing is disabled only when all containing platforms with an
// override disable it; intervals take the minimum; the first non-empty test
// URL wins.
func (s *ControlPlaneService) ResolvePlatformProbe(h node.Hash) probe.PlatformProbeConfig {
	var out probe.PlatformProbeConfig
	matchedCount, disabledCount := 0, 0
	s.Pool.RangePlatforms(func(plat *platform.Platform) bool {
		ov := plat.ProbeOverride
		if ov == nil || !plat.View().Contains(h) {
			return true
		}
		matchedCount++
		if ov.Disabled {
			disabledCount++
		}
		if ov.LatencyProbeIntervalNs > 0 {
			if d := time.Duration(ov.LatencyProbeIntervalNs); out.LatencyInterval == 0 || d < out.LatencyInterval {
				out.LatencyInterval = d
			}
		}
		if ov.EgressProbeIntervalNs > 0 {
			if d := time.Duration(ov.EgressProbeIntervalNs); out.EgressInterval == 0 || d < out.EgressInterval {
				out.EgressInterval = d
			}
		}
		if ov.LatencyTestURL != "" && out.TestURL == "" {
			out.TestURL = ov.LatencyTestURL
		}
		return true
	})
	if matchedCount == 0 {
		return probe.PlatformProbeConfig{}
	}
	// Probing stays enabled when at least one containing platform allows it.
	out.Disabled = disabledCount == matchedCount
	return out
}

const batchProbeMaxConcurrency = 32

// NodeProbeItem is one node's batch probe result.
type NodeProbeItem struct {
	Hash   string `json:"hash"`
	Error  string `json:"error,omitempty"`
	Result any    `json:"result,omitempty"`
}

// BatchProbeLatency probes multiple nodes concurrently and returns per-node
// results in input order. An optional platformID pins the probe URL context.
func (s *ControlPlaneService) BatchProbeLatency(hashes []string, platformID string) ([]NodeProbeItem, error) {
	if platformID != "" {
		if _, ok := s.Pool.GetPlatform(platformID); !ok {
			return nil, notFound("platform not found")
		}
	}
	return s.batchProbe(hashes, func(h string) (any, error) { return s.ProbeLatency(h, platformID) })
}

// BatchProbeEgress probes multiple nodes concurrently and returns per-node
// results in input order.
func (s *ControlPlaneService) BatchProbeEgress(hashes []string) ([]NodeProbeItem, error) {
	return s.batchProbe(hashes, func(h string) (any, error) { return s.ProbeEgress(h) })
}

func (s *ControlPlaneService) batchProbe(hashes []string, run func(string) (any, error)) ([]NodeProbeItem, error) {
	if len(hashes) == 0 {
		return nil, invalidArg("hashes: must be non-empty")
	}
	if len(hashes) > 500 {
		return nil, invalidArg("hashes: must be <= 500")
	}
	items := make([]NodeProbeItem, len(hashes))
	for i, h := range hashes {
		items[i] = NodeProbeItem{Hash: h}
	}

	sem := make(chan struct{}, batchProbeMaxConcurrency)
	var wg sync.WaitGroup
	for i := range items {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res, err := run(items[idx].Hash)
			if err != nil {
				items[idx].Error = err.Error()
				return
			}
			items[idx].Result = res
		}(i)
	}
	wg.Wait()
	return items, nil
}
