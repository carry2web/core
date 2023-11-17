package lib

import (
	"math"

	"github.com/deso-protocol/core/collections"
	"github.com/pkg/errors"
)

func (bav *UtxoView) IsLastBlockInCurrentEpoch(blockHeight uint64) (bool, error) {
	// Returns true if this is the last block in the current epoch.

	if blockHeight < uint64(bav.Params.ForkHeights.ProofOfStake1StateSetupBlockHeight) {
		// Return false if we have not started snapshotting the relevant PoS entries yet.
		return false, nil
	}
	if blockHeight == uint64(bav.Params.ForkHeights.ProofOfStake1StateSetupBlockHeight) {
		// As soon as we enable snapshotting for the first time, we should run the OnEpochCompleteHook.
		return true, nil
	}
	currentEpochEntry, err := bav.GetCurrentEpochEntry()
	if err != nil {
		return false, errors.Wrapf(err, "IsEpochComplete: problem retrieving CurrentEpochEntry: ")
	}
	if currentEpochEntry == nil {
		return false, errors.New("IsEpochComplete: CurrentEpochEntry is nil, this should never happen")
	}
	return currentEpochEntry.FinalBlockHeight == blockHeight, nil
}

// RunEpochCompleteHook performs all of the end-of-epoch operations when connecting the final
// block of a epoch. The epoch completion hook has three steps.
//
// Step 1: Run all state-mutating operations that need to be run when completing an epoch. We always
// perform state-mutating operations before creating snapshots. This way, the snapshot created at the
// end of epoch n always reflects the state of the view at the end of epoch n after all state-mutating
// operations have been applied in the epoch.
// - Jail all inactive validators from the current snapshot validator set.
// - Reward all snapshotted stakes from the current snapshot validator set.
//
// Step 2: Create snapshots of the current state. Snapshotting operations here should only create new
// snapshot state. They should have no other side effects that mutate the existing state of the view.
// - Snapshot the current GlobalParamsEntry.
// - Snapshot the current validator set.
// - Snapshot the current validator set's TotalStakeAmountNanos.
// - Snapshot the leader schedule.
// - Snapshot the current top N stake entries, who will receive staking rewards.
//
// Step 3: Roll over to the next epoch.
// - Compute the final block height for the next epoch.
// - Update CurrentEpochEntry to the next epoch's.
func (bav *UtxoView) RunEpochCompleteHook(blockHeight uint64, blockTimestampNanoSecs uint64) ([]*UtxoOperation, error) {
	// Sanity-check that the current block is the last block in the current epoch.
	//
	// Note that this will also return true if we're currently at the ProofOfStake1StateSetupBlockHeight
	// so that we can run the hook for the first time to initialize the CurrentEpochEntry.
	isLastBlockInCurrentEpoch, err := bav.IsLastBlockInCurrentEpoch(blockHeight)
	if err != nil {
		return nil, errors.Wrapf(err, "RunEpochCompleteHook: ")
	}
	if !isLastBlockInCurrentEpoch {
		return nil, errors.New("RunEpochCompleteHook: called before current epoch is complete, this should never happen")
	}

	// Retrieve the CurrentEpochEntry.
	currentEpochEntry, err := bav.GetCurrentEpochEntry()
	if err != nil {
		return nil, errors.Wrapf(err, "runEpochCompleteSnapshotGeneration: problem retrieving CurrentEpochEntry: ")
	}
	if currentEpochEntry == nil {
		return nil, errors.New("runEpochCompleteSnapshotGeneration: CurrentEpochEntry is nil, this should never happen")
	}

	// Step 1: Run All State Mutating Operations
	utxoOperations, err := bav.runEpochCompleteStateTransition(blockHeight, blockTimestampNanoSecs)
	if err != nil {
		return nil, errors.Wrapf(err, "RunEpochCompleteHook: ")
	}

	// Step 2: Run All Snapshotting Operations
	if err = bav.runEpochCompleteSnapshotGeneration(currentEpochEntry.EpochNumber); err != nil {
		return nil, errors.Wrapf(err, "RunEpochCompleteHook: ")
	}

	// TODO: Evict old snapshots when safe to do so.

	// Step 3: Roll Over to The Next Epoch
	if err = bav.runEpochCompleteEpochRollover(currentEpochEntry.EpochNumber, blockHeight, blockTimestampNanoSecs); err != nil {
		return nil, errors.Wrapf(err, "RunEpochCompleteHook: ")
	}

	return utxoOperations, nil
}

// Runs all state-mutating operations required when completing an epoch.
func (bav *UtxoView) runEpochCompleteStateTransition(blockHeight uint64, blockTimestampNanoSecs uint64) ([]*UtxoOperation, error) {
	// Jail all inactive validators from the current snapshot validator set. This is an O(n) operation
	// that loops through all active unjailed validators from current epoch's snapshot validator set
	// and jails them if they have been inactive.
	//
	// Note, this this will only run if we are past the ProofOfStake2ConsensusCutoverBlockHeight fork height.
	if err := bav.JailAllInactiveSnapshotValidators(blockHeight); err != nil {
		return nil, errors.Wrapf(err, "runEpochCompleteStateTransition: problem jailing all inactive validators: ")
	}

	// Reward all snapshotted stakes from the current snapshot validator set. This is an O(n) operation
	// that loops through all of the snapshotted stakes and rewards them.
	//
	// Note, this this will only run if we are past the ProofOfStake2ConsensusCutoverBlockHeight fork height.
	utxoOperations, err := bav.DistributeStakingRewardsToSnapshotStakes(blockHeight, blockTimestampNanoSecs)
	if err != nil {
		return nil, errors.Wrapf(err, "runEpochCompleteStateTransition: problem rewarding snapshot stakes: ")
	}

	return utxoOperations, nil
}

// Generates all required snapshots for the current epoch.
func (bav *UtxoView) runEpochCompleteSnapshotGeneration(epochNumber uint64) error {
	// Snapshot the current GlobalParamsEntry.
	bav._setSnapshotGlobalParamsEntry(bav.GetCurrentGlobalParamsEntry(), epochNumber)

	// Snapshot the current top m validators as the validator set.
	validatorSet, err := bav.generateAndSnapshotValidatorSet(epochNumber)
	if err != nil {
		return errors.Wrapf(err, "runEpochCompleteSnapshotGeneration: problem snapshotting validator set: ")
	}

	// Snapshot a randomly generated leader schedule.
	if err = bav.generateAndSnapshotLeaderSchedule(epochNumber); err != nil {
		return errors.Wrapf(err, "runEpochCompleteSnapshotGeneration: problem snapshotting leader schedule: ")
	}

	// Snapshot the current top n stake entries as the stakes to reward.
	if err = bav.generateAndSnapshotStakesToReward(epochNumber, validatorSet); err != nil {
		return errors.Wrapf(err, "runEpochCompleteSnapshotGeneration: problem snapshotting stakes to reward: ")
	}

	return nil
}

// Updates the currentEpochEntry to the next epoch's.
func (bav *UtxoView) runEpochCompleteEpochRollover(epochNumber uint64, blockHeight uint64, blockTimestampNanoSecs uint64) error {
	// Retrieve the SnapshotGlobalParamsEntry to determine the next epoch's final block height. We use the
	// snapshot global params here because the next epoch begin immediately, and its length is used in the PoS
	// consensus. The validator set for the next epoch needs to be in agreement on the length of the epoch
	// before the epoch begins.
	snapshotGlobalParamsEntry, err := bav.GetSnapshotGlobalParamsEntry()
	if err != nil {
		return errors.Wrapf(err, "runEpochCompleteEpochRollover: problem retrieving SnapshotGlobalParamsEntry: ")
	}

	// Calculate the NextEpochFinalBlockHeight.
	nextEpochFinalBlockHeight, err := SafeUint64().Add(blockHeight, snapshotGlobalParamsEntry.EpochDurationNumBlocks)
	if err != nil {
		return errors.Wrapf(err, "runEpochCompleteEpochRollover: problem calculating NextEpochFinalBlockHeight: ")
	}

	// Roll-over a new epoch by setting a new CurrentEpochEntry.
	nextEpochEntry := &EpochEntry{
		EpochNumber:                     epochNumber + 1,
		FinalBlockHeight:                nextEpochFinalBlockHeight,
		CreatedAtBlockTimestampNanoSecs: blockTimestampNanoSecs,
	}
	bav._setCurrentEpochEntry(nextEpochEntry)

	return nil
}

func (bav *UtxoView) generateAndSnapshotValidatorSet(epochNumber uint64) ([]*ValidatorEntry, error) {
	// Snapshot the current top n active validators as the validator set.
	validatorSet, err := bav.GetTopActiveValidatorsByStakeAmount(
		bav.GetCurrentGlobalParamsEntry().ValidatorSetMaxNumValidators,
	)
	if err != nil {
		return nil, errors.Wrapf(err, "generateAndSnapshotValidatorSet: error retrieving top ValidatorEntries: ")
	}
	for _, validatorEntry := range validatorSet {
		bav._setSnapshotValidatorSetEntry(validatorEntry, epochNumber)
	}

	// Snapshot the current validator set's total stake. Note, the validator set is already filtered to the top n
	// active validators for the epoch. The total stake is the sum of all of the active validators' stakes.
	validatorSetTotalStakeAmountNanos := SumValidatorEntriesTotalStakeAmountNanos(validatorSet)
	bav._setSnapshotValidatorSetTotalStakeAmountNanos(validatorSetTotalStakeAmountNanos, epochNumber)

	return validatorSet, nil
}

func (bav *UtxoView) generateAndSnapshotLeaderSchedule(epochNumber uint64) error {
	// Generate a random leader schedule and snapshot it.
	leaderSchedule, err := bav.GenerateLeaderSchedule()
	if err != nil {
		return errors.Wrapf(err, "generateAndSnapshotLeaderSchedule: problem generating leader schedule: ")
	}

	for index, validatorPKID := range leaderSchedule {
		if index > math.MaxUint16 {
			return errors.Errorf("generateAndSnapshotLeaderSchedule: LeaderIndex %d overflows uint16", index)
		}
		bav._setSnapshotLeaderScheduleValidator(validatorPKID, uint16(index), epochNumber)
	}

	return nil
}

func (bav *UtxoView) generateAndSnapshotStakesToReward(epochNumber uint64, validatorSet []*ValidatorEntry) error {
	// Fetch the validator set's PKIDs so we can filter the top stakes by the current validator set.
	validatorSetPKIDs := collections.TransformSlice(validatorSet, func(validatorEntry *ValidatorEntry) *PKID {
		return validatorEntry.ValidatorPKID
	})

	// Fetch the current top n stake entries. This query is guaranteed to return the top n stake entries that have
	// staked to the validator set.
	topStakeEntries, err := bav.GetTopStakesForValidatorsByStakeAmount(
		validatorSetPKIDs,
		bav.GetCurrentGlobalParamsEntry().StakingRewardsMaxNumStakes,
	)
	if err != nil {
		return errors.Wrapf(err, "RunEpochCompleteHook: error retrieving top StakeEntries: ")
	}

	// Snapshot only the top m stake entries that are in the validator set.
	for _, stakeEntry := range topStakeEntries {
		bav._setSnapshotStakeToReward(stakeEntry.Copy(), epochNumber)
	}

	return nil
}
