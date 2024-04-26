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
	kubeovnv1 "kubeovn-multivpc/api/v1"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	submarinerv1alpha1 "github.com/submariner-io/submariner-operator/api/v1alpha1"
	Submariner "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=gatewayexips,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=gatewayexips/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=gatewayexips/finalizers,verbs=update
//+kubebuilder:rbac:groups=submariner.io,resources=servicediscoveries,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=submariner.io,resources=clusters,verbs=get;list;watch;create;update;patch;delete

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
	scheme    *runtime.Scheme
}

// 设置环境变量，从 ServiceDiscovery 对象
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

func RefreshGatewayExIp(syncerConfig broker.SyncerConfig, clusterID string) error {
	// 遍历所有 Vpc-Gateway 获取 ip 生成 GatewayExIp
	clientSet, err := kubernetes.NewForConfig(syncerConfig.LocalRestConfig)
	if err != nil {
		log.Log.Error(err, "Error create client")
		return err
	}
	labelSelector := labels.Set{
		"ovn.kubernetes.io/vpc-nat-gw": "true",
	}
	options := metav1.ListOptions{
		LabelSelector: labelSelector.String(),
	}
	podList, err := clientSet.CoreV1().Pods("kube-system").List(context.Background(), options)
	if err != nil {
		log.Log.Error(err, "Error get pods")
		return err
	}
	// 找到本集群的GlobalNetCIDR
	submarinerCluster := &Submariner.Cluster{}
	clusterObj, err := syncerConfig.LocalClient.Resource(schema.GroupVersionResource{
		Group:    "submariner.io",
		Version:  "v1",
		Resource: "clusters",
	}).Namespace("submariner-operator").Get(context.Background(), clusterID, metav1.GetOptions{})
	if err != nil {
		log.Log.Error(err, "Error get gw pod")
		return err
	}
	utilruntime.Must(syncerConfig.Scheme.Convert(clusterObj, submarinerCluster, nil))
	for _, pod := range podList.Items {
		gatewayExIp := &kubeovnv1.GatewayExIp{}
		gatewayExIp.Spec.ExternalIP = pod.ObjectMeta.GetObjectMeta().GetAnnotations()["ovn-vpc-external-network.kube-system.kubernetes.io/ip_address"]
		gatewayExIp.Name = pod.Name[11:len(pod.Name)-2] + "-" + clusterID
		gatewayExIp.Namespace = pod.Namespace
		gatewayExIp.Spec.GlobalNetCIDR = submarinerCluster.Spec.GlobalCIDR[0]
		_, err = syncerConfig.LocalClient.Resource(schema.GroupVersionResource{
			Group:    "kubeovn.ustc.io",
			Version:  "v1",
			Resource: "gatewayexips",
		}).Get(context.Background(), gatewayExIp.Name, metav1.GetOptions{})
		obj := &unstructured.Unstructured{}
		utilruntime.Must(syncerConfig.Scheme.Convert(gatewayExIp, obj, nil))
		if err == nil {
			// GatewatExIp 存在, 进行更新
			_, err = syncerConfig.LocalClient.Resource(schema.GroupVersionResource{
				Group:    "kubeovn.ustc.io",
				Version:  "v1",
				Resource: "gatewayexips",
			}).Namespace(gatewayExIp.Namespace).Update(context.TODO(), obj, metav1.UpdateOptions{})
			if err != nil {
				log.Log.Error(err, "Error update gatewayexips")
				return err
			}
		} else {
			// GatewatExIp 不存在, 进行创建
			_, err = syncerConfig.LocalClient.Resource(schema.GroupVersionResource{
				Group:    "kubeovn.ustc.io",
				Version:  "v1",
				Resource: "gatewayexips",
			}).Namespace(gatewayExIp.Namespace).Create(context.TODO(), obj, metav1.CreateOptions{})
			if err != nil {
				log.Log.Error(err, "Error create gatewayexips")
				return err
			}
		}
	}
	return nil
}

func New(spec *AgentSpecification, syncerConfig broker.SyncerConfig) *Controller {
	c := &Controller{
		clusterID: spec.ClusterID,
		scheme:    syncerConfig.Scheme,
	}
	err := RefreshGatewayExIp(syncerConfig, c.clusterID)
	if err != nil {
		log.Log.Error(err, "error RefreshGatewayExIp")
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
	c.syncer, err = broker.NewSyncer(syncerConfig)
	if err != nil {
		log.Log.Error(err, "error creating GatewayExIp syncer")
		return nil
	}
	return c
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
	gatewayExIp := obj.(*kubeovnv1.GatewayExIp)
	if gatewayExIp.Namespace == "submariner-k8s-broker" {
		return nil, false
	}
	return gatewayExIp, false
}

// local 成功同步到 Broker 后执行的操作
func (c *Controller) onLocalGatewayExIpSynced(obj runtime.Object, op syncer.Operation) bool {
	return false
}

// Broker 同步到 local 前执行的操作
func (c *Controller) onRemoteGatewayExIp(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	gatewayExIp := obj.(*kubeovnv1.GatewayExIp)
	gatewayExIp.Namespace = gatewayExIp.GetObjectMeta().GetLabels()["submariner-io/originatingNamespace"]
	return gatewayExIp, false
}

// Broker 成功同步到 local 后执行的操作
func (c *Controller) onRemoteGatewayExIpSynced(obj runtime.Object, op syncer.Operation) bool {
	gatewayExIp := obj.(*kubeovnv1.GatewayExIp)
	fieldSelector := fields.Selector(
		fields.ParseSelectorOrDie(
			fmt.Sprintf("spec.ClusterId=%s,spec.GatewayId=%s", strings.TrimSuffix(gatewayExIp.Name, fmt.Sprintf("-%s", c.clusterID)), c.clusterID),
		),
	)
	options := metav1.ListOptions{
		FieldSelector: fieldSelector.String(),
	}
	vpcNatTunnelList := &kubeovnv1.VpcNatTunnelList{}
	objList, err := c.syncer.GetLocalClient().Resource(schema.GroupVersionResource{
		Group:    "kubeovn.ustc.io",
		Version:  "v1",
		Resource: "vpcnattunnels",
	}).List(context.Background(), options)
	if err != nil {
		log.Log.Error(err, "Error get vpctunnels")
		return false
	}
	utilruntime.Must(c.scheme.Convert(objList, vpcNatTunnelList, nil))
	for _, vpcNatTunnel := range vpcNatTunnelList.Items {
		vpcNatTunnel.Status.RemoteIP = gatewayExIp.Spec.ExternalIP
		data := &unstructured.Unstructured{}
		utilruntime.Must(c.scheme.Convert(vpcNatTunnel, data, nil))
		_, err = c.syncer.GetLocalClient().Resource(schema.GroupVersionResource{
			Group:    "kubeovn.ustc.io",
			Version:  "v1",
			Resource: "vpcnattunnels",
		}).Update(context.Background(), data, metav1.UpdateOptions{})
		if err != nil {
			log.Log.Error(err, "Error update vpctunnels")
			return false
		}
	}
	return false
}
