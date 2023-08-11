package types

import (
	"sync"

	"github.com/cosmos/cosmos-sdk/telemetry"
	"github.com/dydxprotocol/v4/daemons/pricefeed/types"
	"github.com/dydxprotocol/v4/lib/metrics"
)

// MarketToPrice maintains multiple prices for different markets for the same exchange,
// along with the last time that each market price was updated.
// Methods are goroutine safe.
type MarketToPrice struct {
	sync.RWMutex                                            // reader-writer lock
	MarketToPriceTimestamp map[uint32]*types.PriceTimestamp // {k: market id, v: PriceTimestamp}
}

// NewMarketToPrice creates a new MarketToPrice.
func NewMarketToPrice() *MarketToPrice {
	return &MarketToPrice{
		MarketToPriceTimestamp: make(map[uint32]*types.PriceTimestamp),
	}
}

// UpdatePrice updates a price for a market for an exchange.
// Prices are only updated if the timestamp on the updates are greater than
// the timestamp on existing prices.
func (mtp *MarketToPrice) UpdatePrice(
	marketPriceTimestamp *MarketPriceTimestamp,
) {
	mtp.Lock()
	defer mtp.Unlock()

	marketId := marketPriceTimestamp.MarketId
	priceTimestamp, ok := mtp.MarketToPriceTimestamp[marketId]
	if !ok {
		priceTimestamp = types.NewPriceTimestamp()
		mtp.MarketToPriceTimestamp[marketId] = priceTimestamp
	}
	isUpdated := priceTimestamp.UpdatePrice(marketPriceTimestamp.Price, &marketPriceTimestamp.LastUpdatedAt)

	validity := metrics.Valid
	if !isUpdated {
		validity = metrics.PriceIsInvalid
	}

	// Measure count of valid and invalid prices inserted into the in-memory map.
	telemetry.IncrCounter(1, metrics.PricefeedDaemon, metrics.UpdatePrice, validity, metrics.Count)
}

// GetAllPrices returns a list of all `MarketPriceTimestamps` for an exchange.
func (mtp *MarketToPrice) GetAllPrices() []MarketPriceTimestamp {
	mtp.RLock()
	defer mtp.RUnlock()

	marketPricesForExchange := make([]MarketPriceTimestamp, 0, len(mtp.MarketToPriceTimestamp))
	for marketId, priceTimestamp := range mtp.MarketToPriceTimestamp {
		mpt := MarketPriceTimestamp{
			MarketId:      marketId,
			LastUpdatedAt: priceTimestamp.LastUpdateTime,
			Price:         priceTimestamp.Price,
		}
		marketPricesForExchange = append(marketPricesForExchange, mpt)
	}

	return marketPricesForExchange
}