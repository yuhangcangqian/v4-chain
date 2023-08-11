package types

import sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

// ValidateFromParam checks that the MarketPrice is valid and that it corresponds to the given MarketParam.
func (mp *MarketPrice) ValidateFromParam(marketParam MarketParam) error {
	if marketParam.Id != mp.Id {
		return sdkerrors.Wrapf(
			ErrInvalidInput,
			"market param id %d does not match market price id %d",
			marketParam.Id,
			mp.Id,
		)
	}
	if marketParam.Exponent != mp.Exponent {
		return sdkerrors.Wrapf(
			ErrInvalidInput,
			"market param %d exponent %d does not match market price %d exponent %d",
			marketParam.Id,
			marketParam.Exponent,
			mp.Id,
			mp.Exponent,
		)
	}
	if mp.Price == 0 {
		return sdkerrors.Wrapf(
			ErrInvalidInput,
			"market %d price cannot be zero",
			mp.Id,
		)
	}
	return nil
}