package controller

import (
	"context"
	"fmt"
	kubeovnv1 "kubeovn-multivpc/api/v1"
	"strings"

	Submariner "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type GatewayInformer struct {
	ClusterId string
	Client    client.Client
	Config    *rest.Config
	Tunnelr   *VpcNatTunnelReconciler
}

func NewInformer(clusterId string, client client.Client, config *rest.Config, re *VpcNatTunnelReconciler) *GatewayInformer {
	return &GatewayInformer{ClusterId: clusterId, Client: client, Config: config, Tunnelr: re}
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
		//********************** add 方法对应 创建 Vpc-Gateway StatefulSet 时执行的操作，创建/更新对应的 GatewayExIp
		AddFunc: func(obj interface{}) {
			statefulSet := obj.(*appsv1.StatefulSet)
			// 通过 Vpc-Gateway 的名称找到对应的VpcNatTunnel，可能有多个VpcNatTunnel，因此获取VpcNatTunnelList
			natGw := strings.TrimPrefix(statefulSet.Name, "vpc-nat-gw-")
			if statefulSet.Status.AvailableReplicas == 1 {

				// 创建 Vpc-Gateway 对应的 GatewayExIp
				gatewayExIp := &kubeovnv1.GatewayExIp{}
				pod, err := r.Tunnelr.getNatGwPod(natGw)
				if err != nil {
					log.Log.Error(err, "Error get gw pod")
					return
				}
				// 寻找此gw pod对应的gatewayExIp
				err = r.Client.Get(ctx, client.ObjectKey{
					Name:      natGw + "." + r.ClusterId,
					Namespace: "kube-system",
				}, gatewayExIp)
				if err != nil {
					// 错误为没有找到对应的gatewayExIp，则进行创建
					if errors.IsNotFound(err) {

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
						// 找到此gw pod的ExternIP
						GwExternIP, err := r.Tunnelr.getGwExternIP(pod)
						if err != nil {
							log.Log.Error(err, "Error get GwExternIP")
							return
						}
						gatewayExIp.Spec.ExternalIP = GwExternIP

						// 创建gatewayExIp
						gatewayExIp.Name = natGw + "." + r.ClusterId
						gatewayExIp.Namespace = pod.Namespace
						gatewayExIp.Spec.GlobalNetCIDR = submarinerCluster.Spec.GlobalCIDR[0]

						err = r.Client.Create(ctx, gatewayExIp)
						if err != nil {
							log.Log.Error(err, "Error create gatewayExIp")
							return
						}
					}
				} else {
					// 资源找到，则进行更新而非创建
					GwExternIP, err := r.Tunnelr.getGwExternIP(pod)
					if err != nil {
						log.Log.Error(err, "Error get GwExternIP")
						return
					}
					// 更新ExternalIP
					gatewayExIp.Spec.ExternalIP = GwExternIP
					// 更改gatewayExIp
					err = r.Client.Update(ctx, gatewayExIp)
					if err != nil {
						log.Log.Error(err, "Error update gatewayExIp")
						return
					}
				}
			}
		},
		// update 方法对应 Vpc-Gateway Statefulset 状态更新时执行的操作
		UpdateFunc: func(old, new interface{}) {
			oldStatefulSet := old.(*appsv1.StatefulSet)
			newStatefulSet := new.(*appsv1.StatefulSet)

			//********************* Vpc-Gateway 节点重启，可用 pod 从 0 到 1
			if oldStatefulSet.Status.AvailableReplicas == 0 && newStatefulSet.Status.AvailableReplicas == 1 {
				// 找到所有natGwDp为Vpc-Gateway的VpcTunnel
				natGw := strings.TrimPrefix(newStatefulSet.Name, "vpc-nat-gw-")
				labelsSet := map[string]string{
					"NatGwDp": natGw,
				}
				option := client.ListOptions{
					LabelSelector: labels.SelectorFromSet(labelsSet),
				}
				err = r.Client.List(ctx, &vpcNatTunnelList, &option)
				if err != nil {
					log.Log.Error(err, "Error get vpcNatTunnel list")
					return
				}

				// 更新 Vpc-gw 对应的 GatewayExIp
				gatewayExIp := &kubeovnv1.GatewayExIp{}
				err := r.Client.Get(ctx, client.ObjectKey{
					Name:      natGw + "." + r.ClusterId,
					Namespace: "kube-system",
				}, gatewayExIp)
				if err != nil {
					log.Log.Error(err, "Error get GatewayExIp")
					return
				}
				// 更新 Vpc-Gateway 对应的 GatewayExIp
				pod, err := r.Tunnelr.getNatGwPod(natGw)
				if err != nil {
					log.Log.Error(err, "Error get GwPod")
					return
				}
				GwExternIP, err := r.Tunnelr.getGwExternIP(pod)
				if err != nil {
					log.Log.Error(err, "Error get GwExternIP")
					return
				}
				gatewayExIp.Spec.ExternalIP = GwExternIP

				err = r.Client.Update(ctx, gatewayExIp)
				if err != nil {
					log.Log.Error(err, "Error update gatewayExIp")
					return
				}
				// 更新 相关的vpcNatTunnel 状态
				for _, vpcTunnel := range vpcNatTunnelList.Items {

					/*********************/
					podnext, err := r.Tunnelr.getNatGwPod(vpcTunnel.Spec.NatGwDp) // find pod named Spec.NatGwDp
					if err != nil {
						log.Log.Error(err, "Error get GwPod")
						return
					}

					GwExternIP, err := r.Tunnelr.getGwExternIP(podnext)
					if err != nil {
						log.Log.Error(err, "Error get GwExternIP")
						return
					}
					vpcTunnel.Status.InternalIP = GwExternIP

					err = r.Tunnelr.execCommandInPod(podnext.Name, podnext.Namespace, "vpc-nat-gw", r.Tunnelr.genCreateTunnelCmd(&vpcTunnel))
					if err != nil {
						log.Log.Error(err, "Error get exec CreateTunnelCmd")
						return
					}
					err = r.Tunnelr.execCommandInPod(podnext.Name, podnext.Namespace, "vpc-nat-gw", genGlobalnetRoute(&vpcTunnel))
					if err != nil {
						log.Log.Error(err, "Error get exec GlobalnetRoute")
						return
					}
					if err = r.Tunnelr.Status().Update(ctx, &vpcTunnel); err != nil {
						log.Log.Error(err, "Error update vpcTunnel")
						return
					}
					/*********************/
				}
			}
		},
		//*********************** delete 方法对应删除 Vpc-Gateway StatefulSet 时执行的操作，删除 Vpc-Gateway 对应的 GatewayExIp
		DeleteFunc: func(obj interface{}) {
			statefulSet := obj.(*appsv1.StatefulSet)

			// 通过 Vpc-Gateway 的名称找到对应的 GatewayExIp
			natGw := strings.TrimPrefix(statefulSet.Name, "vpc-nat-gw-")
			// 删除 Vpc-Gateway 对应的 GatewayExIp
			gatewayExIp := &kubeovnv1.GatewayExIp{}
			err := r.Client.Get(ctx, client.ObjectKey{
				Name:      natGw + "." + r.ClusterId,
				Namespace: "kube-system",
			}, gatewayExIp)
			if err != nil {
				log.Log.Error(err, "Error get gatewayExIp")
				return
			}
			err = r.Client.Delete(ctx, gatewayExIp)
			if err != nil {
				log.Log.Error(err, "Error delete gatewayExIp")
				return
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
