package keeper_test

import (
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/dydxprotocol/v4/dtypes"
	indexerevents "github.com/dydxprotocol/v4/indexer/events"
	"github.com/dydxprotocol/v4/indexer/indexer_manager"
	"github.com/dydxprotocol/v4/mocks"
	clobtest "github.com/dydxprotocol/v4/testutil/clob"
	"github.com/dydxprotocol/v4/testutil/constants"
	keepertest "github.com/dydxprotocol/v4/testutil/keeper"
	"github.com/dydxprotocol/v4/x/clob/memclob"
	"github.com/dydxprotocol/v4/x/clob/types"
	sakeeper "github.com/dydxprotocol/v4/x/subaccounts/keeper"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	feetierstypes "github.com/dydxprotocol/v4/x/feetiers/types"
	perptypes "github.com/dydxprotocol/v4/x/perpetuals/types"
	satypes "github.com/dydxprotocol/v4/x/subaccounts/types"
)

type processProposerOperationsTestCase struct {
	// State
	perpetuals                []*perptypes.Perpetual
	perpetualFeeParams        *feetierstypes.PerpetualFeeParams
	clobPairs                 []types.ClobPair
	subaccounts               []satypes.Subaccount
	preExistingStatefulOrders []types.Order
	rawOperations             []types.OperationRaw

	// Liquidation specific setup.
	liquidationConfig    *types.LiquidationsConfig
	insuranceFundBalance uint64

	// Expectations.
	// Note that for expectedProcessProposerMatchesEvents, the OperationsProposedInLastBlock field is populated from
	// the operations field above.
	expectedProcessProposerMatchesEvents types.ProcessProposerMatchesEvents
	expectedMatches                      []*types.MatchWithOrders
	expectedQuoteBalances                map[satypes.SubaccountId]int64
	expectedPerpetualPositions           map[satypes.SubaccountId][]*satypes.PerpetualPosition
	expectedError                        error
}

func TestProcessProposerOperations(t *testing.T) {
	blockHeight := uint32(5)
	tests := map[string]processProposerOperationsTestCase{
		"Succeeds no operations": {
			perpetuals:                []*perptypes.Perpetual{},
			perpetualFeeParams:        &constants.PerpetualFeeParams,
			clobPairs:                 []types.ClobPair{},
			subaccounts:               []satypes.Subaccount{},
			preExistingStatefulOrders: []types.Order{},
			rawOperations:             []types.OperationRaw{},

			expectedProcessProposerMatchesEvents: types.ProcessProposerMatchesEvents{
				BlockHeight: blockHeight,
			},
		},
		"Succeeds no operations with previous stateful orders": {
			perpetuals: []*perptypes.Perpetual{
				&constants.BtcUsd_100PercentMarginRequirement,
			},
			perpetualFeeParams: &constants.PerpetualFeeParams,
			clobPairs: []types.ClobPair{
				constants.ClobPair_Btc,
			},
			subaccounts: []satypes.Subaccount{},
			preExistingStatefulOrders: []types.Order{
				constants.ConditionalOrder_Alice_Num0_Id0_Clob0_Buy5_Price10_GTBT15_StopLoss20,
				constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy5_Price10_GTBT15,
			},
			rawOperations: []types.OperationRaw{},

			expectedProcessProposerMatchesEvents: types.ProcessProposerMatchesEvents{
				BlockHeight: blockHeight,
			},
		},
		"Succeeds with singular match of a short term maker and short term taker": {
			perpetuals: []*perptypes.Perpetual{
				&constants.BtcUsd_100PercentMarginRequirement,
			},
			perpetualFeeParams: &constants.PerpetualFeeParams,
			clobPairs: []types.ClobPair{
				constants.ClobPair_Btc,
			},
			subaccounts: []satypes.Subaccount{
				{
					Id: &constants.Alice_Num0,
					AssetPositions: []*satypes.AssetPosition{
						&constants.Usdc_Asset_100_000,
					},
					PerpetualPositions: []*satypes.PerpetualPosition{
						{
							PerpetualId: 0,
							Quantums:    dtypes.NewInt(1_000_000_000), // 10 BTC
						},
					},
				},
				{
					Id: &constants.Bob_Num0,
					AssetPositions: []*satypes.AssetPosition{
						&constants.Usdc_Asset_100_000,
					},
					PerpetualPositions: []*satypes.PerpetualPosition{
						{
							PerpetualId: 0,
							Quantums:    dtypes.NewInt(1_000_000_000), // 10 BTC
						},
					},
				},
			},
			preExistingStatefulOrders: []types.Order{},
			rawOperations: []types.OperationRaw{
				clobtest.NewShortTermOrderPlacementOperationRaw(
					types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Alice_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_BUY,
						Quantums:     100_000_000, // 1 BTC
						Subticks:     50_000_000,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},
				),
				clobtest.NewShortTermOrderPlacementOperationRaw(
					types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Bob_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_SELL,
						Quantums:     100_000_000, // 1 BTC
						Subticks:     50_000_000,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},
				),
				clobtest.NewMatchOperationRaw(
					&types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Bob_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_SELL,
						Quantums:     100_000_000, // 1 BTC
						Subticks:     50_000_000,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},
					[]types.MakerFill{
						{
							FillAmount:   100_000_000,
							MakerOrderId: types.OrderId{SubaccountId: constants.Alice_Num0, ClientId: 14, ClobPairId: 0},
						},
					},
				),
			},
			expectedMatches: []*types.MatchWithOrders{
				{
					TakerOrder: &types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Bob_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_SELL,
						Quantums:     100_000_000,
						Subticks:     50_000_000,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},
					MakerOrder: &types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Alice_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_BUY,
						Quantums:     100_000_000,
						Subticks:     50_000_000,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},
					FillAmount: 100_000_000,
					MakerFee:   10_000,
					TakerFee:   25_000,
				},
			},
			expectedProcessProposerMatchesEvents: types.ProcessProposerMatchesEvents{
				OrdersIdsFilledInLastBlock: []types.OrderId{
					{SubaccountId: constants.Bob_Num0, ClientId: 14, ClobPairId: 0},
					{SubaccountId: constants.Alice_Num0, ClientId: 14, ClobPairId: 0},
				},
				BlockHeight: blockHeight,
			},
			// Expected balances are initial balance + balance change due to order - fees
			expectedQuoteBalances: map[satypes.SubaccountId]int64{
				constants.Alice_Num0: constants.Usdc_Asset_100_000.GetBigQuantums().Int64() - 50_000_000 - 10_000,
				constants.Bob_Num0:   constants.Usdc_Asset_100_000.GetBigQuantums().Int64() + 50_000_000 - 25_000,
			},
			expectedPerpetualPositions: map[satypes.SubaccountId][]*satypes.PerpetualPosition{
				constants.Bob_Num0: {
					{
						PerpetualId:  0,
						Quantums:     dtypes.NewInt(1_000_000_000 - 100_000_000),
						FundingIndex: dtypes.ZeroInt(),
					},
				},
				constants.Alice_Num0: {
					{
						PerpetualId:  0,
						Quantums:     dtypes.NewInt(1_000_000_000 + 100_000_000),
						FundingIndex: dtypes.ZeroInt(),
					},
				},
			},
		},
		"Succeeds with maker rebate": {
			perpetuals: []*perptypes.Perpetual{
				&constants.BtcUsd_100PercentMarginRequirement,
			},
			perpetualFeeParams: &constants.PerpetualFeeParamsMakerRebate,
			clobPairs: []types.ClobPair{
				constants.ClobPair_Btc,
			},
			subaccounts: []satypes.Subaccount{
				{
					Id: &constants.Alice_Num0,
					AssetPositions: []*satypes.AssetPosition{
						&constants.Usdc_Asset_100_000,
					},
					PerpetualPositions: []*satypes.PerpetualPosition{
						{
							PerpetualId: 0,
							Quantums:    dtypes.NewInt(1_000_000_000), // 10 BTC
						},
					},
				},
				{
					Id: &constants.Bob_Num0,
					AssetPositions: []*satypes.AssetPosition{
						&constants.Usdc_Asset_100_000,
					},
					PerpetualPositions: []*satypes.PerpetualPosition{
						{
							PerpetualId: 0,
							Quantums:    dtypes.NewInt(1_000_000_000), // 10 BTC
						},
					},
				},
			},
			preExistingStatefulOrders: []types.Order{},
			rawOperations: []types.OperationRaw{
				clobtest.NewShortTermOrderPlacementOperationRaw(
					types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Alice_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_BUY,
						Quantums:     100_000_000, // 1 BTC
						Subticks:     50_000_000,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},
				),
				clobtest.NewShortTermOrderPlacementOperationRaw(
					types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Bob_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_SELL,
						Quantums:     100_000_000, // 1 BTC
						Subticks:     50_000_000,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},
				),
				clobtest.NewMatchOperationRaw(
					&types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Bob_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_SELL,
						Quantums:     100_000_000, // 1 BTC
						Subticks:     50_000_000,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},
					[]types.MakerFill{
						{
							FillAmount:   100_000_000,
							MakerOrderId: types.OrderId{SubaccountId: constants.Alice_Num0, ClientId: 14, ClobPairId: 0},
						},
					},
				),
			},
			expectedMatches: []*types.MatchWithOrders{
				{
					TakerOrder: &types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Bob_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_SELL,
						Quantums:     100_000_000,
						Subticks:     50_000_000,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},
					MakerOrder: &types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Alice_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_BUY,
						Quantums:     100_000_000,
						Subticks:     50_000_000,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},
					FillAmount: 100_000_000,
					MakerFee:   -10_000,
					TakerFee:   25_000,
				},
			},
			expectedProcessProposerMatchesEvents: types.ProcessProposerMatchesEvents{
				OrdersIdsFilledInLastBlock: []types.OrderId{
					{SubaccountId: constants.Bob_Num0, ClientId: 14, ClobPairId: 0},
					{SubaccountId: constants.Alice_Num0, ClientId: 14, ClobPairId: 0},
				},
				BlockHeight: blockHeight,
			},
			// Expected balances are initial balance + balance change due to order - fees
			expectedQuoteBalances: map[satypes.SubaccountId]int64{
				constants.Alice_Num0: constants.Usdc_Asset_100_000.GetBigQuantums().Int64() - 50_000_000 + 10_000,
				constants.Bob_Num0:   constants.Usdc_Asset_100_000.GetBigQuantums().Int64() + 50_000_000 - 25_000,
			},
			expectedPerpetualPositions: map[satypes.SubaccountId][]*satypes.PerpetualPosition{
				constants.Bob_Num0: {
					{
						PerpetualId:  0,
						Quantums:     dtypes.NewInt(1_000_000_000 - 100_000_000),
						FundingIndex: dtypes.ZeroInt(),
					},
				},
				constants.Alice_Num0: {
					{
						PerpetualId:  0,
						Quantums:     dtypes.NewInt(1_000_000_000 + 100_000_000),
						FundingIndex: dtypes.ZeroInt(),
					},
				},
			},
		},
		"Succeeds with singular match of a preexisting maker and short term taker": {
			perpetuals: []*perptypes.Perpetual{
				&constants.BtcUsd_100PercentMarginRequirement,
			},
			perpetualFeeParams: &constants.PerpetualFeeParams,
			clobPairs: []types.ClobPair{
				constants.ClobPair_Btc,
			},
			subaccounts: []satypes.Subaccount{
				{
					Id: &constants.Alice_Num0,
					AssetPositions: []*satypes.AssetPosition{
						&constants.Usdc_Asset_100_000,
					},
					PerpetualPositions: []*satypes.PerpetualPosition{
						{
							PerpetualId: 0,
							Quantums:    dtypes.NewInt(1_000_000_000), // 10 BTC
						},
					},
				},
				{
					Id: &constants.Bob_Num0,
					AssetPositions: []*satypes.AssetPosition{
						&constants.Usdc_Asset_100_000,
					},
					PerpetualPositions: []*satypes.PerpetualPosition{
						{
							PerpetualId: 0,
							Quantums:    dtypes.NewInt(1_000_000_000), // 10 BTC
						},
					},
				},
			},
			preExistingStatefulOrders: []types.Order{
				constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy5_Price10_GTBT15,
			},
			rawOperations: []types.OperationRaw{
				clobtest.NewShortTermOrderPlacementOperationRaw(
					types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Bob_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_SELL,
						Quantums:     10,
						Subticks:     10,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},
				),
				clobtest.NewMatchOperationRaw(
					&types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Bob_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_SELL,
						Quantums:     10,
						Subticks:     10,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},
					[]types.MakerFill{
						{
							FillAmount:   5,
							MakerOrderId: constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy5_Price10_GTBT15.GetOrderId(),
						},
					},
				),
			},
			expectedMatches: []*types.MatchWithOrders{
				{
					TakerOrder: &types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Bob_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_SELL,
						Quantums:     10,
						Subticks:     10,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},
					MakerOrder: &constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy5_Price10_GTBT15,
					FillAmount: 5,
				},
			},
			expectedProcessProposerMatchesEvents: types.ProcessProposerMatchesEvents{
				OrdersIdsFilledInLastBlock: []types.OrderId{
					{SubaccountId: constants.Bob_Num0, ClientId: 14, ClobPairId: 0},
					constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy5_Price10_GTBT15.GetOrderId(),
				},
				RemovedStatefulOrderIds: []types.OrderId{
					constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy5_Price10_GTBT15.GetOrderId(),
				},
				BlockHeight: blockHeight,
			},
			expectedQuoteBalances: map[satypes.SubaccountId]int64{
				constants.Alice_Num0: constants.Usdc_Asset_100_000.GetBigQuantums().Int64(),
				constants.Bob_Num0:   constants.Usdc_Asset_100_000.GetBigQuantums().Int64(),
			},
			expectedPerpetualPositions: map[satypes.SubaccountId][]*satypes.PerpetualPosition{
				constants.Bob_Num0: {
					{
						PerpetualId:  0,
						Quantums:     dtypes.NewInt(1_000_000_000 - 5),
						FundingIndex: dtypes.ZeroInt(),
					},
				},
				constants.Alice_Num0: {
					{
						PerpetualId:  0,
						Quantums:     dtypes.NewInt(1_000_000_000 + 5),
						FundingIndex: dtypes.ZeroInt(),
					},
				},
			},
		},
		"Succeeds with singular match of a preexisting maker and newly placed long term taker": {
			perpetuals: []*perptypes.Perpetual{
				&constants.BtcUsd_100PercentMarginRequirement,
			},
			perpetualFeeParams: &constants.PerpetualFeeParams,
			clobPairs: []types.ClobPair{
				constants.ClobPair_Btc,
			},
			subaccounts: []satypes.Subaccount{
				{
					Id: &constants.Alice_Num0,
					AssetPositions: []*satypes.AssetPosition{
						&constants.Usdc_Asset_100_000,
					},
					PerpetualPositions: []*satypes.PerpetualPosition{
						{
							PerpetualId: 0,
							Quantums:    dtypes.NewInt(1_000_000_000), // 10 BTC
						},
					},
				},
				{
					Id: &constants.Bob_Num0,
					AssetPositions: []*satypes.AssetPosition{
						&constants.Usdc_Asset_100_000,
					},
					PerpetualPositions: []*satypes.PerpetualPosition{
						{
							PerpetualId: 0,
							Quantums:    dtypes.NewInt(1_000_000_000), // 10 BTC
						},
					},
				},
			},
			preExistingStatefulOrders: []types.Order{
				constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy5_Price10_GTBT15,
				constants.LongTermOrder_Bob_Num0_Id1_Clob0_Sell50_Price10_GTBT15,
			},
			rawOperations: []types.OperationRaw{
				clobtest.NewMatchOperationRaw(
					&constants.LongTermOrder_Bob_Num0_Id1_Clob0_Sell50_Price10_GTBT15,
					[]types.MakerFill{
						{
							FillAmount:   5,
							MakerOrderId: constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy5_Price10_GTBT15.GetOrderId(),
						},
					},
				),
			},
			expectedMatches: []*types.MatchWithOrders{
				{
					TakerOrder: &constants.LongTermOrder_Bob_Num0_Id1_Clob0_Sell50_Price10_GTBT15,
					MakerOrder: &constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy5_Price10_GTBT15,
					FillAmount: 5,
				},
			},

			expectedProcessProposerMatchesEvents: types.ProcessProposerMatchesEvents{
				OrdersIdsFilledInLastBlock: []types.OrderId{
					constants.LongTermOrder_Bob_Num0_Id1_Clob0_Sell50_Price10_GTBT15.GetOrderId(),
					constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy5_Price10_GTBT15.GetOrderId(),
				},
				RemovedStatefulOrderIds: []types.OrderId{
					constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy5_Price10_GTBT15.GetOrderId(),
				},
				BlockHeight: blockHeight,
			},
			expectedQuoteBalances: map[satypes.SubaccountId]int64{
				constants.Alice_Num0: constants.Usdc_Asset_100_000.GetBigQuantums().Int64(),
				constants.Bob_Num0:   constants.Usdc_Asset_100_000.GetBigQuantums().Int64(),
			},
			expectedPerpetualPositions: map[satypes.SubaccountId][]*satypes.PerpetualPosition{
				constants.Bob_Num0: {
					{
						PerpetualId:  0,
						Quantums:     dtypes.NewInt(1_000_000_000 - 5),
						FundingIndex: dtypes.ZeroInt(),
					},
				},
				constants.Alice_Num0: {
					{
						PerpetualId:  0,
						Quantums:     dtypes.NewInt(1_000_000_000 + 5),
						FundingIndex: dtypes.ZeroInt(),
					},
				},
			},
		},
		"preexisting stateful maker order partially matches with 2 short term taker orders": {
			perpetuals: []*perptypes.Perpetual{
				&constants.BtcUsd_100PercentMarginRequirement,
			},
			perpetualFeeParams: &constants.PerpetualFeeParams,
			clobPairs: []types.ClobPair{
				constants.ClobPair_Btc,
			},
			subaccounts: []satypes.Subaccount{
				{
					Id: &constants.Alice_Num0,
					AssetPositions: []*satypes.AssetPosition{
						&constants.Usdc_Asset_100_000,
					},
					PerpetualPositions: []*satypes.PerpetualPosition{
						{
							PerpetualId: 0,
							Quantums:    dtypes.NewInt(1_000_000_000), // 10 BTC
						},
					},
				},
				{
					Id: &constants.Bob_Num0,
					AssetPositions: []*satypes.AssetPosition{
						&constants.Usdc_Asset_100_000,
					},
					PerpetualPositions: []*satypes.PerpetualPosition{
						{
							PerpetualId: 0,
							Quantums:    dtypes.NewInt(1_000_000_000), // 10 BTC
						},
					},
				},
				{
					Id: &constants.Carl_Num0,
					AssetPositions: []*satypes.AssetPosition{
						&constants.Usdc_Asset_100_000,
					},
					PerpetualPositions: []*satypes.PerpetualPosition{
						{
							PerpetualId: 0,
							Quantums:    dtypes.NewInt(1_000_000_000), // 10 BTC
						},
					},
				},
			},
			preExistingStatefulOrders: []types.Order{
				constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy100_Price10_GTBT15,
			},
			rawOperations: []types.OperationRaw{
				clobtest.NewShortTermOrderPlacementOperationRaw(
					types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Bob_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_SELL,
						Quantums:     10,
						Subticks:     10,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},
				),
				clobtest.NewMatchOperationRaw(
					&types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Bob_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_SELL,
						Quantums:     10,
						Subticks:     10,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},
					[]types.MakerFill{
						{
							FillAmount:   10,
							MakerOrderId: constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy100_Price10_GTBT15.GetOrderId(),
						},
					},
				),
				clobtest.NewShortTermOrderPlacementOperationRaw(
					types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Carl_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_SELL,
						Quantums:     15,
						Subticks:     10,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},
				),
				clobtest.NewMatchOperationRaw(
					&types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Carl_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_SELL,
						Quantums:     15,
						Subticks:     10,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},
					[]types.MakerFill{
						{
							FillAmount:   15,
							MakerOrderId: constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy100_Price10_GTBT15.GetOrderId(),
						},
					},
				),
			},
			expectedMatches: []*types.MatchWithOrders{
				{
					TakerOrder: &types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Bob_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_SELL,
						Quantums:     10,
						Subticks:     10,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},
					MakerOrder: &constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy100_Price10_GTBT15,
					FillAmount: 10,
				},
				{
					TakerOrder: &types.Order{
						OrderId:      types.OrderId{SubaccountId: constants.Carl_Num0, ClientId: 14, ClobPairId: 0},
						Side:         types.Order_SIDE_SELL,
						Quantums:     15,
						Subticks:     10,
						GoodTilOneof: &types.Order_GoodTilBlock{GoodTilBlock: 25},
					},

					MakerOrder: &constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy100_Price10_GTBT15,
					FillAmount: 15,
				},
			},

			expectedProcessProposerMatchesEvents: types.ProcessProposerMatchesEvents{
				OrdersIdsFilledInLastBlock: []types.OrderId{
					{SubaccountId: constants.Bob_Num0, ClientId: 14, ClobPairId: 0},
					constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy100_Price10_GTBT15.GetOrderId(),
					{SubaccountId: constants.Carl_Num0, ClientId: 14, ClobPairId: 0},
				},
				BlockHeight: blockHeight,
			},
			expectedQuoteBalances: map[satypes.SubaccountId]int64{
				constants.Alice_Num0: constants.Usdc_Asset_100_000.GetBigQuantums().Int64(),
				constants.Bob_Num0:   constants.Usdc_Asset_100_000.GetBigQuantums().Int64(),
				constants.Carl_Num0:  constants.Usdc_Asset_100_000.GetBigQuantums().Int64(),
			},
			expectedPerpetualPositions: map[satypes.SubaccountId][]*satypes.PerpetualPosition{
				constants.Bob_Num0: {
					{
						PerpetualId:  0,
						Quantums:     dtypes.NewInt(1_000_000_000 - 10),
						FundingIndex: dtypes.ZeroInt(),
					},
				},
				constants.Alice_Num0: {
					{
						PerpetualId:  0,
						Quantums:     dtypes.NewInt(1_000_000_000 + 10 + 15),
						FundingIndex: dtypes.ZeroInt(),
					},
				},
				constants.Carl_Num0: {
					{
						PerpetualId:  0,
						Quantums:     dtypes.NewInt(1_000_000_000 - 15),
						FundingIndex: dtypes.ZeroInt(),
					},
				},
			},
		},
		// This test matches a liquidation taker order with a short term maker order. The liquidation
		// order is fully filled at one dollar below the bankruptcy price ($49999 vs $50k). Carl's
		// $49,999 is transferred to Dave and Carl's $1 is paid to the insurance fund, leaving him
		// with nothing.
		"Succeeds with liquidation order": {
			perpetuals: []*perptypes.Perpetual{
				&constants.BtcUsd_20PercentInitial_10PercentMaintenance,
			},
			perpetualFeeParams: &constants.PerpetualFeeParams,
			clobPairs: []types.ClobPair{
				constants.ClobPair_Btc_No_Fee,
			},
			subaccounts: []satypes.Subaccount{
				// liquidatable: MMR = $5000, TNC = $0
				constants.Carl_Num0_1BTC_Short_50000USD,
				constants.Dave_Num0_1BTC_Long_50000USD,
			},
			rawOperations: []types.OperationRaw{
				clobtest.NewShortTermOrderPlacementOperationRaw(
					constants.Order_Dave_Num0_Id0_Clob0_Sell1BTC_Price49999_GTB10,
				),
				clobtest.NewMatchOperationRawFromPerpetualLiquidation(
					types.MatchPerpetualLiquidation{
						Liquidated:  constants.Carl_Num0,
						ClobPairId:  0,
						PerpetualId: 0,
						TotalSize:   100_000_000,
						IsBuy:       true,
						Fills: []types.MakerFill{
							{
								FillAmount:   100_000_000,
								MakerOrderId: constants.Order_Dave_Num0_Id0_Clob0_Sell1BTC_Price49999_GTB10.GetOrderId(),
							},
						},
					},
				),
			},
			expectedProcessProposerMatchesEvents: types.ProcessProposerMatchesEvents{
				OrdersIdsFilledInLastBlock: []types.OrderId{
					constants.Order_Dave_Num0_Id0_Clob0_Sell1BTC_Price49999_GTB10.GetOrderId(),
				},
				BlockHeight: blockHeight,
			},
			expectedMatches: []*types.MatchWithOrders{
				{
					TakerOrder: &constants.LiquidationOrder_Carl_Num0_Clob0_Buy1BTC_Price50500,
					MakerOrder: &constants.Order_Dave_Num0_Id0_Clob0_Sell1BTC_Price49999_GTB10,
					FillAmount: 100_000_000,
					MakerFee:   9_999_800,
					TakerFee:   1_000_000,
				},
			},
			expectedQuoteBalances: map[satypes.SubaccountId]int64{
				constants.Carl_Num0: 0,
				constants.Dave_Num0: constants.Usdc_Asset_99_999.GetBigQuantums().Int64() - int64(9_999_800),
			},
			expectedPerpetualPositions: map[satypes.SubaccountId][]*satypes.PerpetualPosition{
				constants.Carl_Num0: {},
				constants.Dave_Num0: {},
			},
		},
		// This test proposes a set of operations where no liquidation match occurs before the
		// deleveraging match. This happens in the case where the first order the liquidation
		// taker order tries to match with results in a match that requires insurance funds but the
		// insurance funds are insufficient. Because no matches took place, the liquidation order
		// is not included in the operations queue but a deleveraging match is. Deleveraging happens
		// at the bankruptcty price ($50,499) so Dave ends up with all of Carl's money.
		"Succeeds with deleveraging with no liquidation order": {
			perpetuals: []*perptypes.Perpetual{
				&constants.BtcUsd_20PercentInitial_10PercentMaintenance,
			},
			perpetualFeeParams: &constants.PerpetualFeeParams,
			clobPairs: []types.ClobPair{
				constants.ClobPair_Btc_No_Fee,
			},
			subaccounts: []satypes.Subaccount{
				// liquidatable: MMR = $5000, TNC = $499
				constants.Carl_Num0_1BTC_Short_50499USD,
				constants.Dave_Num0_1BTC_Long_50000USD,
			},
			rawOperations: []types.OperationRaw{
				clobtest.NewMatchOperationRawFromPerpetualDeleveragingLiquidation(
					types.MatchPerpetualDeleveraging{
						Liquidated:  constants.Carl_Num0,
						PerpetualId: 0,
						Fills: []types.MatchPerpetualDeleveraging_Fill{
							{
								OffsettingSubaccountId: constants.Dave_Num0,
								FillAmount:             100_000_000,
							},
						},
					},
				),
			},
			expectedProcessProposerMatchesEvents: types.ProcessProposerMatchesEvents{
				BlockHeight: blockHeight,
			},
			expectedQuoteBalances: map[satypes.SubaccountId]int64{
				constants.Carl_Num0: 0,
				constants.Dave_Num0: constants.Usdc_Asset_100_499.GetBigQuantums().Int64(),
			},
			expectedPerpetualPositions: map[satypes.SubaccountId][]*satypes.PerpetualPosition{
				constants.Carl_Num0: {},
				constants.Dave_Num0: {},
			},
		},
		// This test proposes a set of operations where a liquidation taker order matches with a short
		// term maker order. A deleveraging match is also proposed. For this to happen, the liquidation
		// would have matched with this first order and then tried to match with a second order, resulting
		// in a match that requires insurance funds but the insurance funds are insufficient. When processing
		// the deleveraging operation, the validator will confirm that the subaccount in the deleveraging match
		// is indeed liquidatable, confirming that this is a valid deleveraging match. In this example, the liquidation
		// and deleveraging both happen at bankruptcy price resulting in all of Carl's funds being transferred to Dave.
		"Succeeds with deleveraging and partially filled liquidation": {
			perpetuals: []*perptypes.Perpetual{
				&constants.BtcUsd_20PercentInitial_10PercentMaintenance,
			},
			perpetualFeeParams: &constants.PerpetualFeeParams,
			clobPairs: []types.ClobPair{
				constants.ClobPair_Btc_No_Fee,
			},
			subaccounts: []satypes.Subaccount{
				// liquidatable: MMR = $5000, TNC = $0
				constants.Carl_Num0_1BTC_Short_50000USD,
				constants.Dave_Num0_1BTC_Long_50000USD,
			},
			rawOperations: []types.OperationRaw{
				clobtest.NewShortTermOrderPlacementOperationRaw(
					constants.Order_Dave_Num0_Id1_Clob0_Sell025BTC_Price50000_GTB11,
				),
				clobtest.NewMatchOperationRawFromPerpetualLiquidation(
					types.MatchPerpetualLiquidation{
						Liquidated:  constants.Carl_Num0,
						ClobPairId:  0,
						PerpetualId: 0,
						TotalSize:   100_000_000,
						IsBuy:       true,
						Fills: []types.MakerFill{
							{
								FillAmount:   25_000_000,
								MakerOrderId: constants.Order_Dave_Num0_Id1_Clob0_Sell025BTC_Price50000_GTB11.GetOrderId(),
							},
						},
					},
				),
				clobtest.NewMatchOperationRawFromPerpetualDeleveragingLiquidation(
					types.MatchPerpetualDeleveraging{
						Liquidated:  constants.Carl_Num0,
						PerpetualId: 0,
						Fills: []types.MatchPerpetualDeleveraging_Fill{
							{
								OffsettingSubaccountId: constants.Dave_Num0,
								FillAmount:             75_000_000,
							},
						},
					},
				),
			},
			expectedMatches: []*types.MatchWithOrders{
				{
					TakerOrder: &constants.LiquidationOrder_Carl_Num0_Clob0_Buy1BTC_Price50500,
					MakerOrder: &constants.Order_Dave_Num0_Id1_Clob0_Sell025BTC_Price50000_GTB11,
					FillAmount: 25_000_000,
					MakerFee:   2_500_000,
					TakerFee:   0,
				},
			},
			expectedProcessProposerMatchesEvents: types.ProcessProposerMatchesEvents{
				OrdersIdsFilledInLastBlock: []types.OrderId{
					constants.Order_Dave_Num0_Id1_Clob0_Sell025BTC_Price50000_GTB11.GetOrderId(),
				},
				BlockHeight: blockHeight,
			},
			expectedQuoteBalances: map[satypes.SubaccountId]int64{
				constants.Carl_Num0: 0,
				constants.Dave_Num0: 100_000_000_000 - 2500000,
			},
			expectedPerpetualPositions: map[satypes.SubaccountId][]*satypes.PerpetualPosition{
				constants.Carl_Num0: {},
				constants.Dave_Num0: {},
			},
		},
		"Succeeds order removal operations with previous stateful orders": {
			perpetuals: []*perptypes.Perpetual{
				&constants.BtcUsd_100PercentMarginRequirement,
			},
			perpetualFeeParams: &constants.PerpetualFeeParams,
			clobPairs: []types.ClobPair{
				constants.ClobPair_Btc,
			},
			subaccounts: []satypes.Subaccount{},
			preExistingStatefulOrders: []types.Order{
				constants.ConditionalOrder_Alice_Num0_Id0_Clob0_Buy5_Price10_GTBT15_StopLoss20,
				constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy5_Price10_GTBT15,
				constants.LongTermOrder_Bob_Num0_Id0_Clob0_Buy25_Price30_GTBT10,
			},
			rawOperations: []types.OperationRaw{
				clobtest.NewOrderRemovalOperationRaw(
					constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy5_Price10_GTBT15.OrderId,
					types.OrderRemoval_REMOVAL_REASON_INVALID_SELF_TRADE,
				),
				clobtest.NewOrderRemovalOperationRaw(
					constants.LongTermOrder_Bob_Num0_Id0_Clob0_Buy25_Price30_GTBT10.OrderId,
					types.OrderRemoval_REMOVAL_REASON_POST_ONLY_WOULD_CROSS_MAKER_ORDER,
				),
			},

			expectedProcessProposerMatchesEvents: types.ProcessProposerMatchesEvents{
				BlockHeight: blockHeight,
				RemovedStatefulOrderIds: []types.OrderId{
					constants.LongTermOrder_Bob_Num0_Id0_Clob0_Buy25_Price30_GTBT10.OrderId,
					constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy5_Price10_GTBT15.OrderId,
				},
			},
		},
		// This test proposes an invalid perpetual deleveraging liquidation match operation. The
		// subaccount is not liquidatable, so the match operation should be rejected.
		"Fails with deleveraging match for non-liquidatable subaccount": {
			perpetuals: []*perptypes.Perpetual{
				&constants.BtcUsd_20PercentInitial_10PercentMaintenance,
			},
			perpetualFeeParams: &constants.PerpetualFeeParams,
			clobPairs: []types.ClobPair{
				constants.ClobPair_Btc_No_Fee,
			},
			subaccounts: []satypes.Subaccount{
				constants.Carl_Num0_1BTC_Short_55000USD,
				constants.Dave_Num0_1BTC_Long_50000USD,
			},
			rawOperations: []types.OperationRaw{
				clobtest.NewMatchOperationRawFromPerpetualDeleveragingLiquidation(
					types.MatchPerpetualDeleveraging{
						Liquidated:  constants.Carl_Num0,
						PerpetualId: 0,
						Fills: []types.MatchPerpetualDeleveraging_Fill{
							{
								OffsettingSubaccountId: constants.Dave_Num0,
								FillAmount:             100_000_000,
							},
						},
					},
				),
			},
			expectedQuoteBalances: map[satypes.SubaccountId]int64{
				constants.Carl_Num0: constants.Carl_Num0_1BTC_Short_55000USD.GetUsdcPosition().Int64(),
				constants.Dave_Num0: constants.Usdc_Asset_50_000.GetBigQuantums().Int64(),
			},
			expectedPerpetualPositions: map[satypes.SubaccountId][]*satypes.PerpetualPosition{
				constants.Carl_Num0: constants.Carl_Num0_1BTC_Short_55000USD.GetPerpetualPositions(),
				constants.Dave_Num0: constants.Dave_Num0_1BTC_Long_50000USD.GetPerpetualPositions(),
			},
			expectedError: types.ErrDeleveragedSubaccountNotLiquidatable,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			mockBankKeeper := &mocks.BankKeeper{}
			mockBankKeeper.On(
				"SendCoinsFromModuleToModule",
				mock.Anything,
				mock.Anything,
				mock.Anything,
				mock.Anything,
			).Return(nil)
			mockBankKeeper.On(
				"GetBalance",
				mock.Anything,
				mock.Anything,
				mock.Anything,
			).Return(sdk.NewCoin("USDC", sdk.NewIntFromUint64(tc.insuranceFundBalance)))

			mockIndexerEventManager := &mocks.IndexerEventManager{}
			// This memclob is not used in the test since DeliverTx creates a new memclob to replay
			// operations on.
			ks := keepertest.NewClobKeepersTestContext(
				t,
				memclob.NewMemClobPriceTimePriority(false),
				mockBankKeeper,
				mockIndexerEventManager,
			)

			// set DeliverTx mode.
			ctx := ks.Ctx.WithIsCheckTx(false)

			// Assert Indexer messages
			setupNewMockEventManager(
				t,
				ctx,
				mockIndexerEventManager,
				tc.expectedMatches,
			)

			// Create the default markets.
			keepertest.CreateTestMarkets(t, ctx, ks.PricesKeeper)

			// Create liquidity tiers.
			keepertest.CreateTestLiquidityTiers(t, ctx, ks.PerpetualsKeeper)

			require.NotNil(t, tc.perpetualFeeParams)
			require.NoError(t, ks.FeeTiersKeeper.SetPerpetualFeeParams(ctx, *tc.perpetualFeeParams))

			err := keepertest.CreateUsdcAsset(ctx, ks.AssetsKeeper)
			require.NoError(t, err)

			// Create all perpetuals.
			for _, p := range tc.perpetuals {
				_, err := ks.PerpetualsKeeper.CreatePerpetual(
					ctx,
					p.Ticker,
					p.MarketId,
					p.AtomicResolution,
					p.DefaultFundingPpm,
					p.LiquidityTier,
				)
				require.NoError(t, err)
			}

			// Create all subaccounts.
			for _, subaccount := range tc.subaccounts {
				ks.SubaccountsKeeper.SetSubaccount(ctx, subaccount)
			}

			// Create all CLOBs.
			for _, clobPair := range tc.clobPairs {
				_, err = ks.ClobKeeper.CreatePerpetualClobPair(
					ctx,
					clobtest.MustPerpetualId(clobPair),
					satypes.BaseQuantums(clobPair.StepBaseQuantums),
					satypes.BaseQuantums(clobPair.MinOrderBaseQuantums),
					clobPair.QuantumConversionExponent,
					clobPair.SubticksPerTick,
					clobPair.Status,
					clobPair.MakerFeePpm,
					clobPair.TakerFeePpm,
				)
				require.NoError(t, err)
			}

			// Initialize the liquidations config.
			if tc.liquidationConfig != nil {
				require.NoError(t, ks.ClobKeeper.InitializeLiquidationsConfig(ctx, *tc.liquidationConfig))
			} else {
				require.NoError(t, ks.ClobKeeper.InitializeLiquidationsConfig(ctx, constants.LiquidationsConfig_No_Limit))
			}

			// Create all pre-existing stateful orders in state. Duplicate orders are not allowed.
			// We don't need to set the stateful order placement in memclob because the deliverTx flow
			// will create its own memclob.
			seenOrderIds := make(map[types.OrderId]struct{})
			for _, order := range tc.preExistingStatefulOrders {
				_, exists := seenOrderIds[order.GetOrderId()]
				require.Falsef(t, exists, "Duplicate pre-existing stateful order (+%v)", order)
				seenOrderIds[order.GetOrderId()] = struct{}{}
				ks.ClobKeeper.SetLongTermOrderPlacement(ctx, order, blockHeight)
				ks.ClobKeeper.MustAddOrderToStatefulOrdersTimeSlice(
					ctx,
					order.MustGetUnixGoodTilBlockTime(),
					order.OrderId,
				)
			}

			// Set the block time on the context and of the last committed block.
			ctx = ctx.WithBlockTime(time.Unix(5, 0)).WithBlockHeight(int64(blockHeight))
			ks.ClobKeeper.SetBlockTimeForLastCommittedBlock(ctx)

			// Run the DeliverTx ProcessProposerOperations flow.
			err = ks.ClobKeeper.ProcessProposerOperations(ctx, tc.rawOperations)
			if tc.expectedError != nil {
				require.ErrorContains(t, err, tc.expectedError.Error())
			} else {
				require.NoError(t, err)
			}

			// Verify that processProposerMatchesEvents is the same.
			processProposerMatchesEvents := ks.ClobKeeper.GetProcessProposerMatchesEvents(ctx)
			require.Equal(t, tc.expectedProcessProposerMatchesEvents, processProposerMatchesEvents)

			// Verify that newly-placed stateful orders were written to state.
			for _, newlyPlacedStatefulOrder := range processProposerMatchesEvents.PlacedStatefulOrders {
				exists := ks.ClobKeeper.DoesLongTermOrderExistInState(ctx, newlyPlacedStatefulOrder)
				require.Truef(t, exists, "order (%+v) was not placed in state.", newlyPlacedStatefulOrder)
			}

			// Verify that removed stateful orders were in fact removed from state.
			for _, removedStatefulOrderId := range processProposerMatchesEvents.RemovedStatefulOrderIds {
				_, exists := ks.ClobKeeper.GetLongTermOrderPlacement(ctx, removedStatefulOrderId)
				require.Falsef(t, exists, "order (%+v) was not removed from state.", removedStatefulOrderId)
			}

			// Verify subaccount state.
			assertSubaccountState(t, ctx, ks.SubaccountsKeeper, tc.expectedQuoteBalances, tc.expectedPerpetualPositions)

			mockIndexerEventManager.AssertExpectations(t)

			// TODO(CLOB-230) Add more assertions.
		})
	}
}

func TestGenerateProcessProposerMatchesEvents(t *testing.T) {
	blockHeight := uint32(5)
	tests := map[string]struct {
		// Params.
		operations []types.InternalOperation

		// Expectations.
		expectedProcessProposerMatchesEvents types.ProcessProposerMatchesEvents
	}{
		"empty operations queue": {
			operations: []types.InternalOperation{},
			expectedProcessProposerMatchesEvents: types.ProcessProposerMatchesEvents{
				PlacedStatefulOrders:        []types.Order{},
				ExpiredStatefulOrderIds:     []types.OrderId{},
				OrdersIdsFilledInLastBlock:  []types.OrderId{},
				PlacedStatefulCancellations: []types.OrderId{},
				RemovedStatefulOrderIds:     []types.OrderId{},
				BlockHeight:                 blockHeight,
			},
		},
		"short term order matches": {
			operations: []types.InternalOperation{
				types.NewShortTermOrderPlacementInternalOperation(
					constants.Order_Alice_Num0_Id9_Clob1_Buy15_Price45_GTB19,
				),
				types.NewShortTermOrderPlacementInternalOperation(
					constants.Order_Bob_Num0_Id0_Clob1_Sell10_Price15_GTB20,
				),
				types.NewMatchOrdersInternalOperation(
					constants.Order_Bob_Num0_Id0_Clob1_Sell10_Price15_GTB20,
					[]types.MakerFill{
						{
							MakerOrderId: constants.Order_Alice_Num0_Id9_Clob1_Buy15_Price45_GTB19.OrderId,
							FillAmount:   19,
						},
					},
				),
			},
			expectedProcessProposerMatchesEvents: types.ProcessProposerMatchesEvents{
				PlacedStatefulOrders:    []types.Order{},
				ExpiredStatefulOrderIds: []types.OrderId{},
				OrdersIdsFilledInLastBlock: []types.OrderId{
					constants.Order_Bob_Num0_Id0_Clob1_Sell10_Price15_GTB20.OrderId,
					constants.Order_Alice_Num0_Id9_Clob1_Buy15_Price45_GTB19.OrderId,
				},
				PlacedStatefulCancellations: []types.OrderId{},
				RemovedStatefulOrderIds:     []types.OrderId{},
				BlockHeight:                 blockHeight,
			},
		},
		"liquidation matches": {
			operations: []types.InternalOperation{
				types.NewShortTermOrderPlacementInternalOperation(
					constants.Order_Alice_Num1_Id13_Clob0_Buy50_Price50_GTB30,
				),
				types.NewMatchPerpetualLiquidationInternalOperation(
					&constants.LiquidationOrder_Alice_Num0_Clob0_Sell20_Price25_BTC,
					[]types.MakerFill{
						{
							MakerOrderId: constants.Order_Alice_Num1_Id13_Clob0_Buy50_Price50_GTB30.OrderId,
							FillAmount:   20,
						},
					},
				),
			},
			expectedProcessProposerMatchesEvents: types.ProcessProposerMatchesEvents{
				PlacedStatefulOrders:    []types.Order{},
				ExpiredStatefulOrderIds: []types.OrderId{},
				OrdersIdsFilledInLastBlock: []types.OrderId{
					constants.Order_Alice_Num1_Id13_Clob0_Buy50_Price50_GTB30.OrderId,
				},
				PlacedStatefulCancellations: []types.OrderId{},
				RemovedStatefulOrderIds:     []types.OrderId{},
				BlockHeight:                 blockHeight,
			},
		},
		"stateful orders in matches": {
			operations: []types.InternalOperation{
				types.NewShortTermOrderPlacementInternalOperation(
					constants.Order_Alice_Num0_Id9_Clob1_Buy15_Price45_GTB19,
				),
				types.NewPreexistingStatefulOrderPlacementInternalOperation(
					constants.LongTermOrder_Alice_Num1_Id1_Clob0_Sell25_Price30_GTBT10,
				),
				types.NewMatchOrdersInternalOperation(
					constants.Order_Alice_Num0_Id9_Clob1_Buy15_Price45_GTB19,
					[]types.MakerFill{
						{
							MakerOrderId: constants.LongTermOrder_Alice_Num1_Id1_Clob0_Sell25_Price30_GTBT10.OrderId,
							FillAmount:   10,
						},
					},
				),
			},
			expectedProcessProposerMatchesEvents: types.ProcessProposerMatchesEvents{
				PlacedStatefulOrders:    []types.Order{},
				ExpiredStatefulOrderIds: []types.OrderId{},
				OrdersIdsFilledInLastBlock: []types.OrderId{
					constants.Order_Alice_Num0_Id9_Clob1_Buy15_Price45_GTB19.OrderId,
					constants.LongTermOrder_Alice_Num1_Id1_Clob0_Sell25_Price30_GTBT10.OrderId,
				},
				PlacedStatefulCancellations: []types.OrderId{},
				RemovedStatefulOrderIds:     []types.OrderId{},
				BlockHeight:                 blockHeight,
			},
		},
		"skips pre existing stateful order operations": {
			operations: []types.InternalOperation{
				types.NewPreexistingStatefulOrderPlacementInternalOperation(
					constants.LongTermOrder_Alice_Num1_Id1_Clob0_Sell25_Price30_GTBT10,
				),
			},
			expectedProcessProposerMatchesEvents: types.ProcessProposerMatchesEvents{
				PlacedStatefulOrders:        []types.Order{},
				ExpiredStatefulOrderIds:     []types.OrderId{},
				OrdersIdsFilledInLastBlock:  []types.OrderId{},
				PlacedStatefulCancellations: []types.OrderId{},
				RemovedStatefulOrderIds:     []types.OrderId{},
				BlockHeight:                 blockHeight,
			},
		},
		"order removals": {
			operations: []types.InternalOperation{
				types.NewOrderRemovalInternalOperation(
					constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy100_Price10_GTBT15.OrderId,
					types.OrderRemoval_REMOVAL_REASON_INVALID_SELF_TRADE,
				),
				types.NewOrderRemovalInternalOperation(
					constants.LongTermOrder_Bob_Num0_Id0_Clob0_Buy25_Price30_GTBT10.OrderId,
					types.OrderRemoval_REMOVAL_REASON_POST_ONLY_WOULD_CROSS_MAKER_ORDER,
				),
			},
			expectedProcessProposerMatchesEvents: types.ProcessProposerMatchesEvents{
				PlacedStatefulOrders:        []types.Order{},
				ExpiredStatefulOrderIds:     []types.OrderId{},
				OrdersIdsFilledInLastBlock:  []types.OrderId{},
				PlacedStatefulCancellations: []types.OrderId{},
				RemovedStatefulOrderIds: []types.OrderId{
					constants.LongTermOrder_Bob_Num0_Id0_Clob0_Buy25_Price30_GTBT10.OrderId,
					constants.LongTermOrder_Alice_Num0_Id0_Clob0_Buy100_Price10_GTBT15.OrderId,
				},
				BlockHeight: blockHeight,
			},
		},
	}

	// Run tests.
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			memclob := memclob.NewMemClobPriceTimePriority(true)
			ks := keepertest.NewClobKeepersTestContext(t, memclob, &mocks.BankKeeper{}, &mocks.IndexerEventManager{})
			ctx := ks.Ctx.WithBlockHeight(int64(blockHeight))

			processProposerMatchesEvents := ks.ClobKeeper.GenerateProcessProposerMatchesEvents(ctx, tc.operations)
			require.Equal(t, tc.expectedProcessProposerMatchesEvents, processProposerMatchesEvents)
		})
	}
}

func setupNewMockEventManager(
	t *testing.T,
	ctx sdk.Context,
	mockIndexerEventManager *mocks.IndexerEventManager,
	matches []*types.MatchWithOrders,
) {
	if len(matches) > 0 {
		mockIndexerEventManager.On("Enabled").Return(true)
	}

	// Add an expectation to the mock for each expected message.
	var matchOrderCallMap = make(map[types.OrderId]*mock.Call)
	for _, match := range matches {
		if match.TakerOrder.IsLiquidation() {
			call := mockIndexerEventManager.On("AddTxnEvent",
				mock.Anything,
				indexerevents.SubtypeOrderFill,
				indexer_manager.GetB64EncodedEventMessage(
					indexerevents.NewLiquidationOrderFillEvent(
						match.MakerOrder.MustGetOrder(),
						match.TakerOrder,
						match.FillAmount,
						match.MakerFee,
						match.TakerFee,
					),
				),
			).Return()

			matchOrderCallMap[match.MakerOrder.MustGetOrder().OrderId] = call
		} else {
			call := mockIndexerEventManager.On("AddTxnEvent",
				mock.Anything,
				indexerevents.SubtypeOrderFill,
				indexer_manager.GetB64EncodedEventMessage(
					indexerevents.NewOrderFillEvent(
						match.MakerOrder.MustGetOrder(),
						match.TakerOrder.MustGetOrder(),
						match.FillAmount,
						match.MakerFee,
						match.TakerFee,
					),
				),
			).Return()
			matchOrderCallMap[match.MakerOrder.MustGetOrder().OrderId] = call
			matchOrderCallMap[match.TakerOrder.MustGetOrder().OrderId] = call
		}
	}
}

func assertSubaccountState(
	t *testing.T,
	ctx sdk.Context,
	subaccountsKeeper *sakeeper.Keeper,
	expectedQuoteBalances map[satypes.SubaccountId]int64,
	expectedPerpetualPositions map[satypes.SubaccountId][]*satypes.PerpetualPosition,
) {
	for subaccountId, quoteBalance := range expectedQuoteBalances {
		subaccount := subaccountsKeeper.GetSubaccount(ctx, subaccountId)
		require.Equal(t, quoteBalance, subaccount.GetUsdcPosition().Int64())
	}

	for subaccountId, perpetualPositions := range expectedPerpetualPositions {
		subaccount := subaccountsKeeper.GetSubaccount(ctx, subaccountId)
		require.ElementsMatch(t, subaccount.PerpetualPositions, perpetualPositions)
	}
}