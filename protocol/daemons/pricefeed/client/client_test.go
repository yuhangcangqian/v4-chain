package client

import (
	"context"
	"errors"
	"fmt"
	"github.com/dydxprotocol/v4/daemons/flags"
	pricefeed_constants "github.com/dydxprotocol/v4/daemons/pricefeed/client/constants"
	"github.com/dydxprotocol/v4/daemons/pricefeed/client/price_fetcher"
	daemonserver "github.com/dydxprotocol/v4/daemons/server"
	pricefeed_types "github.com/dydxprotocol/v4/daemons/server/types/pricefeed"
	"github.com/dydxprotocol/v4/lib"
	grpc_util "github.com/dydxprotocol/v4/testutil/grpc"
	pricetypes "github.com/dydxprotocol/v4/x/prices/types"
	"google.golang.org/grpc"
	"net"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/cometbft/cometbft/libs/log"
	"github.com/dydxprotocol/v4/daemons/pricefeed/api"
	"github.com/dydxprotocol/v4/daemons/pricefeed/client/handler"
	"github.com/dydxprotocol/v4/daemons/pricefeed/client/types"
	pricefeedtypes "github.com/dydxprotocol/v4/daemons/pricefeed/types"
	"github.com/dydxprotocol/v4/mocks"
	"github.com/dydxprotocol/v4/testutil/client"
	"github.com/dydxprotocol/v4/testutil/constants"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	subTaskRunnerImpl = SubTaskRunnerImpl{}
)

// FakeSubTaskRunner acts as a dummy struct replacing `SubTaskRunner` that simply advances the
// counter for each task in a threadsafe manner and allows awaiting go-routine completion. This
// struct should only be used for testing.
type FakeSubTaskRunner struct {
	sync.WaitGroup
	sync.RWMutex
	UpdaterCallCount       int
	EncoderCallCount       int
	FetcherCallCount       int
	MarketUpdaterCallCount int
}

// StartPriceUpdater replaces `client.StartPriceUpdater` and advances `UpdaterCallCount` by one.
func (f *FakeSubTaskRunner) StartPriceUpdater(
	ctx context.Context,
	ticker *time.Ticker,
	stop <-chan bool,
	exchangeToMarketPrices *types.ExchangeToMarketPrices,
	priceFeedServiceClient api.PriceFeedServiceClient,
	logger log.Logger,
) {
	// No need to lock/unlock since there is only one updater running and no risk of race-condition.
	f.UpdaterCallCount += 1
}

// StartPriceEncoder replaces `client.StartPriceEncoder`, marks the embedded waitgroup done and
// advances `EncoderCallCount` by one. This function will be called from a go-routine and is
// threadsafe.
func (f *FakeSubTaskRunner) StartPriceEncoder(
	exchangeId types.ExchangeId,
	exchangeToMarketPrices *types.ExchangeToMarketPrices,
	logger log.Logger,
	bCh <-chan *price_fetcher.PriceFetcherSubtaskResponse,
) {
	f.Lock()
	defer f.Unlock()

	f.EncoderCallCount += 1
	f.Done()
}

// StartPriceFetcher replaces `client.StartPriceFetcher`, marks the embedded waitgroup done and
// advances `FetcherCallCount` by one. This function will be called from a go-routine and is
// threadsafe.
func (f *FakeSubTaskRunner) StartPriceFetcher(
	ticker *time.Ticker,
	stop <-chan bool,
	configs types.PricefeedMutableMarketConfigs,
	exchangeStartupConfig types.ExchangeStartupConfig,
	exchangeDetails types.ExchangeQueryDetails,
	queryHandler handler.ExchangeQueryHandler,
	logger log.Logger,
	bCh chan<- *price_fetcher.PriceFetcherSubtaskResponse,
) {
	f.Lock()
	defer f.Unlock()

	f.FetcherCallCount += 1
	f.Done()
}

func (f *FakeSubTaskRunner) StartMarketParamUpdater(
	ctx context.Context,
	ticker *time.Ticker,
	stop <-chan bool,
	configs types.PricefeedMutableMarketConfigs,
	pricesQueryClient pricetypes.QueryClient,
	logger log.Logger,
) {
	f.Lock()
	defer f.Unlock()

	f.MarketUpdaterCallCount += 1
}

const (
	maxBufferedChannelLength     = 2
	connectionFailsErrorMsg      = "Failed to create connection"
	closeConnectionFailsErrorMsg = "Failed to close connection"
	fiveKilobytes                = 5 * 1024
)

var (
	validExchangeId                 = constants.ExchangeId1
	closeConnectionFailsError       = errors.New(closeConnectionFailsErrorMsg)
	testExchangeStartupConfigLength = len(constants.TestExchangeStartupConfigs)
)

func TestFixedBufferSize(t *testing.T) {
	require.Equal(t, fiveKilobytes, pricefeed_constants.FixedBufferSize)
}

func TestStart_InvalidConfig(t *testing.T) {
	tests := map[string]struct {
		// parameters
		mockGrpcClient              *mocks.GrpcClient
		initialMarketConfig         map[types.MarketId]*types.MutableMarketConfig
		initialExchangeMarketConfig map[types.ExchangeId]*types.MutableExchangeMarketConfig
		exchangeIdToStartupConfig   map[types.ExchangeId]*types.ExchangeStartupConfig
		exchangeIdToExchangeDetails map[types.ExchangeId]types.ExchangeQueryDetails

		// expectations
		expectedError             error
		expectGrpcConnection      bool
		expectCloseTcpConnection  bool
		expectCloseGrpcConnection bool
		// This should equal the length of the `exchangeIdToStartupConfig` passed into
		// `client.Start`.
		expectedNumExchangeTasks int
	}{
		"Invalid: Tcp Connection Fails": {
			mockGrpcClient: grpc_util.GenerateMockGrpcClientWithOptionalTcpConnectionErrors(
				errors.New(connectionFailsErrorMsg),
				nil,
				false,
			),
			expectedError: errors.New(connectionFailsErrorMsg),
		},
		"Invalid: Grpc Connection Fails": {
			mockGrpcClient: grpc_util.GenerateMockGrpcClientWithOptionalGrpcConnectionErrors(
				errors.New(connectionFailsErrorMsg),
				nil,
				false,
			),
			expectedError:            errors.New(connectionFailsErrorMsg),
			expectGrpcConnection:     true,
			expectCloseTcpConnection: true,
		},
		"Valid: 2 exchanges": {
			mockGrpcClient:              grpc_util.GenerateMockGrpcClientWithOptionalGrpcConnectionErrors(nil, nil, true),
			exchangeIdToStartupConfig:   constants.TestExchangeStartupConfigs,
			exchangeIdToExchangeDetails: constants.TestExchangeIdToExchangeQueryDetails,
			expectGrpcConnection:        true,
			expectCloseTcpConnection:    true,
			expectCloseGrpcConnection:   true,
			expectedNumExchangeTasks:    testExchangeStartupConfigLength,
		},
		"Invalid: empty exchange startup config": {
			mockGrpcClient:            grpc_util.GenerateMockGrpcClientWithOptionalGrpcConnectionErrors(nil, nil, true),
			exchangeIdToStartupConfig: map[types.ExchangeId]*types.ExchangeStartupConfig{},
			expectedError:             errors.New("exchangeIds must not be empty"),
			expectGrpcConnection:      true,
			expectCloseTcpConnection:  true,
			expectCloseGrpcConnection: true,
		},
		"Invalid: missing exchange query details": {
			mockGrpcClient: grpc_util.GenerateMockGrpcClientWithOptionalGrpcConnectionErrors(nil, nil, true),
			exchangeIdToStartupConfig: map[string]*types.ExchangeStartupConfig{
				validExchangeId: constants.TestExchangeStartupConfigs[validExchangeId],
			},
			expectedError:             fmt.Errorf("no exchange details exists for exchangeId: %v", validExchangeId),
			expectGrpcConnection:      true,
			expectCloseTcpConnection:  true,
			expectCloseGrpcConnection: true,
		},
		"Invalid: tcp close connection fails with good inputs": {
			mockGrpcClient: grpc_util.GenerateMockGrpcClientWithOptionalTcpConnectionErrors(
				nil,
				closeConnectionFailsError,
				true,
			),
			exchangeIdToStartupConfig:   constants.TestExchangeStartupConfigs,
			exchangeIdToExchangeDetails: constants.TestExchangeIdToExchangeQueryDetails,
			expectedError:               closeConnectionFailsError,
			expectGrpcConnection:        true,
			expectCloseTcpConnection:    true,
			expectCloseGrpcConnection:   true,
			expectedNumExchangeTasks:    testExchangeStartupConfigLength,
		},
		"Invalid: grpc close connection fails with good inputs": {
			mockGrpcClient: grpc_util.GenerateMockGrpcClientWithOptionalGrpcConnectionErrors(
				nil,
				closeConnectionFailsError,
				true,
			),
			exchangeIdToStartupConfig:   constants.TestExchangeStartupConfigs,
			exchangeIdToExchangeDetails: constants.TestExchangeIdToExchangeQueryDetails,
			expectedError:               closeConnectionFailsError,
			expectGrpcConnection:        true,
			expectCloseTcpConnection:    true,
			expectCloseGrpcConnection:   true,
			expectedNumExchangeTasks:    testExchangeStartupConfigLength,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			faketaskRunner := FakeSubTaskRunner{
				UpdaterCallCount:       0,
				EncoderCallCount:       0,
				FetcherCallCount:       0,
				MarketUpdaterCallCount: 0,
			}

			// Wait for each encoder and fetcher call to complete.
			faketaskRunner.WaitGroup.Add(tc.expectedNumExchangeTasks * 2)

			// Run Start.
			client := newClient()
			err := client.start(
				grpc_util.Ctx,
				flags.GetDefaultDaemonFlags(),
				log.NewNopLogger(),
				tc.mockGrpcClient,
				tc.exchangeIdToStartupConfig,
				tc.exchangeIdToExchangeDetails,
				&faketaskRunner,
			)

			if tc.expectedError == nil {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.expectedError.Error())
			}

			// Wait for encoder and fetcher go-routines to complete and then verify each subtask was
			// called the expected amount.
			faketaskRunner.Wait()
			require.Equal(t, tc.expectedNumExchangeTasks, faketaskRunner.EncoderCallCount)
			require.Equal(t, tc.expectedNumExchangeTasks, faketaskRunner.FetcherCallCount)
			if tc.expectedNumExchangeTasks > 0 {
				require.Equal(t, 1, faketaskRunner.UpdaterCallCount)
			} else {
				require.Equal(t, 0, faketaskRunner.UpdaterCallCount)
			}

			tc.mockGrpcClient.AssertCalled(t, "NewTcpConnection", grpc_util.Ctx, grpc_util.TcpEndpoint)
			if tc.expectGrpcConnection {
				tc.mockGrpcClient.AssertCalled(t, "NewGrpcConnection", grpc_util.Ctx, grpc_util.SocketPath)
			} else {
				tc.mockGrpcClient.AssertNotCalled(t, "NewGrpcConnection", grpc_util.Ctx, grpc_util.SocketPath)
			}

			if tc.expectCloseGrpcConnection {
				tc.mockGrpcClient.AssertCalled(t, "CloseConnection", grpc_util.GrpcConn)
			} else {
				tc.mockGrpcClient.AssertNotCalled(t, "CloseConnection", grpc_util.GrpcConn)
			}

			if tc.expectCloseTcpConnection {
				tc.mockGrpcClient.AssertCalled(t, "CloseConnection", grpc_util.TcpConn)
			} else {
				tc.mockGrpcClient.AssertNotCalled(t, "CloseConnection", grpc_util.TcpConn)
			}
		})
	}
}

// TestStop tests that the Stop interface works as expected. It's difficult to ensure that each go-routine
// is stopped, but this test ensures that the Stop executes successfully with no hangs.
func TestStop(t *testing.T) {
	// Setup daemon and grpc servers.
	daemonFlags := flags.GetDefaultDaemonFlags()

	// Configure and run daemon server.
	daemonServer := daemonserver.NewServer(
		log.NewNopLogger(),
		grpc.NewServer(),
		&lib.FileHandlerImpl{},
		daemonFlags.Shared.SocketAddress,
	)
	daemonServer.WithPriceFeedMarketToExchangePrices(
		pricefeed_types.NewMarketToExchangePrices(5 * time.Second),
	)

	defer daemonServer.Stop()
	go daemonServer.Start()

	// Configure and run gRPC server with mock prices query service attached.
	// Mock prices query server response to AllMarketParams.
	pricesQueryServer := mocks.QueryServer{}
	pricesQueryServer.On("AllMarketParams", mock.Anything, mock.Anything).Return(
		&pricetypes.QueryAllMarketParamsResponse{},
		nil,
	)

	// Create a gRPC server running on the default port and attach the mock prices query response.
	grpcServer := grpc.NewServer()
	pricetypes.RegisterQueryServer(grpcServer, &pricesQueryServer)

	// Start gRPC server with cleanup.
	defer grpcServer.Stop()
	go func() {
		ls, err := net.Listen("tcp", daemonFlags.Shared.GrpcServerAddress)
		require.NoError(t, err)
		err = grpcServer.Serve(ls)
		require.NoError(t, err)
	}()

	client := StartNewClient(
		grpc_util.Ctx,
		daemonFlags,
		log.NewNopLogger(),
		&lib.GrpcClientImpl{},
		constants.TestExchangeStartupConfigs,
		constants.TestExchangeIdToExchangeQueryDetails,
		&SubTaskRunnerImpl{},
	)

	// Stop the daemon.
	client.Stop()
}

func TestPriceEncoder_NoWrites(t *testing.T) {
	etmp, bChMap := generateBufferedChannelAndExchangeToMarketPrices(t, constants.Exchange1Exchange2Array)

	runPriceEncoderSequentially(
		t,
		constants.ExchangeId1,
		etmp,
		bChMap[constants.ExchangeId1],
		[]*types.MarketPriceTimestamp{},
	)

	require.Empty(t, etmp.ExchangeMarketPrices[constants.ExchangeId1].MarketToPriceTimestamp)
	require.Empty(t, etmp.ExchangeMarketPrices[constants.ExchangeId2].MarketToPriceTimestamp)
	require.Empty(t, bChMap[constants.ExchangeId1])
	require.Empty(t, bChMap[constants.ExchangeId2])
}

func TestPriceEncoder_DoNotWriteError(t *testing.T) {
	etmp, bChMap := generateBufferedChannelAndExchangeToMarketPrices(t, constants.Exchange1Exchange2Array)

	bCh := bChMap[constants.ExchangeId1]
	bCh <- &price_fetcher.PriceFetcherSubtaskResponse{
		Price: nil,
		Err:   errors.New("Failed to query"),
	}
	close(bCh)

	subTaskRunnerImpl.StartPriceEncoder(constants.ExchangeId1, etmp, log.NewNopLogger(), bCh)

	require.Empty(t, etmp.ExchangeMarketPrices[constants.ExchangeId1].MarketToPriceTimestamp)
	require.Empty(t, etmp.ExchangeMarketPrices[constants.ExchangeId2].MarketToPriceTimestamp)
	require.Empty(t, bChMap[constants.ExchangeId1])
	require.Empty(t, bChMap[constants.ExchangeId2])
}

func TestPriceEncoder_WriteToOneMarket(t *testing.T) {
	etmp, bChMap := generateBufferedChannelAndExchangeToMarketPrices(t, constants.Exchange1Exchange2Array)

	runPriceEncoderSequentially(
		t,
		constants.ExchangeId1,
		etmp,
		bChMap[constants.ExchangeId1],
		[]*types.MarketPriceTimestamp{
			constants.Market9_TimeT_Price1,
		},
	)

	require.Len(t, etmp.ExchangeMarketPrices[constants.ExchangeId1].MarketToPriceTimestamp, 1)
	require.Empty(t, etmp.ExchangeMarketPrices[constants.ExchangeId2].MarketToPriceTimestamp)

	require.Equal(
		t,
		&pricefeedtypes.PriceTimestamp{
			Price:          constants.Price1,
			LastUpdateTime: constants.TimeT,
		},
		etmp.ExchangeMarketPrices[constants.ExchangeId1].MarketToPriceTimestamp[constants.MarketId9],
	)
}

func TestPriceEncoder_WriteToTwoMarkets(t *testing.T) {
	etmp, bChMap := generateBufferedChannelAndExchangeToMarketPrices(t, constants.Exchange1Exchange2Array)

	runPriceEncoderSequentially(
		t,
		constants.ExchangeId1,
		etmp,
		bChMap[constants.ExchangeId1],
		[]*types.MarketPriceTimestamp{
			constants.Market9_TimeT_Price1,
			constants.Market8_TimeTMinusThreshold_Price2,
		},
	)

	require.Len(t, etmp.ExchangeMarketPrices[constants.ExchangeId1].MarketToPriceTimestamp, 2)
	require.Empty(t, etmp.ExchangeMarketPrices[constants.ExchangeId2].MarketToPriceTimestamp)

	require.Equal(
		t,
		&pricefeedtypes.PriceTimestamp{
			Price:          constants.Price1,
			LastUpdateTime: constants.TimeT,
		},
		etmp.ExchangeMarketPrices[constants.ExchangeId1].MarketToPriceTimestamp[constants.MarketId9],
	)
	require.Equal(
		t,
		&pricefeedtypes.PriceTimestamp{
			Price:          constants.Price2,
			LastUpdateTime: constants.TimeTMinusThreshold,
		},
		etmp.ExchangeMarketPrices[constants.ExchangeId1].MarketToPriceTimestamp[constants.MarketId8],
	)
}

func TestPriceEncoder_WriteToOneMarketTwice(t *testing.T) {
	etmp, bChMap := generateBufferedChannelAndExchangeToMarketPrices(t, constants.Exchange1Exchange2Array)

	runPriceEncoderSequentially(
		t,
		constants.ExchangeId1,
		etmp,
		bChMap[constants.ExchangeId1],
		[]*types.MarketPriceTimestamp{
			constants.Market9_TimeTMinusThreshold_Price2,
			constants.Market9_TimeT_Price1,
		},
	)

	require.Len(t, etmp.ExchangeMarketPrices[constants.ExchangeId1].MarketToPriceTimestamp, 1)
	require.Empty(t, etmp.ExchangeMarketPrices[constants.ExchangeId2].MarketToPriceTimestamp)

	require.Equal(
		t,
		&pricefeedtypes.PriceTimestamp{
			Price:          constants.Price1,
			LastUpdateTime: constants.TimeT,
		},
		etmp.ExchangeMarketPrices[constants.ExchangeId1].MarketToPriceTimestamp[constants.MarketId9],
	)
}

func TestPriceEncoder_WriteToTwoExchanges(t *testing.T) {
	etmp, bChMap := generateBufferedChannelAndExchangeToMarketPrices(t, constants.Exchange1Exchange2Array)

	runPriceEncoderSequentially(
		t,
		constants.ExchangeId1,
		etmp,
		bChMap[constants.ExchangeId1],
		[]*types.MarketPriceTimestamp{
			constants.Market9_TimeT_Price1,
		},
	)

	runPriceEncoderSequentially(
		t,
		constants.ExchangeId2,
		etmp,
		bChMap[constants.ExchangeId2],
		[]*types.MarketPriceTimestamp{
			constants.Market8_TimeTMinusThreshold_Price2,
		},
	)

	require.Len(t, etmp.ExchangeMarketPrices[constants.ExchangeId1].MarketToPriceTimestamp, 1)
	require.Len(t, etmp.ExchangeMarketPrices[constants.ExchangeId2].MarketToPriceTimestamp, 1)

	require.Equal(
		t,
		&pricefeedtypes.PriceTimestamp{
			Price:          constants.Price1,
			LastUpdateTime: constants.TimeT,
		},
		etmp.ExchangeMarketPrices[constants.ExchangeId1].MarketToPriceTimestamp[constants.MarketId9],
	)
	require.Equal(
		t,
		&pricefeedtypes.PriceTimestamp{
			Price:          constants.Price2,
			LastUpdateTime: constants.TimeTMinusThreshold,
		},
		etmp.ExchangeMarketPrices[constants.ExchangeId2].MarketToPriceTimestamp[constants.MarketId8],
	)
}

func TestPriceEncoder_WriteToTwoExchangesConcurrentlyWithManyUpdates(t *testing.T) {
	etmp, bChMap := generateBufferedChannelAndExchangeToMarketPrices(t, constants.Exchange1Exchange2Array)

	largeMarketWrite := []*types.MarketPriceTimestamp{
		constants.Market8_TimeTMinusThreshold_Price1,
		constants.Market8_TimeTMinusThreshold_Price2,
		constants.Market8_TimeTMinusThreshold_Price3,
		constants.Market9_TimeTMinusThreshold_Price1,
		constants.Market9_TimeTMinusThreshold_Price2,
		constants.Market9_TimeTMinusThreshold_Price3,
		constants.Market8_TimeT_Price3,
		constants.Market9_TimeT_Price1,
		constants.Market9_TimeT_Price2,
		constants.Market9_TimeT_Price3,
		constants.Market9_TimeTPlusThreshold_Price1,
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		runPriceEncoderConcurrently(
			t,
			constants.ExchangeId1,
			etmp,
			bChMap[constants.ExchangeId1],
			largeMarketWrite,
		)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		runPriceEncoderConcurrently(
			t,
			constants.ExchangeId2,
			etmp,
			bChMap[constants.ExchangeId2],
			largeMarketWrite,
		)
	}()

	wg.Wait()

	require.Len(t, etmp.ExchangeMarketPrices[constants.ExchangeId1].MarketToPriceTimestamp, 2)
	require.Len(t, etmp.ExchangeMarketPrices[constants.ExchangeId2].MarketToPriceTimestamp, 2)

	require.Equal(
		t,
		&pricefeedtypes.PriceTimestamp{
			Price:          constants.Price1,
			LastUpdateTime: constants.TimeTPlusThreshold,
		},
		etmp.ExchangeMarketPrices[constants.ExchangeId1].MarketToPriceTimestamp[constants.MarketId9],
	)
	require.Equal(
		t,
		&pricefeedtypes.PriceTimestamp{
			Price:          constants.Price3,
			LastUpdateTime: constants.TimeT,
		},
		etmp.ExchangeMarketPrices[constants.ExchangeId1].MarketToPriceTimestamp[constants.MarketId8],
	)

	require.Equal(
		t,
		&pricefeedtypes.PriceTimestamp{
			Price:          constants.Price1,
			LastUpdateTime: constants.TimeTPlusThreshold,
		},
		etmp.ExchangeMarketPrices[constants.ExchangeId2].MarketToPriceTimestamp[constants.MarketId9],
	)
	require.Equal(
		t,
		&pricefeedtypes.PriceTimestamp{
			Price:          constants.Price3,
			LastUpdateTime: constants.TimeT,
		},
		etmp.ExchangeMarketPrices[constants.ExchangeId2].MarketToPriceTimestamp[constants.MarketId8],
	)
}

func TestPriceUpdater_Mixed(t *testing.T) {
	tests := map[string]struct {
		// parameters
		exchangeAndMarketPrices []*client.ExchangeIdMarketPriceTimestamp
		priceUpdateError        error

		// expectations
		expectedMarketPriceUpdate []*api.MarketPriceUpdate
	}{
		"Update throws": {
			// Throws error due to mock so that we can simulate fail state.
			exchangeAndMarketPrices: []*client.ExchangeIdMarketPriceTimestamp{
				constants.ExchangeId1_Market9_TimeT_Price1,
			},
			priceUpdateError: errors.New("failed to send price update"),
		},
		"No exchange market prices, does not call `UpdateMarketPrices`": {
			exchangeAndMarketPrices: []*client.ExchangeIdMarketPriceTimestamp{},
		},
		"One market for one exchange": {
			exchangeAndMarketPrices: []*client.ExchangeIdMarketPriceTimestamp{
				constants.ExchangeId1_Market9_TimeT_Price1,
			},
			expectedMarketPriceUpdate: constants.Market9_SingleExchange_AtTimeUpdate,
		},
		"Three markets at timeT": {
			exchangeAndMarketPrices: []*client.ExchangeIdMarketPriceTimestamp{
				constants.ExchangeId1_Market9_TimeT_Price1,
				constants.ExchangeId2_Market9_TimeT_Price2,
				constants.ExchangeId2_Market8_TimeT_Price2,
				constants.ExchangeId3_Market8_TimeT_Price3,
				constants.ExchangeId1_Market7_TimeT_Price1,
				constants.ExchangeId3_Market7_TimeT_Price3,
			},
			expectedMarketPriceUpdate: constants.AtTimeTPriceUpdate,
		},
		"Three markets at mixed time": {
			exchangeAndMarketPrices: []*client.ExchangeIdMarketPriceTimestamp{
				constants.ExchangeId1_Market9_TimeT_Price1,
				constants.ExchangeId2_Market9_TimeT_Price2,
				constants.ExchangeId3_Market9_TimeT_Price3,
				constants.ExchangeId1_Market8_BeforeTimeT_Price3,
				constants.ExchangeId2_Market8_TimeT_Price2,
				constants.ExchangeId3_Market8_TimeT_Price3,
				constants.ExchangeId2_Market7_BeforeTimeT_Price1,
				constants.ExchangeId1_Market7_BeforeTimeT_Price3,
				constants.ExchangeId3_Market7_TimeT_Price3,
			},
			expectedMarketPriceUpdate: constants.MixedTimePriceUpdate,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Create `ExchangeIdMarketPriceTimestamp` and populate it with market-price updates.
			etmp, _ := types.NewExchangeToMarketPrices(
				[]types.ExchangeId{
					constants.ExchangeId1,
					constants.ExchangeId2,
					constants.ExchangeId3,
				},
			)
			for _, exchangeAndMarketPrice := range tc.exchangeAndMarketPrices {
				etmp.UpdatePrice(
					exchangeAndMarketPrice.ExchangeId,
					exchangeAndMarketPrice.MarketPriceTimestamp,
				)
			}

			// Create a mock `PriceFeedServiceClient` and run `RunPriceUpdaterTaskLoop`.
			mockPriceFeedClient := generateMockQueryClient()
			mockPriceFeedClient.On("UpdateMarketPrices", grpc_util.Ctx, mock.Anything).
				Return(nil, tc.priceUpdateError)

			err := RunPriceUpdaterTaskLoop(
				grpc_util.Ctx,
				etmp,
				mockPriceFeedClient,
				log.NewNopLogger(),
			)
			require.Equal(
				t,
				tc.priceUpdateError,
				err,
			)

			// We sort the `expectedUpdates` as ordering is not guaranteed.
			// We then verify `UpdateMarketPrices` was called with an update that, when sorted, matches
			// the sorted `expectedUpdates`.
			expectedUpdates := tc.expectedMarketPriceUpdate
			sortMarketPriceUpdateByMarketIdDescending(expectedUpdates)

			if tc.expectedMarketPriceUpdate != nil {
				mockPriceFeedClient.AssertCalled(
					t,
					"UpdateMarketPrices",
					grpc_util.Ctx,
					mock.MatchedBy(func(i interface{}) bool {
						param := i.(*api.UpdateMarketPricesRequest)
						updates := param.MarketPriceUpdates
						sortMarketPriceUpdateByMarketIdDescending(updates)

						for i, update := range updates {
							prices := update.ExchangePrices
							require.ElementsMatch(
								t,
								expectedUpdates[i].ExchangePrices,
								prices,
							)
						}
						return true
					}),
				)
			} else {
				mockPriceFeedClient.AssertNotCalled(t, "UpdateMarketPrices")
			}
		})
	}
}

// TestMarketUpdater_Mixed tests the `RunMarketParamUpdaterTaskLoop` function invokes the grpc
// query to the prices query client and that if the query succeeds, the config is updated.
func TestMarketUpdater_Mixed(t *testing.T) {
	tests := map[string]struct {
		queryError error
	}{
		"Failure: query fails": {
			queryError: fmt.Errorf("query failed"),
		},
		"Success: query succeeds": {},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Create a mock `PriceFeedServiceClient`, a mock `PricefeedMutableMarketConfigs`, and run
			// `RunMarketParamUpdaterTaskLoop`.
			params := []pricetypes.MarketParam{}
			response := &pricetypes.QueryAllMarketParamsResponse{
				MarketParams: params,
			}
			pricesQueryClient := generateMockQueryClient()
			pricesQueryClient.On("AllMarketParams", grpc_util.Ctx, mock.Anything).
				Return(response, tc.queryError)
			configs := &mocks.PricefeedMutableMarketConfigs{}
			configs.On("UpdateMarkets", params).Return(tc.queryError)

			RunMarketParamUpdaterTaskLoop(
				grpc_util.Ctx,
				configs,
				pricesQueryClient,
				log.NewNopLogger(),
			)
			pricesQueryClient.AssertCalled(t, "AllMarketParams", grpc_util.Ctx, mock.Anything)
			if tc.queryError == nil {
				configs.AssertCalled(t, "UpdateMarkets", params)
			} else {
				configs.AssertNotCalled(t, "UpdateMarkets", params)
			}
		})
	}
}

// ----------------- Generate Mock Instances ----------------- //

// generateMockQueryClient generates a mock QueryClient that can be used to support any of the QueryClient
// interfaces added to the mocks.QueryClient class, including the prices query client and the
// pricefeed service client.
func generateMockQueryClient() *mocks.QueryClient {
	mockPriceFeedServiceClient := &mocks.QueryClient{}

	return mockPriceFeedServiceClient
}

// ----------------- Helper Functions ----------------- //

func generateBufferedChannelAndExchangeToMarketPrices(
	t *testing.T,
	exchangeIds []types.ExchangeId,
) (
	*types.ExchangeToMarketPrices,
	map[types.ExchangeId]chan *price_fetcher.PriceFetcherSubtaskResponse,
) {
	etmp, _ := types.NewExchangeToMarketPrices(exchangeIds)

	exhangeIdToBufferedChannel :=
		map[types.ExchangeId]chan *price_fetcher.PriceFetcherSubtaskResponse{}
	for _, exchangeId := range exchangeIds {
		bCh := make(chan *price_fetcher.PriceFetcherSubtaskResponse, maxBufferedChannelLength)
		exhangeIdToBufferedChannel[exchangeId] = bCh
	}

	require.Len(t, etmp.ExchangeMarketPrices, len(exchangeIds))
	return etmp, exhangeIdToBufferedChannel
}

func runPriceEncoderSequentially(
	t *testing.T,
	exchangeId types.ExchangeId,
	etmp *types.ExchangeToMarketPrices,
	bCh chan *price_fetcher.PriceFetcherSubtaskResponse,
	writes []*types.MarketPriceTimestamp,
) {
	// Make sure there are not more write than the `bufferedChannel` can hold.
	require.True(t, len(writes) <= maxBufferedChannelLength)

	for _, write := range writes {
		bCh <- &price_fetcher.PriceFetcherSubtaskResponse{
			Price: write,
			Err:   nil,
		}
	}

	close(bCh)
	subTaskRunnerImpl.StartPriceEncoder(exchangeId, etmp, log.NewNopLogger(), bCh)
}

func runPriceEncoderConcurrently(
	t *testing.T,
	exchangeId types.ExchangeId,
	etmp *types.ExchangeToMarketPrices,
	bCh chan *price_fetcher.PriceFetcherSubtaskResponse,
	writes []*types.MarketPriceTimestamp,
) {
	// Start a `waitGroup` for the `PriceEncoder` which will complete when the `bufferedChannel`
	// is empty and is closed.
	var priceEncoderWg sync.WaitGroup
	priceEncoderWg.Add(1)
	go func() {
		defer priceEncoderWg.Done()
		subTaskRunnerImpl.StartPriceEncoder(exchangeId, etmp, log.NewNopLogger(), bCh)
	}()

	// Start a `waitGroup` for threads that will write to the `bufferedChannel`.
	var writeWg sync.WaitGroup
	for _, write := range writes {
		writeWg.Add(1)
		go func(write *types.MarketPriceTimestamp) {
			defer writeWg.Done()
			bCh <- &price_fetcher.PriceFetcherSubtaskResponse{
				Price: write,
				Err:   nil,
			}
		}(write)
	}

	writeWg.Wait()
	close(bCh)
	priceEncoderWg.Wait()
}

func sortMarketPriceUpdateByMarketIdDescending(
	marketPriceUpdate []*api.MarketPriceUpdate,
) {
	sort.Slice(
		marketPriceUpdate,
		func(i, j int) bool {
			return marketPriceUpdate[i].MarketId > marketPriceUpdate[j].MarketId
		},
	)
}