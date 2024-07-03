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

package common

import (
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
)

// REST API versions
const (
	V1 = "1.0"
	V2 = "2.0"
)

func getMajorVersion(version string) (int, error) {
	if version == "" {
		return 1, nil
	}
	version = strings.TrimPrefix(version, "v")
	version = strings.TrimPrefix(version, "V")
	v := strings.Split(version, ".")
	majorVersion, err := strconv.Atoi(v[0])
	if err != nil {
		log.Errorf("Error parsing major version from %s, %v", version, err)
		return 1, err
	}
	return majorVersion, nil
}

func IsV1Api(version string) bool {
	if majorVersion, err := getMajorVersion(version); err == nil {
		if majorVersion >= 2 {
			return false
		}
	}
	// by default use V1
	return true
}
