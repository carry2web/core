package lib

import (
	"bytes"
	"fmt"
	"github.com/btcsuite/btcd/btcec"
	"github.com/dgraph-io/badger/v3"
	"github.com/golang/glog"
	"github.com/holiman/uint256"
	"github.com/pkg/errors"
	"math"
	"math/big"
	"sort"
)

//
// TYPES: LockedBalanceEntry
//

type LockedBalanceEntry struct {
	HODLerPKID              *PKID
	ProfilePKID             *PKID
	UnlockTimestampNanoSecs int64
	BalanceBaseUnits        uint256.Int
	isDeleted               bool
}

type LockedBalanceEntryKey struct {
	HODLerPKID              PKID
	ProfilePKID             PKID
	UnlockTimestampNanoSecs int64
}

func (lockedBalanceEntry *LockedBalanceEntry) Copy() *LockedBalanceEntry {
	return &LockedBalanceEntry{
		HODLerPKID:              lockedBalanceEntry.HODLerPKID.NewPKID(),
		ProfilePKID:             lockedBalanceEntry.ProfilePKID.NewPKID(),
		UnlockTimestampNanoSecs: lockedBalanceEntry.UnlockTimestampNanoSecs,
		BalanceBaseUnits:        lockedBalanceEntry.BalanceBaseUnits,
		isDeleted:               lockedBalanceEntry.isDeleted,
	}
}

func (lockedBalanceEntry *LockedBalanceEntry) Eq(other *LockedBalanceEntry) bool {
	return lockedBalanceEntry.ToMapKey() == other.ToMapKey()
}

func (lockedBalanceEntry *LockedBalanceEntry) ToMapKey() LockedBalanceEntryKey {
	return LockedBalanceEntryKey{
		HODLerPKID:              *lockedBalanceEntry.HODLerPKID,
		ProfilePKID:             *lockedBalanceEntry.ProfilePKID,
		UnlockTimestampNanoSecs: lockedBalanceEntry.UnlockTimestampNanoSecs,
	}
}

// DeSoEncoder Interface Implementation for LockedBalanceEntry

func (lockedBalanceEntry *LockedBalanceEntry) RawEncodeWithoutMetadata(blockHeight uint64, skipMetadata ...bool) []byte {
	var data []byte
	data = append(data, EncodeToBytes(blockHeight, lockedBalanceEntry.HODLerPKID, skipMetadata...)...)
	data = append(data, EncodeToBytes(blockHeight, lockedBalanceEntry.ProfilePKID, skipMetadata...)...)
	data = append(data, UintToBuf(uint64(lockedBalanceEntry.UnlockTimestampNanoSecs))...)
	data = append(data, VariableEncodeUint256(&lockedBalanceEntry.BalanceBaseUnits)...)
	return data
}

func (lockedBalanceEntry *LockedBalanceEntry) RawDecodeWithoutMetadata(blockHeight uint64, rr *bytes.Reader) error {
	var err error

	// HODLerPKID
	lockedBalanceEntry.HODLerPKID, err = DecodeDeSoEncoder(&PKID{}, rr)
	if err != nil {
		return errors.Wrapf(err, "LockedBalanceEntry.Decode: Problem reading HODLerPKID")
	}

	// ProfilePKID
	lockedBalanceEntry.ProfilePKID, err = DecodeDeSoEncoder(&PKID{}, rr)
	if err != nil {
		return errors.Wrapf(err, "LockedBalanceEntry.Decode: Problem reading ProfilePKID")
	}

	// UnlockTimestampNanoSecs
	uint64UnlockTimestampUnixNanoSecs, err := ReadUvarint(rr)
	if err != nil {
		return errors.Wrapf(err, "LockedBalanceEntry.Decode: Problem reading UnlockTimestampNanoSecs")
	}
	lockedBalanceEntry.UnlockTimestampNanoSecs = int64(uint64UnlockTimestampUnixNanoSecs)

	// BalanceBaseUnits
	balanceBaseUnits, err := VariableDecodeUint256(rr)
	if err != nil {
		return errors.Wrapf(err, "LockedBalanceEntry.Decode: Problem reading BalanceBaseUnits")
	}
	lockedBalanceEntry.BalanceBaseUnits = *balanceBaseUnits

	return err
}

func (lockedBalanceEntry *LockedBalanceEntry) GetVersionByte(blockHeight uint64) byte {
	return 0
}

func (lockedBalanceEntry *LockedBalanceEntry) GetEncoderType() EncoderType {
	return EncoderTypeLockedBalanceEntry
}

// Set and Delete Functions for LockedBalanceEntry

func (bav *UtxoView) _setLockedBalanceEntry(lockedBalanceEntry *LockedBalanceEntry) {
	// This function shouldn't be called with nil.
	if lockedBalanceEntry == nil {
		glog.Errorf("_setLockedBalanceEntryMappingsWithPKIDsTimestampType: Called with nil LockedBalanceEntry; " +
			"this should never happen.")
		return
	}

	// Add a mapping for the LockedBalanceEntry in the view.
	bav.LockedBalanceEntryKeyToLockedBalanceEntry[lockedBalanceEntry.ToMapKey()] = lockedBalanceEntry
}

func (bav *UtxoView) _deleteLockedBalanceEntry(lockedBalanceEntry *LockedBalanceEntry) {
	// Create a tombstone entry.
	tombstoneLockedBalanceEntry := lockedBalanceEntry.Copy()
	tombstoneLockedBalanceEntry.isDeleted = true

	// Set the LockupYieldCurvePoint as deleted in the view.
	bav._setLockedBalanceEntry(tombstoneLockedBalanceEntry)
}

// Get Helper Functions for LockedBalanceEntry

func (bav *UtxoView) GetLockedBalanceEntryForHODLerPKIDProfilePKIDUnlockTimestampNanoSecs(
	hodlerPKID *PKID, profilePKID *PKID, unlockTimestampNanoSecs int64) (_lockedBalanceEntry *LockedBalanceEntry, _err error) {
	// Create a key associated with the LockedBalanceEntry.
	lockedBalanceEntryKey := (&LockedBalanceEntry{
		HODLerPKID:              hodlerPKID,
		ProfilePKID:             profilePKID,
		UnlockTimestampNanoSecs: unlockTimestampNanoSecs,
	}).ToMapKey()

	// Check if the key exists in the view.
	if viewEntry, viewEntryExists :=
		bav.LockedBalanceEntryKeyToLockedBalanceEntry[lockedBalanceEntryKey]; viewEntryExists {
		return viewEntry, nil
	}

	// No mapping exists in the view, check for an entry in the DB.
	lockedBalanceEntry, err := DBGetLockedBalanceEntryForHODLerPKIDProfilePKIDUnlockTimestampNanoSecs(
		bav.Handle, bav.Snapshot, hodlerPKID, profilePKID, unlockTimestampNanoSecs)
	if err != nil {
		return nil,
			errors.Wrap(err, "GetLockedBalanceEntryForHODLerPKIDProfilePKIDUnlockTimestampNanoSecs")
	}

	// Cache the DB entry in the in-memory map.
	if lockedBalanceEntry != nil {
		bav._setLockedBalanceEntry(lockedBalanceEntry)
	}

	return lockedBalanceEntry, nil
}

func (bav *UtxoView) GetUnlockableLockedBalanceEntries(
	hodlerPKID *PKID,
	profilePKID *PKID,
	currentTimestampNanoSecs int64,
) ([]*LockedBalanceEntry, error) {
	// Validate inputs.
	if hodlerPKID == nil {
		return nil, errors.New("UtxoView.GetUnlockableLockedBalanceEntries: nil hodlerPKID provided as input")
	}
	if profilePKID == nil {
		return nil, errors.New("UtxoView.GetUnlockableLockedBalanceEntries: nil profilePKID provided as input")
	}

	// First, pull unlockable LockedBalanceEntries from the db and cache them in the UtxoView.
	dbUnlockableLockedBalanceEntries, err := DBGetUnlockableLockedBalanceEntries(
		bav.Handle, bav.Snapshot, hodlerPKID, profilePKID, currentTimestampNanoSecs)
	if err != nil {
		return nil, errors.Wrapf(err, "UtxoView.GetUnlockableLockedBalanceEntries")
	}
	for _, lockedBalanceEntry := range dbUnlockableLockedBalanceEntries {
		// Cache results in the UtxoView.
		if _, exists := bav.LockedBalanceEntryKeyToLockedBalanceEntry[lockedBalanceEntry.ToMapKey()]; !exists {
			bav._setLockedBalanceEntry(lockedBalanceEntry)
		}
	}

	// Then, pull unlockable LockedBalanceEntries from the UtxoView.
	var unlockableLockedBalanceEntries []*LockedBalanceEntry
	for _, lockedBalanceEntry := range bav.LockedBalanceEntryKeyToLockedBalanceEntry {
		// Filter to matching LockedBalanceEntries.
		if !lockedBalanceEntry.HODLerPKID.Eq(hodlerPKID) ||
			!lockedBalanceEntry.ProfilePKID.Eq(profilePKID) ||
			lockedBalanceEntry.UnlockTimestampNanoSecs > currentTimestampNanoSecs ||
			lockedBalanceEntry.BalanceBaseUnits.IsZero() ||
			lockedBalanceEntry.isDeleted {
			continue
		}
		unlockableLockedBalanceEntries = append(unlockableLockedBalanceEntries, lockedBalanceEntry)
	}

	// Sort UnlockableLockedBalanceEntries by timestamp ASC.
	sort.Slice(unlockableLockedBalanceEntries, func(ii, jj int) bool {
		return unlockableLockedBalanceEntries[ii].UnlockTimestampNanoSecs <
			unlockableLockedBalanceEntries[jj].UnlockTimestampNanoSecs
	})
	return unlockableLockedBalanceEntries, nil
}

//
// TYPES: LockupYieldCurvePoint
//

type LockupYieldCurvePoint struct {
	ProfilePKID               *PKID
	LockupDurationNanoSecs    int64
	LockupYieldAPYBasisPoints uint64
	isDeleted                 bool
}

type LockupYieldCurvePointKey struct {
	ProfilePKID            *PKID
	LockupDurationNanoSecs int64
}

func (lockupYieldCurvePoint *LockupYieldCurvePoint) Copy() *LockupYieldCurvePoint {
	return &LockupYieldCurvePoint{
		ProfilePKID:               lockupYieldCurvePoint.ProfilePKID.NewPKID(),
		LockupDurationNanoSecs:    lockupYieldCurvePoint.LockupDurationNanoSecs,
		LockupYieldAPYBasisPoints: lockupYieldCurvePoint.LockupYieldAPYBasisPoints,
	}
}

func (lockupYieldCurvePoint *LockupYieldCurvePoint) Eq(other *LockupYieldCurvePoint) bool {
	return lockupYieldCurvePoint.ToMapKey() == other.ToMapKey()
}

func (lockupYieldCurvePoint *LockupYieldCurvePoint) ToMapKey() LockupYieldCurvePointKey {
	return LockupYieldCurvePointKey{
		ProfilePKID:            lockupYieldCurvePoint.ProfilePKID,
		LockupDurationNanoSecs: lockupYieldCurvePoint.LockupDurationNanoSecs,
	}
}

// DeSoEncoder Interface Implementation for LockupYieldCurvePoint

func (lockupYieldCurvePoint *LockupYieldCurvePoint) RawEncodeWithoutMetadata(blockHeight uint64, skipMetadata ...bool) []byte {
	var data []byte
	data = append(data, EncodeToBytes(blockHeight, lockupYieldCurvePoint.ProfilePKID, skipMetadata...)...)
	data = append(data, UintToBuf(uint64(lockupYieldCurvePoint.LockupDurationNanoSecs))...)
	data = append(data, UintToBuf(lockupYieldCurvePoint.LockupYieldAPYBasisPoints)...)
	return data
}

func (lockupYieldCurvePoint *LockupYieldCurvePoint) RawDecodeWithoutMetadata(blockHeight uint64, rr *bytes.Reader) error {
	var err error

	// ProfilePKID
	lockupYieldCurvePoint.ProfilePKID, err = DecodeDeSoEncoder(&PKID{}, rr)
	if err != nil {
		return errors.Wrapf(err, "LockupYieldCurvePoint.Decode: Problem reading ProfilePKID")
	}

	// LockupDurationNanoSecs
	uint64LockupDurationNanoSecs, err := ReadUvarint(rr)
	if err != nil {
		return errors.Wrapf(err, "LockupYieldCurvePoint.Decode: Problem reading LockupDurationNanoSecs")
	}
	lockupYieldCurvePoint.LockupDurationNanoSecs = int64(uint64LockupDurationNanoSecs)

	// LockupYieldAPYBasisPoints
	lockupYieldAPYBasisPoints, err := ReadUvarint(rr)
	if err != nil {
		return errors.Wrapf(err, "LockupYieldCurvePoint.Decode: Problem reading LockupYieldAPYBasisPoints")
	}
	lockupYieldCurvePoint.LockupYieldAPYBasisPoints = lockupYieldAPYBasisPoints

	return err
}

func (lockupYieldCurvePoint *LockupYieldCurvePoint) GetVersionByte(blockHeight uint64) byte {
	return 0
}

func (lockupYieldCurvePoint *LockupYieldCurvePoint) GetEncoderType() EncoderType {
	return EncoderTypeLockupYieldCurvePoint
}

// Set and Delete Functions for LockupYieldCurvePoints

func (bav *UtxoView) _setLockupYieldCurvePoint(point *LockupYieldCurvePoint) {
	// This function shouldn't be called with nil.
	if point == nil {
		glog.Errorf("_setLockupYieldCurvePoint: Called with nil LockupYieldCurvePoint; " +
			"this should never happen.")
		return
	}

	// Check if the PKID needs a map added to the view.
	if _, mapExists := bav.PKIDToLockupYieldCurvePointKeyToLockupYieldCurvePoints[*point.ProfilePKID]; !mapExists {
		bav.PKIDToLockupYieldCurvePointKeyToLockupYieldCurvePoints[*point.ProfilePKID] =
			make(map[LockupYieldCurvePointKey]*LockupYieldCurvePoint)
	}

	// Set the LockupYieldCurvePoint in the view.
	bav.PKIDToLockupYieldCurvePointKeyToLockupYieldCurvePoints[*point.ProfilePKID][point.ToMapKey()] = point
}

func (bav *UtxoView) _deleteLockupYieldCurvePoint(point *LockupYieldCurvePoint) {
	// Create a tombstone entry.
	tombstoneLockupYieldCurvePoint := point.Copy()
	tombstoneLockupYieldCurvePoint.isDeleted = true

	// Set the LockupYieldCurvePoint as deleted in the view.
	bav._setLockupYieldCurvePoint(tombstoneLockupYieldCurvePoint)
}

// Get Helper Functions for LockupYieldCurvePoint

func (bav *UtxoView) GetYieldCurvePointByProfilePKIDAndDurationNanoSecs(profilePKID *PKID,
	lockupDurationNanoSecs int64) (_lockupYieldCurvePoint *LockupYieldCurvePoint) {
	var lockupYieldCurvePoint *LockupYieldCurvePoint

	// Check the view for a yield curve point.
	if _, pointsInView := bav.PKIDToLockupYieldCurvePointKeyToLockupYieldCurvePoints[*profilePKID]; pointsInView {
		lockupYieldCurvePointKey := (&LockupYieldCurvePoint{
			ProfilePKID:            profilePKID,
			LockupDurationNanoSecs: lockupDurationNanoSecs,
		}).ToMapKey()
		if inMemoryYieldCurvePoint, pointExists :=
			bav.PKIDToLockupYieldCurvePointKeyToLockupYieldCurvePoints[*profilePKID][lockupYieldCurvePointKey]; pointExists {
			return inMemoryYieldCurvePoint
		}
	}

	// No mapping exists in the view, check for an entry in the DB.
	lockupYieldCurvePoint = DBGetYieldCurvePointsByProfilePKIDAndDurationNanoSecs(bav.GetDbAdapter().badgerDb,
		bav.Snapshot, profilePKID, lockupDurationNanoSecs)

	// Cache the DB entry in the in-memory map.
	if lockupYieldCurvePoint != nil {
		bav._setLockupYieldCurvePoint(lockupYieldCurvePoint)
	}

	return lockupYieldCurvePoint
}

func (bav *UtxoView) GetLocalYieldCurvePoints(profilePKID *PKID, lockupDuration int64) (
	_leftLockupPoint *LockupYieldCurvePoint, _rightLockupPoint *LockupYieldCurvePoint, _err error) {
	var leftLockupPoint *LockupYieldCurvePoint
	var rightLockupPoint *LockupYieldCurvePoint

	// Fetch all yield curve points in the db.
	dbYieldCurvePoints, err := DBGetAllYieldCurvePointsByProfilePKID(
		bav.GetDbAdapter().badgerDb, bav.Snapshot, profilePKID)
	if err != nil {
		return nil, nil, errors.Wrap(err, "GetLocalYieldCurvePoints")
	}

	// Cache the db points in the view.
	// While there's more efficient ways to do this with specialized badger seek operations, this is sufficient for now.
	if len(dbYieldCurvePoints) > 0 {
		// Check if there's a yield curve in the view for the associated profile.
		if _, mapInView := bav.PKIDToLockupYieldCurvePointKeyToLockupYieldCurvePoints[*profilePKID]; !mapInView {
			bav.PKIDToLockupYieldCurvePointKeyToLockupYieldCurvePoints[*profilePKID] =
				make(map[LockupYieldCurvePointKey]*LockupYieldCurvePoint)
		}

		// Check if any of the points needs to be cached in the view.
		for _, yieldCurvePoint := range dbYieldCurvePoints {
			if _, pointInView :=
				bav.PKIDToLockupYieldCurvePointKeyToLockupYieldCurvePoints[*profilePKID][yieldCurvePoint.ToMapKey()]; !pointInView {
				bav._setLockupYieldCurvePoint(yieldCurvePoint)
			}
		}
	}

	// Check the view for yield curve points.
	if _, pointsInView := bav.PKIDToLockupYieldCurvePointKeyToLockupYieldCurvePoints[*profilePKID]; pointsInView {
		for _, lockupYieldCurvePoint := range bav.PKIDToLockupYieldCurvePointKeyToLockupYieldCurvePoints[*profilePKID] {
			// Ensure the point is not deleted.
			if lockupYieldCurvePoint.isDeleted {
				continue
			}

			// Check for nil pointer cases.
			if lockupYieldCurvePoint.LockupDurationNanoSecs < lockupDuration && leftLockupPoint == nil {
				leftLockupPoint = lockupYieldCurvePoint
			}
			if lockupYieldCurvePoint.LockupDurationNanoSecs >= lockupDuration && rightLockupPoint == nil {
				rightLockupPoint = lockupYieldCurvePoint
			}

			// Check if the point is "more left" than the current left point.
			if lockupYieldCurvePoint.LockupDurationNanoSecs < lockupDuration &&
				lockupYieldCurvePoint.LockupDurationNanoSecs > leftLockupPoint.LockupDurationNanoSecs {
				leftLockupPoint = lockupYieldCurvePoint.Copy()
			}

			// Check if the point is "more right" than the current right point.
			if lockupYieldCurvePoint.LockupDurationNanoSecs >= lockupDuration &&
				lockupYieldCurvePoint.LockupDurationNanoSecs < rightLockupPoint.LockupDurationNanoSecs {
				rightLockupPoint = lockupYieldCurvePoint.Copy()
			}
		}
	}

	return leftLockupPoint, rightLockupPoint, nil
}

//
// TYPES: CoinLockupMetadata
//

type CoinLockupMetadata struct {
	ProfilePublicKey        *PublicKey
	UnlockTimestampNanoSecs int64
	LockupAmountBaseUnits   *uint256.Int
}

func (txnData *CoinLockupMetadata) GetTxnType() TxnType {
	return TxnTypeCoinLockup
}

func (txnData *CoinLockupMetadata) ToBytes(preSignature bool) ([]byte, error) {
	var data []byte
	data = append(data, EncodeByteArray(txnData.ProfilePublicKey.ToBytes())...)
	data = append(data, UintToBuf(uint64(txnData.UnlockTimestampNanoSecs))...)
	data = append(data, VariableEncodeUint256(txnData.LockupAmountBaseUnits)...)
	return data, nil
}

func (txnData *CoinLockupMetadata) FromBytes(data []byte) error {
	rr := bytes.NewReader(data)

	// ProfilePublicKey
	profilePublicKeyBytes, err := DecodeByteArray(rr)
	if err != nil {
		return errors.Wrapf(err, "CoinLockupMetadata.FromBytes: Problem reading ProfilePublicKey")
	}
	txnData.ProfilePublicKey = NewPublicKey(profilePublicKeyBytes)

	// UnlockTimestampNanoSecs
	uint64UnlockTimestampNanoSecs, err := ReadUvarint(rr)
	if err != nil {
		return errors.Wrapf(err, "CoinLockupMetadata.FromBytes: Problem reading UnlockTimestampNanoSecs")
	}
	txnData.UnlockTimestampNanoSecs = int64(uint64UnlockTimestampNanoSecs)

	// LockupAmountBaseUnits
	txnData.LockupAmountBaseUnits, err = VariableDecodeUint256(rr)
	if err != nil {
		return errors.Wrapf(err, "CoinLockupMetadata.FromBytes: Problem reading LockupAmountBaseUnits")
	}

	return nil
}

func (txnData *CoinLockupMetadata) New() DeSoTxnMetadata {
	return &CoinLockupMetadata{}
}

//
// TYPES: UpdateDAOCoinLockupParamsMetadata
//

type UpdateCoinLockupParamsMetadata struct {
	// LockupYieldDurationNanoSecs and LockupYieldAPYBasisPoints describe a coordinate pair
	// of (duration, APY yield) on a yield curve.
	//
	// A yield curve consists of a series of (duration, APY yield) points. For example,
	// the following points describe a simple yield curve:
	//              {(6mo, 3%), (12mo, 3.5%), (18mo, 4%), (24mo, 4.5%)}
	//
	// Assuming RemoveYieldCurvePoint is false:
	//    The point (LockupYieldDurationNanoSecs, LockupYieldAPYBasisPoints)
	//    is added to the profile's yield curve. If a point with the same duration already exists
	//    on the profile's yield curve, it will be updated with the new yield.
	//    Note if LockupYieldDurationNanoSecs=0, nothing is modified or added at t=0.
	// Assuming RemoveYieldCurvePoint is true:
	//    The point (LockupYieldDurationNanoSecs, XXX) is removed from the profile's yield curve.
	//    Note that LockupYieldAPYBasisPoints is ignored in this transaction.
	//
	// By setting LockupYieldDurationNanoSecs to zero, the yield curve attached to the profile
	// is left unmodified. In any UpdateDAOCoinLockupParams transaction looking to modify only
	// LockupTransferRestrictions, LockupYieldDurationNanoSecs would be set to zero.
	LockupYieldDurationNanoSecs int64
	LockupYieldAPYBasisPoints   uint64
	RemoveYieldCurvePoint       bool

	// When NewLockupTransferRestrictions is set true, the TransferRestrictionStatus specified
	// in the transaction is updated in the transactor's profile for locked coins.
	// Any subsequent transfers utilizing the transactor's locked coins are validated against
	// the updated locked transfer restriction status.
	NewLockupTransferRestrictions   bool
	LockupTransferRestrictionStatus TransferRestrictionStatus
}

func (txnData *UpdateCoinLockupParamsMetadata) GetTxnType() TxnType {
	return TxnTypeUpdateCoinLockupParams
}

func (txnData *UpdateCoinLockupParamsMetadata) ToBytes(preSignature bool) ([]byte, error) {
	var data []byte
	data = append(data, UintToBuf(uint64(txnData.LockupYieldDurationNanoSecs))...)
	data = append(data, UintToBuf(txnData.LockupYieldAPYBasisPoints)...)
	data = append(data, BoolToByte(txnData.RemoveYieldCurvePoint))
	data = append(data, BoolToByte(txnData.NewLockupTransferRestrictions))
	data = append(data, byte(txnData.LockupTransferRestrictionStatus))
	return data, nil
}

func (txnData *UpdateCoinLockupParamsMetadata) FromBytes(data []byte) error {
	rr := bytes.NewReader(data)

	lockupYieldDurationNanoSecs, err := ReadUvarint(rr)
	if err != nil {
		return errors.Wrapf(err, "UpdateDAOCoinLockupParams.FromBytes: Problem reading LockupYieldDurationNanoSecs")
	}
	txnData.LockupYieldDurationNanoSecs = int64(lockupYieldDurationNanoSecs)

	txnData.LockupYieldAPYBasisPoints, err = ReadUvarint(rr)
	if err != nil {
		return errors.Wrapf(err, "UpdateDAOCoinLockupParams.FromBytes: Problem reading LockupYieldAPYBasisPoints")
	}

	txnData.RemoveYieldCurvePoint, err = ReadBoolByte(rr)
	if err != nil {
		return errors.Wrapf(err, "UpdateDAOCoinLockupParams.FromBytes: Problem reading RemoveYieldCurvePoint")
	}

	txnData.NewLockupTransferRestrictions, err = ReadBoolByte(rr)
	if err != nil {
		return errors.Wrapf(err, "UpdateDAOCoinLockupParams.FromBytes: Problem reading NewLockupTransferRestrictions")
	}

	lockedStatusByte, err := rr.ReadByte()
	if err != nil {
		return errors.Wrapf(err, "UpdateDAOCoinLockupParams.FromBytes: Problem reading LockupTransferRestrictionStatus")
	}
	txnData.LockupTransferRestrictionStatus = TransferRestrictionStatus(lockedStatusByte)

	return nil
}

func (txnData *UpdateCoinLockupParamsMetadata) New() DeSoTxnMetadata {
	return &UpdateCoinLockupParamsMetadata{}
}

//
// TYPES: DAOCoinLockupTransferMetadata
//

type CoinLockupTransferMetadata struct {
	RecipientPublicKey             *PublicKey
	ProfilePublicKey               *PublicKey
	UnlockTimestampUnixNanoSecs    int64
	LockedCoinsToTransferBaseUnits *uint256.Int
}

func (txnData *CoinLockupTransferMetadata) GetTxnType() TxnType {
	return TxnTypeCoinLockupTransfer
}

func (txnData *CoinLockupTransferMetadata) ToBytes(preSignature bool) ([]byte, error) {
	var data []byte
	data = append(data, EncodeByteArray(txnData.RecipientPublicKey.ToBytes())...)
	data = append(data, EncodeByteArray(txnData.ProfilePublicKey.ToBytes())...)
	data = append(data, UintToBuf(uint64(txnData.UnlockTimestampUnixNanoSecs))...)
	data = append(data, VariableEncodeUint256(txnData.LockedCoinsToTransferBaseUnits)...)
	return data, nil
}

func (txnData *CoinLockupTransferMetadata) FromBytes(data []byte) error {
	rr := bytes.NewReader(data)

	// RecipientPublicKey
	recipientPublicKeyBytes, err := DecodeByteArray(rr)
	if err != nil {
		return errors.Wrapf(err, "DAOCoinLockupTransferMetadata.FromBytes: Problem reading RecipientPublicKey")
	}
	txnData.RecipientPublicKey = NewPublicKey(recipientPublicKeyBytes)

	// ProfilePublicKey
	profilePublicKeyBytes, err := DecodeByteArray(rr)
	if err != nil {
		return errors.Wrapf(err, "DAOCoinLockupTransferMetadata.FromBytes: Problem reading ProfilePublicKey")
	}
	txnData.ProfilePublicKey = NewPublicKey(profilePublicKeyBytes)

	// UnlockTimestampNanoSecs
	uint64UnlockTimestampUnixNanoSecs, err := ReadUvarint(rr)
	if err != nil {
		return errors.Wrapf(err, "DAOCoinLockupTransferMetadata.FromBytes: Problem reading UnlockTimestampNanoSecs")
	}
	txnData.UnlockTimestampUnixNanoSecs = int64(uint64UnlockTimestampUnixNanoSecs)

	// LockedDAOCoinToTransferBaseUnits
	txnData.LockedCoinsToTransferBaseUnits, err = VariableDecodeUint256(rr)
	if err != nil {
		return errors.Wrapf(err, "DAOCoinLockupTransferMetadata.FromBytes: Problem reading LockedDAOCoinToTransferBaseUnits")
	}

	return nil
}

func (txnData *CoinLockupTransferMetadata) New() DeSoTxnMetadata {
	return &CoinLockupTransferMetadata{}
}

//
// TYPES: CoinUnlockMetadata
//

type CoinUnlockMetadata struct {
	ProfilePublicKey *PublicKey
}

func (txnData *CoinUnlockMetadata) GetTxnType() TxnType {
	return TxnTypeCoinUnlock
}

func (txnData *CoinUnlockMetadata) ToBytes(preSignature bool) ([]byte, error) {
	var data []byte
	data = append(data, EncodeByteArray(txnData.ProfilePublicKey.ToBytes())...)
	return data, nil
}

func (txnData *CoinUnlockMetadata) FromBytes(data []byte) error {
	rr := bytes.NewReader(data)

	// ProfilePublicKey
	profilePublicKeyBytes, err := DecodeByteArray(rr)
	if err != nil {
		return errors.Wrapf(err, "CoinUnlockMetadata.FromBytes: Problem reading ProfilePublicKey")
	}
	txnData.ProfilePublicKey = NewPublicKey(profilePublicKeyBytes)

	return nil
}

func (txnData *CoinUnlockMetadata) New() DeSoTxnMetadata {
	return &CoinUnlockMetadata{}
}

//
// CoinLockup Transaction Logic
//

func (bav *UtxoView) _connectCoinLockup(
	txn *MsgDeSoTxn, txHash *BlockHash,
	blockHeight uint32, blockTimestamp int64,
	verifySignatures bool) (_totalInput uint64,
	_totalOutput uint64, _utxoOps []*UtxoOperation, _err error) {
	var utxoOpsForTxn []*UtxoOperation

	// Validate the starting block height.
	if blockHeight < bav.Params.ForkHeights.ProofOfStake1StateSetupBlockHeight ||
		blockHeight < bav.Params.ForkHeights.BalanceModelBlockHeight {
		return 0, 0, nil,
			errors.Wrapf(RuleErrorLockupTxnBeforeBlockHeight, "_connectCoinLockup")
	}

	// Validate the txn TxnType.
	if txn.TxnMeta.GetTxnType() != TxnTypeCoinLockup {
		return 0, 0, nil, fmt.Errorf(
			"_connectCoinLockup: called with bad TxnType %s", txn.TxnMeta.GetTxnType().String(),
		)
	}

	// Try connecting the basic transfer without considering transaction metadata.
	_, _, utxoOpsForBasicTransfer, err := bav._connectBasicTransfer(txn, txHash, blockHeight, verifySignatures)
	if err != nil {
		return 0, 0, nil, errors.Wrapf(err, "_connectCoinLockup")
	}
	utxoOpsForTxn = append(utxoOpsForTxn, utxoOpsForBasicTransfer...)

	// Grab the txn metadata.
	txMeta := txn.TxnMeta.(*CoinLockupMetadata)

	// Check that the target profile public key is valid and that a profile corresponding to that public key exists.
	var profileEntry *ProfileEntry
	if len(txMeta.ProfilePublicKey) != btcec.PubKeyBytesLenCompressed {
		return 0, 0, nil,
			errors.Wrap(RuleErrorCoinLockupInvalidProfilePubKey, "_connectCoinLockup")
	}
	if !txMeta.ProfilePublicKey.IsZeroPublicKey() {
		profileEntry = bav.GetProfileEntryForPublicKey(txMeta.ProfilePublicKey.ToBytes())
		if profileEntry == nil || profileEntry.isDeleted {
			return 0, 0, nil,
				errors.Wrap(RuleErrorCoinLockupOnNonExistentProfile, "_connectCoinLockup")
		}
	}

	// Validate the lockup amount as non-zero. This is meant to prevent wasteful "no-op" transactions.
	if txMeta.LockupAmountBaseUnits.IsZero() {
		return 0, 0, nil,
			errors.Wrap(RuleErrorCoinLockupOfAmountZero, "_connectCoinLockup")
	}

	// If this is a DeSo lockup, ensure the amount is less than 2**64 (maximum DeSo balance).
	if txMeta.ProfilePublicKey.IsZeroPublicKey() && !txMeta.LockupAmountBaseUnits.IsUint64() {
		return 0, 0, nil,
			errors.Wrap(RuleErrorCoinLockupExcessiveDeSoLockup, "_connectCoinLockup")
	}

	// Validate the lockup expires in the future.
	if txMeta.UnlockTimestampNanoSecs <= blockTimestamp {
		return 0, 0, nil,
			errors.Wrap(RuleErrorCoinLockupInvalidLockupDuration, "_connectCoinLockup")
	}

	// Compute the lockup duration in nanoseconds.
	lockupDurationNanoSeconds := txMeta.UnlockTimestampNanoSecs - blockTimestamp

	// Determine the hodler PKID to use.
	transactorPKIDEntry := bav.GetPKIDForPublicKey(txn.PublicKey)
	if transactorPKIDEntry == nil || transactorPKIDEntry.isDeleted {
		return 0, 0, nil,
			errors.Wrap(RuleErrorCoinLockupInvalidTransactorPKID, "_connectCoinLockup")
	}
	hodlerPKID := transactorPKIDEntry.PKID

	// Determine which profile PKID to use.
	var profilePKID *PKID
	if txMeta.ProfilePublicKey.IsZeroPublicKey() {
		profilePKID = ZeroPKID.NewPKID()
	} else {
		profilePKIDEntry := bav.GetPKIDForPublicKey(txMeta.ProfilePublicKey.ToBytes())
		if profilePKIDEntry == nil || profilePKIDEntry.isDeleted {
			return 0, 0, nil,
				errors.Wrap(RuleErrorCoinLockupNonExistentProfile, "_connectCoinLockup")
		}
		profilePKID = profilePKIDEntry.PKID.NewPKID()
	}

	// Validate the transactor as having sufficient DAO Coin or DESO balance for the transaction.
	var transactorBalanceNanos256 *uint256.Int
	var prevTransactorBalanceEntry *BalanceEntry
	if profilePKID.IsZeroPKID() {
		// Check the DeSo balance of the user.
		transactorBalanceNanos, err := bav.GetDeSoBalanceNanosForPublicKey(txn.PublicKey)
		if err != nil {
			return 0, 0, nil, errors.Wrap(err, "_connectCoinLockup")
		}

		// Construct a uint256 balance and validate the transactor as having sufficient DeSo.
		transactorBalanceNanos256, _ = uint256.FromBig(big.NewInt(0).SetUint64(transactorBalanceNanos))
		if txMeta.LockupAmountBaseUnits.Gt(transactorBalanceNanos256) {
			return 0, 0, nil,
				errors.Wrap(RuleErrorCoinLockupInsufficientDeSo, "_connectCoinLockup")
		}

		// Spend the transactor's DeSo balance.
		lockupAmount64 := txMeta.LockupAmountBaseUnits.Uint64()
		newUtxoOp, err := bav._spendBalance(lockupAmount64, txn.PublicKey, blockHeight)
		if err != nil {
			return 0, 0, nil, errors.Wrapf(err, "_connectCoinLockup")
		}
		utxoOpsForTxn = append(utxoOpsForTxn, newUtxoOp)
	} else {
		// Check the BalanceEntry of the user.
		transactorBalanceEntry, _, _ := bav.GetBalanceEntryForHODLerPubKeyAndCreatorPubKey(
			txn.PublicKey,
			txMeta.ProfilePublicKey.ToBytes(),
			true)
		if transactorBalanceEntry == nil || transactorBalanceEntry.isDeleted {
			return 0, 0, nil,
				errors.Wrapf(RuleErrorCoinLockupBalanceEntryDoesNotExist, "_connectCoinLockup")
		}

		// Validate the balance entry as having sufficient funds.
		transactorBalanceNanos256 = transactorBalanceEntry.BalanceNanos.Clone()
		if txMeta.LockupAmountBaseUnits.Gt(transactorBalanceNanos256) {
			return 0, 0, nil,
				errors.Wrap(RuleErrorCoinLockupInsufficientCoins, "_connectCoinLockup")
		}

		// We store the previous transactor balance entry in the event we need to revert the transaction.
		prevTransactorBalanceEntry = transactorBalanceEntry

		// Spend the transactor's DAO coin balance.
		transactorBalanceEntry.BalanceNanos =
			*uint256.NewInt().Sub(&transactorBalanceEntry.BalanceNanos, txMeta.LockupAmountBaseUnits)
		bav._setDAOCoinBalanceEntryMappings(transactorBalanceEntry)
	}

	// By now we know the transaction to be valid. We now source yield information from either
	// the profile's yield curve or the raw DeSo yield curve. Because there's some choice in how
	// to determine the yield when the lockup duration falls between two profile specified yield curve
	// points, we return here the two local points and choose/interpolate between them below.
	leftYieldCurvePoint, rightYieldCurvePoint, err := bav.GetLocalYieldCurvePoints(profilePKID, lockupDurationNanoSeconds)
	if err != nil {
		return 0, 0, nil,
			errors.Wrap(err, "_connectCoinLockup failed to fetch yield curve points")
	}

	// Here we interpolate (choose) the yield between the two returned local yield curve points.
	//
	// If we fall between two points, we choose the left yield curve point (i.e. the one with lesser lockup duration).
	// The transactor earns yield only for the lockup duration specified by the left yield curve point but will
	// be unable to unlock the coins until the transaction specified lockup duration expires.
	txnYieldBasisPoints := uint64(0)
	txnYieldEarningDurationNanoSecs := int64(0)
	if leftYieldCurvePoint.LockupDurationNanoSecs < lockupDurationNanoSeconds {
		txnYieldBasisPoints = leftYieldCurvePoint.LockupYieldAPYBasisPoints
		txnYieldEarningDurationNanoSecs = leftYieldCurvePoint.LockupDurationNanoSecs
	}
	if rightYieldCurvePoint.LockupDurationNanoSecs == lockupDurationNanoSeconds {
		txnYieldBasisPoints = rightYieldCurvePoint.LockupYieldAPYBasisPoints
		txnYieldEarningDurationNanoSecs = rightYieldCurvePoint.LockupDurationNanoSecs
	}

	// Convert variables to a consistent uint256 representation. This is to use them in SafeUint256 math.
	txnYieldBasisPoints256 := uint256.NewInt().SetUint64(txnYieldBasisPoints)
	txnYieldEarningDurationNanoSecs256 := uint256.NewInt().SetUint64(uint64(txnYieldEarningDurationNanoSecs))

	// Compute the yield associated with this operation, checking to ensure there's no overflow.
	yieldFromTxn, err :=
		CalculateLockupYield(txMeta.LockupAmountBaseUnits, txnYieldBasisPoints256, txnYieldEarningDurationNanoSecs256)
	if err != nil {
		return 0, 0, nil, errors.Wrap(err, "_connectCoinLockup")
	}

	// We check that the minted yield does not cause an overflow in the transactor's balance.
	// In the case of DeSo being locked up, we must check that the resulting amount is less than 2**64.
	if uint256.NewInt().Sub(MaxUint256, yieldFromTxn).Lt(transactorBalanceNanos256) {
		return 0, 0, nil,
			errors.Wrap(RuleErrorCoinLockupYieldCausesOverflow, "_connectCoinLockup")
	}

	// Compute the amount to be added to the locked balance entry.
	lockupValue := *uint256.NewInt().Add(transactorBalanceNanos256, yieldFromTxn)

	// In the case of DeSo being locked up, we ensure that the resulting amount is less than 2**64.
	if profilePKID.IsZeroPKID() && !lockupValue.IsUint64() {
		return 0, 0, nil,
			errors.Wrap(RuleErrorCoinLockupYieldCausesOverflow, "_connectCoinLockup")
	}

	// NOTE: While we could check for "global" overflow here, we let this occur on the unlock transaction instead.
	//       Global overflow is where the yield causes fields like CoinEntry.CoinsInCirculationNanos to overflow.
	//       Performing the check here would be redundant and may lead to worse UX in the case of coins being
	//       burned in the future making current lockups no longer an overflow. Checking here would also
	//       create a DoS attack vector where a malicious entity takes out an extremely long-dated lockup
	//       with the sole intent of saturating the CoinsInCirculationNanos field preventing others from locking up.

	// For consolidation, we fetch equivalent LockedBalanceEntries.
	lockedBalanceEntry, err := bav.GetLockedBalanceEntryForHODLerPKIDProfilePKIDUnlockTimestampNanoSecs(
		hodlerPKID, profilePKID, txMeta.UnlockTimestampNanoSecs)
	if err != nil {
		return 0, 0, nil,
			errors.Wrap(err, "_connectCoinLockup failed to fetch lockedBalanceEntry")
	}
	if lockedBalanceEntry == nil || lockedBalanceEntry.isDeleted {
		lockedBalanceEntry = &LockedBalanceEntry{
			HODLerPKID:              hodlerPKID,
			ProfilePKID:             profilePKID,
			UnlockTimestampNanoSecs: txMeta.UnlockTimestampNanoSecs,
			BalanceBaseUnits:        *uint256.NewInt(),
		}
	}
	previousLockedBalanceEntry := *lockedBalanceEntry

	// Check for overflow within the locked balance entry itself.
	if uint256.NewInt().Sub(MaxUint256, yieldFromTxn).Lt(transactorBalanceNanos256) {
		return 0, 0, nil,
			errors.Wrap(RuleErrorCoinLockupYieldCausesOverflow, "_connectCoinLockup")
	}
	if profilePKID.IsZeroPKID() {
		// Check if DeSo minted would overflow 2**64 in the transactor balance.
		if uint256.NewInt().Sub(uint256.NewInt().SetUint64(math.MaxUint64), yieldFromTxn).Lt(transactorBalanceNanos256) {
			return 0, 0, nil,
				errors.Wrap(RuleErrorCoinLockupYieldCausesOverflow, "_connectCoinLockup")
		}
	}

	// Increment the lockedBalanceEntry and update the view.
	lockedBalanceEntry.BalanceBaseUnits = *uint256.NewInt().Add(&lockedBalanceEntry.BalanceBaseUnits, &lockupValue)
	bav._setLockedBalanceEntry(lockedBalanceEntry)

	// Add a UtxoOperation for easy reversion during disconnect.
	utxoOpsForTxn = append(utxoOpsForTxn, &UtxoOperation{
		Type:                       OperationTypeCoinLockup,
		PrevTransactorBalanceEntry: prevTransactorBalanceEntry,
		PrevLockedBalanceEntry:     &previousLockedBalanceEntry,
	})

	// Construct UtxoOps in the event this transaction is reverted.
	return 0, 0, utxoOpsForTxn, nil
}

func CalculateLockupYield(
	principal *uint256.Int,
	apyYieldBasisPoints *uint256.Int,
	durationNanoSecs *uint256.Int,
) (*uint256.Int, error) {
	// Note: We could compute either simple or compounding interest. While compounding interest is ideal from an
	//       application perspective, it becomes incredibly difficult to implement from a numerical perspective.
	//       This is because compound interest requires fractional exponents rather for computing the yield.
	//       Determining overflow and preventing excessive money-printers becomes tricky in the compound interest case.
	//       For this reason, we opt to use simple interest.
	//
	// Simple interest formula:
	//       yield = principal * apy_yield * time_in_years
	//
	// Notice this formula makes detecting computational overflow trivial by utilizing the DeSo SafeUint256 library.

	// Compute the denominators from the nanosecond to year conversion and the basis point computation.
	denominators, err := SafeUint256().Mul(
		uint256.NewInt().SetUint64(_nanoSecsPerYear),
		uint256.NewInt().SetUint64(10000))
	if err != nil {
		return nil,
			errors.Wrap(RuleErrorCoinLockupCoinYieldOverflow, "CalculateLockupYield (nanoSecsPerYear * 10000)")
	}

	// Compute the numerators from the principal, apy yield, and time in nanoseconds.
	numerators, err := SafeUint256().Mul(principal, apyYieldBasisPoints)
	if err != nil {
		return nil,
			errors.Wrap(RuleErrorCoinLockupCoinYieldOverflow, "CalculateLockupYield (principal * yield)")
	}
	numerators, err = SafeUint256().Mul(numerators, durationNanoSecs)
	if err != nil {
		return nil,
			errors.Wrap(RuleErrorCoinLockupCoinYieldOverflow, "CalculateLockupYield ((principal * yield) * duration)")
	}

	// Compute the yield for the transaction.
	yield, err := SafeUint256().Div(numerators, denominators)
	if err != nil {
		return nil,
			errors.Wrap(err, "CalculateLockupYield (numerator / denominator)")
	}

	return yield, nil
}

func (bav *UtxoView) _disconnectCoinLockup(
	operationType OperationType,
	currentTxn *MsgDeSoTxn,
	txnHash *BlockHash,
	utxoOpsForTxn []*UtxoOperation,
	blockHeight uint32) error {

	if len(utxoOpsForTxn) == 0 {
		return fmt.Errorf("_disconnectCoinLockup: utxoOperations are missing")
	}
	operationIndex := len(utxoOpsForTxn) - 1

	// Verify the last operation as being a CoinLockup operation.
	if utxoOpsForTxn[operationIndex].Type != OperationTypeCoinLockup {
		return fmt.Errorf("_disconnectCoinLockup: Trying to revert "+
			"OperationTypeCoinLockup but found type %v", utxoOpsForTxn[operationIndex].Type)
	}

	// Sanity check the CoinLockup operation exists.
	operationData := utxoOpsForTxn[operationIndex]
	if operationData.PrevLockedBalanceEntry == nil || operationData.PrevLockedBalanceEntry.isDeleted {
		return fmt.Errorf("_disconnectCoinLockup: Trying to revert OperationTypeCoinLockup " +
			"but found nil or deleted previous locked balance entry")
	}
	operationIndex--
	if operationIndex < 0 {
		return fmt.Errorf("_disconnectCoinLockup: Trying to revert OperationTypeCoinLockup " +
			"but malformed utxoOpsForTxn")
	}

	// Sanity check the data within the CoinLockup. Reverting a lockup should not result in more coins.
	lockedBalanceEntry, err := bav.GetLockedBalanceEntryForHODLerPKIDProfilePKIDUnlockTimestampNanoSecs(
		operationData.PrevLockedBalanceEntry.HODLerPKID, operationData.PrevLockedBalanceEntry.ProfilePKID,
		operationData.PrevLockedBalanceEntry.UnlockTimestampNanoSecs)
	if err != nil {
		return errors.Wrap(err, "_disconnectCoinLockup failed to fetch current lockedBalanceEntry")
	}
	if lockedBalanceEntry == nil || lockedBalanceEntry.isDeleted {
		lockedBalanceEntry = &LockedBalanceEntry{
			HODLerPKID:              operationData.PrevLockedBalanceEntry.HODLerPKID,
			ProfilePKID:             operationData.PrevLockedBalanceEntry.ProfilePKID,
			UnlockTimestampNanoSecs: operationData.PrevLockedBalanceEntry.UnlockTimestampNanoSecs,
			BalanceBaseUnits:        *uint256.NewInt(),
		}
	}
	if lockedBalanceEntry.BalanceBaseUnits.Lt(&operationData.PrevLockedBalanceEntry.BalanceBaseUnits) {
		return fmt.Errorf("_disconnectCoinLockup: Reversion of coin lockup would result in " +
			"more coins in the lockup")
	}

	// Reset the transactor's LockedBalanceEntry to what it was previously.
	bav._setLockedBalanceEntry(operationData.PrevLockedBalanceEntry)

	// Depending on whether the lockup dealt with DeSo, we should have either a UtxoOp or a PrevTransactorBalanceEntry.
	isDeSoLockup := operationData.PrevLockedBalanceEntry.ProfilePKID.IsZeroPKID()
	if isDeSoLockup {
		// Revert the spent DeSo.
		operationData = utxoOpsForTxn[operationIndex]
		if operationData.Type != OperationTypeSpendBalance {
			return fmt.Errorf("_disconnectCoinLockup: Trying to revert OperationTypeSpendBalance "+
				"but found type %v", operationData.Type)
		}
		if !bytes.Equal(operationData.BalancePublicKey, currentTxn.PublicKey) {
			return fmt.Errorf("_disconnectCoinLockup: Trying to revert OperationTypeSpendBalance but found " +
				"mismatched public keys")
		}
		err := bav._unSpendBalance(operationData.BalanceAmountNanos, currentTxn.PublicKey)
		if err != nil {
			return errors.Wrapf(err, "_disconnectCoinLockup: Problem unSpending balance of %v "+
				"for the transactor", operationData.BalanceAmountNanos)
		}
		operationIndex--
		if operationIndex < 0 {
			return fmt.Errorf("_disconnectCoinLockup: Trying to revert OperationTypeDAOCoinLockup " +
				"but malformed utxoOpsForTxn")
		}
	} else {
		// Revert the transactor's DAO coin balance.
		bav._setBalanceEntryMappings(operationData.PrevTransactorBalanceEntry, true)
	}

	// By here we only need to disconnect the basic transfer associated with the transaction.
	basicTransferOps := utxoOpsForTxn[:operationIndex]
	err = bav._disconnectBasicTransfer(currentTxn, txnHash, basicTransferOps, blockHeight)
	if err != nil {
		return errors.Wrapf(err, "_disconnectCoinLockup")
	}
	return nil
}

//
// UpdateCoinLockupParams Transaction Logic
//

func (bav *UtxoView) _connectUpdateCoinLockupParams(
	txn *MsgDeSoTxn,
	txHash *BlockHash,
	blockHeight uint32,
	verifySignatures bool,
) (_totalInput uint64,
	_totalOutput uint64,
	_utxoOps []*UtxoOperation,
	_err error) {
	var utxoOpsForTxn []*UtxoOperation

	// Validate the starting block height.
	if blockHeight < bav.Params.ForkHeights.ProofOfStake1StateSetupBlockHeight ||
		blockHeight < bav.Params.ForkHeights.BalanceModelBlockHeight {
		return 0, 0, nil, errors.Wrapf(RuleErrorProofofStakeTxnBeforeBlockHeight, "_connectDAOCoinLockup")
	}

	// Validate the txn TxnType.
	if txn.TxnMeta.GetTxnType() != TxnTypeUpdateCoinLockupParams {
		return 0, 0, nil, fmt.Errorf("_connectUpdateCoinLockupParams: "+
			"called with bad TxnType %s", txn.TxnMeta.GetTxnType().String())
	}

	// Try connecting the basic transfer without considering transaction metadata.
	_, _, utxoOpsForBasicTransfer, err := bav._connectBasicTransfer(txn, txHash, blockHeight, verifySignatures)
	if err != nil {
		return 0, 0, nil, errors.Wrapf(err, "_connectUpdateCoinLockupParams")
	}
	utxoOpsForTxn = append(utxoOpsForTxn, utxoOpsForBasicTransfer...)

	// Grab the txn metadata.
	txMeta := txn.TxnMeta.(*UpdateCoinLockupParamsMetadata)

	// Get the profilePKID from the transactor public key.
	var profilePKID *PKID
	_, updaterIsParamUpdater := GetParamUpdaterPublicKeys(blockHeight, bav.Params)[MakePkMapKey(txn.PublicKey)]
	if updaterIsParamUpdater {
		profilePKID = ZeroPKID.NewPKID()
	} else {
		profilePKIDEntry := bav.GetPKIDForPublicKey(txn.PublicKey)
		if profilePKIDEntry == nil || profilePKIDEntry.isDeleted {
			return 0, 0, nil, errors.Wrap(RuleErrorUpdateCoinLockupParamsOnInvalidPKID,
				"_connectUpdateCoinLockupParams")
		}
		profilePKID = profilePKIDEntry.PKID
	}

	// Sanity check the lockup duration as valid.
	if txMeta.LockupYieldDurationNanoSecs < 0 {
		return 0, 0, nil, errors.Wrapf(RuleErrorUpdateCoinLockupParamsNegativeDuration,
			"_connectUpdateCoinLockupParams")
	}

	// Fetch the previous yield curve point associated with this <profilePKID, lockupDurationNanoSecs> pair.
	prevLockupYieldCurvePoint :=
		bav.GetYieldCurvePointByProfilePKIDAndDurationNanoSecs(profilePKID, txMeta.LockupYieldDurationNanoSecs)

	// Check if a yield curve point is being added.
	if !txMeta.RemoveYieldCurvePoint && txMeta.LockupYieldDurationNanoSecs > 0 {
		// NOTE: During the view flush, any comparable LockupYieldCurvePoint with the unique
		//       <ProfilePKID, LockupDurationNanoSecs> pair will be deleted prior to this new
		//       point being added. Above we saved the previous LockupYieldCurvePoint
		//       in the even this is reverted.
		bav._setLockupYieldCurvePoint(&LockupYieldCurvePoint{
			ProfilePKID:               profilePKID,
			LockupDurationNanoSecs:    txMeta.LockupYieldDurationNanoSecs,
			LockupYieldAPYBasisPoints: txMeta.LockupYieldAPYBasisPoints,
		})
	}

	// Check if a yield curve point is being removed.
	if txMeta.RemoveYieldCurvePoint && txMeta.LockupYieldDurationNanoSecs > 0 {
		// Check that we're not deleting a point which doesn't exist. This ensures that disconnects function properly,
		// as well ensures there's no wasteful "no-ops" executed.
		if prevLockupYieldCurvePoint == nil {
			return 0, 0, nil,
				errors.Wrapf(RuleErrorUpdateCoinLockupParamsDeletingNonExistentPoint, "_connectUpdateCoinLockupParams")
		}

		// NOTE: The "LockupYieldAPYBasisPoints" field is effectively irrelevant here.
		//       The DB operations will seek to the unique <ProfilePKID, LockupDurationNanoSecs>
		//       pair and delete it during the view flush. The "isDeleted" field ensures
		//       nothing else is put in its place.
		bav._deleteLockupYieldCurvePoint(&LockupYieldCurvePoint{
			ProfilePKID:            profilePKID,
			LockupDurationNanoSecs: txMeta.LockupYieldDurationNanoSecs,
		})
	}

	// Check if we're updating transfer restriction.
	var prevLockupTransferRestriction TransferRestrictionStatus
	if txMeta.NewLockupTransferRestrictions {
		// Fetch the profile entry and LockupTransferRestriction status.
		profileEntry := bav.GetProfileEntryForPKID(profilePKID)
		if profileEntry == nil || profileEntry.isDeleted {
			return 0, 0, nil,
				errors.Wrapf(RuleErrorUpdateCoinLockupParamsUpdatingNonExistentProfile, "_connectUpdateCoinLockupParams")
		}

		// Store a copy of the previous LockupTransferRestrictionStatus for easy transaction disconnect.
		prevLockupTransferRestriction = profileEntry.DAOCoinEntry.LockupTransferRestrictionStatus

		// Ensure we're not updating a permanent transfer restriction.
		if prevLockupTransferRestriction == TransferRestrictionStatusPermanentlyUnrestricted {
			return 0, 0, nil, errors.Wrapf(
				RuleErrorUpdateCoinLockupParamsUpdatingPermanentTransferRestriction, "_connectUpdateCoinLockupParams")
		}

		// Check that the new transfer restrictions are valid.
		if !(txMeta.LockupTransferRestrictionStatus == TransferRestrictionStatusUnrestricted) &&
			!(txMeta.LockupTransferRestrictionStatus == TransferRestrictionStatusProfileOwnerOnly) &&
			!(txMeta.LockupTransferRestrictionStatus == TransferRestrictionStatusDAOMembersOnly) &&
			!(txMeta.LockupTransferRestrictionStatus == TransferRestrictionStatusPermanentlyUnrestricted) {
			return 0, 0, nil,
				errors.Wrapf(RuleErrorUpdateCoinLockupParamsInvalidRestrictions, "_connectUpdateCoinLockupParams")
		}

		// Update the transfer restrictions.
		profileEntry.DAOCoinEntry.LockupTransferRestrictionStatus = txMeta.LockupTransferRestrictionStatus
		bav._setProfileEntryMappings(profileEntry)
	}

	// Add a UtxoOperation for easy reversion during disconnect.
	utxoOpsForTxn = append(utxoOpsForTxn, &UtxoOperation{
		Type:                          OperationTypeUpdateCoinLockupParams,
		PrevLockupYieldCurvePoint:     prevLockupYieldCurvePoint,
		PrevLockupTransferRestriction: prevLockupTransferRestriction,
	})

	return 0, 0, utxoOpsForTxn, nil
}

func (bav *UtxoView) _disconnectUpdateCoinLockupParams(
	operationType OperationType,
	currentTxn *MsgDeSoTxn,
	txnHash *BlockHash,
	utxoOpsForTxn []*UtxoOperation,
	blockHeight uint32) error {

	if len(utxoOpsForTxn) == 0 {
		return fmt.Errorf("_disconnectUpdateCoinLockupParams: utxoOperations are missing")
	}
	operationIndex := len(utxoOpsForTxn) - 1

	// Verify the last operation as being a UpdateCoinLockupParams operation.
	if utxoOpsForTxn[operationIndex].Type != OperationTypeUpdateCoinLockupParams {
		return fmt.Errorf("_disconnectUpdateCoinLockupParams: Trying to revert "+
			"OperationTypeUpdateCoinLockupParams but found type %v", utxoOpsForTxn[operationIndex].Type)
	}

	// Fetch the UpdateCoinLockupParams operation.
	operationData := utxoOpsForTxn[operationIndex]

	// Grab the txn metadata.
	txMeta := currentTxn.TxnMeta.(*UpdateCoinLockupParamsMetadata)

	// Fetch the profilePKID for the transactor.
	var profilePKID *PKID
	_, updaterIsParamUpdater := GetParamUpdaterPublicKeys(blockHeight, bav.Params)[MakePkMapKey(currentTxn.PublicKey)]
	if updaterIsParamUpdater {
		profilePKID = ZeroPKID.NewPKID()
	} else {
		profilePKIDEntry := bav.GetPKIDForPublicKey(currentTxn.PublicKey)
		if profilePKIDEntry == nil || profilePKIDEntry.isDeleted {
			return errors.Wrap(RuleErrorUpdateCoinLockupParamsOnInvalidPKID,
				"_connectUpdateCoinLockupParams")
		}
		profilePKID = profilePKIDEntry.PKID
	}

	// Check if the transaction added a yield curve point. If it did, we restore the previous point.
	// If the previous point is nil meaning this point didn't have a previous, then we simply delete the current point.
	if !txMeta.RemoveYieldCurvePoint && txMeta.LockupYieldDurationNanoSecs > 0 {
		if operationData.PrevLockupYieldCurvePoint == nil {
			bav._deleteLockupYieldCurvePoint(&LockupYieldCurvePoint{
				ProfilePKID:            profilePKID,
				LockupDurationNanoSecs: txMeta.LockupYieldDurationNanoSecs,
			})
		} else {
			bav._setLockupYieldCurvePoint(&LockupYieldCurvePoint{
				ProfilePKID:               profilePKID,
				LockupDurationNanoSecs:    operationData.PrevLockupYieldCurvePoint.LockupDurationNanoSecs,
				LockupYieldAPYBasisPoints: operationData.PrevLockupYieldCurvePoint.LockupYieldAPYBasisPoints,
			})
		}
	}

	// Check if the transaction deleted a yield curve point. If it did, we add back the previous point.
	// If the previous point is nil, we throw an error. This shouldn't be possible.
	if txMeta.RemoveYieldCurvePoint && txMeta.LockupYieldDurationNanoSecs > 0 {
		if operationData.PrevLockupYieldCurvePoint == nil {
			return fmt.Errorf("_connectUpdateCoinLockupParams: trying to revert point deletion " +
				"but found nil previous yield curve point; this shouldn't be possible")
		}
		bav._setLockupYieldCurvePoint(&LockupYieldCurvePoint{
			ProfilePKID:               profilePKID,
			LockupDurationNanoSecs:    operationData.PrevLockupYieldCurvePoint.LockupDurationNanoSecs,
			LockupYieldAPYBasisPoints: operationData.PrevLockupYieldCurvePoint.LockupYieldAPYBasisPoints,
		})
	}

	// Check if the transaction updated transfer restrictions. If it did, we reset the previous transfer restrictions.
	if txMeta.NewLockupTransferRestrictions {
		// Fetch the profile entry and LockupTransferRestriction status.
		profileEntry := bav.GetProfileEntryForPKID(profilePKID)
		if profileEntry == nil || profileEntry.isDeleted {
			return fmt.Errorf("_connectUpdateCoinLockupParams: Trying to revert lockup transfer restriction " +
				"update but found nil profile entry; this shouldn't be possible")
		}

		// Update the transfer restrictions.
		profileEntry.DAOCoinEntry.LockupTransferRestrictionStatus = operationData.PrevLockupTransferRestriction
		bav._setProfileEntryMappings(profileEntry)
	}

	// Decrement the operationIndex. We expect to find the basic transfer UtxoOps next.
	operationIndex--
	if operationIndex < 0 {
		return fmt.Errorf("_disconnectUpdateCoinLockupParams: Trying to revert OperationTypeUpdateCoinLockupParams " +
			"but found malformed utxoOpsForTxn")
	}

	// By here we only need to disconnect the basic transfer associated with the transaction.
	basicTransferOps := utxoOpsForTxn[:operationIndex]
	err := bav._disconnectBasicTransfer(currentTxn, txnHash, basicTransferOps, blockHeight)
	if err != nil {
		return errors.Wrapf(err, "_disconnectUpdateCoinLockupParams")
	}
	return nil
}

//
// CoinLockupTransfer Transaction Logic
//

func (bav *UtxoView) _connectCoinLockupTransfer(
	txn *MsgDeSoTxn,
	txHash *BlockHash,
	blockHeight uint32,
	verifySignatures bool,
) (_totalInput uint64,
	_totalOutput uint64,
	_utxoOps []*UtxoOperation,
	_err error) {
	var utxoOpsForTxn []*UtxoOperation

	// Validate the starting block height.
	if blockHeight < bav.Params.ForkHeights.ProofOfStake1StateSetupBlockHeight ||
		blockHeight < bav.Params.ForkHeights.BalanceModelBlockHeight {
		return 0, 0, nil, errors.Wrapf(RuleErrorProofofStakeTxnBeforeBlockHeight, "_connectCoinLockupTransfer")
	}

	// Validate the txn TxnType.
	if txn.TxnMeta.GetTxnType() != TxnTypeCoinLockupTransfer {
		return 0, 0, nil, fmt.Errorf(
			"_connectCoinLockupTransfer: called with bad TxnType: %s", txn.TxnMeta.GetTxnType().String())
	}

	// Try connecting the basic transfer without considering transaction metadata.
	_, _, utxoOpsForBasicTransfer, err := bav._connectBasicTransfer(txn, txHash, blockHeight, verifySignatures)
	if err != nil {
		return 0, 0, nil, errors.Wrap(err, "_connectCoinLockupTransfer")
	}
	utxoOpsForTxn = append(utxoOpsForTxn, utxoOpsForBasicTransfer...)

	// Grab the txn metadata.
	txMeta := txn.TxnMeta.(*CoinLockupTransferMetadata)

	// Validate the transfer amount as non-zero.
	if txMeta.LockedCoinsToTransferBaseUnits.IsZero() {
		return 0, 0, nil, errors.Wrap(RuleErrorCoinLockupTransferOfAmountZero,
			"_connectCoinLockupTransfer")
	}

	// If this is a DeSo lockup, ensure the amount is less than 2**64.
	if txMeta.ProfilePublicKey.IsZeroPublicKey() {
		maxUint64, _ := uint256.FromBig(big.NewInt(0).SetUint64(math.MaxUint64))
		if txMeta.LockedCoinsToTransferBaseUnits.Gt(maxUint64) {
			return 0, 0, nil, errors.Wrap(RuleErrorCoinLockupTransferOfDeSoCausesOverflow,
				"_connectCoinLockupTransfer")
		}
	}

	// Fetch PKIDs for the recipient and sender.
	senderPKIDEntry := bav.GetPKIDForPublicKey(txn.PublicKey)
	senderPKID := senderPKIDEntry.PKID
	receiverPKIDEntry := bav.GetPKIDForPublicKey(txMeta.RecipientPublicKey.ToBytes())
	receiverPKID := receiverPKIDEntry.PKID
	profilePKIDEntry := bav.GetPKIDForPublicKey(txMeta.ProfilePublicKey.ToBytes())
	profilePKID := profilePKIDEntry.PKID

	// Ensure the sender and receiver are different.
	if senderPKID.Eq(receiverPKID) {
		return 0, 0, nil, errors.Wrapf(RuleErrorCoinLockupTransferSenderEqualsReceiver,
			"_connectCoinLockupTransfer")
	}

	// Verify the transfer restrictions attached to the transfer.
	profileEntry := bav.GetProfileEntryForPKID(profilePKID)
	if profileEntry.DAOCoinEntry.LockupTransferRestrictionStatus == TransferRestrictionStatusProfileOwnerOnly &&
		!profilePKID.Eq(senderPKID) {
		return 0, 0, nil, errors.Wrapf(RuleErrorCoinLockupTransferRestrictedToProfileOwner,
			"_connectCoinLockupTransfer")
	}
	if profileEntry.DAOCoinEntry.LockupTransferRestrictionStatus == TransferRestrictionStatusDAOMembersOnly {
		// TODO: Determine if this is desired behavior. We assume the sender must be part of the DAO to have
		//       transferable coins. It seems weird to tie locked DAO coin transfers to unlocked DAO coin balances.
		//       An alternative approach is not allow the "TransferRestrictionStatusDAOMembersOnly" restriction.
		receiverBalanceEntry := bav.GetBalanceEntry(receiverPKID, profilePKID, true)
		if receiverBalanceEntry.BalanceNanos.IsZero() {
			return 0, 0, nil,
				errors.Wrapf(RuleErrorCoinLockupTransferRestrictedToDAOMembers, "_connectCoinLockupTransfer")
		}
	}

	// Fetch the sender's balance entries.
	senderLockedBalanceEntry, err := bav.GetLockedBalanceEntryForHODLerPKIDProfilePKIDUnlockTimestampNanoSecs(
		senderPKID, profilePKID, txMeta.UnlockTimestampUnixNanoSecs)
	if err != nil {
		return 0, 0, nil,
			errors.Wrap(err, "connectCoinLockupTransfer failed to fetch senderLockedBalanceEntry:w")
	}
	if senderLockedBalanceEntry == nil || senderLockedBalanceEntry.isDeleted {
		senderLockedBalanceEntry = &LockedBalanceEntry{
			HODLerPKID:              senderPKID,
			ProfilePKID:             profilePKID,
			UnlockTimestampNanoSecs: txMeta.UnlockTimestampUnixNanoSecs,
			BalanceBaseUnits:        *uint256.NewInt(),
		}
	}
	prevSenderLockedBalanceEntry := senderLockedBalanceEntry.Copy()

	// Check that the sender's balance entry has sufficient balance.
	if txMeta.LockedCoinsToTransferBaseUnits.Gt(&senderLockedBalanceEntry.BalanceBaseUnits) {
		return 0, 0, nil, errors.Wrapf(RuleErrorCoinLockupTransferInsufficientBalance,
			"_connectCoinLockupTransfer")
	}

	// Credit the sender's balance entry.
	senderLockedBalanceEntry.BalanceBaseUnits = *uint256.NewInt().Sub(
		&senderLockedBalanceEntry.BalanceBaseUnits, txMeta.LockedCoinsToTransferBaseUnits)

	// Fetch the recipient's balance entry.
	receiverLockedBalanceEntry, err := bav.GetLockedBalanceEntryForHODLerPKIDProfilePKIDUnlockTimestampNanoSecs(
		receiverPKID, profilePKID, txMeta.UnlockTimestampUnixNanoSecs)
	if err != nil {
		return 0, 0, nil,
			errors.Wrap(err, "connectCoinLockupTransfer failed to fetch receiverLockedBalanceEntry")
	}
	if receiverLockedBalanceEntry == nil || receiverLockedBalanceEntry.isDeleted {
		receiverLockedBalanceEntry = &LockedBalanceEntry{
			HODLerPKID:              receiverPKID,
			ProfilePKID:             profilePKID,
			UnlockTimestampNanoSecs: txMeta.UnlockTimestampUnixNanoSecs,
			BalanceBaseUnits:        *uint256.NewInt(),
		}
	}
	prevReceiverLockedBalanceEntry := receiverLockedBalanceEntry.Copy()

	// Add to the recipient's balance entry, checking for overflow.
	newRecipientBalanceBaseUnits, err := SafeUint256().Add(&receiverLockedBalanceEntry.BalanceBaseUnits,
		txMeta.LockedCoinsToTransferBaseUnits)
	if err != nil {
		return 0, 0, nil, errors.Wrapf(RuleErrorCoinLockupTransferBalanceOverflowAtReceiver,
			"_connectCoinLockupTransfer")
	}
	receiverLockedBalanceEntry.BalanceBaseUnits = *newRecipientBalanceBaseUnits

	// Update the balances in the view.
	bav._setLockedBalanceEntry(senderLockedBalanceEntry)
	bav._setLockedBalanceEntry(receiverLockedBalanceEntry)

	// Create a UtxoOperation for easily disconnecting the transaction.
	utxoOpsForTxn = append(utxoOpsForTxn, &UtxoOperation{
		Type:                           OperationTypeCoinLockupTransfer,
		PrevSenderLockedBalanceEntry:   prevSenderLockedBalanceEntry,
		PrevReceiverLockedBalanceEntry: prevReceiverLockedBalanceEntry,
	})

	return 0, 0, utxoOpsForTxn, nil
}

func (bav *UtxoView) _disconnectCoinLockupTransfer(
	operationType OperationType,
	currentTxn *MsgDeSoTxn,
	txnHash *BlockHash,
	utxoOpsForTxn []*UtxoOperation,
	blockHeight uint32) error {
	if len(utxoOpsForTxn) == 0 {
		return fmt.Errorf("_disconnectCoinLockupTransfer: utxoOperations are missing")
	}
	operationIndex := len(utxoOpsForTxn) - 1

	// Verify the last operation as being a CoinLockupTransfer operation.
	if utxoOpsForTxn[operationIndex].Type != OperationTypeCoinLockupTransfer {
		return fmt.Errorf("_disconnectDAOCoinLockup: Trying to revert "+
			"OperationTypeCoinLockupTransfer but found type %v", utxoOpsForTxn[operationIndex].Type)
	}

	// Sanity check the OperationTypeCoinLockupTransfer exists.
	operationData := utxoOpsForTxn[operationIndex]
	operationIndex--
	if operationIndex < 0 {
		return fmt.Errorf("_disconnectCoinLockupTransfer: Trying to revert OperationTypeCoinLockupTransfer " +
			"but malformed utxoOpsForTxn")
	}
	if operationData.PrevSenderLockedBalanceEntry == nil || operationData.PrevSenderLockedBalanceEntry.isDeleted {
		return fmt.Errorf("_disconnectCoinLockupTransfer: Trying to revert OperationTypeCoinLockupTransfer " +
			"but found nil or deleted PrevSenderLockedBalanceEntry")
	}
	if operationData.PrevReceiverLockedBalanceEntry == nil || operationData.PrevReceiverLockedBalanceEntry.isDeleted {
		return fmt.Errorf("_disconnectCoinLockupTransfer: Trying to revert OperationTypeCoinLockupTransfer " +
			"but found nil or deleted PrevReceiverLockedBalanceEntry")
	}

	// Fetch the LockedBalanceEntries in the view.
	senderLockedBalanceEntry, err := bav.GetLockedBalanceEntryForHODLerPKIDProfilePKIDUnlockTimestampNanoSecs(
		operationData.PrevSenderLockedBalanceEntry.HODLerPKID,
		operationData.PrevSenderLockedBalanceEntry.ProfilePKID,
		operationData.PrevSenderLockedBalanceEntry.UnlockTimestampNanoSecs)
	if err != nil {
		return errors.Wrap(err, "_disconnectCoinLockupTransfer failed to fetch senderLockedBalanceEntry")
	}
	if senderLockedBalanceEntry == nil || senderLockedBalanceEntry.isDeleted {
		senderLockedBalanceEntry = &LockedBalanceEntry{
			HODLerPKID:              operationData.PrevSenderLockedBalanceEntry.HODLerPKID,
			ProfilePKID:             operationData.PrevSenderLockedBalanceEntry.ProfilePKID,
			UnlockTimestampNanoSecs: operationData.PrevSenderLockedBalanceEntry.UnlockTimestampNanoSecs,
			BalanceBaseUnits:        *uint256.NewInt(),
		}
	}
	receiverLockedBalanceEntry, err := bav.GetLockedBalanceEntryForHODLerPKIDProfilePKIDUnlockTimestampNanoSecs(
		operationData.PrevReceiverLockedBalanceEntry.HODLerPKID,
		operationData.PrevReceiverLockedBalanceEntry.ProfilePKID,
		operationData.PrevReceiverLockedBalanceEntry.UnlockTimestampNanoSecs)
	if err != nil {
		return errors.Wrap(err, "_disconnectCoinLockupTransfer failed to fetch receiverLockedBalanceEntry")
	}
	if receiverLockedBalanceEntry == nil || receiverLockedBalanceEntry.isDeleted {
		receiverLockedBalanceEntry = &LockedBalanceEntry{
			HODLerPKID:              operationData.PrevReceiverLockedBalanceEntry.HODLerPKID,
			ProfilePKID:             operationData.PrevReceiverLockedBalanceEntry.ProfilePKID,
			UnlockTimestampNanoSecs: operationData.PrevReceiverLockedBalanceEntry.UnlockTimestampNanoSecs,
			BalanceBaseUnits:        *uint256.NewInt(),
		}
	}

	// Ensure reverting the transaction won't cause the recipients balances to increase
	// or cause the senders balances to decrease.
	if operationData.PrevSenderLockedBalanceEntry.BalanceBaseUnits.Lt(&senderLockedBalanceEntry.BalanceBaseUnits) {
		return fmt.Errorf("_disconnectCoinLockupTransfer: Reversion of coin lockup transfer would " +
			"result in less coins for sender")
	}
	if operationData.PrevReceiverLockedBalanceEntry.BalanceBaseUnits.Gt(&receiverLockedBalanceEntry.BalanceBaseUnits) {
		return fmt.Errorf("_disconnectCoinLockupTransfer: Reversion of coin lockup transfer would " +
			"result in more coins for receiver")
	}

	// Set the balance entry mappings.
	bav._setLockedBalanceEntry(operationData.PrevSenderLockedBalanceEntry)
	bav._setLockedBalanceEntry(operationData.PrevReceiverLockedBalanceEntry)

	// By here we only need to disconnect the basic transfer associated with the transaction.
	basicTransferOps := utxoOpsForTxn[:operationIndex]
	err = bav._disconnectBasicTransfer(currentTxn, txnHash, basicTransferOps, blockHeight)
	if err != nil {
		return errors.Wrapf(err, "_disconnectCoinLockupTransfer")
	}

	return nil
}

//
// CoinUnlock Transaction Logic
//

func (bav *UtxoView) _connectCoinUnlock(
	txn *MsgDeSoTxn, txHash *BlockHash,
	blockHeight uint32, blockTimestamp int64,
	verifySignatures bool) (_totalInput uint64, _totalOutput uint64, _utxoOps []*UtxoOperation, _err error) {
	var utxoOpsForTxn []*UtxoOperation

	// Validate the starting block height.
	if blockHeight < bav.Params.ForkHeights.ProofOfStake1StateSetupBlockHeight ||
		blockHeight < bav.Params.ForkHeights.BalanceModelBlockHeight {
		return 0, 0, nil, errors.Wrapf(RuleErrorProofofStakeTxnBeforeBlockHeight, "_connectCoinLockup")
	}

	// Validate the txn TxnType.
	if txn.TxnMeta.GetTxnType() != TxnTypeCoinUnlock {
		return 0, 0, nil, fmt.Errorf(
			"_connectCoinUnlock: called with bad TxnType %s", txn.TxnMeta.GetTxnType().String(),
		)
	}

	// Try connecting the basic transfer without considering transaction metadata.
	_, _, utxoOpsForBasicTransfer, err := bav._connectBasicTransfer(txn, txHash, blockHeight, verifySignatures)
	if err != nil {
		return 0, 0, nil, errors.Wrapf(err, "_connectCoinUnlock")
	}
	utxoOpsForTxn = append(utxoOpsForTxn, utxoOpsForBasicTransfer...)

	// Grab the txn metadata.
	txMeta := txn.TxnMeta.(*CoinUnlockMetadata)

	// Check for a valid profile public key.
	if len(txMeta.ProfilePublicKey) != btcec.PubKeyBytesLenCompressed {
		return 0, 0, nil, errors.Wrap(RuleErrorDAOCoinInvalidPubKey,
			"_connectCoinUnlock")
	}
	if !txMeta.ProfilePublicKey.IsZeroPublicKey() {
		profileEntry := bav.GetProfileEntryForPublicKey(txMeta.ProfilePublicKey.ToBytes())
		if profileEntry == nil || profileEntry.isDeleted {
			return 0, 0, nil, errors.Wrap(RuleErrorCoinUnlockOnNonExistentProfile,
				"_connectCoinUnlock")
		}
	}

	// Convert the TransactorPublicKey to HODLerPKID
	transactorPKIDEntry := bav.GetPKIDForPublicKey(txn.PublicKey)
	if transactorPKIDEntry == nil || transactorPKIDEntry.isDeleted {
		return 0, 0, nil, errors.Wrapf(RuleErrorCoinUnlockInvalidHODLerPKID,
			"_connectCoinUnlock")
	}
	hodlerPKID := transactorPKIDEntry.PKID

	// Convert the ProfilePublicKey to ProfilePKID.
	var profilePKID *PKID
	if txMeta.ProfilePublicKey.IsZeroPublicKey() {
		profilePKID = ZeroPKID.NewPKID()
	} else {
		profilePKIDEntry := bav.GetPKIDForPublicKey(txMeta.ProfilePublicKey.ToBytes())
		if profilePKIDEntry == nil || profilePKIDEntry.isDeleted {
			return 0, 0, nil, errors.Wrapf(RuleErrorCoinUnlockInvalidProfilePKID,
				"_connectCoinUnlock")
		}
		profilePKID = profilePKIDEntry.PKID
	}

	// Retrieve unlockable locked balance entries.
	unlockableLockedBalanceEntries, err := bav.GetUnlockableLockedBalanceEntries(
		hodlerPKID, profilePKID, blockTimestamp)
	if err != nil {
		return 0, 0, nil, errors.Wrapf(err, "_connectCoinUnlock")
	}
	if len(unlockableLockedBalanceEntries) == 0 {
		return 0, 0, nil, errors.Wrapf(RuleErrorCoinUnlockNoUnlockableCoinsFound,
			"_connectCoinUnlock")
	}

	// Unlock coins until the amount specified by the transaction is deducted.
	var prevLockedBalanceEntries []*LockedBalanceEntry
	var unlockedBalance *uint256.Int
	for _, unlockableLockedBalanceEntry := range unlockableLockedBalanceEntries {
		unlockedBalance, err =
			SafeUint256().Add(unlockedBalance, &unlockableLockedBalanceEntry.BalanceBaseUnits)
		if err != nil {
			return 0, 0, nil,
				errors.Wrapf(RuleErrorCoinUnlockUnlockableCoinsOverflow, "_connectCoinUnlock")
		}

		// Append the LockedBalanceEntry in the event we rollback the transaction.
		prevLockedBalanceEntries = append(prevLockedBalanceEntries, unlockableLockedBalanceEntry)

		// Update the LockedBalanceEntry and delete the record.
		unlockableLockedBalanceEntry.BalanceBaseUnits = *uint256.NewInt()
		bav._deleteLockedBalanceEntry(unlockableLockedBalanceEntry)
	}

	// Credit the transactor with either DAO coins or DeSo for this unlock.
	var prevTransactorBalanceEntry *BalanceEntry
	if profilePKID.IsZeroPKID() {
		// Ensure the uint256 can be properly represented as a uint64.
		if !unlockedBalance.IsUint64() {
			return 0, 0, nil,
				errors.Wrapf(RuleErrorCoinUnlockUnlockableDeSoOverflow, "_connectCoinUnlock")
		}

		// Add the unlockedBalance to the transactors DeSo balance.
		// NOTE: _addBalance checks for balance overflow.
		utxoOp, err := bav._addBalance(unlockedBalance.Uint64(), txn.PublicKey)
		if err != nil {
			return 0, 0, nil, errors.Wrapf(err, "_connectCoinUnlock: error"+
				"adding CoinToUnlockBaseUnits to the transactor balance: ")
		}
		utxoOpsForTxn = append(utxoOpsForTxn, utxoOp)
	} else {
		prevTransactorBalanceEntry = bav.GetBalanceEntry(hodlerPKID, profilePKID, true)

		// Credit the transactor with the unlock amount.
		newTransactorBalanceEntry := prevTransactorBalanceEntry.Copy()
		newTransactorBalanceNanos, err := SafeUint256().Add(&newTransactorBalanceEntry.BalanceNanos, unlockedBalance)
		if err != nil {
			return 0, 0, nil, errors.Wrapf(RuleErrorCoinUnlockCausesBalanceOverflow,
				"_connectCoinUnlock")
		}
		newTransactorBalanceEntry.BalanceNanos = *newTransactorBalanceNanos
		bav._setBalanceEntryMappings(newTransactorBalanceEntry, true)
	}

	// Create a UtxoOp for the operation.
	utxoOpsForTxn = append(utxoOpsForTxn, &UtxoOperation{
		Type:                       OperationTypeCoinUnlock,
		PrevTransactorBalanceEntry: prevTransactorBalanceEntry,
		PrevLockedBalanceEntries:   prevLockedBalanceEntries,
	})

	return 0, 0, utxoOpsForTxn, nil
}

func (bav *UtxoView) _disconnectCoinUnlock(
	operationType OperationType,
	currentTxn *MsgDeSoTxn,
	txnHash *BlockHash,
	utxoOpsForTxn []*UtxoOperation,
	blockHeight uint32) error {

	if len(utxoOpsForTxn) == 0 {
		return fmt.Errorf("_disconnectCoinUnlock: utxoOperations are missing")
	}
	operationIndex := len(utxoOpsForTxn) - 1

	// Verify the last operation as being a CoinUnlock operation.
	if utxoOpsForTxn[operationIndex].Type != OperationTypeCoinUnlock {
		return fmt.Errorf("_disconnectCoinUnlock: Trying to revert "+
			"OperationTypeCoinUnlock but found type %v", utxoOpsForTxn[operationIndex].Type)
	}

	// Sanity check the CoinUnlock operation exists.
	operationData := utxoOpsForTxn[operationIndex]
	if operationData.PrevLockedBalanceEntries == nil || len(operationData.PrevLockedBalanceEntries) == 0 {
		return fmt.Errorf("_disconnectCoinUnlock: Trying to revert OperationTypeCoinUnlock " +
			"but found nil or empty previous locked balance entries slice")
	}
	for _, prevLockedBalanceEntry := range operationData.PrevLockedBalanceEntries {
		if prevLockedBalanceEntry == nil || prevLockedBalanceEntry.isDeleted {
			return fmt.Errorf("_disconnectCoinUnlock: Trying to revert OperationTypeCoinUnlock " +
				"but found nil or deleted previous locked balance entry")
		}
	}
	operationIndex--
	if operationIndex < 0 {
		return fmt.Errorf("_disconnectCoinUnlock: Trying to revert OperationTypeCoinUnlock " +
			"but found malformed utxoOpsForTxn")
	}

	// Sanity check the data within the CoinUnlock.
	// Reverting an unlock of LockedBalanceEntry should not result in less coins.
	for _, prevLockedBalanceEntry := range operationData.PrevLockedBalanceEntries {
		lockedBalanceEntry, err := bav.GetLockedBalanceEntryForHODLerPKIDProfilePKIDUnlockTimestampNanoSecs(
			prevLockedBalanceEntry.HODLerPKID,
			prevLockedBalanceEntry.ProfilePKID,
			prevLockedBalanceEntry.UnlockTimestampNanoSecs)
		if err != nil {
			return errors.Wrap(err, "_disconnectCoinUnlock failed to fetch lockedBalanceEntry")
		}
		if lockedBalanceEntry == nil || lockedBalanceEntry.isDeleted {
			lockedBalanceEntry = &LockedBalanceEntry{
				HODLerPKID:              operationData.PrevLockedBalanceEntry.HODLerPKID,
				ProfilePKID:             operationData.PrevLockedBalanceEntry.ProfilePKID,
				UnlockTimestampNanoSecs: operationData.PrevLockedBalanceEntry.UnlockTimestampNanoSecs,
				BalanceBaseUnits:        *uint256.NewInt(),
			}
		}
		if prevLockedBalanceEntry.BalanceBaseUnits.Lt(&lockedBalanceEntry.BalanceBaseUnits) {
			return fmt.Errorf("_disconnectCoinUnlock: Trying to revert OperationTypeCoinUnlock " +
				"would cause locked balance entry balance to decrease")
		}
		bav._setLockedBalanceEntry(prevLockedBalanceEntry)
	}

	// Reverting the BalanceEntry (if applicable) should not result in more coins.
	profilePKID := operationData.PrevLockedBalanceEntries[0].ProfilePKID
	hodlerPKID := operationData.PrevLockedBalanceEntries[0].HODLerPKID
	if !profilePKID.IsZeroPKID() {
		balanceEntry := bav.GetBalanceEntry(hodlerPKID, profilePKID, true)
		if operationData.PrevTransactorBalanceEntry == nil || operationData.PrevTransactorBalanceEntry.isDeleted {
			return fmt.Errorf("_disconnectCoinUnlock: Trying to revert OperationTypeCoinUnlock " +
				"but found nil or deleted previous balance entry")
		}
		if operationData.PrevTransactorBalanceEntry.BalanceNanos.Gt(&balanceEntry.BalanceNanos) {
			return fmt.Errorf("_disconnectCoinUnlock: Trying to revert OperationTypeCoinUnlock " +
				"would cause balance entry balance to increase")
		}
		bav._setBalanceEntryMappings(operationData.PrevTransactorBalanceEntry, true)
	}

	// Reverting the DeSo addition should not result in more coins.
	if profilePKID.IsZeroPKID() {
		// Revert the DeSo add.
		operationData = utxoOpsForTxn[operationIndex]
		if operationData.Type != OperationTypeAddBalance {
			return fmt.Errorf("_disconnectCoinLockup: Trying to revert OperationTypeAddBalance "+
				"but found type %v", operationData.Type)
		}
		if !bytes.Equal(operationData.BalancePublicKey, currentTxn.PublicKey) {
			return fmt.Errorf("_disconnectCoinLockup: Trying to revert OperationTypeAddBalance " +
				"but found mismatched public keys")
		}
		err := bav._unAddBalance(operationData.BalanceAmountNanos, operationData.BalancePublicKey)
		if err != nil {
			return errors.Wrapf(err, "_disconnectCoinLockup: Problem unAdding balance of %v for the "+
				"transactor", operationData.BalanceAmountNanos)
		}
		operationIndex--
		if operationIndex < 0 {
			return fmt.Errorf("_disconnectCoinLockup: Trying to revert OperationTypeCoinUnlock " +
				"but malformed utxoOpsForTxn")
		}
	}

	// By here we only need to disconnect the basic transfer associated with the transaction.
	basicTransferOps := utxoOpsForTxn[:operationIndex]
	err := bav._disconnectBasicTransfer(currentTxn, txnHash, basicTransferOps, blockHeight)
	if err != nil {
		return errors.Wrapf(err, "_disconnectCoinLockup")
	}
	return nil
}

//
// DB FLUSHES
//

func (bav *UtxoView) _flushLockedBalanceEntriesToDbWithTxn(txn *badger.Txn, blockHeight uint64) error {
	// Go through all entries in the LockedBalanceEntryMapKeyToLockedBalanceEntry map.
	for lockedBalanceEntryMapKeyIter, lockedBalanceEntry := range bav.LockedBalanceEntryKeyToLockedBalanceEntry {
		lockedBalanceEntryKey := lockedBalanceEntryMapKeyIter

		// Sanity check the key computed from the lockedBalanceEntry is equal
		// to the lockedBalanceEntryKey that maps to that entry.
		lockedBalanceEntryKeyInEntry := lockedBalanceEntry.ToMapKey()
		if lockedBalanceEntryKeyInEntry != lockedBalanceEntryKey {
			return fmt.Errorf("_flushLockedBalanceEntriesToDbWithTxn: LockedBalanceEntry has "+
				"LockedBalanceEntryKey: %v, which doesn't match the LockedBalanceEntryMapKeyToLockedBalanceEntry map key %v",
				&lockedBalanceEntryKeyInEntry, &lockedBalanceEntry)
		}

		// Delete the existing mappings in the db for this LockedBalanceEntry.
		// They will be re-added if the corresponding entry in memory has isDeleted=false.
		if err := DbDeleteLockedBalanceEntryWithTxn(txn, bav.Snapshot, *lockedBalanceEntry); err != nil {
			return errors.Wrapf(
				err, "_flushLockedBalanceEntriesToDbWithTxn: Problem deleting mappings "+
					"for LockedBalanceEntry: %v", &lockedBalanceEntryKey)
		}
	}
	for _, lockedBalanceEntry := range bav.LockedBalanceEntryKeyToLockedBalanceEntry {
		if lockedBalanceEntry.isDeleted || lockedBalanceEntry.BalanceBaseUnits.IsZero() {
			// We do nothing as we've already deleted the entry above or the balance is zero.
		} else {
			if err := DbPutLockedBalanceEntryMappingsWithTxn(txn, bav.Snapshot, blockHeight,
				*lockedBalanceEntry); err != nil {
				return errors.Wrapf(err, "_flushLockedBalanceEntriesToDbWithTxn")
			}
		}
	}

	// By here the LockedBalanceEntry mappings in the db should be up-to-date.
	return nil
}

func (bav *UtxoView) _flushLockupYieldCurvePointEntriesToDbWithTxn(txn *badger.Txn, blockHeight uint64) error {
	// Go through all PKIDs with changes to their yield curves.
	for _, LockupYieldCurvePointMap := range bav.PKIDToLockupYieldCurvePointKeyToLockupYieldCurvePoints {
		// Go through all LockupYieldCurvePoints in the LockupYieldCurvePoint map.
		for lockupYieldCurvePointKey, lockupYieldCurvePoint := range LockupYieldCurvePointMap {

			// Sanity check the key computed from the lockupYieldCurvePoint is equal
			// to the lockupYieldCurvePointKey that maps to that entry.
			lockupYieldCurvePointKeyInEntry := lockupYieldCurvePoint.ToMapKey()
			if lockupYieldCurvePointKeyInEntry != lockupYieldCurvePointKey {
				return fmt.Errorf("_flushYieldCurveEntriesToDbWithTxn: LockupYieldCurvePoint has "+
					"LockupYieldCurvePoint: %v, which doesn't match the LockupYieldCurvePoint map key %v",
					&lockupYieldCurvePointKeyInEntry, &lockupYieldCurvePointKey)
			}

			// Delete the existing mappings in the db for this LockupYieldCurvePoint.
			// They will be re-added if the corresponding entry in memory has isDeleted=false.
			if err := DbDeleteLockupYieldCurvePointWithTxn(txn, bav.Snapshot, *lockupYieldCurvePoint); err != nil {
				return errors.Wrapf(
					err, "_flushYieldCurveEntriesToDbWithTxn: Problem deleting mappings "+
						"for LockupYieldCurvePoint: %v", &lockupYieldCurvePoint)
			}
		}
	}
	// Go through all PKIDs with changes to their yield curves.
	for _, LockupYieldCurvePointMap := range bav.PKIDToLockupYieldCurvePointKeyToLockupYieldCurvePoints {
		// Go through all LockupYieldCurvePoints in the LockupYieldCurvePoint map.
		for _, lockupYieldCurvePoint := range LockupYieldCurvePointMap {
			if lockupYieldCurvePoint.isDeleted {
				// We do nothing as we've already deleted the entry above.
			} else {
				if err := DbPutLockupYieldCurvePointMappingsWithTxn(txn, bav.Snapshot, blockHeight,
					*lockupYieldCurvePoint); err != nil {
					return errors.Wrapf(err, "_flushYieldCurveEntriesToDbWithTxn")
				}
			}
		}
	}

	// By here the LockupYieldCurvePoint mappings in the db should be up-to-date.
	return nil
}
