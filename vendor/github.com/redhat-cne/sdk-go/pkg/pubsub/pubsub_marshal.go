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
	"bytes"
	"fmt"
	"io"

	jsoniter "github.com/json-iterator/go"
)

// WriteJSON writes the in event in the provided writer.
// Note: this function assumes the input event is valid.
func WriteJSON(in *PubSub, writer io.Writer) error {
	stream := jsoniter.ConfigFastest.BorrowStream(writer)
	defer jsoniter.ConfigFastest.ReturnStream(stream)
	stream.WriteObjectStart()

	stream.WriteObjectField(in.GetResourceName())
	stream.WriteString(in.GetResource())

	stream.WriteMore()
	stream.WriteObjectField(in.GetEndpointURIName())
	stream.WriteString(in.GetEndpointURI())

	if in.GetID() != "" {
		stream.WriteMore()
		stream.WriteObjectField(in.GetIDName())
		stream.WriteString(in.GetID())
	}

	if in.GetURILocation() != "" {
		stream.WriteMore()
		stream.WriteObjectField(in.GetURILocationName())
		stream.WriteString(in.GetURILocation())
	}

	// Let's do a check on the error
	if stream.Error != nil {
		return fmt.Errorf("error while writing the event attributes: %w", stream.Error)
	}

	stream.WriteObjectEnd()
	return stream.Flush()
}

// MarshalJSON implements a custom json marshal method used when this type is
// marshaled using json.Marshal.
func (d PubSub) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	err := WriteJSON(&d, &buf)
	return buf.Bytes(), err
}
