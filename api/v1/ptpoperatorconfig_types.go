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
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Event Enabled",type="boolean",JSONPath=".spec.ptpEventConfig.enableEventPublisher",description="Event Enabled"
// +kubebuilder:validation:XValidation:message="PtpOperatorConfig is a singleton, metadata.name must be 'default'", rule="self.metadata.name == 'default'"

// PtpOperatorConfig is the Schema for the ptpoperatorconfigs API
type PtpOperatorConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PtpOperatorConfigSpec   `json:"spec,omitempty"`
	Status PtpOperatorConfigStatus `json:"status,omitempty"`
}

// PtpOperatorConfigSpec defines the desired state of PtpOperatorConfig.
type PtpOperatorConfigSpec struct {
	// DaemonNodeSelector specifies the node selector for the linuxptp daemon.
	// This is a map of key-value pairs used to select the nodes where the
	// linuxptp daemon will run.
	// If empty {}, the linuxptp daemon will be deployed on each node of the cluster.
	// +required
	DaemonNodeSelector map[string]string `json:"daemonNodeSelector"`

	// EventConfig contains the configuration settings for the PTP event sidecar.
	// This field is optional and can be omitted if event sidecar configuration is not required.
	// +optional
	EventConfig *PtpEventConfig `json:"ptpEventConfig,omitempty"`

	// EnabledPlugins is a map of plugin names to their configuration settings.
	// Each entry in the map specifies the configuration for a specific plugin.
	// This field is optional and can be omitted if no plugins are enabled.
	// +optional
	EnabledPlugins *map[string]*apiextensions.JSON `json:"plugins,omitempty"`
}

// PtpEventConfig defines the desired state of event framework
type PtpEventConfig struct {
	// +kubebuilder:default=false
	// EnableEventPublisher will deploy event proxy as a sidecar
	// +optional
	EnableEventPublisher bool `json:"enableEventPublisher,omitempty"`

	// TransportHost format is <protocol>://<transport-service>.<namespace>.svc.cluster.local:<transport-port>
	// Example HTTP transport: "http://ptp-event-publisher-service-NODE_NAME.openshift-ptp.svc.cluster.local:9043"
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Transport Host",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:text"}
	// +optional
	TransportHost string `json:"transportHost,omitempty"`

	// StorageType is the type of storage to store subscription data
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Storage Type",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:text"}
	// +optional
	StorageType string `json:"storageType,omitempty"`

	// ApiVersion is used to determine which API is used for the event service
	// 1.0: default version. event service is mapped to internal REST-API.
	// 2.x: event service is mapped to O-RAN v3.0 Compliant O-Cloud Notification REST-API.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="ApiVersion",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:text"}
	// +optional
	ApiVersion string `json:"apiVersion,omitempty"`
}

// PtpOperatorConfigStatus defines the observed state of PtpOperatorConfig
type PtpOperatorConfigStatus struct {
}

// +kubebuilder:object:root=true

// PtpOperatorConfigList contains a list of PtpOperatorConfig
type PtpOperatorConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PtpOperatorConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PtpOperatorConfig{}, &PtpOperatorConfigList{})
}
