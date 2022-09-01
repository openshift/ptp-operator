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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PtpHAConfigSpec defines the desired state of PtpHAConfig
type PtpHAConfigSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of PtpHAConfig. Edit ptphaconfig_types.go to remove/update
	HighAvailability HighAvailability `json:"highAvailability,omitempty"`
}

// PtpHAConfigStatus defines the observed state of PtpHAConfig
type PtpHAConfigStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// PtpHAConfig is the Schema for the ptphaconfigs API
type PtpHAConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PtpHAConfigSpec   `json:"spec,omitempty"`
	Status PtpHAConfigStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// PtpHAConfigList contains a list of PtpHAConfig
type PtpHAConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PtpHAConfig `json:"items"`
}

// HighAvailability configuration for ptpConfig
type HighAvailability struct {
	// +kubebuilder.default:=false
	EnableHA *bool `json:"enableHA"`
	// +optional
	PreferredPrimary *string `json:"preferredPrimary,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +optional
	HeartBeatTimeOut *int32 `json:"heartBeatTimeout,omitempty"`
}

func init() {
	SchemeBuilder.Register(&PtpHAConfig{}, &PtpHAConfigList{})
}
