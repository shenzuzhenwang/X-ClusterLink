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
	"bytes"
	"context"
	"errors"
	"fmt"
	kubeovnv1 "kubeovn-multivpc/api/v1"
	"kubeovn-multivpc/internal/tunnel/factory"
	"strings"
	"time"

	Submariner "github.com/submariner-io/submariner/pkg/apis/submariner.io/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// VpcNatTunnelReconciler reconciles a VpcNatTunnel object
type VpcNatTunnelReconciler struct {
	client.Client
	ClusterId    string
	Scheme       *runtime.Scheme
	Config       *rest.Config
	KubeClient   kubernetes.Interface
	tunnelOpFact *factory.TunnelOperationFactory
}

//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=vpcnattunnels,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=vpcnattunnels/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kubeovn.ustc.io,resources=vpcnattunnels/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods/exec,verbs=get;create
//+kubebuilder:rbac:groups=submariner.io,resources=gateways,verbs=get;list;watch;
//+kubebuilder:rbac:groups=submariner.io,resources=clusterglobalegressips,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete

func (r *VpcNatTunnelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	vpcTunnel := &kubeovnv1.VpcNatTunnel{}
	err := r.Get(ctx, req.NamespacedName, vpcTunnel)
	if err != nil {
		log.Log.Error(err, "unable to fetch vpcNatTunnel")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !vpcTunnel.ObjectMeta.DeletionTimestamp.IsZero() {
		// 记录操作开始
		now := time.Now()
		fmt.Println(fmt.Sprintf("%d-%d-%d %d:%d:%d.%d   INFO   删除 VpcNatTunnel :%s", now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(), now.Nanosecond()/1e6, vpcTunnel.ObjectMeta.Name))
		return r.handleDelete(ctx, vpcTunnel)
	}
	// 记录操作开始
	now := time.Now()
	fmt.Println(fmt.Sprintf("%d-%d-%d %d:%d:%d.%d   INFO   创建/更新 VpcNatTunnel :%s", now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(), now.Nanosecond()/1e6, vpcTunnel.ObjectMeta.Name))
	return r.handleCreateOrUpdate(ctx, vpcTunnel)
}

func (r *VpcNatTunnelReconciler) execCommandInPod(podName, namespace, containerName, command string) error {
	clientset, err := kubernetes.NewForConfig(r.Config)
	if err != nil {
		log.Log.Error(err, "Error NewForConfig in execCommandInPod")
		return err
	}
	cmd := []string{
		"sh",
		"-c",
		command,
	}
	const tty = false
	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).SubResource("exec").Param("container", containerName)
	req.VersionedParams(
		&corev1.PodExecOptions{
			Command: cmd,
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
			TTY:     tty,
		},
		scheme.ParameterCodec,
	)

	var stdout, stderr bytes.Buffer
	exec, err := remotecommand.NewSPDYExecutor(r.Config, "POST", req.URL())
	if err != nil {
		log.Log.Error(err, "Error NewSPDYExecutor in execCommandInPod")
		return err
	}
	err = exec.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		log.Log.Error(err, "Error StreamWithContext in execCommandInPod")
		return err
	}
	if strings.TrimSpace(stderr.String()) != "" {
		return fmt.Errorf(strings.TrimSpace(stderr.String()))
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *VpcNatTunnelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Config = mgr.GetConfig()
	r.tunnelOpFact = factory.NewTunnelOpFactory()
	return ctrl.NewControllerManagedBy(mgr).
		For(&kubeovnv1.VpcNatTunnel{}).
		Complete(r)
}

func GenNatGwStsName(name string) string {
	return fmt.Sprintf("vpc-nat-gw-%s", name)
}

func (r *VpcNatTunnelReconciler) getNatGwPod(name string) (*corev1.Pod, error) {
	podList := &corev1.PodList{}
	matchLabels := map[string]string{"app": GenNatGwStsName(name), "ovn.kubernetes.io/vpc-nat-gw": "true"}
	listOpts := []client.ListOption{
		client.InNamespace("kube-system"),
		client.MatchingLabels(matchLabels),
	}
	err := r.List(context.TODO(), podList, listOpts...)
	if err != nil {
		return nil, err
	}
	pods := podList.Items
	switch {
	case len(pods) == 0:
		return nil, k8serrors.NewNotFound(corev1.Resource("pod"), name)
	case len(pods) != 1:
		time.Sleep(5 * time.Second)
		return nil, fmt.Errorf("too many pod")
	case pods[0].Status.Phase != "Running":
		time.Sleep(5 * time.Second)
		return nil, fmt.Errorf("pod is not active now")
	}

	return &pods[0], nil
}

func (r *VpcNatTunnelReconciler) getGlobalnetCIDR() (string, error) {
	submGwlist := &Submariner.GatewayList{}
	err := r.List(context.TODO(), submGwlist, client.InNamespace("submariner-operator"))
	if err != nil {
		return "", err
	}

	submGws := submGwlist.Items
	switch {
	case len(submGws) == 0:
		return "", fmt.Errorf("no Submariner.Gateway")
	default:
		return submGws[0].Status.LocalEndpoint.Subnets[0], nil
	}
}

func (r *VpcNatTunnelReconciler) getGlobalEgressIP() ([]string, error) {
	submGlobalEgressIP := &Submariner.ClusterGlobalEgressIP{}
	err := r.Get(context.TODO(), client.ObjectKey{Name: "cluster-egress.submariner.io"}, submGlobalEgressIP)
	if err != nil {
		return nil, err
	}
	return submGlobalEgressIP.Status.AllocatedIPs, nil
}

func (r *VpcNatTunnelReconciler) getGwExternIP(pod *corev1.Pod) (string, error) {
	if ExternIP, ok := pod.Annotations["ovn-vpc-external-network.kube-system.kubernetes.io/ip_address"]; ok {
		return ExternIP, nil
	} else {
		return "", fmt.Errorf("no ovn-vpc-external-network ip")
	}
}

func (r *VpcNatTunnelReconciler) getOvnGwIP(pod *corev1.Pod) (string, error) {
	if gw, ok := pod.Annotations["ovn.kubernetes.io/gateway"]; ok {
		return gw, nil
	} else {
		return "", fmt.Errorf("no ovn gateway")
	}
}

func (r *VpcNatTunnelReconciler) genCreateTunnelCmd(tunnel *kubeovnv1.VpcNatTunnel) string {
	return r.tunnelOpFact.CreateTunnelOperation(tunnel).CreateCmd()
}

func genGlobalnetRoute(vpcTunnel *kubeovnv1.VpcNatTunnel) string {
	// 入流量转发给ovn网关(逻辑交换机)
	InFlowRoute := fmt.Sprintf("ip route add %s via %s dev eth0", vpcTunnel.Status.GlobalnetCIDR, vpcTunnel.Status.OvnGwIP)
	// 跨集群流量路由至隧道
	OutFlowRoute := fmt.Sprintf("ip route add %s dev %s", vpcTunnel.Status.RemoteGlobalnetCIDR, vpcTunnel.Name)

	// 创建snat，将跨集群流量数据包源地址修改为ClusterGlobalEgressIP(globalnet cidr前8个)
	SNAT := fmt.Sprintf("iptables -t nat -A POSTROUTING -d %s -j SNAT --to-source %s-%s", vpcTunnel.Status.RemoteGlobalnetCIDR, vpcTunnel.Status.GlobalEgressIP[0], vpcTunnel.Status.GlobalEgressIP[len(vpcTunnel.Status.GlobalEgressIP)-1])
	return InFlowRoute + ";" + OutFlowRoute + ";" + SNAT
}

func genDelGlobalnetRoute(vpcTunnel *kubeovnv1.VpcNatTunnel) string {
	InFlowRoute := fmt.Sprintf("ip route del %s via %s dev eth0", vpcTunnel.Status.GlobalnetCIDR, vpcTunnel.Status.OvnGwIP)
	OutFlowRoute := fmt.Sprintf("ip route del %s dev %s", vpcTunnel.Status.RemoteGlobalnetCIDR, vpcTunnel.Name)

	SNAT := fmt.Sprintf("iptables -t nat -D POSTROUTING -d %s -j SNAT --to-source %s-%s", vpcTunnel.Status.RemoteGlobalnetCIDR, vpcTunnel.Status.GlobalEgressIP[0], vpcTunnel.Status.GlobalEgressIP[len(vpcTunnel.Status.GlobalEgressIP)-1])
	return InFlowRoute + ";" + OutFlowRoute + ";" + SNAT
}

func (r *VpcNatTunnelReconciler) genDeleteTunnelCmd(tunnel *kubeovnv1.VpcNatTunnel) string {
	return r.tunnelOpFact.CreateTunnelOperation(tunnel).DeleteCmd()
}

// update PodGwIP GlobalEgressIP GlobalnetCIDR GwExternIP
func (r *VpcNatTunnelReconciler) updateTunnelStatus(vpcTunnel *kubeovnv1.VpcNatTunnel, pod *corev1.Pod) error {
	GlobalnetCIDR, err := r.getGlobalnetCIDR()
	if err != nil {
		log.Log.Error(err, "Error get local GlobalnetCIDR")
		return err
	}
	vpcTunnel.Status.GlobalnetCIDR = GlobalnetCIDR
	ovnGwIP, err := r.getOvnGwIP(pod)
	if err != nil {
		log.Log.Error(err, "Error get local PodGwIP")
		return err
	}
	vpcTunnel.Status.OvnGwIP = ovnGwIP
	GlobalEgressIP, err := r.getGlobalEgressIP()
	if err != nil {
		log.Log.Error(err, "Error get local GlobalEgressIP")
		return err
	}
	vpcTunnel.Status.GlobalEgressIP = GlobalEgressIP
	GwExternIP, err := r.getGwExternIP(pod)
	if err != nil {
		log.Log.Error(err, "Error get local GwExternIP")
		return err
	}
	vpcTunnel.Status.InternalIP = GwExternIP
	return nil
}

func (r *VpcNatTunnelReconciler) handleCreateOrUpdate(ctx context.Context, vpcTunnel *kubeovnv1.VpcNatTunnel) (ctrl.Result, error) {
	if !containsString(vpcTunnel.ObjectMeta.Finalizers, "tunnel.finalizer.ustc.io") {
		controllerutil.AddFinalizer(vpcTunnel, "tunnel.finalizer.ustc.io")
		err := r.Update(ctx, vpcTunnel)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	// 获取 VpcNatTunnel 对应的对端 GatewayExIp
	remoteGatewayExIp := &kubeovnv1.GatewayExIp{}
	err := r.Client.Get(ctx, client.ObjectKey{
		Name:      vpcTunnel.Spec.RemoteVpc + "." + vpcTunnel.Spec.RemoteCluster,
		Namespace: "kube-system",
	}, remoteGatewayExIp)
	if err != nil {
		log.Log.Error(err, "Error get GatewayExIp")
		return ctrl.Result{}, err
	}

	if !vpcTunnel.Status.Initialized { // first build tunnel
		// init remoteIp
		vpcTunnel.Status.RemoteIP = remoteGatewayExIp.Spec.ExternalIP
		vpcTunnel.Spec.RemoteIP = remoteGatewayExIp.Spec.ExternalIP
		// init GlobalNetCIDR
		vpcTunnel.Status.RemoteGlobalnetCIDR = remoteGatewayExIp.Spec.GlobalNetCIDR

		vpcTunnel.Status.Initialized = true
		vpcTunnel.Status.InterfaceAddr = vpcTunnel.Spec.InterfaceAddr
		vpcTunnel.Status.Type = vpcTunnel.Spec.Type
		// 通过 localVpc 找到对应的 gatewayExIp, 从而找到当前正在使用的Gateway
		localGatewayExIp := &kubeovnv1.GatewayExIp{}
		err := r.Client.Get(ctx, client.ObjectKey{
			Name:      vpcTunnel.Spec.LocalVpc + "." + r.ClusterId,
			Namespace: "kube-system",
		}, localGatewayExIp)
		if err != nil {
			log.Log.Error(err, "Error get local GatewayExIp")
			return ctrl.Result{}, err
		}
		vpcTunnel.Spec.LocalGw = localGatewayExIp.GetObjectMeta().GetLabels()["localGateway"]
		vpcTunnel.Status.LocalGw = localGatewayExIp.GetObjectMeta().GetLabels()["localGateway"]

		// add tunnel
		gwPod, err := r.getNatGwPod(vpcTunnel.Status.LocalGw) // find pod named Spec.NatGwDp
		if err != nil {
			log.Log.Error(err, "Error get GwPod")
			return ctrl.Result{}, err
		}

		err = r.updateTunnelStatus(vpcTunnel, gwPod)
		if err != nil {
			return ctrl.Result{}, err
		}

		err = r.execCommandInPod(gwPod.Name, gwPod.Namespace, "vpc-nat-gw", r.genCreateTunnelCmd(vpcTunnel))
		if err != nil {
			log.Log.Error(err, "Error exec genCreateTunnelCmd in Initialized")
			return ctrl.Result{}, err
		}
		err = r.execCommandInPod(gwPod.Name, gwPod.Namespace, "vpc-nat-gw", genGlobalnetRoute(vpcTunnel))
		if err != nil {
			log.Log.Error(err, "Error exec genGlobalnetRoute in Initialized")
			return ctrl.Result{}, err
		}

		err = r.Status().Update(ctx, vpcTunnel)
		if err != nil {
			log.Log.Error(err, "Error Update vpcTunnel Status")
			return ctrl.Result{}, err
		}

		// add label to find tunnel
		labels := vpcTunnel.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels["localVpc"] = vpcTunnel.Spec.LocalVpc
		labels["remoteCluster"] = vpcTunnel.Spec.RemoteCluster
		labels["remoteVpc"] = vpcTunnel.Spec.RemoteVpc
		labels["localGateway"] = vpcTunnel.Spec.LocalGw
		vpcTunnel.Labels = labels

		err = r.Update(ctx, vpcTunnel)
		if err != nil {
			log.Log.Error(err, "Error Update vpcTunnel")
			return ctrl.Result{}, err
		}

		// 记录操作完成
		now := time.Now()
		fmt.Println(fmt.Sprintf("%d-%d-%d %d:%d:%d.%d   INFO   创建/更新 VpcNatTunnel 成功:%s", now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(), now.Nanosecond()/1e6, vpcTunnel.ObjectMeta.Name))

		// if ClusterId or GatewayName update, then gatewayExIp.Spec.ExternalIP or gatewayExIp.Spec.GlobalNetCIDR will update too
	} else if vpcTunnel.Status.Initialized && (vpcTunnel.Status.RemoteIP != remoteGatewayExIp.Spec.ExternalIP ||
		vpcTunnel.Status.InterfaceAddr != vpcTunnel.Spec.InterfaceAddr || vpcTunnel.Status.LocalGw != vpcTunnel.Spec.LocalGw ||
		vpcTunnel.Status.RemoteGlobalnetCIDR != remoteGatewayExIp.Spec.GlobalNetCIDR || vpcTunnel.Status.Type != vpcTunnel.Spec.Type) {
		// can't update type
		if vpcTunnel.Status.Type != vpcTunnel.Spec.Type {
			log.Log.Error(errors.New("tunnel type should not change"), fmt.Sprintf("tunnel :%#v\n", vpcTunnel))
			vpcTunnel.Spec.Type = vpcTunnel.Status.Type
			err := r.Update(ctx, vpcTunnel)
			if err != nil {
				log.Log.Error(err, "Error Update vpcTunnel")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}

		// if spec update, we should delete route and tunnel, then create again
		gwPod, err := r.getNatGwPod(vpcTunnel.Status.LocalGw) // find pod named Status.NatGwDp
		if err != nil {
			log.Log.Error(err, "Error get GwPod")
			return ctrl.Result{}, err
		}
		err = r.execCommandInPod(gwPod.Name, gwPod.Namespace, "vpc-nat-gw", genDelGlobalnetRoute(vpcTunnel))
		if err != nil {
			log.Log.Error(err, "Error exec genDelGlobalnetRoute in update")
			return ctrl.Result{}, err
		}
		err = r.execCommandInPod(gwPod.Name, gwPod.Namespace, "vpc-nat-gw", r.genDeleteTunnelCmd(vpcTunnel))
		if err != nil {
			log.Log.Error(err, "Error exec genDeleteTunnelCmd in update")
			return ctrl.Result{}, err
		}

		vpcTunnel.Status.RemoteIP = remoteGatewayExIp.Spec.ExternalIP
		vpcTunnel.Status.RemoteGlobalnetCIDR = remoteGatewayExIp.Spec.GlobalNetCIDR
		vpcTunnel.Status.InterfaceAddr = vpcTunnel.Spec.InterfaceAddr

		err = r.execCommandInPod(gwPod.Name, gwPod.Namespace, "vpc-nat-gw", r.genCreateTunnelCmd(vpcTunnel))
		if err != nil {
			log.Log.Error(err, "Error exec genCreateTunnelCmd in update")
			return ctrl.Result{}, err
		}
		err = r.execCommandInPod(gwPod.Name, gwPod.Namespace, "vpc-nat-gw", genGlobalnetRoute(vpcTunnel))
		if err != nil {
			log.Log.Error(err, "Error exec genGlobalnetRoute in update")
			return ctrl.Result{}, err
		}

		err = r.Status().Update(ctx, vpcTunnel)
		if err != nil {
			log.Log.Error(err, "Error Update vpcTunnel")
			return ctrl.Result{}, err
		}

		// if RemoteCluster , RemoteVpc or LocalVpc update, label need to update
		if vpcTunnel.Labels["remoteCluster"] != vpcTunnel.Spec.RemoteCluster || vpcTunnel.Labels["remoteVpc"] != vpcTunnel.Spec.RemoteVpc ||
			vpcTunnel.Labels["localVpc"] != vpcTunnel.Spec.LocalVpc || vpcTunnel.Labels["localGateway"] != vpcTunnel.Spec.LocalGw {
			vpcTunnel.Labels["remoteCluster"] = vpcTunnel.Spec.RemoteCluster
			vpcTunnel.Labels["remoteVpc"] = vpcTunnel.Spec.RemoteVpc
			vpcTunnel.Labels["localVpc"] = vpcTunnel.Spec.LocalVpc
			vpcTunnel.Labels["localGateway"] = vpcTunnel.Spec.LocalGw
			err := r.Update(ctx, vpcTunnel)
			if err != nil {
				log.Log.Error(err, "Error Update vpcTunnel")
				return ctrl.Result{}, err
			}
		}

		// 记录操作完成
		now := time.Now()
		fmt.Println(fmt.Sprintf("%d-%d-%d %d:%d:%d.%d   INFO   创建/更新 VpcNatTunnel 成功:%s", now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(), now.Nanosecond()/1e6, vpcTunnel.ObjectMeta.Name))
	}

	return ctrl.Result{}, nil
}

func (r *VpcNatTunnelReconciler) handleDelete(ctx context.Context, vpcTunnel *kubeovnv1.VpcNatTunnel) (ctrl.Result, error) {
	if containsString(vpcTunnel.ObjectMeta.Finalizers, "tunnel.finalizer.ustc.io") {

		// delete route and tunnel
		gwPod, err := r.getNatGwPod(vpcTunnel.Status.LocalGw)
		if err != nil {
			log.Log.Error(err, "Error get GwPod")
			return ctrl.Result{}, err
		}
		err = r.execCommandInPod(gwPod.Name, gwPod.Namespace, "vpc-nat-gw", genDelGlobalnetRoute(vpcTunnel))
		if err != nil {
			log.Log.Error(err, "Error exec genDelGlobalnetRoute in handleDelete")
			return ctrl.Result{}, err
		}
		err = r.execCommandInPod(gwPod.Name, gwPod.Namespace, "vpc-nat-gw", r.genDeleteTunnelCmd(vpcTunnel))
		if err != nil {
			log.Log.Error(err, "Error exec genDeleteTunnelCmd in handleDelete")
			return ctrl.Result{}, err
		}

		controllerutil.RemoveFinalizer(vpcTunnel, "tunnel.finalizer.ustc.io")
		err = r.Update(ctx, vpcTunnel)
		if err != nil {
			log.Log.Error(err, "Error Update vpcTunnel")
			return ctrl.Result{}, err
		}
		// 记录删除操作完成
		now := time.Now()
		fmt.Println(fmt.Sprintf("%d-%d-%d %d:%d:%d.%d   INFO   删除 VpcNatTunnel 成功:%s", now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(), now.Nanosecond()/1e6, vpcTunnel.ObjectMeta.Name))
	}
	return ctrl.Result{}, nil
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
