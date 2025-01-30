// Copyright 2021 The Cloud Native Events Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ptp

// EventResource ...
type EventResource string

const (
	// O-RAN 7.2.3.6
	// GnssSyncStatus notification is signalled from equipment at state change
	GnssSyncStatus EventResource = "/sync/gnss-status/gnss-sync-status"

	// O-RAN 7.2.3.8
	// OsClockSyncState State of node OS clock synchronization is notified at state change
	OsClockSyncState EventResource = "/sync/sync-status/os-clock-sync-state"

	// O-RAN 7.2.3.10
	// PtpClockClass notification is generated when the clock-class changes.
	PtpClockClass EventResource = "/sync/ptp-status/clock-class"

	// Support V1
	// PtpClockClassV1 notification is generated when the clock-class changes for v1.
	PtpClockClassV1 EventResource = "/sync/ptp-status/ptp-clock-class-change"

	// O-RAN 7.2.3.3
	// PtpLockState notification is signalled from equipment at state change
	PtpLockState EventResource = "/sync/ptp-status/lock-state"

	// O-RAN 7.2.3.11
	// SynceClockQuality notification is generated when the clock-quality changes.
	SynceClockQuality EventResource = "/sync/synce-status/clock-quality"

	// O-RAN 7.2.3.9
	// SynceLockState Notification used to inform about synce synchronization state change
	SynceLockState EventResource = "/sync/synce-status/lock-state"

	// SynceLockStateExtended notification is signalled from equipment at state change, enhanced information
	SynceLockStateExtended EventResource = "/sync/synce-status/lock-state-extended"

	// O-RAN 7.2.3.1
	// SyncStatusState is the overall synchronization health of the node, including the OS System Clock
	SyncStatusState EventResource = "/sync/sync-status/sync-state"
)
