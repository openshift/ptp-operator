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
	"net/http"
	"os"
	"strings"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	openshifttls "github.com/openshift/controller-runtime-common/pkg/tls"
	libgocrypto "github.com/openshift/library-go/pkg/crypto"

	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/names"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	ptpv2alpha1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v2alpha1"
	"github.com/k8snetworkplumbingwg/ptp-operator/controllers"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/leaderelection"
	corev1 "k8s.io/api/core/v1"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(ptpv1.AddToScheme(scheme))
	utilruntime.Must(ptpv2alpha1.AddToScheme(scheme))
	utilruntime.Must(configv1.Install(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var enableHTTP2 bool
	flag.BoolVar(&enableHTTP2, "enable-http2", enableHTTP2, "If HTTP/2 should be enabled for the metrics and webhook servers.")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.Parse()
	restConfig := ctrl.GetConfigOrDie()
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	le := leaderelection.GetLeaderElectionConfig(restConfig, enableLeaderElection)

	// Fetch the TLS security profile and adherence policy from the APIServer CR.
	// Defaults to the Intermediate profile if the CR is not accessible.
	tlsProfileSpec, tlsAdherencePolicy, err := fetchTLSConfig(restConfig)
	isOpenShiftCluster := err == nil
	if !isOpenShiftCluster {
		setupLog.Info("unable to fetch TLS config from APIServer CR, using Intermediate profile", "error", err)
		tlsProfileSpec, _ = openshifttls.GetTLSProfileSpec(nil)
	}
	honorClusterTLS := libgocrypto.ShouldHonorClusterTLSProfile(tlsAdherencePolicy)
	setupLog.Info("TLS security profile resolved",
		"minTLSVersion", tlsProfileSpec.MinTLSVersion,
		"ciphers", tlsProfileSpec.Ciphers,
		"tlsAdherence", tlsAdherencePolicy,
		"honorClusterTLSProfile", honorClusterTLS)

	var tlsOption func(*tls.Config)
	if honorClusterTLS {
		var unsupportedCiphers []string
		tlsOption, unsupportedCiphers = openshifttls.NewTLSConfigFromProfile(tlsProfileSpec)
		if len(unsupportedCiphers) > 0 {
			setupLog.Info("some ciphers from the TLS profile are not supported", "unsupportedCiphers", unsupportedCiphers)
		}
	} else {
		setupLog.Info("TLS adherence is legacy, using default TLS configuration")
		tlsOption = func(*tls.Config) {} // no-op: use Go defaults
	}
	disableHTTP2 := func(c *tls.Config) {
		if !enableHTTP2 {
			c.NextProtos = []string{"http/1.1"}
		}
	}

	webhookServerOptions := webhook.Options{
		TLSOpts: []func(config *tls.Config){tlsOption, disableHTTP2},
		Port:    9443,
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	options := ctrl.Options{
		Scheme:                        scheme,
		HealthProbeBindAddress:        probeAddr,
		LeaderElection:                enableLeaderElection,
		LeaseDuration:                 &le.LeaseDuration.Duration,
		RenewDeadline:                 &le.RenewDeadline.Duration,
		RetryPeriod:                   &le.RetryPeriod.Duration,
		LeaderElectionID:              "ptp.openshift.io",
		LeaderElectionReleaseOnCancel: true,
		Metrics: server.Options{
			BindAddress: metricsAddr,
			TLSOpts:     []func(config *tls.Config){tlsOption, disableHTTP2},
		},
		WebhookServer: webhookServer,
	}

	namespace := os.Getenv("WATCH_NAMESPACE")
	// create multi namespace cache if list of namespaces
	if namespace != "" {
		defaultNamespaces := map[string]cache.Config{}

		for _, namespace := range strings.Split(namespace, ",") {
			defaultNamespaces[namespace] = cache.Config{}
		}

		options.NewCache = func(config *rest.Config, opts cache.Options) (cache.Cache, error) {
			opts.DefaultNamespaces = defaultNamespaces
			// Restrict Secret watching to openshift-ptp namespace only
			opts.ByObject = map[client.Object]cache.ByObject{
				&corev1.Secret{}: {
					Namespaces: map[string]cache.Config{
						names.Namespace: {},
					},
				},
			}
			return cache.New(config, opts)
		}
		setupLog.Info(fmt.Sprintf("Namespaces added to the cache: %s", namespace))
	} else {
		// If no WATCH_NAMESPACE is set, still restrict Secret watching to openshift-ptp namespace
		options.Cache = cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				&corev1.Secret{}: {
					Namespaces: map[string]cache.Config{
						names.Namespace: {},
					},
				},
			},
		}
		setupLog.Info("Restricting Secret watching to openshift-ptp namespace only")
	}

	mgr, err := ctrl.NewManager(restConfig, options)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controllers.PtpOperatorConfigReconciler{
		Client:             mgr.GetClient(),
		Log:                ctrl.Log.WithName("controllers").WithName("PtpOperatorConfig"),
		Scheme:             mgr.GetScheme(),
		TLSProfileSpec:     tlsProfileSpec,
		TLSAdherencePolicy: tlsAdherencePolicy,
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

	if err = (&controllers.HardwareConfigReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("HardwareConfig"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "HardwareConfig")
		os.Exit(1)
	}

	// Set up the TLS security profile watcher to detect profile changes at runtime.
	// When the APIServer TLS profile changes, the watcher triggers a graceful shutdown
	// so the operator restarts with the new TLS configuration.
	ctx, cancel := context.WithCancel(ctrl.SetupSignalHandler())
	defer cancel()

	if isOpenShiftCluster {
		if err = (&openshifttls.SecurityProfileWatcher{
			Client:                    mgr.GetClient(),
			InitialTLSProfileSpec:     tlsProfileSpec,
			InitialTLSAdherencePolicy: tlsAdherencePolicy,
			OnProfileChange: func(ctx context.Context, oldProfile, newProfile configv1.TLSProfileSpec) {
				setupLog.Info("TLS security profile changed, initiating graceful shutdown",
					"oldMinTLSVersion", oldProfile.MinTLSVersion,
					"newMinTLSVersion", newProfile.MinTLSVersion)
				cancel()
			},
			OnAdherencePolicyChange: func(ctx context.Context, oldPolicy, newPolicy configv1.TLSAdherencePolicy) {
				setupLog.Info("TLS adherence policy changed, initiating graceful shutdown",
					"oldPolicy", oldPolicy,
					"newPolicy", newPolicy)
				cancel()
			},
		}).SetupWithManager(mgr); err != nil {
			setupLog.Info("unable to set up TLS security profile watcher, continuing without it", "error", err)
		}
	} else {
		setupLog.Info("TLS security profile watcher not started (non-OpenShift cluster)")
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

	checker := mgr.GetWebhookServer().StartedChecker()
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", checker); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", checker); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	go func() {
		// Wait until the webhook server is ready.
		setupLog.Info("waiting for validating webhook to be ready")
		err = waitForWebhookServer(checker)
		if err != nil {
			setupLog.Error(err, "unable to create default PtpOperatorConfig due to webhook not ready")
		} else {
			// create default before the webhook are setup
			err = createDefaultOperatorConfig(ctrl.GetConfigOrDie())
			if err != nil {
				setupLog.Error(err, "unable to create default PtpOperatorConfig")
			}
		}
	}()
	setupLog.Info("starting manager")
	if err = mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}

}

func createDefaultOperatorConfig(cfg *rest.Config) error {
	logger := setupLog.WithName("createDefaultOperatorConfig")
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("couldn't create client: %v", err)
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

func setupChecks(mgr ctrl.Manager, checker healthz.Checker) {
	if err := mgr.AddReadyzCheck("webhook", checker); err != nil {
		setupLog.Error(err, "unable to create ready check")
		os.Exit(1)
	}
	if err := mgr.AddHealthzCheck("webhook", checker); err != nil {
		setupLog.Error(err, "unable to create health check")
		os.Exit(1)
	}
}

// fetchTLSConfig creates a temporary client to read the APIServer TLS profile
// and adherence policy at startup, before the manager's cache is available.
func fetchTLSConfig(cfg *rest.Config) (configv1.TLSProfileSpec, configv1.TLSAdherencePolicy, error) {
	s := runtime.NewScheme()
	if err := configv1.Install(s); err != nil {
		return configv1.TLSProfileSpec{}, "", fmt.Errorf("failed to add configv1 to scheme: %v", err)
	}
	c, err := client.New(cfg, client.Options{Scheme: s})
	if err != nil {
		return configv1.TLSProfileSpec{}, "", fmt.Errorf("failed to create client: %v", err)
	}
	profileSpec, err := openshifttls.FetchAPIServerTLSProfile(context.TODO(), c)
	if err != nil {
		return configv1.TLSProfileSpec{}, "", err
	}
	adherencePolicy, err := openshifttls.FetchAPIServerTLSAdherencePolicy(context.TODO(), c)
	if err != nil {
		// Adherence policy fetch may fail on older clusters without the field.
		// Default to no opinion (legacy behavior).
		setupLog.Info("unable to fetch TLS adherence policy, defaulting to legacy behavior", "error", err)
		adherencePolicy = configv1.TLSAdherencePolicyNoOpinion
	}
	return profileSpec, adherencePolicy, nil
}

// waitForWebhookServer waits until the webhook server is ready.
func waitForWebhookServer(checker func(req *http.Request) error) error {
	const (
		timeout     = 30 * time.Second // Adjust timeout as needed
		pollingFreq = 1 * time.Second  // Polling frequency
	)
	start := time.Now()

	// Create an HTTP request to check the readiness of the webhook server.
	req, err := http.NewRequest("GET", "https://localhost:9443/healthz", nil)
	if err != nil {
		return err
	}

	// Poll the checker function until it returns nil (indicating success)
	// or until the timeout is reached.
	for {
		if err = checker(req); err == nil {
			return nil
		} else if time.Since(start) > timeout {
			return fmt.Errorf("timeout waiting for webhook server to start")
		}
		time.Sleep(pollingFreq) // Poll every second
	}
}
