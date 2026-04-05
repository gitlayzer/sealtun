# Sealtun

[English Version](./README_EN.md)

Sealtun 是一款功能强大、设计优雅的 CLI 工具，旨在为 **Sealos Cloud** 和 **Kubernetes** 用户提供类似 `cloudflared` 的内网穿透体验。

它通过动态调度 Kubernetes 资源（Deployments, Services, Ingresses），并利用双向多路复用 WebSocket 流（`yamux`）建立安全隧道，将你的本地开发环境直接暴露到公网。

## ✨ 特性

- 🔑 **无密码 OAuth2 登录**：使用设备授权流（Device Authorization Grant）通过 `sealtun login` 轻松连接。
- 🚀 **一键暴露服务**：执行 `sealtun expose 8080`，即可获得一个受信任的 HTTPS URL，将流量安全地路由到本地。
- 🌐 **深度适配 Higress & Sealos**：原生支持 Sealos Cloud 的域名后缀（`.app`）和 Higress 网关协议（WS/GRPC）。
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
```
*注：这会自动获取你的 Kubernetes 凭据并安全地存储在 `~/.sealtun` 目录中。*

### 2. 暴露本地端口
例如，让运行在本地 `3000` 端口的 Web 服务可以被公网访问：
```bash
# 默认使用 https 协议 (兼容 WebSocket)
sealtun expose 3000

# 暴露 gRPC 服务
sealtun expose 50051 --protocol grpcs
```

Sealtun 会自动执行以下操作：
1. 在你的 Sealos Namespace 中启动一个隧道代理 Pod。
2. 配置 Ingress 路由规则。
3. 建立加密 WebSocket 隧道，并将所有流量转发至 `localhost:3000`。

## 🛠️ 架构详情

- **底层协议**：基于 WebSocket 的 Yamux 多路复用。
- **Sealos 资源**：触发 `sealtun expose` 时，会在集群中创建以 `sealtun-*` 命名的 `Deployment`、`Service` 和 `Ingress`。
- **镜像来源**：依赖于 `ghcr.io/gitlayzer/sealtun` 的原生镜像。

## 📄 许可证

MIT License.
