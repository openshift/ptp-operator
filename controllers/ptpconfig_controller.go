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

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/go-logr/logr"
	"github.com/golang/glog"
	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/apply"
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/names"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	uns "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const PTP_SEC_FOLDER = "/etc/ptp-secret-mount/"
const TLV_AUTH_SUFFIX = "-tlv-auth"

// PtpConfigReconciler reconciles a PtpConfig object
type PtpConfigReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=ptp.openshift.io,resources=ptpconfigs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ptp.openshift.io,resources=ptpconfigs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ptp.openshift.io,resources=ptpconfigs/finalizers,verbs=update
//+kubebuilder:rbac:groups=config.openshift.io,resources=infrastructures,verbs=get;list;watch

func (r *PtpConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (reconcile.Result, error) {
	reqLogger := r.Log.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name)
	reqLogger.Info("Reconciling PtpConfig")

	instances := &ptpv1.PtpConfigList{}
	err := r.List(ctx, instances, &client.ListOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	nodeList := &corev1.NodeList{}
	err = r.List(ctx, nodeList, &client.ListOptions{})
	if err != nil {
		glog.Errorf("failed to list nodes")
		return reconcile.Result{}, err
	}

	if err = r.syncPtpConfig(ctx, instances, nodeList); err != nil {
		return reconcile.Result{}, err
	}

	// After syncing ConfigMap, update DaemonSet with secret mounts
	if err = r.syncLinuxptpDaemonSecrets(ctx, instances); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

// syncPtpConfig synchronizes PtpConfig CR
func (r *PtpConfigReconciler) syncPtpConfig(ctx context.Context, ptpConfigList *ptpv1.PtpConfigList, nodeList *corev1.NodeList) error {
	var err error

	nodePtpConfigMap := &corev1.ConfigMap{}
	nodePtpConfigMap.Name = names.DefaultPTPConfigMapName
	nodePtpConfigMap.Namespace = names.Namespace
	nodePtpConfigMap.Data = make(map[string]string)

	// Also update PTP config status with match list
	for _, ptpConfig := range ptpConfigList.Items {
		var matchList []ptpv1.NodeMatchList

		for _, node := range nodeList.Items {
			nodePtpProfiles, err := getRecommendNodePtpProfilesForConfig(&ptpConfig, node)
			if err != nil {
				glog.Errorf("failed to get recommended profiles for node %s: %v", node.Name, err)
				continue
			}

			// If this PTP config recommends profiles for this node, add to match list
			if len(nodePtpProfiles) > 0 {
				for _, profile := range nodePtpProfiles {
					matchList = append(matchList, ptpv1.NodeMatchList{
						NodeName: &node.Name,
						Profile:  profile.Name,
					})
				}
			}
		}

		// Update PTP config status if it has changed
		if !reflect.DeepEqual(ptpConfig.Status.MatchList, matchList) {
			ptpConfig.Status.MatchList = matchList
			err = r.Status().Update(ctx, &ptpConfig)
			if err != nil {
				glog.Errorf("failed to update PTP config status for %s: %v", ptpConfig.Name, err)
			} else {
				glog.Infof("updated PTP config status for %s with %d matches", ptpConfig.Name, len(matchList))
			}
		}
	}

	for _, node := range nodeList.Items {
		nodePtpProfiles, err := getRecommendNodePtpProfiles(ptpConfigList, node)
		if err != nil {
			return fmt.Errorf("failed to get recommended node PtpConfig: %v", err)
		}

		data, err := json.Marshal(nodePtpProfiles)
		if err != nil {
			return fmt.Errorf("failed to Marshal nodePtpProfiles: %v", err)
		}
		nodePtpConfigMap.Data[node.Name] = string(data)
	}

	cm := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{
		Namespace: names.Namespace, Name: names.DefaultPTPConfigMapName}, cm)
	if err != nil {
		return fmt.Errorf("failed to get ptp config map: %v", err)
	} else {
		glog.Infof("ptp config map already exists, updating")
		cm.Data = nodePtpConfigMap.Data
		err = r.Update(ctx, cm)
		if err != nil {
			return fmt.Errorf("failed to update ptp config map: %v", err)
		}
	}
	return nil
}

// getRecommendNodePtpProfilesForConfig returns recommended PTP profiles for a node from a single PTP config
func getRecommendNodePtpProfilesForConfig(ptpConfig *ptpv1.PtpConfig, node corev1.Node) ([]ptpv1.PtpProfile, error) {
	profilesNames := getRecommendProfilesNamesForConfig(ptpConfig, node)
	if len(profilesNames) == 0 {
		return []ptpv1.PtpProfile{}, nil
	}

	profiles := []ptpv1.PtpProfile{}
	if ptpConfig.Spec.Profile != nil {
		for _, profile := range ptpConfig.Spec.Profile {
			if _, exist := profilesNames[*profile.Name]; exist {
				profiles = append(profiles, profile)
			}
		}
	}

	return profiles, nil
}

// getRecommendProfilesNamesForConfig returns recommended profile names for a node from a single PTP config
func getRecommendProfilesNamesForConfig(ptpConfig *ptpv1.PtpConfig, node corev1.Node) map[string]interface{} {
	var (
		allRecommend []ptpv1.PtpRecommend
	)

	// Get recommend section from this PTP config
	if ptpConfig.Spec.Recommend != nil {
		allRecommend = append(allRecommend, ptpConfig.Spec.Recommend...)
	}

	// Sort by priority (lower numbers have higher priority)
	sort.Slice(allRecommend, func(i, j int) bool {
		if allRecommend[i].Priority != nil && allRecommend[j].Priority != nil {
			return *allRecommend[i].Priority < *allRecommend[j].Priority
		}
		return allRecommend[i].Priority != nil
	})

	// Find matching profiles
	profilesNames := make(map[string]interface{})
	foundPolicy := false
	priority := int64(-1)

	// Loop through recommendations from high priority (0) to low (*)
	for _, r := range allRecommend {
		// Ignore if profile not defined in recommend
		if r.Profile == nil {
			continue
		}

		// Ignore if match section is empty
		if len(r.Match) == 0 {
			continue
		}

		// Check if the policy matches the node
		switch {
		case !ptpNodeMatches(&node, r.Match):
			continue
		case !foundPolicy:
			profilesNames[*r.Profile] = struct{}{}
			priority = *r.Priority
			foundPolicy = true
		case *r.Priority == priority:
			profilesNames[*r.Profile] = struct{}{}
		default:

		}
	}

	return profilesNames
}

// ptpNodeMatches checks if a node matches the given match rules for PTP config
func ptpNodeMatches(node *corev1.Node, matchRuleList []ptpv1.MatchRule) bool {
	// Loop over Match list
	for _, m := range matchRuleList {
		// NodeName has higher priority than nodeLabel
		// Return immediately if nodeName matches
		if m.NodeName != nil && *m.NodeName == node.Name {
			return true
		}

		// Return immediately when label matches
		for k := range node.Labels {
			if m.NodeLabel != nil && *m.NodeLabel == k {
				return true
			}
		}
	}

	return false
}

func (r *PtpConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ptpv1.PtpConfig{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
			return object.GetNamespace() == names.Namespace
		}))).
		Complete(r)
}

// secretMount represents a secret and sa_file pair for a profile
type secretMount struct {
	secretName string
	saFilePath string
	secretKey  string // The actual key name in the secret
}

// syncLinuxptpDaemonSecrets updates the linuxptp-daemon DaemonSet with secret volume mounts
func (r *PtpConfigReconciler) syncLinuxptpDaemonSecrets(ctx context.Context, ptpConfigList *ptpv1.PtpConfigList) error {
	// 1. Collect {secretName, sa_file} pairs from each profile
	var mounts []secretMount

	glog.Info("Scanning PtpConfigs for {secretName, sa_file} pairs...")
	for _, cfg := range ptpConfigList.Items {
		for _, profile := range cfg.Spec.Profile {
			profileName := "unknown"
			if profile.Name != nil {
				profileName = *profile.Name
			}

			if profile.Ptp4lConf == nil {
				continue
			}
			// Parse ptp4lConf to get sa_file
			conf := &ptpv1.Ptp4lConf{}
			if err := conf.PopulatePtp4lConf(profile.Ptp4lConf, profile.Ptp4lOpts); err != nil {
				glog.Warningf("Failed to parse ptp4lConf for profile %s: %v", profileName, err)
				continue
			}

			// Get sa_file from [global] section
			saFilePath := conf.GetOption("[global]", "sa_file")
			if saFilePath == "" {
				continue
			}
			secretName := ptpv1.GetSecretNameFromSaFilePath(saFilePath)
			glog.Infof("Found {secret, sa_file} pair in PtpConfig %s, profile %s: {%s, %s}",
				cfg.Name, profileName, secretName, saFilePath)

			// extract the secret key from the sa_file path
			secretKey := ptpv1.GetSecretKeyFromSaFilePath(saFilePath)

			mounts = append(mounts, secretMount{
				secretName: secretName,
				saFilePath: saFilePath,
				secretKey:  secretKey,
			})
		}
	}

	uniqueSecrets := make(map[string]secretMount)
	for _, mount := range mounts {
		if _, exists := uniqueSecrets[mount.secretName]; !exists {
			uniqueSecrets[mount.secretName] = mount
			glog.Infof("Adding unique secret '%s'", mount.secretName)
		} else {
			glog.Infof("Skipping duplicate secret '%s'", mount.secretName)
		}
	}

	glog.Infof("Found %d unique secret(s) to mount", len(uniqueSecrets))

	// 2. Get the linuxptp-daemon DaemonSet
	daemonSet := &appsv1.DaemonSet{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: names.Namespace,
		Name:      "linuxptp-daemon",
	}, daemonSet)
	if err != nil {
		if errors.IsNotFound(err) {
			glog.Info("linuxptp-daemon DaemonSet not found yet - will retry in 10 seconds")
			return fmt.Errorf("DaemonSet not found, will retry")
		}
		return fmt.Errorf("failed to get linuxptp-daemon DaemonSet: %v", err)
	}
	// 3. Remove all old security volumes first
	removeSecurityVolumesFromDaemonSet(daemonSet)

	// 4. Add volumes - iterate over DEDUPLICATED secrets
	for _, mount := range uniqueSecrets {
		glog.Infof("Injecting security volume for secret '%s'", mount.secretName)
		injectPtpSecurityVolume(daemonSet, mount.secretName)
	}
	// 5. Convert to Unstructured and apply with merge (like PtpOperatorConfig does)
	scheme := kscheme.Scheme
	updated := &uns.Unstructured{}
	if err := scheme.Convert(daemonSet, updated, nil); err != nil {
		return fmt.Errorf("failed to convert DaemonSet to Unstructured: %v", err)
	}

	// 6. Use apply.ApplyObject which will call MergeObjectForUpdate
	// Set context to indicate this update is from PtpConfig controller
	// This allows the merge logic to use security volumes from updated (even if empty)
	ctxWithSource := context.WithValue(ctx, apply.ControllerSourceKey, apply.SourcePtpConfig)
	if err := apply.ApplyObject(ctxWithSource, r.Client, updated); err != nil {
		return fmt.Errorf("failed to apply DaemonSet: %v", err)
	}

	glog.Info("Successfully updated linuxptp-daemon DaemonSet with security mounts")
	return nil
}

// removeSecurityVolumesFromDaemonSet removes all PTP security-related volumes and mounts from DaemonSet
// Security volumes are identified by: ending with "-tlv-auth" suffix
func removeSecurityVolumesFromDaemonSet(ds *appsv1.DaemonSet) {
	// Remove security volumes (both regular and projected)
	var filteredVolumes []corev1.Volume
	for _, vol := range ds.Spec.Template.Spec.Volumes {
		// Check if volume ends with "-tlv-auth" (9 characters)
		if len(vol.Name) >= 9 && vol.Name[len(vol.Name)-9:] == TLV_AUTH_SUFFIX {
			glog.Infof("Removing old security volume: %s", vol.Name)
			continue
		}
		filteredVolumes = append(filteredVolumes, vol)
	}
	ds.Spec.Template.Spec.Volumes = filteredVolumes

	// Remove security volume mounts from linuxptp-daemon-container
	for i := range ds.Spec.Template.Spec.Containers {
		if ds.Spec.Template.Spec.Containers[i].Name == "linuxptp-daemon-container" {
			var filteredMounts []corev1.VolumeMount
			for _, mount := range ds.Spec.Template.Spec.Containers[i].VolumeMounts {
				// Skip mounts ending with "-tlv-auth" (9 characters)
				if len(mount.Name) >= 9 && mount.Name[len(mount.Name)-9:] == TLV_AUTH_SUFFIX {
					glog.Infof("Removing old security mount: %s", mount.Name)
					continue
				}
				filteredMounts = append(filteredMounts, mount)
			}
			ds.Spec.Template.Spec.Containers[i].VolumeMounts = filteredMounts
			break
		}
	}
}

// injectPtpSecurityVolume adds a single secret volume and mount to the DaemonSet
func injectPtpSecurityVolume(ds *appsv1.DaemonSet, secretName string) {
	volumeName := secretName + TLV_AUTH_SUFFIX
	mountDir := PTP_SEC_FOLDER + secretName

	// Add volume - mount ENTIRE secret (all keys become files)
	ds.Spec.Template.Spec.Volumes = append(ds.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: secretName,
			},
		},
	})

	// Add volume mount
	for i := range ds.Spec.Template.Spec.Containers {
		if ds.Spec.Template.Spec.Containers[i].Name == "linuxptp-daemon-container" {
			ds.Spec.Template.Spec.Containers[i].VolumeMounts = append(
				ds.Spec.Template.Spec.Containers[i].VolumeMounts,
				corev1.VolumeMount{
					Name:      volumeName,
					MountPath: mountDir,
					ReadOnly:  true,
				},
			)
			break
		}
	}

	glog.Infof("Mounted secret '%s' to %s", secretName, mountDir)
}
