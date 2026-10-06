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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PtpConfigSpec defines the desired state of PtpConfig
type PtpConfigSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	Profile   []PtpProfile   `json:"profile"`
	Recommend []PtpRecommend `json:"recommend"`
}

// ProfileStatus reports the relationship and apply state of a single profile within this PtpConfig.
type ProfileStatus struct {
	// Name is the fully qualified profile name: <ptpconfigName>_<profileName>.
	Name string `json:"name"`

	// ControlledBy is the fully qualified name of the profile that controls this one.
	// +optional
	ControlledBy string `json:"controlledBy,omitempty"`

	// Controls lists the fully qualified names of profiles this one controls (reverse of controllingProfile).
	// +optional
	Controls []string `json:"controls,omitempty"`

	// HaMembers lists the fully qualified names of profiles coordinated by this HA profile.
	// +optional
	HaMembers []string `json:"haMembers,omitempty"`

	// PartOfHa is the fully qualified name of the HA coordinator profile this one belongs to.
	// Empty if not part of HA.
	// +optional
	PartOfHa string `json:"partOfHa,omitempty"`

	// HasPhc2sys is true if this profile has phc2sysOpts configured (syncs system clock).
	HasPhc2sys bool `json:"hasPhc2sys"`

	// ClockType is T-GM, T-BC, BC, or OC. Written by linuxptp-daemon at apply.
	// +optional
	ClockType string `json:"clockType,omitempty"`

	// AppliedOnNodes lists the nodes where the linuxptp-daemon has applied this profile.
	// This is observed from NodePtpDevice.status.sync.profiles, not from matchList.
	// +optional
	AppliedOnNodes []string `json:"appliedOnNodes,omitempty"`

	// HardwareConfigs lists HardwareConfig CRs whose relatedPtpProfileName refers to this profile.
	// +optional
	HardwareConfigs []string `json:"hardwareConfigs,omitempty"`
}

// PtpConfigStatus defines the observed state of PtpConfig
type PtpConfigStatus struct {
	// MatchList contains the nodes matched by this PtpConfig's recommend rules.
	MatchList []NodeMatchList `json:"matchList,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ProfileStatuses reports the relationship and apply state of each profile in this PtpConfig.
	// +optional
	ProfileStatuses []ProfileStatus `json:"profileStatuses,omitempty"`

	// Warnings contains any detected misconfigurations (e.g., multiple profiles with phc2sys on same node).
	// +optional
	Warnings []string `json:"warnings,omitempty"`

	// Conditions represent the latest available observations of the PtpConfig's state.
	// Known condition types are: "ProfileReferenceValid".
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Refs Valid",type="string",JSONPath=`.status.conditions[?(@.type=="ProfileReferenceValid")].status`,priority=0
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// PtpConfig is the Schema for the ptpconfigs API
type PtpConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PtpConfigSpec   `json:"spec,omitempty"`
	Status PtpConfigStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// PtpConfigList contains a list of PtpConfig
type PtpConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PtpConfig `json:"items"`
}

type PtpProfile struct {
	Name        *string `json:"name"`
	Interface   *string `json:"interface,omitempty"`
	Ptp4lOpts   *string `json:"ptp4lOpts,omitempty"`
	Phc2sysOpts *string `json:"phc2sysOpts,omitempty"`
	Ts2PhcOpts  *string `json:"ts2phcOpts,omitempty"`
	Synce4lOpts *string `json:"synce4lOpts,omitempty"`
	ChronydOpts *string `json:"chronydOpts,omitempty"`
	Ptp4lConf   *string `json:"ptp4lConf,omitempty"`
	Phc2sysConf *string `json:"phc2sysConf,omitempty"`
	Ts2PhcConf  *string `json:"ts2phcConf,omitempty"`
	Synce4lConf *string `json:"synce4lConf,omitempty"`
	ChronydConf *string `json:"chronydConf,omitempty"`
	// +kubebuilder:validation:Enum=SCHED_OTHER;SCHED_FIFO;
	PtpSchedulingPolicy *string `json:"ptpSchedulingPolicy,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65
	PtpSchedulingPriority *int64             `json:"ptpSchedulingPriority,omitempty"`
	PtpClockThreshold     *PtpClockThreshold `json:"ptpClockThreshold,omitempty"`
	// PtpSettings holds free-form, string-typed configuration knobs validated individually
	// by the PtpConfig admission webhook. Recognized keys include (non-exhaustive):
	//   - logReduce: log reduction mode ('true', 'false', 'basic', 'enhanced').
	//   - inSyncConditionThreshold: ptp4l offset threshold (ns) used by the T-BC state machine.
	//   - inSyncConditionTimes: number of consecutive in-sync samples before T-BC reports LOCKED.
	//   - stdoutFilter: regexp filtering ptp4l/phc2sys stdout.
	PtpSettings map[string]string              `json:"ptpSettings,omitempty"`
	Plugins     map[string]*apiextensions.JSON `json:"plugins,omitempty"`
}

type PtpClockThreshold struct {
	// +kubebuilder:default=5
	// clock state to stay in holdover state in secs
	HoldOverTimeout int64 `json:"holdOverTimeout,omitempty"`
	// +kubebuilder:default=100
	// max offset in nano secs
	MaxOffsetThreshold int64 `json:"maxOffsetThreshold,omitempty"`
	// DEPRECATED: min offset in nano secs. This field is no longer used for offset-range evaluation; the system now evaluates abs(offset) against maxOffsetThreshold only. The field is retained for backward compatibility with existing PtpConfig resources and its value is ignored. Do not set this field in new configurations.
	// Deprecated: MinOffsetThreshold is no longer used for offset-range evaluation; abs(offset) < MaxOffsetThreshold is evaluated instead.
	MinOffsetThreshold int64 `json:"minOffsetThreshold,omitempty"`
	// sysOffsetInSyncThreshold is the phc2sys-to-CLOCK_REALTIME offset threshold, in nanoseconds, that must be met to gate the OS Clock Sync (E3) LOCKED state. abs(offset) <= this threshold for sysOffsetSamples consecutive samples triggers LOCKED. When unset, it defaults to maxOffsetThreshold.
	// +kubebuilder:validation:Minimum=0
	// +optional
	SysOffsetInSyncThreshold *int64 `json:"sysOffsetInSyncThreshold,omitempty"`
	// sysOffsetOutOfSyncThreshold is the phc2sys-to-CLOCK_REALTIME offset threshold, in nanoseconds, that when exceeded gates the OS Clock Sync (E3) FREERUN state. abs(offset) > this threshold for sysOffsetSamples consecutive samples triggers FREERUN. Independent of sysOffsetInSyncThreshold; together they form a hysteresis band. When unset, it defaults to maxOffsetThreshold.
	// +kubebuilder:validation:Minimum=0
	// +optional
	SysOffsetOutOfSyncThreshold *int64 `json:"sysOffsetOutOfSyncThreshold,omitempty"`
	// sysOffsetSamples is the number of consecutive phc2sys offset samples required for an E3 LOCKED/FREERUN state transition in either direction (relative to sysOffsetInSyncThreshold for LOCKED, or sysOffsetOutOfSyncThreshold for FREERUN). When unset, it defaults to 10.
	// +kubebuilder:validation:Minimum=1
	// +optional
	SysOffsetSamples *int64 `json:"sysOffsetSamples,omitempty"`
	// Acceptable process downtime in seconds for each process
	ProcessDowntimeThresholds *ProcessDowntimeThresholds `json:"processDowntimeThresholds,omitempty"`
}

// ProcessDowntimeThresholds defines acceptable downtime thresholds for PTP processes
// All values are in seconds. 0 means no downtime is accepted.
type ProcessDowntimeThresholds struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=86400
	// +kubebuilder:default=5
	// Acceptable downtime for ptp4l process in seconds (max 1 day)
	Ptp4l *int `json:"ptp4l,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=86400
	// +kubebuilder:default=5
	// Acceptable downtime for phc2sys process in seconds (max 1 day)
	Phc2sys *int `json:"phc2sys,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=86400
	// +kubebuilder:default=5
	// Acceptable downtime for ts2phc process in seconds (max 1 day)
	Ts2phc *int `json:"ts2phc,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=86400
	// +kubebuilder:default=5
	// Acceptable downtime for synce4l process in seconds (max 1 day)
	Synce4l *int `json:"synce4l,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=86400
	// +kubebuilder:default=5
	// Acceptable downtime for chronyd process in seconds (max 1 day)
	Chronyd *int `json:"chronyd,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=86400
	// +kubebuilder:default=1
	// Acceptable downtime for gpsd process in seconds (max 1 day)
	Gpsd *int `json:"gpsd,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=86400
	// +kubebuilder:default=1
	// Acceptable downtime for gpspipe process in seconds (max 1 day)
	Gpspipe *int `json:"gpspipe,omitempty"`
}

type PtpRecommend struct {
	Profile  *string     `json:"profile"`
	Priority *int64      `json:"priority"`
	Match    []MatchRule `json:"match,omitempty"`
}

type MatchRule struct {
	NodeLabel *string `json:"nodeLabel,omitempty"`
	NodeName  *string `json:"nodeName,omitempty"`
}

type NodeMatchList struct {
	NodeName *string `json:"nodeName"`
	// Profile is the fully qualified profile name: <ptpconfigName>_<profileName>.
	Profile *string `json:"profile"`
}

func init() {
	SchemeBuilder.Register(&PtpConfig{}, &PtpConfigList{})
}
