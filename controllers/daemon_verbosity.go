/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"fmt"
	"os"
	"strconv"
)

const (
	defaultDaemonVerbosity = "10"
	maxDaemonVerbosity     = 14
)

func resolveDaemonVerbosity(envValue string) (string, error) {
	if envValue == "" {
		return defaultDaemonVerbosity, nil
	}
	v, err := strconv.Atoi(envValue)
	if err != nil {
		return defaultDaemonVerbosity, fmt.Errorf("invalid linuxptp-daemon verbosity %q: %v", envValue, err)
	}
	if v < 0 || v > maxDaemonVerbosity {
		return defaultDaemonVerbosity, fmt.Errorf("linuxptp-daemon verbosity %q out of range (supported: 0..%d)", envValue, maxDaemonVerbosity)
	}
	return strconv.Itoa(v), nil
}

func daemonVerbosityFromEnv() (string, error) {
	return resolveDaemonVerbosity(os.Getenv("LINUXPTP_VERBOSITY"))
}
