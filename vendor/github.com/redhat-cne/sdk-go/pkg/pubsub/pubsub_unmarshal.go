// Copyright 2024 The Cloud Native Events Authors
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
	"fmt"
	"io"
	"sync"

	jsoniter "github.com/json-iterator/go"
	"github.com/redhat-cne/sdk-go/pkg/common"
)

var iterPool = sync.Pool{
	New: func() interface{} {
		return jsoniter.Parse(jsoniter.ConfigFastest, nil, 1024)
	},
}

func borrowIterator(reader io.Reader) *jsoniter.Iterator {
	iter := iterPool.Get().(*jsoniter.Iterator)
	iter.Reset(reader)
	return iter
}

func returnIterator(iter *jsoniter.Iterator) {
	iter.Error = nil
	iter.Attachment = nil
	iterPool.Put(iter)
}

// ReadJSON ...
func ReadJSON(out *PubSub, reader io.Reader) error {
	iterator := borrowIterator(reader)
	defer returnIterator(iterator)
	return readJSONFromIterator(out, iterator)
}

// readJSONFromIterator allows you to read the bytes reader as an PubSub
func readJSONFromIterator(out *PubSub, iterator *jsoniter.Iterator) error {
	var (
		id          string
		endpointUri string //nolint:revive
		uriLocation string
		resource    string
		version     string
	)

	for key := iterator.ReadObject(); key != ""; key = iterator.ReadObject() {
		// Check if we have some error in our error cache
		if iterator.Error != nil {
			return iterator.Error
		}

		// If no specversion ...
		switch key {
		case "SubscriptionId":
			id = iterator.ReadString()
		case "EndpointUri":
			endpointUri = iterator.ReadString()
		case "UriLocation":
			uriLocation = iterator.ReadString()
		case "ResourceAddress":
			resource = iterator.ReadString()
			version = common.RestAPIV2
		case "id":
			id = iterator.ReadString()
		case "endpointUri":
			endpointUri = iterator.ReadString()
		case "uriLocation":
			uriLocation = iterator.ReadString()
		case "resource":
			resource = iterator.ReadString()
			version = common.RestAPIV1
		default:
			iterator.Skip()
		}
	}

	if iterator.Error != nil {
		return iterator.Error
	}
	// Skip checking EndPointURI here since it is not used in http transport.
	// Check EndPointURI in O-RAN REST API handler
	//if endpointUri == "" {
	//	return fmt.Errorf("mandatory field EndPointURI is not set")
	//}
	if resource == "" {
		return fmt.Errorf("mandatory field ResourceAddress is not set")
	}
	out.SetID(id)
	out.SetEndpointURI(endpointUri) //nolint:errcheck
	out.SetURILocation(uriLocation) //nolint:errcheck
	out.SetResource(resource)       //nolint:errcheck
	out.SetVersion(version)

	return nil
}

// UnmarshalJSON implements the json unmarshal method used when this type is
// unmarshaled using json.Unmarshal.
func (d *PubSub) UnmarshalJSON(b []byte) error {
	iterator := jsoniter.ConfigFastest.BorrowIterator(b)
	defer jsoniter.ConfigFastest.ReturnIterator(iterator)
	return readJSONFromIterator(d, iterator)
}
