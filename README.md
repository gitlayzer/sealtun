<p align="center">
  <img src="./assets/sealtun-logo.svg.png" alt="Sealtun logo" width="220">
</p>

# Sealtun

[English](./README_EN.md) · [QuickStart](./QuickStart.md) · [Changelog](./CHANGELOG.md)

面向 **Sealos Cloud** 和 **Kubernetes** 用户的本地隧道 CLI。一条命令把本地 Web 服务、远端 HTTP upstream、SSH、数据库或调试服务暴露到公网。

```bash
sealtun login
sealtun expose 3000
# => https://sealtun-xxxx-ns-xxxx.sealosgzg.site
```

## 特性

- **HTTPS 隧道**：Ingress + 反向代理，支持 Basic Auth、Bearer Token、临时分享链接、IP 规则、限流、审计和自定义域名
- **SSH/TCP 隧道**：`expose 22 --protocol ssh`、`expose 5432 --protocol tcp`，NodePort 直连
- **智能入口**：`up` 自动发现本地端口、引导配置、复用项目隧道
- **声明式管理**：`apply -f sealtun.yaml` 幂等创建/更新多隧道，支持 dry-run 和 diff 预览
- **诊断运维**：`inspect --remote/--metrics/--resources`、`list --check/--watch`、`doctor --fix`、`logs`
- **账号体系**：OAuth 设备流登录、多 region、命名 profile

## 快速开始

```bash
npm install -g sealtun
sealtun login          # 浏览器完成设备授权
sealtun up             # 交互式创建第一条隧道
```

详细安装方式、访问控制、自定义域名、SSH/TCP、声明式配置见 [QuickStart.md](./QuickStart.md)。

## 成本说明

Sealtun 本身不收软件费。成本来自 Sealos Cloud 为隧道分配的 Pod（CPU + 内存）、公网端口和网络流量，按小时计费，各 region 单价见 Sealos Cloud 控制台价格页。

压低成本：`stop` 不用的隧道缩容到 0、YAML `resources` 调低 requests/limits、短时调试隧道按需开启。

## 许可证

MIT License.
