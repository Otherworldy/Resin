package api

import (
	"cmp"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Resinat/Resin/internal/service"
)

func nodeTagSortKey(n service.NodeSummary) string {
	if n.DisplayTag != "" {
		return n.DisplayTag
	}
	if len(n.Tags) == 0 {
		return ""
	}
	bestCreated := int64(math.MaxInt64)
	bestTag := ""
	for _, t := range n.Tags {
		if t.SubscriptionCreatedAtNs < bestCreated {
			bestCreated = t.SubscriptionCreatedAtNs
			bestTag = t.Tag
			continue
		}
		if t.SubscriptionCreatedAtNs == bestCreated && (bestTag == "" || t.Tag < bestTag) {
			bestTag = t.Tag
		}
	}
	return bestTag
}

// nodeLatencySortKey returns the latency value used for sorting: the last
// observed probe latency when present, otherwise the reference average.
// Missing values always sort last, regardless of sort direction.
func nodeLatencySortKey(n service.NodeSummary) (float64, bool) {
	if n.LastProbeLatencyMs != nil {
		return *n.LastProbeLatencyMs, true
	}
	if n.ReferenceLatencyMs != nil {
		return *n.ReferenceLatencyMs, true
	}
	return 0, false
}

func compareNodeSummaries(sortBy string, a, b service.NodeSummary) int {
	order := 0
	switch sortBy {
	case "created_at":
		order = strings.Compare(a.CreatedAt, b.CreatedAt)
	case "failure_count":
		order = cmp.Compare(a.FailureCount, b.FailureCount)
	case "region":
		order = strings.Compare(a.Region, b.Region)
	default:
		order = strings.Compare(nodeTagSortKey(a), nodeTagSortKey(b))
	}
	if order != 0 {
		return order
	}
	return strings.Compare(a.NodeHash, b.NodeHash)
}

func sortNodeSummaries(nodes []service.NodeSummary, sorting Sorting) {
	slices.SortStableFunc(nodes, func(a, b service.NodeSummary) int {
		if sorting.SortBy == "reference_latency" {
			la, oka := nodeLatencySortKey(a)
			lb, okb := nodeLatencySortKey(b)
			// Nodes without latency data always sort last, independent of direction.
			if !oka != !okb {
				if !oka {
					return 1
				}
				return -1
			}
			return applySortOrder(cmp.Compare(la, lb), sorting.SortOrder)
		}
		return applySortOrder(compareNodeSummaries(sorting.SortBy, a, b), sorting.SortOrder)
	})
}

type nodeListPageResponse struct {
	Items                  []service.NodeSummary `json:"items"`
	Total                  int                   `json:"total"`
	Limit                  int                   `json:"limit"`
	Offset                 int                   `json:"offset"`
	UniqueEgressIPs        int                   `json:"unique_egress_ips"`
	UniqueHealthyEgressIPs int                   `json:"unique_healthy_egress_ips"`
}

func countUniqueEgressIPs(nodes []service.NodeSummary) int {
	seen := make(map[string]struct{})
	for _, n := range nodes {
		if n.EgressIP == "" {
			continue
		}
		seen[n.EgressIP] = struct{}{}
	}
	return len(seen)
}

func countUniqueHealthyAndEnabledEgressIPs(nodes []service.NodeSummary) int {
	seen := make(map[string]struct{})
	for _, n := range nodes {
		if n.EgressIP == "" {
			continue
		}
		if !n.IsHealthyAndEnabled() {
			continue
		}
		seen[n.EgressIP] = struct{}{}
	}
	return len(seen)
}

// HandleListNodes returns a handler for GET /api/v1/nodes.
func HandleListNodes(cp *service.ControlPlaneService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		filters := service.NodeFilters{}

		platformID, ok := parseOptionalUUIDQuery(w, r, "platform_id", "platform_id")
		if !ok {
			return
		}
		filters.PlatformID = platformID

		subscriptionID, ok := parseOptionalUUIDQuery(w, r, "subscription_id", "subscription_id")
		if !ok {
			return
		}
		filters.SubscriptionID = subscriptionID

		if v := q.Get("region"); v != "" {
			filters.Region = &v
		}
		if v := q.Get("egress_ip"); v != "" {
			filters.EgressIP = &v
		}
		if v := strings.TrimSpace(q.Get("tag_keyword")); v != "" {
			filters.TagKeyword = &v
		}

		circuitOpen, ok := parseBoolQueryOrWriteInvalid(w, r, "circuit_open")
		if !ok {
			return
		}
		filters.CircuitOpen = circuitOpen

		hasOutbound, ok := parseBoolQueryOrWriteInvalid(w, r, "has_outbound")
		if !ok {
			return
		}
		filters.HasOutbound = hasOutbound

		enabled, ok := parseBoolQueryOrWriteInvalid(w, r, "enabled")
		if !ok {
			return
		}
		filters.Enabled = enabled

		if v := q.Get("probed_since"); v != "" {
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				writeInvalidArgument(w, "probed_since: invalid RFC3339 timestamp")
				return
			}
			filters.ProbedSince = &t
		}

		nodes, err := cp.ListNodes(filters)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		sorting, ok := parseSortingOrWriteInvalid(w, r, []string{"tag", "created_at", "failure_count", "region", "reference_latency"}, "tag", "asc")
		if !ok {
			return
		}
		sortNodeSummaries(nodes, sorting)

		pg, ok := parsePaginationOrWriteInvalid(w, r)
		if !ok {
			return
		}
		WriteJSON(w, http.StatusOK, nodeListPageResponse{
			Items:                  PaginateSlice(nodes, pg),
			Total:                  len(nodes),
			Limit:                  pg.Limit,
			Offset:                 pg.Offset,
			UniqueEgressIPs:        countUniqueEgressIPs(nodes),
			UniqueHealthyEgressIPs: countUniqueHealthyAndEnabledEgressIPs(nodes),
		})
	}
}

// HandleGetNode returns a handler for GET /api/v1/nodes/{hash}.
func HandleGetNode(cp *service.ControlPlaneService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash := PathParam(r, "hash")
		n, err := cp.GetNode(hash)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, n)
	}
}

// HandleProbeEgress returns a handler for POST /api/v1/nodes/{hash}/actions/probe-egress.
func HandleProbeEgress(cp *service.ControlPlaneService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash := PathParam(r, "hash")
		result, err := cp.ProbeEgress(hash)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

// HandleProbeLatency returns a handler for POST /api/v1/nodes/{hash}/actions/probe-latency.
// Optional query param platform_id pins the probe to that platform's test URL.
func HandleProbeLatency(cp *service.ControlPlaneService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hash := PathParam(r, "hash")
		result, err := cp.ProbeLatency(hash, r.URL.Query().Get("platform_id"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

// batchProbeRequest is the body for batch node probe endpoints.
type batchProbeRequest struct {
	Hashes     []string `json:"hashes"`
	PlatformID string   `json:"platform_id,omitempty"`
}

type batchProbeResponse struct {
	Items []service.NodeProbeItem `json:"items"`
}

// HandleBatchProbeLatency returns a handler for POST /api/v1/nodes/actions/probe-latency.
func HandleBatchProbeLatency(cp *service.ControlPlaneService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req batchProbeRequest
		if err := DecodeBody(r, &req); err != nil {
			writeInvalidArgument(w, err.Error())
			return
		}
		items, err := cp.BatchProbeLatency(req.Hashes, req.PlatformID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, batchProbeResponse{Items: items})
	}
}

// HandleBatchProbeEgress returns a handler for POST /api/v1/nodes/actions/probe-egress.
func HandleBatchProbeEgress(cp *service.ControlPlaneService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req batchProbeRequest
		if err := DecodeBody(r, &req); err != nil {
			writeInvalidArgument(w, err.Error())
			return
		}
		items, err := cp.BatchProbeEgress(req.Hashes)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, batchProbeResponse{Items: items})
	}
}
