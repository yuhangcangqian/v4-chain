package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	gometrics "github.com/armon/go-metrics"
	"github.com/cosmos/cosmos-sdk/telemetry"
	"github.com/dydxprotocol/v4/daemons/pricefeed/client/constants"
	"github.com/dydxprotocol/v4/daemons/pricefeed/client/types"
	pricefeedmetrics "github.com/dydxprotocol/v4/daemons/pricefeed/metrics"
	"github.com/dydxprotocol/v4/lib"
	"github.com/dydxprotocol/v4/lib/metrics"
)

// ExchangeQueryHandlerImpl is the struct that implements the `ExchangeQueryHandler` interface.
type ExchangeQueryHandlerImpl struct {
	lib.TimeProvider
}

// Ensure the `ExchangeQueryHandlerImpl` struct is implemented at compile time
var _ ExchangeQueryHandler = (*ExchangeQueryHandlerImpl)(nil)

// ExchangeQueryHandler is an interface that encapsulates querying an exchange for price info.
type ExchangeQueryHandler interface {
	lib.TimeProvider
	Query(
		ctx context.Context,
		exchangeQueryDetails *types.ExchangeQueryDetails,
		exchangeConfig *types.MutableExchangeMarketConfig,
		marketIds []types.MarketId,
		requestHandler lib.RequestHandler,
		marketPriceExponent map[types.MarketId]types.Exponent,
	) (marketPriceTimestamps []*types.MarketPriceTimestamp, unavailableMarkets map[types.MarketId]error, err error)
}

// Query makes an API call to a specific exchange and returns the transformed response, including both valid prices
// and any unavailable markets with specific errors.
// 1) Validate `marketIds` contains at least one id.
// 2) Convert the list of `marketIds` to tickers that are specific for a given exchange. Create a mapping of
// tickers to price exponents and a reverse mapping of ticker back to `MarketId`.
// 3) Make API call to an exchange and verify the response status code is not an error status code.
// 4) Transform the API response to market prices, while tracking unavailable tickers.
// 5) Return dual values:
// - a slice of `MarketPriceTimestamp`s that contains resolved market prices
// - a map of marketIds that could not be resolved with corresponding specific errors.
func (eqh *ExchangeQueryHandlerImpl) Query(
	ctx context.Context,
	exchangeQueryDetails *types.ExchangeQueryDetails,
	exchangeConfig *types.MutableExchangeMarketConfig,
	marketIds []types.MarketId,
	requestHandler lib.RequestHandler,
	marketPriceExponent map[types.MarketId]types.Exponent,
) (marketPriceTimestamps []*types.MarketPriceTimestamp, unavailableMarkets map[types.MarketId]error, err error) {
	// Measure latency to run query function per exchange.
	defer metrics.ModuleMeasureSinceWithLabels(
		metrics.PricefeedDaemon,
		[]string{
			metrics.PricefeedDaemon,
			metrics.PriceFetcherQueryExchange,
			metrics.Latency,
		},
		time.Now(),
		[]gometrics.Label{pricefeedmetrics.GetLabelForExchangeId(exchangeQueryDetails.Exchange)},
	)
	// 1) Validate `marketIds` contains at least one id.
	if len(marketIds) == 0 {
		return nil, nil, errors.New("At least one marketId must be queried")
	}

	// 2) Convert the list of `marketIds` to tickers that are specific for a given exchange. Create a mapping
	// of tickers to price exponents and a reverse mapping of ticker back to `MarketId`.
	tickers := make([]string, 0, len(marketIds))
	tickerToPriceExponent := make(map[string]int32, len(marketIds))
	tickerToMarketId := make(map[string]types.MarketId, len(marketIds))
	for _, marketId := range marketIds {
		ticker, ok := exchangeConfig.MarketToTicker[marketId]
		if !ok {
			return nil, nil, fmt.Errorf("No ticker for market: %v", marketId)
		}
		priceExponent, ok := marketPriceExponent[marketId]
		if !ok {
			return nil, nil, fmt.Errorf("No market price exponent for id: %v", marketId)
		}

		tickers = append(tickers, ticker)
		tickerToPriceExponent[ticker] = priceExponent
		tickerToMarketId[ticker] = marketId

		// Measure count of requests sent.
		telemetry.IncrCounterWithLabels(
			[]string{
				metrics.PricefeedDaemon,
				metrics.HttpGetRequest,
				metrics.Count,
			},
			1,
			[]gometrics.Label{
				pricefeedmetrics.GetLabelForMarketId(marketId),
				pricefeedmetrics.GetLabelForExchangeId(exchangeQueryDetails.Exchange),
			},
		)
	}

	// 3) Make API call to an exchange and verify the response status code is not an error status code.
	url := CreateRequestUrl(exchangeQueryDetails.Url, tickers)

	beforeRequest := time.Now()
	response, err := requestHandler.Get(ctx, url)
	// Measure time to make API request for exchange.
	metrics.ModuleMeasureSinceWithLabels(
		metrics.PricefeedDaemon,
		[]string{
			metrics.PricefeedDaemon,
			metrics.ExchangeQueryHandlerApiRequest,
			metrics.Latency,
		},
		beforeRequest,
		[]gometrics.Label{
			pricefeedmetrics.GetLabelForExchangeId(exchangeQueryDetails.Exchange),
		},
	)
	if err != nil {
		return nil, nil, err
	}

	// Measure count of exchange API calls as well as what the status code is.
	telemetry.IncrCounterWithLabels(
		[]string{
			metrics.PricefeedDaemon,
			metrics.HttpGetResponse,
			metrics.Count,
		},
		1,
		[]gometrics.Label{
			metrics.GetLabelForIntValue(metrics.StatusCode, response.StatusCode),
			pricefeedmetrics.GetLabelForExchangeId(exchangeQueryDetails.Exchange),
		},
	)

	// Verify response is not 4xx or 5xx.
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, nil, fmt.Errorf("%s %v", constants.UnexpectedResponseStatusMessage, response.StatusCode)
	}

	// 4) Transform the API response to market prices, while tracking unavailable tickers.
	prices, unavailableTickers, err := exchangeQueryDetails.PriceFunction(
		response,
		tickerToPriceExponent,
		&lib.MedianizerImpl{},
	)
	if err != nil {
		return nil, nil, err
	}

	// 5) Insert prices into MarketPriceTimestamp struct slice, convert unavailable tickers back into marketIds,
	// and return.
	marketPriceTimestamps = make([]*types.MarketPriceTimestamp, 0, len(prices))
	now := eqh.Now()

	for ticker, price := range prices {
		marketId, ok := tickerToMarketId[ticker]
		if !ok {
			return nil, nil, fmt.Errorf("Severe unexpected error: no market id for ticker: %v", ticker)
		}

		marketPriceTimestamp := &types.MarketPriceTimestamp{
			MarketId:      marketId,
			Price:         price,
			LastUpdatedAt: now,
		}

		marketPriceTimestamps = append(marketPriceTimestamps, marketPriceTimestamp)
	}

	unavailableMarkets = make(map[types.MarketId]error, len(unavailableTickers))
	for ticker, error := range unavailableTickers {
		marketId, ok := tickerToMarketId[ticker]
		if !ok {
			return nil, nil, fmt.Errorf("Severe unexpected error: no market id for ticker: %v", ticker)
		}
		unavailableMarkets[marketId] = error
	}

	return marketPriceTimestamps, unavailableMarkets, nil
}

func CreateRequestUrl(baseUrl string, tickers []string) string {
	return strings.Replace(baseUrl, "$", strings.Join(tickers, ","), -1)
}