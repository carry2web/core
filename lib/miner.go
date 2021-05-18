// TODO(DELETEME): This entire file is replaced by remote_miner.go. We should
// delete all of this code and use remote_miner in all the places where we currently
// use the miner. The reason we don't do this now is it would break a lot of test cases
// that we have.
package lib

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/bitclout/core/clouthash"
	"github.com/btcsuite/btcd/wire"

	"github.com/btcsuite/btcd/btcec"
	"github.com/davecgh/go-spew/spew"
	"github.com/golang/glog"
	merkletree "github.com/laser/go-merkle-tree"
	"github.com/pkg/errors"
)

// miner.go contains all of the logic for mining blocks with a CPU.

type BitCloutMiner struct {
	PublicKeys    []*btcec.PublicKey
	numThreads    uint32
	BlockProducer *BitCloutBlockProducer
	params        *BitCloutParams

	stopping int32
}

func NewBitCloutMiner(_minerPublicKeys []string, _numThreads uint32,
	_blockProducer *BitCloutBlockProducer, _params *BitCloutParams) (*BitCloutMiner, error) {

	// Convert the public keys from Base58Check encoding to bytes.
	_pubKeys := []*btcec.PublicKey{}
	for _, publicKeyBase58 := range _minerPublicKeys {
		pkBytes, _, err := Base58CheckDecode(publicKeyBase58)
		if err != nil {
			return nil, errors.Wrapf(err, "NewBitCloutMiner: ")
		}
		pkObj, err := btcec.ParsePubKey(pkBytes, btcec.S256())
		if err != nil {
			return nil, errors.Wrapf(err, "NewBitCloutMiner: ")
		}
		_pubKeys = append(_pubKeys, pkObj)
	}

	return &BitCloutMiner{
		PublicKeys:    _pubKeys,
		numThreads:    _numThreads,
		BlockProducer: _blockProducer,
		params:        _params,
	}, nil
}

func (bitcloutMiner *BitCloutMiner) Stop() {
	atomic.AddInt32(&bitcloutMiner.stopping, 1)
}

func (bitcloutMiner *BitCloutMiner) _getBlockToMine(threadIndex uint32) (
	_blk *MsgBitCloutBlock, _diffTarget *BlockHash, _lastNode *BlockNode, _err error) {

	// Choose a random address to contribute the coins to. Use the extraNonce to
	// choose the random address since it's random.
	var rewardPk *btcec.PublicKey
	if len(bitcloutMiner.PublicKeys) == 0 {
		// This is to account for a really weird edge case where somebody stops the miner
		// in the middle of us getting a block.
		rewardPk = nil
	} else {
		randomNum, _ := wire.RandomUint64()
		pkIndex := int(randomNum % uint64(len(bitcloutMiner.PublicKeys)))
		rewardPk = bitcloutMiner.PublicKeys[pkIndex]
	}

	return bitcloutMiner.BlockProducer._getBlockTemplate(rewardPk.SerializeCompressed())
}

func (bitcloutMiner *BitCloutMiner) _getRandomPublicKey() []byte {
	rand.Seed(time.Now().Unix())
	return bitcloutMiner.PublicKeys[rand.Intn(len(bitcloutMiner.PublicKeys))].SerializeCompressed()
}

func (bitcloutMiner *BitCloutMiner) _mineSingleBlock(threadIndex uint32) (_diffTarget *BlockHash, minedBlock *MsgBitCloutBlock) {
	for {
		// This provides a way for outside processes to pause the miner.
		if len(bitcloutMiner.PublicKeys) == 0 {
			if atomic.LoadInt32(&bitcloutMiner.stopping) == 1 {
				glog.Debugf("BitCloutMiner._startThread: Stopping thread %d", threadIndex)
				break
			}
			time.Sleep(1 * time.Second)
			continue
		}

		// Get a single header to hash on from our BlockProducer. This will have a unique
		// ExtraNonce associated with it, making the hash space this thread is mining on
		// different from all the other threads.
		//
		// TODO(miner): Replace with a call to GetBlockTemplate
		publicKey := bitcloutMiner._getRandomPublicKey()
		blockID, headerBytes, extraNonces, diffTarget, err := bitcloutMiner.BlockProducer.GetHeadersAndExtraDatas(
			publicKey, 1 /*numHeaders*/, CurrentHeaderVersion)
		if err != nil {
			glog.Errorf("BitCloutMiner._startThread: Error getting header to "+
				"hash on; this should never happen unless we're starting up: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}
		header := &MsgBitCloutHeader{}
		if err := header.FromBytes(headerBytes[0]); err != nil {
			glog.Errorf("BitCloutMiner._startThread: Error parsing header to " +
				"hash on; this should never happen")
			time.Sleep(1 * time.Second)
			continue
		}

		// Compute a few hashes before checking if we've solved the block.
		timeBefore := time.Now()
		bestHash, bestNonce, err := FindLowestHash(header, uint64(bitcloutMiner.params.MiningIterationsPerCycle))
		glog.Tracef("BitCloutMiner._startThread: Time per iteration: %v", time.Since(timeBefore))
		if err != nil {
			// If there's an error just log it and break out.
			glog.Error(errors.Wrapf(err, "BitCloutMiner._startThread: Problem while mining: "))
			break
		}

		if atomic.LoadInt32(&bitcloutMiner.stopping) == 1 {
			glog.Debugf("BitCloutMiner._startThread: Stopping thread %d", threadIndex)
			break
		}

		if LessThan(diffTarget, bestHash) {
			//glog.Tracef("BitCloutMiner._startThread: Best hash found %v does not beat target %v",
			//hex.EncodeToString(bestHash[:]), hex.EncodeToString(diffTarget[:]))
			continue
		}

		// If we get here then it means our bestHash has beaten the target and
		// that bestNonce is the nonce that generates the solution hash.

		// Set the winning nonce on the block's header.
		blockToMine, err := bitcloutMiner.BlockProducer.GetCopyOfRecentBlock(blockID)
		if err != nil {
			glog.Errorf("BitCloutMiner._startThread: Error getting block for blockID %v; "+
				"this should never happen", blockID)
			time.Sleep(1 * time.Second)
			continue
		}

		// Swap in the public key and extraNonce. This should make the block consistent with
		// the header we were just mining on.
		blockToMine.Txns[0].TxOutputs[0].PublicKey = publicKey
		blockToMine.Txns[0].TxnMeta.(*BlockRewardMetadataa).ExtraData = UintToBuf(extraNonces[0])

		// Set the header for the block, which should update the merkle root.
		blockToMine.Header = header

		// Use the nonce we computed
		blockToMine.Header.Nonce = bestNonce

		return diffTarget, blockToMine
	}

	return nil, nil
}

func (bitcloutMiner *BitCloutMiner) MineAndProcessSingleBlock(threadIndex uint32, mempoolToUpdate *BitCloutMempool) (_block *MsgBitCloutBlock, _err error) {
	// Add a call to update the BlockProducer.
	// TODO(performance): We shouldn't have to do this, it just makes tests pass right now.
	if err := bitcloutMiner.BlockProducer.UpdateLatestBlockTemplate(); err != nil {
		// Error if we can't update the template but don't stop the show.
		glog.Error(err)
	}

	diffTarget, blockToMine := bitcloutMiner._mineSingleBlock(threadIndex)
	if blockToMine == nil {
		return nil, fmt.Errorf("BitCloutMiner._startThread: _mineSingleBlock returned nil; should only happen if we're stopping")
	}

	// Log information on the block we just mined.
	bestHash, _ := blockToMine.Hash()
	glog.Infof("================== YOU MINED A NEW BLOCK! ================== Height: %d, Hash: %s", blockToMine.Header.Height, hex.EncodeToString(bestHash[:]))
	glog.Debugf("Height: (%d), Diff target: (%s), "+
		"New hash: (%s), , Header Tip: %v, Block Tip: %v", blockToMine.Header.Height,
		hex.EncodeToString(diffTarget[:])[:10], hex.EncodeToString(bestHash[:]),
		bitcloutMiner.BlockProducer.chain.headerTip().Header,
		bitcloutMiner.BlockProducer.chain.blockTip().Header)
	scs := spew.ConfigState{DisableMethods: true, Indent: "  ", DisablePointerAddresses: true}
	glog.Debugf(scs.Sdump(blockToMine))
	// Sanitize the block for the comparison we're about to do. We need to do
	// this because the comparison function below will think they're different
	// if one has nil and one has an empty list. Annoying, but this solves the
	// issue.
	for _, tx := range blockToMine.Txns {
		if len(tx.TxInputs) == 0 {
			tx.TxInputs = nil
		}
	}
	blockBytes, err := blockToMine.ToBytes(false)
	if err != nil {
		glog.Error(err)
		return nil, err
	}
	glog.Debugf("Block bytes hex %d: %s", blockToMine.Header.Height, hex.EncodeToString(blockBytes))
	blockFromBytes := &MsgBitCloutBlock{}
	err = blockFromBytes.FromBytes(blockBytes)
	if err != nil || !reflect.DeepEqual(*blockToMine, *blockFromBytes) {
		glog.Error(err)
		fmt.Println("Block as it was mined: ", *blockToMine)
		scs.Dump(blockToMine)
		fmt.Println("Block as it was de-serialized:", *blockFromBytes)
		scs.Dump(blockFromBytes)
		glog.Debugf("In case you missed the hex %d: %s", blockToMine.Header.Height, hex.EncodeToString(blockBytes))
		glog.Errorf("BitCloutMiner.MineAndProcessSingleBlock: ERROR: Problem with block "+
			"serialization (see above for dumps of blocks): Diff: %v, err?: %v", Diff(blockToMine, blockFromBytes), err)
	}
	glog.Tracef("Mined block height:num_txns: %d:%d\n", blockToMine.Header.Height, len(blockToMine.Txns))

	// TODO: This is duplicate code, but this whole file should probably be deleted or
	// reworked to use the block producer API anyway.
	if err := bitcloutMiner.BlockProducer.SignBlock(blockToMine); err != nil {
		return nil, fmt.Errorf("Error signing block: %v", err)
	}

	// Process the block. If the block is connected and/or accepted, the Server
	// will be informed about it. This will cause it to be relayed appropriately.
	verifySignatures := true
	// TODO(miner): Replace with a call to SubmitBlock.
	isMainChain, isOrphan, err := bitcloutMiner.BlockProducer.chain.ProcessBlock(
		blockToMine, verifySignatures)
	glog.Tracef("Called ProcessBlock: isMainChain=(%v), isOrphan=(%v), err=(%v)",
		isMainChain, isOrphan, err)
	if err != nil {
		glog.Errorf("ERROR calling ProcessBlock: isMainChain=(%v), isOrphan=(%v), err=(%v)",
			isMainChain, isOrphan, err)
		// We return the block even when we have an error in case the caller wants to do
		// something with it.
		return blockToMine, fmt.Errorf("ERROR calling ProcessBlock: isMainChain=(%v), isOrphan=(%v), err=(%v)",
			isMainChain, isOrphan, err)
	}

	// If a mempool object is passed then update it. Normally this isn't necessary because
	// ProcessBlock will trigger it because the backendServer will be set on the blockchain
	// object. But it's useful for tests.
	if mempoolToUpdate != nil {
		mempoolToUpdate.UpdateAfterConnectBlock(blockToMine)
	}

	decimalPlaces := int64(1000)
	diffTargetBaseline, _ := hex.DecodeString(bitcloutMiner.params.MinDifficultyTargetHex)
	diffTargetBaselineBlockHash := BlockHash{}
	copy(diffTargetBaselineBlockHash[:], diffTargetBaseline)
	diffTargetBaselineBigint := big.NewInt(0).Mul(HashToBigint(&diffTargetBaselineBlockHash), big.NewInt(decimalPlaces))
	diffTargetBigint := HashToBigint(diffTarget)
	glog.Debugf("Difficulty factor (1 = 1 core running): %v", float32(big.NewInt(0).Div(diffTargetBaselineBigint, diffTargetBigint).Int64())/float32(decimalPlaces))

	if atomic.LoadInt32(&bitcloutMiner.stopping) == 1 {
		return nil, fmt.Errorf("BitCloutMiner._startThread: Stopping thread %d", threadIndex)
	}

	return blockToMine, nil
}

func (bitcloutMiner *BitCloutMiner) _startThread(threadIndex uint32) {
	for {
		newBlock, err := bitcloutMiner.MineAndProcessSingleBlock(threadIndex, nil /*mempoolToUpdate*/)
		if err != nil {
			glog.Errorf(err.Error())
		}
		isFinished := (newBlock == nil)
		if isFinished {
			return
		}
	}
}

func (bitcloutMiner *BitCloutMiner) Start() {
	if bitcloutMiner.BlockProducer == nil {
		glog.Infof("BitCloutMiner.Start: NOT starting miner because " +
			"max_block_templates_to_cache = 0; set it to a non-zero value to " +
			"start the miner")
		return
	}
	glog.Infof("BitCloutMiner.Start: Starting miner with difficulty target %s", bitcloutMiner.params.MinDifficultyTargetHex)
	blockTip := bitcloutMiner.BlockProducer.chain.blockTip()
	glog.Infof("BitCloutMiner.Start: Block tip height %d, cum work %v, and difficulty %v",
		blockTip.Header.Height, BigintToHash(blockTip.CumWork), blockTip.DifficultyTarget)
	// Start a bunch of threads to mine for blocks.
	for threadIndex := uint32(0); threadIndex < bitcloutMiner.numThreads; threadIndex++ {
		go func(threadIndex uint32) {
			glog.Debugf("BitCloutMiner.Start: Starting thread %d", threadIndex)
			bitcloutMiner._startThread(threadIndex)
		}(threadIndex)
	}
}

func CopyBytesIntoBlockHash(data []byte) *BlockHash {
	if len(data) != HashSizeBytes {
		errorStr := fmt.Sprintf("CopyBytesIntoBlockHash: Got data of size %d for BlockHash of size %d", len(data), HashSizeBytes)
		glog.Error(errorStr)
		return nil
	}
	var blockHash BlockHash
	copy(blockHash[:], data)
	return &blockHash
}

// ProofOfWorkHash is a hash function designed for computing BitClout block hashes.
// It seems the optimal hash function is one that satisfies two properties:
// 1) It is not computable by any existing ASICs. If this property isn't satisfied
//    then miners with pre-existing investments in ASICs for other coins can very
//    cheaply mine on our chain for a short period of time to pull off a 51% attack.
//    This has actually happened with "merge-mined" coins like Namecoin.
// 2) If implemented on an ASIC, there is an "orders of magnitude" speed-up over
//    using a CPU or GPU. This is because ASICs require some amount of capital
//    expenditure up-front in order to mine, which then aligns the owner of the
//    ASIC to care about the health of the network over a longer period of time. In
//    contrast, a hash function that is CPU or GPU-mineable can be attacked with
//    an AWS fleet early on. This also may result in a more eco-friendly chain, since
//    the hash power will be more bottlenecked by up-front CapEx rather than ongoing
//    electricity cost, as is the case with GPU-mined coins.
//
// Note that our pursuit of (2) above runs counter to existing dogma which seeks to
// prioritize "ASIC-resistance" in hash functions.
//
// Given the above, the hash function chosen is a simple twist on sha3
// that we don't think any ASIC exists for currently. Note that creating an ASIC for
// this should be relatively straightforward, however, which allows us to satisfy
// property (2) above.
func ProofOfWorkHash(inputBytes []byte, version uint32) *BlockHash {
	output := BlockHash{}

	if version == HeaderVersion0 {
		hashBytes := clouthash.CloutHashV0(inputBytes)
		copy(output[:], hashBytes[:])
	} else if version == HeaderVersion1 {
		hashBytes := clouthash.CloutHashV1(inputBytes)
		copy(output[:], hashBytes[:])
	} else {
		// If we don't recognize the version, we return the v0 hash. We do
		// this to avoid having to return an error or panic.
		hashBytes := clouthash.CloutHashV0(inputBytes)
		copy(output[:], hashBytes[:])
	}

	return &output
}

func Sha256DoubleHash(input []byte) *BlockHash {
	hashBytes := merkletree.Sha256DoubleHash(input)
	ret := &BlockHash{}
	copy(ret[:], hashBytes[:])
	return ret
}

func HashToBigint(hash *BlockHash) *big.Int {
	// No need to check errors since the string is necessarily a valid hex
	// string.
	val, itWorked := new(big.Int).SetString(hex.EncodeToString(hash[:]), 16)
	if !itWorked {
		glog.Errorf("Failed in converting []byte (%#v) to bigint.", hash)
	}
	return val
}

func BigintToHash(bigint *big.Int) *BlockHash {
	hexStr := bigint.Text(16)
	if len(hexStr)%2 != 0 {
		// If we have an odd number of bytes add one to the beginning (remember
		// the bigints are big-endian.
		hexStr = "0" + hexStr
	}
	hexBytes, err := hex.DecodeString(hexStr)
	if err != nil {
		glog.Errorf("Failed in converting bigint (%#v) with hex "+
			"string (%s) to hash.", bigint, hexStr)
	}
	if len(hexBytes) > HashSizeBytes {
		glog.Errorf("BigintToHash: Bigint %v overflows the hash size %d", bigint, HashSizeBytes)
		return nil
	}

	var retBytes BlockHash
	copy(retBytes[HashSizeBytes-len(hexBytes):], hexBytes)
	return &retBytes
}

func BytesToBigint(bb []byte) *big.Int {
	val, itWorked := new(big.Int).SetString(hex.EncodeToString(bb), 16)
	if !itWorked {
		glog.Errorf("Failed in converting []byte (%#v) to bigint.", bb)
	}
	return val
}

func BigintToBytes(bigint *big.Int) []byte {
	hexStr := bigint.Text(16)
	if len(hexStr)%2 != 0 {
		// If we have an odd number of bytes add one to the beginning (remember
		// the bigints are big-endian.
		hexStr = "0" + hexStr
	}
	hexBytes, err := hex.DecodeString(hexStr)
	if err != nil {
		glog.Errorf("Failed in converting bigint (%#v) with hex "+
			"string (%s) to []byte.", bigint, hexStr)
	}
	return hexBytes
}

// FindLowestHash
// Mine for a given number of iterations and return the lowest hash value
// found and its associated nonce. Hashing starts at the value of the Nonce
// set on the blockHeader field when it is passed and increments the value
// of the passed blockHeader field as it iterates. This makes it easy to
// continue a subsequent batch of iterations after we return.
func FindLowestHash(
	blockHeaderr *MsgBitCloutHeader, iterations uint64) (
	lowestHash *BlockHash, lowestNonce uint64, ee error) {
	//// Compute a hash of the header with the current nonce value.
	bestNonce := blockHeaderr.Nonce
	bestHash, err := blockHeaderr.Hash()
	if err != nil {
		return nil, 0, err
	}

	for iterations > 0 {
		// Increment the nonce.
		blockHeaderr.Nonce++

		// Compute a new hash.
		currentHash, err := blockHeaderr.Hash()
		if err != nil {
			return nil, 0, err
		}

		// See if it's better than what we currently have
		if LessThan(currentHash, bestHash) {
			bestHash = currentHash
			bestNonce = blockHeaderr.Nonce
		}

		iterations--
	}

	// Increment the nonce one last time since we checked this hash.
	blockHeaderr.Nonce++

	return bestHash, bestNonce, nil
}

func LessThan(aa *BlockHash, bb *BlockHash) bool {
	aaBigint := new(big.Int)
	aaBigint.SetBytes(aa[:])
	bbBigint := new(big.Int)
	bbBigint.SetBytes(bb[:])

	return aaBigint.Cmp(bbBigint) < 0
}
