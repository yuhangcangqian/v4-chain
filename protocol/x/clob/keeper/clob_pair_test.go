package keeper_test

import (
	"strconv"
	"testing"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/store/prefix"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/dydxprotocol/v4/mocks"
	clobtest "github.com/dydxprotocol/v4/testutil/clob"
	"github.com/dydxprotocol/v4/testutil/constants"
	keepertest "github.com/dydxprotocol/v4/testutil/keeper"
	"github.com/dydxprotocol/v4/testutil/nullify"
	"github.com/dydxprotocol/v4/x/clob/keeper"
	"github.com/dydxprotocol/v4/x/clob/memclob"
	"github.com/dydxprotocol/v4/x/clob/types"
	"github.com/dydxprotocol/v4/x/perpetuals"
	"github.com/dydxprotocol/v4/x/prices"
	satypes "github.com/dydxprotocol/v4/x/subaccounts/types"
	"github.com/stretchr/testify/require"
)

// Prevent strconv unused error
var _ = strconv.IntSize

func createNClobPair(keeper *keeper.Keeper, ctx sdk.Context, n int) []types.ClobPair {
	items := make([]types.ClobPair, n)
	for i := range items {
		items[i].Id = uint32(i)
		items[i].Metadata = &types.ClobPair_PerpetualClobMetadata{
			PerpetualClobMetadata: &types.PerpetualClobMetadata{
				PerpetualId: 0,
			},
		}
		items[i].SubticksPerTick = 5
		items[i].StepBaseQuantums = 5
		items[i].Status = types.ClobPair_STATUS_ACTIVE
		items[i].MinOrderBaseQuantums = 10
		items[i].MakerFeePpm = constants.MakerFeePpm
		items[i].TakerFeePpm = constants.TakerFeePpm

		_, err := keeper.CreatePerpetualClobPair(
			ctx,
			clobtest.MustPerpetualId(items[i]),
			satypes.BaseQuantums(items[i].StepBaseQuantums),
			satypes.BaseQuantums(items[i].MinOrderBaseQuantums),
			items[i].QuantumConversionExponent,
			items[i].SubticksPerTick,
			items[i].Status,
			items[i].MakerFeePpm,
			items[i].TakerFeePpm,
		)
		if err != nil {
			panic(err)
		}
	}
	return items
}

func TestCreateClobPair(t *testing.T) {
	tests := map[string]struct {
		// CLOB pair.
		clobPair types.ClobPair

		// Expectations.
		expectedErr string
	}{
		"CLOB pair is valid": {
			clobPair: *clobtest.GenerateClobPair(),
		},
		"CLOB pair is invalid when the perpetual ID does not match an existing perpetual in the store": {
			clobPair: *clobtest.GenerateClobPair(clobtest.WithPerpetualMetadata(
				&types.ClobPair_PerpetualClobMetadata{
					PerpetualClobMetadata: &types.PerpetualClobMetadata{
						PerpetualId: 1000000,
					},
				},
			)),
			expectedErr: "has invalid perpetual.",
		},
		"CLOB pair is invalid when the minimum order size is 0": {
			clobPair:    *clobtest.GenerateClobPair(clobtest.WithMinOrderBaseQuantums(0)),
			expectedErr: "invalid ClobPair parameter: MinOrderBaseQuantums must be > 0.",
		},
		"CLOB pair is invalid when the step size is 0": {
			clobPair:    *clobtest.GenerateClobPair(clobtest.WithStepBaseQuantums(0)),
			expectedErr: "invalid ClobPair parameter: StepBaseQuantums must be > 0.",
		},
		"CLOB pair is invalid when minimum order size is not a multiple of step size": {
			clobPair: *clobtest.GenerateClobPair(
				clobtest.WithMinOrderBaseQuantums(satypes.BaseQuantums(21)),
				clobtest.WithStepBaseQuantums(satypes.BaseQuantums(4)),
			),
			expectedErr: "must be divisible by StepBaseQuantums",
		},
		"CLOB pair is invalid when the subticks per tick is 0": {
			clobPair:    *clobtest.GenerateClobPair(clobtest.WithSubticksPerTick(0)),
			expectedErr: "invalid ClobPair parameter: SubticksPerTick must be > 0.",
		},
		"CLOB pair is invalid when the status is unspecified": {
			clobPair:    *clobtest.GenerateClobPair(clobtest.WithStatus(types.ClobPair_STATUS_UNSPECIFIED)),
			expectedErr: "invalid ClobPair parameter: Status must be specified.",
		},
		"CLOB pair is invalid when the maker fee is greater than the max fee": {
			clobPair:    *clobtest.GenerateClobPair(clobtest.WithMakerFeePpm(1000000)),
			expectedErr: "be <= MaxFeePpm",
		},
		"CLOB pair is invalid when the taker fee is greater than the max fee": {
			clobPair:    *clobtest.GenerateClobPair(clobtest.WithTakerFeePpm(1000000)),
			expectedErr: "be <= MaxFeePpm",
		},
		"CLOB pair is invalid when the maker fee is higher than the taker fee": {
			clobPair: *clobtest.GenerateClobPair(
				clobtest.WithMakerFeePpm(100),
				clobtest.WithTakerFeePpm(10),
			),
			expectedErr: "must be <= TakerFeePpm",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Boilerplate setup.
			memClob := memclob.NewMemClobPriceTimePriority(false)
			ks := keepertest.NewClobKeepersTestContext(t, memClob, &mocks.BankKeeper{}, &mocks.IndexerEventManager{})

			prices.InitGenesis(ks.Ctx, *ks.PricesKeeper, constants.Prices_DefaultGenesisState)
			perpetuals.InitGenesis(ks.Ctx, *ks.PerpetualsKeeper, constants.Perpetuals_DefaultGenesisState)

			// Perform the method under test.
			createdClobPair, actualErr := ks.ClobKeeper.CreatePerpetualClobPair(
				ks.Ctx,
				clobtest.MustPerpetualId(tc.clobPair),
				satypes.BaseQuantums(tc.clobPair.StepBaseQuantums),
				satypes.BaseQuantums(tc.clobPair.MinOrderBaseQuantums),
				tc.clobPair.QuantumConversionExponent,
				tc.clobPair.SubticksPerTick,
				tc.clobPair.Status,
				tc.clobPair.MakerFeePpm,
				tc.clobPair.TakerFeePpm,
			)
			storedClobPair, found := ks.ClobKeeper.GetClobPair(ks.Ctx, types.ClobPairId(tc.clobPair.Id))
			numClobPairs := ks.ClobKeeper.GetNumClobPairs(ks.Ctx)

			if tc.expectedErr == "" {
				// A valid CLOB pair should not raise any validation errors.
				require.NoError(t, actualErr)

				// The CLOB pair returned should be identical to the test case.
				require.Equal(t, tc.clobPair, createdClobPair)

				// The CLOB pair should be able to be retrieved from the store.
				require.True(t, found)
				require.NotNil(t, storedClobPair)

				// The stored CLOB pair should be identical to the test case.
				require.Equal(t, tc.clobPair, storedClobPair)

				// The stored count of CLOB pairs should have been incremented.
				require.Equal(t, uint32(1), numClobPairs)
			} else {
				// The create method should have returned a validation error matching the test case.
				require.Error(t, actualErr)
				require.ErrorContains(t, actualErr, tc.expectedErr)

				// The CLOB pair should not be able to be found in the store.
				require.False(t, found)

				// The stored count of CLOB pairs should not have been incremented.
				require.Equal(t, uint32(0), numClobPairs)
			}
		})
	}
}

func TestCreateMultipleClobPairs(t *testing.T) {
	type CreationExpectation struct {
		// CLOB pair.
		clobPair types.ClobPair

		// Expectations.
		expectedErr string
	}
	tests := map[string]struct {
		// The CLOB pairs to attempt to make.
		clobPairs []CreationExpectation

		// The expected number of created CLOB pairs.
		expectedNumClobPairs uint32

		// The expected mapping of ID -> CLOB pair.
		expectedStoredClobPairs map[types.ClobPairId]types.ClobPair
	}{
		"Successfully makes multiple CLOB pairs": {
			clobPairs: []CreationExpectation{
				{clobPair: constants.ClobPair_Btc},
				{clobPair: constants.ClobPair_Eth},
			},
			expectedNumClobPairs: 2,
			expectedStoredClobPairs: map[types.ClobPairId]types.ClobPair{
				0: constants.ClobPair_Btc,
				1: constants.ClobPair_Eth,
			},
		},
		"Can create a CLOB pair and then fail validation": {
			clobPairs: []CreationExpectation{
				{clobPair: constants.ClobPair_Btc},
				{
					clobPair:    *clobtest.GenerateClobPair(clobtest.WithStatus(types.ClobPair_STATUS_UNSPECIFIED)),
					expectedErr: "invalid ClobPair parameter: Status must be specified.",
				},
			},
			expectedNumClobPairs: 1,
			expectedStoredClobPairs: map[types.ClobPairId]types.ClobPair{
				0: constants.ClobPair_Btc,
			},
		},
		"Can create a CLOB pair after failing to create one": {
			clobPairs: []CreationExpectation{
				{
					clobPair:    *clobtest.GenerateClobPair(clobtest.WithStatus(types.ClobPair_STATUS_UNSPECIFIED)),
					expectedErr: "invalid ClobPair parameter: Status must be specified.",
				},
				{clobPair: constants.ClobPair_Btc},
			},
			expectedNumClobPairs: 1,
			expectedStoredClobPairs: map[types.ClobPairId]types.ClobPair{
				0: constants.ClobPair_Btc,
			},
		},
		"Can alternate between passing/failing CLOB pair validation with no issues": {
			clobPairs: []CreationExpectation{
				{clobPair: constants.ClobPair_Btc},
				{
					clobPair:    *clobtest.GenerateClobPair(clobtest.WithStatus(types.ClobPair_STATUS_UNSPECIFIED)),
					expectedErr: "invalid ClobPair parameter: Status must be specified.",
				},
				{clobPair: constants.ClobPair_Eth},
			},
			expectedNumClobPairs: 2,
			expectedStoredClobPairs: map[types.ClobPairId]types.ClobPair{
				0: constants.ClobPair_Btc,
				1: constants.ClobPair_Eth,
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Boilerplate setup.
			memClob := memclob.NewMemClobPriceTimePriority(false)
			ks := keepertest.NewClobKeepersTestContext(t, memClob, &mocks.BankKeeper{}, &mocks.IndexerEventManager{})

			prices.InitGenesis(ks.Ctx, *ks.PricesKeeper, constants.Prices_DefaultGenesisState)
			perpetuals.InitGenesis(ks.Ctx, *ks.PerpetualsKeeper, constants.Perpetuals_DefaultGenesisState)

			// Perform the method under test.
			for _, make := range tc.clobPairs {
				_, err := ks.ClobKeeper.CreatePerpetualClobPair(
					ks.Ctx,
					clobtest.MustPerpetualId(make.clobPair),
					satypes.BaseQuantums(make.clobPair.StepBaseQuantums),
					satypes.BaseQuantums(make.clobPair.MinOrderBaseQuantums),
					make.clobPair.QuantumConversionExponent,
					make.clobPair.SubticksPerTick,
					make.clobPair.Status,
					make.clobPair.MakerFeePpm,
					make.clobPair.TakerFeePpm,
				)
				if make.expectedErr == "" {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
					require.ErrorContains(t, err, make.expectedErr)
				}
			}

			actualNumClobPairs := ks.ClobKeeper.GetNumClobPairs(ks.Ctx)
			require.Equal(t, tc.expectedNumClobPairs, actualNumClobPairs)

			for key, expectedClobPair := range tc.expectedStoredClobPairs {
				actual, found := ks.ClobKeeper.GetClobPair(ks.Ctx, key)
				require.True(t, found)
				require.Equal(t, expectedClobPair, actual)
			}

			_, found := ks.ClobKeeper.GetClobPair(ks.Ctx, types.ClobPairId(tc.expectedNumClobPairs))
			require.False(t, found)
		})
	}
}

func TestInitMemClobOrderbooks(t *testing.T) {
	memClob := memclob.NewMemClobPriceTimePriority(false)
	ks := keepertest.NewClobKeepersTestContext(
		t,
		memClob,
		&mocks.BankKeeper{},
		&mocks.IndexerEventManager{},
	)

	// Read a new `ClobPair` and make sure it does not exist.
	_, err := memClob.GetClobPairForPerpetual(ks.Ctx, 1)
	require.ErrorIs(t, err, types.ErrNoClobPairForPerpetual)

	// Write multiple `ClobPairs` to state, but don't call `MemClob.CreateOrderbook`.
	store := prefix.NewStore(ks.Ctx.KVStore(ks.StoreKey), types.KeyPrefix(types.ClobPairKeyPrefix))
	registry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(registry)

	b := cdc.MustMarshal(&constants.ClobPair_Eth)
	store.Set(types.ClobPairKey(
		types.ClobPairId(constants.ClobPair_Eth.Id),
	), b)

	b = cdc.MustMarshal(&constants.ClobPair_Btc)
	store.Set(types.ClobPairKey(
		types.ClobPairId(constants.ClobPair_Btc.Id),
	), b)

	// Read the new `ClobPairs` and make sure they do not exist.
	_, err = memClob.GetClobPairForPerpetual(ks.Ctx, 1)
	require.ErrorIs(t, err, types.ErrNoClobPairForPerpetual)

	// Initialize the `ClobPairs` from Keeper state.
	ks.ClobKeeper.InitMemClobOrderbooks(ks.Ctx)

	// Read the new `ClobPairs` and make sure they exist.
	_, err = memClob.GetClobPairForPerpetual(ks.Ctx, 0)
	require.NoError(t, err)

	_, err = memClob.GetClobPairForPerpetual(ks.Ctx, 1)
	require.NoError(t, err)
}

func TestClobPairGet(t *testing.T) {
	memClob := memclob.NewMemClobPriceTimePriority(false)
	ks := keepertest.NewClobKeepersTestContext(t, memClob, &mocks.BankKeeper{}, &mocks.IndexerEventManager{})
	prices.InitGenesis(ks.Ctx, *ks.PricesKeeper, constants.Prices_DefaultGenesisState)
	perpetuals.InitGenesis(ks.Ctx, *ks.PerpetualsKeeper, constants.Perpetuals_DefaultGenesisState)
	items := createNClobPair(ks.ClobKeeper, ks.Ctx, 10)
	for _, item := range items {
		rst, found := ks.ClobKeeper.GetClobPair(ks.Ctx,
			types.ClobPairId(item.Id),
		)
		require.True(t, found)
		require.Equal(t,
			nullify.Fill(&item), //nolint:staticcheck
			nullify.Fill(&rst),  //nolint:staticcheck
		)
	}
}
func TestClobPairRemove(t *testing.T) {
	memClob := memclob.NewMemClobPriceTimePriority(false)
	ks := keepertest.NewClobKeepersTestContext(t, memClob, &mocks.BankKeeper{}, &mocks.IndexerEventManager{})
	prices.InitGenesis(ks.Ctx, *ks.PricesKeeper, constants.Prices_DefaultGenesisState)
	perpetuals.InitGenesis(ks.Ctx, *ks.PerpetualsKeeper, constants.Perpetuals_DefaultGenesisState)
	items := createNClobPair(ks.ClobKeeper, ks.Ctx, 10)
	for _, item := range items {
		ks.ClobKeeper.RemoveClobPair(ks.Ctx,
			types.ClobPairId(item.Id),
		)
		_, found := ks.ClobKeeper.GetClobPair(ks.Ctx,
			types.ClobPairId(item.Id),
		)
		require.False(t, found)
	}
}

func TestClobPairGetAll(t *testing.T) {
	memClob := memclob.NewMemClobPriceTimePriority(false)
	ks := keepertest.NewClobKeepersTestContext(t, memClob, &mocks.BankKeeper{}, &mocks.IndexerEventManager{})
	prices.InitGenesis(ks.Ctx, *ks.PricesKeeper, constants.Prices_DefaultGenesisState)
	perpetuals.InitGenesis(ks.Ctx, *ks.PerpetualsKeeper, constants.Perpetuals_DefaultGenesisState)
	items := createNClobPair(ks.ClobKeeper, ks.Ctx, 10)
	require.ElementsMatch(t,
		nullify.Fill(items), //nolint:staticcheck
		nullify.Fill(ks.ClobKeeper.GetAllClobPair(ks.Ctx)), //nolint:staticcheck
	)
}