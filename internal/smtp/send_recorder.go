// Copyright (c) 2020 Proton Technologies AG
//
// This file is part of ProtonMail Bridge.
//
// ProtonMail Bridge is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// ProtonMail Bridge is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with ProtonMail Bridge.  If not, see <https://www.gnu.org/licenses/>.

package smtp

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"github.com/ProtonMail/proton-bridge/pkg/pmapi"
)

type messageGetter interface {
	GetMessage(string) (*pmapi.Message, error)
}

type sendRecorderValue struct {
	messageID string
	time      time.Time
}

type sendRecorder struct {
	lock   *sync.RWMutex
	hashes map[string]sendRecorderValue
}

func newSendRecorder() *sendRecorder {
	return &sendRecorder{
		lock:   &sync.RWMutex{},
		hashes: map[string]sendRecorderValue{},
	}
}

func (q *sendRecorder) getMessageHash(message *pmapi.Message) string {
	h := sha256.New()
	_, _ = h.Write([]byte(message.AddressID + message.Subject))
	if message.Sender != nil {
		_, _ = h.Write([]byte(message.Sender.Address))
	}
	for _, to := range message.ToList {
		_, _ = h.Write([]byte(to.Address))
	}
	for _, to := range message.CCList {
		_, _ = h.Write([]byte(to.Address))
	}
	for _, to := range message.BCCList {
		_, _ = h.Write([]byte(to.Address))
	}
	_, _ = h.Write([]byte(message.Body))
	for _, att := range message.Attachments {
		_, _ = h.Write([]byte(att.Name + att.MIMEType + fmt.Sprintf("%d", att.Size)))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (q *sendRecorder) addMessage(hash, messageID string) {
	q.lock.Lock()
	defer q.lock.Unlock()

	q.deleteExpiredKeys()
	q.hashes[hash] = sendRecorderValue{
		messageID: messageID,
		time:      time.Now(),
	}
}

func (q *sendRecorder) isSendingOrSent(client messageGetter, hash string) (isSending bool, wasSent bool) {
	q.lock.Lock()
	defer q.lock.Unlock()

	q.deleteExpiredKeys()
	value, ok := q.hashes[hash]
	if !ok {
		return
	}
	message, err := client.GetMessage(value.messageID)
	// Message could be deleted or there could be an internet issue or whatever,
	// so let's assume the message was not sent.
	if err != nil {
		return
	}
	if message.Type == pmapi.MessageTypeDraft {
		// If message is in draft for a long time, let's assume there is
		// some problem and message will not be sent anymore.
		if time.Since(time.Unix(message.Time, 0)).Minutes() > 10 {
			return
		}
		isSending = true
	}
	// MessageTypeInboxAndSent can be when message was sent to myself.
	if message.Type == pmapi.MessageTypeSent || message.Type == pmapi.MessageTypeInboxAndSent {
		wasSent = true
	}
	return
}

func (q *sendRecorder) deleteExpiredKeys() {
	for key, value := range q.hashes {
		// It's hard to find a good expiration time.
		// On the one hand, a user could set up some cron job sending the same message over and over again (heartbeat).
		// On the the other, a user could put the device into sleep mode while sending.
		// Changing the expiration time will always make one of the edge cases worse.
		// But both edge cases are something we don't care much about. Important thing is we don't send the same message many times.
		if time.Since(value.time) > 30*time.Minute {
			delete(q.hashes, key)
		}
	}
}
