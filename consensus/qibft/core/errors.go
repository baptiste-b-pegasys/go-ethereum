// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package core

import "errors"

var (
	// errInconsistentSubject is returned when received subject is different from
	// current subject.
	errInconsistentSubject = errors.New("inconsistent subjects")
	// errNotFromProposer is returned when received message_deprecated is supposed to be from
	// proposer.
	errNotFromProposer = errors.New("message_deprecated does not come from proposer")
	// errFutureMessage is returned when current view is earlier than the
	// view of the received message_deprecated.
	errFutureMessage = errors.New("future message_deprecated")
	// errOldMessage is returned when the received message_deprecated's view is earlier
	// than current view.
	errOldMessage = errors.New("old message_deprecated")
	// errInvalidMessage is returned when the message_deprecated is malformed.
	errInvalidMessage = errors.New("invalid message_deprecated")
	// errFailedDecodePreprepare is returned when the PRE-PREPARE message_deprecated is malformed.
	errFailedDecodePreprepare = errors.New("failed to decode PRE-PREPARE")
	// errFailedDecodeRoundChange is returned when the ROUNDCHANGE message_deprecated is malformed.
	errFailedDecodeRoundChange = errors.New("failed to decode ROUNDCHANGE")
	// errFailedDecodePrepare is returned when the PREPARE message_deprecated is malformed.
	errFailedDecodePrepare = errors.New("failed to decode PREPARE")
	// errFailedDecodeCommit is returned when the COMMIT message_deprecated is malformed.
	errFailedDecodeCommit = errors.New("failed to decode COMMIT")
	// errFailedDecodePiggybackMsgs is returned when the Piggyback messages are malformed
	errFailedDecodePiggybackMsgs = errors.New("failed to decode Piggyback Messages")
	// errInvalidSigner is returned when the message_deprecated is signed by a validator different than message_deprecated sender
	errInvalidSigner = errors.New("message_deprecated not signed by the sender")
	// errInvalidPreparedBlock is returned when prepared block is not validated in round change messages
	errInvalidPreparedBlock = errors.New("invalid prepared block in round change messages")
)
