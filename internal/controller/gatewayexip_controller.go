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
	"fmt"
	"github.com/kelseyhightower/envconfig"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	kubeovnv1 "kubeovn-multivpc/api/v1"
	"os"
	"strconv"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	submarinerv1alpha1 "github.com/submariner-io/submariner-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=gatewayexips,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=gatewayexips/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=gatewayexips/finalizers,verbs=update
//+kubebuilder:rbac:groups=submariner.io,resources=servicediscoveries,verbs=get;list;watch;create;update;patch;delete

// GatewayExIpReconciler reconciles a GatewayExIp object
type GatewayExIpReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

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

	// Update the GatewayExIp instance
	if err := r.Update(ctx, &gatewayExIp); err != nil {
		log.Log.Error(err, "unable to update GatewayExIp")
		return ctrl.Result{}, err
	}

	log.Log.Info("Updated GatewayExIp successfully", "ExternalIP", gatewayExIp.Spec.ExternalIP)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GatewayExIpReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kubeovnv1.GatewayExIp{}).
		Complete(r)
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

var BrokerResyncPeriod = time.Minute * 2

type Controller struct {
	clusterID string
	syncer    *broker.Syncer
}

// 设置环境变量，从ServiceDiscovery对象
func InitEnvVars(syncerConf broker.SyncerConfig) error {
	cr := &submarinerv1alpha1.ServiceDiscovery{}
	obj, err := syncerConf.LocalClient.Resource(schema.GroupVersionResource{
		Group:    "submariner.io",
		Version:  "v1alpha1",
		Resource: "servicediscoveries",
	}).Namespace("submariner-operator").Get(context.TODO(), "service-discovery", metav1.GetOptions{})
	if err != nil {
		return err
	}
	utilruntime.Must(syncerConf.Scheme.Convert(obj, cr, nil))
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
	return nil
}

func New(spec *AgentSpecification, syncerConfig broker.SyncerConfig) *Controller {
	c := &Controller{
		clusterID: spec.ClusterID,
	}
	err := InitEnvVars(syncerConfig)
	if err != nil {
		return nil
	}

	// 配置 Syncer
	syncerConfig.LocalNamespace = metav1.NamespaceAll
	syncerConfig.LocalClusterID = spec.ClusterID
	syncerConfig.ResourceConfigs = []broker.ResourceConfig{
		{
			LocalSourceNamespace:       metav1.NamespaceAll,
			LocalResourceType:          &kubeovnv1.GatewayExIp{},
			TransformLocalToBroker:     c.onLocalGatewayExIp,
			OnSuccessfulSyncToBroker:   c.onLocalGatewayExIpSynced,
			BrokerResourceType:         &kubeovnv1.GatewayExIp{},
			TransformBrokerToLocal:     c.onRemoteGatewayExIp,
			OnSuccessfulSyncFromBroker: c.onRemoteGatewayExIpSynced,
			BrokerResyncPeriod:         BrokerResyncPeriod,
		},
	}
	// 创建broker Syncer, 对于syncerConfig.ResourceConfigs中的所有资源进行双向同步
	brokerSpec, err := getBrokerSpecification()
	if err != nil {
		klog.Info(err)
	}
	syncerConfig.BrokerRestConfig = &rest.Config{
		Host:            fmt.Sprintf("https://%s", brokerSpec.APIServer),
		TLSClientConfig: rest.TLSClientConfig{Insecure: brokerSpec.Insecure},
		BearerToken:     brokerSpec.APIServerToken,
	}
	flag, err := IsAuthorizedFor(syncerConfig.BrokerRestConfig, schema.GroupVersionResource{
		Group:    "kubeovn.ustc.io",
		Version:  "v1",
		Resource: "gatewayexips",
	}, "submariner-k8s-broker")
	klog.Info(flag)
	klog.Info(err)
	c.syncer, err = broker.NewSyncer(syncerConfig)
	if err != nil {
		klog.Info(err)
		return nil
	}
	return c
}

type brokerSpecification struct {
	APIServer       string
	APIServerToken  string
	RemoteNamespace string
	Insecure        bool `default:"false"`
	Ca              string
	Secret          string
}

const brokerConfigPrefix = "broker_k8s"

func IsAuthorizedFor(restConfig *rest.Config, gvr schema.GroupVersionResource, namespace string) (bool, error) {
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return false, errors.Wrap(err, "error creating dynamic client")
	}
	_, err = dynamicClient.Resource(gvr).Namespace(namespace).Get(context.TODO(), "any", metav1.GetOptions{})
	if err != nil {
		klog.Info(err)
	}
	return true, err
}

func getBrokerSpecification() (*brokerSpecification, error) {
	brokerSpec := brokerSpecification{}

	err := envconfig.Process(brokerConfigPrefix, &brokerSpec)
	if err != nil {
		return nil, errors.Wrap(err, "error processing env configuration")
	}

	return &brokerSpec, nil
}

func (c *Controller) Start(ctx context.Context) error {
	stopCh := ctx.Done()
	if err := c.syncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting syncer")
	}
	log.Log.Info("Agent controller started")
	return nil
}

// local 同步到 Broker 前执行的操作
func (c *Controller) onLocalGatewayExIp(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	return obj, false
}

// local 成功同步到 Broker 后执行的操作
func (c *Controller) onLocalGatewayExIpSynced(obj runtime.Object, op syncer.Operation) bool {
	return false
}

// Broker 同步到 local 前执行的操作
func (c *Controller) onRemoteGatewayExIp(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	return obj, false
}

// Broker 成功同步到 local 后执行的操作
func (c *Controller) onRemoteGatewayExIpSynced(obj runtime.Object, op syncer.Operation) bool {
	return false
}
