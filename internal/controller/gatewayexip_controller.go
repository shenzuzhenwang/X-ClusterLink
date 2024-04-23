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
	"os"
	"strconv"
	"time"

	kubeovnv1 "kubeovn-multivpc/api/v1"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	submarinerv1alpha1 "github.com/submariner-io/submariner-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// GatewayExIpReconciler reconciles a GatewayExIp object
type GatewayExIpReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=gatewayexips,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=gatewayexips/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=gatewayexips/finalizers,verbs=update
//+kubebuilder:rbac:groups=submariner.io,resources=servicediscoveries,verbs=get;list;watch;create;update;patch;delete

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
	r.initEnvVars()
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

// 设置环境变量，从ServiceDiscovery对象
func (r *GatewayExIpReconciler) initEnvVars() {
	cr := &submarinerv1alpha1.ServiceDiscovery{}
	if err := r.Get(context.TODO(), types.NamespacedName{Namespace: "submariner-operator"}, cr); err != nil {
		log.Log.Error(err, "problem init envvar")
		os.Exit(1)
	}

	os.Setenv("SUBMARINER_NAMESPACE", cr.Spec.Namespace)
	os.Setenv("SUBMARINER_CLUSTERID", cr.Spec.ClusterID)
	os.Setenv("SUBMARINER_DEBUG", strconv.FormatBool(cr.Spec.Debug))
	os.Setenv("SUBMARINER_GLOBALNET_ENABLED", strconv.FormatBool(cr.Spec.GlobalnetEnabled))
	os.Setenv("SUBMARINER_HALT_ON_CERT_ERROR", strconv.FormatBool(cr.Spec.HaltOnCertificateError))
	os.Setenv(broker.EnvironmentVariable("ApiServer"), cr.Spec.BrokerK8sApiServer)
	os.Setenv(broker.EnvironmentVariable("ApiServerToken"), cr.Spec.BrokerK8sApiServerToken)
	os.Setenv(broker.EnvironmentVariable("RemoteNamespace"), cr.Spec.BrokerK8sRemoteNamespace)
	os.Setenv(broker.EnvironmentVariable("CA"), cr.Spec.BrokerK8sCA)
	os.Setenv(broker.EnvironmentVariable("Insecure"), strconv.FormatBool(cr.Spec.BrokerK8sInsecure))
	os.Setenv(broker.EnvironmentVariable("Secret"), cr.Spec.BrokerK8sSecret)
}

type AgentSpecification struct {
	ClusterID        string
	Namespace        string
	Verbosity        int
	GlobalnetEnabled bool `split_words:"true"`
	Uninstall        bool
	HaltOnCertError  bool `split_words:"true"`
	Debug            bool
}
type Controller struct {
	clusterID string
	syncer    *broker.Syncer
	// serviceImportAggregator *ServiceImportAggregator
	// serviceExportClient     *ServiceExportClient
	// serviceSyncer syncer.Interface
	// conflictCheckWorkQueue workqueue.Interface
}

var BrokerResyncPeriod = time.Minute * 2

func New(spec *AgentSpecification, syncerConfig broker.SyncerConfig) (*Controller, error) {
	c := &Controller{
		clusterID: spec.ClusterID,
		// serviceExportClient:    serviceExportClient,
		// serviceSyncer:          serviceSyncer,
		// conflictCheckWorkQueue: workqueue.New("ConflictChecker"),
	}
	syncerConfig.LocalNamespace = metav1.NamespaceAll
	syncerConfig.LocalClusterID = spec.ClusterID
	syncerConfig.ResourceConfigs = []broker.ResourceConfig{
		{
			LocalSourceNamespace: metav1.NamespaceAll,
			// LocalSourceLabelSelector: k8slabels.SelectorFromSet(map[string]string{
			// 	discovery.LabelManagedBy: constants.LabelValueManagedBy,
			// }).String(),
			LocalResourceType:          &kubeovnv1.GatewayExIp{},
			TransformLocalToBroker:     c.onLocalGatewayExIp,
			OnSuccessfulSyncToBroker:   c.onLocalGatewayExIpSynced,
			BrokerResourceType:         &kubeovnv1.GatewayExIp{},
			TransformBrokerToLocal:     c.onRemoteGatewayExIp,
			OnSuccessfulSyncFromBroker: c.onRemoteGatewayExIpSynced,
			BrokerResyncPeriod:         BrokerResyncPeriod,
		},
	}

	var err error
	// broker 接口，对于syncerConfig.ResourceConfigs中的所有资源进行双向同步
	c.syncer, err = broker.NewSyncer(syncerConfig)
	if err != nil {
		return nil, errors.Wrap(err, "error creating GatewayExIp syncer")
	}

	// c.serviceImportAggregator = newServiceImportAggregator(c.syncer.GetBrokerClient(), c.syncer.GetBrokerNamespace(),
	// 	spec.ClusterID, syncerConfig.Scheme)

	return c, nil
}

func (c *Controller) Start(stopCh <-chan struct{}) error {
	if err := c.syncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting syncer")
	}
	// c.conflictCheckWorkQueue.Run(stopCh, c.checkForConflicts)

	// go func() {
	// 	<-stopCh
	// 	c.conflictCheckWorkQueue.ShutDown()
	// }()
	log.Log.Info("Agent controller started")

	return nil
}

func (c *Controller) onLocalGatewayExIp(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	return obj, false
}

func (c *Controller) onLocalGatewayExIpSynced(obj runtime.Object, op syncer.Operation) bool {
	return false
	// return err != nil
}
func (c *Controller) onRemoteGatewayExIp(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	return obj, false
}
func (c *Controller) onRemoteGatewayExIpSynced(obj runtime.Object, op syncer.Operation) bool {
	return false
	// return err != nil
}
