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

package v1

import (
	"errors"

	semver "github.com/Masterminds/semver/v3"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var ptpoperatorconfiglog = logf.Log.WithName("ptpoperatorconfig-resource")

var k8sclient client.Client

func (r *PtpOperatorConfig) SetupWebhookWithManager(mgr ctrl.Manager, client client.Client) error {
	k8sclient = client
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

//+kubebuilder:webhook:path=/validate-ptp-openshift-io-v1-ptpoperatorconfig,mutating=false,failurePolicy=fail,sideEffects=None,groups=ptp.openshift.io,resources=ptpoperatorconfigs,verbs=create;update,versions=v1,name=vptpoperatorconfig.kb.io,admissionReviewVersions=v1

func (r *PtpOperatorConfig) validate() error {
	if r.GetName() != "default" {
		return errors.New("PtpOperatorConfig name must be 'default'. Only one 'default' PtpOperatorConfig configuration is allowed")
	}

	if r.Spec.EventConfig != nil && r.Spec.EventConfig.EnableEventPublisher {
		if r.Spec.EventConfig.ApiVersion != "" {
			if !isValidVersion(r.Spec.EventConfig.ApiVersion) {
				return errors.New("ptpEventConfig.apiVersion=" +
					r.Spec.EventConfig.ApiVersion +
					" is not a valid version. Valid version is \"2.0\".")
			}
			if r.Spec.EventConfig.ApiVersion == "1.0" {
				return errors.New("v1 is no longer supported and has reached End " +
					"of Life (EOL). PTP event functionality is now available only in " +
					"v2. Consumers using v1 will no longer be able to communicate " +
					"with the PTP event system. Please upgrade to v2 and follow " +
					"the documentation to make the necessary changes.")
			}
		}
	}

	return nil
}

var _ webhook.Validator = &PtpOperatorConfig{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *PtpOperatorConfig) ValidateCreate() (admission.Warnings, error) {
	ptpoperatorconfiglog.Info("validate create", "name", r.Name)
	if err := r.validate(); err != nil {
		return admission.Warnings{}, err
	}
	return admission.Warnings{}, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *PtpOperatorConfig) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	ptpoperatorconfiglog.Info("validate update", "name", r.Name)
	if err := r.validate(); err != nil {
		return admission.Warnings{}, err
	}
	return admission.Warnings{}, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *PtpOperatorConfig) ValidateDelete() (admission.Warnings, error) {
	ptpoperatorconfiglog.Info("validate delete", "name", r.Name)
	return admission.Warnings{}, nil
}

// check if the version is valid based semanic versioning (semver.org)
func isValidVersion(version string) bool {
	_, err := semver.NewVersion(version)
	return err == nil
}
