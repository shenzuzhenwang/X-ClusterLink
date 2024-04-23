package controller

import (
	"context"
	"fmt"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/submariner-io/admiral/pkg/federate"
	"github.com/submariner-io/admiral/pkg/resource"
	"github.com/submariner-io/admiral/pkg/syncer"
	"github.com/submariner-io/admiral/pkg/syncer/broker"
	"github.com/submariner-io/admiral/pkg/util"
	submarinerv1alpha1 "github.com/submariner-io/submariner-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	kubeovnv1 "kubeovn-multivpc/api/v1"
	"os"
	"strconv"
	"time"
)

type GatewayExIpController struct {
	scheme         *runtime.Scheme
	localClient    dynamic.Interface
	restMapper     meta.RESTMapper
	syncer         *broker.Syncer
	localSyncer    syncer.Interface
	remoteSyncer   syncer.Interface
	clusterID      string
	localNamespace string
}

var (
	BrokerResyncPeriod = time.Minute * 2
	GateewayExIpGVR    = schema.GroupVersionResource{
		Group:    kubeovnv1.GroupVersion.Group,
		Version:  kubeovnv1.GroupVersion.Version,
		Resource: "gatewayexips",
	}
)

func NewGatewayIpController(syncerConfig broker.SyncerConfig) *GatewayExIpController {
	controller := &GatewayExIpController{
		localClient: syncerConfig.LocalClient,
		restMapper:  syncerConfig.RestMapper,
		scheme:      syncerConfig.Scheme,
	}
	// 初始化环境变量
	cr := &submarinerv1alpha1.ServiceDiscovery{}
	gvk := schema.GroupVersionKind{
		Group:   "submariner.io",
		Version: "v1alpha1",
		Kind:    "ServiceDiscovery",
	}
	obj, err := controller.localClient.Resource(schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: "servicediscoveries",
	}).Namespace("submariner-operator").Get(context.TODO(), "service-discovery", metav1.GetOptions{})
	if err != nil {
		return nil
	}
	utilruntime.Must(controller.scheme.Convert(obj, cr, nil))
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

	controller.clusterID = cr.Spec.ClusterID
	controller.localNamespace = cr.Spec.Namespace
	// 创建 Broker Syncer用于之后创建localSyncer
	brokerSyncerConfig := syncerConfig
	brokerSyncerConfig.LocalNamespace = metav1.NamespaceAll
	brokerSyncerConfig.LocalClusterID = controller.clusterID
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
	// 创建brokerSyncer
	brokerSyncer, err := broker.NewSyncer(syncerConfig)
	if err != nil {
		return nil
	}
	controller.syncer = brokerSyncer
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

// 将 Object 转为 GatewayExIp CR
func (g *GatewayExIpController) converterToStructured(obj runtime.Object) *kubeovnv1.GatewayExIp {
	to := &kubeovnv1.GatewayExIp{}
	utilruntime.Must(g.scheme.Convert(obj, to, nil))
	return to
}

// 将 GatewayExIp CR 转为 Object
func (g *GatewayExIpController) converterToUnstructured(obj runtime.Object) *unstructured.Unstructured {
	to := &unstructured.Unstructured{}
	utilruntime.Must(g.scheme.Convert(obj, to, nil))
	return to
}

func (g *GatewayExIpController) reconcileLocal() {
	g.remoteSyncer.Reconcile(func() []runtime.Object {
		siList, err := g.localClient.Resource(GateewayExIpGVR).Namespace(corev1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			klog.Info(err, "Error listing ServiceImports")
			return nil
		}
		retList := make([]runtime.Object, 0, len(siList.Items))
		for i := range siList.Items {
			si := g.converterToStructured(&siList.Items[i])
			// 加点逻辑
			si.Name = fmt.Sprintf("%s-%s", si.Name, si.Namespace)
			si.Namespace = g.syncer.GetBrokerNamespace()

			retList = append(retList, si)
		}
		return retList
	})
}

func (g *GatewayExIpController) reconcileRemote() {
	g.localSyncer.Reconcile(func() []runtime.Object {
		siList := g.remoteSyncer.ListResources()
		retList := make([]runtime.Object, 0, len(siList))
		for i := range siList {
			si := g.converterToStructured(siList[i])
			si.Namespace = g.localNamespace
			// 加点逻辑
			retList = append(retList, si)
		}
		return retList
	})
}

func (g *GatewayExIpController) onLocal(obj runtime.Object, _ int, op syncer.Operation) (runtime.Object, bool) {
	// 后面要加逻辑
	gatewayExIp := obj.(*kubeovnv1.GatewayExIp)
	key, _ := cache.MetaNamespaceKeyFunc(gatewayExIp)
	klog.Info("Local GatewayExIp %q %sd", key, op)
	if op == syncer.Delete {

		return obj, false
	} else if op == syncer.Create {
	}

	return obj, false
}

func (g *GatewayExIpController) onRemote(obj runtime.Object, _ int, _ syncer.Operation) (runtime.Object, bool) {
	// 后面要加逻辑
	gatewayExIp := obj.(*kubeovnv1.GatewayExIp)
	gatewayExIp.Namespace = g.localNamespace
	return gatewayExIp, false
}

func (g *GatewayExIpController) onSuccessfulSyncFromBroker(obj runtime.Object, op syncer.Operation) bool {
	// 后面要加逻辑
	if op == syncer.Delete {
		return false
	}
	return false
}

// 根据 本集群上的 GatewayExIp 对 Broker 上的 GatewayExIp 做 Create（之前没有）/ Update（之前有）
func (g *GatewayExIpController) Distribute(ctx context.Context, obj runtime.Object) error {
	localGatewayExIp := g.converterToStructured(obj)
	key, _ := cache.MetaNamespaceKeyFunc(localGatewayExIp)
	klog.Info("Distribute for local GatewayExIp %q", key)
	// 构造要在 broker 部署的 GatewayExIp CR
	brokerGatewayExIp := &kubeovnv1.GatewayExIp{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", " ", ""),
		},
		Spec: kubeovnv1.GatewayExIpSpec{
			ExternalIP: localGatewayExIp.Spec.ExternalIP,
		},
	}
	brokerClient := g.syncer.GetBrokerClient().Resource(GateewayExIpGVR).Namespace(g.syncer.GetBrokerNamespace())
	result, err := util.CreateOrUpdate(ctx,
		resource.ForDynamic(brokerClient),
		g.converterToUnstructured(brokerGatewayExIp),
		func(obj *unstructured.Unstructured) (*unstructured.Unstructured, error) { return obj, nil },
	)
	if err != nil {
		klog.Info(err)
	}
	if result == util.OperationResultCreated {
		klog.Info("Created aggregated ServiceImport %q", brokerGatewayExIp.Name)
	}
	return err
}

// 根据 本集群上的 GatewayExIp 对 Broker 上的 GatewayExIp 删除
func (g *GatewayExIpController) Delete(ctx context.Context, obj runtime.Object) error {
	localGatewayExIp := g.converterToStructured(obj)
	key, _ := cache.MetaNamespaceKeyFunc(localGatewayExIp)
	klog.Info("Delete for local GatewayExIp %q", key)
	brokerClient := g.syncer.GetBrokerClient().Resource(GateewayExIpGVR).Namespace(g.syncer.GetBrokerNamespace())
	err := brokerClient.Delete(ctx, localGatewayExIp.Name, metav1.DeleteOptions{})
	if err != nil {
		klog.Info(err)
	}
	return err
}
