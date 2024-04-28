# Summary

internal code folder:

#### controller

- vpcdnsforward_controller.go和vpcnattunnel_controller.go都是在crd发生改变时进行实际操作（pod内运行sh指令）的逻辑。基于controller runtime
- gateway_informer.go则是控制vpc-gw statefulset状态改变时的操作逻辑（待完善）

#### tunnel

工厂模式，仅暴露接口interface.go

- gre：gre隧道的相关指令生成
- vxlan：vxlan隧道的相关指令生成