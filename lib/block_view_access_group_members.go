package lib

import (
	"bytes"
	"fmt"
	"github.com/pkg/errors"
)

// GetAccessGroupMemberEntry will check the membership index for membership of memberPublicKey in the group
// <groupOwnerPublicKey, groupKeyName>. Based on the blockheight, we fetch the full group or we fetch
// the simplified message group entry from the membership index. forceFullEntry is an optional parameter that
// will force us to always fetch the full group entry.
func (bav *UtxoView) GetAccessGroupMemberEntry(memberPublicKey *PublicKey, groupOwnerPublicKey *PublicKey,
	groupKeyName *GroupKeyName) (*AccessGroupMemberEntry, error) {

	// If either of the provided parameters is nil, we return.
	if memberPublicKey == nil || groupOwnerPublicKey == nil || groupKeyName == nil {
		return nil, fmt.Errorf("GetAccessGroupMemberEntry: Called with nil memberPublicKey, groupOwnerPublicKey, or groupKeyName")
	}

	groupMembershipKey := NewGroupMembershipKey(*memberPublicKey, *groupOwnerPublicKey, *groupKeyName)

	// If the group has already been fetched in this utxoView, then we get it directly from there.
	if mapValue, exists := bav.GroupMembershipKeyToAccessGroupMember[*groupMembershipKey]; exists {
		return mapValue, nil
	}

	// If we get here, it means that the group has not been fetched in this utxoView. We fetch it from the db.
	accessGroupMember, err := DBGetAccessGroupMemberEntry(bav.Handle, bav.Snapshot, *memberPublicKey, *groupOwnerPublicKey, *groupKeyName)
	if err != nil {
		return nil, errors.Wrapf(err, "GetAccessGroupMemberEntry: Problem fetching access group member entry")
	}
	// If member exists in DB, we also set the mapping in utxoView.
	if accessGroupMember != nil {
		if err := bav._setGroupMembershipKeyToAccessGroupMemberMapping(accessGroupMember, groupOwnerPublicKey, groupKeyName); err != nil {
			return nil, errors.Wrapf(err, "GetAccessGroupMemberEntry: Problem setting group membership key to access group member mapping")
		}
	}
	return accessGroupMember, nil
}

// _setAccessGroupMemberEntry will set the membership mapping of AccessGroupMember.
func (bav *UtxoView) _setAccessGroupMemberEntry(accessGroupMemberEntry *AccessGroupMemberEntry,
	groupOwnerPublicKey *PublicKey, groupKeyName *GroupKeyName) error {

	// This function shouldn't be called with a nil member.
	if accessGroupMemberEntry == nil {
		return fmt.Errorf("_setAccessGroupMemberEntry: Called with nil accessGroupMemberEntry")
	}

	// If either of the provided parameters is nil, we return.
	if groupOwnerPublicKey == nil || groupKeyName == nil || accessGroupMemberEntry == nil {
		return fmt.Errorf("_setAccessGroupMemberEntry: Called with nil groupOwnerPublicKey, groupKeyName, or accessGroupMemberEntry")
	}

	// set utxoView mapping
	return errors.Wrapf(
		bav._setGroupMembershipKeyToAccessGroupMemberMapping(accessGroupMemberEntry, groupOwnerPublicKey, groupKeyName),
		"_setAccessGroupMemberEntry: Problem setting group membership key to access group member mapping")
}

// _deleteAccessGroupMember will set the membership mapping of AccessGroupMember.isDeleted to true.
func (bav *UtxoView) _deleteAccessGroupMember(accessGroupMemberEntry *AccessGroupMemberEntry, groupOwnerPublicKey *PublicKey,
	groupKeyName *GroupKeyName) error {

	// This function shouldn't be called with a nil member.
	if accessGroupMemberEntry == nil || accessGroupMemberEntry.AccessGroupMemberPublicKey == nil ||
		groupOwnerPublicKey == nil || groupKeyName == nil {
		return fmt.Errorf("_deleteAccessGroupMember: Called with nil accessGroupMemberEntry, " +
			"accessGroupMemberEntry.AccessGroupMemberPublicKey, groupOwnerPublicKey, or groupKeyName")
	}

	// Create a tombstone entry.
	tombstoneAccessGroupMember := *accessGroupMemberEntry
	tombstoneAccessGroupMember.isDeleted = true

	// set utxoView mapping
	if err := bav._setGroupMembershipKeyToAccessGroupMemberMapping(&tombstoneAccessGroupMember, groupOwnerPublicKey, groupKeyName); err != nil {
		return errors.Wrapf(err, "_deleteAccessGroupMember: Problem setting group membership key to access group member mapping")
	}
	return nil
}

func (bav *UtxoView) _setGroupMembershipKeyToAccessGroupMemberMapping(accessGroupMemberEntry *AccessGroupMemberEntry,
	groupOwnerPublicKey *PublicKey, groupKeyName *GroupKeyName) error {

	// This function shouldn't be called with a nil member.
	if accessGroupMemberEntry == nil || groupOwnerPublicKey == nil || groupKeyName == nil {
		return fmt.Errorf("_setGroupMembershipKeyToAccessGroupMemberMapping: " +
			"Called with nil accessGroupMemberEntry, groupOwnerPublicKey, or groupKeyName")
	}

	// Create group membership key.
	groupMembershipKey := *NewGroupMembershipKey(
		*accessGroupMemberEntry.AccessGroupMemberPublicKey, *groupOwnerPublicKey, *groupKeyName)
	// Set the mapping.
	bav.GroupMembershipKeyToAccessGroupMember[groupMembershipKey] = accessGroupMemberEntry
	return nil
}

// _connectAccessGroupMembers is used to connect a AccessGroupMembers transaction to the UtxoView. This transaction
// is used to update members of an existing access group that was previously created via AccessGroupCreate transaction.
// Member updates comprise operations such as adding a new member, removing an existing member, or modifying an existing
// member's entry.
//
// Access group members are identified by a tuple of:
// 	<AccessGroupOwnerPublicKey, AccessGroupKeyName, AccessGroupMemberPublicKey, AccessGroupMemberKeyName>
// It is worth noting that access group members are added to access groups via their own access groups. You can see by
// looking at the index, that it is essentially a 2-access group relationship between the owner's access group and
// member's access group.
func (bav *UtxoView) _connectAccessGroupMembers(
	txn *MsgDeSoTxn, txHash *BlockHash, blockHeight uint32, verifySignatures bool) (
	_totalInput uint64, _totalOutput uint64, _utxoOps []*UtxoOperation, _err error) {

	// Make sure access groups are live.
	if blockHeight < bav.Params.ForkHeights.DeSoAccessGroupsBlockHeight {
		return 0, 0, nil, errors.Wrapf(
			RuleErrorAccessGroupMembersBeforeBlockHeight, "_connectAccessGroupMembers: "+
				"Problem connecting access group members: DeSo V3 messages are not live yet")
	}

	// Check that the transaction has the right TxnType.
	if txn.TxnMeta.GetTxnType() != TxnTypeAccessGroupMembers {
		return 0, 0, nil, fmt.Errorf("_connectAccessGroupMembers: called with bad TxnType %s",
			txn.TxnMeta.GetTxnType().String())
	}
	// Now we know txn.TxnMeta is AccessGroupMembersMetadata
	txMeta := txn.TxnMeta.(*AccessGroupMembersMetadata)

	// If the key name is just a list of 0s, then return because this name is reserved for the base key.
	if EqualGroupKeyName(NewGroupKeyName(txMeta.AccessGroupKeyName), BaseGroupKeyName()) {
		return 0, 0, nil, errors.Wrapf(
			RuleErrorAccessGroupsNameCannotBeZeros, "_connectAccessGroupMembers: "+
				"Problem connecting access group members: Cannot add members to base key.")
	}

	// Make sure that the access group to which we want to add members exists.
	if err := bav.ValidateAccessGroupPublicKeyAndNameWithUtxoView(
		txMeta.AccessGroupOwnerPublicKey, txMeta.AccessGroupKeyName, blockHeight); err != nil {

		return 0, 0, nil, errors.Wrapf(
			RuleErrorAccessGroupDoesntExist, "_connectAccessGroupMembers: Problem connecting access group "+
				"members: Access group does not exist for txnMeta (%v). Error: %v", txMeta, err)
	}

	// Connect basic txn to get the total input and the total output without considering the transaction metadata.
	// Note that it doesn't matter when we do this, because if the transaction fails later on, we will just revert the
	// UtxoView to a previous stable state that isn't corrupted with partial block view entries.
	totalInput, totalOutput, utxoOpsForTxn, err := bav._connectBasicTransfer(
		txn, txHash, blockHeight, verifySignatures)
	if err != nil {
		return 0, 0, nil, errors.Wrapf(err, "_connectAccessGroupMembers: ")
	}

	// Make sure there are no duplicate members with the same AccessGroupMemberPublicKey in the transaction's metadata.
	accessGroupMemberPublicKeys := make(map[PublicKey]struct{})
	for _, accessMember := range txMeta.AccessGroupMembersList {
		memberPublicKey := *NewPublicKey(accessMember.AccessGroupMemberPublicKey)
		if _, exists := accessGroupMemberPublicKeys[memberPublicKey]; !exists {
			accessGroupMemberPublicKeys[memberPublicKey] = struct{}{}
		} else {
			return 0, 0, nil, errors.Wrapf(
				RuleErrorAccessGroupMemberListDuplicateMember, "_connectAccessGroupMembers: "+
					"Problem connecting access group members: Access group member with public key (%v) "+
					"appears more than once in the AccessGroupMemberList.", memberPublicKey)
		}
	}

	// Determine the operation that we want to perform on the access group members.
	switch txMeta.AccessGroupMemberOperationType {
	case AccessGroupMemberOperationTypeAdd:
		// AccessGroupMemberOperationTypeAdd indicates that we want to add members to the access group.
		// Members are added to the access group by their own existing access groups, identified by the pair of:
		// 	<AccessGroupMemberPublicKey, AccessGroupMemberKeyName>
		// Aside from the member's public key and group key name, access group member entries contain
		// a field called EncryptedKey, which stores the main group's access public key encrypted to the member
		// group's access public key. This is used to allow the member to decrypt the main group's access public key
		// using their individual access groups' secrets.
		for _, accessMember := range txMeta.AccessGroupMembersList {
			// We allow a situation, where the group owner adds themselves as a member of their own group. This behavior
			// is recommended for all groups, to allow having a single master access group that can be used to decrypt
			// all the other access groups. The suggested approach is to select an access group with group key name of
			// "default-key" (encoded as utf-8 bytes).
			//
			// If the group owner decides to add themselves as a member, there is an edge-case where the owner would
			// add themselves as a member by the same group -- which would create a possible recursion. We prevent this
			// situation with the below validation check.
			if bytes.Equal(txMeta.AccessGroupOwnerPublicKey, accessMember.AccessGroupMemberPublicKey) &&
				bytes.Equal(NewGroupKeyName(txMeta.AccessGroupKeyName).ToBytes(), NewGroupKeyName(accessMember.AccessGroupMemberKeyName).ToBytes()) {
				return 0, 0, nil, errors.Wrapf(RuleErrorAccessGroupMemberCantAddOwnerBySameGroup,
					"_disconnectAccessGroupMembers: Can't add the owner of the group as a member of the group using the same group key name.")
			}
			// Now we should validate that the accessMember public key and access key name are valid, and point to
			// an existing access group. Moreover, we should validate that the access member hasn't already been added
			// to this group in the past. I.e. we check the following:
			// 	- Validate that the member's access group exist
			// 	- Validate that the member wasn't already added to the main group.
			if err := bav.ValidateAccessGroupPublicKeyAndNameWithUtxoView(
				accessMember.AccessGroupMemberPublicKey, accessMember.AccessGroupMemberKeyName, blockHeight); err != nil {
				return 0, 0, nil, errors.Wrapf(err, "_connectAccessGroupMembers: "+
					"Problem validating access group for member with (AccessGroupMemberPublicKey: %v, AccessGroupMemberKeyName: %v)",
					accessMember.AccessGroupMemberPublicKey, accessMember.AccessGroupMemberKeyName)
			}

			memberGroupEntry, err := bav.GetAccessGroupMemberEntry(NewPublicKey(accessMember.AccessGroupMemberPublicKey),
				NewPublicKey(txMeta.AccessGroupOwnerPublicKey), NewGroupKeyName(txMeta.AccessGroupKeyName))
			if err != nil {
				return 0, 0, nil, errors.Wrapf(err, "_connectAccessGroupMembers: "+
					"Problem getting access group member entry for (AccessGroupMemberPublicKey: %v, AccessGroupMemberKeyName: %v)",
					accessMember.AccessGroupMemberPublicKey, accessMember.AccessGroupMemberKeyName)
			}
			// If the access group member already exists, and wasn't deleted, we error because we can't add the same member twice.
			if memberGroupEntry != nil && !memberGroupEntry.isDeleted {
				return 0, 0, nil, errors.Wrapf(
					RuleErrorAccessMemberAlreadyExists, "_connectAccessGroupCreate: member already exists "+
						"for member with (AccessGroupMemberPublicKey: %v, AccessGroupMemberKeyName %v)",
					accessMember.AccessGroupMemberPublicKey, accessMember.AccessGroupMemberKeyName)
			}

			// Since this new access group member passed all the validation steps, we can add the AccessGroupMemberEntry
			// to the UtxoView. Note that it doesn't matter when we do this, because if the transaction fails later on,
			// we will revert UtxoView to the backup view.
			accessGroupMemberEntry := &AccessGroupMemberEntry{
				AccessGroupMemberPublicKey: NewPublicKey(accessMember.AccessGroupMemberPublicKey),
				AccessGroupMemberKeyName:   NewGroupKeyName(accessMember.AccessGroupMemberKeyName),
				EncryptedKey:               accessMember.EncryptedKey,
				ExtraData:                  accessMember.ExtraData,
			}

			if err := bav._setAccessGroupMemberEntry(accessGroupMemberEntry,
				NewPublicKey(txMeta.AccessGroupOwnerPublicKey), NewGroupKeyName(txMeta.AccessGroupKeyName)); err != nil {
				return 0, 0, nil, errors.Wrapf(err, "_connectAccessGroupMembers: "+
					"Problem setting access group member entry for (AccessGroupMemberPublicKey: %v, AccessGroupMemberKeyName: %v)",
					accessMember.AccessGroupMemberPublicKey, accessMember.AccessGroupMemberKeyName)
			}
		}

	case AccessGroupMemberOperationTypeRemove:
		// TODO: Implement this later
		return 0, 0, nil, errors.Wrapf(
			RuleErrorAccessGroupMemberOperationTypeNotSupported, "_connectAccessGroupCreate: "+
				"Operation type %v not supported yet.", txMeta.AccessGroupMemberOperationType)
	default:
		return 0, 0, nil, errors.Wrapf(
			RuleErrorAccessGroupMemberOperationTypeNotSupported, "_connectAccessGroupCreate: "+
				"Operation type %v not supported.", txMeta.AccessGroupMemberOperationType)
	}

	// utxoOpsForTxn is an array of UtxoOperations. We append to it below to record the UtxoOperations
	// associated with this transaction.
	utxoOpsForTxn = append(utxoOpsForTxn, &UtxoOperation{
		Type: OperationTypeAccessGroupMembers,
	})

	return totalInput, totalOutput, utxoOpsForTxn, nil
}

// _disconnectAccessGroupMembers is the inverse of _connectAccessGroupMembers. It is used to disconnect an AccessGroupMembers
// transaction from the UtxoView.
func (bav *UtxoView) _disconnectAccessGroupMembers(
	operationType OperationType, currentTxn *MsgDeSoTxn, txnHash *BlockHash,
	utxoOpsForTxn []*UtxoOperation, blockHeight uint32) error {

	// Verify that the last UtxoOperation is an AccessGroupMembersOperation.
	if len(utxoOpsForTxn) == 0 {
		return fmt.Errorf("_disconnectAccessGroupMembers: Trying to revert " +
			"AccessGroupMembersList but with no operations")
	}
	accessGroupMembersOp := utxoOpsForTxn[len(utxoOpsForTxn)-1]
	if accessGroupMembersOp.Type != OperationTypeAccessGroupMembers {
		return fmt.Errorf("_disconnectAccessGroupMembers: Trying to revert "+
			"AccessGroupMembersList but found types %v and %v", accessGroupMembersOp.Type, operationType)
	}

	// Check that the transaction has the right TxnType.
	if currentTxn.TxnMeta.GetTxnType() != TxnTypeAccessGroupMembers {
		return fmt.Errorf("_disconnectAccessGroupMembers: called with bad TxnType %s",
			currentTxn.TxnMeta.GetTxnType().String())
	}

	// Get the transaction metadata.
	txMeta := currentTxn.TxnMeta.(*AccessGroupMembersMetadata)

	// Sanity check that the access public key and key name are valid.
	if err := bav.ValidateAccessGroupPublicKeyAndNameWithUtxoView(
		txMeta.AccessGroupOwnerPublicKey, txMeta.AccessGroupKeyName, blockHeight); err != nil {
		return errors.Wrapf(RuleErrorAccessGroupDoesntExist, "_disconnectAccessGroupMembers: "+
			"Problem validating access public key or group key name for txnMeta (%v): error: %v", txMeta, err)
	}

	// Loop over members to make sure they are the same.
	switch txMeta.AccessGroupMemberOperationType {
	case AccessGroupMemberOperationTypeAdd:
		// We will iterate over all members in the transaction's metadata and delete each one. Since the result of the
		// AccessGroupMemberOperationTypeAdd is that a new member is added to the access group, we can just delete the
		// members from the metadata, since a member could have only been added if he hasn't existed before.
		for _, accessMember := range txMeta.AccessGroupMembersList {
			// Make sure the access group member public key and key name are valid and that they point to an existing
			// access group.
			if err := bav.ValidateAccessGroupPublicKeyAndNameWithUtxoView(
				accessMember.AccessGroupMemberPublicKey, accessMember.AccessGroupMemberKeyName, blockHeight); err != nil {
				return errors.Wrapf(err, "_disconnectAccessGroupMembers: "+
					"Problem validating public key or access key for member with "+
					"(AccessGroupMemberPublicKey: %v, AccessGroupMemberKeyName: %v)",
					accessMember.AccessGroupMemberPublicKey, accessMember.AccessGroupMemberKeyName)
			}

			// Now fetch the access group member entry and verify that it exists.
			memberGroupEntry, err := bav.GetAccessGroupMemberEntry(NewPublicKey(accessMember.AccessGroupMemberPublicKey),
				NewPublicKey(txMeta.AccessGroupOwnerPublicKey), NewGroupKeyName(txMeta.AccessGroupKeyName))
			if err != nil {
				return errors.Wrapf(err, "_disconnectAccessGroupMembers: "+
					"Problem getting access group member entry for (AccessGroupMemberPublicKey: %v, AccessGroupMemberKeyName: %v)",
					accessMember.AccessGroupMemberPublicKey, accessMember.AccessGroupMemberKeyName)
			}
			// If the access group member was already deleted, we error because something went wrong.
			if memberGroupEntry == nil || memberGroupEntry.isDeleted {
				return errors.Wrapf(
					RuleErrorAccessMemberDoesntExist, "_disconnectAccessGroupMembers: member doesn't exist "+
						"for member with (AccessGroupMemberPublicKey: %v, AccessGroupMemberKeyName %v)",
					accessMember.AccessGroupMemberPublicKey, accessMember.AccessGroupMemberKeyName)
			}

			// Delete the access group member from the UtxoView.
			if err := bav._deleteAccessGroupMember(memberGroupEntry, NewPublicKey(txMeta.AccessGroupOwnerPublicKey),
				NewGroupKeyName(txMeta.AccessGroupKeyName)); err != nil {
				return errors.Wrapf(err, "_disconnectAccessGroupMembers: "+
					"Problem deleting access group member entry for (AccessGroupMemberPublicKey: %v, AccessGroupMemberKeyName: %v)",
					memberGroupEntry.AccessGroupMemberPublicKey, memberGroupEntry.AccessGroupMemberKeyName)
			}
		}

	case AccessGroupMemberOperationTypeRemove:
		// TODO: Implement this later
		return errors.Wrapf(RuleErrorAccessGroupMemberOperationTypeNotSupported, "_connectAccessGroupCreate: "+
			"Operation type %v not supported yet.", txMeta.AccessGroupMemberOperationType)
	default:
		return errors.Wrapf(RuleErrorAccessGroupMemberOperationTypeNotSupported, "_connectAccessGroupCreate: "+
			"Operation type %v not supported.", txMeta.AccessGroupMemberOperationType)
	}

	// Now disconnect the basic transfer.
	operationIndex := len(utxoOpsForTxn) - 1
	return bav._disconnectBasicTransfer(currentTxn, txnHash, utxoOpsForTxn[:operationIndex], blockHeight)
}
