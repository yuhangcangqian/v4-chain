package process_test

import (
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/dydxprotocol/v4/app/process"
	"github.com/dydxprotocol/v4/testutil/constants"
	"github.com/dydxprotocol/v4/testutil/encoding"
	keepertest "github.com/dydxprotocol/v4/testutil/keeper"
	"github.com/stretchr/testify/require"
)

func TestDecodeProcessProposalTxs_Error(t *testing.T) {
	invalidTxBytes := []byte{1, 2, 3}

	// Valid operations tx.
	validOperationsTx := constants.ValidEmptyMsgProposedOperationsTxBytes

	// Valid add funding tx.
	validAddFundingTx := constants.ValidMsgAddPremiumVotesTxBytes

	// Valid update price tx.
	validUpdatePriceTx := constants.ValidMsgUpdateMarketPricesTxBytes

	// Valid "other" tx.
	validSendTx := constants.Msg_Send_TxBytes

	tests := map[string]struct {
		txsBytes    [][]byte
		expectedErr error
	}{
		"Less than min num txs": {
			txsBytes: [][]byte{validOperationsTx, validAddFundingTx}, // need at least 3.
			expectedErr: sdkerrors.Wrapf(
				process.ErrUnexpectedNumMsgs,
				"Expected the proposal to contain at least 3 txs, but got 2",
			),
		},
		"Order tx decoding fails": {
			txsBytes: [][]byte{invalidTxBytes, validAddFundingTx, validUpdatePriceTx},
			expectedErr: sdkerrors.Wrapf(
				process.ErrDecodingTxBytes,
				"invalid field number: tx parse error",
			),
		},
		"Add funding tx decoding fails": {
			txsBytes: [][]byte{validOperationsTx, invalidTxBytes, validUpdatePriceTx},
			expectedErr: sdkerrors.Wrapf(
				process.ErrDecodingTxBytes,
				"invalid field number: tx parse error",
			),
		},
		"Update prices tx decoding fails": {
			txsBytes: [][]byte{validOperationsTx, validAddFundingTx, invalidTxBytes},
			expectedErr: sdkerrors.Wrapf(
				process.ErrDecodingTxBytes,
				"invalid field number: tx parse error",
			),
		},
		"Other txs fails: invalid bytes": {
			txsBytes: [][]byte{
				validOperationsTx,
				validSendTx,    // other tx: valid.
				invalidTxBytes, // other tx: invalid.
				validAddFundingTx,
				validUpdatePriceTx,
			},
			expectedErr: sdkerrors.Wrapf(
				process.ErrDecodingTxBytes,
				"invalid field number: tx parse error",
			),
		},
		"Other txs fails: app-injected msg": {
			txsBytes: [][]byte{
				validOperationsTx,
				validSendTx,        // other tx: valid.
				validUpdatePriceTx, // other tx: invalid due to app-injected msg.
				validAddFundingTx,
				validUpdatePriceTx,
			},
			expectedErr: sdkerrors.Wrapf(
				process.ErrUnexpectedMsgType,
				"Invalid msg type or content in OtherTxs *types.MsgUpdateMarketPrices",
			),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Setup.
			ctx, pricesKeeper, _, _, _, _ := keepertest.PricesKeepers(t)

			// Run.
			_, err := process.DecodeProcessProposalTxs(
				ctx,
				constants.TestEncodingCfg.TxConfig.TxDecoder(),
				abci.RequestProcessProposal{Txs: tc.txsBytes},
				pricesKeeper,
			)

			// Validate.
			require.ErrorContains(t, err, tc.expectedErr.Error())
		})
	}
}

func TestDecodeProcessProposalTxs_Valid(t *testing.T) {
	// Valid order tx.
	validOperationsTx := constants.ValidEmptyMsgProposedOperationsTxBytes

	// Valid add funding tx.
	validAddFundingTx := constants.ValidMsgAddPremiumVotesTxBytes

	// Valid update price tx.
	validUpdatePriceTx := constants.ValidMsgUpdateMarketPricesTxBytes

	// Valid "other" tx.
	validSingleMsgOtherTx := constants.Msg_Send_TxBytes

	// Valid "other" multi msgs tx.
	validMultiMsgOtherTx := constants.Msg_SendAndTransfer_TxBytes

	tests := map[string]struct {
		txsBytes [][]byte

		expectedOtherTxsNum    int
		expectedOtherTxOneMsgs []sdk.Msg
		expectedOtherTxTwoMsgs []sdk.Msg
	}{
		"Valid: no other tx": {
			txsBytes: [][]byte{validOperationsTx, validAddFundingTx, validUpdatePriceTx},
		},
		"Valid: single other tx": {
			txsBytes: [][]byte{
				validOperationsTx,
				validSingleMsgOtherTx,
				validAddFundingTx,
				validUpdatePriceTx,
			},
			expectedOtherTxsNum:    1,
			expectedOtherTxOneMsgs: []sdk.Msg{constants.Msg_Send},
		},
		"Valid: mult other txs": {
			txsBytes: [][]byte{
				validOperationsTx,
				validSingleMsgOtherTx,
				validMultiMsgOtherTx,
				validAddFundingTx,
				validUpdatePriceTx,
			},
			expectedOtherTxsNum:    2,
			expectedOtherTxOneMsgs: []sdk.Msg{constants.Msg_Send},
			expectedOtherTxTwoMsgs: []sdk.Msg{constants.Msg_Send, constants.Msg_Transfer},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Setup.
			ctx, pricesKeeper, _, _, _, _ := keepertest.PricesKeepers(t)

			// Run.
			ppt, err := process.DecodeProcessProposalTxs(
				ctx,
				constants.TestEncodingCfg.TxConfig.TxDecoder(),
				abci.RequestProcessProposal{Txs: tc.txsBytes},
				pricesKeeper,
			)

			// Validate.
			require.NoError(t, err)
			require.NotNil(t, ppt)

			require.Equal(t, constants.ValidEmptyMsgProposedOperations, ppt.ProposedOperationsTx.GetMsg())
			require.Equal(t, constants.ValidMsgAddPremiumVotes, ppt.AddPremiumVotesTx.GetMsg())
			require.Equal(t, constants.ValidMsgUpdateMarketPrices, ppt.UpdateMarketPricesTx.GetMsg())

			require.Len(t, ppt.OtherTxs, tc.expectedOtherTxsNum)

			if tc.expectedOtherTxTwoMsgs != nil {
				require.Len(t, ppt.OtherTxs, 2)
				require.ElementsMatch(t, tc.expectedOtherTxOneMsgs, ppt.OtherTxs[0].GetMsgs())
				require.ElementsMatch(t, tc.expectedOtherTxTwoMsgs, ppt.OtherTxs[1].GetMsgs())
			} else if tc.expectedOtherTxOneMsgs != nil {
				require.Len(t, ppt.OtherTxs, 1)
				require.ElementsMatch(t, tc.expectedOtherTxOneMsgs, ppt.OtherTxs[0].GetMsgs())
			}
		})
	}
}

func TestProcessProposalTxs_Validate_Error(t *testing.T) {
	encodingCfg := encoding.GetTestEncodingCfg()
	txBuilder := encodingCfg.TxConfig.NewTxBuilder()

	// Operations tx.
	validOperationsTx := constants.ValidEmptyMsgProposedOperationsTxBytes

	// Add funding tx.
	validAddFundingTx := constants.ValidMsgAddPremiumVotesTxBytes
	invalidAddFundingTx := constants.InvalidMsgAddPremiumVotesTxBytes

	// Update price tx.
	validUpdatePriceTx := constants.ValidMsgUpdateMarketPricesTxBytes
	invalidUpdatePriceTx := constants.InvalidMsgUpdateMarketPricesStatelessTxBytes

	// "Other" tx.
	validSingleMsgOtherTx := constants.Msg_Send_TxBytes
	invalidSingleMsgOtherTx := constants.Msg_Send_Invalid_Zero_Amount_TxBytes
	_ = txBuilder.SetMsgs(constants.Msg_Send, constants.Msg_Send_Invalid_Zero_Amount)
	invalidMultiMsgOtherTx, _ := encodingCfg.TxConfig.TxEncoder()(txBuilder.GetTx())

	tests := map[string]struct {
		txsBytes    [][]byte
		expectedErr error
	}{
		"AddFunding tx validation fails": {
			txsBytes: [][]byte{validOperationsTx, invalidAddFundingTx, validUpdatePriceTx},
			expectedErr: sdkerrors.Wrap(
				process.ErrMsgValidateBasic,
				"premium votes must be sorted by perpetual id in ascending order and "+
					"cannot contain duplicates: MsgAddPremiumVotes is invalid"),
		},
		"UpdatePrices tx validation fails": {
			txsBytes: [][]byte{validOperationsTx, validAddFundingTx, invalidUpdatePriceTx},
			expectedErr: sdkerrors.Wrap(
				process.ErrMsgValidateBasic,
				"price cannot be 0 for market id (0): Market price update is invalid: stateless.",
			),
		},
		"Other txs validation fails: single tx": {
			txsBytes: [][]byte{
				validOperationsTx,
				validSingleMsgOtherTx,
				invalidSingleMsgOtherTx,
				validAddFundingTx,
				validUpdatePriceTx,
			},
			expectedErr: sdkerrors.Wrap(process.ErrMsgValidateBasic, "0foo: invalid coins"),
		},
		"Other txs validation fails: multi txs": {
			txsBytes: [][]byte{
				validOperationsTx,
				validSingleMsgOtherTx,
				invalidMultiMsgOtherTx,
				validAddFundingTx,
				validUpdatePriceTx,
			},
			expectedErr: sdkerrors.Wrap(process.ErrMsgValidateBasic, "0foo: invalid coins"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Setup.
			ctx, pricesKeeper, _, indexPriceCache, _, mockTimeProvider := keepertest.PricesKeepers(t)
			keepertest.CreateTestMarkets(t, ctx, pricesKeeper)
			indexPriceCache.UpdatePrices(constants.AtTimeTSingleExchangePriceUpdate)
			mockTimeProvider.On("Now").Return(constants.TimeT)

			ppt, err := process.DecodeProcessProposalTxs(
				ctx,
				encodingCfg.TxConfig.TxDecoder(),
				abci.RequestProcessProposal{Txs: tc.txsBytes},
				pricesKeeper,
			)
			require.NoError(t, err)

			// Run.
			err = ppt.Validate()

			// Validate.
			require.ErrorContains(t, err, tc.expectedErr.Error())
		})
	}
}

func TestProcessProposalTxs_Validate_Valid(t *testing.T) {
	// Valid order tx.
	validOperationsTx := constants.ValidEmptyMsgProposedOperationsTxBytes

	// Valid add funding tx.
	validAddFundingTx := constants.ValidMsgAddPremiumVotesTxBytes

	// Valid update price tx.
	validUpdatePriceTx := constants.ValidMsgUpdateMarketPricesTxBytes

	// Valid "other" tx.
	validSingleMsgOtherTx := constants.Msg_Send_TxBytes

	// Valid "other" multi msgs tx.
	validMultiMsgOtherTx := constants.Msg_SendAndTransfer_TxBytes

	tests := map[string]struct {
		txsBytes [][]byte
	}{
		"No other txs": {
			txsBytes: [][]byte{validOperationsTx, validAddFundingTx, validUpdatePriceTx},
		},
		"Single other txs": {
			txsBytes: [][]byte{
				validOperationsTx,
				validSingleMsgOtherTx,
				validAddFundingTx,
				validUpdatePriceTx,
			},
		},
		"Multi other txs": {
			txsBytes: [][]byte{
				validOperationsTx,
				validSingleMsgOtherTx,
				validMultiMsgOtherTx,
				validAddFundingTx,
				validUpdatePriceTx,
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Setup.
			ctx, pricesKeeper, _, indexPriceCache, _, mockTimeProvider := keepertest.PricesKeepers(t)
			keepertest.CreateTestMarkets(t, ctx, pricesKeeper)
			indexPriceCache.UpdatePrices(constants.AtTimeTSingleExchangePriceUpdate)
			mockTimeProvider.On("Now").Return(constants.TimeT)

			ppt, err := process.DecodeProcessProposalTxs(
				ctx,
				constants.TestEncodingCfg.TxConfig.TxDecoder(),
				abci.RequestProcessProposal{Txs: tc.txsBytes},
				pricesKeeper,
			)
			require.NoError(t, err)

			// Run.
			err = ppt.Validate()

			// Validate.
			require.NoError(t, err)
		})
	}
}