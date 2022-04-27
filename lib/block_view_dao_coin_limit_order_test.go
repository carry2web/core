package lib

import (
	"bytes"
	"fmt"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
	"math"
	"math/big"
	"testing"
)

func TestDAOCoinLimitOrder(t *testing.T) {
	// Initialization
	require := require.New(t)
	chain, params, db := NewLowDifficultyBlockchain()
	mempool, miner := NewTestMiner(t, chain, params, true)
	params.ForkHeights.DAOCoinBlockHeight = uint32(0)
	params.ForkHeights.DAOCoinLimitOrderBlockHeight = uint32(0)
	utxoView, err := NewUtxoView(db, params, chain.postgres)
	require.NoError(err)
	dbAdapter := utxoView.GetDbAdapter()

	// Mine a few blocks to give the senderPkString some $DESO.
	for ii := 0; ii < 15; ii++ {
		_, err = miner.MineAndProcessSingleBlock(0, mempool)
		require.NoError(err)
	}

	// We build the testMeta obj after mining blocks so that we save the correct block height.
	testMeta := DAOCoinLimitOrderTestMeta{
		TestMeta: &TestMeta{
			t:       t,
			chain:   chain,
			params:  params,
			db:      db,
			mempool: mempool,
			miner:   miner,
			// We take the block tip to be the blockchain height rather than the header chain height.
			savedHeight: chain.blockTip().Height + 1,
		},
		UtxoView:          utxoView,
		FeeRateNanosPerKb: uint64(101),
	}

	deso := testMeta.GetUser("$DESO")
	m0 := testMeta.GetUser("m0")
	m1 := testMeta.GetUser("m1")
	m2 := testMeta.GetUser("m2")
	m3 := testMeta.GetUser("m3")
	m4 := testMeta.GetUser("m4")

	_registerOrTransferWithTestMeta(testMeta.TestMeta, m0.Name, senderPkString, m0.Pub, senderPrivString, 7000)
	_registerOrTransferWithTestMeta(testMeta.TestMeta, m1.Name, senderPkString, m1.Pub, senderPrivString, 4000)
	_registerOrTransferWithTestMeta(testMeta.TestMeta, m2.Name, senderPkString, m2.Pub, senderPrivString, 1400)
	_registerOrTransferWithTestMeta(testMeta.TestMeta, m3.Name, senderPkString, m3.Pub, senderPrivString, 210)
	_registerOrTransferWithTestMeta(testMeta.TestMeta, m4.Name, senderPkString, m4.Pub, senderPrivString, 100)
	_registerOrTransferWithTestMeta(testMeta.TestMeta, "", senderPkString, paramUpdaterPub, senderPrivString, 100)

	{
		params.ParamUpdaterPublicKeys[MakePkMapKey(paramUpdaterPkBytes)] = true
		_updateGlobalParamsEntryWithTestMeta(
			testMeta.TestMeta, testMeta.FeeRateNanosPerKb, paramUpdaterPub, paramUpdaterPriv,
			-1, int64(testMeta.FeeRateNanosPerKb), -1, -1, -1, /*maxCopiesPerNFT*/
		)
	}
	{
		// RuleErrorDAOCoinLimitOrderCannotBuyAndSellSameCoin
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: deso, Selling: deso, Price: 0.1, Quantity: 100,
		})
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderCannotBuyAndSellSameCoin)
	}
	{
		// RuleErrorDAOCoinLimitOrderInvalidOperationType
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: m0, Selling: deso, Price: 0.1, Quantity: 100, OperationType: 99,
		})
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderInvalidOperationType)
	}
	{
		// RuleErrorDAOCoinLimitOrderInvalidFillType
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: m0, Selling: deso, Price: 0.1, Quantity: 100, FillType: 99,
		})
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderInvalidFillType)
	}
	{
		// RuleErrorDAOCoinLimitOrderBuyingDAOCoinCreatorMissingProfile
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: m0, Selling: deso, Price: 0.1, Quantity: 100,
		})
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderBuyingDAOCoinCreatorMissingProfile)
	}
	{
		// Create a profile for m0.
		testMeta.CreateProfile(m0)
	}
	{
		// RuleErrorDAOCoinLimitOrderInvalidExchangeRate: zero
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: m0, Selling: deso, Price: 0, Quantity: 100,
		})
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderInvalidExchangeRate)
	}
	{
		// RuleErrorDAOCoinLimitOrderInvalidQuantity: zero
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: m0, Selling: deso, Price: 0.1, Quantity: 0,
		})
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderInvalidQuantity)
	}
	{
		// RuleErrorDAOCoinLimitOrderTotalCostOverflowsUint256
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor: m0,
			Buying:     m0,
			Selling:    deso,
			ScaledExchangeRateCoinsToSellPerCoinToBuy: MaxUint256.Clone(),
			QuantityToFillInBaseUints:                 MaxUint256.Clone(),
		})
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderTotalCostOverflowsUint256)
	}
	{
		// RuleErrorDAOCoinLimitOrderTotalCostIsLessThanOneNano
		// 100 * .009 = .9 should truncate to 0 coins to sell
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: m0, Selling: deso, Price: 0.009, Quantity: 100,
		})
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderTotalCostIsLessThanOneNano)
	}
	{
		// RuleErrorDAOCoinLimitOrderTotalCostIsLessThanOneNano
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor: m0,
			Buying:     m0,
			Selling:    deso,
			ScaledExchangeRateCoinsToSellPerCoinToBuy: uint256.NewInt().SetUint64(1),
			Quantity: 1,
		})
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderTotalCostIsLessThanOneNano)
	}
	{
		// RuleErrorDAOCoinLimitOrderInsufficientDESOToOpenOrder
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: m0, Selling: deso, Price: 1, Quantity: math.MaxUint64,
		})
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderInsufficientDESOToOpenOrder)
	}
	{
		// Happy path: m0 submits limit order which is stored.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			Buying:         m0,
			Selling:        deso,
			Price:          0.1,
			Quantity:       100,
			OrderBookDelta: 1, // Order is stored.
		})
		require.NoError(err)
	}
	{
		// Test GetAllDAOCoinLimitOrdersForThisDAOCoinPair()
		expectedOrder := DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: m0, Selling: deso, Price: 0.1, Quantity: 100,
		}

		// Test database query.
		// Confirm 1 existing limit order, and it's from m0.
		orderEntries, err := dbAdapter.GetAllDAOCoinLimitOrdersForThisDAOCoinPair(m0.PKID, deso.PKID)
		require.NoError(err)
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(expectedOrder))

		// Test UTXO view query.
		// Confirm 1 existing limit order, and it's from m0.
		orderEntries, err = utxoView.GetAllDAOCoinLimitOrdersForThisDAOCoinPair(m0.PKID, deso.PKID)
		require.NoError(err)
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(expectedOrder))
	}
	{
		// Test GetAllDAOCoinLimitOrdersForThisTransactor()
		expectedOrder := DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: m0, Selling: deso, Price: 0.1, Quantity: 100,
		}

		// Test database query.
		// Confirm 1 existing limit order, and it's from m0.
		orderEntries, err := dbAdapter.GetAllDAOCoinLimitOrdersForThisTransactor(m0.PKID)
		require.NoError(err)
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(expectedOrder))

		// Test UTXO view query.
		// Confirm 1 existing limit order, and it's from m0.
		orderEntries, err = utxoView.GetAllDAOCoinLimitOrdersForThisTransactor(m0.PKID)
		require.NoError(err)
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(expectedOrder))
	}
	{
		// RuleErrorDAOCoinLimitOrderInsufficientDAOCoinsToOpenOrder
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor: m1, Buying: deso, Selling: m0, Price: 10, Quantity: 10,
		})
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderInsufficientDAOCoinsToOpenOrder)
	}
	{
		// Mint m0 DAO coins and transfer to m1.
		testMeta.MintDAOCoins(m0, 1e4)
		testMeta.TransferDAOCoins(m0, m0, m1, 3000)
	}
	{
		// m1 submits limit order for 10 $DESO @ 10 DAO coin / $DESO.
		// Orders fulfilled for transferring 100 DAO coins <--> 10 $DESO.
		// Submit matching order and confirm matching happy path.

		// m1 submits order that matches m0's.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			Buying:         deso,
			Selling:        m0,
			Price:          10,
			Quantity:       10,
			OrderBookDelta: -1,
			CoinDeltas: map[string]map[string]int{
				m0.Name: {deso.Name: -10, m0.Name: 100},
				m1.Name: {deso.Name: 10, m0.Name: -100},
			},
		})
		require.NoError(err)
	}
	{
		// Scenario: partially fulfilled orders sorting by best price
		// m1 submits order buying 20 $DESO nanos @ 11 DAO coin / $DESO.
		// m1 submits order buying 5 $DESO nanos @ 12 DAO coin / $DESO.
		// m1 submits order buying 5 $DESO nanos @ 12 DAO coin / $DESO.
		// m0 submits order buying 240 DAO coin nanos @ 1/8 $DESO / DAO coin.
		// m0's order is fully fulfilled.
		// m1's orders are partially fulfilled for:
		//   * 5 $DESO @ 12 DAO coin / $DESO (fully fulfilled)
		//   * 5 $DESO @ 12 DAO coin / $DESO (full fulfilled)
		//   * 10 $DESO @ 11 DAO coin / $DESO (partially fulfilled)

		// m1 submits order buying 20 $DESO @ 11 DAO coin / $DESO.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			Buying:         deso,
			Selling:        m0,
			Price:          11,
			Quantity:       20,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		// m1 submits order buying 5 $DESO nanos @ 12 DAO coin / $DESO.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			Buying:         deso,
			Selling:        m0,
			Price:          12,
			Quantity:       5,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		// m1 submits order buying 5 $DESO nanos @ 12 DAO coin / $DESO.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			Buying:         deso,
			Selling:        m0,
			Price:          12,
			Quantity:       5,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		// m0 submits order buying 240 DAO coin units @ 1/8 $DESO / DAO coin.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			Buying:         m0,
			Selling:        deso,
			Price:          float64(1) / float64(8),
			Quantity:       240,
			OrderBookDelta: -2,
			CoinDeltas: map[string]map[string]int{
				m0.Name: {deso.Name: -20, m0.Name: 240},
				m1.Name: {deso.Name: 20, m0.Name: -240},
			},
		})
		require.NoError(err)
	}
	{
		// Scenario: cancel an open order.
		// m1 tries to cancel non-existent order. Fails.
		// m0 tries to cancel m1's order. Fails.
		// m1 cancels their open order. Succeeds.

		// m1 tries to cancel non-existent order.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:    m1,
			CancelOrderID: NewBlockHash(uint256.NewInt().SetUint64(1).Bytes()),
		})
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderToCancelNotFound)

		// m0 tries to cancel m1's order.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:    m0,
			CancelOrderID: testMeta.OrderBook()[0].OrderID,
		})
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderToCancelNotYours)

		// m1 cancels their open order.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			CancelOrderID:  testMeta.OrderBook()[0].OrderID,
			OrderBookDelta: -1,
		})
		require.NoError(err)
	}
	{
		// Scenario: user sells DAO coins for $DESO, but is able to find a good matching
		// order such that they receive/buy the same amount of $DESO by selling a lower
		// quantity of DAO coins than they intended. This is expected behavior.

		// m0 submits order buying 100 DAO coin units @ 10 $DESO / DAO coin.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			Buying:         m0,
			Selling:        deso,
			Price:          10,
			Quantity:       100,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		// m1 submits order selling 50 DAO coin units @ 5 $DESO / DAO coin.
		// m0's order is partially fulfilled with 75 coins remaining. m1's order is fully
		// fulfilled. Note that he gets his full amount of $DESO but sells only 25 of the
		// 50 DAO coin units he intended to. This is expected behavior at the moment. We
		// specify a buying quantity but allow the selling quantity to vary depending on
		// the best offer(s) available.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor: m1,
			Buying:     deso,
			Selling:    m0,
			Price:      0.2,
			Quantity:   250,
			CoinDeltas: map[string]map[string]int{
				m0.Name: {deso.Name: -250, m0.Name: 25},
				m1.Name: {deso.Name: 250, m0.Name: -25},
			},
		})
		require.NoError(err)

		// m0 cancels the remainder of his order.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			CancelOrderID:  testMeta.OrderBook()[0].OrderID,
			OrderBookDelta: -1,
		})
		require.NoError(err)
	}
	{
		// Scenario: m0 and m1 both submit identical orders. Both orders are stored.

		// m0 submits order buying 100 DAO coin units @ 0.1 $DESO / DAO coin.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			Buying:         m0,
			Selling:        deso,
			Price:          0.1,
			Quantity:       100,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		// m1 submits order buying 100 DAO coins @ 0.1 $DESO / DAO coin.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			Buying:         m0,
			Selling:        deso,
			Price:          0.1,
			Quantity:       100,
			OrderBookDelta: 1,
		})
		require.NoError(err)
	}
	{
		// Scenario: non-matching order.

		// m0 cancels their order.
		orderEntries, err := dbAdapter.GetAllDAOCoinLimitOrdersForThisTransactor(m0.PKID)
		require.NoError(err)
		require.Len(orderEntries, 1)
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			CancelOrderID:  orderEntries[0].OrderID,
			OrderBookDelta: -1,
		})
		require.NoError(err)

		// Confirm 1 existing order from m1.
		require.Len(testMeta.OrderBook(), 1)
		require.True(testMeta.OrderBook()[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m1, Buying: m0, Selling: deso, Price: 0.1, Quantity: 100,
		}))

		// m0 submits order for a worse exchange rate than m1 is willing to accept.
		// Doesn't match m1's order. Stored instead.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			Buying:         deso,
			Selling:        m0,
			Price:          9,
			Quantity:       100,
			OrderBookDelta: 1,
		})
		require.NoError(err)
	}
	{
		// Scenario: m1 submits order matching their own order. Fails.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor: m1, Buying: deso, Selling: m0, Price: 10, Quantity: 100,
		})
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderMatchingOwnOrder)
	}
	{
		// Cancel order with insufficient funds to cover the order.

		// Just a reminder of m0's current balance of their own DAO Coins
		m0BalanceNanos := testMeta.GetDAOCoinBalanceNanos(m0, m0)
		require.Equal(m0BalanceNanos.Uint64(), uint64(7365))

		// m0 transfers away some of their DAO coin such that they no longer have 100 nanos (to cover their order).
		testMeta.TransferDAOCoins(m0, m0, m2, m0BalanceNanos.Uint64()-1)
		require.Equal(testMeta.GetDAOCoinBalanceNanos(m0, m0).Uint64(), uint64(1))

		orderEntries := testMeta.OrderBook()
		require.Len(orderEntries, 2)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: deso, Selling: m0, Price: 9, Quantity: 100,
		}))
		require.True(orderEntries[1].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m1, Buying: m0, Selling: deso, Price: 0.1, Quantity: 100,
		}))

		// m0 cancels their order.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			CancelOrderID:  orderEntries[0].OrderID,
			OrderBookDelta: -1,
		})
		require.NoError(err)

		// Before we transfer the DAO coins back to m0, let's create an order for m2 that is slightly better
		// than m0's order. We'll have m1 submit an order that matches this later.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m2,
			Buying:         deso,
			Selling:        m0,
			Price:          9.5,
			Quantity:       100,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		// Okay let's transfer the DAO coins back to m0 and recreate the order
		testMeta.TransferDAOCoins(m0, m2, m0, 7339)

		// m0 resubmits their order.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			Buying:         deso,
			Selling:        m0,
			Price:          9,
			Quantity:       100,
			OrderBookDelta: 1,
		})
		require.NoError(err)
	}
	{
		// m1 submits an order that would match both m0 and m2's order. We expect to see m2's order cancelled
		// and m0's order filled as m2 doesn't have sufficient DAO coins to cover the order they placed.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			Buying:         m0,
			Selling:        deso,
			Price:          float64(1) / float64(8),
			Quantity:       100,
			OrderBookDelta: -1,
			CoinDeltas: map[string]map[string]int{
				m0.Name: {deso.Name: 11, m0.Name: -100},
				m1.Name: {deso.Name: -11, m0.Name: 100},
			},
		})
		require.NoError(err)

		// Confirm m2's order was deleted.
		orderEntries, err := dbAdapter.GetAllDAOCoinLimitOrdersForThisTransactor(m2.PKID)
		require.NoError(err)
		require.Empty(orderEntries)
	}
	{
		// Let's start fresh and mint some DAO coins for m1
		testMeta.CreateProfile(m1)
		testMeta.MintDAOCoins(m1, 1e15)            // Mint 1e15 nanos for m1 DAO coin
		testMeta.TransferDAOCoins(m1, m1, m2, 1e4) // Transfer 10K nanos to m2
	}
	{
		// m1 and m2 submit orders to SELL m1 DAO Coin
		// Sell DAO @ 5 DAO / DESO, up to 10 DESO. Max DAO = 50
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			Buying:         deso,
			Selling:        m1,
			Price:          5,
			Quantity:       10,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		// Sell DAO @ 2 DAO / DESO, up to 5 DESO. Max DAO = 10
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m2,
			Buying:         deso,
			Selling:        m1,
			Price:          2,
			Quantity:       5,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		orders, err := dbAdapter.GetAllDAOCoinLimitOrdersForThisDAOCoinPair(deso.PKID, m1.PKID)
		require.NoError(err)
		require.Len(orders, 2)
	}
	{
		// m0 submits order to buy m1 DAO Coin that matches

		// Sell DESO @ 1 DESO / DAO for up to 100 DAO coins. Max DESO: 100 DESO
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			Buying:         m1,
			Selling:        deso,
			Price:          1,
			Quantity:       300,
			OrderBookDelta: -1,
			CoinDeltas: map[string]map[string]int{
				m0.Name: {deso.Name: -15, m1.Name: 60},
				m1.Name: {deso.Name: 10, m1.Name: -50},
				m2.Name: {deso.Name: 5, m1.Name: -10},
			},
		})
		require.NoError(err)

		orderEntries, err := dbAdapter.GetAllDAOCoinLimitOrdersForThisDAOCoinPair(&ZeroPKID, m1.PKID)
		require.NoError(err)
		require.Empty(orderEntries)

		orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrdersForThisDAOCoinPair(m1.PKID, &ZeroPKID)
		require.NoError(err)
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: m1, Selling: deso, Price: 1, Quantity: 240,
		}))
	}
	{
		// Test get all DAO coin limit orders.
		// TODO: y is this weird?
		orderEntries, err := utxoView._getAllDAOCoinLimitOrders()
		require.NoError(err)
		require.Len(orderEntries, 4)

		// Test get all DAO coin limit orders for this DAO coin pair.
		orderEntries, err = utxoView.GetAllDAOCoinLimitOrdersForThisDAOCoinPair(m0.PKID, deso.PKID)
		require.NoError(err)
		require.Len(orderEntries, 2)

		// Test get all DAO coin limit orders for this transactor.
		orderEntries, err = utxoView.GetAllDAOCoinLimitOrdersForThisTransactor(m1.PKID)
		require.NoError(err)
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m1, Buying: m0, Selling: deso, Price: 0.1, Quantity: 100,
		}))

		// Test get matching DAO coin limit orders.
		queryEntry := testMeta.ToOrderEntry(DAOCoinLimitOrderTestInput{
			Transactor: m1, Buying: deso, Selling: m1, Price: 0.9, Quantity: 100,
		})
		orderEntries, err = utxoView._getNextLimitOrdersToFill(queryEntry, nil)
		require.NoError(err)
		require.Empty(orderEntries)
		queryEntry.ScaledExchangeRateCoinsToSellPerCoinToBuy, err = CalculateScaledExchangeRate(1.1)
		require.NoError(err)
		orderEntries, err = utxoView._getNextLimitOrdersToFill(queryEntry, nil)
		require.NoError(err)
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: m1, Selling: deso, Price: 1, Quantity: 240,
		}))

		// m0 submits another order slightly better than previous.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			Buying:         m1,
			Selling:        deso,
			Price:          1.05,
			Quantity:       110,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		// Test get matching DAO coin limit orders.
		// Query with identical order as before. Should match m0's new + better order.
		orderEntries, err = utxoView._getNextLimitOrdersToFill(queryEntry, nil)
		require.NoError(err)
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: m1, Selling: deso, Price: 1.05, Quantity: 110,
		}))

		// Test get matching DAO coin limit orders.
		// Query with identical order as before but higher quantity.
		// Should match both of m0's orders with better listed first.
		queryEntry.QuantityToFillInBaseUnits = uint256.NewInt().SetUint64(150)
		orderEntries, err = utxoView._getNextLimitOrdersToFill(queryEntry, nil)
		require.NoError(err)
		require.Len(orderEntries, 2)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: m1, Selling: deso, Price: 1.05, Quantity: 110,
		}))
		require.True(orderEntries[1].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: m1, Selling: deso, Price: 1, Quantity: 240,
		}))
	}
	{
		// Scenario: ASK orders

		// Check what open DAO coin limit orders are in the order book.
		orderEntries := testMeta.OrderBook()
		require.Len(orderEntries, 4)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: deso, Selling: m0, Price: 9, Quantity: 89,
		}))
		require.True(orderEntries[1].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m1, Buying: m0, Selling: deso, Price: 0.1, Quantity: 100,
		}))
		require.True(orderEntries[2].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: m1, Selling: deso, Price: 1, Quantity: 240,
		}))
		require.True(orderEntries[3].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: m1, Selling: deso, Price: 1.05, Quantity: 110,
		}))

		// m1 cancels open order.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			CancelOrderID:  orderEntries[1].OrderID,
			OrderBookDelta: -1,
		})
		require.NoError(err)

		// m0 has 3 open orders.
		orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrdersForThisTransactor(m0.PKID)
		require.NoError(err)
		require.Len(orderEntries, 3)

		// m1 has zero open orders.
		orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrdersForThisTransactor(m1.PKID)
		require.NoError(err)
		require.Empty(orderEntries)

		// m1 submits ASK order selling m1 DAO coins that is fulfilled by m0's open limit orders.
		// Transactor: m0, Buying: m1, Selling: deso, Price: 1, Quantity: 240
		// Transactor: m0, Buying: m1, Selling: deso, Price: 1.05, Quantity: 110
		// 110 DAO coin base units transferred @ 1.05 $DESO per DAO coin.
		//  50 DAO coin base units transferred @ 1.0  $DESO per DAO coin.
		// TOTAL = 160 DAO coin base units transferred, 165 $DESO transferred.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			Buying:         deso,
			Selling:        m1,
			Price:          1,
			Quantity:       160,
			OperationType:  DAOCoinLimitOrderOperationTypeASK,
			OrderBookDelta: -1,
			CoinDeltas: map[string]map[string]int{
				m0.Name: {deso.Name: -165, m1.Name: 160},
				m1.Name: {deso.Name: 165, m1.Name: -160},
			},
		})
		require.NoError(err)

		// m0 has 2 remaining open orders.
		orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrdersForThisTransactor(m0.PKID)
		require.NoError(err)
		require.Len(orderEntries, 2)

		// m1 submits ASK order selling m1 DAO coins that fulfills m0's open limit order.
		// Transactor: m0, Buying: m1, Selling:  $, Price: 1, Quantity: 200
		// m1 would be ok selling 1.2 DAO coins / $DESO.
		// m0 has a better offer willing to buy 1.0 DAO coins / $DESO.
		// 190 DAO coin base units transferred @ 1.0  $DESO per DAO coin.
		// TOTAL = 190 DAO coin base units transferred, 190 $DESO transferred.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:    m1,
			Buying:        deso,
			Selling:       m1,
			Price:         1.2,
			Quantity:      250,
			OperationType: DAOCoinLimitOrderOperationTypeASK,
			CoinDeltas: map[string]map[string]int{
				m0.Name: {deso.Name: -190, m1.Name: 190},
				m1.Name: {deso.Name: 190, m1.Name: -190},
			},
		})
		require.NoError(err)

		// m1's limit order is left open with 60 DAO coin base units left to be fulfilled.
		// m0 has 1 remaining open orders.
		orderEntries = testMeta.OrderBook()
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: deso, Selling: m0, Price: 9, Quantity: 89,
		}))
		require.True(orderEntries[1].Eq(DAOCoinLimitOrderTestInput{
			Transactor:    m1,
			Buying:        deso,
			Selling:       m1,
			Price:         1.2,
			Quantity:      60,
			OperationType: DAOCoinLimitOrderOperationTypeASK,
		}))
	}
	{
		// Scenario: matching orders buying/selling m0 DAO coin <--> m1 DAO coin

		// Confirm no existing orders for m0 DAO coin <--> m1 DAO coin.
		orderEntries, err := dbAdapter.GetAllDAOCoinLimitOrdersForThisDAOCoinPair(m0.PKID, m1.PKID)
		require.NoError(err)
		require.Empty(orderEntries)
		orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrdersForThisDAOCoinPair(m1.PKID, m0.PKID)
		require.NoError(err)
		require.Empty(orderEntries)

		// m0 submits BID order buying m1 coins and selling m0 coins.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			Buying:         m1,
			Selling:        m0,
			Price:          0.5,
			Quantity:       200,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		// m1 submits BID order buying m0 coins and selling m1 coins.
		// Orders match for 100 m0 DAO coin units <--> 200 m1 DAO coin units.
		// Orders match fully so m0's order is removed from the order book.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			Buying:         m0,
			Selling:        m1,
			Price:          2,
			Quantity:       100,
			OrderBookDelta: -1,
			CoinDeltas: map[string]map[string]int{
				m0.Name: {m0.Name: -100, m1.Name: 200},
				m1.Name: {m0.Name: 100, m1.Name: -200},
			},
		})
		require.NoError(err)
	}
	{
		// Scenario: matching 2 orders from 2 different users selling DAO coins.

		// Confirm existing orders in the order book.
		orderEntries := testMeta.OrderBook()
		require.Len(orderEntries, 2)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: deso, Selling: m0, Price: 9, Quantity: 89,
		}))
		require.True(orderEntries[1].Eq(DAOCoinLimitOrderTestInput{
			Transactor:    m1,
			Buying:        deso,
			Selling:       m1,
			Price:         1.2,
			Quantity:      60,
			OperationType: DAOCoinLimitOrderOperationTypeASK,
		}))

		// m0 submits an order selling m1 DAO coins.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			Buying:         deso,
			Selling:        m1,
			Price:          1.1,
			Quantity:       50,
			OperationType:  DAOCoinLimitOrderOperationTypeASK,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		// m2 submits an order buying m1 DAO coins fulfilled by m0 and m1's open ASK orders.
		// 60 DAO coin units were transferred from m1 to m2 in exchange for 50 $DESO nanos.
		// 50 DAO coin units were transferred from m0 to m2 in exchange for 45 $DESO nanos.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m2,
			Buying:         m1,
			Selling:        deso,
			Price:          1,
			Quantity:       110,
			OrderBookDelta: -2,
			CoinDeltas: map[string]map[string]int{
				m0.Name: {deso.Name: 45, m1.Name: -50},
				m1.Name: {deso.Name: 50, m1.Name: -60},
				m2.Name: {deso.Name: -95, m1.Name: 110},
			},
		})
		require.NoError(err)
	}
	{
		// Scenario: matching 2 orders from 2 different users buying DAO coins.

		// Confirm existing orders in order book.
		orderEntries, err := dbAdapter.GetAllDAOCoinLimitOrders()
		require.NoError(err)
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: deso, Selling: m0, Price: 9, Quantity: 89,
		}))

		// m0 submits an order buying m1 DAO coins. Order is stored.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			Buying:         m1,
			Selling:        deso,
			Price:          0.1,
			Quantity:       300,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		// m1 submits an order buying m1 DAO coins. Order is stored.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			Buying:         m1,
			Selling:        deso,
			Price:          0.2,
			Quantity:       600,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		// m2 submits an order selling m1 DAO coins.
		// Orders match and are removed from the order book.
		// 600 DAO coin units were transferred from m2 to m1 in exchange for 120 $DESO nanos.
		// 300 DAO coin units were transferred from m2 to m0 in exchange for 30 $DESO nanos.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m2,
			Buying:         deso,
			Selling:        m1,
			Price:          12,
			Quantity:       900,
			OperationType:  DAOCoinLimitOrderOperationTypeASK,
			OrderBookDelta: -2,
			CoinDeltas: map[string]map[string]int{
				m0.Name: {deso.Name: -30, m1.Name: 300},
				m1.Name: {deso.Name: -120, m1.Name: 600},
				m2.Name: {deso.Name: 150, m1.Name: -900},
			},
		})
		require.NoError(err)
	}
	{
		// Scenario: trying to modify FeeNanos up or down

		// Confirm existing orders in the order book.
		orderEntries := testMeta.OrderBook()
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: deso, Selling: m0, Price: 9, Quantity: 89,
		}))

		// m1 submits an order which should match to m0, but we'll modify the FeeNanos.
		testInput := DAOCoinLimitOrderTestInput{
			Transactor: m1,
			Buying:     m0,
			Selling:    deso,
			Price:      0.2,
			Quantity:   89,
		}

		// Confirm m1's order would match to m0.
		orderEntries, err = dbAdapter.GetMatchingDAOCoinLimitOrders(
			testMeta.ToOrderEntry(testInput), nil)
		require.NoError(err)
		require.Len(orderEntries, 1)

		// Construct txn.
		currentTxn, totalInput, _, err := testMeta.CreateOrderTxn(testInput)
		require.NoError(err)
		txnMeta := currentTxn.TxnMeta.(*DAOCoinLimitOrderMetadata)

		// Modify FeeNanos to zero $DESO and try to connect. Errors.
		originalFeeNanos := txnMeta.FeeNanos
		require.True(originalFeeNanos > uint64(0))
		txnMeta.FeeNanos = uint64(0)
		err = testMeta.ConnectOrderTxn(testInput, currentTxn, totalInput)
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderFeeNanosBelowMinTxFee)

		// Modify FeeNanos down and try to connect. Errors.
		txnMeta.FeeNanos, err = SafeUint64().Div(originalFeeNanos, 2)
		require.NoError(err)
		err = testMeta.ConnectOrderTxn(testInput, currentTxn, totalInput)
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderFeeNanosBelowMinTxFee)

		// Modify FeeNanos up and try to connect. Errors.
		txnMeta.FeeNanos = originalFeeNanos + uint64(1)
		err = testMeta.ConnectOrderTxn(testInput, currentTxn, totalInput)
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderOverspendingDESO)

		// Confirm no new orders in the order book.
		orderEntries = testMeta.OrderBook()
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: deso, Selling: m0, Price: 9, Quantity: 89,
		}))
	}
	{
		// Scenario: unused bidder inputs get refunded

		// Confirm existing orders in the order book.
		orderEntries := testMeta.OrderBook()
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: deso, Selling: m0, Price: 9, Quantity: 89,
		}))

		// m1 submits an order to which we'll add additional BidderInputs.
		testInput := DAOCoinLimitOrderTestInput{
			Transactor: m1, Buying: m1, Selling: deso, Price: 0.1, Quantity: 10,
		}

		// Construct transaction. Note: we double the feeRateNanosPerKb here so that we can
		// modify the transaction after construction and have enough inputs to cover the fee.
		testMeta.FeeRateNanosPerKb = testMeta.FeeRateNanosPerKb * 2
		currentTxn, totalInput, _, err := testMeta.CreateOrderTxn(testInput)
		require.NoError(err)
		testMeta.FeeRateNanosPerKb = testMeta.FeeRateNanosPerKb / 2
		txnMeta := currentTxn.TxnMeta.(*DAOCoinLimitOrderMetadata)

		// Track m0's $DESO balance before/after.
		originalM0DESOBalance := testMeta.GetDESOBalanceNanos(m0)

		// Add additional BidderInput from m0.
		utxoEntriesM0, err := chain.GetSpendableUtxosForPublicKey(m0.PkBytes, mempool, nil)
		require.NoError(err)

		txnMeta.BidderInputs = append(
			[]*DeSoInputsByTransactor{},
			&DeSoInputsByTransactor{
				TransactorPublicKey: m0.PublicKey,
				Inputs:              append([]*DeSoInput{}, (*DeSoInput)(utxoEntriesM0[0].UtxoKey)),
			})

		// Connect txn.
		err = testMeta.ConnectOrderTxn(testInput, currentTxn, totalInput)
		require.NoError(err)

		// Confirm unused BidderInput UTXOs are refunded.
		updatedM0DESOBalance := testMeta.GetDESOBalanceNanos(m0)
		require.Equal(originalM0DESOBalance, updatedM0DESOBalance)

		// m1 cancels the above txn.
		orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrdersForThisTransactor(m1.PKID)
		require.NoError(err)
		require.Len(orderEntries, 1)
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			CancelOrderID:  orderEntries[0].OrderID,
			OrderBookDelta: -1,
		})
		require.NoError(err)
	}
	{
		// Scenario: invalid BidderInputs should fail

		// Confirm existing orders in the order book.
		orderEntries := testMeta.OrderBook()
		require.NoError(err)
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: deso, Selling: m0, Price: 9, Quantity: 89,
		}))

		// m0 submits order buying m1 coins. Order is stored.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			Buying:         m1,
			Selling:        deso,
			Price:          0.1,
			Quantity:       50,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		// m1 creates txn selling m1 coins that should match m0's.
		testInput := DAOCoinLimitOrderTestInput{
			Transactor:    m1,
			Buying:        deso,
			Selling:       m1,
			Price:         10,
			Quantity:      50,
			OperationType: DAOCoinLimitOrderOperationTypeASK,
		}

		currentTxn, totalInput, _, err := testMeta.CreateOrderTxn(testInput)
		require.NoError(err)
		txnMeta := currentTxn.TxnMeta.(*DAOCoinLimitOrderMetadata)

		// Confirm txn has BidderInputs from m0 as m1's
		// order would match m0 and m0 is selling $DESO.
		require.Len(txnMeta.BidderInputs, 1)
		originalBidderInput := txnMeta.BidderInputs[0]
		require.True(bytes.Equal(originalBidderInput.TransactorPublicKey.ToBytes(), m0.PkBytes))

		// m1 deletes m0's BidderInputs and tries to connect. Should error.
		txnMeta.BidderInputs = []*DeSoInputsByTransactor{}
		err = testMeta.ConnectOrderTxn(testInput, currentTxn, totalInput)
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderOverspendingDESO)

		// m1 swaps out m0's BidderInputs for their own and tries to connect. Should error.
		utxoEntriesM1, err := chain.GetSpendableUtxosForPublicKey(m1.PkBytes, mempool, nil)
		require.NoError(err)
		require.NotEmpty(utxoEntriesM1)

		txnMeta.BidderInputs = append(
			[]*DeSoInputsByTransactor{},
			&DeSoInputsByTransactor{
				TransactorPublicKey: m1.PublicKey,
				Inputs:              append([]*DeSoInput{}, (*DeSoInput)(utxoEntriesM1[0].UtxoKey)),
			})

		err = testMeta.ConnectOrderTxn(testInput, currentTxn, totalInput)
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderBidderInputNoLongerExists)

		// m1 swaps out m0's BidderInputs for m2's and tries to connect. Should error.
		utxoEntriesM2, err := chain.GetSpendableUtxosForPublicKey(m2.PkBytes, mempool, nil)
		require.NoError(err)
		require.NotEmpty(utxoEntriesM2)

		txnMeta.BidderInputs = append(
			[]*DeSoInputsByTransactor{},
			&DeSoInputsByTransactor{
				TransactorPublicKey: m2.PublicKey,
				Inputs:              append([]*DeSoInput{}, (*DeSoInput)(utxoEntriesM2[0].UtxoKey)),
			})

		err = testMeta.ConnectOrderTxn(testInput, currentTxn, totalInput)
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderOverspendingDESO)

		// m1 swaps out m0's BidderInputs for spent UTXOs
		// from m0 and tries to connect. Should error.
		utxoEntriesM0, err := chain.GetSpendableUtxosForPublicKey(m0.PkBytes, mempool, nil)
		require.NoError(err)
		require.NotEmpty(utxoEntriesM0) // Unspent UTXOs exist for m0.

		// Spend m0's existing UTXO.
		tempUtxoView, err := NewUtxoView(db, params, chain.postgres)
		require.NoError(err)
		utxoOp, err := tempUtxoView._spendUtxo(utxoEntriesM0[0].UtxoKey)
		require.NoError(err)
		err = tempUtxoView.FlushToDb()
		require.NoError(err)
		utxoEntriesM0, err = chain.GetSpendableUtxosForPublicKey(m0.PkBytes, mempool, nil)
		require.NoError(err)
		require.Empty(utxoEntriesM0) // No unspent UTXOs exist for m0.

		txnMeta.BidderInputs = append(
			[]*DeSoInputsByTransactor{},
			&DeSoInputsByTransactor{
				TransactorPublicKey: m0.PublicKey,
				Inputs:              append([]*DeSoInput{}, (*DeSoInput)(utxoOp.Key)),
			})

		err = testMeta.ConnectOrderTxn(testInput, currentTxn, totalInput)
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderBidderInputNoLongerExists)

		// Unspend m0's existing UTXO.
		err = tempUtxoView._unSpendUtxo(utxoOp.Entry)
		require.NoError(err)
		err = tempUtxoView.FlushToDb()
		require.NoError(err)
		utxoEntriesM0, err = chain.GetSpendableUtxosForPublicKey(m0.PkBytes, mempool, nil)
		require.NoError(err)
		require.NotEmpty(utxoEntriesM0) // Unspent UTXOs exist for m0.

		// m1 includes m0's BidderInputs in addition to
		// their own and tries to connect. Should error.
		bidderInputs := append([]*DeSoInputsByTransactor{}, originalBidderInput)

		bidderInputs = append(
			bidderInputs,
			&DeSoInputsByTransactor{
				TransactorPublicKey: m1.PublicKey,
				Inputs:              append([]*DeSoInput{}, (*DeSoInput)(utxoEntriesM1[0].UtxoKey)),
			})

		txnMeta.BidderInputs = bidderInputs
		err = testMeta.ConnectOrderTxn(testInput, currentTxn, totalInput)
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderFeeNanosBelowMinTxFee)

		// m1 includes m0's BidderInputs in addition to
		// m2's and tries to connect. Should error.
		bidderInputs = append([]*DeSoInputsByTransactor{}, originalBidderInput)

		bidderInputs = append(
			bidderInputs,
			&DeSoInputsByTransactor{
				TransactorPublicKey: m2.PublicKey,
				Inputs:              append([]*DeSoInput{}, (*DeSoInput)(utxoEntriesM2[0].UtxoKey)),
			})

		txnMeta.BidderInputs = bidderInputs
		err = testMeta.ConnectOrderTxn(testInput, currentTxn, totalInput)
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderFeeNanosBelowMinTxFee)

		// m1 increases fee rate and resubmits BidderInputs from m0
		// in addition to m1 and separately m2. Should still fail.
		testMeta.FeeRateNanosPerKb = testMeta.FeeRateNanosPerKb * 2
		currentTxn, totalInput, feeNanos, err := testMeta.CreateOrderTxn(testInput)
		require.NoError(err)
		testMeta.FeeRateNanosPerKb = testMeta.FeeRateNanosPerKb / 2
		txnMeta = currentTxn.TxnMeta.(*DAOCoinLimitOrderMetadata)

		// Confirm txn has BidderInputs from m0 as m1's
		// order would match m0 and m0 is selling $DESO.
		require.Len(txnMeta.BidderInputs, 1)
		originalBidderInput = txnMeta.BidderInputs[0]
		require.True(bytes.Equal(originalBidderInput.TransactorPublicKey.ToBytes(), m0.PkBytes))

		// m1 includes m0's BidderInputs in addition to
		// their own and tries to connect. Should error.
		bidderInputs = append([]*DeSoInputsByTransactor{}, originalBidderInput)

		bidderInputs = append(
			bidderInputs,
			&DeSoInputsByTransactor{
				TransactorPublicKey: m1.PublicKey,
				Inputs:              append([]*DeSoInput{}, (*DeSoInput)(utxoEntriesM1[0].UtxoKey)),
			})

		txnMeta.BidderInputs = bidderInputs
		err = testMeta.ConnectOrderTxn(testInput, currentTxn, totalInput)
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderBidderInputNoLongerExists)

		// m1 includes m0's BidderInputs in addition to
		// m2's and tries to connect, but specifies m1's
		// PK with m2's UTXO. Should error.
		bidderInputs = append([]*DeSoInputsByTransactor{}, originalBidderInput)

		bidderInputs = append(
			bidderInputs,
			&DeSoInputsByTransactor{
				// m1's public key
				TransactorPublicKey: m1.PublicKey,
				// m2's UTXO
				Inputs: append([]*DeSoInput{}, (*DeSoInput)(utxoEntriesM2[0].UtxoKey)),
			})

		txnMeta.BidderInputs = bidderInputs
		err = testMeta.ConnectOrderTxn(testInput, currentTxn, totalInput)
		require.Error(err)
		require.Contains(err.Error(), RuleErrorInputWithPublicKeyDifferentFromTxnPublicKey)

		// m1 includes m0's BidderInputs in addition to
		// m2's and tries to connect. Should pass. And
		// all unused UTXOs should be refunded.
		originalM0DESOBalance := testMeta.GetDESOBalanceNanos(m0)
		originalM1DESOBalance := testMeta.GetDESOBalanceNanos(m1)
		originalM2DESOBalance := testMeta.GetDESOBalanceNanos(m2)
		bidderInputs = append([]*DeSoInputsByTransactor{}, originalBidderInput)

		bidderInputs = append(
			bidderInputs,
			&DeSoInputsByTransactor{
				TransactorPublicKey: m2.PublicKey,
				Inputs:              append([]*DeSoInput{}, (*DeSoInput)(utxoEntriesM2[0].UtxoKey)),
			})

		txnMeta.BidderInputs = bidderInputs
		err = testMeta.ConnectOrderTxn(testInput, currentTxn, totalInput)
		require.NoError(err)

		// m0 and m1's orders match and are removed from the order book.
		orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrders()
		require.NoError(err)
		require.Len(orderEntries, 1)

		// 5 $DESO nanos are transferred from m0 to m1.
		// m2 gets refunded their unused UTXOs.
		updatedM0DESOBalance := testMeta.GetDESOBalanceNanos(m0)
		updatedM1DESOBalance := testMeta.GetDESOBalanceNanos(m1)
		updatedM2DESOBalance := testMeta.GetDESOBalanceNanos(m2)
		require.Equal(originalM0DESOBalance-uint64(5), updatedM0DESOBalance)
		require.Equal(originalM1DESOBalance+uint64(5)-feeNanos, updatedM1DESOBalance)
		require.Equal(originalM2DESOBalance, updatedM2DESOBalance)
	}
	{
		// Scenario: unused BidderInputs in DAO <--> DAO coin trade

		// Confirm existing orders in the order book.
		orderEntries := testMeta.OrderBook()
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: deso, Selling: m0, Price: 9, Quantity: 89,
		}))

		// m0 submits order buying m1 coins for m0 coins. Order is stored.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			Buying:         m1,
			Selling:        m0,
			Price:          0.1,
			Quantity:       50,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		// m1 creates txn buying m0 coins for m1 coins that should match m0's.
		testInput := DAOCoinLimitOrderTestInput{
			Transactor:    m1,
			Buying:        m0,
			Selling:       m1,
			Price:         10,
			Quantity:      50,
			OperationType: DAOCoinLimitOrderOperationTypeASK,
		}

		currentTxn, totalInput, _, err := testMeta.CreateOrderTxn(testInput)
		require.NoError(err)
		txnMeta := currentTxn.TxnMeta.(*DAOCoinLimitOrderMetadata)

		// Since this is a DAO <--> DAO coin trade,
		// no BidderInputs are specified.
		require.Empty(txnMeta.BidderInputs)

		// m1 adds BidderInputs from m0 and tries to connect. Should error.
		utxoEntriesM0, err := chain.GetSpendableUtxosForPublicKey(m0.PkBytes, mempool, utxoView)
		require.NoError(err)
		require.NotEmpty(utxoEntriesM0)

		txnMeta.BidderInputs = append(
			[]*DeSoInputsByTransactor{},
			&DeSoInputsByTransactor{
				TransactorPublicKey: m0.PublicKey,
				Inputs:              append([]*DeSoInput{}, (*DeSoInput)(utxoEntriesM0[0].UtxoKey)),
			})

		err = testMeta.ConnectOrderTxn(testInput, currentTxn, totalInput)
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderFeeNanosBelowMinTxFee)

		// m1 increases fee rate and resubmits BidderInputs from m0.
		// Should pass. And all unused UTXOs should be refunded.
		testMeta.FeeRateNanosPerKb = testMeta.FeeRateNanosPerKb * 2
		currentTxn, totalInput, _, err = testMeta.CreateOrderTxn(testInput)
		require.NoError(err)
		testMeta.FeeRateNanosPerKb = testMeta.FeeRateNanosPerKb / 2
		txnMeta = currentTxn.TxnMeta.(*DAOCoinLimitOrderMetadata)

		// Since this is a DAO <--> DAO coin trade,
		// no BidderInputs are specified.
		require.Empty(txnMeta.BidderInputs)

		// m1 adds BidderInputs from m0 and tries to connect. Should pass.
		originalM0DESOBalance := testMeta.GetDESOBalanceNanos(m0)
		utxoEntriesM0, err = chain.GetSpendableUtxosForPublicKey(m0.PkBytes, mempool, utxoView)
		require.NoError(err)
		require.NotEmpty(utxoEntriesM0)

		txnMeta.BidderInputs = append(
			[]*DeSoInputsByTransactor{},
			&DeSoInputsByTransactor{
				TransactorPublicKey: m0.PublicKey,
				Inputs:              append([]*DeSoInput{}, (*DeSoInput)(utxoEntriesM0[0].UtxoKey)),
			})

		err = testMeta.ConnectOrderTxn(testInput, currentTxn, totalInput)
		require.NoError(err)

		// m0 and m1's orders match and are removed from the order book.
		orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrders()
		require.NoError(err)
		require.Len(orderEntries, 1)

		// m0 gets refunded their unused UTXOs.
		updatedM0DESOBalance := testMeta.GetDESOBalanceNanos(m0)
		require.Equal(originalM0DESOBalance, updatedM0DESOBalance)
	}
	{
		// Scenario: FillOrKill BID market order (exchange rate = zero)

		// Confirm existing orders in the order book.
		orderEntries := testMeta.OrderBook()
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: deso, Selling: m0, Price: 9, Quantity: 89,
		}))

		// m0 submits an order selling 100 m1 DAO coin units. Order is stored.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			Buying:         deso,
			Selling:        m1,
			Price:          10,
			Quantity:       100,
			OperationType:  DAOCoinLimitOrderOperationTypeASK,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		// m1 submits an order with an invalid FillType. Errors.
		// We set the exchange rate to zero to signify this is a market order.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor: m1,
			Buying:     m1,
			Selling:    deso,
			Price:      0,
			Quantity:   200,
			FillType:   99,
		})
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderInvalidFillType)

		// m1 submits a FillOrKill order buying 200 m1 DAO coin units that is killed.
		// Order book is unchanged and no coins change hands.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor: m1,
			Buying:     m1,
			Selling:    deso,
			Price:      0,
			Quantity:   200,
			FillType:   DAOCoinLimitOrderFillTypeFillOrKill,
		})
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderFillOrKillOrderUnfulfilled)

		// m1 submits a FillOrKill order buying 100 m1 DAO coins that is filled.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			Buying:         m1,
			Selling:        deso,
			Price:          0,
			Quantity:       100,
			FillType:       DAOCoinLimitOrderFillTypeFillOrKill,
			OrderBookDelta: -1,
			CoinDeltas: map[string]map[string]int{
				m0.Name: {deso.Name: 10, m1.Name: -100},
				m1.Name: {deso.Name: -10, m1.Name: 100},
			},
		})
		require.NoError(err)
	}
	{
		// Scenario: FillOrKill ASK market order (exchange rate = zero)

		// Confirm existing orders in the order book.
		orderEntries := testMeta.OrderBook()
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: deso, Selling: m0, Price: 9, Quantity: 89,
		}))

		// m0 submits an order buying 100 m1 DAO coin units. Order is stored.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			Buying:         m1,
			Selling:        deso,
			Price:          0.1,
			Quantity:       100,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		// m1 submits a FillOrKill order selling 200 m1 DAO coin units that is killed.
		// Order book is unchanged. No coins change hands.
		// We set the exchange rate to zero to signify this is a market order.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:    m1,
			Buying:        deso,
			Selling:       m1,
			Price:         0,
			Quantity:      200,
			OperationType: DAOCoinLimitOrderOperationTypeASK,
			FillType:      DAOCoinLimitOrderFillTypeFillOrKill,
		})
		require.Error(err)
		require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderFillOrKillOrderUnfulfilled)

		// m1 submits a FillOrKill order selling 100 m1 DAO coins that is filled.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			Buying:         deso,
			Selling:        m1,
			Price:          0,
			Quantity:       100,
			OperationType:  DAOCoinLimitOrderOperationTypeASK,
			FillType:       DAOCoinLimitOrderFillTypeFillOrKill,
			OrderBookDelta: -1,
			CoinDeltas: map[string]map[string]int{
				m0.Name: {deso.Name: -10, m1.Name: 100},
				m1.Name: {deso.Name: 10, m1.Name: -100},
			},
		})
		require.NoError(err)
	}
	{
		// Scenario: ImmediateOrCancel BID market order (exchange rate = zero)

		// Confirm existing orders in the order book.
		orderEntries := testMeta.OrderBook()
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: deso, Selling: m0, Price: 9, Quantity: 89,
		}))

		// m0 submits an order selling 100 m1 DAO coin units. Order is stored.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			Buying:         deso,
			Selling:        m1,
			Price:          10,
			Quantity:       100,
			OperationType:  DAOCoinLimitOrderOperationTypeASK,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		// m1 submits an ImmediateOrCancel order buying 200 m1 DAO coins that is
		// filled for 100 DAO coin units with the remaining quantity cancelled.
		// We set the exchange rate to zero to signify this is a market order.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			Buying:         m1,
			Selling:        deso,
			Price:          0,
			Quantity:       200,
			FillType:       DAOCoinLimitOrderFillTypeImmediateOrCancel,
			OrderBookDelta: -1,
			CoinDeltas: map[string]map[string]int{
				m0.Name: {deso.Name: 10, m1.Name: -100},
				m1.Name: {deso.Name: -10, m1.Name: 100},
			},
		})
		require.NoError(err)
	}
	{
		// Scenario: ImmediateOrCancel ASK market order (exchange rate = zero)

		// Confirm existing orders in the order book.
		orderEntries := testMeta.OrderBook()
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: deso, Selling: m0, Price: 9, Quantity: 89,
		}))

		// m0 submits an order buying 100 m1 DAO coin units. Order is stored.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m0,
			Buying:         m1,
			Selling:        deso,
			Price:          0.1,
			Quantity:       100,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		// m1 submits an ImmediateOrCancel order selling 200 m1 DAO coins that is
		// filled for 100 DAO coin units with the remaining quantity cancelled.
		// We set the exchange rate to zero to signify this is a market order.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			Buying:         deso,
			Selling:        m1,
			Price:          0,
			Quantity:       200,
			OperationType:  DAOCoinLimitOrderOperationTypeASK,
			FillType:       DAOCoinLimitOrderFillTypeImmediateOrCancel,
			OrderBookDelta: -1,
			CoinDeltas: map[string]map[string]int{
				m0.Name: {deso.Name: -10, m1.Name: 100},
				m1.Name: {deso.Name: 10, m1.Name: -100},
			},
		})
		require.NoError(err)
	}
	{
		// Scenario: FillOrKill and ImmediateToCancel market orders where
		// transactor doesn't have sufficient $DESO to complete the order.

		// Confirm existing orders in the order book.
		orderEntries := testMeta.OrderBook()
		require.Len(orderEntries, 1)
		require.True(orderEntries[0].Eq(DAOCoinLimitOrderTestInput{
			Transactor: m0, Buying: deso, Selling: m0, Price: 9, Quantity: 89,
		}))

		// m1 submits an order selling all of their m1 DAO coin units for an expensive
		// price, such that m0 does not have sufficient $DESO to afford to fulfill
		// m1's order. m1's order is stored.
		originalM1BalanceM1Coins := testMeta.GetDAOCoinBalanceNanos(m1, m1)
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			Buying:         deso,
			Selling:        m1,
			Price:          0.0001,
			Quantity:       originalM1BalanceM1Coins.Uint64(),
			OperationType:  DAOCoinLimitOrderOperationTypeASK,
			OrderBookDelta: 1,
		})
		require.NoError(err)

		orderEntries = testMeta.OrderBook()
		require.Len(orderEntries, 2)
		m1OrderEntry := orderEntries[1]
		require.True(m1OrderEntry.Eq(DAOCoinLimitOrderTestInput{
			Transactor:    m1,
			Buying:        deso,
			Selling:       m1,
			Price:         0.0001,
			Quantity:      originalM1BalanceM1Coins.Uint64(),
			OperationType: DAOCoinLimitOrderOperationTypeASK,
		}))

		// Confirm that m0 cannot afford to fulfill m1's order.
		originalM0DESOBalance := testMeta.GetDESOBalanceNanos(m0)
		m1RequestedDESONanos, err := m1OrderEntry.BaseUnitsToBuyUint256()
		require.NoError(err)
		require.True(m1RequestedDESONanos.Gt(uint256.NewInt().SetUint64(originalM0DESOBalance)))

		// m0 submits a FillOrKill market order trying to fulfill m1's order.
		// m0 does not have sufficient $DESO.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor: m0,
			Buying:     m1,
			Selling:    deso,
			Price:      0,
			Quantity:   originalM1BalanceM1Coins.Uint64(),
			FillType:   DAOCoinLimitOrderFillTypeFillOrKill,
		})
		require.Error(err)
		require.Contains(err.Error(), "AddInputsAndChangeToTransaction: Sanity check failed")

		// m0 submits a ImmediateOrCancel market order trying to fulfill m1's order.
		// m0 does not have sufficient $DESO. No coins change hands.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor: m0,
			Buying:     m1,
			Selling:    deso,
			Price:      0,
			Quantity:   originalM1BalanceM1Coins.Uint64(),
			FillType:   DAOCoinLimitOrderFillTypeImmediateOrCancel,
		})
		require.Error(err)
		require.Contains(err.Error(), "AddInputsAndChangeToTransaction: Sanity check failed")

		// m1 cancels their order.
		err = testMeta.SubmitOrder(DAOCoinLimitOrderTestInput{
			Transactor:     m1,
			CancelOrderID:  m1OrderEntry.OrderID,
			OrderBookDelta: -1,
		})
		require.NoError(err)
		require.Len(testMeta.OrderBook(), 1)
	}

	//{
	//	// Scenario: FillOrKill and ImmediateToCancel market orders where transactor
	//	// doesn't have sufficient selling DAO coins to complete the order. Errors.
	//
	//	// Confirm existing orders in the order book.
	//	// transactor: m0, buying:  $, selling: m0, price: 9, quantity: 89, type: BID
	//	orderEntries, err := dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//
	//	// m1 submits an order selling all of their m1 DAO coin units for an expensive
	//	// price, such that m0 does not have sufficient m0 DAO coin units to afford to
	//	// fulfill m1's order. m1's order is stored.
	//	exchangeRate, err := CalculateScaledExchangeRate(0.0001)
	//	require.NoError(err)
	//	originalM1BalanceM1Coins := dbAdapter.GetBalanceEntry(m1.PKID, m1.PKID, true).BalanceNanos
	//
	//	metadataM1 = DAOCoinLimitOrderMetadata{
	//		BuyingDAOCoinCreatorPublicKey:             m0.PublicKey,
	//		SellingDAOCoinCreatorPublicKey:            m1.PublicKey,
	//		ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
	//		QuantityToFillInBaseUnits:                 &originalM1BalanceM1Coins,
	//		OperationType:                             DAOCoinLimitOrderOperationTypeASK,
	//		FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
	//	}
	//
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m1.Pub, m1.Priv, metadataM1)
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 2)
	//	m1OrderEntry := orderEntries[1]
	//	require.True(m1OrderEntry.Eq(test.ToOrderEntry("m1", metadataM1)))
	//
	//	// Track coin balances to compare later.
	//	originalM0DESOBalance := _getBalance(t, chain, mempool, m0.Pub)
	//	originalM1DESOBalance := _getBalance(t, chain, mempool, m1.Pub)
	//	originalM0BalanceM0Coins := dbAdapter.GetBalanceEntry(m0.PKID, m0.PKID, true).BalanceNanos
	//	originalM1BalanceM0Coins := dbAdapter.GetBalanceEntry(m1.PKID, m0.PKID, true).BalanceNanos
	//	originalM0BalanceM1Coins := dbAdapter.GetBalanceEntry(m0.PKID, m1.PKID, true).BalanceNanos
	//
	//	// Confirm that m0 cannot afford to fulfill m1's order.
	//	m1RequestedM0Coins, err := m1OrderEntry.BaseUnitsToBuyUint256()
	//	require.NoError(err)
	//	require.True(m1RequestedM0Coins.Gt(&originalM0BalanceM0Coins))
	//
	//	// m0 submits a FillOrKill order trying to fulfill m1's order.
	//	// m0 does not have sufficient m0 DAO coins.
	//	metadataM0 = DAOCoinLimitOrderMetadata{
	//		BuyingDAOCoinCreatorPublicKey:             m1.PublicKey,
	//		SellingDAOCoinCreatorPublicKey:            m0.PublicKey,
	//		ScaledExchangeRateCoinsToSellPerCoinToBuy: uint256.NewInt(),
	//		QuantityToFillInBaseUnits:                 metadataM0.QuantityToFillInBaseUnits,
	//		OperationType:                             DAOCoinLimitOrderOperationTypeBID,
	//		FillType:                                  DAOCoinLimitOrderFillTypeFillOrKill,
	//	}
	//
	//	_, _, _, err = _doDAOCoinLimitOrderTxn(
	//		t, chain, db, params, feeRateNanosPerKb, m0.Pub, m0.Priv, metadataM0)
	//	require.Error(err)
	//	require.Contains(err.Error(), "not enough to cover the amount they are selling")
	//
	//	// m0 submits a ImmediateOrCancel order trying to fulfill m1's order.
	//	// m0 does not have sufficient m0 DAO coins.
	//	metadataM0.FillType = DAOCoinLimitOrderFillTypeImmediateOrCancel
	//	_, _, _, err = _doDAOCoinLimitOrderTxn(
	//		t, chain, db, params, feeRateNanosPerKb, m0.Pub, m0.Priv, metadataM0)
	//	require.Error(err)
	//	require.Contains(err.Error(), "not enough to cover the amount they are selling")
	//
	//	// No coins change hands.
	//	updatedM0DESOBalance := _getBalance(t, chain, mempool, m0.Pub)
	//	updatedM1DESOBalance := _getBalance(t, chain, mempool, m1.Pub)
	//	updatedM0BalanceM0Coins := dbAdapter.GetBalanceEntry(m0.PKID, m0.PKID, true).BalanceNanos
	//	updatedM1BalanceM0Coins := dbAdapter.GetBalanceEntry(m1.PKID, m0.PKID, true).BalanceNanos
	//	updatedM0BalanceM1Coins := dbAdapter.GetBalanceEntry(m0.PKID, m1.PKID, true).BalanceNanos
	//	updatedM1BalanceM1Coins := dbAdapter.GetBalanceEntry(m1.PKID, m1.PKID, true).BalanceNanos
	//	require.Equal(originalM0DESOBalance, updatedM0DESOBalance)
	//	require.Equal(originalM1DESOBalance, updatedM1DESOBalance)
	//	require.Equal(originalM0BalanceM0Coins, updatedM0BalanceM0Coins)
	//	require.Equal(originalM1BalanceM0Coins, updatedM1BalanceM0Coins)
	//	require.Equal(originalM0BalanceM1Coins, updatedM0BalanceM1Coins)
	//	require.Equal(originalM1BalanceM1Coins, updatedM1BalanceM1Coins)
	//
	//	// m1 cancels their order.
	//	metadataM1 = DAOCoinLimitOrderMetadata{CancelOrderID: m1OrderEntry.OrderID}
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m1.Pub, m1.Priv, metadataM1)
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//}
	//
	//{
	//	// Scenario: FillOrKill and ImmediateOrCancel limit orders (exchange rate != zero)
	//
	//	// Confirm existing orders in the order book.
	//	// transactor: m0, buying:  $, selling: m0, price: 9, quantity: 89, type: BID
	//	orderEntries, err := dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//
	//	// m0 submits an order selling 100 m1 DAO coin units. Order is stored.
	//	exchangeRate, err := CalculateScaledExchangeRate(5.0)
	//	require.NoError(err)
	//
	//	metadataM0 = DAOCoinLimitOrderMetadata{
	//		BuyingDAOCoinCreatorPublicKey:             deso.PublicKey,
	//		SellingDAOCoinCreatorPublicKey:            m1.PublicKey,
	//		ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
	//		QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(100),
	//		OperationType:                             DAOCoinLimitOrderOperationTypeASK,
	//		FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
	//	}
	//
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m0.Pub, m0.Priv, metadataM0)
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 2)
	//
	//	// Track coin balances to compare later.
	//	originalM0DESOBalance := _getBalance(t, chain, mempool, m0.Pub)
	//	originalM1DESOBalance := _getBalance(t, chain, mempool, m1.Pub)
	//	originalM0BalanceM1Coins := dbAdapter.GetBalanceEntry(m0.PKID, m1.PKID, true).BalanceNanos
	//	originalM1BalanceM1Coins := dbAdapter.GetBalanceEntry(m1.PKID, m1.PKID, true).BalanceNanos
	//
	//	// m1 submits a FillOrKill order buying 50 m1 DAO coin units.
	//	// The exchange rate is such that m0's order will not match.
	//	// Order is cancelled.
	//	exchangeRate, err = CalculateScaledExchangeRate(0.1)
	//	require.NoError(err)
	//
	//	metadataM1 = DAOCoinLimitOrderMetadata{
	//		BuyingDAOCoinCreatorPublicKey:             m1.PublicKey,
	//		SellingDAOCoinCreatorPublicKey:            deso.PublicKey,
	//		ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
	//		QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(50),
	//		OperationType:                             DAOCoinLimitOrderOperationTypeBID,
	//		FillType:                                  DAOCoinLimitOrderFillTypeFillOrKill,
	//	}
	//
	//	orderEntries, err = utxoView._getNextLimitOrdersToFill(
	//		test.ToOrderEntry("m1", metadataM1), nil)
	//	require.NoError(err)
	//	require.Empty(orderEntries)
	//
	//	_, _, _, err = _doDAOCoinLimitOrderTxn(
	//		t, chain, db, params, feeRateNanosPerKb, m1.Pub, m1.Priv, metadataM1)
	//	require.Error(err)
	//	require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderFillOrKillOrderUnfulfilled)
	//
	//	// m1 submits an ImmediateOrCancel order buying 50 m1 DAO coin units.
	//	// The exchange rate is such that m0's order will not match.
	//	// Order is cancelled.
	//	metadataM1.FillType = DAOCoinLimitOrderFillTypeImmediateOrCancel
	//	orderEntries, err = utxoView._getNextLimitOrdersToFill(
	//		test.ToOrderEntry("m1", metadataM1), nil)
	//	require.NoError(err)
	//	require.Empty(orderEntries)
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m1.Pub, m1.Priv, metadataM1)
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 2)
	//
	//	// No coins change hands other than m1's gas fees
	//	// for submitting the ImmediateOrCancel order.
	//	updatedM0DESOBalance := _getBalance(t, chain, mempool, m0.Pub)
	//	updatedM1DESOBalance := _getBalance(t, chain, mempool, m1.Pub)
	//	updatedM0BalanceM1Coins := dbAdapter.GetBalanceEntry(m0.PKID, m1.PKID, true).BalanceNanos
	//	updatedM1BalanceM1Coins := dbAdapter.GetBalanceEntry(m1.PKID, m1.PKID, true).BalanceNanos
	//	require.Equal(originalM0DESOBalance, updatedM0DESOBalance)
	//	require.Equal(originalM1DESOBalance-_feeNanos(), updatedM1DESOBalance)
	//	require.Equal(originalM0BalanceM1Coins, updatedM0BalanceM1Coins)
	//	require.Equal(originalM1BalanceM1Coins, updatedM1BalanceM1Coins)
	//	originalM1DESOBalance = updatedM1DESOBalance
	//
	//	// m1 submits a FillOrKill order buying 50 m1 DAO coin units.
	//	// The exchange rate is such that m0's order will match.
	//	// Order is fulfilled.
	//	exchangeRate, err = CalculateScaledExchangeRate(0.2)
	//	require.NoError(err)
	//	metadataM1.ScaledExchangeRateCoinsToSellPerCoinToBuy = exchangeRate
	//	metadataM1.FillType = DAOCoinLimitOrderFillTypeFillOrKill
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m1.Pub, m1.Priv, metadataM1)
	//
	//	// m1 submits an ImmediateOrCancel order buying 50 m1 DAO coin units.
	//	// The exchange rate is such that m0's order will match.
	//	// Order is fulfilled.
	//	metadataM1.FillType = DAOCoinLimitOrderFillTypeImmediateOrCancel
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m1.Pub, m1.Priv, metadataM1)
	//
	//	// m0's order is fulfilled.
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//
	//	// Correct coins change hands.
	//	updatedM0DESOBalance = _getBalance(t, chain, mempool, m0.Pub)
	//	updatedM1DESOBalance = _getBalance(t, chain, mempool, m1.Pub)
	//	updatedM0BalanceM1Coins = dbAdapter.GetBalanceEntry(m0.PKID, m1.PKID, true).BalanceNanos
	//	updatedM1BalanceM1Coins = dbAdapter.GetBalanceEntry(m1.PKID, m1.PKID, true).BalanceNanos
	//	require.Equal(originalM0DESOBalance+uint64(20), updatedM0DESOBalance)
	//	require.Equal(originalM1DESOBalance-uint64(20)-_feeNanos()-_feeNanos(), updatedM1DESOBalance)
	//	require.Equal(originalM0BalanceM1Coins.Uint64()-uint64(100), updatedM0BalanceM1Coins.Uint64())
	//	require.Equal(originalM1BalanceM1Coins.Uint64()+uint64(100), updatedM1BalanceM1Coins.Uint64())
	//}
	//
	//{
	//	// Scenario: sell all $DESO in limit order, smaller amount
	//
	//	// Confirm existing orders in the order book.
	//	// transactor: m0, buying:  $, selling: m0, price: 9, quantity: 89, type: BID
	//	orderEntries, err := dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//
	//	// Confirm m4 only owns 100 $DESO nanos. We will construct
	//	// a trade where m4 sells all of their $DESO. We save some
	//	// $DESO for fees. Here, we assume that the fee for m4's
	//	// txn will be the same for the previous txn.
	//	originalM4DESONanos := _getBalance(t, chain, mempool, m4.Pub)
	//	require.Equal(originalM4DESONanos, uint64(100))
	//	m4QuantityToFill := uint256.NewInt().SetUint64(originalM4DESONanos - _feeNanos())
	//
	//	// m0 submits an order selling m1 DAO coin units for $DESO. Order is stored.
	//	exchangeRate, err := CalculateScaledExchangeRate(1.0)
	//	require.NoError(err)
	//	m0QuantityToFill, err := SafeUint256().Mul(m4QuantityToFill, uint256.NewInt().SetUint64(2))
	//	require.NoError(err)
	//
	//	metadataM0 = DAOCoinLimitOrderMetadata{
	//		BuyingDAOCoinCreatorPublicKey:             deso.PublicKey,
	//		SellingDAOCoinCreatorPublicKey:            m1.PublicKey,
	//		ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
	//		QuantityToFillInBaseUnits:                 m0QuantityToFill,
	//		OperationType:                             DAOCoinLimitOrderOperationTypeASK,
	//		FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
	//	}
	//
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m0.Pub, m0.Priv, metadataM0)
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 2)
	//	require.True(orderEntries[1].Eq(test.ToOrderEntry("m0", metadataM0)))
	//
	//	// m4 submits a BID order buying m1 DAO coins for $DESO.
	//	exchangeRate, err = CalculateScaledExchangeRate(1.0)
	//	require.NoError(err)
	//
	//	metadataM4 := DAOCoinLimitOrderMetadata{
	//		BuyingDAOCoinCreatorPublicKey:             m1.PublicKey,
	//		SellingDAOCoinCreatorPublicKey:            deso.PublicKey,
	//		ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
	//		OperationType: DAOCoinLimitOrderOperationTypeBID,
	//		FillType:      DAOCoinLimitOrderFillTypeGoodTillCancelled,
	//	}
	//
	//	// m4 submits an order buying m1 coins and selling more $DESO than they have.
	//	metadataM4.QuantityToFillInBaseUnits, err = SafeUint256().Add(
	//		m4QuantityToFill, uint256.NewInt().SetUint64(1))
	//	require.NoError(err)
	//	_, _, _, err = _doDAOCoinLimitOrderTxn(
	//		t, chain, db, params, feeRateNanosPerKb, m4.Pub, m4.Priv, metadataM4)
	//	require.Error(err)
	//	require.Contains(err.Error(), "not sufficient to cover the spend amount")
	//
	//	// m4 submits an order buying m1 coins and selling all of their $DESO.
	//	metadataM4.QuantityToFillInBaseUnits = m4QuantityToFill
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m4.Pub, m4.Priv, metadataM4)
	//
	//	// Confirm m4 has zero $DESO left over.
	//	updatedM4DESONanos := _getBalance(t, chain, mempool, m4.Pub)
	//	require.Zero(updatedM4DESONanos)
	//
	//	// m0 cancels the remainder of their order.
	//	metadataM0 = DAOCoinLimitOrderMetadata{CancelOrderID: orderEntries[1].OrderID}
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m0.Pub, m0.Priv, metadataM0)
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//}
	//
	//{
	//	// Scenario: sell all $DESO in limit order, larger amount
	//
	//	// Confirm existing orders in the order book.
	//	// transactor: m0, buying:  $, selling: m0, price: 9, quantity: 89, type: BID
	//	orderEntries, err := dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//
	//	// m1 submits order selling m1 DAO coins.
	//	exchangeRate, err := CalculateScaledExchangeRate(1.0)
	//	require.NoError(err)
	//	m4QuantityToFill := uint256.NewInt().SetUint64(5 * NanosPerUnit)
	//	m1QuantityToFill, err := SafeUint256().Mul(m4QuantityToFill, uint256.NewInt().SetUint64(2))
	//
	//	metadataM1 = DAOCoinLimitOrderMetadata{
	//		BuyingDAOCoinCreatorPublicKey:             deso.PublicKey,
	//		SellingDAOCoinCreatorPublicKey:            m1.PublicKey,
	//		ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
	//		QuantityToFillInBaseUnits:                 m1QuantityToFill,
	//		OperationType:                             DAOCoinLimitOrderOperationTypeASK,
	//		FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
	//	}
	//
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m1.Pub, m1.Priv, metadataM1)
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 2)
	//	require.True(orderEntries[1].Eq(test.ToOrderEntry("m1", metadataM1)))
	//
	//	// Transfer 5 $DESO to m4 (plus enough to cover fees).
	//	// We assume m4's txn fee will be the same as the prev txn.
	//	feeNanos := _feeNanos()
	//	testMeta.expectedSenderBalances = append(
	//		testMeta.expectedSenderBalances, _getBalance(t, chain, mempool, senderPkString))
	//	currentOps, currentTxn, _ := _doBasicTransferWithViewFlush(
	//		t, chain, db, params, senderPkString, m4.Pub, senderPrivString,
	//		m4QuantityToFill.Uint64()+feeNanos, feeRateNanosPerKb)
	//	testMeta.txnOps = append(testMeta.txnOps, currentOps)
	//	testMeta.txns = append(testMeta.txns, currentTxn)
	//	originalM4DESONanos := _getBalance(t, chain, mempool, m4.Pub)
	//	require.Equal(originalM4DESONanos, m4QuantityToFill.Uint64()+feeNanos)
	//
	//	// m4 submits a BID limit order buying m1 DAO coins for $DESO.
	//	exchangeRate, err = CalculateScaledExchangeRate(1.0)
	//	require.NoError(err)
	//
	//	metadataM4 := DAOCoinLimitOrderMetadata{
	//		BuyingDAOCoinCreatorPublicKey:             m1.PublicKey,
	//		SellingDAOCoinCreatorPublicKey:            deso.PublicKey,
	//		ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
	//		OperationType: DAOCoinLimitOrderOperationTypeBID,
	//		FillType:      DAOCoinLimitOrderFillTypeGoodTillCancelled,
	//	}
	//
	//	// Confirm m4's order is a limit order.
	//	require.False(test.ToOrderEntry("m4", metadataM4).IsMarketOrder())
	//
	//	// m4 submits an order buying m1 coins and selling more $DESO than they have.
	//	metadataM4.QuantityToFillInBaseUnits, err = SafeUint256().Add(
	//		m4QuantityToFill, uint256.NewInt().SetUint64(1))
	//	require.NoError(err)
	//	_, _, _, err = _doDAOCoinLimitOrderTxn(
	//		t, chain, db, params, feeRateNanosPerKb, m4.Pub, m4.Priv, metadataM4)
	//	require.Error(err)
	//	require.Contains(err.Error(), "not sufficient to cover the spend amount")
	//
	//	// m4 submits an order buying m1 coins and selling all of their $DESO.
	//	metadataM4.QuantityToFillInBaseUnits = m4QuantityToFill
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m4.Pub, m4.Priv, metadataM4)
	//
	//	// Confirm m4 has zero $DESO left over.
	//	updatedM4DESONanos := _getBalance(t, chain, mempool, m4.Pub)
	//	require.Zero(updatedM4DESONanos)
	//
	//	// m1 cancels the remainder of their order.
	//	metadataM1 = DAOCoinLimitOrderMetadata{CancelOrderID: orderEntries[1].OrderID}
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m1.Pub, m1.Priv, metadataM1)
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//}
	//
	//{
	//	// Scenario: sell all $DESO in market order, larger amount
	//
	//	// Confirm existing orders in the order book.
	//	// transactor: m0, buying:  $, selling: m0, price: 9, quantity: 89, type: BID
	//	orderEntries, err := dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//
	//	// m1 submits order selling m1 DAO coins.
	//	exchangeRate, err := CalculateScaledExchangeRate(1.0)
	//	require.NoError(err)
	//	m4QuantityToFill := uint256.NewInt().SetUint64(5 * NanosPerUnit)
	//	m1QuantityToFill, err := SafeUint256().Mul(m4QuantityToFill, uint256.NewInt().SetUint64(2))
	//
	//	metadataM1 = DAOCoinLimitOrderMetadata{
	//		BuyingDAOCoinCreatorPublicKey:             deso.PublicKey,
	//		SellingDAOCoinCreatorPublicKey:            m1.PublicKey,
	//		ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
	//		QuantityToFillInBaseUnits:                 m1QuantityToFill,
	//		OperationType:                             DAOCoinLimitOrderOperationTypeASK,
	//		FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
	//	}
	//
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m1.Pub, m1.Priv, metadataM1)
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 2)
	//	require.True(orderEntries[1].Eq(test.ToOrderEntry("m1", metadataM1)))
	//
	//	// Transfer 5 $DESO to m4 (plus enough to cover fees).
	//	// We assume m4's txn fee will be the same as the prev txn.
	//	feeNanos := _feeNanos()
	//	testMeta.expectedSenderBalances = append(
	//		testMeta.expectedSenderBalances, _getBalance(t, chain, mempool, senderPkString))
	//	currentOps, currentTxn, _ := _doBasicTransferWithViewFlush(
	//		t, chain, db, params, senderPkString, m4.Pub, senderPrivString,
	//		m4QuantityToFill.Uint64()+feeNanos, feeRateNanosPerKb)
	//	testMeta.txnOps = append(testMeta.txnOps, currentOps)
	//	testMeta.txns = append(testMeta.txns, currentTxn)
	//	originalM4DESONanos := _getBalance(t, chain, mempool, m4.Pub)
	//	require.Equal(originalM4DESONanos, m4QuantityToFill.Uint64()+feeNanos)
	//
	//	// m4 submits a BID market order buying m1 DAO coins for $DESO.
	//	metadataM4 := DAOCoinLimitOrderMetadata{
	//		BuyingDAOCoinCreatorPublicKey:             m1.PublicKey,
	//		SellingDAOCoinCreatorPublicKey:            deso.PublicKey,
	//		ScaledExchangeRateCoinsToSellPerCoinToBuy: uint256.NewInt(),
	//		OperationType: DAOCoinLimitOrderOperationTypeBID,
	//		FillType:      DAOCoinLimitOrderFillTypeFillOrKill,
	//	}
	//
	//	// Confirm m4's order is a market order.
	//	require.True(test.ToOrderEntry("m4", metadataM4).IsMarketOrder())
	//
	//	// m4 submits an order buying m1 coins and selling more $DESO than they have.
	//	metadataM4.QuantityToFillInBaseUnits, err = SafeUint256().Add(
	//		m4QuantityToFill, uint256.NewInt().SetUint64(1))
	//	require.NoError(err)
	//	_, _, _, err = _doDAOCoinLimitOrderTxn(
	//		t, chain, db, params, feeRateNanosPerKb, m4.Pub, m4.Priv, metadataM4)
	//	require.Error(err)
	//	require.Contains(err.Error(), "not sufficient to cover the spend amount")
	//
	//	// m4 submits an order buying m1 coins and selling all of their $DESO.
	//	metadataM4.QuantityToFillInBaseUnits = m4QuantityToFill
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m4.Pub, m4.Priv, metadataM4)
	//
	//	// Confirm m4 has zero $DESO left over.
	//	updatedM4DESONanos := _getBalance(t, chain, mempool, m4.Pub)
	//	require.Zero(updatedM4DESONanos)
	//
	//	// m1 cancels the remainder of their order.
	//	metadataM1 = DAOCoinLimitOrderMetadata{CancelOrderID: orderEntries[1].OrderID}
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m1.Pub, m1.Priv, metadataM1)
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//}
	//
	//{
	//	// Scenario: sell all DAO coins in limit order
	//
	//	// Confirm existing orders in the order book.
	//	// transactor: m0, buying:  $, selling: m0, price: 9, quantity: 89, type: BID
	//	orderEntries, err := dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//
	//	// m1 submits a BID limit order buying m1 DAO coins for $DESO.
	//	exchangeRate, err := CalculateScaledExchangeRate(1.0)
	//	require.NoError(err)
	//	m2QuantityToFill := &dbAdapter.GetBalanceEntry(m2.PKID, m1.PKID, true).BalanceNanos
	//	m1QuantityToFill, err := SafeUint256().Mul(m2QuantityToFill, uint256.NewInt().SetUint64(2))
	//	require.NoError(err)
	//
	//	metadataM1 = DAOCoinLimitOrderMetadata{
	//		BuyingDAOCoinCreatorPublicKey:             m1.PublicKey,
	//		SellingDAOCoinCreatorPublicKey:            deso.PublicKey,
	//		ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
	//		QuantityToFillInBaseUnits:                 m1QuantityToFill,
	//		OperationType:                             DAOCoinLimitOrderOperationTypeBID,
	//		FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
	//	}
	//
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m1.Pub, m1.Priv, metadataM1)
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 2)
	//	require.True(orderEntries[1].Eq(test.ToOrderEntry("m1", metadataM1)))
	//
	//	// m2 submits an ASK limit order selling m1 DAO coins for $DESO.
	//	exchangeRate, err = CalculateScaledExchangeRate(1.0)
	//	require.NoError(err)
	//
	//	metadataM2 := DAOCoinLimitOrderMetadata{
	//		BuyingDAOCoinCreatorPublicKey:             deso.PublicKey,
	//		SellingDAOCoinCreatorPublicKey:            m1.PublicKey,
	//		ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
	//		OperationType: DAOCoinLimitOrderOperationTypeASK,
	//		FillType:      DAOCoinLimitOrderFillTypeFillOrKill,
	//	}
	//
	//	// Confirm m2's order is a limit order.
	//	require.False(test.ToOrderEntry("m2", metadataM2).IsMarketOrder())
	//
	//	// m2 submits an order selling more DAO coins than they have.
	//	metadataM2.QuantityToFillInBaseUnits, err = SafeUint256().Add(
	//		m2QuantityToFill, uint256.NewInt().SetUint64(1))
	//	require.NoError(err)
	//	_, _, _, err = _doDAOCoinLimitOrderTxn(
	//		t, chain, db, params, feeRateNanosPerKb, m2.Pub, m2.Priv, metadataM2)
	//	require.Error(err)
	//	require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderInsufficientDAOCoinsToOpenOrder)
	//
	//	// m2 submits an order selling all the DAO coins they have.
	//	metadataM2.QuantityToFillInBaseUnits = m2QuantityToFill
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m2.Pub, m2.Priv, metadataM2)
	//
	//	// Confirm m2 has zero m1 DAO coins left over.
	//	updatedM2BalanceM1Coins := dbAdapter.GetBalanceEntry(m2.PKID, m1.PKID, true).BalanceNanos
	//	require.Zero(updatedM2BalanceM1Coins)
	//
	//	// m1 cancels the remainder of their order.
	//	metadataM1 = DAOCoinLimitOrderMetadata{CancelOrderID: orderEntries[1].OrderID}
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m1.Pub, m1.Priv, metadataM1)
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//}
	//
	//{
	//	// Scenario: sell all DAO coins in market order
	//
	//	// Confirm existing orders in the order book.
	//	// transactor: m0, buying:  $, selling: m0, price: 9, quantity: 89, type: BID
	//	orderEntries, err := dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//
	//	// m1 submits a BID limit order buying m1 DAO coins for $DESO.
	//	exchangeRate, err := CalculateScaledExchangeRate(0.01)
	//	require.NoError(err)
	//	m4QuantityToFill := &dbAdapter.GetBalanceEntry(m4.PKID, m1.PKID, true).BalanceNanos
	//	m1QuantityToFill, err := SafeUint256().Mul(m4QuantityToFill, uint256.NewInt().SetUint64(2))
	//	require.NoError(err)
	//
	//	metadataM1 = DAOCoinLimitOrderMetadata{
	//		BuyingDAOCoinCreatorPublicKey:             m1.PublicKey,
	//		SellingDAOCoinCreatorPublicKey:            deso.PublicKey,
	//		ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
	//		QuantityToFillInBaseUnits:                 m1QuantityToFill,
	//		OperationType:                             DAOCoinLimitOrderOperationTypeBID,
	//		FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
	//	}
	//
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m1.Pub, m1.Priv, metadataM1)
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 2)
	//	require.True(orderEntries[1].Eq(test.ToOrderEntry("m1", metadataM1)))
	//
	//	// Transfer 1 $DESO to m4 to cover fees in the txn below.
	//	testMeta.expectedSenderBalances = append(
	//		testMeta.expectedSenderBalances, _getBalance(t, chain, mempool, senderPkString))
	//	currentOps, currentTxn, _ := _doBasicTransferWithViewFlush(
	//		t, chain, db, params, senderPkString, m4.Pub, senderPrivString, NanosPerUnit, feeRateNanosPerKb)
	//	testMeta.txnOps = append(testMeta.txnOps, currentOps)
	//	testMeta.txns = append(testMeta.txns, currentTxn)
	//	originalM4DESONanos := _getBalance(t, chain, mempool, m4.Pub)
	//	require.Equal(originalM4DESONanos, NanosPerUnit)
	//
	//	// m4 submits an ASK market order selling m1 DAO coins for $DESO.
	//	metadataM4 := DAOCoinLimitOrderMetadata{
	//		BuyingDAOCoinCreatorPublicKey:             deso.PublicKey,
	//		SellingDAOCoinCreatorPublicKey:            m1.PublicKey,
	//		ScaledExchangeRateCoinsToSellPerCoinToBuy: uint256.NewInt(),
	//		OperationType: DAOCoinLimitOrderOperationTypeASK,
	//		FillType:      DAOCoinLimitOrderFillTypeFillOrKill,
	//	}
	//
	//	// Confirm m4's order is a market order.
	//	require.True(test.ToOrderEntry("m4", metadataM4).IsMarketOrder())
	//
	//	// m4 submits an order selling more DAO coins than they have.
	//	metadataM4.QuantityToFillInBaseUnits, err = SafeUint256().Add(
	//		m4QuantityToFill, uint256.NewInt().SetUint64(1))
	//	require.NoError(err)
	//	_, _, _, err = _doDAOCoinLimitOrderTxn(
	//		t, chain, db, params, feeRateNanosPerKb, m4.Pub, m4.Priv, metadataM4)
	//	require.Error(err)
	//	require.Contains(err.Error(), "not enough to cover the amount they are selling")
	//
	//	// m4 submits an order selling all the DAO coins they have.
	//	metadataM4.QuantityToFillInBaseUnits = m4QuantityToFill
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m4.Pub, m4.Priv, metadataM4)
	//
	//	// Confirm m4 has zero m1 DAO coins left over.
	//	updatedM4BalanceM1Coins := dbAdapter.GetBalanceEntry(m4.PKID, m1.PKID, true).BalanceNanos
	//	require.Zero(updatedM4BalanceM1Coins)
	//
	//	// m1 cancels the remainder of their order.
	//	metadataM1 = DAOCoinLimitOrderMetadata{CancelOrderID: orderEntries[1].OrderID}
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m1.Pub, m1.Priv, metadataM1)
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//}

	{
		// Scenario: matching limit order selling all of their $DESO
	}

	{
		// Scenario: matching
	}

	//{
	//	// Scenario: swapping identity
	//
	//	// Confirm existing orders in the order book.
	//	// transactor: m0, buying:  $, selling: m0, price: 9, quantity: 89, type: BID
	//	orderEntries, err := dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//
	//	// m1 submits order selling m0 DAO coins.
	//	exchangeRate, err := CalculateScaledExchangeRate(8.0)
	//	require.NoError(err)
	//
	//	metadataM1 = DAOCoinLimitOrderMetadata{
	//		BuyingDAOCoinCreatorPublicKey:             deso.PublicKey,
	//		SellingDAOCoinCreatorPublicKey:            m0.PublicKey,
	//		ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
	//		QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(100),
	//		OperationType:                             DAOCoinLimitOrderOperationTypeASK,
	//		FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
	//	}
	//
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m1.Pub, m1.Priv, metadataM1)
	//
	//	// Confirm order is added to the order book.
	//	// transactor: m0, buying:  $, selling: m0, price: 9, quantity: 89, type: BID
	//	// transactor: m1, buying:  $, selling: m0, price: 8, quantity: 100, type: ASK
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrders()
	//	require.NoError(err)
	//	require.Len(orderEntries, 2)
	//
	//	// Confirm 1 order belonging to m0.
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrdersForThisTransactor(m0.PKID)
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//
	//	// Confirm 1 order belonging to m1.
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrdersForThisTransactor(m1.PKID)
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//
	//	// Confirm 0 orders belonging to m3.
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrdersForThisTransactor(m3.PKID)
	//	require.NoError(err)
	//	require.Empty(orderEntries)
	//
	//	// Swap m0's and m3's identities.
	//	originalM0PKID := m0.PKID.NewPKID()
	//	originalM3PKID := m3.PKID.NewPKID()
	//	_swapIdentityWithTestMeta(testMeta, feeRateNanosPerKb, paramUpdaterPub, paramUpdaterPriv, m0.PkBytes, m3.PkBytes)
	//	m0.PKID = dbAdapter.GetPKIDForPublicKey(m0.PkBytes)
	//	m3.PKID = dbAdapter.GetPKIDForPublicKey(m3.PkBytes)
	//	require.True(m0.PKID.Eq(originalM3PKID))
	//	require.True(m3.PKID.Eq(originalM0PKID))
	//
	//	// Validate m0's 1 existing order was transferred to m3.
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrdersForThisTransactor(m0.PKID)
	//	require.NoError(err)
	//	require.Empty(orderEntries)
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrdersForThisTransactor(m3.PKID)
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//
	//	// Validate if m3 submits an order, they can't match to their existing order.
	//	exchangeRate, err = CalculateScaledExchangeRate(0.2)
	//	require.NoError(err)
	//
	//	metadataM3 := DAOCoinLimitOrderMetadata{
	//		BuyingDAOCoinCreatorPublicKey:             m3.PublicKey,
	//		SellingDAOCoinCreatorPublicKey:            deso.PublicKey,
	//		ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
	//		QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(350),
	//		OperationType:                             DAOCoinLimitOrderOperationTypeBID,
	//		FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
	//	}
	//
	//	_, _, _, err = _doDAOCoinLimitOrderTxn(
	//		t, chain, db, params, feeRateNanosPerKb, m3.Pub, m3.Priv, metadataM3)
	//
	//	require.Error(err)
	//	require.Contains(err.Error(), RuleErrorDAOCoinLimitOrderMatchingOwnOrder)
	//
	//	// Validate m3 can cancel their open order.
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrdersForThisTransactor(m3.PKID)
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//	metadataM3 = DAOCoinLimitOrderMetadata{CancelOrderID: orderEntries[0].OrderID}
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m3.Pub, m3.Priv, metadataM3)
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrdersForThisTransactor(m3.PKID)
	//	require.NoError(err)
	//	require.Empty(orderEntries)
	//
	//	// Validate m1's orders for m3 DAO coins still persist.
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrdersForThisTransactor(m1.PKID)
	//	require.NoError(err)
	//	require.Len(orderEntries, 1)
	//	require.True(orderEntries[0].SellingDAOCoinCreatorPKID.Eq(m3.PKID))
	//
	//	// Validate m1 can still open an order for m3 DAO coin.
	//	exchangeRate, err = CalculateScaledExchangeRate(7.0)
	//	require.NoError(err)
	//
	//	metadataM1 = DAOCoinLimitOrderMetadata{
	//		BuyingDAOCoinCreatorPublicKey:             deso.PublicKey,
	//		SellingDAOCoinCreatorPublicKey:            m3.PublicKey,
	//		ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
	//		QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(100),
	//		OperationType:                             DAOCoinLimitOrderOperationTypeASK,
	//		FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
	//	}
	//
	//	_doDAOCoinLimitOrderTxnWithTestMeta(testMeta, feeRateNanosPerKb, m1.Pub, m1.Priv, metadataM1)
	//
	//	orderEntries, err = dbAdapter.GetAllDAOCoinLimitOrdersForThisTransactor(m1.PKID)
	//	require.NoError(err)
	//	require.Len(orderEntries, 2)
	//}

	_executeAllTestRollbackAndFlush(testMeta.TestMeta)
}

func TestCalculateDAOCoinsTransferredInLimitOrderMatch(t *testing.T) {
	require := require.New(t)
	m0PKID := NewPKID(m0PkBytes)
	m1PKID := NewPKID(m1PkBytes)

	// Scenario 1: one ASK, one BID, exactly matching orders
	{
		// m0 sells 1000 DAO coin base units @ 0.1 $DESO / DAO coin.
		exchangeRate, err := CalculateScaledExchangeRate(10.0)
		require.NoError(err)
		m0Order := &DAOCoinLimitOrderEntry{
			OrderID:                   NewBlockHash(uint256.NewInt().SetUint64(1).Bytes()), // Not used
			TransactorPKID:            m0PKID,
			BuyingDAOCoinCreatorPKID:  &ZeroPKID,
			SellingDAOCoinCreatorPKID: m0PKID,
			ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
			QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(1000),
			OperationType:                             DAOCoinLimitOrderOperationTypeASK,
			FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
		}

		// m1 buys 1000 DAO coin base units @ 0.1 $DESO / DAO coin.
		exchangeRate, err = CalculateScaledExchangeRate(0.1)
		require.NoError(err)
		m1Order := &DAOCoinLimitOrderEntry{
			OrderID:                   NewBlockHash(uint256.NewInt().SetUint64(1).Bytes()), // Not used
			TransactorPKID:            m1PKID,
			BuyingDAOCoinCreatorPKID:  m0PKID,
			SellingDAOCoinCreatorPKID: &ZeroPKID,
			ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
			QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(1000),
			OperationType:                             DAOCoinLimitOrderOperationTypeBID,
			FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
		}

		// m0 = transactor, m1 = matching order
		updatedTransactorQuantityToFillInBaseUnits,
			updatedMatchingQuantityToFillInBaseUnits,
			transactorBuyingCoinBaseUnitsTransferred,
			transactorSellingCoinBaseUnitsTransferred,
			err := _calculateDAOCoinsTransferredInLimitOrderMatch(m1Order, m0Order.OperationType, m0Order.QuantityToFillInBaseUnits)
		require.NoError(err)
		require.Equal(updatedTransactorQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(updatedMatchingQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(transactorBuyingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(100))
		require.Equal(transactorSellingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(1000))

		// m1 = transactor, m0 = matching order
		updatedTransactorQuantityToFillInBaseUnits,
			updatedMatchingQuantityToFillInBaseUnits,
			transactorBuyingCoinBaseUnitsTransferred,
			transactorSellingCoinBaseUnitsTransferred,
			err = _calculateDAOCoinsTransferredInLimitOrderMatch(m0Order, m1Order.OperationType, m1Order.QuantityToFillInBaseUnits)
		require.NoError(err)
		require.Equal(updatedTransactorQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(updatedMatchingQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(transactorBuyingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(1000))
		require.Equal(transactorSellingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(100))
	}

	// Scenario 2: one BID, one ASK, matching orders w/ mismatched prices
	{
		// m0 buys 1000 DAO coin base units @ 10 $DESO / DAO coin.
		exchangeRate, err := CalculateScaledExchangeRate(10.0)
		require.NoError(err)
		m0Order := &DAOCoinLimitOrderEntry{
			OrderID:                   NewBlockHash(uint256.NewInt().SetUint64(1).Bytes()), // Not used
			TransactorPKID:            m0PKID,
			BuyingDAOCoinCreatorPKID:  m0PKID,
			SellingDAOCoinCreatorPKID: &ZeroPKID,
			ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
			QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(1000),
			OperationType:                             DAOCoinLimitOrderOperationTypeBID,
			FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
		}

		// m1 sells 500 DAO coin base units @ 5 $DESO / DAO coin.
		exchangeRate, err = CalculateScaledExchangeRate(0.2)
		require.NoError(err)
		m1Order := &DAOCoinLimitOrderEntry{
			OrderID:                   NewBlockHash(uint256.NewInt().SetUint64(1).Bytes()), // Not used
			TransactorPKID:            m1PKID,
			BuyingDAOCoinCreatorPKID:  &ZeroPKID,
			SellingDAOCoinCreatorPKID: m0PKID,
			ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
			QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(500),
			OperationType:                             DAOCoinLimitOrderOperationTypeASK,
			FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
		}

		// m0 = transactor, m1 = matching order
		// m0 buys 500 DAO coin base units @ 5 $DESO / DAO coin.
		updatedTransactorQuantityToFillInBaseUnits,
			updatedMatchingQuantityToFillInBaseUnits,
			transactorBuyingCoinBaseUnitsTransferred,
			transactorSellingCoinBaseUnitsTransferred,
			err := _calculateDAOCoinsTransferredInLimitOrderMatch(m1Order, m0Order.OperationType, m0Order.QuantityToFillInBaseUnits)
		require.NoError(err)
		require.Equal(updatedTransactorQuantityToFillInBaseUnits, uint256.NewInt().SetUint64(500))
		require.Equal(updatedMatchingQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(transactorBuyingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(500))
		require.Equal(transactorSellingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(2500))

		// m1 = transactor, m0 = matching order
		// m1 sells 500 DAO coin base units @ 10 $DESO / DAO coin.
		updatedTransactorQuantityToFillInBaseUnits,
			updatedMatchingQuantityToFillInBaseUnits,
			transactorBuyingCoinBaseUnitsTransferred,
			transactorSellingCoinBaseUnitsTransferred,
			err = _calculateDAOCoinsTransferredInLimitOrderMatch(m0Order, m1Order.OperationType, m1Order.QuantityToFillInBaseUnits)
		require.NoError(err)
		require.Equal(updatedTransactorQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(updatedMatchingQuantityToFillInBaseUnits, uint256.NewInt().SetUint64(500))
		require.Equal(transactorBuyingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(5000))
		require.Equal(transactorSellingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(500))
	}

	// Scenario 3: m0 and m1 both submit BIDs that should match
	{
		// m0 buys 100 DAO coin base units @ 10 $DESO / DAO coin.
		exchangeRate, err := CalculateScaledExchangeRate(10.0)
		require.NoError(err)
		m0Order := &DAOCoinLimitOrderEntry{
			OrderID:                   NewBlockHash(uint256.NewInt().SetUint64(1).Bytes()), // Not used
			TransactorPKID:            m0PKID,
			BuyingDAOCoinCreatorPKID:  m0PKID,
			SellingDAOCoinCreatorPKID: &ZeroPKID,
			ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
			QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(100),
			OperationType:                             DAOCoinLimitOrderOperationTypeBID,
			FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
		}

		// m1 buys 1000 $DESO @ 0.1 DAO coin / $DESO.
		exchangeRate, err = CalculateScaledExchangeRate(0.1)
		require.NoError(err)
		m1Order := &DAOCoinLimitOrderEntry{
			OrderID:                   NewBlockHash(uint256.NewInt().SetUint64(1).Bytes()), // Not used
			TransactorPKID:            m1PKID,
			BuyingDAOCoinCreatorPKID:  &ZeroPKID,
			SellingDAOCoinCreatorPKID: m0PKID,
			ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
			QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(1000),
			OperationType:                             DAOCoinLimitOrderOperationTypeBID,
			FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
		}

		// m0 = transactor, m1 = matching order
		// m0 buys 100 DAO coin base units @ 10 $DESO / DAO coin.
		updatedTransactorQuantityToFillInBaseUnits,
			updatedMatchingQuantityToFillInBaseUnits,
			transactorBuyingCoinBaseUnitsTransferred,
			transactorSellingCoinBaseUnitsTransferred,
			err := _calculateDAOCoinsTransferredInLimitOrderMatch(m1Order, m0Order.OperationType, m0Order.QuantityToFillInBaseUnits)
		require.NoError(err)
		require.Equal(updatedTransactorQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(updatedMatchingQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(transactorBuyingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(100))
		require.Equal(transactorSellingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(1000))

		// m1 = transactor, m0 = matching order
		// m1 buys 1000 $DESO @ 0.1 DAO coin / $DESO.
		updatedTransactorQuantityToFillInBaseUnits,
			updatedMatchingQuantityToFillInBaseUnits,
			transactorBuyingCoinBaseUnitsTransferred,
			transactorSellingCoinBaseUnitsTransferred,
			err = _calculateDAOCoinsTransferredInLimitOrderMatch(m0Order, m1Order.OperationType, m1Order.QuantityToFillInBaseUnits)
		require.NoError(err)
		require.Equal(updatedTransactorQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(updatedMatchingQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(transactorBuyingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(1000))
		require.Equal(transactorSellingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(100))
	}

	// Scenario 4: m0 and m1 both submit BIDs that match, m1 gets a better price than expected
	{
		// m0 buys 100 DAO coin base units @ 10 $DESO / DAO coin.
		exchangeRate, err := CalculateScaledExchangeRate(10.0)
		require.NoError(err)
		m0Order := &DAOCoinLimitOrderEntry{
			OrderID:                   NewBlockHash(uint256.NewInt().SetUint64(1).Bytes()), // Not used
			TransactorPKID:            m0PKID,
			BuyingDAOCoinCreatorPKID:  m0PKID,
			SellingDAOCoinCreatorPKID: &ZeroPKID,
			ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
			QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(100),
			OperationType:                             DAOCoinLimitOrderOperationTypeBID,
			FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
		}

		// m1 buys 250 $DESO @ 0.2 DAO coin / $DESO.
		exchangeRate, err = CalculateScaledExchangeRate(0.2)
		require.NoError(err)
		m1Order := &DAOCoinLimitOrderEntry{
			OrderID:                   NewBlockHash(uint256.NewInt().SetUint64(1).Bytes()), // Not used
			TransactorPKID:            m1PKID,
			BuyingDAOCoinCreatorPKID:  &ZeroPKID,
			SellingDAOCoinCreatorPKID: m0PKID,
			ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
			QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(250),
			OperationType:                             DAOCoinLimitOrderOperationTypeBID,
			FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
		}

		// m0 = transactor, m1 = matching order
		// m0 buys 50 DAO coin base units @ 5 $DESO / DAO coin.
		updatedTransactorQuantityToFillInBaseUnits,
			updatedMatchingQuantityToFillInBaseUnits,
			transactorBuyingCoinBaseUnitsTransferred,
			transactorSellingCoinBaseUnitsTransferred,
			err := _calculateDAOCoinsTransferredInLimitOrderMatch(m1Order, m0Order.OperationType, m0Order.QuantityToFillInBaseUnits)
		require.NoError(err)
		require.Equal(updatedTransactorQuantityToFillInBaseUnits, uint256.NewInt().SetUint64(50))
		require.Equal(updatedMatchingQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(transactorBuyingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(50))
		require.Equal(transactorSellingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(250))

		// m1 = transactor, m0 = matching order
		// m1 buys 250 $DESO @ 0.1 DAO coins / $DESO.
		updatedTransactorQuantityToFillInBaseUnits,
			updatedMatchingQuantityToFillInBaseUnits,
			transactorBuyingCoinBaseUnitsTransferred,
			transactorSellingCoinBaseUnitsTransferred,
			err = _calculateDAOCoinsTransferredInLimitOrderMatch(m0Order, m1Order.OperationType, m1Order.QuantityToFillInBaseUnits)
		require.NoError(err)
		require.Equal(updatedTransactorQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(updatedMatchingQuantityToFillInBaseUnits, uint256.NewInt().SetUint64(75))
		require.Equal(transactorBuyingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(250))
		require.Equal(transactorSellingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(25))
	}

	// Scenario 5: m0 and m1 both submit ASKs that should match
	{
		// m0 sells 1000 $DESO @ 10 $DESO / DAO coin.
		exchangeRate, err := CalculateScaledExchangeRate(10.0)
		require.NoError(err)
		m0Order := &DAOCoinLimitOrderEntry{
			OrderID:                   NewBlockHash(uint256.NewInt().SetUint64(1).Bytes()), // Not used
			TransactorPKID:            m0PKID,
			BuyingDAOCoinCreatorPKID:  m0PKID,
			SellingDAOCoinCreatorPKID: &ZeroPKID,
			ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
			QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(1000),
			OperationType:                             DAOCoinLimitOrderOperationTypeASK,
			FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
		}

		// m1 sells 100 DAO coin base units @ 0.1 DAO coin / $DESO.
		exchangeRate, err = CalculateScaledExchangeRate(0.1)
		require.NoError(err)
		m1Order := &DAOCoinLimitOrderEntry{
			OrderID:                   NewBlockHash(uint256.NewInt().SetUint64(1).Bytes()), // Not used
			TransactorPKID:            m1PKID,
			BuyingDAOCoinCreatorPKID:  &ZeroPKID,
			SellingDAOCoinCreatorPKID: m0PKID,
			ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
			QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(100),
			OperationType:                             DAOCoinLimitOrderOperationTypeASK,
			FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
		}

		// m0 = transactor, m1 = matching order
		// m0 sells 1000 $DESO @ 10 $DESO / DAO coin.
		updatedTransactorQuantityToFillInBaseUnits,
			updatedMatchingQuantityToFillInBaseUnits,
			transactorBuyingCoinBaseUnitsTransferred,
			transactorSellingCoinBaseUnitsTransferred,
			err := _calculateDAOCoinsTransferredInLimitOrderMatch(m1Order, m0Order.OperationType, m0Order.QuantityToFillInBaseUnits)
		require.NoError(err)
		require.Equal(updatedTransactorQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(updatedMatchingQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(transactorBuyingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(100))
		require.Equal(transactorSellingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(1000))

		// m1 = transactor, m0 = matching order
		// m1 sells 100 DAO coin base units @ 0.1 DAO coin / $DESO.
		updatedTransactorQuantityToFillInBaseUnits,
			updatedMatchingQuantityToFillInBaseUnits,
			transactorBuyingCoinBaseUnitsTransferred,
			transactorSellingCoinBaseUnitsTransferred,
			err = _calculateDAOCoinsTransferredInLimitOrderMatch(m0Order, m1Order.OperationType, m1Order.QuantityToFillInBaseUnits)
		require.NoError(err)
		require.Equal(updatedTransactorQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(updatedMatchingQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(transactorBuyingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(1000))
		require.Equal(transactorSellingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(100))
	}

	// Scenario 6: m0 and m1 both submit ASKs that match, m1 gets a better price than expected
	{
		// m0 sells 1000 $DESO @ 10 $DESO / DAO coin.
		exchangeRate, err := CalculateScaledExchangeRate(10.0)
		require.NoError(err)
		m0Order := &DAOCoinLimitOrderEntry{
			OrderID:                   NewBlockHash(uint256.NewInt().SetUint64(1).Bytes()), // Not used
			TransactorPKID:            m0PKID,
			BuyingDAOCoinCreatorPKID:  m0PKID,
			SellingDAOCoinCreatorPKID: &ZeroPKID,
			ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
			QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(1000),
			OperationType:                             DAOCoinLimitOrderOperationTypeASK,
			FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
		}

		// m1 sells 50 DAO coin units for 0.2 DAO coin / $DESO.
		exchangeRate, err = CalculateScaledExchangeRate(0.2)
		require.NoError(err)
		m1Order := &DAOCoinLimitOrderEntry{
			OrderID:                   NewBlockHash(uint256.NewInt().SetUint64(1).Bytes()), // Not used
			TransactorPKID:            m1PKID,
			BuyingDAOCoinCreatorPKID:  &ZeroPKID,
			SellingDAOCoinCreatorPKID: m0PKID,
			ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
			QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(50),
			OperationType:                             DAOCoinLimitOrderOperationTypeASK,
			FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
		}

		// m0 = transactor, m1 = matching order
		// m0 sells 250 $DESO @ 5 $DESO / DAO coin.
		updatedTransactorQuantityToFillInBaseUnits,
			updatedMatchingQuantityToFillInBaseUnits,
			transactorBuyingCoinBaseUnitsTransferred,
			transactorSellingCoinBaseUnitsTransferred,
			err := _calculateDAOCoinsTransferredInLimitOrderMatch(m1Order, m0Order.OperationType, m0Order.QuantityToFillInBaseUnits)
		require.NoError(err)
		require.Equal(updatedTransactorQuantityToFillInBaseUnits, uint256.NewInt().SetUint64(750))
		require.Equal(updatedMatchingQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(transactorBuyingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(50))
		require.Equal(transactorSellingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(250))

		// m1 = transactor, m0 = matching order
		// m1 sells 50 DAO coin units for 0.1 DAO coin / $DESO.
		updatedTransactorQuantityToFillInBaseUnits,
			updatedMatchingQuantityToFillInBaseUnits,
			transactorBuyingCoinBaseUnitsTransferred,
			transactorSellingCoinBaseUnitsTransferred,
			err = _calculateDAOCoinsTransferredInLimitOrderMatch(m0Order, m1Order.OperationType, m1Order.QuantityToFillInBaseUnits)
		require.NoError(err)
		require.Equal(updatedTransactorQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(updatedMatchingQuantityToFillInBaseUnits, uint256.NewInt().SetUint64(500))
		require.Equal(transactorBuyingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(500))
		require.Equal(transactorSellingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(50))
	}

	// Scenario 7:
	//   * Transactor submits ASK matching existing BID.
	//   * Transactor order quantity is greater than matching order's quantity.
	{
		// m0 sells 1000 DAO coin units @ 10 DAO coin / $DESO.
		exchangeRate, err := CalculateScaledExchangeRate(10.0)
		require.NoError(err)
		m0Order := &DAOCoinLimitOrderEntry{
			OrderID:                   NewBlockHash(uint256.NewInt().SetUint64(1).Bytes()), // Not used
			TransactorPKID:            m0PKID,
			BuyingDAOCoinCreatorPKID:  &ZeroPKID,
			SellingDAOCoinCreatorPKID: m0PKID,
			ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
			QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(1000),
			OperationType:                             DAOCoinLimitOrderOperationTypeASK,
			FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
		}

		// m1 buys 500 DAO coin units for 0.2 $DESO / DAO coin.
		exchangeRate, err = CalculateScaledExchangeRate(0.2)
		require.NoError(err)
		m1Order := &DAOCoinLimitOrderEntry{
			OrderID:                   NewBlockHash(uint256.NewInt().SetUint64(1).Bytes()), // Not used
			TransactorPKID:            m1PKID,
			BuyingDAOCoinCreatorPKID:  m0PKID,
			SellingDAOCoinCreatorPKID: &ZeroPKID,
			ScaledExchangeRateCoinsToSellPerCoinToBuy: exchangeRate,
			QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(500),
			OperationType:                             DAOCoinLimitOrderOperationTypeBID,
			FillType:                                  DAOCoinLimitOrderFillTypeGoodTillCancelled,
		}

		// m0 = transactor, m1 = matching order
		// m0 sells 500 DAO coin units @ 0.2 $DESO / DAO coin.
		updatedTransactorQuantityToFillInBaseUnits,
			updatedMatchingQuantityToFillInBaseUnits,
			transactorBuyingCoinBaseUnitsTransferred,
			transactorSellingCoinBaseUnitsTransferred,
			err := _calculateDAOCoinsTransferredInLimitOrderMatch(m1Order, m0Order.OperationType, m0Order.QuantityToFillInBaseUnits)
		require.NoError(err)
		require.Equal(updatedTransactorQuantityToFillInBaseUnits, uint256.NewInt().SetUint64(500))
		require.Equal(updatedMatchingQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(transactorBuyingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(100))
		require.Equal(transactorSellingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(500))

		// m1 = transactor, m0 = matching order
		// m1 buys 500 DAO coin units @ 10 DAO coin / $DESO.
		updatedTransactorQuantityToFillInBaseUnits,
			updatedMatchingQuantityToFillInBaseUnits,
			transactorBuyingCoinBaseUnitsTransferred,
			transactorSellingCoinBaseUnitsTransferred,
			err = _calculateDAOCoinsTransferredInLimitOrderMatch(m0Order, m1Order.OperationType, m1Order.QuantityToFillInBaseUnits)
		require.NoError(err)
		require.Equal(updatedTransactorQuantityToFillInBaseUnits, uint256.NewInt())
		require.Equal(updatedMatchingQuantityToFillInBaseUnits, uint256.NewInt().SetUint64(500))
		require.Equal(transactorBuyingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(500))
		require.Equal(transactorSellingCoinBaseUnitsTransferred, uint256.NewInt().SetUint64(50))
	}
}

func TestComputeBaseUnitsToBuyUint256(t *testing.T) {
	require := require.New(t)

	assertEqualStr := func(exchangeRateStr string, quantityToSellStr string, quantityToBuyStr string) {
		exchangeRate, err := CalculateScaledExchangeRateFromString(exchangeRateStr)
		require.NoError(err)
		sellValBig, worked := big.NewInt(0).SetString(quantityToSellStr, 10)
		if !worked {
			panic(fmt.Sprintf("Failed to convert sell quantity %v into bigint", quantityToSellStr))
		}
		selLValUint256 := uint256.NewInt()
		overflow := selLValUint256.SetFromBig(sellValBig)
		if overflow {
			panic(fmt.Sprintf("Failed to convert sell quantity %v into uint256 because of overflow", quantityToSellStr))
		}
		quantityToBuy, err := ComputeBaseUnitsToBuyUint256(exchangeRate, selLValUint256)
		require.NoError(err)

		buyValBig, worked := big.NewInt(0).SetString(quantityToBuyStr, 10)
		if !worked {
			panic(fmt.Sprintf("Failed to convert buy quantity %v into bigint", quantityToBuyStr))
		}
		buyValUint256 := uint256.NewInt()
		overflow = buyValUint256.SetFromBig(buyValBig)
		if overflow {
			panic(fmt.Sprintf("Failed to convert buy quantity %v into uint256 because of overflow", quantityToBuyStr))
		}

		require.Equal(quantityToBuy, buyValUint256)
	}
	assertEqual := func(exchangeRateFloat float64, quantityToSellInt int, quantityToBuyInt int) {
		exchangeRate, err := CalculateScaledExchangeRate(exchangeRateFloat)
		require.NoError(err)
		quantityToSell := uint256.NewInt().SetUint64(uint64(quantityToSellInt))
		quantityToBuy, err := ComputeBaseUnitsToBuyUint256(exchangeRate, quantityToSell)
		require.NoError(err)
		require.Equal(quantityToBuy, uint256.NewInt().SetUint64(uint64(quantityToBuyInt)))

		// We also call assertEqualStr when this function is used
		assertEqualStr(
			fmt.Sprintf("%v", exchangeRateFloat),
			fmt.Sprintf("%v", quantityToSellInt),
			fmt.Sprintf("%v", quantityToBuyInt))
	}

	// Math to verify:
	// exchange rate = # coins to sell / # coins to buy
	//   => exchange rate * # coins to buy = # coins to sell
	//   => # coins to buy = # coins to sell / exchange rate
	assertEqual(0.001, 100, 100000)
	assertEqual(0.002, 100, 50000)
	assertEqual(0.1, 100, 1000)
	assertEqual(0.15, 100, 666)
	assertEqual(0.16, 100, 625)
	assertEqual(0.2, 100, 500)
	assertEqual(0.3, 100, 333)
	assertEqual(0.32, 100, 312)
	assertEqual(0.4, 100, 250)
	assertEqual(0.5, 100, 200)
	assertEqual(0.6, 100, 166)
	assertEqual(0.64, 100, 156)
	assertEqual(0.7, 100, 142)
	assertEqual(0.8, 100, 125)
	assertEqual(0.9, 100, 111)
	assertEqual(1.0, 100, 100)
	assertEqual(1.1, 100, 90)
	assertEqual(1.2, 100, 83)
	assertEqual(1.3, 100, 76)
	assertEqual(1.6, 100, 62)
	assertEqual(2.0, 100, 50)
	assertEqual(4.0, 100, 25)
	assertEqual(10.0, 100, 10)
	assertEqual(0.25, 100, 400)
	assertEqual(3.0, 100, 33)
	assertEqual(0.2, 25000, 125000)
	assertEqual(1.75, 100, 57)
	assertEqual(0.6, 115, 191)
	assertEqual(2.3, 250, 108)
	assertEqual(0.01, 100, 10000)
	assertEqual(0.01, 37, 3700)
	assertEqual(0.3, 100, 333)
	assertEqual(0.115, 259, 2252)

	// Note: integer division isn't exact if the numbers don't divide evenly.
	// 120 / 12.0 is 10 exact.
	assertEqual(12.0, 120, 10)
	// 120 / 11.0 is about 10.9. This becomes 10 in integer division.
	assertEqual(11.0, 120, 10)

	assertEqualStr("0.115", "259", "2252")

	// Test extreme values to make sure everything holds up.
	assertEqualStr("0.00000000000000000000000000000000000002", "300000000000000000000000000000000000004", "15000000000000000000000000000000000000200000000000000000000000000000000000000")
	assertEqualStr("0.0123456", "3123000000000000000000000000000001234541234567", "252964618973561430793157076205287813839848574957")
	assertEqualStr("1234578901234578901234578901234578.09876543210987654321098765432109876543", "3123000000000000000000000000000001234541234567", "2529607461197")
	assertEqualStr("1234578901234578901234578901234578.09876543210987654321098765432109876543", "312300000000000000000000000000000123454123456712345412345671234541234567", "252960746119749819148861202795544558915")
	assertEqualStr("50000000000000000000000000000000000000.000000000000000000000000000000000000002", "400000000000000000000000000000000000000", "8")

	// Test an overflow of the buy amount
	assertErrorStr := func(exchangeRateStr string, quantityToSellStr string) error {
		exchangeRate, err := CalculateScaledExchangeRateFromString(exchangeRateStr)
		require.NoError(err)
		sellValBig, worked := big.NewInt(0).SetString(quantityToSellStr, 10)
		if !worked {
			panic(fmt.Sprintf("Failed to convert sell quantity %v into bigint", quantityToSellStr))
		}
		selLValUint256 := uint256.NewInt()
		overflow := selLValUint256.SetFromBig(sellValBig)
		if overflow {
			panic(fmt.Sprintf("Failed to convert sell quantity %v into uint256 because of overflow", quantityToSellStr))
		}
		_, err = ComputeBaseUnitsToBuyUint256(exchangeRate, selLValUint256)
		require.Error(err)
		return err
	}
	{
		err := assertErrorStr("0.00000000000000000000000000000000000002", "10000000000000000000000000000000000000000")
		require.Contains(err.Error(), "RuleErrorDAOCoinLimitOrderTotalCostOverflowsUint256")
	}
	{
		err := assertErrorStr("0.000000000000000000000000000000000000002", "10000000000000000000000000000000000000000")
		require.Contains(err.Error(), "invalid exchange rate")
	}
	{
		err := assertErrorStr("500000000000000000000000000000000000000.000000000000000000000000000000000000002", "400000000000000000000000000000000000000")
		require.Contains(err.Error(), "RuleErrorDAOCoinLimitOrderTotalCostIsLessThanOneNano")
	}
}

func TestCalculateScaledExchangeRate(t *testing.T) {
	require := require.New(t)
	{
		exchangeRate, err := CalculateScaledExchangeRateFromString(".1234567890123456789012345678901234567890")
		require.NoError(err)
		bigintExpected, _ := big.NewInt(0).SetString("12345678901234567890123456789012345678", 10)
		uint256Expected, _ := uint256.FromBig(bigintExpected)
		require.Equal(exchangeRate, uint256Expected)
	}
	{
		_, err := CalculateScaledExchangeRateFromString("1234567890123456789012345678901234567890.")
		require.Error(err)
	}
	{
		exchangeRate, err := CalculateScaledExchangeRateFromString("12345678901234567890123456789012345678")
		require.NoError(err)
		bigintExpected, _ := big.NewInt(0).SetString("1234567890123456789012345678901234567800000000000000000000000000000000000000", 10)
		uint256Expected, _ := uint256.FromBig(bigintExpected)
		require.Equal(exchangeRate, uint256Expected)
	}
	{
		exchangeRate, err := CalculateScaledExchangeRateFromString("12345678901234567890123456789012345678")
		require.NoError(err)
		bigintExpected, _ := big.NewInt(0).SetString("1234567890123456789012345678901234567800000000000000000000000000000000000000", 10)
		uint256Expected, _ := uint256.FromBig(bigintExpected)
		require.Equal(exchangeRate, uint256Expected)
	}
	{
		exchangeRate, err := CalculateScaledExchangeRateFromString("12345678901234567890123456789012345678.")
		require.NoError(err)
		bigintExpected, _ := big.NewInt(0).SetString("1234567890123456789012345678901234567800000000000000000000000000000000000000", 10)
		uint256Expected, _ := uint256.FromBig(bigintExpected)
		require.Equal(exchangeRate, uint256Expected)
	}
	{
		exchangeRate, err := CalculateScaledExchangeRateFromString("")
		require.NoError(err)
		bigintExpected, _ := big.NewInt(0).SetString("0", 10)
		uint256Expected, _ := uint256.FromBig(bigintExpected)
		require.Equal(exchangeRate, uint256Expected)
	}
}

//
// ----- HELPERS
//

type DAOCoinLimitOrderTestMeta struct {
	TestMeta          *TestMeta
	UtxoView          *UtxoView
	FeeRateNanosPerKb uint64
}

type DAOCoinLimitOrderTestInput struct {
	Transactor                                DAOCoinLimitOrderTestUser
	Buying                                    DAOCoinLimitOrderTestUser
	Selling                                   DAOCoinLimitOrderTestUser
	ScaledExchangeRateCoinsToSellPerCoinToBuy *uint256.Int
	Price                                     float64
	QuantityToFillInBaseUints                 *uint256.Int
	Quantity                                  uint64
	OperationType                             DAOCoinLimitOrderOperationType
	FillType                                  DAOCoinLimitOrderFillType
	CancelOrderID                             *BlockHash
	OrderBookDelta                            int
	CoinDeltas                                map[string]map[string]int
}

type DAOCoinLimitOrderTestUser struct {
	Name      string
	Pub       string
	Priv      string
	PkBytes   []byte
	PublicKey *PublicKey
	PKID      *PKID
}

func (testMeta *DAOCoinLimitOrderTestMeta) SubmitOrder(testInput DAOCoinLimitOrderTestInput) error {
	require := require.New(testMeta.TestMeta.t)

	// Initialize all coin deltas to ZERO.
	coinDeltas := make(map[string]map[string]int)
	usernames := []string{"$DESO", "m0", "m1", "m2", "m3", "m4"}

	for _, username := range usernames {
		coinDeltas[username] = make(map[string]int)

		for _, coinCreatorName := range usernames {
			coinDeltas[username][coinCreatorName] = 0
		}
	}

	// Update coin deltas with any input coin deltas.
	for username, deltaMap := range testInput.CoinDeltas {
		for coinCreatorName, delta := range deltaMap {
			coinDeltas[username][coinCreatorName] = delta
		}
	}

	// Track original order book size.
	originalOrderBookSize := testMeta.OrderBook()

	// Track original coin balances.
	originalCoinBalances := make(map[string]map[string]*uint256.Int)

	for username, balanceMap := range coinDeltas {
		if username == "$DESO" {
			continue
		}
		user := testMeta.GetUser(username)
		originalCoinBalances[username] = make(map[string]*uint256.Int)
		for coinCreatorName := range balanceMap {
			coinCreator := testMeta.GetUser(coinCreatorName)
			if coinCreatorName == "$DESO" {
				originalCoinBalances[username][coinCreatorName] = uint256.NewInt().SetUint64(
					testMeta.GetDESOBalanceNanos(user))
			} else {
				originalCoinBalances[username][coinCreatorName] = testMeta.GetDAOCoinBalanceNanos(
					user, coinCreator)
			}
		}
	}

	// Create txn.
	currentTxn, totalInput, currentFeeNanos, failure := testMeta.CreateOrderTxn(testInput)
	feeNanos := uint256.NewInt().SetUint64(currentFeeNanos)

	// Connect txn if creating txn succeeded.
	if failure == nil {
		failure = testMeta.ConnectOrderTxn(testInput, currentTxn, totalInput)
	}

	// Compare updated order book size.
	require.Equal(len(originalOrderBookSize)+testInput.OrderBookDelta, len(testMeta.OrderBook()))

	// Track updated coin balances.
	updatedCoinBalances := make(map[string]map[string]*uint256.Int)

	for username, balanceMap := range coinDeltas {
		if username == "$DESO" {
			continue
		}
		user := testMeta.GetUser(username)
		updatedCoinBalances[username] = make(map[string]*uint256.Int)
		for coinCreatorName := range balanceMap {
			coinCreator := testMeta.GetUser(coinCreatorName)
			if coinCreatorName == "$DESO" {
				updatedCoinBalances[username][coinCreatorName] = uint256.NewInt().SetUint64(
					testMeta.GetDESOBalanceNanos(user))
			} else {
				updatedCoinBalances[username][coinCreatorName] = testMeta.GetDAOCoinBalanceNanos(
					user, coinCreator)
			}
		}
	}

	// Compare coin deltas.
	var err error
	for username, balanceMap := range coinDeltas {
		if username == "$DESO" {
			continue
		}

		for coinCreatorName := range balanceMap {
			calculatedCoinBalance := originalCoinBalances[username][coinCreatorName]

			if testInput.Transactor.Name == username && coinCreatorName == "$DESO" && failure == nil {
				// If calculating transactor's change in $DESO
				// and this txn doesn't have an error, we have
				// to include the txn fees.
				calculatedCoinBalance, err = SafeUint256().Sub(calculatedCoinBalance, feeNanos)
				require.NoError(err)
			}

			if coinDeltas[username][coinCreatorName] > 0 {
				calculatedCoinBalance, err = SafeUint256().Add(
					calculatedCoinBalance, uint256.NewInt().SetUint64(
						uint64(coinDeltas[username][coinCreatorName])))
				require.NoError(err)
				require.Equal(
					calculatedCoinBalance, updatedCoinBalances[username][coinCreatorName])
			} else if coinDeltas[username][coinCreatorName] < 0 {
				calculatedCoinBalance, err = SafeUint256().Sub(
					calculatedCoinBalance, uint256.NewInt().SetUint64(
						uint64(math.Abs(float64(coinDeltas[username][coinCreatorName])))))
				require.NoError(err)
				require.Equal(
					calculatedCoinBalance, updatedCoinBalances[username][coinCreatorName])
			} else {
				require.Equal(
					calculatedCoinBalance, updatedCoinBalances[username][coinCreatorName])
			}
		}
	}

	return failure
}

func (testMeta *DAOCoinLimitOrderTestMeta) CreateOrderTxn(testInput DAOCoinLimitOrderTestInput) (
	*MsgDeSoTxn, uint64, uint64, error) {
	require := require.New(testMeta.TestMeta.t)

	txn, totalInput, changeAmount, fees, err := testMeta.TestMeta.chain.CreateDAOCoinLimitOrderTxn(
		testInput.Transactor.PkBytes, testMeta.ToOrderMetadata(testInput),
		testMeta.FeeRateNanosPerKb, nil, []*DeSoOutput{})
	if err != nil {
		return nil, 0, 0, err
	}

	// There is some spend amount that may go to matching orders.
	// That is why these are not always exactly equal.
	require.True(totalInput >= changeAmount+fees)
	return txn, totalInput, fees, nil
}

func (testMeta *DAOCoinLimitOrderTestMeta) ConnectOrderTxn(
	testInput DAOCoinLimitOrderTestInput, txn *MsgDeSoTxn, totalInputMake uint64) error {

	require := require.New(testMeta.TestMeta.t)
	meta := testMeta.TestMeta
	meta.expectedSenderBalances = append(
		meta.expectedSenderBalances, testMeta.GetDESOBalanceNanos(testInput.Transactor))
	currentUtxoView, err := NewUtxoView(meta.db, meta.params, meta.chain.postgres)
	require.NoError(err)
	// Sign the transaction now that its inputs are set up.
	_signTxn(meta.t, txn, testInput.Transactor.Priv)
	// Always use savedHeight (blockHeight+1) for validation since it's
	// assumed the transaction will get mined into the next block.
	utxoOps, totalInput, totalOutput, feeNanos, err := currentUtxoView.ConnectTransaction(
		txn, txn.Hash(), getTxnSize(*txn), meta.savedHeight, true, false)
	if err != nil {
		// If error, remove most-recent expected sender balance added for this txn.
		meta.expectedSenderBalances = meta.expectedSenderBalances[:len(meta.expectedSenderBalances)-1]
		return err
	}
	require.Equal(totalInput, totalOutput+feeNanos)
	// totalInput will be greater than totalInputMake since we add BidderInputs to totalInput.
	require.True(totalInput >= totalInputMake)
	require.Equal(utxoOps[len(utxoOps)-1].Type, OperationTypeDAOCoinLimitOrder)
	require.NoError(currentUtxoView.FlushToDb())
	meta.txnOps = append(meta.txnOps, utxoOps)
	meta.txns = append(meta.txns, txn)
	require.NoError(err)
	return nil
}

func (testMeta *DAOCoinLimitOrderTestMeta) CreateProfile(user DAOCoinLimitOrderTestUser) {
	require := require.New(testMeta.TestMeta.t)
	require.Nil(testMeta.UtxoView.GetProfileEntryForPKID(user.PKID))
	_updateProfileWithTestMeta(
		testMeta.TestMeta,
		testMeta.FeeRateNanosPerKb, /*feeRateNanosPerKB*/
		user.Pub,                   /*updaterPkBase58Check*/
		user.Priv,                  /*updaterPrivBase58Check*/
		[]byte{},                   /*profilePubKey*/
		user.Name,                  /*newUsername*/
		"",                         /*newDescription*/
		shortPic,                   /*newProfilePic*/
		10*100,                     /*newCreatorBasisPoints*/
		1.25*100*100,               /*newStakeMultipleBasisPoints*/
		false,                      /*isHidden*/
	)
	require.NotNil(testMeta.UtxoView.GetProfileEntryForPKID(user.PKID))
}

func (testMeta *DAOCoinLimitOrderTestMeta) MintDAOCoins(user DAOCoinLimitOrderTestUser, numCoinNanos uint64) {
	// Confirm original balance is zero.
	require := require.New(testMeta.TestMeta.t)
	daoCoinUnits := uint256.NewInt().SetUint64(numCoinNanos)
	originalBalanceNanos := testMeta.GetDAOCoinBalanceNanos(user, user)
	require.Zero(*originalBalanceNanos)

	// Mint coins.
	daoCoinMintMetadata := DAOCoinMetadata{
		ProfilePublicKey: user.PkBytes,
		OperationType:    DAOCoinOperationTypeMint,
		CoinsToMintNanos: *daoCoinUnits,
	}
	_daoCoinTxnWithTestMeta(testMeta.TestMeta, testMeta.FeeRateNanosPerKb, user.Pub, user.Priv, daoCoinMintMetadata)

	// Confirm updated balance.
	updatedBalanceNanos := testMeta.GetDAOCoinBalanceNanos(user, user)
	require.Equal(updatedBalanceNanos, daoCoinUnits)
}

func (testMeta *DAOCoinLimitOrderTestMeta) TransferDAOCoins(
	coinCreator DAOCoinLimitOrderTestUser, from DAOCoinLimitOrderTestUser, to DAOCoinLimitOrderTestUser, numCoinNanos uint64) {
	// Track original balances to compare.
	require := require.New(testMeta.TestMeta.t)
	daoCoinUnitsToTransfer := uint256.NewInt().SetUint64(numCoinNanos)
	originalFromBalanceNanos := testMeta.GetDAOCoinBalanceNanos(from, coinCreator)
	originalToBalanceNanos := testMeta.GetDAOCoinBalanceNanos(to, coinCreator)

	// Transfer coins.
	daoCoinTransferMetadata := DAOCoinTransferMetadata{
		ProfilePublicKey:       coinCreator.PkBytes,
		DAOCoinToTransferNanos: *daoCoinUnitsToTransfer,
		ReceiverPublicKey:      to.PkBytes,
	}
	_daoCoinTransferTxnWithTestMeta(testMeta.TestMeta, testMeta.FeeRateNanosPerKb, from.Pub, from.Priv, daoCoinTransferMetadata)

	// Confirm updated balances.
	updatedFromBalance := testMeta.GetDAOCoinBalanceNanos(from, coinCreator)
	calculatedFromBalance, err := SafeUint256().Sub(originalFromBalanceNanos, daoCoinUnitsToTransfer)
	require.NoError(err)
	require.Equal(calculatedFromBalance, updatedFromBalance)
	updatedToBalance := testMeta.GetDAOCoinBalanceNanos(to, coinCreator)
	calculatedToBalance, err := SafeUint256().Add(originalToBalanceNanos, daoCoinUnitsToTransfer)
	require.NoError(err)
	require.Equal(calculatedToBalance, updatedToBalance)
}

func (testMeta *DAOCoinLimitOrderTestMeta) GetUser(username string) DAOCoinLimitOrderTestUser {
	switch username {
	case "$DESO":
		return DAOCoinLimitOrderTestUser{
			Name:      "$DESO",
			Pub:       "",
			Priv:      "",
			PkBytes:   ZeroPublicKey.ToBytes(),
			PublicKey: &ZeroPublicKey,
			PKID:      &ZeroPKID,
		}
	case "m0":
		return DAOCoinLimitOrderTestUser{
			Name:      "m0",
			Pub:       m0Pub,
			Priv:      m0Priv,
			PkBytes:   m0PkBytes,
			PublicKey: NewPublicKey(m0PkBytes),
			PKID:      testMeta.UtxoView.GetDbAdapter().GetPKIDForPublicKey(m0PkBytes),
		}
	case "m1":
		return DAOCoinLimitOrderTestUser{
			Name:      "m1",
			Pub:       m1Pub,
			Priv:      m1Priv,
			PkBytes:   m1PkBytes,
			PublicKey: NewPublicKey(m1PkBytes),
			PKID:      testMeta.UtxoView.GetDbAdapter().GetPKIDForPublicKey(m1PkBytes),
		}
	case "m2":
		return DAOCoinLimitOrderTestUser{
			Name:      "m2",
			Pub:       m2Pub,
			Priv:      m2Priv,
			PkBytes:   m2PkBytes,
			PublicKey: NewPublicKey(m2PkBytes),
			PKID:      testMeta.UtxoView.GetDbAdapter().GetPKIDForPublicKey(m2PkBytes),
		}
	case "m3":
		return DAOCoinLimitOrderTestUser{
			Name:      "m3",
			Pub:       m3Pub,
			Priv:      m3Priv,
			PkBytes:   m3PkBytes,
			PublicKey: NewPublicKey(m3PkBytes),
			PKID:      testMeta.UtxoView.GetDbAdapter().GetPKIDForPublicKey(m3PkBytes),
		}
	case "m4":
		return DAOCoinLimitOrderTestUser{
			Name:      "m4",
			Pub:       m4Pub,
			Priv:      m4Priv,
			PkBytes:   m4PkBytes,
			PublicKey: NewPublicKey(m4PkBytes),
			PKID:      testMeta.UtxoView.GetDbAdapter().GetPKIDForPublicKey(m4PkBytes),
		}
	default:
		return DAOCoinLimitOrderTestUser{}
	}
}

func (testMeta *DAOCoinLimitOrderTestMeta) OrderBook() []*DAOCoinLimitOrderEntry {
	require := require.New(testMeta.TestMeta.t)
	orderEntries, err := testMeta.UtxoView.GetDbAdapter().GetAllDAOCoinLimitOrders()
	require.NoError(err)
	return orderEntries
}

func (testMeta *DAOCoinLimitOrderTestMeta) ToOrderEntry(testInput DAOCoinLimitOrderTestInput) *DAOCoinLimitOrderEntry {
	require := require.New(testMeta.TestMeta.t)
	price, err := CalculateScaledExchangeRate(testInput.Price)
	require.NoError(err)
	operationType := testInput.OperationType
	if operationType == 0 {
		operationType = DAOCoinLimitOrderOperationTypeBID
	}
	fillType := testInput.FillType
	if fillType == 0 {
		fillType = DAOCoinLimitOrderFillTypeGoodTillCancelled
	}

	return &DAOCoinLimitOrderEntry{
		OrderID:                   NewBlockHash(uint256.NewInt().SetUint64(1).Bytes()), // Not used
		TransactorPKID:            testInput.Transactor.PKID,
		BuyingDAOCoinCreatorPKID:  testInput.Buying.PKID,
		SellingDAOCoinCreatorPKID: testInput.Selling.PKID,
		ScaledExchangeRateCoinsToSellPerCoinToBuy: price,
		QuantityToFillInBaseUnits:                 uint256.NewInt().SetUint64(testInput.Quantity),
		OperationType:                             operationType,
		FillType:                                  fillType,
	}
}

func (testMeta *DAOCoinLimitOrderTestMeta) ToOrderMetadata(testInput DAOCoinLimitOrderTestInput) *DAOCoinLimitOrderMetadata {
	require := require.New(testMeta.TestMeta.t)
	var err error
	metadata := &DAOCoinLimitOrderMetadata{}
	// Initialize BuyCoin.
	if testInput.Buying.Name != "" {
		metadata.BuyingDAOCoinCreatorPublicKey = testInput.Buying.PublicKey
	}
	// Initialize SellCoin.
	if testInput.Selling.Name != "" {
		metadata.SellingDAOCoinCreatorPublicKey = testInput.Selling.PublicKey
	}
	// Initialize Price.
	if testInput.ScaledExchangeRateCoinsToSellPerCoinToBuy != nil {
		metadata.ScaledExchangeRateCoinsToSellPerCoinToBuy = testInput.ScaledExchangeRateCoinsToSellPerCoinToBuy
	} else {
		metadata.ScaledExchangeRateCoinsToSellPerCoinToBuy, err = CalculateScaledExchangeRate(testInput.Price)
		require.NoError(err)
	}
	// Initialize Quantity.
	if testInput.QuantityToFillInBaseUints != nil {
		metadata.QuantityToFillInBaseUnits = testInput.QuantityToFillInBaseUints
	}
	if testInput.Quantity != 0 {
		metadata.QuantityToFillInBaseUnits = uint256.NewInt().SetUint64(testInput.Quantity)
	}
	// Initialize OperationType.
	metadata.OperationType = testInput.OperationType
	if metadata.OperationType == 0 {
		metadata.OperationType = DAOCoinLimitOrderOperationTypeBID
	}
	// Initialize FillType.
	metadata.FillType = testInput.FillType
	if metadata.FillType == 0 {
		metadata.FillType = DAOCoinLimitOrderFillTypeGoodTillCancelled
	}
	// Initialize CancelOrderID.
	if testInput.CancelOrderID != nil {
		metadata.CancelOrderID = testInput.CancelOrderID
	}
	return metadata
}

func (testMeta *DAOCoinLimitOrderTestMeta) GetDAOCoinBalanceNanos(
	user DAOCoinLimitOrderTestUser, coinCreator DAOCoinLimitOrderTestUser) *uint256.Int {
	balanceEntry := testMeta.UtxoView.GetDbAdapter().GetBalanceEntry(user.PKID, coinCreator.PKID, true)
	if balanceEntry == nil {
		return uint256.NewInt()
	}
	return &balanceEntry.BalanceNanos
}

func (testMeta *DAOCoinLimitOrderTestMeta) GetDESOBalanceNanos(user DAOCoinLimitOrderTestUser) uint64 {
	return _getBalance(testMeta.TestMeta.t, testMeta.TestMeta.chain, testMeta.TestMeta.mempool, user.Pub)
}

func (order *DAOCoinLimitOrderEntry) Eq(testInput DAOCoinLimitOrderTestInput) bool {
	if !order.TransactorPKID.Eq(testInput.Transactor.PKID) {
		return false
	}
	if !order.BuyingDAOCoinCreatorPKID.Eq(testInput.Buying.PKID) {
		return false
	}
	if !order.SellingDAOCoinCreatorPKID.Eq(testInput.Selling.PKID) {
		return false
	}
	price, err := CalculateScaledExchangeRate(testInput.Price)
	if err != nil {
		return false
	}
	if !order.ScaledExchangeRateCoinsToSellPerCoinToBuy.Eq(price) {
		return false
	}
	if !order.QuantityToFillInBaseUnits.Eq(uint256.NewInt().SetUint64(testInput.Quantity)) {
		return false
	}
	if testInput.OperationType == 0 && order.OperationType != DAOCoinLimitOrderOperationTypeBID {
		return false
	}
	if testInput.OperationType != 0 && order.OperationType != testInput.OperationType {
		return false
	}
	if testInput.FillType == 0 && order.FillType != DAOCoinLimitOrderFillTypeGoodTillCancelled {
		return false
	}
	if testInput.FillType != 0 && order.FillType != testInput.FillType {
		return false
	}
	return true
}
