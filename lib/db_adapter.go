package lib

import (
	"github.com/dgraph-io/badger/v3"
	"github.com/golang/glog"
)

type DbAdapter struct {
	badgerDb   *badger.DB
	postgresDb *Postgres
	snapshot   *Snapshot
}

func (bc *Blockchain) NewDbAdapter() *DbAdapter {
	return &DbAdapter{
		badgerDb:   bc.db,
		postgresDb: bc.postgres,
		snapshot:   bc.snapshot,
	}
}

func (bav *UtxoView) GetDbAdapter() *DbAdapter {
	snap := bav.Snapshot
	if bav.Postgres != nil {
		snap = nil
	}
	return &DbAdapter{
		badgerDb:   bav.Handle,
		postgresDb: bav.Postgres,
		snapshot:   snap,
	}
}

//
// Balance entry
//

func (adapter *DbAdapter) GetBalanceEntry(holder *PKID, creator *PKID, isDAOCoin bool) *BalanceEntry {
	if adapter.postgresDb != nil {
		if isDAOCoin {
			return adapter.postgresDb.GetDAOCoinBalance(holder, creator).NewBalanceEntry()
		}

		return adapter.postgresDb.GetCreatorCoinBalance(holder, creator).NewBalanceEntry()
	}

	return DbGetBalanceEntry(adapter.badgerDb, adapter.snapshot, holder, creator, isDAOCoin)
}

//
// Derived keys
//

func (adapter *DbAdapter) GetOwnerToDerivedKeyMapping(ownerPublicKey PublicKey, derivedPublicKey PublicKey) *DerivedKeyEntry {
	if adapter.postgresDb != nil {
		return adapter.postgresDb.GetDerivedKey(&ownerPublicKey, &derivedPublicKey).NewDerivedKeyEntry()
	}

	return DBGetOwnerToDerivedKeyMapping(adapter.badgerDb, adapter.snapshot, ownerPublicKey, derivedPublicKey)
}

//
// DAO coin limit order
//

func (adapter *DbAdapter) GetDAOCoinLimitOrder(orderID *BlockHash) (*DAOCoinLimitOrderEntry, error) {
	// Temporarily use badger to support DAO Coin limit order DB operations
	//if adapter.postgresDb != nil {
	//	return adapter.postgresDb.GetDAOCoinLimitOrder(orderID)
	//}

	return DBGetDAOCoinLimitOrder(adapter.badgerDb, adapter.snapshot, orderID)
}

func (adapter *DbAdapter) GetAllDAOCoinLimitOrders() ([]*DAOCoinLimitOrderEntry, error) {
	// This function is currently used for testing purposes only.
	// Temporarily use badger to support DAO Coin limit order DB operations
	//if adapter.postgresDb != nil {
	//	return adapter.postgresDb.GetAllDAOCoinLimitOrders()
	//}

	return DBGetAllDAOCoinLimitOrders(adapter.badgerDb)
}

func (adapter *DbAdapter) GetAllDAOCoinLimitOrdersForThisDAOCoinPair(buyingDAOCoinCreatorPKID *PKID, sellingDAOCoinCreatorPKID *PKID) ([]*DAOCoinLimitOrderEntry, error) {
	// Temporarily use badger to support DAO Coin limit order DB operations
	//if adapter.postgresDb != nil {
	//	return adapter.postgresDb.GetAllDAOCoinLimitOrdersForThisDAOCoinPair(buyingDAOCoinCreatorPKID, sellingDAOCoinCreatorPKID)
	//}

	return DBGetAllDAOCoinLimitOrdersForThisDAOCoinPair(adapter.badgerDb, buyingDAOCoinCreatorPKID, sellingDAOCoinCreatorPKID)
}

func (adapter *DbAdapter) GetAllDAOCoinLimitOrdersForThisTransactor(transactorPKID *PKID) ([]*DAOCoinLimitOrderEntry, error) {
	// Temporarily use badger to support DAO Coin limit order DB operations
	//if adapter.postgresDb != nil {
	//	return adapter.postgresDb.GetAllDAOCoinLimitOrdersForThisTransactor(transactorPKID)
	//}

	return DBGetAllDAOCoinLimitOrdersForThisTransactor(adapter.badgerDb, transactorPKID)
}

func (adapter *DbAdapter) GetMatchingDAOCoinLimitOrders(inputOrder *DAOCoinLimitOrderEntry, lastSeenOrder *DAOCoinLimitOrderEntry, orderEntriesInView map[DAOCoinLimitOrderMapKey]bool) ([]*DAOCoinLimitOrderEntry, error) {
	// Temporarily use badger to support DAO Coin limit order DB operations
	//if adapter.postgresDb != nil {
	//	return adapter.postgresDb.GetMatchingDAOCoinLimitOrders(inputOrder, lastSeenOrder, orderEntriesInView)
	//}

	var outputOrders []*DAOCoinLimitOrderEntry
	var err error

	err = adapter.badgerDb.View(func(txn *badger.Txn) error {
		outputOrders, err = DBGetMatchingDAOCoinLimitOrders(txn, inputOrder, lastSeenOrder, orderEntriesInView)
		return err
	})

	return outputOrders, err
}

//
// PKID
//

func (adapter *DbAdapter) GetPKIDForPublicKey(pkBytes []byte) *PKID {
	if adapter.postgresDb != nil {
		profile := adapter.postgresDb.GetProfileForPublicKey(pkBytes)
		if profile == nil {
			return NewPKID(pkBytes)
		}
		return profile.PKID
	}

	return DBGetPKIDEntryForPublicKey(adapter.badgerDb, adapter.snapshot, pkBytes).PKID
}

//
// AccessGroups
//

func (adapter *DbAdapter) GetAccessGroupEntryByAccessGroupId(accessGroupId *AccessGroupId) (*AccessGroupEntry, error) {
	if accessGroupId == nil {
		glog.Errorf("GetAccessGroupEntryByAccessGroupId: Called with nil accessGroupId, this should never happen")
		return nil, nil
	}

	if adapter.postgresDb != nil {
		pgAccessGroup := adapter.postgresDb.GetAccessGroupByAccessGroupId(accessGroupId)
		if pgAccessGroup == nil {
			return nil, nil
		}
		return pgAccessGroup.ToAccessGroupEntry(), nil
	} else {
		return DBGetAccessGroupEntryByAccessGroupId(adapter.badgerDb, adapter.snapshot,
			&accessGroupId.AccessGroupOwnerPublicKey, &accessGroupId.AccessGroupKeyName)
	}
}

//
// AccessGroupMembers
//

func (adapter *DbAdapter) GetAccessGroupMemberEntry(accessGroupMemberPublicKey PublicKey,
	accessGroupOwnerPublicKey PublicKey, accessGroupKeyName GroupKeyName) (*AccessGroupMemberEntry, error) {

	if adapter.postgresDb != nil {
		pgAccessGroupMember := adapter.postgresDb.GetAccessGroupMemberEntry(accessGroupMemberPublicKey,
			accessGroupOwnerPublicKey, accessGroupKeyName)
		if pgAccessGroupMember == nil {
			return nil, nil
		}
		_, _, accessGroupMember := pgAccessGroupMember.ToAccessGroupMemberEntry()
		return accessGroupMember, nil
	} else {
		return DBGetAccessGroupMemberEntry(adapter.badgerDb, adapter.snapshot,
			accessGroupMemberPublicKey, accessGroupOwnerPublicKey, accessGroupKeyName)
	}
}

func (adapter *DbAdapter) GetAccessGroupMemberEnumerationEntry(accessGroupMemberPublicKey PublicKey,
	accessGroupOwnerPublicKey PublicKey, accessGroupKeyName GroupKeyName) (_exists bool, _err error) {

	if adapter.postgresDb != nil {
		return adapter.postgresDb.GetAccessGroupMemberEnumerationEntry(accessGroupMemberPublicKey,
			accessGroupOwnerPublicKey, accessGroupKeyName), nil
	} else {
		// TODO: Use similar function signatures.
		return DBGetAccessGroupMemberExistenceFromEnumerationIndex(adapter.badgerDb, adapter.snapshot,
			accessGroupMemberPublicKey, accessGroupOwnerPublicKey, accessGroupKeyName)
	}
}

func (adapter *DbAdapter) GetPaginatedAccessGroupMembersEnumerationEntries(
	accessGroupOwnerPublicKey PublicKey, accessGroupKeyName GroupKeyName,
	startingAccessGroupMemberPublicKeyBytes []byte, maxMembersToFetch uint32) (
	_accessGroupMemberPublicKeys []*PublicKey, _err error) {

	if maxMembersToFetch == 0 {
		return nil, nil
	}

	if adapter.postgresDb != nil {
		return adapter.postgresDb.GetPaginatedAccessGroupMembersFromEnumerationIndex(
			accessGroupOwnerPublicKey, accessGroupKeyName,
			startingAccessGroupMemberPublicKeyBytes, maxMembersToFetch)
	} else {
		return DBGetPaginatedAccessGroupMembersFromEnumerationIndex(adapter.badgerDb, adapter.snapshot,
			accessGroupOwnerPublicKey, accessGroupKeyName,
			startingAccessGroupMemberPublicKeyBytes, maxMembersToFetch)
	}
}
