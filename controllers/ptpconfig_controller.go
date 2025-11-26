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
	"github.com/k8snetworkplumbingwg/ptp-operator/pkg/names"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

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
		For(&ptpv1.PtpConfig{}).
		Complete(r)
}
