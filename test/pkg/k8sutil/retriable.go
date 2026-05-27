// Package k8sutil has small helpers for Kubernetes API test flakes.
package k8sutil

import (
	"strings"
)

// IsTransientL2OrPrivilegedNamespaceError matches errors from L2 / privileged-DS
// init when a test namespace is slow to leave Terminating.
func IsTransientL2OrPrivilegedNamespaceError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	if strings.Contains(s, "failed waiting for namespace") {
		return true
	}
	if strings.Contains(s, "context deadline exceeded") {
		if strings.Contains(s, "namespace") || strings.Contains(s, "L2") || strings.Contains(s, "privileged") {
			return true
		}
	}
	return false
}
