package indexer_manager_test

import (
	"testing"

	storetypes "github.com/cosmos/cosmos-sdk/store/types"
	"github.com/cosmos/cosmos-sdk/types"
	indexerevents "github.com/dydxprotocol/v4/indexer/events"
	"github.com/dydxprotocol/v4/indexer/indexer_manager"
	"github.com/dydxprotocol/v4/indexer/msgsender"
	"github.com/dydxprotocol/v4/mocks"
	"github.com/dydxprotocol/v4/testutil/constants"
	"github.com/dydxprotocol/v4/testutil/sdk"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var ExpectedEvent0 = indexer_manager.IndexerTendermintEvent{
	Subtype: indexerevents.SubtypeOrderFill,
	Data: indexer_manager.GetB64EncodedEventMessage(
		&OrderFillEvent,
	),
	OrderingWithinBlock: &indexer_manager.IndexerTendermintEvent_TransactionIndex{
		TransactionIndex: 0,
	},
	EventIndex: 0,
}

var ExpectedEvent1 = indexer_manager.IndexerTendermintEvent{
	Subtype: indexerevents.SubtypeSubaccountUpdate,
	Data: indexer_manager.GetB64EncodedEventMessage(
		&SubaccountEvent,
	),
	OrderingWithinBlock: &indexer_manager.IndexerTendermintEvent_TransactionIndex{
		TransactionIndex: 0,
	},
	EventIndex: 1,
}

var ExpectedEvent2 = indexer_manager.IndexerTendermintEvent{
	Subtype: indexerevents.SubtypeTransfer,
	Data: indexer_manager.GetB64EncodedEventMessage(
		&TransferEvent,
	),
	OrderingWithinBlock: &indexer_manager.IndexerTendermintEvent_TransactionIndex{
		TransactionIndex: 1,
	},
	EventIndex: 0,
}

var ExpectedEvent3 = indexer_manager.IndexerTendermintEvent{
	Subtype: indexerevents.SubtypeFundingValues,
	Data: indexer_manager.GetB64EncodedEventMessage(
		&FundingRateAndIndexEvent,
	),
	OrderingWithinBlock: &indexer_manager.IndexerTendermintEvent_BlockEvent_{
		BlockEvent: indexer_manager.IndexerTendermintEvent_BLOCK_EVENT_END_BLOCK,
	},
}

var ExpectedEvent4 = indexer_manager.IndexerTendermintEvent{
	Subtype: indexerevents.SubtypeFundingValues,
	Data: indexer_manager.GetB64EncodedEventMessage(
		&FundingPremiumSampleEvent,
	),
	OrderingWithinBlock: &indexer_manager.IndexerTendermintEvent_BlockEvent_{
		BlockEvent: indexer_manager.IndexerTendermintEvent_BLOCK_EVENT_END_BLOCK,
	},
}

func assertIsEnabled(t *testing.T, isEnabled bool) {
	storeKey := types.NewTransientStoreKey(indexer_manager.TransientStoreKey)
	mockMsgSender := &mocks.IndexerMessageSender{}
	mockMsgSender.On("Enabled").Return(isEnabled)
	indexerEventManager := indexer_manager.NewIndexerEventManager(mockMsgSender, storeKey, isEnabled)
	require.Equal(t, isEnabled, indexerEventManager.Enabled())
}

func TestIsEnabled(t *testing.T) {
	assertIsEnabled(t, true)
	assertIsEnabled(t, false)
}

func TestSendOffchainData(t *testing.T) {
	storeKey := types.NewTransientStoreKey(indexer_manager.TransientStoreKey)
	mockMsgSender := &mocks.IndexerMessageSender{}
	mockMsgSender.On("Enabled").Return(true)
	mockMsgSender.On("SendOffchainData", mock.Anything).Return(nil)
	indexerEventManager := indexer_manager.NewIndexerEventManager(mockMsgSender, storeKey, true)
	var message msgsender.Message
	indexerEventManager.SendOffchainData(message)
	mockMsgSender.AssertExpectations(t)
}

func TestSendOnchainData(t *testing.T) {
	storeKey := types.NewTransientStoreKey(indexer_manager.TransientStoreKey)
	indexerTendermintBlock := &indexer_manager.IndexerTendermintBlock{}
	mockMsgSender := &mocks.IndexerMessageSender{}
	mockMsgSender.On("Enabled").Return(true)
	mockMsgSender.On("SendOnchainData", mock.Anything).Return(nil)
	indexerEventManager := indexer_manager.NewIndexerEventManager(mockMsgSender, storeKey, true)
	indexerEventManager.SendOnchainData(indexerTendermintBlock)
	mockMsgSender.AssertExpectations(t)
}

func TestProduceBlockBasicTxnEvent(t *testing.T) {
	ctx, stateStore, db := sdk.NewSdkContextWithMultistore()
	storeKey := types.NewTransientStoreKey(indexer_manager.TransientStoreKey)
	stateStore.MountStoreWithDB(storeKey, storetypes.StoreTypeTransient, db)
	ctx = ctx.WithBlockTime(BlockTime).WithBlockHeight(BlockHeight).WithTxBytes(constants.TestTxBytes)
	ctx.GasMeter().ConsumeGas(ConsumedGas, "beforeWrite")
	require.NoError(t, stateStore.LoadLatestVersion())
	mockMsgSender := &mocks.IndexerMessageSender{}
	mockMsgSender.On("Enabled").Return(true)
	indexerEventManager := indexer_manager.NewIndexerEventManager(mockMsgSender, storeKey, true)
	indexerEventManager.AddTxnEvent(
		ctx,
		indexerevents.SubtypeOrderFill,
		indexer_manager.GetB64EncodedEventMessage(
			&OrderFillEvent,
		),
	)

	block := indexerEventManager.ProduceBlock(ctx)
	require.Len(t, block.Events, 1)
	require.Equal(t, ExpectedEvent0, *block.Events[0])
	require.Equal(t, []string{string(constants.TestTxHashString)}, block.TxHashes)
	require.Equal(t, uint32(BlockHeight), block.Height)
	require.Equal(t, BlockTime, block.Time)
	require.Equal(t, ConsumedGas, ctx.GasMeter().GasConsumed())
}

func TestProduceBlockBasicBlockEvent(t *testing.T) {
	ctx, stateStore, db := sdk.NewSdkContextWithMultistore()
	storeKey := types.NewTransientStoreKey(indexer_manager.TransientStoreKey)
	stateStore.MountStoreWithDB(storeKey, storetypes.StoreTypeTransient, db)
	ctx = ctx.WithBlockTime(BlockTime).WithBlockHeight(BlockHeight)
	ctx.GasMeter().ConsumeGas(ConsumedGas, "beforeWrite")
	require.NoError(t, stateStore.LoadLatestVersion())
	mockMsgSender := &mocks.IndexerMessageSender{}
	mockMsgSender.On("Enabled").Return(true)
	indexerEventManager := indexer_manager.NewIndexerEventManager(mockMsgSender, storeKey, true)
	indexerEventManager.AddBlockEvent(
		ctx,
		indexerevents.SubtypeFundingValues,
		indexer_manager.GetB64EncodedEventMessage(
			&FundingRateAndIndexEvent,
		),
		indexer_manager.IndexerTendermintEvent_BLOCK_EVENT_END_BLOCK,
	)

	block := indexerEventManager.ProduceBlock(ctx)
	require.Len(t, block.Events, 1)
	require.Equal(t, ExpectedEvent3, *block.Events[0])
	require.Empty(t, block.TxHashes)
	require.Equal(t, uint32(BlockHeight), block.Height)
	require.Equal(t, BlockTime, block.Time)
	require.Equal(t, ConsumedGas, ctx.GasMeter().GasConsumed())
}

func TestProduceBlockMultipleTxnEvents(t *testing.T) {
	ctx, stateStore, db := sdk.NewSdkContextWithMultistore()
	storeKey := types.NewTransientStoreKey(indexer_manager.TransientStoreKey)
	stateStore.MountStoreWithDB(storeKey, storetypes.StoreTypeTransient, db)
	ctx = ctx.WithBlockTime(BlockTime).WithBlockHeight(BlockHeight).WithTxBytes(constants.TestTxBytes)
	ctx.GasMeter().ConsumeGas(ConsumedGas, "beforeWrite")
	require.NoError(t, stateStore.LoadLatestVersion())
	mockMsgSender := &mocks.IndexerMessageSender{}
	mockMsgSender.On("Enabled").Return(true)
	indexerEventManager := indexer_manager.NewIndexerEventManager(mockMsgSender, storeKey, true)
	indexerEventManager.AddTxnEvent(
		ctx,
		indexerevents.SubtypeOrderFill,
		indexer_manager.GetB64EncodedEventMessage(
			&OrderFillEvent,
		),
	)
	indexerEventManager.AddTxnEvent(
		ctx,
		indexerevents.SubtypeSubaccountUpdate,
		indexer_manager.GetB64EncodedEventMessage(
			&SubaccountEvent,
		),
	)
	ctx = ctx.WithTxBytes(constants.TestTxBytes1)
	indexerEventManager.AddTxnEvent(
		ctx,
		indexerevents.SubtypeTransfer,
		indexer_manager.GetB64EncodedEventMessage(
			&TransferEvent,
		),
	)

	block := indexerEventManager.ProduceBlock(ctx)
	require.Len(t, block.Events, 3)
	require.Equal(t, ExpectedEvent0, *block.Events[0])
	require.Equal(t, ExpectedEvent1, *block.Events[1])
	require.Equal(t, ExpectedEvent2, *block.Events[2])
	require.Equal(t, []string{
		string(constants.TestTxHashString),
		string(constants.TestTxHashString1),
	}, block.TxHashes)
	require.Equal(t, uint32(BlockHeight), block.Height)
	require.Equal(t, BlockTime, block.Time)
	require.Equal(t, ConsumedGas, ctx.GasMeter().GasConsumed())
}

func TestProduceBlockMultipleTxnAndBlockEvents(t *testing.T) {
	ctx, stateStore, db := sdk.NewSdkContextWithMultistore()
	storeKey := types.NewTransientStoreKey(indexer_manager.TransientStoreKey)
	stateStore.MountStoreWithDB(storeKey, storetypes.StoreTypeTransient, db)
	ctx = ctx.WithBlockTime(BlockTime).WithBlockHeight(BlockHeight).WithTxBytes(constants.TestTxBytes)
	ctx.GasMeter().ConsumeGas(ConsumedGas, "beforeWrite")
	require.NoError(t, stateStore.LoadLatestVersion())
	mockMsgSender := &mocks.IndexerMessageSender{}
	mockMsgSender.On("Enabled").Return(true)
	indexerEventManager := indexer_manager.NewIndexerEventManager(mockMsgSender, storeKey, true)
	indexerEventManager.AddTxnEvent(
		ctx,
		indexerevents.SubtypeOrderFill,
		indexer_manager.GetB64EncodedEventMessage(
			&OrderFillEvent,
		),
	)
	indexerEventManager.AddTxnEvent(
		ctx,
		indexerevents.SubtypeSubaccountUpdate,
		indexer_manager.GetB64EncodedEventMessage(
			&SubaccountEvent,
		),
	)
	ctx = ctx.WithTxBytes(constants.TestTxBytes1)
	indexerEventManager.AddTxnEvent(
		ctx,
		indexerevents.SubtypeTransfer,
		indexer_manager.GetB64EncodedEventMessage(
			&TransferEvent,
		),
	)
	indexerEventManager.AddBlockEvent(
		ctx,
		indexerevents.SubtypeFundingValues,
		indexer_manager.GetB64EncodedEventMessage(
			&FundingRateAndIndexEvent,
		),
		indexer_manager.IndexerTendermintEvent_BLOCK_EVENT_END_BLOCK,
	)
	indexerEventManager.AddBlockEvent(
		ctx,
		indexerevents.SubtypeFundingValues,
		indexer_manager.GetB64EncodedEventMessage(
			&FundingPremiumSampleEvent,
		),
		indexer_manager.IndexerTendermintEvent_BLOCK_EVENT_END_BLOCK,
	)

	block := indexerEventManager.ProduceBlock(ctx)
	require.Len(t, block.Events, 5)
	require.Equal(t, ExpectedEvent0, *block.Events[0])
	require.Equal(t, ExpectedEvent1, *block.Events[1])
	require.Equal(t, ExpectedEvent2, *block.Events[2])
	require.Equal(t, ExpectedEvent3, *block.Events[3])
	require.Equal(t, ExpectedEvent4, *block.Events[4])
	require.Equal(t, []string{
		string(constants.TestTxHashString),
		string(constants.TestTxHashString1),
	}, block.TxHashes)
	require.Equal(t, uint32(BlockHeight), block.Height)
	require.Equal(t, BlockTime, block.Time)
	require.Equal(t, ConsumedGas, ctx.GasMeter().GasConsumed())
}