# Summary

internal code folder:

## controller

### vpcdnsforward_controller.go
vpcdnsforward_controller.go 基于 controller runtime, Reconcile 函数在 VpcDnsForward CRD 发生改变时进行相关操作

- VpcDnsForward 发生创建/更新行为，触发 handleCreateOrUpdate 函数
- VpcDnsForward 发生删除行为，触发 handleDelete 函数

函数介绍
- handleCreateOrUpdate：若 VpcDnsForward 没有 Finalizer，说明 VpcDnsForward 是刚创建的，随即打上 Finalizer，然后进行 createDnsConnection；否则说明 VpcDnsForward 之前已经创建，进行 Update
- handleDelete：首先执行 deleteDnsConnection，删除DNS路由，然后去除 Finalizer，完成 VpcDnsForward 删除
- createDnsConnection：首先检查Dns Corefile 是否更新（checkDnsCorefile），若没有则执行更新（updateDnsCorefile），然后根据 VpcDnsForward 找到对应的 VpcDns Deployment，在其模板中添加 Dns 路由，更新 VpcDns Deployment

### vpcnattunnel_controller.go
vpcnattunnel_controller.go 基于 controller runtime, Reconcile 函数在 VpcNatTunnel CRD 发生改变时进行相关操作

- VpcNatTunnel 发生创建/更新行为，触发 handleCreateOrUpdate 函数
- VpcNatTunnel 发生删除行为，触发 handleDelete 函数

函数介绍
- handleCreateOrUpdate：若 VpcDnsForward 没有 Finalizer，说明 VpcDnsForward 是刚创建的，随即打上 Finalizer，然后进行隧道创建；否则进行隧道更新
  
  隧道创建：获取 VpcNatTunnel 对应的对端 GatewayExIp crd，初始化 VpcDnsForward 的信息，在 VpcNatTunnel 对应的 Gateway pod中执行shell命令（execCommandInPod），建立隧道，最后更新 VpcNatTunnel
  
  隧道更新：若更新后的 VpcNatTunnel 的类别不同于之前，则返回不处理；首先执行 execCommandInPod 删除原先建立的隧道，再建立新的 VpcNatTunnel 对应的隧道，最后更新 VpcNatTunnel
- handleDelete：首先执行 execCommandInPod 删除原先建立的隧道，然后然后去除 Finalizer，完成 VpcNatTunnel 删除

### gatewayexip_controller.go
gatewayexip_controller.go 基于 Submariner admiral 的 syncer，用于实现本地集群和 broker 上 GatewayExIp CRD 的同步
- InitEnvVars：在整个程序启动时执行（cmd/main.go）,读取 Submariner ServiceDiscovery，设置系统环境变量，如 broker ApiServer 的 ip，ca等
- NewGwExIpSyncer：根据已有信息创建在本地集群和 broker上建立连接的 syncer，syncer 能对本集群和 broker 上 GatewayExIp 进行更改
- onLocalGatewayExIp：本地 GatewayExIp 更改时 syncer 更新到 broker 前修改在 broker 上所在的命名空间
- onLocalGatewayExIpSynced：本地 GatewayExIp 更改时 syncer 更新到 broker 后执行的操作，此处没有进行处理
- onRemoteGatewayExIp：broker GatewayExIp 更改时 syncer 更新到本地前修改 GatewayExIp 在本地集群上所在的命名空间
- onRemoteGatewayExIpSynced：broker GatewayExIp 更改时 syncer 更新到本地后找到与该 GatewayExIp 相关的 VpcNatTunnel，更新 VpcNatTunnel

### gateway_informer.go
gateway_informer.go 基于 k8s Informer，对 Vpc-Gateway StatefulSet 进行监视，对 StatefulSet 的行为会触发相应的操作逻辑
- Vpc-Gateway StatefulSet 创建：创建（GatewayExIp 之前没有）/更新（GatewayExIp 之前存在） StatefulSet 下的 pod 的 GatewayExIp
- Vpc-Gateway StatefulSet 更新：若更新操作为 Vpc-Gateway 节点重启， 更新 Vpc-Gateway 对应的 GatewayExIp 和 相关的 VpcNatTunnel 状态
- Vpc-Gateway StatefulSet 删除：删除 Vpc-Gateway 对应的 GatewayExIp

## tunnel

工厂模式，仅暴露接口 interface.go

- gre：gre隧道的相关指令生成
- vxlan：vxlan 隧道的相关指令生成