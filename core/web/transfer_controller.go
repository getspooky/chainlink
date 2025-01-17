package web

import (
	"math/big"
	"net/http"

	"github.com/ethereum/go-ethereum/common"
	"github.com/pkg/errors"

	"github.com/smartcontractkit/chainlink/core/assets"
	"github.com/smartcontractkit/chainlink/core/chains/evm"
	"github.com/smartcontractkit/chainlink/core/services/chainlink"
	"github.com/smartcontractkit/chainlink/core/store/models"
	"github.com/smartcontractkit/chainlink/core/utils"
	"github.com/smartcontractkit/chainlink/core/web/presenters"

	"github.com/gin-gonic/gin"
)

// TransfersController can send LINK tokens to another address
type TransfersController struct {
	App chainlink.Application
}

// Create sends ETH from the Chainlink's account to a specified address.
//
// Example: "<application>/withdrawals"
func (tc *TransfersController) Create(c *gin.Context) {
	var tr models.SendEtherRequest
	if err := c.ShouldBindJSON(&tr); err != nil {
		jsonAPIError(c, http.StatusBadRequest, err)
		return
	}

	chain, err := getChain(tc.App.GetChainSet(), tr.EVMChainID.String())
	switch err {
	case ErrInvalidChainID, ErrMultipleChains, ErrMissingChainID:
		jsonAPIError(c, http.StatusUnprocessableEntity, err)
		return
	case nil:
		break
	default:
		jsonAPIError(c, http.StatusInternalServerError, err)
		return
	}

	if tr.FromAddress == utils.ZeroAddress {
		jsonAPIError(c, http.StatusUnprocessableEntity, errors.Errorf("withdrawal source address is missing: %v", tr.FromAddress))
		return
	}

	if !tr.AllowHigherAmounts {
		err = ValidateEthBalanceForTransfer(c, chain, tr.FromAddress, tr.Amount)
		if err != nil {
			jsonAPIError(c, http.StatusUnprocessableEntity, errors.Errorf("transaction failed: %v", err))
			return
		}
	}

	etx, err := chain.TxManager().SendEther(chain.ID(), tr.FromAddress, tr.DestinationAddress, tr.Amount, chain.Config().EvmGasLimitTransfer())
	if err != nil {
		jsonAPIError(c, http.StatusBadRequest, errors.Errorf("transaction failed: %v", err))
		return
	}

	jsonAPIResponse(c, presenters.NewEthTxResource(etx), "eth_tx")
}

// ValidateEthBalanceForTransfer validates that the current balance can cover the transaction amount
func ValidateEthBalanceForTransfer(c *gin.Context, chain evm.Chain, fromAddr common.Address, amount assets.Eth) error {
	var err error
	var balance *big.Int

	balanceMonitor := chain.BalanceMonitor()

	if balanceMonitor != nil {
		balance = balanceMonitor.GetEthBalance(fromAddr).ToInt()
	} else {
		balance, err = chain.Client().BalanceAt(c, fromAddr, nil)
		if err != nil {
			return err
		}
	}

	zero := big.NewInt(0)

	if balance == nil || balance.Cmp(zero) == 0 {
		return errors.Errorf("balance is too low for this transaction to be executed: %v", balance)
	}

	var gasPrice *big.Int

	gasLimit := chain.Config().EvmGasLimitTransfer()
	estimator := chain.TxManager().GetGasEstimator()
	gasPrice, gasLimit, err = estimator.GetLegacyGas(nil, gasLimit)
	if err != nil {
		return errors.Wrap(err, "failed to estimate gas")
	}

	// Creating a `Big` struct to avoid having a mutation on `tr.Amount` and hence affecting the value stored in the DB
	amountAsBig := utils.NewBig(amount.ToInt())
	fee := new(big.Int).Mul(gasPrice, big.NewInt(int64(gasLimit)))
	amountWithFees := new(big.Int).Add(amountAsBig.ToInt(), fee)
	if balance.Cmp(amountWithFees) < 0 {
		// ETH balance is less than the sent amount + fees
		return errors.Errorf("balance is too low for this transaction to be executed: %v", balance)
	}

	return nil
}
