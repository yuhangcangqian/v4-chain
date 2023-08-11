package perpetuals_test

import (
	"errors"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/dydxprotocol/v4/mocks"
	keepertest "github.com/dydxprotocol/v4/testutil/keeper"
	"github.com/dydxprotocol/v4/x/perpetuals"
	"github.com/stretchr/testify/require"
)

func TestEndBlocker(t *testing.T) {
	testError := errors.New("error")

	tests := map[string]struct {
		setupMocks  func(ctx sdk.Context, mck *mocks.PerpetualsKeeper)
		expectedErr error
	}{
		"Success": {
			setupMocks: func(ctx sdk.Context, mck *mocks.PerpetualsKeeper) {
				mck.On(
					"MaybeProcessNewFundingTickEpoch",
					ctx,
				).Return(nil)
				mck.On(
					"MaybeProcessNewFundingSampleEpoch",
					ctx,
				).Return(nil)
			},
			expectedErr: nil,
		},
		"MaybeProcessNewFundingTickEpoch Error": {
			setupMocks: func(ctx sdk.Context, mck *mocks.PerpetualsKeeper) {
				mck.On(
					"MaybeProcessNewFundingSampleEpoch",
					ctx,
				).Return(nil)
				mck.On(
					"MaybeProcessNewFundingTickEpoch",
					ctx,
				).Panic(testError.Error())
			},
			expectedErr: testError,
		},
		"MaybeProcessNewFundingSampleEpoch Error": {
			setupMocks: func(ctx sdk.Context, mck *mocks.PerpetualsKeeper) {
				mck.On(
					"MaybeProcessNewFundingSampleEpoch",
					ctx,
				).Panic(testError.Error())
				mck.On(
					"MaybeProcessNewFundingTickEpoch",
					ctx,
				).Return(nil)
			},
			expectedErr: testError,
		},
	}

	// Run tests.
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Initialize Mocks and Context.
			mockKeeper := &mocks.PerpetualsKeeper{}
			ctx, _, _, _, _ := keepertest.PerpetualsKeepers(t)

			// Setup mocks.
			tc.setupMocks(ctx, mockKeeper)

			if tc.expectedErr != nil {
				// Call EndBlocker.
				require.PanicsWithValue(t, tc.expectedErr.Error(), func() {
					//nolint:errcheck
					perpetuals.EndBlocker(ctx, mockKeeper)
				})
			} else {
				perpetuals.EndBlocker(ctx, mockKeeper)

				// Assert mock expectations if no error was expected.
				result := mockKeeper.AssertExpectations(t)
				require.True(t, result)
			}
		})
	}
}