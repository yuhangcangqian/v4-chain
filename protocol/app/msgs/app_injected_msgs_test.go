package msgs_test

import (
	"strings"
	"testing"

	"github.com/cosmos/gogoproto/proto"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"

	"github.com/dydxprotocol/v4/app/msgs"
	"github.com/dydxprotocol/v4/lib/maps"
	"github.com/dydxprotocol/v4/testutil/encoding"
	testmsgs "github.com/dydxprotocol/v4/testutil/msgs"
	"github.com/stretchr/testify/require"
)

func TestAppInjectedMsgSamples_Key(t *testing.T) {
	expectedMsgs := []string{
		// blocktime
		"/dydxprotocol.blocktime.MsgIsDelayedBlock",
		"/dydxprotocol.blocktime.MsgIsDelayedBlockResponse",

		// bridge
		"/dydxprotocol.bridge.MsgAcknowledgeBridge",
		"/dydxprotocol.bridge.MsgAcknowledgeBridgeResponse",

		// clob
		"/dydxprotocol.clob.MsgProposedOperations",
		"/dydxprotocol.clob.MsgProposedOperationsResponse",

		// perpetuals
		"/dydxprotocol.perpetuals.MsgAddPremiumVotes",
		"/dydxprotocol.perpetuals.MsgAddPremiumVotesResponse",

		// prices
		"/dydxprotocol.prices.MsgUpdateMarketPrices",
		"/dydxprotocol.prices.MsgUpdateMarketPricesResponse",
	}

	require.Equal(t, expectedMsgs, maps.GetSortedKeys(msgs.AppInjectedMsgSamples))
}

func TestAppInjectedMsgSamples_Value(t *testing.T) {
	validateSampleMsgValue(t, msgs.AppInjectedMsgSamples)
}

// validateSampleMsgValue ensures that the sample message is
//  1. not nil for "<module>.<version>.Msg<Name>"
//  2. sample msg's proto msg name matches the key it's registered under
//  3. nil sample message for others
func validateSampleMsgValue(
	t *testing.T,
	sampleMsgs map[string]sdk.Msg,
) {
	for key, sample := range sampleMsgs {
		keyTokens := strings.Split(key, ".")
		if testmsgs.IsValidMsgFormat(keyTokens) && !strings.HasSuffix(key, "Response") {
			// Sample msg cannot be nil.
			require.NotNil(t, sample, "key: %s", key)

			// Sample msg type must match the key it's registered under.
			expectedTypeUrl := "/" + proto.MessageName(sample)
			require.Equal(t, expectedTypeUrl, key)
		} else {
			// "Response" messages are msgs that cannot be submitted, so no sample is provided.
			// Additionally, all other intermediary msgs should not be submitted as a top-level msg.
			require.Nil(t, sample)
		}
	}
}

func TestAppInjectedMsgSamples_GetSigners(t *testing.T) {
	testEncodingCfg := encoding.GetTestEncodingCfg()
	testTxBuilder := testEncodingCfg.TxConfig.NewTxBuilder()

	for _, sample := range testmsgs.GetNonNilSampleMsgs(msgs.AppInjectedMsgSamples) {
		_ = testTxBuilder.SetMsgs(sample.Msg)
		sigTx, ok := testTxBuilder.GetTx().(authsigning.SigVerifiableTx)
		require.True(t, ok)
		require.Empty(t, sigTx.GetSigners())
	}
}