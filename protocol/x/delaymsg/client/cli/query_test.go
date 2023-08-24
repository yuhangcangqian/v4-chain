//go:build all || integration_test

package cli_test

import (
	"github.com/cosmos/cosmos-sdk/client"
	clitestutil "github.com/cosmos/cosmos-sdk/testutil/cli"
	"github.com/dydxprotocol/v4-chain/protocol/testutil/constants"
	"github.com/dydxprotocol/v4-chain/protocol/testutil/network"
	"github.com/dydxprotocol/v4-chain/protocol/x/delaymsg/client/cli"
	"github.com/dydxprotocol/v4-chain/protocol/x/delaymsg/types"
	"github.com/stretchr/testify/require"
	"strconv"
	"testing"
)

const (
	GrpcNotFoundError = "NotFound"
)

// Prevent strconv unused error
var _ = strconv.IntSize

func setupNetwork(
	t *testing.T,
	state *types.GenesisState,
) (
	*network.Network,
	client.Context,
) {
	t.Helper()
	cfg := network.DefaultConfig(nil)

	// Init state.
	// Validate global genesis state contains a delaymsg genesis state.
	configDefaultGenesisState := types.GenesisState{}
	require.NoError(t, cfg.Codec.UnmarshalJSON(cfg.GenesisState[types.ModuleName], &configDefaultGenesisState))

	// Update global genesis state with specified delaymsg genesis state.
	buf, err := cfg.Codec.MarshalJSON(state)
	require.NoError(t, err)
	cfg.GenesisState[types.ModuleName] = buf

	// Create network.
	net := network.New(t, cfg)
	ctx := net.Validators[0].ClientCtx
	return net, ctx
}

func TestQueryNumMessages(t *testing.T) {
	tests := map[string]struct {
		state *types.GenesisState
	}{
		"Default: 0": {
			state: types.DefaultGenesis(),
		},
		"Non-zero": {
			state: &types.GenesisState{
				NumMessages:     20,
				DelayedMessages: []*types.DelayedMessage{},
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, ctx := setupNetwork(t, tc.state)
			out, err := clitestutil.ExecTestCLICmd(ctx, cli.CmdQueryNumMessages(), []string{})
			require.NoError(t, err)
			var resp types.QueryNumMessagesResponse
			require.NoError(t, ctx.Codec.UnmarshalJSON(out.Bytes(), &resp))
			require.Equal(t, tc.state.NumMessages, resp.NumMessages)
		})
	}
}

func TestQueryMessage(t *testing.T) {
	tests := map[string]struct {
		state       *types.GenesisState
		expectedMsg *types.DelayedMessage
	}{
		"Default: 0": {
			state: types.DefaultGenesis(),
		},
		"Non-zero": {
			state: &types.GenesisState{
				NumMessages: 20,
				DelayedMessages: []*types.DelayedMessage{
					{
						Id:  0,
						Msg: constants.Msg1Bytes,
					},
				},
			},
			expectedMsg: &types.DelayedMessage{
				Id:  0,
				Msg: constants.Msg1Bytes,
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, ctx := setupNetwork(t, tc.state)
			out, err := clitestutil.ExecTestCLICmd(ctx, cli.CmdQueryMessage(), []string{"0"})
			if tc.expectedMsg == nil {
				require.ErrorContains(t, err, GrpcNotFoundError)
			} else {
				require.NoError(t, err)
				var resp types.QueryMessageResponse
				require.NoError(t, ctx.Codec.UnmarshalJSON(out.Bytes(), &resp))
				require.Equal(t, tc.expectedMsg, resp.Message)
			}
		})
	}
}

func TestQueryBlockMessageIds(t *testing.T) {
	tests := map[string]struct {
		state                   *types.GenesisState
		expectedBlockMessageIds []uint32
	}{
		"Default: 0": {
			state: types.DefaultGenesis(),
		},
		"Non-zero": {
			state: &types.GenesisState{
				NumMessages: 20,
				DelayedMessages: []*types.DelayedMessage{
					{
						Id:          0,
						Msg:         constants.Msg1Bytes,
						BlockHeight: 10,
					},
				},
			},
			expectedBlockMessageIds: []uint32{0},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, ctx := setupNetwork(t, tc.state)
			out, err := clitestutil.ExecTestCLICmd(ctx, cli.CmdQueryBlockMessageIds(), []string{"10"})
			if tc.expectedBlockMessageIds == nil {
				require.ErrorContains(t, err, GrpcNotFoundError)
			} else {
				require.NoError(t, err)
				var resp types.QueryBlockMessageIdsResponse
				require.NoError(t, ctx.Codec.UnmarshalJSON(out.Bytes(), &resp))
				require.Equal(t, tc.expectedBlockMessageIds, resp.MessageIds)
			}
		})
	}
}