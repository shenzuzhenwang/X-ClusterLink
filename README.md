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
  name: ovn-gre0
  namespace: ns1
spec:
  clusterId: "cluster2" #互联的对端集群ID
  gatewayName: "gw1"    # 互联的对端集群 vpc-gw 名字
  interfaceAddr: "10.100.0.1/24" #隧道地址
  natGwDp: "gw1" #本集群 vpc-gw 名字
```

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
  name: gw1-cluster1    # 本端隧道的vpc-gw name + 本端集群的submariner cluster id
  namespace: kube-system
spec:
  externalip: 172.50.16.99    # 本端隧道的vpc-gw的物理网络IP
  globalnetcidr: 242.1.0.0/16    # 本端集群的submariner GlobalnetCIDR
```

其中，`externalip`代表本端隧道的vpc-gw的物理网络IP（net1网卡IP）；`globalnetcidr`代表本端集群的submariner GlobalnetCIDR；`name`生成时，会使用`vpc-gw name` - `cluster id`，以防多集群同步出现碰撞。

这个GatewayExIp crd会同步至broker，然后同步到各集群上，其他集群建立VpcNatTunnel 时，就可以通过指定`gatewayName`和`clusterId`来寻找对应的GatewayExIp，从而得到对端隧道的`externalip`和`globalnetcidr`，最后成功建立隧道。隧道的具体创建方式与前文一样。



## Directory Structure

* api: crd的定义
* bin: bin kubebuilder的一些编译工具
* cmd: main函数起始点nfig
* config: kubuilder生成的配置文件，包括CRD 定义文件、RBAC 角色配置文件等
* internal: 包括crd状态变更时的代码逻辑
* sample: crd的例子
* test: kubebuilder自带的测试，由于我们都是在集群上进行测试，因此未使用