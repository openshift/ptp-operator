// Copyright 2020 The Cloud Native Events Authors
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

package store

import (
	"sync"

	"github.com/redhat-cne/sdk-go/pkg/pubsub"
)

// PubSubStore  defines pub/sub store struct
type PubSubStore struct {
	sync.RWMutex
	// PublisherStore stores publishers in a map
	Store map[string]*pubsub.PubSub
}

// Set is a wrapper for setting the value of a key in the underlying map
func (ps *PubSubStore) Set(key string, val pubsub.PubSub) {
	ps.Lock()
	defer ps.Unlock()
	storeSub := &pubsub.PubSub{
		ID:          val.ID,
		EndPointURI: val.EndPointURI,
		URILocation: val.URILocation,
		Resource:    val.Resource,
	}
	if ps.Store == nil {
		ps.Store = make(map[string]*pubsub.PubSub)
	}
	ps.Store[key] = storeSub
}

// Delete ... delete from store
func (ps *PubSubStore) Delete(key string) {
	ps.Lock()
	defer ps.Unlock()
	delete(ps.Store, key)
}
