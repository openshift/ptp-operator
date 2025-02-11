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

package event

import (
	"fmt"
	"regexp"
)

// DataType
//
// swagger:type string
type DataType string

const (
	// NOTIFICATION ...
	NOTIFICATION DataType = "notification"
	// METRIC ...
	METRIC DataType = "metric"
)

// ValueType
//
// swagger:type string
type ValueType string

const (
	// ENUMERATION ...
	ENUMERATION ValueType = "enumeration"
	// DECIMAL ...
	DECIMAL ValueType = "decimal64.3"
	// REDFISH_EVENT ...
	REDFISH_EVENT ValueType = "redfish-event" //nolint:all
)

// Data
//
// Array of JSON objects defining the information for the event.
//
// Example:
// ```go
//
//	{
//	  "version": "v1.0",
//	  "values": [{
//	    "ResourceAddress": "/sync/sync-status/sync-state",
//	    "data_type": "notification",
//	    "value_type": "enumeration",
//	    "value": "ACQUIRING-SYNC"
//	    }, {
//	    "ResourceAddress": "/sync/sync-status/sync-state",
//	    "data_type": "metric",
//	    "value_type": "decimal64.3",
//	    "value": 100.3
//	    }
//	  }]
//	}
//
// ```
type Data struct {
	// example: 1.0
	Version string      `json:"version" example:"1.0"`
	Values  []DataValue `json:"values"`
}

// DataValue
//
// A json array of values defining the event.
//
// Example:
// ```go
//
//	{
//	  "ResourceAddress": "/cluster/node/ptp",
//	  "data_type": "notification",
//	  "value_type": "enumeration",
//	  "value": "ACQUIRING-SYNC"
//	}
//
// ```
type DataValue struct {
	// The resource address specifies the Event Producer with a hierarchical path. Currently hierarchical paths with wild cards are not supported.
	// example: /east-edge-10/Node3/sync/sync-status/sync-state
	Resource string `json:"ResourceAddress" example:"/east-edge-10/Node3/sync/sync-status/sync-state"`
	// Type of value object. ( notification | metric)
	// example: notification
	DataType DataType `json:"data_type" example:"notification"`
	// The type format of the value property.
	// example: enumeration
	ValueType ValueType `json:"value_type" example:"enumeration"`
	// value in value_type format.
	// example: HOLDOVER
	Value interface{} `json:"value" example:"HOLDOVER"`
}

// SetVersion  ...
func (d *Data) SetVersion(s string) error {
	d.Version = s
	if s == "" {
		err := fmt.Errorf("version cannot be empty")
		return err
	}
	return nil
}

// SetValues ...
func (d *Data) SetValues(v []DataValue) {
	d.Values = v
}

// AppendValues ...
func (d *Data) AppendValues(v DataValue) {
	d.Values = append(d.Values, v)
}

// GetVersion ...
func (d *Data) GetVersion() string {
	return d.Version
}

// GetValues ...
func (d *Data) GetValues() []DataValue {
	return d.Values
}

// GetResource ...
func (v *DataValue) GetResource() string {
	return v.Resource
}

// SetResource ...
func (v *DataValue) SetResource(r string) error {
	matched, err := regexp.MatchString(`([^/]+(/{2,}[^/]+)?)`, r)
	if matched {
		v.Resource = r
	} else {
		return err
	}
	return nil
}
