package client

import (
	"context"
	"errors"
	"github.com/dydxprotocol/v4/daemons/pricefeed/client/price_fetcher"
	"github.com/dydxprotocol/v4/daemons/pricefeed/client/price_function"
	pricetypes "github.com/dydxprotocol/v4/x/prices/types"
	"net/http"
	"syscall"
	"time"

	gometrics "github.com/armon/go-metrics"
	"github.com/cometbft/cometbft/libs/log"
	"github.com/cosmos/cosmos-sdk/telemetry"
	"github.com/dydxprotocol/v4/daemons/pricefeed/api"
	"github.com/dydxprotocol/v4/daemons/pricefeed/client/handler"
	"github.com/dydxprotocol/v4/daemons/pricefeed/client/types"
	pricefeedmetrics "github.com/dydxprotocol/v4/daemons/pricefeed/metrics"
	"github.com/dydxprotocol/v4/lib"
	"github.com/dydxprotocol/v4/lib/metrics"
)

const (
	// https://stackoverflow.com/questions/37774624/go-http-get-concurrency-and-connection-reset-by-peer.
	// This is a good number to start with based on the above link. Adjustments can/will be made accordingly.
	MaxConnectionsPerExchange = 50
)

var (
	HttpClient = http.Client{
		Transport: &http.Transport{MaxConnsPerHost: MaxConnectionsPerExchange},
	}
)

// SubTaskRunnerImpl is the struct that implements the `SubTaskRunner` interface.
type SubTaskRunnerImpl struct{}

// Ensure the `SubTaskRunnerImpl` struct is implemented at compile time.
var _ SubTaskRunner = (*SubTaskRunnerImpl)(nil)

// SubTaskRunner is the interface for running pricefeed client task functions.
type SubTaskRunner interface {
	StartPriceUpdater(
		ctx context.Context,
		ticker *time.Ticker,
		stop <-chan bool,
		exchangeToMarketPrices *types.ExchangeToMarketPrices,
		priceFeedServiceClient api.PriceFeedServiceClient,
		logger log.Logger,
	)
	StartPriceEncoder(
		exchangeId types.ExchangeId,
		exchangeToMarketPrices *types.ExchangeToMarketPrices,
		logger log.Logger,
		bCh <-chan *price_fetcher.PriceFetcherSubtaskResponse,
	)
	StartPriceFetcher(
		ticker *time.Ticker,
		stop <-chan bool,
		configs types.PricefeedMutableMarketConfigs,
		exchangeStartupConfig types.ExchangeStartupConfig,
		exchangeDetails types.ExchangeQueryDetails,
		queryHandler handler.ExchangeQueryHandler,
		logger log.Logger,
		bCh chan<- *price_fetcher.PriceFetcherSubtaskResponse,
	)
	StartMarketParamUpdater(
		ctx context.Context,
		ticker *time.Ticker,
		stop <-chan bool,
		configs types.PricefeedMutableMarketConfigs,
		pricesQueryClient pricetypes.QueryClient,
		logger log.Logger,
	)
}

// StartPriceUpdater periodically runs a task loop to send price updates to the pricefeed server
// via:
// 1) Get `MarketPriceTimestamps` for all exchanges in an `ExchangeToMarketPrices` struct.
// 2) Transform `MarketPriceTimestamps` and exchange ids into an `UpdateMarketPricesRequest` struct.
// StartPriceUpdater runs in the daemon's main goroutine and does not need access to the daemon's wait group
// to signal task completion.
func (s *SubTaskRunnerImpl) StartPriceUpdater(
	ctx context.Context,
	ticker *time.Ticker,
	stop <-chan bool,
	exchangeToMarketPrices *types.ExchangeToMarketPrices,
	priceFeedServiceClient api.PriceFeedServiceClient,
	logger log.Logger,
) {
	for {
		select {
		case <-ticker.C:
			err := RunPriceUpdaterTaskLoop(ctx, exchangeToMarketPrices, priceFeedServiceClient, logger)
			if err != nil {
				panic(err)
			}

		case <-stop:
			return
		}
	}
}

// StartPriceEncoder continuously reads from a buffered channel, reading encoded API responses for exchange
// requests and inserting them into an `ExchangeToMarketPrices` cache.
// StartPriceEncoder reads price fetcher responses from a shared channel, and does not need a ticker or stop
// signal from the daemon to exit. It marks itself as done in the daemon's wait group when the price fetcher
// closes the shared channel.
func (s *SubTaskRunnerImpl) StartPriceEncoder(
	exchangeId types.ExchangeId,
	exchangeToMarketPrices *types.ExchangeToMarketPrices,
	logger log.Logger,
	bCh <-chan *price_fetcher.PriceFetcherSubtaskResponse,
) {
	// Listen for prices from the buffered channel and update the exchangeToMarketPrices cache.
	// Also log any errors that occur.
	for response := range bCh {
		processPriceFetcherResponse(
			response,
			exchangeId,
			exchangeToMarketPrices,
			logger,
		)
	}
}

// processPriceFetcherResponse consumes the (price, error) response from the price fetecher and either updates the
// exchangeToMarketPrices cache with a valid price, or appropriately logs and reports metrics for errors.
func processPriceFetcherResponse(
	response *price_fetcher.PriceFetcherSubtaskResponse,
	exchangeId types.ExchangeId,
	exchangeToMarketPrices *types.ExchangeToMarketPrices,
	logger log.Logger,
) {
	// Capture nil response on channel close.
	if response == nil {
		panic("nil response received from price fetcher")
	}

	if response.Err == nil {
		exchangeToMarketPrices.UpdatePrice(exchangeId, response.Price)
	} else {
		if errors.Is(response.Err, context.DeadlineExceeded) {
			// Log info if there are timeout errors in the ingested buffered channel prices.
			// This is only an info so that there aren't noisy errors when undesirable but
			// expected behavior occurs.
			logger.Info(
				"Failed to update exchange price in price daemon priceEncoder due to timeout",
				"error",
				response.Err,
				"exchangeId",
				exchangeId,
			)

			// Measure timeout failures.
			telemetry.IncrCounterWithLabels(
				[]string{
					metrics.PricefeedDaemon,
					metrics.PriceEncoderUpdatePrice,
					metrics.HttpGetTimeout,
					metrics.Error,
				},
				1,
				[]gometrics.Label{
					pricefeedmetrics.GetLabelForExchangeId(exchangeId),
				},
			)
		} else if price_function.IsExchangeError(response.Err) {
			// Log info if there are 5xx errors in the ingested buffered channel prices.
			// This is only an info so that there aren't noisy errors when undesirable but
			// expected behavior occurs.
			logger.Info(
				"Failed to update exchange price in price daemon priceEncoder due to exchange-side error",
				"error",
				response.Err,
				"exchangeId",
				exchangeId,
			)

			// Measure 5xx failures.
			telemetry.IncrCounterWithLabels(
				[]string{
					metrics.PricefeedDaemon,
					metrics.PriceEncoderUpdatePrice,
					metrics.HttpGet5xxx,
					metrics.Error,
				},
				1,
				[]gometrics.Label{
					pricefeedmetrics.GetLabelForExchangeId(exchangeId),
				},
			)
		} else if errors.Is(response.Err, syscall.ECONNRESET) {
			// Log info if there are connections reset by the exchange.
			// This is only an info so that there aren't noisy errors when undesirable but
			// expected behavior occurs.
			logger.Info(
				"Failed to update exchange price in price daemon priceEncoder due to exchange-side hang-up",
				"error",
				response.Err,
				"exchangeId",
				exchangeId,
			)

			// Measure HTTP GET hangups.
			telemetry.IncrCounterWithLabels(
				[]string{
					metrics.PricefeedDaemon,
					metrics.PriceEncoderUpdatePrice,
					metrics.HttpGetHangup,
					metrics.Error,
				},
				1,
				[]gometrics.Label{
					pricefeedmetrics.GetLabelForExchangeId(exchangeId),
				},
			)
		} else {
			// Log error if there are errors in the ingested buffered channel prices.
			logger.Error(
				"Failed to update exchange price in price daemon priceEncoder",
				"error",
				response.Err,
				"exchangeId",
				exchangeId,
			)

			// Measure all failures in querying other than timeout.
			telemetry.IncrCounterWithLabels(
				[]string{
					metrics.PricefeedDaemon,
					metrics.PriceEncoderUpdatePrice,
					metrics.Error,
				},
				1,
				[]gometrics.Label{
					pricefeedmetrics.GetLabelForExchangeId(exchangeId),
				},
			)
		}
	}
}

// StartPriceFetcher periodically starts goroutines to "fetch" market prices from a specific exchange. Each
// goroutine does the following:
// 1) query a single market price from a specific exchange
// 2) transform response to `MarketPriceTimestamp`
// 3) send transformed response to a buffered channel that's shared across multiple goroutines
// NOTE: the subtask response shared channel has a buffer size and goroutines will block if the buffer is full.
// NOTE: the price fetcher kicks off 1 to n go routines every time the subtask loop runs, but the subtask
// loop blocks until all go routines are done. This means that these go routines are not tracked by the wait group.
func (s *SubTaskRunnerImpl) StartPriceFetcher(
	ticker *time.Ticker,
	stop <-chan bool,
	configs types.PricefeedMutableMarketConfigs,
	exchangeStartupConfig types.ExchangeStartupConfig,
	exchangeDetails types.ExchangeQueryDetails,
	queryHandler handler.ExchangeQueryHandler,
	logger log.Logger,
	bCh chan<- *price_fetcher.PriceFetcherSubtaskResponse,
) {
	exchangeMarketConfig, err := configs.GetExchangeMarketConfigCopy(exchangeStartupConfig.ExchangeId)
	if err != nil {
		panic(err)
	}

	marketConfigs, err := configs.GetMarketConfigCopies(exchangeMarketConfig.GetMarketIds())
	if err != nil {
		panic(err)
	}

	// Create PriceFetcher to begin querying with.
	priceFetcher, err := price_fetcher.NewPriceFetcher(
		exchangeStartupConfig,
		exchangeDetails,
		exchangeMarketConfig,
		marketConfigs,
		queryHandler,
		logger,
		bCh,
	)
	if err != nil {
		panic(err)
	}

	// The PricefeedMutableMarketConfigs object that stores the configs for each exchange
	// is not initialized with the price fetcher, because both objects have references to
	// each other defined during normal daemon operation. Instead, the price fetcher is
	// initialized with the configs object after the price fetcher is created, and then adds
	// itself to the config's list of exchange config updaters here.
	configs.AddExchangeConfigUpdater(priceFetcher)

	requestHandler := lib.NewRequestHandlerImpl(
		&HttpClient,
	)
	// Begin loop to periodically start goroutines to query market prices.
	for {
		select {
		case <-ticker.C:
			// Start goroutines to query exchange markets. The goroutines started by the price
			// fetcher are not tracked by the global wait group, because RunTaskLoop will
			// block until all goroutines are done.
			priceFetcher.RunTaskLoop(requestHandler)

		case <-stop:
			// Signal to the encoder that the price fetcher is done.
			close(bCh)
			return
		}
	}
}

// StartMarketParamUpdater periodically starts a goroutine to update the market parameters that control which
// markets the daemon queries and how they are queried and computed from each exchange.
func (s *SubTaskRunnerImpl) StartMarketParamUpdater(
	ctx context.Context,
	ticker *time.Ticker,
	stop <-chan bool,
	configs types.PricefeedMutableMarketConfigs,
	pricesQueryClient pricetypes.QueryClient,
	logger log.Logger,
) {
	// Periodically update market parameters.
	for {
		select {
		case <-ticker.C:
			RunMarketParamUpdaterTaskLoop(ctx, configs, pricesQueryClient, logger)

		case <-stop:
			return
		}
	}
}

// -------------------- Task Loops -------------------- //

// RunPriceUpdaterTaskLoop copies the map of current `exchangeId -> MarketPriceTimestamp`,
// transforms the map values into a market price update request and sends the request to the socket
// where the pricefeed server is listening.
func RunPriceUpdaterTaskLoop(
	ctx context.Context,
	exchangeToMarketPrices *types.ExchangeToMarketPrices,
	priceFeedServiceClient api.PriceFeedServiceClient,
	logger log.Logger,
) error {
	priceUpdates := exchangeToMarketPrices.GetAllPrices()
	request := transformPriceUpdates(priceUpdates)

	// Measure latency to send prices over gRPC.
	// Note: intentionally skipping latency for `GetAllPrices`.
	defer telemetry.ModuleMeasureSince(
		metrics.PricefeedDaemon,
		time.Now(),
		metrics.PriceUpdaterSendPrices,
		metrics.Latency,
	)

	// On startup the length of request will likely be 0. However, sending a request of length 0
	// is a fatal error.
	// panic: rpc error: code = Unknown desc = Market price update has length of 0.
	if len(request.MarketPriceUpdates) > 0 {
		_, err := priceFeedServiceClient.UpdateMarketPrices(ctx, request)
		if err != nil {
			// Log error if an error will be returned from the task loop and measure failure.
			logger.Error("Failed to run price updater task loop for price daemon", "error", err)
			telemetry.IncrCounter(
				1,
				metrics.PricefeedDaemon,
				metrics.PriceUpdaterTaskLoop,
				metrics.Error,
			)
			return err
		}
	} else {
		// This is expected to happen on startup until prices have been encoded into the in-memory
		// `exchangeToMarketPrices` map. After that point, there should be no price updates of length 0.
		logger.Info(
			"Price update had length of 0",
		)
		telemetry.IncrCounter(
			1,
			metrics.PricefeedDaemon,
			metrics.PriceUpdaterZeroPrices,
			metrics.Count,
		)
	}

	return nil
}

// RunMarketParamUpdaterTaskLoop queries all market params from the query client, and then updates the
// shared, in-memory `PricefeedMutableMarketConfigs` object with the latest market params.
func RunMarketParamUpdaterTaskLoop(
	ctx context.Context,
	configs types.PricefeedMutableMarketConfigs,
	pricesQueryClient pricetypes.QueryClient,
	logger log.Logger,
) {
	// Measure latency to fetch and parse the market params, and propagate all updates.
	defer telemetry.ModuleMeasureSince(
		metrics.PricefeedDaemon,
		time.Now(),
		metrics.MarketUpdaterUpdateMarkets,
		metrics.Latency,
	)
	// Query all market params from the query client.
	getAllMarketsResponse, err := pricesQueryClient.AllMarketParams(ctx, &pricetypes.QueryAllMarketParamsRequest{})
	if err != nil {
		logger.Error("Failed to get all market params", "error", err)
		// Measure all failures to retrieve market params from the query client.
		telemetry.IncrCounter(
			1,
			metrics.PricefeedDaemon,
			metrics.MarketUpdaterGetAllMarketParams,
			metrics.Error,
		)
		return
	}

	// Update shared, in-memory config with the latest market params. Report update success/failure via logging/metrics.
	err = configs.UpdateMarkets(getAllMarketsResponse.MarketParams)
	if err == nil {
		telemetry.IncrCounter(
			1,
			metrics.PricefeedDaemon,
			metrics.MarketUpdaterApplyMarketUpdates,
			metrics.Success,
		)
	} else {
		logger.Error("Failed to apply market updates", "error", err)
		// Measure all failures to update market params.
		telemetry.IncrCounter(
			1,
			metrics.PricefeedDaemon,
			metrics.MarketUpdaterApplyMarketUpdates,
			metrics.Error,
		)
	}
}

// -------------------- Task Loop Helpers -------------------- //

// transformPriceUpdates transforms a map (key: exchangeId, value: list of market prices) into a
// market price update request.
func transformPriceUpdates(
	updates map[types.ExchangeId][]types.MarketPriceTimestamp,
) *api.UpdateMarketPricesRequest {
	// Measure latency to transform prices being sent over gRPC.
	defer telemetry.ModuleMeasureSince(
		metrics.PricefeedDaemon,
		time.Now(),
		metrics.PriceUpdaterTransformPrices,
		metrics.Latency,
	)

	marketPriceUpdateMap := make(map[types.MarketId]*api.MarketPriceUpdate)

	// Invert to marketId -> `api.MarketPriceUpdate`.
	for exchangeId, marketPriceTimestamps := range updates {
		for _, marketPriceTimestamp := range marketPriceTimestamps {
			telemetry.IncrCounterWithLabels(
				[]string{
					metrics.PricefeedDaemon,
					metrics.PriceUpdateCount,
					metrics.Count,
				},
				1,
				[]gometrics.Label{
					pricefeedmetrics.GetLabelForExchangeId(exchangeId),
					pricefeedmetrics.GetLabelForMarketId(marketPriceTimestamp.MarketId),
				},
			)

			marketPriceUpdate, exists := marketPriceUpdateMap[marketPriceTimestamp.MarketId]
			// Add key with empty `api.MarketPriceUpdate` if entry does not exist.
			if !exists {
				marketPriceUpdate = &api.MarketPriceUpdate{
					MarketId:       marketPriceTimestamp.MarketId,
					ExchangePrices: []*api.ExchangePrice{},
				}
				marketPriceUpdateMap[marketPriceTimestamp.MarketId] = marketPriceUpdate
			}

			// Add `api.ExchangePrice`.
			priceUpdateTime := marketPriceTimestamp.LastUpdatedAt
			exchangePrice := &api.ExchangePrice{
				ExchangeId:     exchangeId,
				Price:          marketPriceTimestamp.Price,
				LastUpdateTime: &priceUpdateTime,
			}
			marketPriceUpdate.ExchangePrices = append(marketPriceUpdate.ExchangePrices, exchangePrice)
		}
	}

	// Add all `api.MarketPriceUpdate` to request to be sent by `client.UpdateMarketPrices`.
	request := &api.UpdateMarketPricesRequest{
		MarketPriceUpdates: make([]*api.MarketPriceUpdate, 0, len(marketPriceUpdateMap)),
	}
	for _, update := range marketPriceUpdateMap {
		request.MarketPriceUpdates = append(
			request.MarketPriceUpdates,
			update,
		)
	}
	return request
}