package k8sutil

import (
	"context"
	"fmt"
	"time"

	"github.com/k8snetworkplumbingwg/ptp-operator/test/pkg/client"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

// PrivilegedDaemonsetNamespaceStuckDeleteWait is how long to allow a *Terminating* namespace
// to disappear before calling into github.com/redhat-cne/privileged-daemonset, whose
// own namespace delete wait is only 2 minutes.
const PrivilegedDaemonsetNamespaceStuckDeleteWait = 12 * time.Minute

// PreWaitPrivilegedDSNamespaceIfTerminating returns nil when the namespace is absent, active,
// or not yet being deleted. If the namespace exists with DeletionTimestamp set (Terminating
// and possibly stuck on finalizers), it polls until NotFound or the timeout. Active namespaces
// are left to privileged-daemonset to delete+recreate.
func PreWaitPrivilegedDSNamespaceIfTerminating(ctx context.Context, name string, timeout time.Duration) error {
	ns, err := client.Client.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get namespace %q: %w", name, err)
	}
	if ns.DeletionTimestamp == nil {
		return nil
	}
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		_, e := client.Client.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(e) {
			return true, nil
		}
		if e != nil {
			return false, e
		}
		return false, nil
	})
}
