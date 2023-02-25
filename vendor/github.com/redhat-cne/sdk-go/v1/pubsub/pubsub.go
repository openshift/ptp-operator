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

package pubsub

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/google/uuid"
	"github.com/redhat-cne/sdk-go/pkg/pubsub"
	"github.com/redhat-cne/sdk-go/pkg/store"
	"github.com/redhat-cne/sdk-go/pkg/types"
)

// API ... api methods  for publisher subscriber
type API struct {
	pubStore         *store.PubSubStore
	subStore         *store.PubSubStore
	subFile          string
	pubFile          string
	storeFilePath    string
	transportEnabled bool
}

var instance *API
var once sync.Once
var mu sync.Mutex

// NewPubSub create new publisher or subscriber
func NewPubSub(endPointURI *types.URI, resource string) pubsub.PubSub {
	return pubsub.PubSub{
		EndPointURI: endPointURI,
		Resource:    resource,
	}
}

// New creates empty publisher or subscriber
func New() pubsub.PubSub {
	return pubsub.PubSub{}
}

// GetAPIInstance get event instance
func GetAPIInstance(storeFilePath string) *API {
	once.Do(func() {
		instance = &API{
			transportEnabled: true,
			pubStore: &store.PubSubStore{
				RWMutex: sync.RWMutex{},
				Store:   map[string]*pubsub.PubSub{},
			},
			subStore: &store.PubSubStore{
				RWMutex: sync.RWMutex{},
				Store:   map[string]*pubsub.PubSub{},
			},
			subFile:       "sub.json",
			pubFile:       "pub.json",
			storeFilePath: storeFilePath,
		}
		instance.ReloadStore()
	})
	return instance
}

// ReloadStore reload store if there is any change or refresh is required
func (p *API) ReloadStore() {
	// load for file
	if b, err := loadFromFile(fmt.Sprintf("%s/%s", p.storeFilePath, p.subFile)); err == nil {
		if len(b) > 0 {
			var subs []pubsub.PubSub
			if err := json.Unmarshal(b, &subs); err == nil {
				for _, sub := range subs {
					p.subStore.Set(sub.ID, sub)
				}
			}
		}
	}
	// load for file
	if b, err := loadFromFile(fmt.Sprintf("%s/%s", p.storeFilePath, p.pubFile)); err == nil {
		if len(b) > 0 {
			var pubs []pubsub.PubSub
			if err := json.Unmarshal(b, &pubs); err == nil {
				for _, pub := range pubs {
					p.pubStore.Set(pub.ID, pub)
				}
			}
		}
	}
}

// HasTransportEnabled flag to indicate if amqp is enabled
func (p *API) HasTransportEnabled() bool {
	return p.transportEnabled
}

// DisableTransport disables usage of amqp
func (p *API) DisableTransport() {
	p.transportEnabled = false
}

// EnableTransport enable usage of amqp
func (p *API) EnableTransport() {
	p.transportEnabled = true
}

// GetFromPubStore get data from publisher store
func (p *API) GetFromPubStore(address string) (pubsub.PubSub, error) {
	for _, pub := range p.pubStore.Store {
		if pub.GetResource() == address {
			return pubsub.PubSub{
				ID:          pub.ID,
				EndPointURI: pub.EndPointURI,
				URILocation: pub.URILocation,
				Resource:    pub.Resource,
			}, nil
		}
	}
	return pubsub.PubSub{}, fmt.Errorf("publisher not found for address %s", address)
}

// GetFromSubStore get data from subscription store
func (p *API) GetFromSubStore(address string) (pubsub.PubSub, error) {
	for _, sub := range p.subStore.Store {
		if sub.GetResource() == address {
			return pubsub.PubSub{
				ID:          sub.ID,
				EndPointURI: sub.EndPointURI,
				URILocation: sub.URILocation,
				Resource:    sub.Resource,
			}, nil
		}
	}
	return pubsub.PubSub{}, fmt.Errorf("subscription not found for address %s ", address)
}

// HasSubscription check if the subscription is already exists in the store/cache
func (p *API) HasSubscription(address string) (pubsub.PubSub, bool) {
	if sub, err := p.GetFromSubStore(address); err == nil {
		return sub, true
	}
	return pubsub.PubSub{}, false
}

// HasPublisher check if the publisher is already exists in the store/cache
func (p *API) HasPublisher(address string) (pubsub.PubSub, bool) {
	if pub, err := p.GetFromPubStore(address); err == nil {
		return pub, true
	}
	return pubsub.PubSub{}, false
}

// CreateSubscription create a subscription and store it in a file and cache
func (p *API) CreateSubscription(sub pubsub.PubSub) (pubsub.PubSub, error) {
	if subExists, ok := p.HasSubscription(sub.GetResource()); ok {
		log.Warnf("there was already a subscription in the store,skipping creation %v", subExists)
		p.subStore.Set(sub.ID, subExists)
		return subExists, nil
	}
	if sub.ID == "" { //this will be always set by rest api
		sub.SetID(uuid.New().String())
	}
	// persist the subscription -
	//TODO:might want to use PVC to live beyond pod crash
	err := writeToFile(sub, fmt.Sprintf("%s/%s", p.storeFilePath, p.subFile))
	if err != nil {
		log.Errorf("error writing to a store %v\n", err)
		return pubsub.PubSub{}, err
	}
	log.Infof("subscription persisted into a file %s", fmt.Sprintf("%s/%s  - content %s", p.storeFilePath, p.subFile, sub.String()))
	// store the publisher
	p.subStore.Set(sub.ID, sub)
	return sub, nil
}

// CreatePublisher create a publisher data and store it a file and cache
func (p *API) CreatePublisher(pub pubsub.PubSub) (pubsub.PubSub, error) {
	if pubExists, ok := p.HasPublisher(pub.GetResource()); ok {
		log.Warnf("There was already a publisher, skipping creation %v", pubExists)
		p.pubStore.Set(pub.ID, pubExists)
		return pubExists, nil
	}
	if pub.ID == "" { //this will be always set by rest api
		pub.SetID(uuid.New().String())
	}
	// persist the subscription -
	//TODO:might want to use PVC to live beyond pod crash
	err := writeToFile(pub, fmt.Sprintf("%s/%s", p.storeFilePath, p.pubFile))
	if err != nil {
		log.Errorf("error writing to a store %v\n", err)
		return pubsub.PubSub{}, err
	}
	log.Infof("publisher persisted into a file %s", fmt.Sprintf("%s/%s  - content %s", p.storeFilePath, p.pubFile, pub.String()))
	// store the publisher
	p.pubStore.Set(pub.ID, pub)
	return pub, nil
}

// GetSubscription  get a subscription by it's id
func (p *API) GetSubscription(subscriptionID string) (pubsub.PubSub, error) {
	if sub, ok := p.subStore.Store[subscriptionID]; ok {
		return *sub, nil
	}
	return pubsub.PubSub{}, fmt.Errorf("subscription data was not found for id %s", subscriptionID)
}

// GetPublisher get a publisher by it's id
func (p *API) GetPublisher(publisherID string) (pubsub.PubSub, error) {
	if sub, ok := p.pubStore.Store[publisherID]; ok {
		return *sub, nil
	}
	return pubsub.PubSub{}, fmt.Errorf("publisher data was not found for id %s", publisherID)
}

// GetSubscriptions  get all subscription inforamtions
func (p *API) GetSubscriptions() map[string]*pubsub.PubSub {
	return p.subStore.Store
}

// GetPublishers  get all publishers information
func (p *API) GetPublishers() map[string]*pubsub.PubSub {
	return p.pubStore.Store
}

// DeletePublisher delete a publisher by id
func (p *API) DeletePublisher(publisherID string) error {
	log.Info("deleting publisher")
	if pub, ok := p.pubStore.Store[publisherID]; ok {
		err := deleteFromFile(*pub, fmt.Sprintf("%s/%s", p.storeFilePath, p.pubFile))
		p.pubStore.Delete(publisherID)
		return err
	}
	return nil
}

// DeleteSubscription delete a subscription by id
func (p *API) DeleteSubscription(subscriptionID string) error {
	log.Info("deleting subscription")
	if pub, ok := p.subStore.Store[subscriptionID]; ok {
		err := deleteFromFile(*pub, fmt.Sprintf("%s/%s", p.storeFilePath, p.subFile))
		p.subStore.Delete(subscriptionID)
		return err
	}
	return nil
}

// DeleteAllSubscriptions  delete all subscription information
func (p *API) DeleteAllSubscriptions() error {
	log.Info("deleting all subscription")
	if err := deleteAllFromFile(fmt.Sprintf("%s/%s", p.storeFilePath, p.subFile)); err != nil {
		return err
	}
	// empty the store
	p.subStore.Store = make(map[string]*pubsub.PubSub)
	return nil
}

// DeleteAllPublishers delete all the publisher information the store and cache.
func (p *API) DeleteAllPublishers() error {
	log.Info("deleting all publishers")
	if err := deleteAllFromFile(fmt.Sprintf("%s/%s", p.storeFilePath, p.pubFile)); err != nil {
		return err
	}
	//empty the store
	p.pubStore.Store = make(map[string]*pubsub.PubSub)
	return nil
}

// GetPublishersFromFile  get publisher data from the file store
func (p *API) GetPublishersFromFile() ([]byte, error) {
	b, err := loadFromFile(fmt.Sprintf("%s/%s", p.storeFilePath, p.pubFile))
	return b, err
}

// GetSubscriptionsFromFile  get subscriptions data from the file store
func (p *API) GetSubscriptionsFromFile() ([]byte, error) {
	b, err := loadFromFile(fmt.Sprintf("%s/%s", p.storeFilePath, p.subFile))
	return b, err
}

// deleteAllFromFile deletes  publisher and subscription information from the file system
func deleteAllFromFile(filePath string) error {
	//open file
	if err := os.WriteFile(filePath, []byte{}, 0666); err != nil {
		return err
	}
	return nil
}

// DeleteFromFile is used to delete subscription from the file system
func deleteFromFile(sub pubsub.PubSub, filePath string) error {
	//open file
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	//read file and unmarshall json file to slice of users
	b, err := io.ReadAll(file)
	if err != nil {
		return err
	}
	var allSubs []pubsub.PubSub
	if len(b) > 0 {
		err = json.Unmarshal(b, &allSubs)
		if err != nil {
			return err
		}
	}
	for k := range allSubs {
		// Remove the element at index i from a.
		if allSubs[k].ID == sub.ID {
			allSubs[k] = allSubs[len(allSubs)-1]      // Copy last element to index i.
			allSubs[len(allSubs)-1] = pubsub.PubSub{} // Erase last element (write zero value).
			allSubs = allSubs[:len(allSubs)-1]        // Truncate slice.
			break
		}
	}
	newBytes, err := json.MarshalIndent(&allSubs, "", " ")
	if err != nil {
		log.Errorf("error deleting sub %v", err)
		return err
	}
	if err := os.WriteFile(filePath, newBytes, 0666); err != nil {
		return err
	}
	return nil
}

// loadFromFile is used to read subscription/publisher from the file system
func loadFromFile(filePath string) (b []byte, err error) {
	//open file
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	//read file and unmarshall json file to slice of users
	b, err = io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// writeToFile writes subscription data to a file
func writeToFile(sub pubsub.PubSub, filePath string) error {
	//open file
	mu.Lock()
	defer mu.Unlock()
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	//read file and unmarshall json file to slice of users
	b, err := io.ReadAll(file)
	if err != nil {
		return err
	}
	var allSubs []pubsub.PubSub
	if len(b) > 0 {
		err = json.Unmarshal(b, &allSubs)
		if err != nil {
			return err
		}
	}
	allSubs = append(allSubs, sub)
	newBytes, err := json.MarshalIndent(&allSubs, "", " ")
	if err != nil {
		return err
	}
	log.Infof("persisting following contents %s to a file %s\n", string(newBytes), filePath)
	if err := os.WriteFile(filePath, newBytes, 0666); err != nil {
		return err
	}
	return nil
}
