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

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	ptpv1 "github.com/openshift/ptp-operator/api/v1"
	"github.com/openshift/ptp-operator/controllers"
	"github.com/openshift/ptp-operator/pkg/leaderelection"
	"github.com/openshift/ptp-operator/pkg/names"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(ptpv1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var enableHTTP2 bool

	flag.BoolVar(&enableHTTP2, "enable-http2", enableHTTP2, "If HTTP/2 should be enabled for the metrics and webhook servers.")
	flag.StringVar(&metricsAddr, "metrics-addr", "0", "The address the metric endpoint binds to.") // Setting to 0, we don't need metrics
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.Parse()
	restConfig := ctrl.GetConfigOrDie()
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	le := leaderelection.GetLeaderElectionConfig(restConfig, enableLeaderElection)

	disableHTTP2 := func(c *tls.Config) {
		if enableHTTP2 {
			return
		}
		c.NextProtos = []string{"http/1.1"}
	}

	namespace := os.Getenv("WATCH_NAMESPACE")
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		Port:               9443,
		LeaderElection:     enableLeaderElection,
		LeaseDuration:      &le.LeaseDuration.Duration,
		RenewDeadline:      &le.RenewDeadline.Duration,
		RetryPeriod:        &le.RetryPeriod.Duration,
		LeaderElectionID:   "ptp.openshift.io",
		Namespace:          namespace,
		TLSOpts:            []func(config *tls.Config){disableHTTP2},
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controllers.PtpOperatorConfigReconciler{
		Client:        mgr.GetClient(),
		Log:           ctrl.Log.WithName("controllers").WithName("PtpOperatorConfig"),
		Scheme:        mgr.GetScheme(),
		IsInitialSync: true,
		TransportHostStatus: &controllers.EventTransportHostStatus{
			TransportHostRetryCount: 0,
			LastTransportHostValue:  "",
			Success:                 false,
		},
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PtpOperatorConfig")
		os.Exit(1)
	}

	if err = (&controllers.PtpConfigReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("PtpConfig"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PtpConfig")
		os.Exit(1)
	}
	if err = (&ptpv1.PtpConfig{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "PtpConfig")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err = (&ptpv1.PtpOperatorConfig{}).SetupWebhookWithManager(mgr, mgr.GetClient()); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "PtpOperatorConfig")
		os.Exit(1)
	}

	err = createDefaultOperatorConfig(ctrl.GetConfigOrDie())
	if err != nil {
		setupLog.Error(err, "unable to create default PtpOperatorConfig")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func createDefaultOperatorConfig(cfg *rest.Config) error {
	logger := setupLog.WithName("createDefaultOperatorConfig")
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("Couldn't create client: %v", err)
	}
	config := &ptpv1.PtpOperatorConfig{
		Spec: ptpv1.PtpOperatorConfigSpec{
			DaemonNodeSelector: map[string]string{},
		},
	}
	err = c.Get(context.TODO(), types.NamespacedName{
		Name: names.DefaultOperatorConfigName, Namespace: names.Namespace}, config)

	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Create default OperatorConfig")
			config.Namespace = names.Namespace
			config.Name = names.DefaultOperatorConfigName
			err = c.Create(context.TODO(), config)
			if err != nil {
				return err
			}
		}
		// Error reading the object - requeue the request.
		return err
	}
	return nil
}
