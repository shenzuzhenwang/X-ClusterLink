/*
Copyright 2024.

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

package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kubeovnv1 "kubeovn-multivpc/api/v1"
)

// GatewayExIpReconciler reconciles a GatewayExIp object
type GatewayExIpReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=gatewayexips,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=gatewayexips/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=gatewayexips/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the GatewayExIp object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.0/pkg/reconcile
func (r *GatewayExIpReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	// TODO(user): your logic here
	// Fetch the GatewayExIp instance
	var gatewayExIp kubeovnv1.GatewayExIp
	if err := r.Get(ctx, req.NamespacedName, &gatewayExIp); err != nil {
		log.Log.Error(err, "unable to fetch GatewayExIp")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !gatewayExIp.ObjectMeta.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	gatewayExIp.Status.ExternalIP = gatewayExIp.Spec.ExternalIP

	// Update the GatewayExIp instance
	if err := r.Update(ctx, &gatewayExIp); err != nil {
		log.Log.Error(err, "unable to update GatewayExIp")
		return ctrl.Result{}, err
	}

	log.Log.Info("Updated GatewayExIp successfully", "ExternalIP", gatewayExIp.Status.ExternalIP)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GatewayExIpReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kubeovnv1.GatewayExIp{}).
		Complete(r)
}
