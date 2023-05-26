package lib

import (
	"github.com/pkg/errors"
)

func (bav *UtxoView) IsLastBlockInCurrentEpoch(blockHeight uint64) (bool, error) {
	// Returns true if this is the last block in the current epoch.
	if blockHeight < uint64(bav.Params.ForkHeights.ProofOfStakeBlockHeight) {
		// Return false if we are still using PoW and haven't cut over to PoS yet.
		return false, nil
	}
	currentEpochEntry, err := bav.GetCurrentEpochEntry()
	if err != nil {
		return false, errors.Wrapf(err, "UtxoView.IsEpochComplete: problem retrieving CurrentEpochEntry: ")
	}
	if currentEpochEntry == nil {
		return false, errors.New("UtxoView.IsEpochComplete: CurrentEpochEntry is nil, this should never happen")
	}
	return currentEpochEntry.FinalBlockHeight == blockHeight, nil
}

func (bav *UtxoView) RunEpochCompleteHook(blockHeight uint64) error {
	// Rolls-over the current epoch into a new one. Handles the associated snapshotting + accounting.

	// Sanity-check that the current block is the last block in the current epoch.
	isLastBlockInCurrentEpoch, err := bav.IsLastBlockInCurrentEpoch(blockHeight)
	if err != nil {
		return errors.Wrapf(err, "UtxoView.RunEpochCompleteHook: ")
	}
	if !isLastBlockInCurrentEpoch {
		return errors.New("UtxoView.RunEpochCompleteHook: called before current epoch is complete, this should never happen")
	}

	// Retrieve the CurrentEpochEntry.
	currentEpochEntry, err := bav.GetCurrentEpochEntry()
	if err != nil {
		return errors.Wrapf(err, "UtxoView.RunEpochCompleteHook: problem retrieving CurrentEpochEntry: ")
	}
	if currentEpochEntry == nil {
		return errors.New("UtxoView.RunEpochCompleteHook: CurrentEpochEntry is nil, this should never happen")
	}

	// Snapshot the current GlobalParamsEntry.
	bav._setSnapshotGlobalParamsEntry(bav.GlobalParamsEntry, currentEpochEntry.EpochNumber)

	// Snapshot the current validators by PKID.
	// TODO

	// Snapshot the current validators by stake.
	// TODO

	// Snapshot the current GlobalActiveStakeAmountNanos.
	globalActiveStakeAmountNanos, err := bav.GetGlobalActiveStakeAmountNanos()
	if err != nil {
		return errors.Wrapf(err, "UtxoView.RunEpochCompleteHook: problem retrieving GlobalActiveStakeAmountNanos: ")
	}
	bav._setSnapshotGlobalActiveStakeAmountNanos(globalActiveStakeAmountNanos, currentEpochEntry.EpochNumber)

	// Generate + snapshot a leader schedule.
	// TODO

	// TODO: Is there any clean-up we should do here deleting old snapshots that are no longer useful?

	// Roll-over a new epoch by setting a new CurrentEpochEntry.
	nextEpochEntry := &EpochEntry{
		EpochNumber:      currentEpochEntry.EpochNumber + 1,
		FinalBlockHeight: blockHeight + 100, // TODO: read this duration from the GlobalParamsEntry.
	}
	bav._setCurrentEpochEntry(nextEpochEntry)

	return nil
}
