<p align="center">
  <img src="./assets/sealtun-logo.svg.png" alt="Sealtun logo" width="220">
</p>

# Sealtun

[English Version](./README_EN.md)

Sealtun 是一款面向 **Sealos Cloud** 和 **Kubernetes** 用户的本地隧道 CLI。它可以把本地 Web、远端 HTTP upstream、SSH、数据库或调试服务快速暴露到公网。

> 快速安装、登录、创建隧道、访问控制、自定义域名、运维和声明式配置，请看 [QuickStart.md](./QuickStart.md)。

## ✨ 特性

- 🔑 **无密码 OAuth2 登录**：使用设备授权流（Device Authorization Grant）通过 `sealtun login` 轻松连接。
- 🌍 **区域切换**：支持查看已内置的 Sealos Cloud region，并通过 `sealtun region use` 重新登录切换区域。
- 👤 **Profile 多账号管理**：可把不同 Sealos 账号、region、workspace 和 kubeconfig 保存为命名 profile，按需切换。
- 🚀 **一键暴露服务**：执行 `sealtun expose 8080` 或 `sealtun expose --target http://10.0.0.12:8080`，即可获得一个受信任的 HTTPS URL。
- 🌐 **自定义域名自动化**：可用 `domain plan/add/verify/status/doctor` 生成 CNAME 指引、等待 DNS、绑定域名并检查证书状态。
- 🔗 **临时分享链接与轮换**：可用 `share create/list/revoke/rotate` 为 HTTPS 隧道生成、废弃或轮换自动失效的访问链接。
- 🛡️ **安全运营**：HTTPS 隧道支持 Basic Auth、Bearer Token、临时链接、IP 规则、rate limit、访问审计和 server secret 轮换。
- 📊 **状态与诊断**：`doctor <tunnel-id>`、`inspect --remote/--metrics/--resources`、`logs` 和 `list --check` 可定位本地端口、daemon、远端 Pod、Service、Ingress 与证书问题。
- 🧭 **引导与自动修复**：`up` 提供日常创建引导（含本地端口发现与协议模板）；`doctor --fix --dry-run` 可先预览再保守修复隧道状态。
- 👀 **实时观察**：`list --watch` 和 `inspect --watch` 可持续刷新隧道状态，支持 `--interval` 和 `--count` 控制。
- 🧾 **声明式配置**：`apply -f sealtun.yaml` 可用 YAML 声明隧道（含资源 requests/limits），并以稳定名称幂等创建或更新；`diff` 可先比较本地 session 与期望配置。
- 🌐 **深度适配 Sealos**：原生使用 Sealos Cloud 的域名、证书和 Kubernetes 资源能力。

## 💰 计费说明
Sealtun 本身不额外单独收取一笔“软件费”，实际成本来自 Sealos Cloud 为隧道远端 Pod 和公网入口分配的云资源。CLI 目前会在 `inspect --resources` 里展示资源占用提示，但那不是账单估算；真实费用仍以 Sealos Cloud 控制台的计费表和账单为准。

结合当前 Sealos Cloud 价格页，你至少可以按下面几个维度理解 Sealtun 隧道成本：

- `CPU`：按核 / 小时计费
- `内存`：按 GB / 小时计费
- `端口`：按个 / 小时计费
- `网络`：按流量计费

Sealtun 的常见成本来源是：

- 一个远端 tunnel Pod：消耗 `CPU + 内存`
- HTTP/HTTPS 隧道和 TCP/SSH NodePort 暴露：会占用 `端口`
- 公网访问流量：会产生 `网络` 成本

不同区域单价不同。根据当前 Sealos Cloud 控制台截图，`杭州 H`、`新加坡 B`、`北京 A`、`广州 G`、`US West` 的 CPU / 内存 / 端口价格都不一样，所以同样一条隧道在不同区域的小时成本会有差异。

当前可直接参考下面这张小时单价表：

| 区域 | CPU (核/小时) | 内存 (GB/小时) | 端口 (个/小时) | 网络 |
| --- | ---: | ---: | ---: | ---: |
| `杭州 H` | `0.027671` | `0.013956` | `0.013900` | `0.000781 /M` |
| `新加坡 B` | `0.067000` | `0.033792` | `0.013900` | `0.000781 /M` |
| `北京 A` | `0.017125` | `0.008637` | `0.007000` | `0.000781 /M` |
| `广州 G` | `0.017420` | `0.008786` | `0.007000` | `0.000781 /M` |
| `US West` | `0.020833` | `0.012500` | `0.006944` | `0.000107 /M` |

如果只做一个最小 HTTPS 隧道，通常至少会涉及：

- 1 个远端 Pod 的 `CPU + 内存`
- 1 个公网入口端口
- 实际产生的公网流量

估算时可以用这个思路：

```text
总成本 ~= Pod(CPU + 内存) + 公网端口 + 网络流量
```

如果你要压低成本，优先考虑：

- 关闭不用的隧道，或用 `sealtun stop` 把副本缩容为 0
- 在 `sealtun.yaml` 的 `resources` 里调低 Pod requests / limits 后重新 `apply`
- 避免给短时调试隧道分配过高资源
- 对低流量场景优先按需开启，而不是长期常驻
