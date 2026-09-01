package proxy

import (
	"github.com/Resinat/Resin/internal/outbound"
	"github.com/Resinat/Resin/internal/routing"
	"github.com/sagernet/sing-box/adapter"
)

type routedOutbound struct {
	Route    routing.RouteResult
	Outbound adapter.Outbound
}

// transportKey derives the transport-pool key for this route so that different
// relay paths never share pooled keep-alive transports.
func (r routedOutbound) transportKey() TransportKey {
	return TransportKey{
		NodeHash:  r.Route.NodeHash,
		RelayHash: r.Route.RelayNodeHash,
	}
}

func resolveRoutedOutbound(
	router *routing.Router,
	pool outbound.PoolAccessor,
	chains outbound.ChainedOutboundResolver,
	platformName string,
	account string,
	target string,
) (routedOutbound, *ProxyError) {
	result, err := router.RouteRequest(platformName, account, target)
	if err != nil {
		return routedOutbound{}, mapRouteError(err)
	}

	entry, ok := pool.GetEntry(result.NodeHash)
	if !ok {
		return routedOutbound{}, ErrNoAvailableNodes
	}

	if !result.RelayNodeHash.IsZero() {
		if chains == nil {
			return routedOutbound{}, ErrNoAvailableNodes
		}
		ob, chainErr := chains.EnsureChainedOutbound(result.NodeHash, result.RelayNodeHash)
		if chainErr != nil {
			return routedOutbound{}, ErrNoAvailableNodes
		}
		return routedOutbound{
			Route:    result,
			Outbound: ob,
		}, nil
	}

	obPtr := entry.Outbound.Load()
	if obPtr == nil {
		return routedOutbound{}, ErrNoAvailableNodes
	}

	return routedOutbound{
		Route:    result,
		Outbound: *obPtr,
	}, nil
}
