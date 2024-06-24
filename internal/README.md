# Summary

internal code folder:

## controller

### vpcdnsforward_controller.go
`vpcdnsforward_controller.go` 基于 controller runtime pkg, `Reconcile` 函数在 VpcDnsForward CRD 发生改变时进行相关操作

- VpcDnsForward 发生创建/更新行为，触发 `handleCreateOrUpdate` 函数
- VpcDnsForward 发生删除行为，触发 `handleDelete` 函数

函数介绍
- `handleCreateOrUpdate`：若 VpcDnsForward 没有 Finalizer，说明 VpcDnsForward 是刚创建的，随即打上 Finalizer，然后进行 `createDnsConnection`；否则说明 VpcDnsForward 之前已经创建，进行 Update
- `handleDelete`：首先执行 deleteDnsConnection，删除DNS路由，然后去除 Finalizer，完成 VpcDnsForward 删除
- `createDnsConnection`：首先检查Dns Corefile 是否更新（`checkDnsCorefile`），若没有则执行更新（`updateDnsCorefile`），然后根据 VpcDnsForward 找到对应的 VpcDns Deployment，在其模板中添加 Dns 路由，更新 VpcDns Deployment

### vpcnattunnel_controller.go
`vpcnattunnel_controller.go` 基于 controller runtime pkg, `Reconcile` 函数在 VpcNatTunnel CRD 发生改变时进行相关操作

- VpcNatTunnel 发生创建/更新行为，触发 `handleCreateOrUpdate` 函数
- VpcNatTunnel 发生删除行为，触发 `handleDelete` 函数

函数介绍
- `handleCreateOrUpdate`：若 VpcDnsForward 没有 Finalizer，说明 VpcDnsForward 是刚创建的，随即打上 Finalizer，然后进行隧道创建；否则进行隧道更新
  
  隧道创建：获取 VpcNatTunnel 对应的对端 GatewayExIp crd，初始化 VpcDnsForward 的信息，在 VpcNatTunnel 对应的 Gateway pod中执行shell命令（execCommandInPod），建立隧道，最后更新 VpcNatTunnel
  
  隧道更新：若更新后的 VpcNatTunnel 的类别不同于之前，则返回不处理；首先执行 `execCommandInPod` 删除原先建立的隧道，再建立新的 VpcNatTunnel 对应的隧道，最后更新 VpcNatTunnel
- `handleDelete`：首先执行 `execCommandInPod` 删除原先建立的隧道，然后然后去除 Finalizer，完成 VpcNatTunnel 删除

### gatewayexip_controller.go
`gatewayexip_controller.go` 基于 Submariner admiral 的 syncer，用于实现本地集群和 broker 上 GatewayExIp CRD 的同步

- `InitEnvVars`：在整个程序启动时执行（cmd/main.go）,读取 Submariner ServiceDiscovery，设置系统环境变量，如 broker ApiServer 的 ip，ca等
- `NewGwExIpSyncer`：根据已有信息创建在本地集群和 broker上建立连接的 syncer，syncer 能对本集群和 broker 上 GatewayExIp 进行更改
- `onLocalGatewayExIp`：本地 GatewayExIp 更改时 syncer 更新到 broker 前修改在 broker 上所在的命名空间
- `onLocalGatewayExIpSynced`：本地 GatewayExIp 更改时 syncer 更新到 broker 后执行的操作，此处没有进行处理
- `onRemoteGatewayExIp`：broker GatewayExIp 更改时 syncer 更新到本地前修改 GatewayExIp 在本地集群上所在的命名空间
- `onRemoteGatewayExIpSynced`：broker GatewayExIp 更改时 syncer 更新到本地后找到与该 GatewayExIp 相关的 VpcNatTunnel，更新 VpcNatTunnel

### gateway_informer.go
`gateway_informer.go` 基于 k8s Informer，对 Vpc-Gateway StatefulSet 进行监视，对 StatefulSet 的行为会触发相应的操作逻辑

#### handle Gateway Add

在VPC下部署一个Vpc-Nat-Gateway时会触发Informer的Add Function，分为三种情况进行处理

- 当前VPC有与之对应的GatewayExIp且其指向的Gateway就是当前添加的网关，则根据当前Gateway更新GatewayExIp
- 当前VPC有与之对应的GatewayExIp但其指向的Gateway不是当前添加的网关，说明此时VPC已经有主网关，因此不做处理
- 当前VPC没有GatewayExIp，说明此时VPC下没有网关，则添加的网关成为主网关，创建对应的GatewayExIp

#### handle Gateway Update

Vpc-Nat-Gateway 宕掉或者重启时会触发Informer的Update Function，分为以下情况处理

- 若是网关宕掉，判断是不是主网关宕掉，若不是则不进行处理；若是则寻找可用的备用网关，选中备用网关作为新的主网关并进行以下操作
    - 将VPC的逻辑路由器的流量路径从之前主网关切换到当前网关
    - 根据当前网关的信息更新VPC对应的GatewayExIp
    - 根据与VPC相关的VpcNatTunnel在新的主网关建立隧道
- 若是网关重启，判断是不是主网关重启，若不是则不进行处理，若是则说明VPC是单网关，因为多网关下在网关宕掉时已经将主网关进行切换，完成以下操作
    -  根据当前网关的信息更新VPC对应的GatewayExIp
    -  根据与VPC相关的VpcNatTunnel在重启的主网关建立隧道

#### handle Gateway Delete

删除VPC中的一个Vpc-Nat-Gateway时会触发Informer的Delete Function，分为以下情况处理

- 判断是不是主网关宕掉，若不是则不进行处理；若是则寻找可用的备用网关，
    - 若没有可用的备用网关，说明删除前VPC为单网关，删除后VPC没有网关，因此删除GatewayExIp
    - 若发现可用的备用网关，完成以下操作
        - 将VPC的逻辑路由器的流量路径从之前主网关切换到当前网关
        - 根据当前网关的信息更新VPC对应的GatewayExIp
        - 根据与VPC相关的VpcNatTunnel在新的主网关建立隧道

## tunnel

工厂模式，仅暴露接口 interface.go

- gre：gre隧道的相关指令生成
- vxlan：vxlan 隧道的相关指令生成