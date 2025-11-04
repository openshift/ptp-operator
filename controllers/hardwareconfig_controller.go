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
	"fmt"

	"github.com/go-logr/logr"
	ptpv1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v1"
	ptpv2alpha1 "github.com/k8snetworkplumbingwg/ptp-operator/api/v2alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HardwareConfigReconciler reconciles a HardwareConfig object
type HardwareConfigReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=ptp.openshift.io,resources=hardwareconfigs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ptp.openshift.io,resources=hardwareconfigs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ptp.openshift.io,resources=hardwareconfigs/finalizers,verbs=update
//+kubebuilder:rbac:groups=ptp.openshift.io,resources=ptpconfigs,verbs=get;list;watch

func (r *HardwareConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (reconcile.Result, error) {
	reqLogger := r.Log.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name)
	reqLogger.Info("Reconciling HardwareConfig")

	// Get the HardwareConfig instance
	hardwareConfig := &ptpv2alpha1.HardwareConfig{}
	err := r.Get(ctx, req.NamespacedName, hardwareConfig)
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// Get all PTP configs
	ptpConfigList := &ptpv1.PtpConfigList{}
	err = r.List(ctx, ptpConfigList, &client.ListOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if err = r.syncHardwareConfigStatus(ctx, hardwareConfig, ptpConfigList); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

// syncHardwareConfigStatus updates the HardwareConfig status based on PTP config status
func (r *HardwareConfigReconciler) syncHardwareConfigStatus(ctx context.Context, hardwareConfig *ptpv2alpha1.HardwareConfig, ptpConfigList *ptpv1.PtpConfigList) error {
	reqLogger := r.Log.WithValues("HardwareConfig", hardwareConfig.Name)

	// If RelatedPtpProfileName is not set, clear the status
	if hardwareConfig.Spec.RelatedPtpProfileName == "" {
		if len(hardwareConfig.Status.MatchedNodes) > 0 {
			hardwareConfig.Status.MatchedNodes = nil
			err := r.Status().Update(ctx, hardwareConfig)
			if err != nil {
				return fmt.Errorf("failed to update hardware config status: %v", err)
			}
		}
		reqLogger.Info("RelatedPtpProfileName not set, clearing matched nodes")
		return nil
	}

	// Find nodes that have been recommended the related PTP profile
	var matchedNodes []ptpv2alpha1.MatchedNode
	for _, ptpConfig := range ptpConfigList.Items {
		for _, match := range ptpConfig.Status.MatchList {
			if match.Profile != nil && *match.Profile == hardwareConfig.Spec.RelatedPtpProfileName {
				if match.NodeName != nil {
					matchedNodes = append(matchedNodes, ptpv2alpha1.MatchedNode{
						NodeName:   *match.NodeName,
						PtpProfile: hardwareConfig.Spec.RelatedPtpProfileName,
					})
					reqLogger.Info("Found matching node", "node", *match.NodeName, "ptpProfile", hardwareConfig.Spec.RelatedPtpProfileName)
				}
			}
		}
	}

	// Update status if it has changed
	statusChanged := len(matchedNodes) != len(hardwareConfig.Status.MatchedNodes)
	if !statusChanged && len(matchedNodes) > 0 {
		// Check if the matched nodes are the same
		nodeMap := make(map[string]bool)
		for _, node := range hardwareConfig.Status.MatchedNodes {
			nodeMap[node.NodeName] = true
		}
		for _, node := range matchedNodes {
			if !nodeMap[node.NodeName] {
				statusChanged = true
				break
			}
		}
	}

	if statusChanged {
		hardwareConfig.Status.MatchedNodes = matchedNodes
		err := r.Status().Update(ctx, hardwareConfig)
		if err != nil {
			return fmt.Errorf("failed to update hardware config status: %v", err)
		}
		reqLogger.Info("Updated hardware config status", "matchedNodes", len(matchedNodes))
	}

	return nil
}

// HardwareConfigPtpConfigHandler handles PTP config changes and triggers HardwareConfig reconciliation
type HardwareConfigPtpConfigHandler struct {
	Client client.Client
	Log    logr.Logger
}

// Create handles PTP config creation events
func (h *HardwareConfigPtpConfigHandler) Create(ctx context.Context, evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	h.enqueueHardwareConfigs(ctx, q, "create")
}

// Update handles PTP config update events
func (h *HardwareConfigPtpConfigHandler) Update(ctx context.Context, evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	h.enqueueHardwareConfigs(ctx, q, "update")
}

// Delete handles PTP config deletion events
func (h *HardwareConfigPtpConfigHandler) Delete(ctx context.Context, evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	h.enqueueHardwareConfigs(ctx, q, "delete")
}

// Generic handles generic PTP config events
func (h *HardwareConfigPtpConfigHandler) Generic(ctx context.Context, evt event.GenericEvent, q workqueue.RateLimitingInterface) {
	h.enqueueHardwareConfigs(ctx, q, "generic")
}

// enqueueHardwareConfigs enqueues reconcile requests for all HardwareConfig resources
func (h *HardwareConfigPtpConfigHandler) enqueueHardwareConfigs(ctx context.Context, q workqueue.RateLimitingInterface, eventType string) {
	h.Log.Info("PTP config changed, triggering HardwareConfig reconciliation", "eventType", eventType)

	// Get all HardwareConfig resources
	hardwareConfigList := &ptpv2alpha1.HardwareConfigList{}
	err := h.Client.List(ctx, hardwareConfigList)
	if err != nil {
		h.Log.Error(err, "failed to list HardwareConfig resources")
		return
	}

	// Create reconcile requests for all HardwareConfig resources
	for _, hardwareConfig := range hardwareConfigList.Items {
		q.Add(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      hardwareConfig.Name,
				Namespace: hardwareConfig.Namespace,
			},
		})
	}

	h.Log.Info("Enqueued HardwareConfig reconciliation requests", "count", len(hardwareConfigList.Items), "eventType", eventType)
}

func (r *HardwareConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ptpv2alpha1.HardwareConfig{}).
		Watches(&ptpv1.PtpConfig{}, &HardwareConfigPtpConfigHandler{Client: r.Client, Log: r.Log}).
		Complete(r)
}
