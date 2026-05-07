# Sealtun

[English Version](./README_EN.md)

Sealtun 是一款功能强大、设计优雅的 CLI 工具，旨在为 **Sealos Cloud** 和 **Kubernetes** 用户提供类似 `cloudflared` 的内网穿透体验。

它通过动态调度 Kubernetes 资源（Deployments, Services, Ingresses），并利用双向多路复用 WebSocket 流（`yamux`）建立安全隧道，将你的本地开发环境直接暴露到公网。

## ✨ 特性

- 🔑 **无密码 OAuth2 登录**：使用设备授权流（Device Authorization Grant）通过 `sealtun login` 轻松连接。
- 🌍 **区域切换**：支持查看已内置的 Sealos Cloud region，并通过 `sealtun region use` 重新登录切换区域。
- 🚀 **一键暴露服务**：执行 `sealtun expose 8080`，即可获得一个受信任的 HTTPS URL，将流量安全地路由到本地。
- 🌐 **深度适配 Sealos**：原生使用 Sealos Cloud 的 Kubernetes、Service 与 Ingress 能力，当前稳定支持 HTTPS 入口和 WebSocket 隧道。
- 🐳 **全能二进制文件**：客户端和服务器代理共用同一个精简的二进制文件和 Docker 镜像。
- ☸️ **云原生设计**：完全使用标准的 Kubernetes API 管理资源，无需额外的复杂中间件。

## 📦 安装

你可以从源码构建 Sealtun CLI：

```bash
git clone https://github.com/gitlayzer/sealtun.git
cd sealtun
go build -o sealtun main.go
```

## 🚀 快速上手

### 1. 登录到 Sealos
执行设备认证（类似于 `gh auth login`，无需手动输入密码）：
```bash
sealtun login

# 查看支持的 region
sealtun region list

# 切换到指定 region
sealtun region use hzh
```
*注：目前仅支持内置的 Sealos Cloud region。登录会获取 Kubernetes 凭据和当前 region 的 `SEALOS_DOMAIN`，并安全地存储在 `~/.sealtun` 目录中。*

### 2. 暴露本地端口
例如，让运行在本地 `3000` 端口的 Web 服务可以被公网访问：
```bash
# 默认使用 https 协议 (兼容普通 HTTP 与 WebSocket 应用流量)
sealtun expose 3000

```

Sealtun 会自动执行以下操作：
1. 在你的 Sealos Namespace 中启动一个隧道代理 Pod。
2. 配置 Ingress 路由规则。
3. 建立加密 WebSocket 隧道，并将所有流量转发至 `localhost:3000`。

## 🛠️ 架构详情

- **底层协议**：基于 WebSocket 的 Yamux 多路复用。
- **Sealos 资源**：触发 `sealtun expose` 时，会在集群中创建以 `sealtun-*` 命名的 `Deployment`、`Service` 和 `Ingress`。
- **镜像来源**：依赖于 `ghcr.io/gitlayzer/sealtun` 的原生镜像。

## 🔧 当前已补强

- `expose` 现在会校验端口与协议参数，非法输入会在本地直接报错。
- `expose` 默认交给本地 daemon 后台维护；需要阻塞在当前终端时可使用 `--foreground`。
- 远端隧道 Pod 等待阶段增加了默认 `90s` 超时，可通过 `--ready-timeout` 调整。
- 配置目录统一为 `~/.sealtun`，首次运行只会迁移旧 `~/.sealos` 下的登录凭据和 kubeconfig，不会迁移旧 session 记录。
- Ingress 域名优先使用 Sealos Launchpad 返回的 `SEALOS_DOMAIN`，避免按 region 猜测公网域名。
- 提供 `status`、`list`、`inspect`、`doctor`、`stop`、`cleanup`、`logout` 等本地控制命令。
- `list` 默认只读取本地 session；需要探测本地端口健康时可使用 `list --check`。
- `inspect` 默认展示本地状态；需要远端 Kubernetes 诊断时可使用 `inspect --remote`。
- `logout` 会先回收本地记录中的隧道资源再删除凭据；如果只想强制清除本地凭据，可使用 `logout --force`。
- 当前 `--protocol` 只接受 `https`。TCP、UDP 和 gRPC 泛化暂不支持，后续如果需要会以单独能力设计，而不是继续复用当前 HTTP Ingress 路径。
- `doctor` 会汇总本地 daemon、登录、session、端口健康和远端 Deployment、Service、Ingress、Pod 与 Event 状态，用于定位镜像拉取、Pod 未就绪、Ingress 缺失等问题。

## 📄 许可证

MIT License.
