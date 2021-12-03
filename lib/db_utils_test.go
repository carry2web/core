package lib

import (
	"github.com/deso-protocol/core/block_view"
	db2 "github.com/deso-protocol/core/db"
	"github.com/deso-protocol/core/network"
	"github.com/deso-protocol/core/types"
	"io/ioutil"
	"log"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec"
	"github.com/dgraph-io/badger/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func _GetTestBlockNode() *types.BlockNode {
	bs := types.BlockNode{}

	// Hash
	bs.Hash = &types.BlockHash{
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09,
		0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19,
		0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x29,
		0x30, 0x31,
	}

	// Height
	bs.Height = 123456789

	// DifficultyTarget
	bs.DifficultyTarget = &types.BlockHash{
		0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x29,
		0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19,
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09,
		0x30, 0x31,
	}

	// CumWork
	bs.CumWork = big.NewInt(5)

	// Header (make a copy)
	bs.Header = network.NewMessage(network.MsgTypeHeader).(*types.MsgDeSoHeader)
	headerBytes, _ := expectedBlockHeader.ToBytes(false)
	bs.Header.FromBytes(headerBytes)

	// Status
	bs.Status = types.StatusBlockValidated

	return &bs
}

func GetTestBadgerDb() (_db *badger.DB, _dir string) {
	dir, err := ioutil.TempDir("", "badgerdb")
	if err != nil {
		log.Fatal(err)
	}

	// Open a badgerdb in a temporary directory.
	opts := badger.DefaultOptions(dir)
	opts.Dir = dir
	opts.ValueDir = dir
	db, err := badger.Open(opts)
	if err != nil {
		log.Fatal(err)
	}

	return db, dir
}

func TestBlockNodeSerialize(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	_ = assert
	_ = require

	bs := _GetTestBlockNode()

	serialized, err := db2.SerializeBlockNode(bs)
	require.NoError(err)
	deserialized, err := db2.DeserializeBlockNode(serialized)
	require.NoError(err)

	assert.Equal(bs, deserialized)
}

func TestBlockNodePutGet(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	_ = assert
	_ = require

	// Create a test db and clean up the files at the end.
	db, dir := GetTestBadgerDb()
	defer os.RemoveAll(dir)

	// Make a blockchain that looks as follows:
	// b1 - -> b2 -> b3
	//    \ -> b4
	// I.e. there's a side chain in there.
	b1 := _GetTestBlockNode()
	b1.Height = 0
	b2 := _GetTestBlockNode()
	b2.Hash[0] = 0x99 // Make the hash of b2 sort lexographically later than b4 for kicks.
	b2.Header.PrevBlockHash = b1.Hash
	b2.Height = 1
	b3 := _GetTestBlockNode()
	b3.Hash[0] = 0x03
	b3.Header.PrevBlockHash = b2.Hash
	b3.Height = 2
	b4 := _GetTestBlockNode()
	b4.Hash[0] = 0x04
	b4.Header.PrevBlockHash = b1.Hash
	b4.Height = 1

	err := db2.PutHeightHashToNodeInfo(b1, db, false /*bitcoinNodes*/)
	require.NoError(err)

	err = db2.PutHeightHashToNodeInfo(b2, db, false /*bitcoinNodes*/)
	require.NoError(err)

	err = db2.PutHeightHashToNodeInfo(b3, db, false /*bitcoinNodes*/)
	require.NoError(err)

	err = db2.PutHeightHashToNodeInfo(b4, db, false /*bitcoinNodes*/)
	require.NoError(err)

	blockIndex, err := db2.GetBlockIndex(db, false /*bitcoinNodes*/)
	require.NoError(err)

	require.Len(blockIndex, 4)
	b1Ret, exists := blockIndex[*b1.Hash]
	require.True(exists, "b1 not found")

	b2Ret, exists := blockIndex[*b2.Hash]
	require.True(exists, "b2 not found")

	b3Ret, exists := blockIndex[*b3.Hash]
	require.True(exists, "b3 not found")

	b4Ret, exists := blockIndex[*b4.Hash]
	require.True(exists, "b4 not found")

	// Make sure the hashes all line up.
	require.Equal(b1.Hash[:], b1Ret.Hash[:])
	require.Equal(b2.Hash[:], b2Ret.Hash[:])
	require.Equal(b3.Hash[:], b3Ret.Hash[:])
	require.Equal(b4.Hash[:], b4Ret.Hash[:])

	// Make sure the nodes are connected properly.
	require.Nil(b1Ret.Parent)
	require.Equal(b2Ret.Parent, b1Ret)
	require.Equal(b3Ret.Parent, b2Ret)
	require.Equal(b4Ret.Parent, b1Ret)

	// Check that getting the best chain works.
	{
		bestChain, err := db2.GetBestChain(b3Ret, blockIndex)
		require.NoError(err)
		require.Len(bestChain, 3)
		require.Equal(b1Ret, bestChain[0])
		require.Equal(b2Ret, bestChain[1])
		require.Equal(b3Ret, bestChain[2])
	}
}

func TestInitDbWithGenesisBlock(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	_ = assert
	_ = require

	// Create a test db and clean up the files at the end.
	db, dir := GetTestBadgerDb()
	defer os.RemoveAll(dir)

	err := db2.InitDbWithDeSoGenesisBlock(&types.DeSoTestnetParams, db, nil)
	require.NoError(err)

	// Check the block index.
	blockIndex, err := db2.GetBlockIndex(db, false /*bitcoinNodes*/)
	require.NoError(err)
	require.Len(blockIndex, 1)
	genesisHash := *types.MustDecodeHexBlockHash(types.DeSoTestnetParams.GenesisBlockHashHex)
	genesis, exists := blockIndex[genesisHash]
	require.True(exists, "genesis block not found in index")
	require.NotNil(genesis)
	require.Equal(&genesisHash, genesis.Hash)

	// Check the bestChain.
	bestChain, err := db2.GetBestChain(genesis, blockIndex)
	require.NoError(err)
	require.Len(bestChain, 1)
	require.Equal(genesis, bestChain[0])
}

func TestPrivateMessages(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	_ = assert
	_ = require

	// Create a test db and clean up the files at the end.
	db, dir := GetTestBadgerDb()
	defer os.RemoveAll(dir)

	priv1, err := btcec.NewPrivateKey(btcec.S256())
	require.NoError(err)
	pk1 := priv1.PubKey().SerializeCompressed()

	priv2, err := btcec.NewPrivateKey(btcec.S256())
	require.NoError(err)
	pk2 := priv2.PubKey().SerializeCompressed()

	priv3, err := btcec.NewPrivateKey(btcec.S256())
	require.NoError(err)
	pk3 := priv3.PubKey().SerializeCompressed()

	tstamp1 := uint64(1)
	tstamp2 := uint64(2)
	tstamp3 := uint64(12345)
	tstamp4 := uint64(time.Now().UnixNano())
	tstamp5 := uint64(time.Now().UnixNano())

	message1Str := []byte("message1: abcdef")
	message2Str := []byte("message2: ghi")
	message3Str := []byte("message3: klmn\123\000\000\000_")
	message4Str := append([]byte("message4: "), db2.RandomBytes(100)...)
	message5Str := append([]byte("message5: "), db2.RandomBytes(123)...)

	// pk1 -> pk2: message1Str, tstamp1
	require.NoError(db2.DbPutMessageEntry(
		db, &block_view.MessageEntry{
			SenderPublicKey:    pk1,
			TstampNanos:        tstamp1,
			RecipientPublicKey: pk2,
			EncryptedText:      message1Str,
		}))
	// pk2 -> pk1: message2Str, tstamp2
	require.NoError(db2.DbPutMessageEntry(
		db, &block_view.MessageEntry{
			SenderPublicKey:    pk2,
			TstampNanos:        tstamp2,
			RecipientPublicKey: pk1,
			EncryptedText:      message2Str,
		}))
	// pk3 -> pk1: message3Str, tstamp3
	require.NoError(db2.DbPutMessageEntry(
		db, &block_view.MessageEntry{
			SenderPublicKey:    pk3,
			TstampNanos:        tstamp3,
			RecipientPublicKey: pk1,
			EncryptedText:      message3Str,
		}))
	// pk2 -> pk1: message4Str, tstamp4
	require.NoError(db2.DbPutMessageEntry(
		db, &block_view.MessageEntry{
			SenderPublicKey:    pk2,
			TstampNanos:        tstamp4,
			RecipientPublicKey: pk1,
			EncryptedText:      message4Str,
		}))
	// pk1 -> pk3: message5Str, tstamp5
	require.NoError(db2.DbPutMessageEntry(
		db, &block_view.MessageEntry{
			SenderPublicKey:    pk1,
			TstampNanos:        tstamp5,
			RecipientPublicKey: pk3,
			EncryptedText:      message5Str,
		}))

	// Define all the messages as they appear in the db.
	message1 := &block_view.MessageEntry{
		SenderPublicKey:    pk1,
		RecipientPublicKey: pk2,
		EncryptedText:      message1Str,
		TstampNanos:        tstamp1,
	}
	message2 := &block_view.MessageEntry{
		SenderPublicKey:    pk2,
		RecipientPublicKey: pk1,
		EncryptedText:      message2Str,
		TstampNanos:        tstamp2,
	}
	message3 := &block_view.MessageEntry{
		SenderPublicKey:    pk3,
		RecipientPublicKey: pk1,
		EncryptedText:      message3Str,
		TstampNanos:        tstamp3,
	}
	message4 := &block_view.MessageEntry{
		SenderPublicKey:    pk2,
		RecipientPublicKey: pk1,
		EncryptedText:      message4Str,
		TstampNanos:        tstamp4,
	}
	message5 := &block_view.MessageEntry{
		SenderPublicKey:    pk1,
		RecipientPublicKey: pk3,
		EncryptedText:      message5Str,
		TstampNanos:        tstamp5,
	}

	// Fetch message3 directly using both public keys.
	{
		msg := db2.DbGetMessageEntry(db, pk3, tstamp3)
		require.Equal(message3, msg)
	}
	{
		msg := db2.DbGetMessageEntry(db, pk1, tstamp3)
		require.Equal(message3, msg)
	}

	// Fetch all messages for pk1
	{
		messages, err := db2.DbGetMessageEntriesForPublicKey(db, pk1)
		require.NoError(err)

		require.Equal([]*block_view.MessageEntry{
			message1,
			message2,
			message3,
			message4,
			message5,
		}, messages)
	}

	// Fetch all messages for pk2
	{
		messages, err := db2.DbGetMessageEntriesForPublicKey(db, pk2)
		require.NoError(err)

		require.Equal([]*block_view.MessageEntry{
			message1,
			message2,
			message4,
		}, messages)
	}

	// Fetch all messages for pk3
	{
		messages, err := db2.DbGetMessageEntriesForPublicKey(db, pk3)
		require.NoError(err)

		require.Equal([]*block_view.MessageEntry{
			message3,
			message5,
		}, messages)
	}

	// Delete message3
	require.NoError(db2.DbDeleteMessageEntryMappings(db, pk1, tstamp3))

	// Now all the messages returned should exclude message3
	{
		messages, err := db2.DbGetMessageEntriesForPublicKey(db, pk1)
		require.NoError(err)

		require.Equal([]*block_view.MessageEntry{
			message1,
			message2,
			message4,
			message5,
		}, messages)
	}
	{
		messages, err := db2.DbGetMessageEntriesForPublicKey(db, pk2)
		require.NoError(err)

		require.Equal([]*block_view.MessageEntry{
			message1,
			message2,
			message4,
		}, messages)
	}
	{
		messages, err := db2.DbGetMessageEntriesForPublicKey(db, pk3)
		require.NoError(err)

		require.Equal([]*block_view.MessageEntry{
			message5,
		}, messages)
	}

	// Delete all remaining messages, sometimes using the recipient rather
	// than the sender public key
	require.NoError(db2.DbDeleteMessageEntryMappings(db, pk2, tstamp1))
	require.NoError(db2.DbDeleteMessageEntryMappings(db, pk1, tstamp2))
	require.NoError(db2.DbDeleteMessageEntryMappings(db, pk2, tstamp4))
	require.NoError(db2.DbDeleteMessageEntryMappings(db, pk1, tstamp5))

	// Now all public keys should have zero messages.
	{
		messages, err := db2.DbGetMessageEntriesForPublicKey(db, pk1)
		require.NoError(err)
		require.Equal(0, len(messages))
	}
	{
		messages, err := db2.DbGetMessageEntriesForPublicKey(db, pk2)
		require.NoError(err)
		require.Equal(0, len(messages))
	}
	{
		messages, err := db2.DbGetMessageEntriesForPublicKey(db, pk3)
		require.NoError(err)
		require.Equal(0, len(messages))
	}
}

func TestFollows(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	_ = assert
	_ = require

	// Create a test db and clean up the files at the end.
	db, dir := GetTestBadgerDb()
	defer os.RemoveAll(dir)

	priv1, err := btcec.NewPrivateKey(btcec.S256())
	require.NoError(err)
	pk1 := priv1.PubKey().SerializeCompressed()

	priv2, err := btcec.NewPrivateKey(btcec.S256())
	require.NoError(err)
	pk2 := priv2.PubKey().SerializeCompressed()

	priv3, err := btcec.NewPrivateKey(btcec.S256())
	require.NoError(err)
	pk3 := priv3.PubKey().SerializeCompressed()

	// Get the PKIDs for all the public keys
	pkid1 := db2.DBGetPKIDEntryForPublicKey(db, pk1).PKID
	pkid2 := db2.DBGetPKIDEntryForPublicKey(db, pk2).PKID
	pkid3 := db2.DBGetPKIDEntryForPublicKey(db, pk3).PKID

	// PK2 follows everyone. Make sure "get" works properly.
	require.Nil(db2.DbGetFollowerToFollowedMapping(db, pkid2, pkid1))
	require.NoError(db2.DbPutFollowMappings(db, pkid2, pkid1))
	require.NotNil(db2.DbGetFollowerToFollowedMapping(db, pkid2, pkid1))
	require.Nil(db2.DbGetFollowerToFollowedMapping(db, pkid2, pkid3))
	require.NoError(db2.DbPutFollowMappings(db, pkid2, pkid3))
	require.NotNil(db2.DbGetFollowerToFollowedMapping(db, pkid2, pkid3))

	// pkid3 only follows pkid1. Make sure "get" works properly.
	require.Nil(db2.DbGetFollowerToFollowedMapping(db, pkid3, pkid1))
	require.NoError(db2.DbPutFollowMappings(db, pkid3, pkid1))
	require.NotNil(db2.DbGetFollowerToFollowedMapping(db, pkid3, pkid1))

	// Check PK1's followers.
	{
		pubKeys, err := db2.DbGetPubKeysFollowingYou(db, pk1)
		require.NoError(err)
		for i := 0; i < len(pubKeys); i++ {
			require.Contains([][]byte{pk2, pk3}, pubKeys[i])
		}
	}

	// Check PK1's follows.
	{
		pubKeys, err := db2.DbGetPubKeysYouFollow(db, pk1)
		require.NoError(err)
		require.Equal(len(pubKeys), 0)
	}

	// Check PK2's followers.
	{
		pubKeys, err := db2.DbGetPubKeysFollowingYou(db, pk2)
		require.NoError(err)
		require.Equal(len(pubKeys), 0)
	}

	// Check PK2's follows.
	{
		pubKeys, err := db2.DbGetPubKeysYouFollow(db, pk2)
		require.NoError(err)
		for i := 0; i < len(pubKeys); i++ {
			require.Contains([][]byte{pk1, pk3}, pubKeys[i])
		}
	}

	// Check PK3's followers.
	{
		pubKeys, err := db2.DbGetPubKeysFollowingYou(db, pk3)
		require.NoError(err)
		for i := 0; i < len(pubKeys); i++ {
			require.Contains([][]byte{pk2}, pubKeys[i])
		}
	}

	// Check PK3's follows.
	{
		pubKeys, err := db2.DbGetPubKeysYouFollow(db, pk3)
		require.NoError(err)
		for i := 0; i < len(pubKeys); i++ {
			require.Contains([][]byte{pk1, pk1}, pubKeys[i])
		}
	}

	// Delete PK2's follows.
	require.NoError(db2.DbDeleteFollowMappings(db, pkid2, pkid1))
	require.NoError(db2.DbDeleteFollowMappings(db, pkid2, pkid3))

	// Check PK2's follows were actually deleted.
	{
		pubKeys, err := db2.DbGetPubKeysYouFollow(db, pk2)
		require.NoError(err)
		require.Equal(len(pubKeys), 0)
	}
}
