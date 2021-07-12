package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PtpOperatorConfigSpec defines the desired state of PtpOperatorConfig
// +k8s:openapi-gen=true
type PtpOperatorConfigSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
	DaemonNodeSelector map[string]string `json:"daemonNodeSelector"`
	EventConfig PtpEventConfig `json:"ptpEventConfig,omitempty"`
}

// PtpOperatorConfigStatus defines the observed state of PtpOperatorConfig
// +k8s:openapi-gen=true
type PtpOperatorConfigStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
}

// PtpEventConfig defines the desired state of event framework
// +k8s:openapi-gen=true
type PtpEventConfig struct {
	//EnableEventPublisher will deploy event proxy as a sidecar
	EnableEventPublisher  bool `json:"enableEventPublisher,omitempty"`
	//clock state to stay in holdover state in secs
	HoldOverTimeout int64 `json:"holdOverTimeout,omitempty"`
	//max offset in nano secs
	MaxOffsetThreshold int64 `json:"maxOffsetThreshold,omitempty"`
	//min offset in nano secs
	MinOffsetThreshold int64 `json:"minOffsetThreshold,omitempty"`
    // transportHost address for event messages
	TransportHost string `json:"transportHost,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PtpOperatorConfig is the Schema for the ptpoperatorconfigs API
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=ptpoperatorconfigs,scope=Namespaced
type PtpOperatorConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PtpOperatorConfigSpec   `json:"spec,omitempty"`
	Status PtpOperatorConfigStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PtpOperatorConfigList contains a list of PtpOperatorConfig
type PtpOperatorConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PtpOperatorConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PtpOperatorConfig{}, &PtpOperatorConfigList{})
}
