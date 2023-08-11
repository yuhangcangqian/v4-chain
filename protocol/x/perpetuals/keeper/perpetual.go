package keeper

import (
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/dydxprotocol/v4/indexer/indexer_manager"

	gometrics "github.com/armon/go-metrics"
	"github.com/cosmos/cosmos-sdk/store/prefix"
	"github.com/cosmos/cosmos-sdk/telemetry"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/dydxprotocol/v4/dtypes"
	indexerevents "github.com/dydxprotocol/v4/indexer/events"
	"github.com/dydxprotocol/v4/lib"
	"github.com/dydxprotocol/v4/lib/metrics"
	epochstypes "github.com/dydxprotocol/v4/x/epochs/types"
	"github.com/dydxprotocol/v4/x/perpetuals/types"
	pricestypes "github.com/dydxprotocol/v4/x/prices/types"
	"github.com/pkg/errors"
)

// CreatePerpetual creates a new perpetual in the store.
// Returns an error if any of the perpetual fields fail validation,
// or if the `marketId` does not exist.
func (k Keeper) CreatePerpetual(
	ctx sdk.Context,
	ticker string,
	marketId uint32,
	atomicResolution int32,
	defaultFundingPpm int32,
	liquidityTier uint32,
) (types.Perpetual, error) {
	// Get the `nextId`.
	nextId := k.GetNumPerpetuals(ctx)

	// Create the perpetual.
	perpetual := types.Perpetual{
		Id:                nextId,
		Ticker:            ticker,
		MarketId:          marketId,
		AtomicResolution:  atomicResolution,
		DefaultFundingPpm: defaultFundingPpm,
		FundingIndex:      dtypes.ZeroInt(),
		OpenInterest:      types.DefaultOpenInterest,
		LiquidityTier:     liquidityTier,
	}

	if err := k.validatePerpetual(
		ctx,
		&perpetual,
	); err != nil {
		return perpetual, err
	}

	// Store the new perpetual.
	k.setPerpetual(ctx, perpetual)

	// Store the new `numPerpetuals`.
	k.setNumPerpetuals(ctx, nextId+1)

	k.SetEmptyPremiumSamples(ctx)
	k.SetEmptyPremiumVotes(ctx)

	return perpetual, nil
}

func (k Keeper) ModifyPerpetual(
	ctx sdk.Context,
	id uint32,
	ticker string,
	marketId uint32,
	defaultFundingPpm int32,
	liquidityTier uint32,
) (types.Perpetual, error) {
	// Get perpetual.
	perpetual, err := k.GetPerpetual(ctx, id)
	if err != nil {
		return perpetual, err
	}

	// Modify perpetual.
	perpetual.Ticker = ticker
	perpetual.MarketId = marketId
	perpetual.DefaultFundingPpm = defaultFundingPpm
	perpetual.LiquidityTier = liquidityTier

	// Validate updates to perpetual.
	if err = k.validatePerpetual(
		ctx,
		&perpetual,
	); err != nil {
		return perpetual, err
	}

	// Store the modified perpetual.
	k.setPerpetual(ctx, perpetual)

	return perpetual, nil
}

// getUint32InStore gets a uint32 value from store.
func (k Keeper) getUint32InStore(
	ctx sdk.Context,
	key string,
) uint32 {
	store := ctx.KVStore(k.storeKey)
	var numBytes []byte = store.Get(types.KeyPrefix(key))
	return lib.BytesToUint32(numBytes)
}

// GetNumPerpetuals returns the number of perpetuals created.
func (k Keeper) GetNumPerpetuals(
	ctx sdk.Context,
) uint32 {
	return k.getUint32InStore(ctx, types.NumPerpetualsKey)
}

// GetPerpetual returns a perpetual from its id.
func (k Keeper) GetPerpetual(
	ctx sdk.Context,
	id uint32,
) (val types.Perpetual, err error) {
	store := prefix.NewStore(ctx.KVStore(k.storeKey), types.KeyPrefix(types.PerpetualKeyPrefix))

	b := store.Get(types.PerpetualKey(id))
	if b == nil {
		return val, sdkerrors.Wrap(types.ErrPerpetualDoesNotExist, lib.Uint32ToString(id))
	}

	k.cdc.MustUnmarshal(b, &val)
	return val, nil
}

// GetAllPerpetuals returns all perpetuals, sorted by perpetual Id.
func (k Keeper) GetAllPerpetuals(ctx sdk.Context) []types.Perpetual {
	num := k.GetNumPerpetuals(ctx)
	perpetuals := make([]types.Perpetual, num)

	for i := uint32(0); i < num; i++ {
		perpetual, err := k.GetPerpetual(ctx, i)
		if err != nil {
			panic(err)
		}

		perpetuals[i] = perpetual
	}

	return perpetuals
}

// processStoredPremiums combines all stored premiums into a single premium value
// for each `MarketPremiums` in the premium storage.
// Returns a mapping from perpetual Id to summarized premium value.
// Arguments:
// - `premiumKey`: indicates whether the function is processing `PremiumSamples`
// or `PremiumVotes`.
// - `combineFunc`: a function that converts a list of premium values into one
// premium value (e.g. average or median)
// - `filterFunc`: a function that takes in a list of premium values and filter
// out some values.
// - `minNumPremiumsRequired`: minimum number of premium values required for each
// market. Padding will be added if `NumPerpetuals < minNumPremiumsRequired`.
func (k Keeper) processStoredPremiums(
	ctx sdk.Context,
	newEpochInfo epochstypes.EpochInfo,
	premiumKey string,
	minNumPremiumsRequired uint32,
	combineFunc func([]int32) int32,
	filterFunc func([]int32) []int32,
) (
	perpIdToPremium map[uint32]int32,
) {
	perpIdToPremium = make(map[uint32]int32)

	premiumStore := k.getPremiumStore(ctx, premiumKey)

	telemetry.SetGaugeWithLabels(
		[]string{
			types.ModuleName,
			metrics.NumPremiumsFromEpoch,
			metrics.Count,
		},
		float32(premiumStore.NumPremiums),
		[]gometrics.Label{
			metrics.GetLabelForIntValue(
				metrics.BlockHeight,
				int(ctx.BlockHeight()),
			),
			metrics.GetLabelForStringValue(
				metrics.PremiumType,
				premiumKey,
			),
			metrics.GetLabelForStringValue(
				metrics.EpochInfoName,
				newEpochInfo.Name,
			),
			metrics.GetLabelForIntValue(
				metrics.EpochNumber,
				int(newEpochInfo.CurrentEpoch),
			),
		},
	)

	for _, marketPremiums := range premiumStore.AllMarketPremiums {
		// Invariant: `len(marketPremiums.Premiums) <= NumPremiums`
		if uint32(len(marketPremiums.Premiums)) > premiumStore.NumPremiums {
			panic(fmt.Errorf(
				"marketPremiums (%+v) has more non-zero premiums than total number of premiums (%d)",
				marketPremiums,
				premiumStore.NumPremiums,
			))
		}

		// Use minimum number of premiums as final length of array, if it's greater than NumPremiums.
		// For `PremiumSamples`, this may happen in the event of a chain halt where there were
		// fewer than expected `funding-sample` epochs. For `PremiumVotes`, this may happen
		// if block times are longer than expected and hence there were not enough blocks to
		// collect votes.
		// Note `NumPremiums >= len(marketPremiums.Premiums)`, so `lenPadding >= 0`.
		lenPadding := int64(
			lib.MaxUint32(premiumStore.NumPremiums,
				minNumPremiumsRequired)) - int64(len(marketPremiums.Premiums))

		padding := make([]int32, lenPadding)
		paddedPremiums := append(marketPremiums.Premiums, padding...)

		perpIdToPremium[marketPremiums.PerpetualId] = combineFunc(filterFunc(paddedPremiums))
	}

	return perpIdToPremium
}

// processPremiumVotesIntoSamples summarizes premium votes from proposers into premium samples.
// For each perpetual market:
//  1. Get the median of `PremiumVotes` collected during the past `funding-sample` epoch.
//     This median value is referred to as a "sample".
//  2. Append the new "sample" to the `PremiumSamples` in state.
//  3. Clear `PremiumVotes` to an empty slice.
func (k Keeper) processPremiumVotesIntoSamples(
	ctx sdk.Context,
	newFundingSampleEpoch epochstypes.EpochInfo,
) {
	// For premium votes, we take the median of all votes without modifying the list
	// (using identify function as `filterFunc`)
	perpIdToSummarizedPremium := k.processStoredPremiums(
		ctx,
		newFundingSampleEpoch,
		types.PremiumVotesKey,
		k.GetMinNumVotesPerSample(ctx),
		lib.MustGetMedianInt32, // combineFunc
		func(input []int32) []int32 { return input }, // filterFunc
	)

	newSamples := []types.FundingPremium{}
	newSamplesForEvent := []indexerevents.FundingUpdateV1{}

	for perpId := uint32(0); perpId < k.GetNumPerpetuals(ctx); perpId++ {
		summarizedPremium, found := perpIdToSummarizedPremium[perpId]
		if !found {
			summarizedPremium = 0
		}

		telemetry.SetGaugeWithLabels(
			[]string{
				types.ModuleName,
				metrics.PremiumSampleValue,
			},
			float32(summarizedPremium),
			[]gometrics.Label{
				metrics.GetLabelForIntValue(
					metrics.BlockHeight,
					int(ctx.BlockHeight()),
				),
				metrics.GetLabelForIntValue(
					metrics.PerpetualId,
					int(perpId),
				),
				// TODO(DEC-1071): Add epoch number as label.
			},
		)

		// Append all samples (including zeros) to `newSamplesForEvent`, since
		// the indexer should forward all sample values to users.
		newSamplesForEvent = append(newSamplesForEvent, indexerevents.FundingUpdateV1{
			PerpetualId:     perpId,
			FundingValuePpm: summarizedPremium,
		})

		if summarizedPremium != 0 {
			// Append non-zero sample to `PremiumSample` storage.
			newSamples = append(newSamples, types.FundingPremium{
				PerpetualId: perpId,
				PremiumPpm:  summarizedPremium,
			})
		}
	}

	if err := k.AddPremiumSamples(ctx, newSamples); err != nil {
		panic(err)
	}

	k.indexerEventManager.AddBlockEvent(
		ctx,
		indexerevents.SubtypeFundingValues,
		indexer_manager.GetB64EncodedEventMessage(
			indexerevents.NewPremiumSamplesEvent(newSamplesForEvent),
		),
		indexer_manager.IndexerTendermintEvent_BLOCK_EVENT_END_BLOCK,
	)

	k.SetEmptyPremiumVotes(ctx)
}

// MaybeProcessNewFundingSampleEpoch summarizes premium votes stored in application
// states into new funding samples, if the current block is the start of a new
// `funding-sample` epoch. Otherwise, does nothing.
func (k Keeper) MaybeProcessNewFundingSampleEpoch(
	ctx sdk.Context,
) {
	numBlocks, err := k.epochsKeeper.NumBlocksSinceEpochStart(
		ctx,
		epochstypes.FundingSampleEpochInfoName,
	)
	if err != nil {
		panic(err)
	}

	// If the current block is not the start of a new funding-sample epoch, do nothing.
	if numBlocks != 0 {
		return
	}

	newFundingSampleEpoch := k.epochsKeeper.MustGetFundingSampleEpochInfo(ctx)

	k.processPremiumVotesIntoSamples(ctx, newFundingSampleEpoch)
}

// getFundingIndexDelta returns fundingIndexDelta which represents the change of FundingIndex since
// the last time `funding-tick` was processed.
// TODO(DEC-1536): Make the 8-hour funding rate period configurable.
func (k Keeper) getFundingIndexDelta(
	ctx sdk.Context,
	perp types.Perpetual,
	big8hrFundingRatePpm *big.Int,
	timeSinceLastFunding uint32,
) (
	fundingIndexDelta *big.Int,
	err error,
) {
	marketPrice, err := k.pricesKeeper.GetMarketPrice(ctx, perp.MarketId)
	if err != nil {
		return nil, fmt.Errorf("failed to get market price for perpetual %v, err = %w", perp.Id, err)
	}

	// Get pro-rated funding rate adjusted by time delta.
	proratedFundingRate := new(big.Rat).SetInt(big8hrFundingRatePpm)
	proratedFundingRate.Mul(
		proratedFundingRate,
		new(big.Rat).SetUint64(uint64(timeSinceLastFunding)),
	)

	proratedFundingRate.Quo(
		proratedFundingRate,
		new(big.Rat).SetUint64(3600*8),
	)

	bigFundingIndexDelta := lib.FundingRateToIndex(
		proratedFundingRate,
		perp.GetAtomicResolution(),
		marketPrice.Price,
		marketPrice.Exponent,
	)

	return bigFundingIndexDelta, nil
}

// GetAddPremiumVotes returns the newest premiums for all perpetuals,
// if the current block is the start of a new funding-sample epoch.
// Otherwise, does nothing and returns an empty message.
// Does not make any changes to state.
// TODO(DEC-1310): Rename to reflect new premium sampling design.
func (k Keeper) GetAddPremiumVotes(
	ctx sdk.Context,
) (
	msgAddPremiumVotes *types.MsgAddPremiumVotes,
) {
	newPremiumVotes, err := k.sampleAllPerpetuals(ctx)
	if err != nil {
		k.Logger(ctx).Error(fmt.Sprintf(
			"failed to sample perpetuals, err = %v",
			err,
		))
	}

	telemetry.SetGaugeWithLabels(
		[]string{
			types.ModuleName,
			metrics.NewPremiumVotes,
			metrics.Count,
			metrics.Proposer,
		},
		float32(len(newPremiumVotes)),
		[]gometrics.Label{
			metrics.GetLabelForIntValue(
				metrics.BlockHeight,
				int(ctx.BlockHeight()),
			),
			// TODO(DEC-1071): Add epoch number as label.
		},
	)

	return types.NewMsgAddPremiumVotes(newPremiumVotes)
}

// sampleAllPerpetuals takes premium samples for each perpetual market,
// and returns as a list of samples sorted by perpetual Id.
// Markets with zero premium samples are skipped in return value.
func (k Keeper) sampleAllPerpetuals(ctx sdk.Context) (
	samples []types.FundingPremium,
	err error,
) {
	allPerpetuals := k.GetAllPerpetuals(ctx)
	allLiquidityTiers := k.GetAllLiquidityTiers(ctx)

	// Calculate `maxAbsPremiumVotePpm` of each liquidity tier.
	liquidityTierToMaxAbsPremiumVotePpm := k.getLiquidityTiertoMaxAbsPremiumVotePpm(ctx)

	// Measure latency of calling `GetPricePremiumForPerpetual` for all perpetuals.
	defer telemetry.ModuleMeasureSince(
		types.ModuleName,
		time.Now(),
		metrics.GetAllPerpetualPricePremiums,
		metrics.Latency,
	)

	for _, perp := range allPerpetuals {
		marketPrice, err := k.pricesKeeper.GetMarketPrice(ctx, perp.MarketId)
		if err != nil {
			panic(err)
		}

		// Get impact notional corresponding to this perpetual market (panic if its liquidity tier doesn't exist).
		liquidityTier := lib.MustGetValue(allLiquidityTiers, uint(perp.LiquidityTier))
		bigImpactNotionalQuoteQuantums := new(big.Int).SetUint64(liquidityTier.ImpactNotional)

		premiumPpm, err := k.pricePremiumGetter.GetPricePremiumForPerpetual(
			ctx,
			perp.Id,
			types.GetPricePremiumParams{
				MarketPrice:                 marketPrice,
				BaseAtomicResolution:        perp.AtomicResolution,
				QuoteAtomicResolution:       lib.QuoteCurrencyAtomicResolution,
				ImpactNotionalQuoteQuantums: bigImpactNotionalQuoteQuantums,
				// Get `maxAbsPremiumVotePpm` for this perpetual's liquidity tier (panic if index is invalid).
				MaxAbsPremiumVotePpm: lib.MustGetValue(liquidityTierToMaxAbsPremiumVotePpm, uint(perp.LiquidityTier)),
			},
		)
		if err != nil {
			return nil, err
		}

		if premiumPpm == 0 {
			// Do not include zero premiums in message.
			k.Logger(ctx).Debug(
				fmt.Sprintf(
					"Perpetual (%d) has zero sampled premium. Not including in AddPremiumVotes message",
					perp.Id,
				))
			continue
		}

		samples = append(
			samples,
			*types.NewFundingPremium(
				perp.Id,
				premiumPpm,
			),
		)
	}
	return samples, nil
}

// GetRemoveSampleTailsFunc returns a function that sorts the input samples (in place) and returns
// the sub-slice from the original slice, which removes `tailRemovalRatePpm` from top and bottom from the samples.
// Note the returned sub-slice is not a copy but references a sub-sequence of the original slice.
func (k Keeper) GetRemoveSampleTailsFunc(
	ctx sdk.Context,
	tailRemovalRatePpm uint32,
) func(input []int32) (output []int32) {
	return func(premiums []int32) []int32 {
		totalRemoval := lib.Int64MulPpm(
			int64(len(premiums)),
			tailRemovalRatePpm*2,
		)

		// Return early if no tail to remove.
		if totalRemoval == 0 {
			return premiums
		} else if totalRemoval >= int64(len(premiums)) {
			k.Logger(ctx).Error(fmt.Sprintf(
				"GetRemoveSampleTailsFunc: totalRemoval(%d) > length of premium samples (%d); skip removing",
				totalRemoval,
				len(premiums),
			))
			return premiums
		}

		bottomRemoval := totalRemoval / 2
		topRemoval := totalRemoval - bottomRemoval

		end := int64(len(premiums)) - topRemoval

		sort.Slice(premiums, func(i, j int) bool { return premiums[i] < premiums[j] })

		return premiums[bottomRemoval:end]
	}
}

// MaybeProcessNewFundingTickEpoch processes funding ticks if the current block
// is the start of a new funding-tick epoch. Otherwise, do nothing.
func (k Keeper) MaybeProcessNewFundingTickEpoch(ctx sdk.Context) {
	numBlocks, err := k.epochsKeeper.NumBlocksSinceEpochStart(
		ctx,
		epochstypes.FundingTickEpochInfoName,
	)
	if err != nil {
		panic(err)
	}

	// If the current block is not the start of a new funding-tick epoch, do nothing.
	if numBlocks != 0 {
		return
	}

	allPerps := k.GetAllPerpetuals(ctx)
	fundingRateClampFactorPpm := k.GetFundingRateClampFactorPpm(ctx)

	fundingTickEpochInfo := k.epochsKeeper.MustGetFundingTickEpochInfo(ctx)
	fundingSampleEpochInfo := k.epochsKeeper.MustGetFundingSampleEpochInfo(ctx)

	// Use the ratio between funding-tick and funding-sample durations
	// as minimum number of samples required to get a premium rate.
	minSampleRequiredForPremiumRate := lib.DivisionUint32RoundUp(
		fundingTickEpochInfo.Duration,
		fundingSampleEpochInfo.Duration,
	)

	// TODO(DEC-1449): Read `RemovedTailSampleRatioPpm` from state. Determine initial value.
	// This value should be 0% or some low value like 5%, since we already has a layer of
	// filtering we compute samples as median of premium votes.
	tailRemovalRatePpm := types.RemovedTailSampleRatioPpm

	// Get `sampleTailsRemovalFunc` which removes a percentage of top and bottom samples
	// from the input after sorting.

	sampleTailsRemovalFunc := k.GetRemoveSampleTailsFunc(ctx, tailRemovalRatePpm)

	// Process stored samples from last `funding-tick` epoch, and retrieve
	// a mapping from `perpetualId` to summarized premium rate for this epoch.
	// For premiums, we first remove a fixed amount of bottom/top samples, then
	// take the average of the remaining samples.
	perpIdToPremiumPpm := k.processStoredPremiums(
		ctx,
		fundingTickEpochInfo,
		types.PremiumSamplesKey,
		minSampleRequiredForPremiumRate,
		lib.AvgInt32,           // combineFunc
		sampleTailsRemovalFunc, // filterFunc
	)

	newFundingRatesAndIndicesForEvent := []indexerevents.FundingUpdateV1{}

	for _, perp := range allPerps {
		premiumPpm, found := perpIdToPremiumPpm[perp.Id]

		if !found {
			k.Logger(ctx).Info(
				fmt.Sprintf(
					"MaybeProcessNewFundingTickEpoch: No samples found for perpetual (%v) during `funding-tick` epoch\n",
					perp.Id,
				),
			)

			premiumPpm = 0
		}

		bigFundingRatePpm := new(big.Int).SetInt64(int64(premiumPpm))

		// funding rate = premium + default funding
		bigFundingRatePpm.Add(
			bigFundingRatePpm,
			new(big.Int).SetInt64(int64(perp.DefaultFundingPpm)),
		)

		liquidityTier, err := k.GetLiquidityTier(ctx, perp.LiquidityTier)
		if err != nil {
			panic(err)
		}

		// Panic if maintenance fraction ppm is larger than its maximum value.
		if liquidityTier.MaintenanceFractionPpm > types.MaxMaintenanceFractionPpm {
			panic(sdkerrors.Wrapf(
				types.ErrMaintenanceFractionPpmExceedsMax,
				"perpetual Id = (%d), liquidity tier Id = (%d), maintenance fraction ppm = (%v)",
				perp.Id, perp.LiquidityTier, liquidityTier.MaintenanceFractionPpm,
			))
		}

		// Clamp funding rate according to equation:
		// |R| <= clamp_factor * (initial margin - maintenance margin)
		fundingRateUpperBoundPpm := liquidityTier.GetMaxAbsFundingClampPpm(fundingRateClampFactorPpm)
		bigFundingRatePpm = lib.BigIntClamp(
			bigFundingRatePpm,
			new(big.Int).Neg(fundingRateUpperBoundPpm),
			fundingRateUpperBoundPpm,
		)

		// Emit clamped funding rate.
		telemetry.SetGaugeWithLabels(
			[]string{
				types.ModuleName,
				metrics.PremiumRate,
			},
			float32(bigFundingRatePpm.Int64()),
			[]gometrics.Label{
				metrics.GetLabelForIntValue(
					metrics.PerpetualId,
					int(perp.Id),
				),
				// TODO(DEC-1071): Add epoch number as label.
			},
		)

		if bigFundingRatePpm.Cmp(lib.BigMaxInt32()) > 0 {
			panic(sdkerrors.Wrapf(
				types.ErrFundingRateInt32Overflow,
				"perpetual Id = (%d), funding rate = (%v)",
				perp.Id, bigFundingRatePpm,
			))
		}

		if bigFundingRatePpm.Sign() != 0 {
			fundingIndexDelta, err := k.getFundingIndexDelta(
				ctx,
				perp,
				bigFundingRatePpm,
				// use funding-tick duration as `timeSinceLastFunding`
				// TODO(DEC-1483): Handle the case when duration value is updated
				// during the epoch.
				fundingTickEpochInfo.Duration,
			)
			if err != nil {
				panic(err)
			}

			if err := k.ModifyFundingIndex(ctx, perp.Id, fundingIndexDelta); err != nil {
				panic(err)
			}
		}

		// Get perpetual object with updated funding index.
		perp, err = k.GetPerpetual(ctx, perp.Id)
		if err != nil {
			panic(err)
		}
		newFundingRatesAndIndicesForEvent = append(newFundingRatesAndIndicesForEvent, indexerevents.FundingUpdateV1{
			PerpetualId:     perp.Id,
			FundingValuePpm: int32(bigFundingRatePpm.Int64()),
			FundingIndex:    perp.FundingIndex,
		})
	}

	k.indexerEventManager.AddBlockEvent(
		ctx,
		indexerevents.SubtypeFundingValues,
		indexer_manager.GetB64EncodedEventMessage(
			indexerevents.NewFundingRatesAndIndicesEvent(newFundingRatesAndIndicesForEvent),
		),
		indexer_manager.IndexerTendermintEvent_BLOCK_EVENT_END_BLOCK,
	)

	// Clear premium samples.
	k.SetEmptyPremiumSamples(ctx)
}

// GetNetNotional returns the net notional in quote quantums, which can be represented by the following equation:
// `quantums / 10^baseAtomicResolution * marketPrice * 10^marketExponent * 10^quoteAtomicResolution`.
// Note that longs are positive, and shorts are negative.
// Returns an error if a perpetual with `id` does not exist or if the `Perpetual.MarketId` does
// not exist.
func (k Keeper) GetNetNotional(
	ctx sdk.Context,
	id uint32,
	bigQuantums *big.Int,
) (
	bigNetNotionalQuoteQuantums *big.Int,
	err error,
) {
	defer telemetry.ModuleMeasureSince(
		types.ModuleName,
		time.Now(),
		metrics.GetNetNotional,
		metrics.Latency,
	)

	perpetual, marketPrice, err := k.GetPerpetualAndMarketPrice(ctx, id)
	if err != nil {
		return new(big.Int), err
	}

	bigQuoteQuantums := lib.BaseToQuoteQuantums(
		bigQuantums,
		perpetual.AtomicResolution,
		marketPrice.Price,
		marketPrice.Exponent,
	)

	return bigQuoteQuantums, nil
}

// GetNotionalInBaseQuantums returns the net notional in base quantums, which can be represented
// by the following equation:
// `quoteQuantums * 10^baseAtomicResolution / (marketPrice * 10^marketExponent * 10^quoteAtomicResolution)`.
// Note that longs are positive, and shorts are negative.
// Returns an error if a perpetual with `id` does not exist or if the `Perpetual.MarketId` does
// not exist.
func (k Keeper) GetNotionalInBaseQuantums(
	ctx sdk.Context,
	id uint32,
	bigQuoteQuantums *big.Int,
) (
	bigBaseQuantums *big.Int,
	err error,
) {
	defer telemetry.ModuleMeasureSince(
		types.ModuleName,
		time.Now(),
		metrics.GetNotionalInBaseQuantums,
		metrics.Latency,
	)

	perpetual, marketPrice, err := k.GetPerpetualAndMarketPrice(ctx, id)
	if err != nil {
		return new(big.Int), err
	}

	bigBaseQuantums = lib.QuoteToBaseQuantums(
		bigQuoteQuantums,
		perpetual.AtomicResolution,
		marketPrice.Price,
		marketPrice.Exponent,
	)
	return bigBaseQuantums, nil
}

// GetNetCollateral returns the net collateral in quote quantums. The net collateral is equal to
// the net open notional, which can be represented by the following equation:
// `quantums / 10^baseAtomicResolution * marketPrice * 10^marketExponent * 10^quoteAtomicResolution`.
// Note that longs are positive, and shorts are negative.
// Returns an error if a perpetual with `id` does not exist or if the `Perpetual.MarketId` does
// not exist.
func (k Keeper) GetNetCollateral(
	ctx sdk.Context,
	id uint32,
	bigQuantums *big.Int,
) (
	bigNetCollateralQuoteQuantums *big.Int,
	err error,
) {
	// The net collateral is equal to the net open notional.
	return k.GetNetNotional(ctx, id, bigQuantums)
}

// GetMarginRequirements returns initial and maintenance margin requirements in quote quantums, given the position
// size in base quantums.
//
// Margin requirements are a function of the absolute value of the open notional of the position as well as
// the parameters of the relevant `LiquidityTier` of the perpetual.
// Initial margin requirement is determined by multiplying `InitialMarginPpm` by `marginAdjustmentPpm`,
// then limited to a maximum of 100%. `marginAdjustmentPpm“ is given by the equation
// `sqrt(notionalValue / liquidityTier.BasePositionNotional)` limited to a minimum of 100%.
// `notionalValue` is determined by multiplying the size of the position by the oracle price of the position.
// Maintenance margin requirement is then simply a fraction (`maintenanceFractionPpm`) of initial margin requirement.
//
// Returns an error if a perpetual with `id`, `Perpetual.MarketId`, or `Perpetual.LiquidityTier` does not exist.
func (k Keeper) GetMarginRequirements(
	ctx sdk.Context,
	id uint32,
	bigQuantums *big.Int,
) (
	bigInitialMarginQuoteQuantums *big.Int,
	bigMaintenanceMarginQuoteQuantums *big.Int,
	err error,
) {
	defer telemetry.ModuleMeasureSince(
		types.ModuleName,
		time.Now(),
		metrics.GetMarginRequirements,
		metrics.Latency,
	)
	// Get perpetual and market price.
	perpetual, marketPrice, err := k.GetPerpetualAndMarketPrice(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	// Get perpetual's liquidity tier.
	liquidityTier, err := k.GetLiquidityTier(ctx, perpetual.LiquidityTier)
	if err != nil {
		return nil, nil, err
	}

	// Always consider the magnitude of the position regardless of whether it is long/short.
	bigAbsQuantums := new(big.Int).Set(bigQuantums).Abs(bigQuantums)

	bigQuoteQuantums := lib.BaseToQuoteQuantums(
		bigAbsQuantums,
		perpetual.AtomicResolution,
		marketPrice.Price,
		marketPrice.Exponent,
	)

	// Initial margin requirement quote quantums = size in quote quantums * adjusted initial margin PPM.
	bigInitialMarginQuoteQuantums = liquidityTier.GetAdjustedInitialMarginQuoteQuantums(bigQuoteQuantums)

	// Maintenance margin requirement quote quantums = IM in quote quantums * maintenance fraction PPM.
	bigMaintenanceMarginQuoteQuantums =
		lib.BigIntMulPpm(bigInitialMarginQuoteQuantums, liquidityTier.MaintenanceFractionPpm)

	return bigInitialMarginQuoteQuantums, bigMaintenanceMarginQuoteQuantums, nil
}

// GetSettlement returns the net settlement amount (in quote quantums) given the
// perpetual Id and position size (in base quantums).
// When handling rounding, always round positive settlement amount to zero, and
// negative amount to negative infinity. This ensures total amount of value does
// not increase after settlement.
// Example:
// For a round of funding payments, accounts A, B are to receive 102.5 quote quantums;
// account C is to pay 205 quote quantums.
// After settlement, accounts A, B are credited 102 quote quantum each; account C
// is debited 205 quote quantums.
func (k Keeper) GetSettlement(
	ctx sdk.Context,
	perpetualId uint32,
	quantums *big.Int,
	index *big.Int,
) (
	bigNetSettlement *big.Int,
	newFundingIndex *big.Int,
	err error,
) {
	// Get the perpetual for newest FundingIndex.
	perpetual, err := k.GetPerpetual(ctx, perpetualId)
	if err != nil {
		return big.NewInt(0), big.NewInt(0), err
	}

	indexDelta := new(big.Int).Sub(perpetual.FundingIndex.BigInt(), index)

	// if indexDelta is zero, then net settlement is zero.
	if indexDelta.Sign() == 0 {
		return big.NewInt(0), perpetual.FundingIndex.BigInt(), nil
	}

	bigNetSettlement = new(big.Int).Mul(indexDelta, quantums)

	// `bigNetSettlement`` carries sign. `indexDelta`` is the increase in `fundingIndex`, so if
	// the position is long (positive), the net settlement should be short (negative), and vice versa.
	// Thus, always negate `bigNetSettlement` here.
	bigNetSettlement = bigNetSettlement.Neg(bigNetSettlement)

	// `Div` implements Euclidean division (unlike Go). When the diviser is positive,
	// division result always rounds towards negative infinity.
	return bigNetSettlement.Div(
		bigNetSettlement,
		big.NewInt(int64(lib.OneMillion)),
	), perpetual.FundingIndex.BigInt(), nil
}

// GetPremiumSamples reads premium samples from the current `funding-tick` epoch,
// stored in a `PremiumStore` struct.
func (k Keeper) GetPremiumSamples(ctx sdk.Context) (
	premiumStore types.PremiumStore,
) {
	return k.getPremiumStore(ctx, types.PremiumSamplesKey)
}

// GetPremiumVotes premium sample votes from the current `funding-sample` epoch,
// stored in a `PremiumStore` struct.
func (k Keeper) GetPremiumVotes(ctx sdk.Context) (
	premiumStore types.PremiumStore,
) {
	return k.getPremiumStore(ctx, types.PremiumVotesKey)
}

func (k Keeper) getPremiumStore(ctx sdk.Context, key string) (
	premiumStore types.PremiumStore,
) {
	store := ctx.KVStore(k.storeKey)

	premiumStoreBytes := store.Get(types.KeyPrefix(key))

	if premiumStoreBytes == nil {
		return types.PremiumStore{}
	}

	k.cdc.MustUnmarshal(premiumStoreBytes, &premiumStore)
	return premiumStore
}

// AddPremiumVotes adds a list of new premium votes to state.
func (k Keeper) AddPremiumVotes(
	ctx sdk.Context,
	newVotes []types.FundingPremium,
) error {
	return k.addToPremiumStore(
		ctx,
		newVotes,
		types.PremiumVotesKey,
		metrics.AddPremiumVotes,
	)
}

// AddPremiumSamples adds a list of new premium samples to state.
func (k Keeper) AddPremiumSamples(
	ctx sdk.Context,
	newSamples []types.FundingPremium,
) error {
	return k.addToPremiumStore(
		ctx,
		newSamples,
		types.PremiumSamplesKey,
		metrics.AddPremiumSamples,
	)
}

func (k Keeper) addToPremiumStore(
	ctx sdk.Context,
	newSamples []types.FundingPremium,
	key string,
	metricsLabel string,
) error {
	defer telemetry.ModuleMeasureSince(
		types.ModuleName,
		time.Now(),
		metricsLabel,
		metrics.Latency,
	)

	premiumStore := k.getPremiumStore(ctx, key)

	marketPremiumsMap := premiumStore.GetMarketPremiumsMap()

	numPerpetuals := k.GetNumPerpetuals(ctx)

	for _, sample := range newSamples {
		// Invariant: perpetualId < numPerpetuals
		if sample.PerpetualId >= numPerpetuals {
			return sdkerrors.Wrapf(
				types.ErrPerpetualDoesNotExist,
				"Perpetual Id from new sample: %d",
				sample.PerpetualId,
			)
		}

		premiums, found := marketPremiumsMap[sample.PerpetualId]
		if !found {
			premiums = types.MarketPremiums{
				Premiums:    []int32{},
				PerpetualId: sample.PerpetualId,
			}
		}
		premiums.Premiums = append(premiums.Premiums, sample.PremiumPpm)
		marketPremiumsMap[sample.PerpetualId] = premiums
	}

	k.setPremiumStore(
		ctx,
		*types.NewPremiumStoreFromMarketPremiumMap(
			marketPremiumsMap,
			numPerpetuals,
			premiumStore.NumPremiums+1, // increment NumPerpetuals
		),
		key,
	)

	return nil
}

func (k Keeper) ModifyFundingIndex(
	ctx sdk.Context,
	perpetualId uint32,
	bigFundingIndexDelta *big.Int,
) (
	err error,
) {
	// Get perpetual.
	perpetual, err := k.GetPerpetual(ctx, perpetualId)
	if err != nil {
		return err
	}

	bigFundingIndex := new(big.Int).Set(perpetual.FundingIndex.BigInt())
	bigFundingIndex.Add(bigFundingIndex, bigFundingIndexDelta)

	perpetual.FundingIndex = dtypes.NewIntFromBigInt(bigFundingIndex)
	k.setPerpetual(ctx, perpetual)
	return nil
}

// SetEmptyPremiumSamples initializes empty premium samples for all perpetuals
func (k Keeper) SetEmptyPremiumSamples(
	ctx sdk.Context,
) {
	k.setPremiumStore(
		ctx,
		types.PremiumStore{},
		types.PremiumSamplesKey,
	)
}

// SetEmptyPremiumSamples initializes empty premium sample votes for all perpetuals
func (k Keeper) SetEmptyPremiumVotes(
	ctx sdk.Context,
) {
	k.setPremiumStore(
		ctx,
		types.PremiumStore{},
		types.PremiumVotesKey,
	)
}

func (k Keeper) ModifyOpenInterest(
	ctx sdk.Context,
	id uint32,
	isIncrease bool,
	deltaBaseQuantums uint64,
) (newOpenInterestBaseQuantums uint64, err error) {
	return 0, types.ErrNotImplementedOpenInterest
}

func (k Keeper) setPerpetual(
	ctx sdk.Context,
	perpetual types.Perpetual,
) {
	b := k.cdc.MustMarshal(&perpetual)
	perpetualStore := prefix.NewStore(ctx.KVStore(k.storeKey), types.KeyPrefix(types.PerpetualKeyPrefix))
	perpetualStore.Set(types.PerpetualKey(perpetual.Id), b)
}

// setUint32InStore sets a uint32 value in store for a given key.
func (k Keeper) setUint32InStore(
	ctx sdk.Context,
	key string,
	num uint32,
) {
	// Get necessary stores
	store := ctx.KVStore(k.storeKey)

	// Set key value pair
	store.Set(types.KeyPrefix(key), lib.Uint32ToBytes(num))
}

func (k Keeper) setNumPerpetuals(
	ctx sdk.Context,
	num uint32,
) {
	// Set `numPerpetuals`
	k.setUint32InStore(ctx, types.NumPerpetualsKey, num)
}

// GetPerpetualAndMarketPrice retrieves a Perpetual by its id and its corresponding MarketPrice.
func (k Keeper) GetPerpetualAndMarketPrice(
	ctx sdk.Context,
	perpetualId uint32,
) (types.Perpetual, pricestypes.MarketPrice, error) {
	defer telemetry.ModuleMeasureSince(
		types.ModuleName,
		time.Now(),
		metrics.GetPerpetualAndMarketPrice,
		metrics.Latency,
	)

	// Get perpetual.
	perpetual, err := k.GetPerpetual(ctx, perpetualId)
	if err != nil {
		return perpetual, pricestypes.MarketPrice{}, err
	}

	// Get market price.
	marketPrice, err := k.pricesKeeper.GetMarketPrice(ctx, perpetual.MarketId)
	if err != nil {
		if sdkerrors.IsOf(err, pricestypes.ErrMarketPriceDoesNotExist) {
			return perpetual, marketPrice, sdkerrors.Wrap(
				types.ErrMarketDoesNotExist,
				fmt.Sprintf(
					"Market ID %d does not exist on perpetual ID %d",
					perpetual.MarketId,
					perpetual.Id,
				),
			)
		} else {
			return perpetual, marketPrice, err
		}
	}

	return perpetual, marketPrice, nil
}

// Performs the following validation (stateful and stateless) on a `Perpetual`
// structs fields, returning an error if any conditions are false:
// - MarketId is not a valid market.
// - All stateless validation performed in `validatePerpetualStateless`.
func (k Keeper) validatePerpetual(
	ctx sdk.Context,
	perpetual *types.Perpetual,
) error {
	if err := k.validatePerpetualStateless(perpetual); err != nil {
		return err
	}
	// Validate `marketId`.
	if _, err := k.pricesKeeper.GetMarketPrice(ctx, perpetual.MarketId); err != nil {
		return err
	}
	// Validate `liquidityTier`.
	if perpetual.LiquidityTier >= k.GetNumLiquidityTiers(ctx) {
		return sdkerrors.Wrap(types.ErrLiquidityTierDoesNotExist, lib.Uint32ToString(perpetual.LiquidityTier))
	}

	return nil
}

// Performs the following validation (stateful and stateless) on a `Perpetual`
// structs fields, returning an error if any conditions are false:
// - Ticker is a non-empty string.
func (k Keeper) validatePerpetualStateless(perpetual *types.Perpetual) error {
	// Validate `ticker`.
	if len(perpetual.Ticker) == 0 {
		return errors.WithStack(types.ErrTickerEmptyString)
	}

	// Validate `defaultFundingPpm`
	defaultFundingPpm := lib.AbsInt32(perpetual.DefaultFundingPpm)
	if defaultFundingPpm > types.MaxDefaultFundingPpmAbs {
		return sdkerrors.Wrap(types.ErrDefaultFundingPpmMagnitudeExceedsMax, lib.Int32ToString(perpetual.DefaultFundingPpm))
	}

	return nil
}

func (k Keeper) setPremiumStore(
	ctx sdk.Context,
	premiumStore types.PremiumStore,
	key string,
) {
	b := k.cdc.MustMarshal(&premiumStore)

	// Get necessary stores
	store := ctx.KVStore(k.storeKey)

	store.Set(types.KeyPrefix(key), b)
}

func (k Keeper) SetPremiumSamples(
	ctx sdk.Context,
	premiumStore types.PremiumStore,
) {
	k.setPremiumStore(ctx, premiumStore, types.PremiumSamplesKey)
}

func (k Keeper) SetPremiumVotes(
	ctx sdk.Context,
	premiumStore types.PremiumStore,
) {
	k.setPremiumStore(ctx, premiumStore, types.PremiumVotesKey)
}

// PerformStatefulPremiumVotesValidation performs stateful validation on `MsgAddPremiumVotes`.
// For each vote, it checks that:
// - The perpetual Id is valid.
// - The premium vote value is correctly clamped.
func (k Keeper) PerformStatefulPremiumVotesValidation(
	ctx sdk.Context,
	msg *types.MsgAddPremiumVotes,
) (
	err error,
) {
	numPerpetuals := k.GetNumPerpetuals(ctx)
	liquidityTierToMaxAbsPremiumVotePpm := k.getLiquidityTiertoMaxAbsPremiumVotePpm(ctx)

	for _, vote := range msg.Votes {
		// Check that the perpetual Id is valid.
		if vote.PerpetualId >= numPerpetuals {
			return sdkerrors.Wrapf(
				types.ErrPerpetualDoesNotExist,
				"perpetualId = %d",
				vote.PerpetualId,
			)
		}

		// Check that premium vote value is correctly clamped.
		// Get perpetual object based on perpetual ID.
		perpetual, err := k.GetPerpetual(ctx, vote.PerpetualId)
		if err != nil {
			return err
		}
		// Get `maxAbsPremiumVotePpm` for this perpetual's liquidity tier (panic if index is invalid).
		maxAbsPremiumVotePpm := lib.MustGetValue(liquidityTierToMaxAbsPremiumVotePpm, uint(perpetual.LiquidityTier))
		// Check premium vote value is within bounds.
		bigAbsPremiumPpm := new(big.Int).SetUint64(uint64(
			lib.AbsInt32(vote.PremiumPpm),
		))
		if bigAbsPremiumPpm.Cmp(maxAbsPremiumVotePpm) > 0 {
			return sdkerrors.Wrapf(
				types.ErrPremiumVoteNotClamped,
				"perpetualId = %d, premium vote = %d, maxAbsPremiumVotePpm = %d",
				vote.PerpetualId,
				vote.PremiumPpm,
				maxAbsPremiumVotePpm,
			)
		}
	}

	return nil
}

/* === LIQUIDITY TIER FUNCTIONS === */

// `CreateLiquidityTier` creates a new liquidity tier in the store.
// Returns an error if any of its fields fails validation.
func (k Keeper) CreateLiquidityTier(
	ctx sdk.Context,
	name string,
	initialMarginPpm uint32,
	maintenanceFractionPpm uint32,
	basePositionNotional uint64,
	impactNotional uint64,
) (
	liquidityTier types.LiquidityTier,
	err error,
) {
	// Get id for a new liquidity tier.
	nextId := k.GetNumLiquidityTiers(ctx)

	liquidityTier = types.LiquidityTier{
		Id:                     nextId,
		Name:                   name,
		InitialMarginPpm:       initialMarginPpm,
		MaintenanceFractionPpm: maintenanceFractionPpm,
		BasePositionNotional:   basePositionNotional,
		ImpactNotional:         impactNotional,
	}

	// Validate liquidity tier's fields.
	if err := liquidityTier.Validate(); err != nil {
		return liquidityTier, err
	}

	// Set liquidity tier in store.
	k.setLiquidityTier(ctx, liquidityTier)
	// Increase `numLiquidityTiers` by 1.
	k.setNumLiquidityTiers(ctx, nextId+1)

	return liquidityTier, nil
}

// `ModifyLiquidityTier` modifies a liquidity tier in the store.
func (k Keeper) ModifyLiquidityTier(
	ctx sdk.Context,
	id uint32,
	name string,
	initialMarginPpm uint32,
	maintenanceFractionPpm uint32,
	basePositionNotional uint64,
	impactNotional uint64,
) (
	liquidityTier types.LiquidityTier,
	err error,
) {
	// Retrieve LiquidityTier.
	liquidityTier, err = k.GetLiquidityTier(ctx, id)
	if err != nil {
		return liquidityTier, err
	}

	// Modify LiquidityTier.
	liquidityTier.Name = name
	liquidityTier.InitialMarginPpm = initialMarginPpm
	liquidityTier.MaintenanceFractionPpm = maintenanceFractionPpm
	liquidityTier.BasePositionNotional = basePositionNotional
	liquidityTier.ImpactNotional = impactNotional

	// Validate modified fields.
	if err = liquidityTier.Validate(); err != nil {
		return liquidityTier, err
	}

	// Store LiquidityTier.
	k.setLiquidityTier(ctx, liquidityTier)

	return liquidityTier, nil
}

// `GetNumLiquidityTiers` returns the number of liquidity tiers created (`numLiquidityTiers`).
func (k Keeper) GetNumLiquidityTiers(ctx sdk.Context) (
	numLiquidityTiers uint32,
) {
	return k.getUint32InStore(ctx, types.NumLiquidityTiersKey)
}

// `setNumLiquidityTiers` sets number of liquidity tiers in store.
func (k Keeper) setNumLiquidityTiers(
	ctx sdk.Context,
	num uint32,
) {
	// Set `numLiquidityTiers`.
	k.setUint32InStore(ctx, types.NumLiquidityTiersKey, num)
}

// `GetLiquidityTier` gets a liquidity tier given its id.
func (k Keeper) GetLiquidityTier(ctx sdk.Context, id uint32) (
	liquidityTier types.LiquidityTier,
	err error,
) {
	store := prefix.NewStore(ctx.KVStore(k.storeKey), types.KeyPrefix(types.LiquidityTierKeyPrefix))

	b := store.Get(types.LiquidityTierKey(id))
	if b == nil {
		return liquidityTier, sdkerrors.Wrap(types.ErrLiquidityTierDoesNotExist, lib.Uint32ToString(id))
	}

	k.cdc.MustUnmarshal(b, &liquidityTier)
	return liquidityTier, nil
}

// `GetAllLiquidityTiers` returns all liquidity tiers, sorted by id.
func (k Keeper) GetAllLiquidityTiers(ctx sdk.Context) (
	liquidityTiers []types.LiquidityTier,
) {
	num := k.GetNumLiquidityTiers(ctx)
	liquidityTiers = make([]types.LiquidityTier, num)

	for i := uint32(0); i < num; i++ {
		liquidityTier, err := k.GetLiquidityTier(ctx, i)
		if err != nil {
			panic(err)
		}

		liquidityTiers[i] = liquidityTier
	}

	return liquidityTiers
}

// `setLiquidityTier` sets a liquidity tier in store.
func (k Keeper) setLiquidityTier(
	ctx sdk.Context,
	liquidityTier types.LiquidityTier,
) {
	b := k.cdc.MustMarshal(&liquidityTier)
	liquidityTierStore := prefix.NewStore(ctx.KVStore(k.storeKey), types.KeyPrefix(types.LiquidityTierKeyPrefix))
	liquidityTierStore.Set(types.LiquidityTierKey(liquidityTier.Id), b)
}

/* === PARAMETERS FUNCTIONS === */
// `GetParams` returns all perpetuals module parameters as a `Params` object from store.
func (k Keeper) GetParams(ctx sdk.Context) types.Params {
	return types.Params{
		FundingRateClampFactorPpm: k.GetFundingRateClampFactorPpm(ctx),
		PremiumVoteClampFactorPpm: k.GetPremiumVoteClampFactorPpm(ctx),
		MinNumVotesPerSample:      k.GetMinNumVotesPerSample(ctx),
	}
}

// `GetFundingRateClampFactorPpm` returns funding rate clamp factor (in parts-per-million).
func (k Keeper) GetFundingRateClampFactorPpm(ctx sdk.Context) uint32 {
	return k.getUint32InStore(ctx, types.FundingRateClampFactorPpmKey)
}

// `SetFundingRateClampFactorPpm` sets funding rate clamp factor (in parts-per-million) in store
// and returns error instead if parameter value fails validation.
func (k Keeper) SetFundingRateClampFactorPpm(ctx sdk.Context, num uint32) error {
	// Validate `num` first.
	if err := types.ValidateFundingRateClampFactorPpm(num); err != nil {
		return err
	}
	// Set 'fundingRateClampFactorPpm`.
	k.setUint32InStore(ctx, types.FundingRateClampFactorPpmKey, num)
	return nil
}

// `GetPremiumVoteClampFactorPpm` returns premium vote clamp factor (in parts-per-million).
func (k Keeper) GetPremiumVoteClampFactorPpm(ctx sdk.Context) uint32 {
	return k.getUint32InStore(ctx, types.PremiumVoteClampFactorPpmKey)
}

// `SetPremiumVoteClampFactorPpm` sets premium vote clamp factor (in parts-per-million) in store
// and returns error instead if parameter value fails validation.
func (k Keeper) SetPremiumVoteClampFactorPpm(ctx sdk.Context, num uint32) error {
	// Validate `num` first.
	if err := types.ValidatePremiumVoteClampFactorPpm(num); err != nil {
		return err
	}
	// Set 'premiumVoteClampFactorPpm`.
	k.setUint32InStore(ctx, types.PremiumVoteClampFactorPpmKey, num)
	return nil
}

// `GetMinNumVotesPerSample` returns minimum number of votes per sample.
func (k Keeper) GetMinNumVotesPerSample(ctx sdk.Context) uint32 {
	return k.getUint32InStore(ctx, types.MinNumVotesPerSampleKey)
}

// `SetMinNumVotesPerSample` sets minimum number of votes per sample in store.
func (k Keeper) SetMinNumVotesPerSample(ctx sdk.Context, num uint32) error {
	k.setUint32InStore(ctx, types.MinNumVotesPerSampleKey, num)
	return nil
}

// `getLiquidityTiertoMaxAbsPremiumVotePpm` returns `maxAbsPremiumVotePpm` for each liquidity tier
// (used for clamping premium votes), sorted by increasing liquidity tier ID.
func (k Keeper) getLiquidityTiertoMaxAbsPremiumVotePpm(ctx sdk.Context) []*big.Int {
	premiumVoteClampFactorPpm := k.GetPremiumVoteClampFactorPpm(ctx)
	allLiquidityTiers := k.GetAllLiquidityTiers(ctx)
	var maxAbsPremiumVotePpms = make([]*big.Int, len(allLiquidityTiers))
	for i, liquidityTier := range allLiquidityTiers {
		maxAbsPremiumVotePpms[i] = liquidityTier.GetMaxAbsFundingClampPpm(premiumVoteClampFactorPpm)
	}
	return maxAbsPremiumVotePpms
}