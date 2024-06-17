package lib

import "github.com/pkg/errors"

// SafeUtxoView is a wrapper around a UtxoView that provides a safe way to connect transactions
// into a UtxoView without side effects when the connect fails.
type SafeUtxoView struct {
	primaryView *UtxoView
	backupView  *UtxoView
}

// NewSafeUtxoView create a new instance of a SafeUtxoView using the input UtxoView as a template.
// The input UtxoView should never get mutated and only copies of it are used internally.
func NewSafeUtxoView(utxoView *UtxoView) *SafeUtxoView {
	return &SafeUtxoView{
		primaryView: utxoView.CopyUtxoView(),
		backupView:  utxoView.CopyUtxoView(),
	}
}

// ConnectTransaction is a safe way to connect a transaction to a view:
//   - If the transaction successfully connects, then the view is updated, and the result of the connect is
//     returned. In the success case, this operation is O(1).
//   - If the transaction fails to connect, then the view is left unchanged and an error is returned.
//     In the failure case, this operation is O(N) where N is the number of entries in the view.
//
// The primary view is the view that is used to connect transactions. If a transaction fails to connect
// to the primary view, then the secondary view is used to restore the primary view.
func (safeUtxoView *SafeUtxoView) ConnectTransaction(
	txn *MsgDeSoTxn,
	txHash *BlockHash,
	blockHeight uint32,
	blockTimestampNanoSecs int64,
	verifySignatures bool,
	ignoreUtxos bool,
) (
	_utxoOps []*UtxoOperation,
	_totalInput uint64,
	_totalOutput uint64,
	_fees uint64,
	_err error,
) {
	// Connect the transaction to the primary view.
	utxoOpsForTxn, totalInput, totalOutput, fees, err := safeUtxoView.primaryView.ConnectTransaction(
		txn, txHash, blockHeight, blockTimestampNanoSecs, verifySignatures, ignoreUtxos,
	)

	// If the transaction failed to connect, then restore the primary view and return the error.
	if err != nil {
		safeUtxoView.primaryView = safeUtxoView.backupView.CopyUtxoView()
		return nil, 0, 0, 0, errors.Wrapf(err, "TryConnectTransaction: Problem connecting txn on copy view")
	}

	// Connect the transaction to the backup view.
	_, _, _, _, err = safeUtxoView.backupView.ConnectTransaction(
		txn, txHash, blockHeight, blockTimestampNanoSecs, verifySignatures, ignoreUtxos,
	)

	// If the transaction failed to connect to the backup view, then restore the backup view from
	// the primary view and swallow the error. This should never happen.
	if err != nil {
		safeUtxoView.backupView = safeUtxoView.primaryView.CopyUtxoView()
	}

	// Return the result from connecting the transaction to the primary view.
	return utxoOpsForTxn, totalInput, totalOutput, fees, nil
}

// GetUtxoView returns a copy of the primary view that is safe to be used and mutated by the caller.
func (safeUtxoView *SafeUtxoView) GetUtxoView() *UtxoView {
	return safeUtxoView.primaryView.CopyUtxoView()
}
