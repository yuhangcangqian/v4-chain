package keeper

import (
	"github.com/dydxprotocol/v4/x/clob/types"
)

type msgServer struct {
	Keeper types.ClobKeeper
}

// NewMsgServerImpl returns an implementation of the MsgServer interface
// for the provided Keeper.
func NewMsgServerImpl(keeper types.ClobKeeper) types.MsgServer {
	return &msgServer{Keeper: keeper}
}

var _ types.MsgServer = msgServer{}