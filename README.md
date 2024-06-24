# kubeovn-multivpc

### operator 功能
- 在 kube-ovn vpc-dns dp 上自动创建和维护 Dns 转发
- 在 kube-ovn vpc 网关上自动创建和维护隧道
- 在本集群和 broker 之间维护 GatewayExIp 同步

## Build

### 环境要求
- go version v1.21.0+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### 构建镜像

**通过以下命令，手动构建和同岁镜像。（`IMG`为构建的镜像名）:**

```
make docker-build docker-push  #到远程仓库
```

### 生成yaml文件

```
make deploy
```

## Getting Started

### 部署（k8s集群上）

```
kubectl apply -f deploy.yaml
```

以下即为部署成功：

```
sdn@server02:~$ kubectl get pod -A
multi-vpc-system      multi-vpc-controller-manager-7cf6c6b9d6-q4sq5    2/2     Running            0               73s
```

### 更改broker的RBAC

在部署broker的集群上更改RBAC（目的是通过broker同步gatewayexips）

```YAML
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: submariner-k8s-broker-cluster
  namespace: submariner-k8s-broker
rules:
  - apiGroups:
      - submariner.io
    resources:
      - clusters
      - endpoints
    verbs:
      - create
      - get
      - list
      - watch
      - patch
      - update
      - delete
  - apiGroups:
      - submariner.io
    resources:
      - brokers
    verbs:
      - get
      - list
  - apiGroups:
      - multicluster.x-k8s.io
    resources:
      - '*'
    verbs:
      - create
      - get
      - list
      - watch
      - patch
      - update
      - delete
  - apiGroups:
      - discovery.k8s.io
    resources:
      - endpointslices
      - endpointslices/restricted
    verbs:
      - create
      - get
      - list
      - watch
      - patch
      - update
      - delete
  - apiGroups:
      - ""
    resources:
      - secrets
    verbs:
      - get
      - list
      - watch
  - apiGroups:
      - ""
    resources:
      - serviceaccounts
    verbs:
      - get
      - list
  - apiGroups:
      - kubeovn.ustc.io
    resources:
      - gatewayexips
    verbs:
      - create
      - get
      - list
      - watch
      - patch
      - update
      - delete
```

### 使用 DNS 转发

新建 dns.yaml
```yaml
apiVersion: "kubeovn.ustc.io/v1"
kind: VpcDnsForward
metadata:
  name: dns-1
  namespace: ns1
spec:
  vpc: test-vpc-1	 # 需要更改路由的VPC，其中应该部署了vpc-dns
```
```sh
kubectl apply -f dns.yaml
```
查看vpc-dns deployment, 可以看到路由已经转发

```sh
sdn@server10:~$ kubectl describe deployment vpc-dns-test-dns1 -n kube-system
Pod Template:
  Labels:           k8s-app=vpc-dns-test-dns1
  Annotations:      k8s.v1.cni.cncf.io/networks: default/ovn-nad
                    ovn-nad.default.ovn.kubernetes.io/logical_switch: ovn-default
                    ovn.kubernetes.io/logical_switch: vpc2-net1
  Service Account:  vpc-dns
  Init Containers:
   init-route:
    Image:      docker.io/kubeovn/vpc-nat-gateway:v1.12.8
    Port:       <none>
    Host Port:  <none>
    Command:
      sh
      -c
      ip -4 route add 10.96.0.1 via 10.244.0.1 dev net1;ip -4 route add 218.2.2.2 via 10.244.0.1 dev net1;ip -4 route add 114.114.114.114 via 10.244.0.1 dev net1;ip -4 route add 10.96.0.10 via 10.244.0.1 dev net1;
```



```sh
kubectl delete -f dns.yaml
```
查看vpc-dns deployment, 可以看到路由已被删除

```sh
sdn@server10:~$ kubectl describe deployment vpc-dns-test-dns1 -n kube-system
Pod Template:
  Labels:           k8s-app=vpc-dns-test-dns1
  Annotations:      k8s.v1.cni.cncf.io/networks: default/ovn-nad
                    ovn-nad.default.ovn.kubernetes.io/logical_switch: ovn-default
                    ovn.kubernetes.io/logical_switch: vpc2-net1
  Service Account:  vpc-dns
  Init Containers:
   init-route:
    Image:      docker.io/kubeovn/vpc-nat-gateway:v1.12.8
    Port:       <none>
    Host Port:  <none>
    Command:
      sh
      -c
      ip -4 route add 10.96.0.1 via 10.244.0.1 dev net1;ip -4 route add 218.2.2.2 via 10.244.0.1 dev net1;ip -4 route add 114.114.114.114 via 10.244.0.1 dev net1;
```



### 使用 网关隧道
新建 tunnel.yaml
```yaml
apiVersion: "kubeovn.ustc.io/v1"
kind: VpcNatTunnel
metadata:
  name: gre1
  namespace: ns2
spec:
  remoteCluster: "cluster2"      #对端集群
  remoteVpc: "test-vpc-2"        #对端集群 vpc
  interfaceAddr: "10.100.0.1/24" #隧道地址
  localVpc: "test-vpc-2"         #本集群 vpc
  type: "vxlan" #隧道类型，或"gre"，默认为"gre"
```
其中，`interfaceAddr`代表本端隧道的IP地址；`localVpc`需要指定隧道本端的vpc的名称；`type`可以使用VXLAN和GRE两种；`remoteCluster`指的是隧道对端集群的Submariner cluster id；`remoteVpc`指的是隧道对端的 vpc名称，可以与本端的一样；其中，本端的GlobalnetCIDR和vpc-gateway的物理网络IP(GatewayExIp CRD)都可以自动获取，而对端的则可以从broker上同步过来。
```sh
kubectl apply -f tunnel.yaml
```
进入vpc网关pod，可以观察到隧道创建

```sh
sdn@server10:~$ kubectl exec -it -n kube-system vpc-nat-gw-vpc2-net1-gateway-0 -- /bin/sh
/kube-ovn # ifconfig
ovn-gre0  Link encap:Ethernet  HWaddr 7E:9F:E9:59:81:01
          inet addr:10.0.0.1  Bcast:0.0.0.0  Mask:255.255.255.0
          inet6 addr: fe80::7c9f:e9ff:fe59:8101/64 Scope:Link
          UP BROADCAST RUNNING MULTICAST  MTU:1450  Metric:1
          RX packets:0 errors:0 dropped:0 overruns:0 frame:0
          TX packets:9 errors:0 dropped:0 overruns:0 carrier:0
          collisions:0 txqueuelen:1000
          RX bytes:0 (0.0 B)  TX bytes:600 (600.0 B)
/kube-ovn # ip route
10.0.0.0/24 dev ovn-gre0 proto kernel scope link src 10.0.0.1
242.0.0.0/16 via 10.0.1.1 dev eth0
242.1.0.0/16 dev ovn-gre0 scope link
/kube-ovn # iptables -t nat -L POSTROUTING
SNAT       all  --  anywhere             242.1.0.0/16         to:242.0.0.1-242.0.0.8
```



```sh
kubectl delete -f tunnel.yaml
```
进入vpc网关pod，可以观察到以上内容均被删除





### GatewayExIp

这个crd是自动生成的，无需手动创建或更改

```yaml
apiVersion: "kubeovn.ustc.io/v1"
kind: GatewayExIp
metadata:
  name: test-vpc-2.cluster1
  namespace: ns2
spec:
  externalip: 172.50.16.99
  globalnetcidr: 242.1.0.0/16    # 本端集群的submariner GlobalnetCIDR
```

其中，`externalip`代表本端隧道的vpc-gw的物理网络IP（net1网卡IP）；`globalnetcidr`代表本端集群的submariner GlobalnetCIDR；`name`生成时，会使用`vpc name` - `cluster id`，以防多集群同步出现碰撞。

这个GatewayExIp crd会同步至broker，然后同步到各集群上，其他集群建立VpcNatTunnel 时，就可以通过指定`gatewayName`和`clusterId`来寻找对应的GatewayExIp，从而得到对端隧道的`externalip`和`globalnetcidr`，最后成功建立隧道。隧道的具体创建方式与前文一样。