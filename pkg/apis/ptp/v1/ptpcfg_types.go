package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PtpCfgSpec defines the desired state of PtpCfg
// +k8s:openapi-gen=true
type PtpCfgSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
	Profile		[]PtpProfile	`json:"profile"`
	Recommend	[]PtpRecommend	`json:"recommend"`
}

type PtpProfile struct {
	Name		*string	`json:"name"`
	Interface	*string	`json:"interface"`
	Ptp4lOpts	*string	`json:"ptp4lOpts,omitempty"`
	Phc2sysOpts	*string	`json:"phc2sysOpts,omitempty"`
	Ptp4lConf	*string	`json:"ptp4lConf,omitempty"`
}

type PtpRecommend struct {
	Profile		*string		`json:"profile"`
	Priority	*int64		`json:"priority"`
	Match		[]MatchRule	`json:"match,omitempty"`
}

type MatchRule struct {
	NodeLabel	*string	`json:"nodeLabel,omitempty"`
	NodeName	*string	`json:"nodeName,omitempty"`
}

// PtpCfgStatus defines the observed state of PtpCfg
// +k8s:openapi-gen=true
type PtpCfgStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
	MatchList	[]NodeMatchList	`json:"matchList,omitempty"`
}

type NodeMatchList struct {
	NodeName	*string	`json:"nodeName"`
	Profile		*string	`json:"profile"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PtpCfg is the Schema for the ptpcfgs API
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
type PtpCfg struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PtpCfgSpec   `json:"spec,omitempty"`
	Status PtpCfgStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PtpCfgList contains a list of PtpCfg
type PtpCfgList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PtpCfg `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PtpCfg{}, &PtpCfgList{})
}
