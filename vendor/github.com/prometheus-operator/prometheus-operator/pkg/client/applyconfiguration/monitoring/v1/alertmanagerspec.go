// Copyright The prometheus-operator Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

import (
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AlertmanagerSpecApplyConfiguration represents an declarative configuration of the AlertmanagerSpec type for use
// with apply.
type AlertmanagerSpecApplyConfiguration struct {
	PodMetadata                         *EmbeddedObjectMetadataApplyConfiguration            `json:"podMetadata,omitempty"`
	Image                               *string                                              `json:"image,omitempty"`
	ImagePullPolicy                     *corev1.PullPolicy                                   `json:"imagePullPolicy,omitempty"`
	Version                             *string                                              `json:"version,omitempty"`
	Tag                                 *string                                              `json:"tag,omitempty"`
	SHA                                 *string                                              `json:"sha,omitempty"`
	BaseImage                           *string                                              `json:"baseImage,omitempty"`
	ImagePullSecrets                    []corev1.LocalObjectReference                        `json:"imagePullSecrets,omitempty"`
	Secrets                             []string                                             `json:"secrets,omitempty"`
	ConfigMaps                          []string                                             `json:"configMaps,omitempty"`
	ConfigSecret                        *string                                              `json:"configSecret,omitempty"`
	LogLevel                            *string                                              `json:"logLevel,omitempty"`
	LogFormat                           *string                                              `json:"logFormat,omitempty"`
	Replicas                            *int32                                               `json:"replicas,omitempty"`
	Retention                           *monitoringv1.GoDuration                             `json:"retention,omitempty"`
	Storage                             *StorageSpecApplyConfiguration                       `json:"storage,omitempty"`
	Volumes                             []corev1.Volume                                      `json:"volumes,omitempty"`
	VolumeMounts                        []corev1.VolumeMount                                 `json:"volumeMounts,omitempty"`
	ExternalURL                         *string                                              `json:"externalUrl,omitempty"`
	RoutePrefix                         *string                                              `json:"routePrefix,omitempty"`
	Paused                              *bool                                                `json:"paused,omitempty"`
	NodeSelector                        map[string]string                                    `json:"nodeSelector,omitempty"`
	Resources                           *corev1.ResourceRequirements                         `json:"resources,omitempty"`
	Affinity                            *corev1.Affinity                                     `json:"affinity,omitempty"`
	Tolerations                         []corev1.Toleration                                  `json:"tolerations,omitempty"`
	TopologySpreadConstraints           []corev1.TopologySpreadConstraint                    `json:"topologySpreadConstraints,omitempty"`
	SecurityContext                     *corev1.PodSecurityContext                           `json:"securityContext,omitempty"`
	ServiceAccountName                  *string                                              `json:"serviceAccountName,omitempty"`
	ListenLocal                         *bool                                                `json:"listenLocal,omitempty"`
	Containers                          []corev1.Container                                   `json:"containers,omitempty"`
	InitContainers                      []corev1.Container                                   `json:"initContainers,omitempty"`
	PriorityClassName                   *string                                              `json:"priorityClassName,omitempty"`
	AdditionalPeers                     []string                                             `json:"additionalPeers,omitempty"`
	ClusterAdvertiseAddress             *string                                              `json:"clusterAdvertiseAddress,omitempty"`
	ClusterGossipInterval               *monitoringv1.GoDuration                             `json:"clusterGossipInterval,omitempty"`
	ClusterLabel                        *string                                              `json:"clusterLabel,omitempty"`
	ClusterPushpullInterval             *monitoringv1.GoDuration                             `json:"clusterPushpullInterval,omitempty"`
	ClusterPeerTimeout                  *monitoringv1.GoDuration                             `json:"clusterPeerTimeout,omitempty"`
	PortName                            *string                                              `json:"portName,omitempty"`
	ForceEnableClusterMode              *bool                                                `json:"forceEnableClusterMode,omitempty"`
	AlertmanagerConfigSelector          *metav1.LabelSelector                                `json:"alertmanagerConfigSelector,omitempty"`
	AlertmanagerConfigMatcherStrategy   *AlertmanagerConfigMatcherStrategyApplyConfiguration `json:"alertmanagerConfigMatcherStrategy,omitempty"`
	AlertmanagerConfigNamespaceSelector *metav1.LabelSelector                                `json:"alertmanagerConfigNamespaceSelector,omitempty"`
	MinReadySeconds                     *uint32                                              `json:"minReadySeconds,omitempty"`
	HostAliases                         []HostAliasApplyConfiguration                        `json:"hostAliases,omitempty"`
	Web                                 *AlertmanagerWebSpecApplyConfiguration               `json:"web,omitempty"`
	AlertmanagerConfiguration           *AlertmanagerConfigurationApplyConfiguration         `json:"alertmanagerConfiguration,omitempty"`
	AutomountServiceAccountToken        *bool                                                `json:"automountServiceAccountToken,omitempty"`
	EnableFeatures                      []string                                             `json:"enableFeatures,omitempty"`
}

// AlertmanagerSpecApplyConfiguration constructs an declarative configuration of the AlertmanagerSpec type for use with
// apply.
func AlertmanagerSpec() *AlertmanagerSpecApplyConfiguration {
	return &AlertmanagerSpecApplyConfiguration{}
}

// WithPodMetadata sets the PodMetadata field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the PodMetadata field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithPodMetadata(value *EmbeddedObjectMetadataApplyConfiguration) *AlertmanagerSpecApplyConfiguration {
	b.PodMetadata = value
	return b
}

// WithImage sets the Image field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Image field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithImage(value string) *AlertmanagerSpecApplyConfiguration {
	b.Image = &value
	return b
}

// WithImagePullPolicy sets the ImagePullPolicy field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ImagePullPolicy field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithImagePullPolicy(value corev1.PullPolicy) *AlertmanagerSpecApplyConfiguration {
	b.ImagePullPolicy = &value
	return b
}

// WithVersion sets the Version field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Version field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithVersion(value string) *AlertmanagerSpecApplyConfiguration {
	b.Version = &value
	return b
}

// WithTag sets the Tag field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Tag field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithTag(value string) *AlertmanagerSpecApplyConfiguration {
	b.Tag = &value
	return b
}

// WithSHA sets the SHA field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the SHA field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithSHA(value string) *AlertmanagerSpecApplyConfiguration {
	b.SHA = &value
	return b
}

// WithBaseImage sets the BaseImage field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the BaseImage field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithBaseImage(value string) *AlertmanagerSpecApplyConfiguration {
	b.BaseImage = &value
	return b
}

// WithImagePullSecrets adds the given value to the ImagePullSecrets field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the ImagePullSecrets field.
func (b *AlertmanagerSpecApplyConfiguration) WithImagePullSecrets(values ...corev1.LocalObjectReference) *AlertmanagerSpecApplyConfiguration {
	for i := range values {
		b.ImagePullSecrets = append(b.ImagePullSecrets, values[i])
	}
	return b
}

// WithSecrets adds the given value to the Secrets field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Secrets field.
func (b *AlertmanagerSpecApplyConfiguration) WithSecrets(values ...string) *AlertmanagerSpecApplyConfiguration {
	for i := range values {
		b.Secrets = append(b.Secrets, values[i])
	}
	return b
}

// WithConfigMaps adds the given value to the ConfigMaps field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the ConfigMaps field.
func (b *AlertmanagerSpecApplyConfiguration) WithConfigMaps(values ...string) *AlertmanagerSpecApplyConfiguration {
	for i := range values {
		b.ConfigMaps = append(b.ConfigMaps, values[i])
	}
	return b
}

// WithConfigSecret sets the ConfigSecret field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ConfigSecret field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithConfigSecret(value string) *AlertmanagerSpecApplyConfiguration {
	b.ConfigSecret = &value
	return b
}

// WithLogLevel sets the LogLevel field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the LogLevel field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithLogLevel(value string) *AlertmanagerSpecApplyConfiguration {
	b.LogLevel = &value
	return b
}

// WithLogFormat sets the LogFormat field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the LogFormat field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithLogFormat(value string) *AlertmanagerSpecApplyConfiguration {
	b.LogFormat = &value
	return b
}

// WithReplicas sets the Replicas field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Replicas field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithReplicas(value int32) *AlertmanagerSpecApplyConfiguration {
	b.Replicas = &value
	return b
}

// WithRetention sets the Retention field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Retention field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithRetention(value monitoringv1.GoDuration) *AlertmanagerSpecApplyConfiguration {
	b.Retention = &value
	return b
}

// WithStorage sets the Storage field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Storage field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithStorage(value *StorageSpecApplyConfiguration) *AlertmanagerSpecApplyConfiguration {
	b.Storage = value
	return b
}

// WithVolumes adds the given value to the Volumes field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Volumes field.
func (b *AlertmanagerSpecApplyConfiguration) WithVolumes(values ...corev1.Volume) *AlertmanagerSpecApplyConfiguration {
	for i := range values {
		b.Volumes = append(b.Volumes, values[i])
	}
	return b
}

// WithVolumeMounts adds the given value to the VolumeMounts field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the VolumeMounts field.
func (b *AlertmanagerSpecApplyConfiguration) WithVolumeMounts(values ...corev1.VolumeMount) *AlertmanagerSpecApplyConfiguration {
	for i := range values {
		b.VolumeMounts = append(b.VolumeMounts, values[i])
	}
	return b
}

// WithExternalURL sets the ExternalURL field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ExternalURL field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithExternalURL(value string) *AlertmanagerSpecApplyConfiguration {
	b.ExternalURL = &value
	return b
}

// WithRoutePrefix sets the RoutePrefix field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the RoutePrefix field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithRoutePrefix(value string) *AlertmanagerSpecApplyConfiguration {
	b.RoutePrefix = &value
	return b
}

// WithPaused sets the Paused field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Paused field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithPaused(value bool) *AlertmanagerSpecApplyConfiguration {
	b.Paused = &value
	return b
}

// WithNodeSelector puts the entries into the NodeSelector field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, the entries provided by each call will be put on the NodeSelector field,
// overwriting an existing map entries in NodeSelector field with the same key.
func (b *AlertmanagerSpecApplyConfiguration) WithNodeSelector(entries map[string]string) *AlertmanagerSpecApplyConfiguration {
	if b.NodeSelector == nil && len(entries) > 0 {
		b.NodeSelector = make(map[string]string, len(entries))
	}
	for k, v := range entries {
		b.NodeSelector[k] = v
	}
	return b
}

// WithResources sets the Resources field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Resources field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithResources(value corev1.ResourceRequirements) *AlertmanagerSpecApplyConfiguration {
	b.Resources = &value
	return b
}

// WithAffinity sets the Affinity field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Affinity field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithAffinity(value corev1.Affinity) *AlertmanagerSpecApplyConfiguration {
	b.Affinity = &value
	return b
}

// WithTolerations adds the given value to the Tolerations field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Tolerations field.
func (b *AlertmanagerSpecApplyConfiguration) WithTolerations(values ...corev1.Toleration) *AlertmanagerSpecApplyConfiguration {
	for i := range values {
		b.Tolerations = append(b.Tolerations, values[i])
	}
	return b
}

// WithTopologySpreadConstraints adds the given value to the TopologySpreadConstraints field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the TopologySpreadConstraints field.
func (b *AlertmanagerSpecApplyConfiguration) WithTopologySpreadConstraints(values ...corev1.TopologySpreadConstraint) *AlertmanagerSpecApplyConfiguration {
	for i := range values {
		b.TopologySpreadConstraints = append(b.TopologySpreadConstraints, values[i])
	}
	return b
}

// WithSecurityContext sets the SecurityContext field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the SecurityContext field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithSecurityContext(value corev1.PodSecurityContext) *AlertmanagerSpecApplyConfiguration {
	b.SecurityContext = &value
	return b
}

// WithServiceAccountName sets the ServiceAccountName field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ServiceAccountName field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithServiceAccountName(value string) *AlertmanagerSpecApplyConfiguration {
	b.ServiceAccountName = &value
	return b
}

// WithListenLocal sets the ListenLocal field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ListenLocal field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithListenLocal(value bool) *AlertmanagerSpecApplyConfiguration {
	b.ListenLocal = &value
	return b
}

// WithContainers adds the given value to the Containers field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Containers field.
func (b *AlertmanagerSpecApplyConfiguration) WithContainers(values ...corev1.Container) *AlertmanagerSpecApplyConfiguration {
	for i := range values {
		b.Containers = append(b.Containers, values[i])
	}
	return b
}

// WithInitContainers adds the given value to the InitContainers field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the InitContainers field.
func (b *AlertmanagerSpecApplyConfiguration) WithInitContainers(values ...corev1.Container) *AlertmanagerSpecApplyConfiguration {
	for i := range values {
		b.InitContainers = append(b.InitContainers, values[i])
	}
	return b
}

// WithPriorityClassName sets the PriorityClassName field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the PriorityClassName field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithPriorityClassName(value string) *AlertmanagerSpecApplyConfiguration {
	b.PriorityClassName = &value
	return b
}

// WithAdditionalPeers adds the given value to the AdditionalPeers field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the AdditionalPeers field.
func (b *AlertmanagerSpecApplyConfiguration) WithAdditionalPeers(values ...string) *AlertmanagerSpecApplyConfiguration {
	for i := range values {
		b.AdditionalPeers = append(b.AdditionalPeers, values[i])
	}
	return b
}

// WithClusterAdvertiseAddress sets the ClusterAdvertiseAddress field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ClusterAdvertiseAddress field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithClusterAdvertiseAddress(value string) *AlertmanagerSpecApplyConfiguration {
	b.ClusterAdvertiseAddress = &value
	return b
}

// WithClusterGossipInterval sets the ClusterGossipInterval field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ClusterGossipInterval field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithClusterGossipInterval(value monitoringv1.GoDuration) *AlertmanagerSpecApplyConfiguration {
	b.ClusterGossipInterval = &value
	return b
}

// WithClusterLabel sets the ClusterLabel field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ClusterLabel field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithClusterLabel(value string) *AlertmanagerSpecApplyConfiguration {
	b.ClusterLabel = &value
	return b
}

// WithClusterPushpullInterval sets the ClusterPushpullInterval field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ClusterPushpullInterval field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithClusterPushpullInterval(value monitoringv1.GoDuration) *AlertmanagerSpecApplyConfiguration {
	b.ClusterPushpullInterval = &value
	return b
}

// WithClusterPeerTimeout sets the ClusterPeerTimeout field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ClusterPeerTimeout field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithClusterPeerTimeout(value monitoringv1.GoDuration) *AlertmanagerSpecApplyConfiguration {
	b.ClusterPeerTimeout = &value
	return b
}

// WithPortName sets the PortName field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the PortName field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithPortName(value string) *AlertmanagerSpecApplyConfiguration {
	b.PortName = &value
	return b
}

// WithForceEnableClusterMode sets the ForceEnableClusterMode field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ForceEnableClusterMode field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithForceEnableClusterMode(value bool) *AlertmanagerSpecApplyConfiguration {
	b.ForceEnableClusterMode = &value
	return b
}

// WithAlertmanagerConfigSelector sets the AlertmanagerConfigSelector field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the AlertmanagerConfigSelector field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithAlertmanagerConfigSelector(value metav1.LabelSelector) *AlertmanagerSpecApplyConfiguration {
	b.AlertmanagerConfigSelector = &value
	return b
}

// WithAlertmanagerConfigMatcherStrategy sets the AlertmanagerConfigMatcherStrategy field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the AlertmanagerConfigMatcherStrategy field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithAlertmanagerConfigMatcherStrategy(value *AlertmanagerConfigMatcherStrategyApplyConfiguration) *AlertmanagerSpecApplyConfiguration {
	b.AlertmanagerConfigMatcherStrategy = value
	return b
}

// WithAlertmanagerConfigNamespaceSelector sets the AlertmanagerConfigNamespaceSelector field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the AlertmanagerConfigNamespaceSelector field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithAlertmanagerConfigNamespaceSelector(value metav1.LabelSelector) *AlertmanagerSpecApplyConfiguration {
	b.AlertmanagerConfigNamespaceSelector = &value
	return b
}

// WithMinReadySeconds sets the MinReadySeconds field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the MinReadySeconds field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithMinReadySeconds(value uint32) *AlertmanagerSpecApplyConfiguration {
	b.MinReadySeconds = &value
	return b
}

// WithHostAliases adds the given value to the HostAliases field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the HostAliases field.
func (b *AlertmanagerSpecApplyConfiguration) WithHostAliases(values ...*HostAliasApplyConfiguration) *AlertmanagerSpecApplyConfiguration {
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithHostAliases")
		}
		b.HostAliases = append(b.HostAliases, *values[i])
	}
	return b
}

// WithWeb sets the Web field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Web field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithWeb(value *AlertmanagerWebSpecApplyConfiguration) *AlertmanagerSpecApplyConfiguration {
	b.Web = value
	return b
}

// WithAlertmanagerConfiguration sets the AlertmanagerConfiguration field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the AlertmanagerConfiguration field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithAlertmanagerConfiguration(value *AlertmanagerConfigurationApplyConfiguration) *AlertmanagerSpecApplyConfiguration {
	b.AlertmanagerConfiguration = value
	return b
}

// WithAutomountServiceAccountToken sets the AutomountServiceAccountToken field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the AutomountServiceAccountToken field is set to the value of the last call.
func (b *AlertmanagerSpecApplyConfiguration) WithAutomountServiceAccountToken(value bool) *AlertmanagerSpecApplyConfiguration {
	b.AutomountServiceAccountToken = &value
	return b
}

// WithEnableFeatures adds the given value to the EnableFeatures field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the EnableFeatures field.
func (b *AlertmanagerSpecApplyConfiguration) WithEnableFeatures(values ...string) *AlertmanagerSpecApplyConfiguration {
	for i := range values {
		b.EnableFeatures = append(b.EnableFeatures, values[i])
	}
	return b
}
