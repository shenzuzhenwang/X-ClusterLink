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
	"github.com/pkg/errors"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	submarinerv1alpha1 "github.com/submariner-io/submariner-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubeovnv1 "kubeovn-multivpc/api/v1"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"strconv"
	"time"
)

//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=gatewayexips,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=gatewayexips/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=gatewayexips/finalizers,verbs=update
//+kubebuilder:rbac:groups=submariner.io,resources=servicediscoveries,verbs=get;list;watch;create;update;patch;delete

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
func InitEnvVars(c client.Client) error {
	cr := &submarinerv1alpha1.ServiceDiscovery{}
	if err := c.Get(context.TODO(), types.NamespacedName{Namespace: "submariner-operator"}, cr); err != nil {
		log.Log.Error(err, "problem init envvar")
		return err
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
	return nil
}

func New(spec *AgentSpecification, syncerConfig broker.SyncerConfig) *Controller {
	c := &Controller{
		clusterID: spec.ClusterID,
	}
	var err error
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
