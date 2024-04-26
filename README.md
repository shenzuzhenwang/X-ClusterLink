# kubeovn-multivpc

### operator 功能
- 在 kube-ovn vpc-dns dp 上自动创建和维护 Dns 转发
- 在 kube-ovn vpc 网关上自动创建和维护隧道
- 在本集群和 broker 之间维护 GatewayExIp 同步

## Getting Started

### 环境要求
- go version v1.21.0+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.


### GatewayExIp
```yaml
apiVersion: "kubeovn.ustc.io/v1"
kind: GatewayExIp
metadata:
  name: gw1-cluster1
  namespace: kube-system
spec:
  externalip: 172.50.16.99
```


### 测试 DNS 转发

新建 dns.yaml
```yaml
apiVersion: "kubeovn.ustc.io/v1"
kind: VpcDnsForward
metadata:
  name: dns-1
  namespace: ns1
spec:
  vpc: test-vpc-1

```
```sh
kubectl apply -f dns.yaml
```
登陆vpc-dns deployment, 可以看到路由已经转发

```sh
kubectl delete -f dns.yaml
```
登陆vpc-dns deployment, 可以看到路由已被删除

### 测试 网关隧道
新建 tunnel.yaml
```yaml
apiVersion: "kubeovn.ustc.io/v1"
kind: VpcNatTunnel
metadata:
  name: ovn-gre0
  namespace: ns1
spec:
  clusterId: "cluster2" #互联的对端集群ID
  gatewayId: "gw1"    # 互联的对端集群 vpc-gw 名字
  interfaceAddr: "10.100.0.1/24" #隧道地址
  natGwDp: "gw1" #本集群 vpc-gw 名字
  remoteGlobalnetCIDR: "242.0.0.0/16"
```

```sh
kubectl apply -f tunnel.yaml
```
登陆vpc网关pod，可以观察到隧道创建
```sh
kubectl delete -f tunnel.yaml
```
登陆vpc网关pod,可以观察到隧道被删除