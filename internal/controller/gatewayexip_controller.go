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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	kubeovnv1 "kubeovn-multivpc/api/v1"
	"time"
)

//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=gatewayexips,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=gatewayexips/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=gatewayexips/finalizers,verbs=update

type GatewayExIpController struct {
	localClient    dynamic.Interface
	restMapper     meta.RESTMapper
	localSyncer    syncer.Interface
	remoteSyncer   syncer.Interface
	clusterID      string
	localNamespace string
}

type AgentSpecification struct {
	ClusterID      string
	LocalNamespace string
}

var BrokerResyncPeriod = time.Minute * 2

func NewGatewayIpController(spec AgentSpecification, syncerConfig broker.SyncerConfig) *GatewayExIpController {
	controller := &GatewayExIpController{
		localClient:    syncerConfig.LocalClient,
		restMapper:     syncerConfig.RestMapper,
		clusterID:      spec.ClusterID,
		localNamespace: spec.LocalNamespace,
	}
	// 创建 Broker Syncer用于之后创建localSyncer
	brokerSyncerConfig := syncerConfig
	brokerSyncerConfig.LocalNamespace = metav1.NamespaceAll
	brokerSyncerConfig.LocalClusterID = spec.ClusterID
	brokerSyncerConfig.ResourceConfigs = []broker.ResourceConfig{
		{
			LocalSourceNamespace:   metav1.NamespaceAll,
			LocalResourceType:      &kubeovnv1.GatewayExIp{},
			TransformLocalToBroker: controller.onLocal,
			OnSuccessfulSyncToBroker: func(obj runtime.Object, op syncer.Operation) bool {
				// 逻辑代码
				return false
			},
			BrokerResourceType:     &kubeovnv1.GatewayExIp{},
			TransformBrokerToLocal: controller.onRemote,
			OnSuccessfulSyncFromBroker: func(obj runtime.Object, op syncer.Operation) bool {
				// 逻辑代码
				return false
			},
			BrokerResyncPeriod: BrokerResyncPeriod,
		},
	}
	var err error
	// 创建brokerSyncer
	brokerSyncer, err := broker.NewSyncer(syncerConfig)
	if err != nil {
		return nil
	}
	// 创建localSyncer
	controller.localSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:            "Local GatewayExIp",
		SourceClient:    syncerConfig.LocalClient,
		SourceNamespace: controller.localNamespace,
		Direction:       syncer.LocalToRemote,
		RestMapper:      syncerConfig.RestMapper,
		Federator:       controller,
		ResourceType:    &kubeovnv1.GatewayExIp{},
		Transform:       controller.onLocal,
		Scheme:          syncerConfig.Scheme,
		SyncCounterOpts: &prometheus.GaugeOpts{
			// 自定义
			//Name: syncerMetricNames.ServiceExportCounterName,
			//Help: "Count of exported services",
		},
	})
	if err != nil {
		return nil
	}
	//创建remoteSyncer
	controller.remoteSyncer, err = syncer.NewResourceSyncer(&syncer.ResourceSyncerConfig{
		Name:             "Remote GatewayExIp",
		SourceClient:     brokerSyncer.GetBrokerClient(),
		SourceNamespace:  brokerSyncer.GetBrokerNamespace(),
		RestMapper:       syncerConfig.RestMapper,
		Federator:        federate.NewCreateOrUpdateFederator(syncerConfig.LocalClient, syncerConfig.RestMapper, corev1.NamespaceAll, ""),
		ResourceType:     &kubeovnv1.GatewayExIp{},
		Transform:        controller.onRemote,
		OnSuccessfulSync: controller.onSuccessfulSyncFromBroker,
		Scheme:           syncerConfig.Scheme,
		ResyncPeriod:     BrokerResyncPeriod,
		SyncCounterOpts:  &prometheus.GaugeOpts{
			// 自定义
			//Name: syncerMetricNames.ServiceImportCounterName,
			//Help: "Count of imported services",
		},
	})
	if err != nil {
		return nil
	}
	return controller
}

func (g *GatewayExIpController) Start(ctx context.Context) error {
	// 创建一个带有 context 的停止通道
	stopCh := ctx.Done()

	if err := g.localSyncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting local ServiceImport syncer")
	}

	if err := g.remoteSyncer.Start(stopCh); err != nil {
		return errors.Wrap(err, "error starting remote ServiceImport syncer")
	}

	g.reconcileLocal()
	g.reconcileRemote()

	return nil
}

func (g *GatewayExIpController) reconcileLocal() {

}

func (g *GatewayExIpController) reconcileRemote() {

}

func (g *GatewayExIpController) onLocal(obj runtime.Object, _ int, _ syncer.Operation) (runtime.Object, bool) {

}

func (g *GatewayExIpController) onRemote(obj runtime.Object, _ int, _ syncer.Operation) (runtime.Object, bool) {

}

func (g *GatewayExIpController) onSuccessfulSyncFromBroker(obj runtime.Object, op syncer.Operation) bool {

}

func (g *GatewayExIpController) Distribute(ctx context.Context, obj runtime.Object) error {

}

func (g *GatewayExIpController) Delete(ctx context.Context, obj runtime.Object) error {

}
