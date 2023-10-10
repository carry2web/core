package lib

import (
	"github.com/dgraph-io/badger/v3"
	"github.com/golang/glog"
	"github.com/pkg/errors"
	"path/filepath"
	"strings"
	"sync"
)

type PosMempoolStatus int

const (
	PosMempoolStatusRunning PosMempoolStatus = iota
	PosMempoolStatusNotRunning
)

type Mempool interface {
	Start() error
	Stop()
	IsRunning() bool
	AddTransaction(txn *MsgDeSoTxn, verifySignature bool) error
	RemoveTransaction(txnHash *BlockHash) error
	GetTransaction(txnHash *BlockHash) *MsgDeSoTxn
	GetTransactions() []*MsgDeSoTxn
	GetIterator() MempoolIterator
	Refresh() error
	UpdateLatestBlock(blockView *UtxoView, blockHeight uint64)
	UpdateGlobalParams(globalParams *GlobalParamsEntry)
}

type MempoolIterator interface {
	Next() bool
	Value() (*MsgDeSoTxn, bool)
	Initialized() bool
}

// PosMempool is used by the node to keep track of uncommitted transactions. The main responsibilities of the PosMempool
// include addition/removal of transactions, back up of transaction to database, and retrieval of transactions ordered
// by Fee-Time algorithm. More on the Fee-Time algorithm can be found in the documentation of TransactionRegister.
type PosMempool struct {
	sync.RWMutex
	status PosMempoolStatus
	// params of the blockchain
	params *DeSoParams
	// globalParams are used to track the latest GlobalParamsEntry. In case the GlobalParamsEntry changes, the PosMempool
	// is equipped with UpdateGlobalParams method to handle upgrading GlobalParamsEntry.
	globalParams *GlobalParamsEntry
	// inMemoryOnly is a setup flag that determines whether the mempool should be backed up to db or not. If set to true,
	// the mempool will not open a db nor instantiate the persister.
	inMemoryOnly bool
	// dir of the directory where the database should be stored.
	dir string
	// db is the database that the mempool will use to persist transactions.
	db *badger.DB

	// txnRegister is the in-memory data structure keeping track of the transactions in the mempool. The TransactionRegister
	// is responsible for ordering transactions by the Fee-Time algorithm.
	txnRegister *TransactionRegister
	// persister is responsible for interfacing with the database. The persister backs up mempool transactions so not to
	// lose them when node reboots. The persister also retrieves transactions from the database when the node starts up.
	// The persister runs on its dedicated thread and events are used to notify the persister thread whenever
	// transactions are added/removed from the mempool. The persister thread then updates the database accordingly.
	persister *MempoolPersister
	// ledger is a simple data structure that keeps track of cumulative transaction fees in the mempool.
	// The ledger keeps track of how much each user would have spent in fees across all their transactions in the mempool.
	ledger *BalanceLedger
	// nonceTracker is responsible for keeping track of a (public key, nonce) -> Txn index. The index is useful in
	// facilitating a "replace by higher fee" feature. This feature gives users the ability to replace their existing
	// mempool transaction with a new transaction having the same nonce but higher fee.
	nonceTracker *NonceTracker

	// latestBlockView is used to check if a transaction is valid before being added to the mempool. The latestBlockView
	// checks if the transaction has a valid signature and if the transaction's sender has enough funds to cover the fee.
	// The latestBlockView should be updated whenever a new block is added to the blockchain via UpdateLatestBlock.
	// PosMempool only needs read-access to the block view. It isn't necessary to copy the block view before passing it
	// to the mempool.
	latestBlockView *UtxoView
	// latestBlockNode is used to infer the latest block height. The latestBlockNode should be updated whenever a new
	// block is added to the blockchain via UpdateLatestBlock.
	latestBlockHeight uint64
}

// PosMempoolIterator is a wrapper around FeeTimeIterator, modified to return MsgDeSoTxn instead of MempoolTx.
type PosMempoolIterator struct {
	it *FeeTimeIterator
}

func (it *PosMempoolIterator) Next() bool {
	return it.it.Next()
}

func (it *PosMempoolIterator) Value() (*MsgDeSoTxn, bool) {
	txn, ok := it.it.Value()
	if txn == nil || txn.Tx == nil {
		return nil, ok
	}
	return txn.Tx, ok
}

func (it *PosMempoolIterator) Initialized() bool {
	return it.it.Initialized()
}

func NewPosMempoolIterator(it *FeeTimeIterator) *PosMempoolIterator {
	return &PosMempoolIterator{it: it}
}

func NewPosMempool(params *DeSoParams, globalParams *GlobalParamsEntry, latestBlockView *UtxoView,
	latestBlockHeight uint64, dir string, inMemoryOnly bool) *PosMempool {
	return &PosMempool{
		status:            PosMempoolStatusNotRunning,
		params:            params,
		globalParams:      globalParams,
		inMemoryOnly:      inMemoryOnly,
		dir:               dir,
		latestBlockView:   latestBlockView,
		latestBlockHeight: latestBlockHeight,
	}
}

func (mp *PosMempool) Start() error {
	mp.Lock()
	defer mp.Unlock()

	if mp.IsRunning() {
		return nil
	}

	// Setup the database.
	if !mp.inMemoryOnly {
		mempoolDirectory := filepath.Join(mp.dir, "mempool")
		opts := DefaultBadgerOptions(mempoolDirectory)
		db, err := badger.Open(opts)
		if err != nil {
			return errors.Wrapf(err, "PosMempool.Start: Problem setting up database")
		}
		mp.db = db
	}

	// Create the transaction register, the ledger, and the nonce tracker,
	mp.txnRegister = NewTransactionRegister(mp.globalParams)
	mp.ledger = NewBalanceLedger()
	mp.nonceTracker = NewNonceTracker()

	// Create the persister
	if !mp.inMemoryOnly {
		mp.persister = NewMempoolPersister(mp.db, int(mp.params.MempoolBackupTimeMilliseconds))

		// Start the persister and retrieve transactions from the database.
		mp.persister.Start()
		err := mp.loadPersistedTransactions()
		if err != nil {
			return errors.Wrapf(err, "PosMempool.Start: Problem loading persisted transactions")
		}
	}

	mp.status = PosMempoolStatusRunning
	return nil
}

func (mp *PosMempool) Stop() {
	mp.Lock()
	defer mp.Unlock()

	if !mp.IsRunning() {
		return
	}

	// Close the persister and stop the database.
	if !mp.inMemoryOnly {
		if err := mp.persister.Stop(); err != nil {
			glog.Errorf("PosMempool.Stop: Problem stopping persister: %v", err)
		}
		if err := mp.db.Close(); err != nil {
			glog.Errorf("PosMempool.Stop: Problem closing database: %v", err)
		}
	}

	// Reset the transaction register, the ledger, and the nonce tracker.
	mp.txnRegister.Reset()
	mp.ledger.Reset()
	mp.nonceTracker.Reset()

	mp.status = PosMempoolStatusNotRunning
}

func (mp *PosMempool) IsRunning() bool {
	return mp.status == PosMempoolStatusRunning
}

// AddTransaction validates a MsgDeSoTxn transaction and adds it to the mempool if it is valid.
// If the mempool overflows as a result of adding the transaction, the mempool is pruned. The
// transaction signature verification can be skipped if verifySignature is passed as true.
func (mp *PosMempool) AddTransaction(txn *MsgDeSoTxn, verifySignature bool) error {
	// First, validate that the transaction is properly formatted according to BalanceModel. We acquire a read lock on
	// the mempool. This allows multiple goroutines to safely perform transaction validation concurrently. In particular,
	// transaction signature verification can be parallelized.
	if err := mp.validateTransaction(txn, verifySignature); err != nil {
		return errors.Wrapf(err, "PosMempool.AddTransaction: Problem verifying transaction")
	}

	// If we get this far, it means that the transaction is valid. We can now add it to the mempool.
	// We lock the mempool to ensure that no other thread is modifying it while we add the transaction.
	mp.Lock()
	defer mp.Unlock()

	if !mp.IsRunning() {
		return errors.Wrapf(MempoolErrorNotRunning, "PosMempool.AddTransaction: ")
	}

	// Construct the MempoolTx from the MsgDeSoTxn.
	mempoolTx, err := NewMempoolTx(txn, mp.latestBlockHeight)
	if err != nil {
		return errors.Wrapf(err, "PosMempool.AddTransaction: Problem constructing MempoolTx")
	}

	// Add the transaction to the mempool and then prune if needed.
	if err := mp.addTransactionNoLock(mempoolTx, true); err != nil {
		return errors.Wrapf(err, "PosMempool.AddTransaction: Problem adding transaction to mempool")
	}

	if err := mp.pruneNoLock(); err != nil {
		glog.Errorf("PosMempool.AddTransaction: Problem pruning mempool: %v", err)
	}

	return nil
}

func (mp *PosMempool) validateTransaction(txn *MsgDeSoTxn, verifySignature bool) error {
	mp.RLock()
	defer mp.RUnlock()

	if err := CheckTransactionSanity(txn, uint32(mp.latestBlockHeight), mp.params); err != nil {
		return errors.Wrapf(err, "PosMempool.AddTransaction: Problem validating transaction sanity")
	}

	if err := ValidateDeSoTxnSanityBalanceModel(txn, mp.latestBlockHeight, mp.params, mp.globalParams); err != nil {
		return errors.Wrapf(err, "PosMempool.AddTransaction: Problem validating transaction sanity")
	}

	if err := mp.latestBlockView.ValidateTransactionNonce(txn, mp.latestBlockHeight); err != nil {
		return errors.Wrapf(err, "PosMempool.AddTransaction: Problem validating transaction nonce")
	}

	if !verifySignature {
		return nil
	}

	// Check transaction signature.
	if _, err := mp.latestBlockView.VerifySignature(txn, uint32(mp.latestBlockHeight)); err != nil {
		return errors.Wrapf(err, "PosMempool.AddTransaction: Signature validation failed")
	}

	return nil
}

func (mp *PosMempool) addTransactionNoLock(txn *MempoolTx, persistToDb bool) error {
	userPk := NewPublicKey(txn.Tx.PublicKey)
	txnFee := txn.Tx.TxnFeeNanos

	// Validate that the user has enough balance to cover the transaction fees.
	spendableBalanceNanos, err := mp.latestBlockView.GetSpendableDeSoBalanceNanosForPublicKey(userPk.ToBytes(),
		uint32(mp.latestBlockHeight))
	if err != nil {
		return errors.Wrapf(err, "PosMempool.addTransactionNoLock: Problem getting spendable balance")
	}
	if err := mp.ledger.CanIncreaseEntryWithLimit(*userPk, txnFee, spendableBalanceNanos); err != nil {
		return errors.Wrapf(err, "PosMempool.addTransactionNoLock: Problem checking balance increase for transaction with"+
			"hash %v, fee %v", txn.Tx.Hash(), txnFee)
	}

	// Check the nonceTracker to see if this transaction is meant to replace an existing one.
	existingTxn := mp.nonceTracker.GetTxnByPublicKeyNonce(*userPk, *txn.Tx.TxnNonce)
	if existingTxn != nil && existingTxn.FeePerKB > txn.FeePerKB {
		return errors.Wrapf(MempoolFailedReplaceByHigherFee, "PosMempool.AddTransaction: Problem replacing transaction "+
			"by higher fee failed. New transaction has lower fee.")
	}

	// If we get here, it means that the transaction's sender has enough balance to cover transaction fees. Moreover, if
	// this transaction is meant to replace an existing one, at this point we know the new txn has a sufficient fee to
	// do so. We can now add the transaction to mempool.
	if err := mp.txnRegister.AddTransaction(txn); err != nil {
		return errors.Wrapf(err, "PosMempool.addTransactionNoLock: Problem adding txn to register")
	}

	// If we've determined that this transaction is meant to replace an existing one, we remove the existing transaction now.
	if existingTxn != nil {
		if err := mp.removeTransactionNoLock(existingTxn, true); err != nil {
			recoveryErr := mp.txnRegister.RemoveTransaction(txn)
			return errors.Wrapf(err, "PosMempool.AddTransaction: Problem removing old transaction from mempool during "+
				"replacement with higher fee. Recovery error: %v", recoveryErr)
		}
	}

	// At this point the transaction is in the mempool. We can now update the ledger and nonce tracker.
	mp.ledger.IncreaseEntry(*userPk, txnFee)
	mp.nonceTracker.AddTxnByPublicKeyNonce(txn, *userPk, *txn.Tx.TxnNonce)

	// Emit an event for the newly added transaction.
	if persistToDb && !mp.inMemoryOnly {
		event := &MempoolEvent{
			Txn:  txn,
			Type: MempoolEventAdd,
		}
		mp.persister.EnqueueEvent(event)
	}

	return nil
}

// loadPersistedTransactions fetches transactions from the persister's storage and adds the transactions to the mempool.
// No lock is held and (persistToDb = false) flag is used when adding transactions internally.
func (mp *PosMempool) loadPersistedTransactions() error {
	if mp.inMemoryOnly {
		return nil
	}

	txns, err := mp.persister.GetPersistedTransactions()
	if err != nil {
		return errors.Wrapf(err, "PosMempool.Start: Problem retrieving transactions from persister")
	}
	// We set the persistToDb flag to false so that persister doesn't try to save the transactions.
	for _, txn := range txns {
		if err := mp.addTransactionNoLock(txn, false); err != nil {
			glog.Errorf("PosMempool.Start: Problem adding transaction with hash (%v) from persister: %v",
				txn.Hash, err)
		}
	}
	return nil
}

// RemoveTransaction is the main function for removing a transaction from the mempool.
func (mp *PosMempool) RemoveTransaction(txnHash *BlockHash) error {
	mp.Lock()
	defer mp.Unlock()

	if !mp.IsRunning() {
		return errors.Wrapf(MempoolErrorNotRunning, "PosMempool.RemoveTransaction: ")
	}

	// Get the transaction from the register.
	txn := mp.txnRegister.GetTransaction(txnHash)
	if txn == nil {
		return nil
	}

	return mp.removeTransactionNoLock(txn, true)
}

func (mp *PosMempool) removeTransactionNoLock(txn *MempoolTx, persistToDb bool) error {
	// First, sanity check our reserved balance.
	userPk := NewPublicKey(txn.Tx.PublicKey)

	// Remove the transaction from the register.
	if err := mp.txnRegister.RemoveTransaction(txn); err != nil {
		return errors.Wrapf(err, "PosMempool.removeTransactionNoLock: Problem removing txn from register")
	}

	// Remove the txn from the balance ledger and the nonce tracker.
	mp.ledger.DecreaseEntry(*userPk, txn.Fee)
	mp.nonceTracker.RemoveTxnByPublicKeyNonce(*userPk, *txn.Tx.TxnNonce)

	// Emit an event for the removed transaction.
	if persistToDb && !mp.inMemoryOnly {
		event := &MempoolEvent{
			Txn:  txn,
			Type: MempoolEventRemove,
		}
		mp.persister.EnqueueEvent(event)
	}

	return nil
}

// GetTransaction returns the transaction with the given hash if it exists in the mempool. This function is thread-safe.
func (mp *PosMempool) GetTransaction(txnHash *BlockHash) *MsgDeSoTxn {
	mp.RLock()
	defer mp.RUnlock()

	if !mp.IsRunning() {
		return nil
	}

	txn := mp.txnRegister.GetTransaction(txnHash)
	if txn == nil || txn.Tx == nil {
		return nil
	}

	return txn.Tx
}

// GetTransactions returns all transactions in the mempool ordered by the Fee-Time algorithm. This function is thread-safe.
func (mp *PosMempool) GetTransactions() []*MsgDeSoTxn {
	mp.RLock()
	defer mp.RUnlock()

	if !mp.IsRunning() {
		return nil
	}

	var desoTxns []*MsgDeSoTxn
	poolTxns := mp.getTransactionsNoLock()
	for _, txn := range poolTxns {
		if txn == nil || txn.Tx == nil {
			continue
		}
		desoTxns = append(desoTxns, txn.Tx)
	}
	return desoTxns
}

func (mp *PosMempool) getTransactionsNoLock() []*MempoolTx {
	return mp.txnRegister.GetFeeTimeTransactions()
}

// GetIterator returns an iterator for the mempool transactions. The iterator can be used to peek transactions in the
// mempool ordered by the Fee-Time algorithm. Transactions can be fetched with the following pattern:
//
//	for it.Next() {
//		if txn, ok := it.Value(); ok {
//			// Do something with txn.
//		}
//	}
//
// Note that the iteration pattern is not thread-safe. Another lock should be used to ensure thread-safety.
func (mp *PosMempool) GetIterator() MempoolIterator {
	mp.RLock()
	defer mp.RUnlock()

	if !mp.IsRunning() {
		return nil
	}

	return NewPosMempoolIterator(mp.txnRegister.GetFeeTimeIterator())
}

// Refresh can be used to evict stale transactions from the mempool. However, it is a bit expensive and should be used
// sparingly. Upon being called, Refresh will create an in-memory temp PosMempool and populate it with transactions from
// the main mempool. The temp mempool will have the most up-to-date latestBlockView, Height, and globalParams. Any
// transaction that fails to add to the temp mempool will be removed from the main mempool.
func (mp *PosMempool) Refresh() error {
	mp.Lock()
	defer mp.Unlock()

	if !mp.IsRunning() {
		return nil
	}

	if err := mp.refreshNoLock(); err != nil {
		return errors.Wrapf(err, "PosMempool.Refresh: Problem refreshing mempool")
	}
	return nil
}

func (mp *PosMempool) refreshNoLock() error {
	// Create the temporary in-memory mempool with the most up-to-date latestBlockView, Height, and globalParams.
	tempPool := NewPosMempool(mp.params, mp.globalParams, mp.latestBlockView, mp.latestBlockHeight, "", true)
	if err := tempPool.Start(); err != nil {
		return errors.Wrapf(err, "PosMempool.refreshNoLock: Problem starting temp pool")
	}
	defer tempPool.Stop()

	// Add all transactions from the main mempool to the temp mempool. Skip signature verification.
	var txnsToRemove []*MempoolTx
	txns := mp.getTransactionsNoLock()
	for _, txn := range txns {
		err := tempPool.AddTransaction(txn.Tx, false)
		if err == nil {
			continue
		}

		// If we've encountered an error while adding the transaction to the temp mempool, we add it to our txnsToRemove list.
		txnsToRemove = append(txnsToRemove, txn)
	}

	// Now remove all transactions from the txnsToRemove list from the main mempool.
	for _, txn := range txnsToRemove {
		if err := mp.removeTransactionNoLock(txn, true); err != nil {
			glog.Errorf("PosMempool.refreshNoLock: Problem removing transaction with hash (%v): %v", txn.Hash, err)
		}
	}

	// Log the hashes for transactions that were removed.
	if len(txnsToRemove) > 0 {
		var removedTxnHashes []string
		for _, txn := range txnsToRemove {
			removedTxnHashes = append(removedTxnHashes, txn.Hash.String())
		}
		glog.Infof("PosMempool.refreshNoLock: Transactions with the following hashes were removed: %v",
			strings.Join(removedTxnHashes, ","))
	}
	return nil
}

// pruneNoLock removes transactions from the mempool until the mempool size is below the maximum allowed size. The transactions
// are removed in lowest to highest Fee-Time priority, i.e. opposite way that transactions are ordered in
// GetTransactions().
func (mp *PosMempool) pruneNoLock() error {
	if mp.txnRegister.Size() < mp.params.MaxMempoolPosSizeBytes {
		return nil
	}

	prunedTxns, err := mp.txnRegister.PruneToSize(mp.params.MaxMempoolPosSizeBytes)
	if err != nil {
		return errors.Wrapf(err, "PosMempool.pruneNoLock: Problem pruning mempool")
	}
	for _, prunedTxn := range prunedTxns {
		if err := mp.removeTransactionNoLock(prunedTxn, true); err != nil {
			// We should never get to here since the transaction was already pruned from the TransactionRegister.
			glog.Errorf("PosMempool.pruneNoLock: Problem removing transaction from mempool: %v", err)
		}
	}
	return nil
}

// UpdateLatestBlock updates the latest block view and latest block node in the mempool.
func (mp *PosMempool) UpdateLatestBlock(blockView *UtxoView, blockHeight uint64) {
	mp.Lock()
	defer mp.Unlock()

	if !mp.IsRunning() {
		return
	}

	mp.latestBlockView = blockView
	mp.latestBlockHeight = blockHeight
}

// UpdateGlobalParams updates the global params in the mempool. Changing GlobalParamsEntry can impact the validity of
// transactions in the mempool. For example, if the minimum network fee is increased, transactions with a fee below the
// new minimum will be removed from the mempool. To safely handle this, this method re-creates the TransactionRegister
// with the new global params and re-adds all transactions in the mempool to the new register.
func (mp *PosMempool) UpdateGlobalParams(globalParams *GlobalParamsEntry) {
	mp.Lock()
	defer mp.Unlock()

	if !mp.IsRunning() {
		return
	}

	mp.globalParams = globalParams
	if err := mp.refreshNoLock(); err != nil {
		glog.Errorf("PosMempool.UpdateGlobalParams: Problem refreshing mempool: %v", err)
	}
}
