package leaderelection

import (
	"context"
	"time"

	"github.com/golang/glog"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/config/clusterstatus"
	"github.com/openshift/library-go/pkg/config/leaderelection"
	"k8s.io/client-go/rest"
)

// GetLeaderElectionConfig returns leader election configs defaults based on the cluster topology
func GetLeaderElectionConfig(restConfig *rest.Config, enabled bool) configv1.LeaderElection {

	// Defaults follow conventions
	// https://github.com/openshift/enhancements/blob/master/CONVENTIONS.md#high-availability
	defaultLeaderElection := leaderelection.LeaderElectionDefaulting(
		configv1.LeaderElection{
			Disable: !enabled,
		},
		"", "",
	)

	if enabled {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
		defer cancel()
		if infra, err := clusterstatus.GetClusterInfraStatus(ctx, restConfig); err == nil && infra != nil {
			if infra.ControlPlaneTopology == configv1.SingleReplicaTopologyMode {
				return leaderelection.LeaderElectionSNOConfig(defaultLeaderElection)
			}
		} else {
			glog.Warningf("unable to get cluster infrastructure status, using HA cluster values for leader election: %v", err)
		}
	}

	return defaultLeaderElection
}
