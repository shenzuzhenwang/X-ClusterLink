package controller

import (
	"context"
	"fmt"
	ovn "github.com/kubeovn/kube-ovn/pkg/apis/kubeovn/v1"
	Submariner "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	kubeovnv1 "kubeovn-multivpc/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"strings"
)

type GatewayInformer struct {
	ClusterId string
	Client    client.Client
	Config    *rest.Config
}

//+kubebuilder:rbac:groups=kubeovn.io,resources=vpc-nat-gateways,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kubeovn.io,resources=vpcs,verbs=get;list;watch;create;update;patch;delete

func NewInformer(clusterId string, client client.Client, config *rest.Config) *GatewayInformer {
	return &GatewayInformer{ClusterId: clusterId, Client: client, Config: config}
}

func (r *GatewayInformer) Start(ctx context.Context) error {
	clientSet, err := kubernetes.NewForConfig(r.Config)
	if err != nil {
		log.Log.Error(err, "Error create client")
		return err
	}
	var vpcNatTunnelList kubeovnv1.VpcNatTunnelList
	labelSelector := labels.Set{
		"ovn.kubernetes.io/vpc-nat-gw": "true",
	}
	informer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				options.LabelSelector = labelSelector.AsSelector().String()
				return clientSet.AppsV1().StatefulSets("kube-system").List(ctx, options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				options.LabelSelector = labelSelector.AsSelector().String()
				return clientSet.AppsV1().StatefulSets("kube-system").Watch(ctx, options)
			},
		},
		&appsv1.StatefulSet{},
		0,
		cache.Indexers{},
	)
	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		//  add 方法对应 创建 Vpc-Gateway StatefulSet 时执行的操作，创建对应的 GatewayExIp
		AddFunc: func(obj interface{}) {
			statefulSet := obj.(*appsv1.StatefulSet)
			// 通过 Vpc-Gateway 的名称找到对应的VpcNatTunnel，可能有多个VpcNatTunnel，因此获取VpcNatTunnelList
			gatewayName := strings.TrimPrefix(statefulSet.Name, "vpc-nat-gw-")
			if statefulSet.Status.AvailableReplicas == 1 {
				natGw := &ovn.VpcNatGateway{}
				err = r.Client.Get(ctx, client.ObjectKey{
					Name: gatewayName,
				}, natGw)
				if err != nil {
					log.Log.Error(err, "Error Get Vpc-Nat-Gateway")
					return
				}
				// 创建 Vpc-Gateway 对应的 GatewayExIp
				gatewayExIp := &kubeovnv1.GatewayExIp{}
				pod, err := getNatGwPod(gatewayName, r.Client)
				if err != nil {
					log.Log.Error(err, "Error get gw pod")
					return
				}
				// 寻找此 gw pod 对应的 GatewayExIp
				err = r.Client.Get(ctx, client.ObjectKey{
					Name:      natGw.Spec.Vpc + "." + r.ClusterId,
					Namespace: "kube-system",
				}, gatewayExIp)
				if err != nil {
					// 错误为没有找到对应的 gatewayExIp，则进行创建
					if errors.IsNotFound(err) {
						vpcName := natGw.Spec.Vpc
						vpc := &ovn.Vpc{}
						err = r.Client.Get(ctx, client.ObjectKey{
							Name: vpcName,
						}, vpc)
						if err != nil {
							log.Log.Error(err, "Error Get Vpc")
							return
						}
						for _, router := range vpc.Spec.StaticRoutes {
							// 若 VPC 路由不是流向当前 Vpc-Gateway，则不添加该Vpc-Gateway的GatewayExIp
							if router.NextHopIP != natGw.Spec.LanIP {
								return
							}
						}
						// 找到本集群的GlobalNetCIDR
						submarinerCluster := &Submariner.Cluster{}
						err := r.Client.Get(ctx, client.ObjectKey{
							Namespace: "submariner-operator",
							Name:      r.ClusterId,
						}, submarinerCluster)
						if err != nil {
							log.Log.Error(err, "Error get submarinerCluster")
							return
						}
						// 找到此 gw 对应的 ExternIP
						GwExternIP, err := getGwExternIP(pod)
						if err != nil {
							log.Log.Error(err, "Error get GwExternIP")
							return
						}
						// 创建 gatewayExIp
						gatewayExIp.Name = natGw.Spec.Vpc + "." + r.ClusterId
						gatewayExIp.Namespace = pod.Namespace
						gatewayExIp.Spec.ExternalIP = GwExternIP
						gatewayExIp.Spec.GlobalNetCIDR = submarinerCluster.Spec.GlobalCIDR[0]
						label := make(map[string]string)
						label["localVpc"] = natGw.Spec.Vpc
						label["localGateway"] = natGw.Name
						gatewayExIp.Labels = label
						err = r.Client.Create(ctx, gatewayExIp)
						if err != nil {
							log.Log.Error(err, "Error create gatewayExIp")
							return
						}
					}
				} else {
					// 若 gatewayExIp 存在，说明已经有网关在Vpc中运行，则新加的网关当作备用网关，不做处理
				}
			}
		},
		// update 方法对应 Vpc-Gateway StatefulSet 状态更新时执行的操作
		UpdateFunc: func(old, new interface{}) {
			oldStatefulSet := old.(*appsv1.StatefulSet)
			newStatefulSet := new.(*appsv1.StatefulSet)

			// Vpc-Gateway 节点宕掉，可用 pod 从 1 到 0 ，判断是不是正在使用的 Vpc-Gateway 宕掉，若是则切换 Vpc-Gateway
			if oldStatefulSet.Status.AvailableReplicas == 1 && newStatefulSet.Status.AvailableReplicas == 0 {
				gatewayName := strings.TrimPrefix(newStatefulSet.Name, "vpc-nat-gw-")
				// 通过 Vpc-Gateway name 寻找对应的 GatewayExIp
				labelSet := map[string]string{
					"localGateway": gatewayName,
				}
				options := client.ListOptions{
					LabelSelector: labels.SelectorFromSet(labelSet),
				}
				gatewayExIpList := &kubeovnv1.GatewayExIpList{}
				gatewayExIp := &kubeovnv1.GatewayExIp{}
				if err = r.Client.List(ctx, gatewayExIpList, &options); err != nil {
					return
				}
				for _, gatewayExIp = range gatewayExIpList.Items {
					if gatewayExIp.Labels["localGateway"] == gatewayName {
						break
					}
				}
				if gatewayExIp.Labels["localGateway"] != gatewayName {
					// 若没有找到，说明当前宕掉的网关是备用的，则不处理
					return
				}
				// 获取 vpc
				vpc := &ovn.Vpc{}
				if err = r.Client.Get(ctx, client.ObjectKey{Name: gatewayExIp.Labels["localVpc"]}, vpc); err != nil {
					return
				}
				// 寻找可用的 Vpc-Gateway
				GwList := &appsv1.StatefulSetList{}
				err = r.Client.List(ctx, GwList, client.InNamespace("kube-system"), client.MatchingLabels{"ovn.kubernetes.io/vpc-nat-gw": "true"})
				if err != nil {
					log.Log.Error(err, "Error get StatefulSetList")
					return
				}
				GwStatefulSet := &appsv1.StatefulSet{}
				// find Vpc-Gateway which status is Active
				for _, statefulSet := range GwList.Items {
					// gateway 可用 pod 为 1 且 gateway 对应的 vpc 要与之前的一致
					if statefulSet.Status.AvailableReplicas == 1 &&
						statefulSet.Spec.Template.Annotations["ovn.kubernetes.io/logical_router"] == newStatefulSet.Spec.Template.Annotations["ovn.kubernetes.io/logical_router"] {
						GwStatefulSet = &statefulSet
						break
					}
				}
				if GwStatefulSet.Name == "" {
					return
				}
				for _, route := range vpc.Spec.StaticRoutes {
					route.NextHopIP = GwStatefulSet.Spec.Template.Annotations["ovn.kubernetes.io/ip_address"]
				}
				podNext, err := getNatGwPod(strings.TrimPrefix(GwStatefulSet.Name, "vpc-nat-gw-"), r.Client)
				if err != nil {
					log.Log.Error(err, "Error get GwPod")
					return
				}
				GwExternIP, err := getGwExternIP(podNext)
				if err != nil {
					log.Log.Error(err, "Error get GwExternIP")
					return
				}
				// 更新 Vpc 路由策略
				err = r.Client.Update(ctx, vpc)
				if err != nil {
					log.Log.Error(err, "Error update Vpc")
					return
				}
				// 更新 GatewayExIp
				gatewayExIp.Spec.ExternalIP = GwExternIP
				gatewayExIp.Labels["localGateway"] = strings.TrimPrefix(GwStatefulSet.Name, "vpc-nat-gw-")

				err = r.Client.Update(ctx, gatewayExIp)
				if err != nil {
					log.Log.Error(err, "Error update gatewayExIp")
					return
				}
				// 找到所有 localVpc 为 当前 Vpc 的 VpcTunnel
				labelsSet := map[string]string{
					"localVpc": vpc.Name,
				}
				option := client.ListOptions{
					LabelSelector: labels.SelectorFromSet(labelsSet),
				}
				err = r.Client.List(ctx, &vpcNatTunnelList, &option)
				if err != nil {
					log.Log.Error(err, "Error get vpcNatTunnel list")
					return
				}
				// 更新 相关的 VpcNatTunnel
				for _, vpcTunnel := range vpcNatTunnelList.Items {
					vpcTunnel.Spec.InternalIP = GwExternIP
					vpcTunnel.Spec.LocalGw = strings.TrimPrefix(GwStatefulSet.Name, "vpc-nat-gw-")

					if err = r.Client.Update(ctx, &vpcTunnel); err != nil {
						log.Log.Error(err, "Error update vpcTunnel")
						return
					}
				}
			}
			// Vpc-Gateway 节点重启，可用 pod 从 0 到 1，这里主要处理单网关的情况，多网关直接在上面处理完成了
			if oldStatefulSet.Status.AvailableReplicas == 0 && newStatefulSet.Status.AvailableReplicas == 1 {
				gatewayName := strings.TrimPrefix(newStatefulSet.Name, "vpc-nat-gw-")
				// 通过 Vpc-Gateway name 寻找对应的 GatewayExIp
				labelSet := map[string]string{
					"localGateway": gatewayName,
				}
				options := client.ListOptions{
					LabelSelector: labels.SelectorFromSet(labelSet),
				}
				gatewayExIpList := &kubeovnv1.GatewayExIpList{}
				gatewayExIp := &kubeovnv1.GatewayExIp{}
				if err = r.Client.List(ctx, gatewayExIpList, &options); err != nil {
					return
				}
				for _, gatewayExIp = range gatewayExIpList.Items {
					if gatewayExIp.Labels["localGateway"] == gatewayName {
						break
					}
				}
				if gatewayExIp.Labels["localGateway"] != gatewayName {
					// 若没有找到，说明当前重启的网关是备用的，则不处理
					return
				}
				// 若重启的网关是正在使用的，说明该 Vpc 是单网关
				podNext, err := getNatGwPod(gatewayName, r.Client)
				if err != nil {
					log.Log.Error(err, "Error get GwPod")
					return
				}
				GwExternIP, err := getGwExternIP(podNext)
				if err != nil {
					log.Log.Error(err, "Error get GwExternIP")
					return
				}
				// 更新 GatewayExIp
				gatewayExIp.Spec.ExternalIP = GwExternIP
				err = r.Client.Update(ctx, gatewayExIp)
				if err != nil {
					log.Log.Error(err, "Error update gatewayExIp")
					return
				}
				// 找到所有 localVpc 为 当前 Vpc 的 VpcTunnel
				labelsSet := map[string]string{
					"localVpc": gatewayExIp.Labels["localVpc"],
				}
				option := client.ListOptions{
					LabelSelector: labels.SelectorFromSet(labelsSet),
				}
				err = r.Client.List(ctx, &vpcNatTunnelList, &option)
				if err != nil {
					log.Log.Error(err, "Error get vpcNatTunnel list")
					return
				}
				klog.Info("test")
				// 更新 相关的 VpcNatTunnel
				for _, vpcTunnel := range vpcNatTunnelList.Items {
					vpcTunnel.Spec.InternalIP = GwExternIP
					if err = r.Client.Update(ctx, &vpcTunnel); err != nil {
						log.Log.Error(err, "Error update vpcTunnel")
						return
					}
				}
			}
		},
		//  delete 方法对应删除 Vpc-Gateway StatefulSet 时执行的操作
		DeleteFunc: func(obj interface{}) {
			statefulSet := obj.(*appsv1.StatefulSet)
			// 通过 Vpc-Gateway 的名称找到对应的 Vpc-Gateway
			gatewayName := strings.TrimPrefix(statefulSet.Name, "vpc-nat-gw-")
			// 通过 Vpc-Gateway name 寻找对应的 GatewayExIp
			labelSet := map[string]string{
				"localGateway": gatewayName,
			}
			options := client.ListOptions{
				LabelSelector: labels.SelectorFromSet(labelSet),
			}
			gatewayExIpList := &kubeovnv1.GatewayExIpList{}
			gatewayExIp := &kubeovnv1.GatewayExIp{}
			if err = r.Client.List(ctx, gatewayExIpList, &options); err != nil {
				return
			}
			for _, gatewayExIp = range gatewayExIpList.Items {
				if gatewayExIp.Labels["localGateway"] == gatewayName {
					break
				}
			}
			if gatewayExIp.Labels["localGateway"] != gatewayName {
				// 若没有找到，说明当前被删除的网关是备用的，则不处理
				return
			}
			vpcName := gatewayExIp.Labels["localVpc"]
			vpc := &ovn.Vpc{}
			err = r.Client.Get(ctx, client.ObjectKey{
				Name: vpcName,
			}, vpc)
			if err != nil {
				log.Log.Error(err, "Error Get Vpc")
				return
			}
			// 寻找可用的 Vpc-Gateway
			GwList := &appsv1.StatefulSetList{}
			err = r.Client.List(ctx, GwList, client.InNamespace("kube-system"), client.MatchingLabels{"ovn.kubernetes.io/vpc-nat-gw": "true"})
			if err != nil {
				log.Log.Error(err, "Error get StatefulSetList")
				return
			}
			GwStatefulSet := &appsv1.StatefulSet{}
			// find Vpc-Gateway which status is Active
			for _, st := range GwList.Items {
				// gateway 可用 pod 为 1 且 gateway 对应的 vpc 要与之前的一致
				if st.Status.AvailableReplicas == 1 &&
					st.Spec.Template.Annotations["ovn.kubernetes.io/logical_router"] == statefulSet.Spec.Template.Annotations["ovn.kubernetes.io/logical_router"] {
					GwStatefulSet = &st
					break
				}
			}
			if GwStatefulSet.Name == "" {
				// 若没有可用的备用网关，直接删除 gatewayExIp
				err = r.Client.Delete(ctx, gatewayExIp)
				if err != nil {
					log.Log.Error(err, "Error delete gatewayExIp")
					return
				}
			}
			for _, route := range vpc.Spec.StaticRoutes {
				route.NextHopIP = GwStatefulSet.Spec.Template.Annotations["ovn.kubernetes.io/ip_address"]
			}
			podNext, err := getNatGwPod(strings.TrimPrefix(GwStatefulSet.Name, "vpc-nat-gw-"), r.Client)
			if err != nil {
				log.Log.Error(err, "Error get GwPod")
				return
			}
			GwExternIP, err := getGwExternIP(podNext)
			if err != nil {
				log.Log.Error(err, "Error get GwExternIP")
				return
			}
			// 更新 Vpc 路由策略
			err = r.Client.Update(ctx, vpc)
			if err != nil {
				log.Log.Error(err, "Error update Vpc")
				return
			}
			gatewayExIp.Spec.ExternalIP = GwExternIP
			gatewayExIp.Labels["localGateway"] = strings.TrimPrefix(GwStatefulSet.Name, "vpc-nat-gw-")
			err = r.Client.Update(ctx, gatewayExIp)
			if err != nil {
				log.Log.Error(err, "Error update gatewayExIp")
				return
			}
			// 找到所有 localVpc 为 当前 Vpc 的 VpcTunnel
			labelsSet := map[string]string{
				"localVpc": vpcName,
			}
			option := client.ListOptions{
				LabelSelector: labels.SelectorFromSet(labelsSet),
			}
			err = r.Client.List(ctx, &vpcNatTunnelList, &option)
			if err != nil {
				log.Log.Error(err, "Error get vpcNatTunnel list")
				return
			}
			// 更新 相关的 VpcNatTunnel
			for _, vpcTunnel := range vpcNatTunnelList.Items {
				vpcTunnel.Spec.InternalIP = GwExternIP
				vpcTunnel.Spec.LocalGw = strings.TrimPrefix(GwStatefulSet.Name, "vpc-nat-gw-")
				if err = r.Client.Update(ctx, &vpcTunnel); err != nil {
					log.Log.Error(err, "Error update vpcTunnel")
					return
				}
			}
		},
	})
	if err != nil {
		return err
	}
	stopCh := make(chan struct{})
	defer close(stopCh)
	go informer.Run(stopCh)
	if !cache.WaitForCacheSync(stopCh, informer.HasSynced) {
		return fmt.Errorf("error syncing cache")
	}
	select {
	case <-stopCh:
		klog.Info("received termination signal, exiting")
		log.Log.Info("received termination signal, exiting")
	}
	return nil
}
