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

	"github.com/go-logr/logr"
	"github.com/golang/glog"
	ptpv1 "github.com/openshift/ptp-operator/api/v1"
	"github.com/openshift/ptp-operator/pkg/names"
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

func (r *PtpConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ptpv1.PtpConfig{}).
		Complete(r)
}
