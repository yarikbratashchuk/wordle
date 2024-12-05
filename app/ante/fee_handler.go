package ante

import (
	"bytes"
	"fmt"
	"math"

	"cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	txsigning "cosmossdk.io/x/tx/signing"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/x/auth/ante"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/tendermint/go-amino"
)

// FeeCalculator calculates fees based on tx size
type FeeCalculator struct {
	// BaseRate is the base fee rate per byte (e.g., 1uatom per byte)
	BaseRate sdkmath.Int
	// ThresholdSize is the size in bytes above which higher rates apply
	ThresholdSize int64
	// LargeTransactionMultiplier is the multiplier for transactions above threshold
	LargeTransactionMultiplier sdkmath.Int
}

// NewFeeCalculator creates a new FeeCalculator
func NewFeeCalculator(baseRate sdkmath.Int, thresholdSize int64, multiplier sdkmath.Int) FeeCalculator {
	return FeeCalculator{
		BaseRate:                   baseRate,
		ThresholdSize:              thresholdSize,
		LargeTransactionMultiplier: multiplier,
	}
}

// calculateRequiredFee calculates the required fee based on transaction size
func (fc FeeCalculator) calculateRequiredFee(txSize int64) sdk.Coins {
	var fee sdkmath.Int

	if txSize <= fc.ThresholdSize {
		fee = fc.BaseRate.Mul(sdkmath.NewInt(txSize))
	} else {
		baseFee := fc.BaseRate.Mul(sdkmath.NewInt(fc.ThresholdSize))
		extraSize := txSize - fc.ThresholdSize
		extraFee := fc.BaseRate.
			Mul(fc.LargeTransactionMultiplier).
			Mul(sdkmath.NewInt(extraSize))
		fee = baseFee.Add(extraFee)
	}

	return sdk.NewCoins(sdk.NewCoin(sdk.DefaultBondDenom, fee))
}

// SizeBasedDeductFeeDecorator combines size-based fee calculation with fee deduction
type SizeBasedDeductFeeDecorator struct {
	calculator FeeCalculator

	accountKeeper  ante.AccountKeeper
	bankKeeper     authtypes.BankKeeper
	feegrantKeeper ante.FeegrantKeeper
}

// NewSizeBasedDeductFeeDecorator creates a new SizeBasedDeductFeeDecorator
func NewSizeBasedDeductFeeDecorator(
	calculator FeeCalculator,
	ak ante.AccountKeeper,
	bk authtypes.BankKeeper,
	fk ante.FeegrantKeeper,
) SizeBasedDeductFeeDecorator {
	return SizeBasedDeductFeeDecorator{
		calculator: calculator,

		accountKeeper:  ak,
		bankKeeper:     bk,
		feegrantKeeper: fk,
	}
}

// AnteHandle implements the Ante decorator interface
func (sd SizeBasedDeductFeeDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	feeTx, ok := tx.(sdk.FeeTx)
	if !ok {
		return ctx, errors.Wrap(sdkerrors.ErrTxDecode, "Tx must be a FeeTx")
	}

	if !simulate && ctx.BlockHeight() > 0 && feeTx.GetGas() == 0 {
		return ctx, errors.Wrap(sdkerrors.ErrInvalidGasLimit, "must provide positive gas")
	}

	// Calculate required fee based on transaction size
	txBytes := amino.MustMarshalBinaryBare(tx)
	txSize := int64(len(txBytes))
	requiredFee := sd.calculator.calculateRequiredFee(txSize)

	// Get the fee the transaction is actually paying
	providedFee := feeTx.GetFee()

	// Check if provided fee is sufficient
	if providedFee.IsAllLT(requiredFee) {
		return ctx, errors.Wrapf(sdkerrors.ErrInsufficientFee,
			"insufficient fee; got: %s required: %s", providedFee, requiredFee)
	}

	// Handle fee deduction
	if err := sd.deductFees(ctx, tx, requiredFee); err != nil {
		return ctx, err
	}

	priority := getTxPriority(requiredFee, int64(feeTx.GetGas()))
	newCtx := ctx.WithPriority(priority)

	return next(newCtx, tx, simulate)
}

// deductFees handles the fee deduction logic
func (sd SizeBasedDeductFeeDecorator) deductFees(ctx sdk.Context, sdkTx sdk.Tx, fee sdk.Coins) error {
	feeTx, ok := sdkTx.(sdk.FeeTx)
	if !ok {
		return errors.Wrap(sdkerrors.ErrTxDecode, "Tx must be a FeeTx")
	}

	if addr := sd.accountKeeper.GetModuleAddress(authtypes.FeeCollectorName); addr == nil {
		return fmt.Errorf("fee collector module account (%s) has not been set", authtypes.FeeCollectorName)
	}

	feePayer := feeTx.FeePayer()
	feeGranter := feeTx.FeeGranter()
	deductFeesFrom := feePayer

	// Handle fee granting if enabled
	if feeGranter != nil {
		feeGranterAddr := sdk.AccAddress(feeGranter)

		if sd.feegrantKeeper == nil {
			return sdkerrors.ErrInvalidRequest.Wrap("fee grants are not enabled")
		} else if !bytes.Equal(feeGranterAddr, feePayer) {
			err := sd.feegrantKeeper.UseGrantedFees(ctx, feeGranterAddr, feePayer, fee, sdkTx.GetMsgs())
			if err != nil {
				return errors.Wrapf(err, "%s does not allow to pay fees for %s", feeGranter, feePayer)
			}
		}

		deductFeesFrom = feeGranterAddr
	}

	deductFeesFromAcc := sd.accountKeeper.GetAccount(ctx, deductFeesFrom)
	if deductFeesFromAcc == nil {
		return sdkerrors.ErrUnknownAddress.Wrapf("fee payer address: %s does not exist", deductFeesFrom)
	}

	// Deduct the fees
	if !fee.IsZero() {
		err := DeductFees(sd.bankKeeper, ctx, deductFeesFromAcc, fee)
		if err != nil {
			return err
		}
	}

	events := sdk.Events{
		sdk.NewEvent(
			sdk.EventTypeTx,
			sdk.NewAttribute(sdk.AttributeKeyFee, fee.String()),
			sdk.NewAttribute(sdk.AttributeKeyFeePayer, sdk.AccAddress(deductFeesFrom).String()),
		),
	}
	ctx.EventManager().EmitEvents(events)

	return nil
}

// HandlerOptions are the options required for constructing an AnteHandler.
type HandlerOptions struct {
	AccountKeeper   ante.AccountKeeper
	BankKeeper      authtypes.BankKeeper
	SignModeHandler *txsigning.HandlerMap
	FeegrantKeeper  ante.FeegrantKeeper
	SigGasConsumer  ante.SignatureVerificationGasConsumer
}

// NewAnteHandler returns an AnteHandler that checks and increments sequence
// numbers, checks signatures & account numbers, and deducts fees from the first
// signer.
func NewAnteHandler(options HandlerOptions) (sdk.AnteHandler, error) {
	if options.AccountKeeper == nil {
		return nil, errors.Wrap(sdkerrors.ErrLogic, "account keeper is required for ante handler")
	}
	if options.BankKeeper == nil {
		return nil, errors.Wrap(sdkerrors.ErrLogic, "bank keeper is required for ante handler")
	}
	if options.SignModeHandler == nil {
		return nil, errors.Wrap(sdkerrors.ErrLogic, "sign mode handler is required for ante handler")
	}
	if options.FeegrantKeeper == nil {
		return nil, errors.Wrap(sdkerrors.ErrLogic, "feegrant keeper is required for ante handler")
	}

	return sdk.ChainAnteDecorators(
		ante.NewSetUpContextDecorator(),
		ante.NewValidateBasicDecorator(),
		ante.NewValidateMemoDecorator(options.AccountKeeper),
		ante.NewConsumeGasForTxSizeDecorator(options.AccountKeeper),
		NewSizeBasedDeductFeeDecorator(
			NewFeeCalculator(sdkmath.NewInt(1000), 1000, sdkmath.NewInt(2)),
			options.AccountKeeper,
			options.BankKeeper,
			options.FeegrantKeeper,
		),
		ante.NewSetPubKeyDecorator(options.AccountKeeper),
		ante.NewValidateSigCountDecorator(options.AccountKeeper),
		ante.NewSigGasConsumeDecorator(options.AccountKeeper, options.SigGasConsumer),
		ante.NewSigVerificationDecorator(options.AccountKeeper, options.SignModeHandler),
		ante.NewIncrementSequenceDecorator(options.AccountKeeper),
	), nil
}

// DeductFees deducts fees from the given account.
func DeductFees(bankKeeper authtypes.BankKeeper, ctx sdk.Context, acc sdk.AccountI, fees sdk.Coins) error {
	if !fees.IsValid() {
		return errors.Wrapf(sdkerrors.ErrInsufficientFee, "invalid fee amount: %s", fees)
	}

	err := bankKeeper.SendCoinsFromAccountToModule(ctx, acc.GetAddress(), authtypes.FeeCollectorName, fees)
	if err != nil {
		return errors.Wrapf(sdkerrors.ErrInsufficientFunds, err.Error())
	}

	return nil
}

// getTxPriority returns a naive tx priority based on the amount of the smallest denomination of the gas price
// provided in a transaction.
// NOTE: This implementation should be used with a great consideration as it opens potential attack vectors
// where txs with multiple coins could not be prioritize as expected.
func getTxPriority(fee sdk.Coins, gas int64) int64 {
	var priority int64
	for _, c := range fee {
		p := int64(math.MaxInt64)
		gasPrice := c.Amount.QuoRaw(gas)
		if gasPrice.IsInt64() {
			p = gasPrice.Int64()
		}
		if priority == 0 || p < priority {
			priority = p
		}
	}

	return priority
}
