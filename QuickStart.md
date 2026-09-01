# Sealtun QuickStart

[README](./README.md) · [English](./QuickStart_EN.md) · [Changelog](./CHANGELOG.md)

## 安装

```bash
npm install -g sealtun
```

其他方式：npx 临时运行（`npx sealtun@latest login`）、GitHub Releases 直接下载各平台二进制、源码 `make build`。

## 三步上手

```bash
sealtun login      # 1. 浏览器授权登录
sealtun up         # 2. 交互式创建隧道（自动发现本地端口）
sealtun list       # 3. 查看隧道
```

公网 URL 在创建输出中直接给出。

## 登录与账号

```bash
sealtun login gzg                      # 指定 region 登录
sealtun login gzg --profile gzg-main   # 保存为命名 profile
sealtun profile list / use / delete    # profile 管理
sealtun region list / current / use    # region 管理
sealtun status                         # 当前登录状态
```

内置 region：`gzg`（广州）、`hzh`（杭州）、`bja`（北京）、`cloud`（国际站）、`usw`（美西）。凭据保存在 `~/.sealtun`，profile 保存在 `~/.sealtun/profiles/<name>`。

## 创建隧道

**HTTPS（默认）**：本地端口或远端 HTTP upstream 转公网 URL。

```bash
sealtun expose 3000                              # 本地端口
sealtun expose --target http://10.0.0.12:8080    # 远端 HTTP upstream
sealtun up                                        # 交互引导（推荐日常使用）
```

**SSH**：`expose 22 --protocol ssh`，输出 `ssh <user>@<public-host> -p <node-port>`。

**通用 TCP**：`expose 5432 --protocol tcp`（数据库、队列、MQTT 等），输出 `<public-host>:<node-port>`。

SSH/TCP 走 NodePort 直连，不支持 HTTPS 的认证、域名和策略功能。

`up` 是 `expose` 的智能入口：复用项目 `.sealtun/state.json`、自动发现本地端口、支持 `--template`（https/ssh/tcp/mysql/postgres/redis/mongodb/mqtt）、`--guided` 强制引导。`--target` 仅 HTTPS；私有自签名 upstream 用 `--target-insecure-skip-verify`。

## 访问控制（仅 HTTPS）

```bash
# Basic Auth（推荐环境变量方式）
export SEALTUN_BASIC_AUTH_PASSWORD='change-me'
sealtun expose 3000 --basic-auth-user admin --basic-auth-password-env SEALTUN_BASIC_AUTH_PASSWORD

# Bearer Token
sealtun expose 3000 --bearer-token-env SEALTUN_BEARER_TOKEN

# IP 白/黑名单
sealtun expose 3000 --ip-allowlist 203.0.113.10 --ip-denylist 198.51.100.9

# 限流与审计
sealtun expose 3000 --rate-limit 60/m --audit
```

认证在 Sealtun server 代理层执行，只保护公网业务流量，不影响控制通道。token 只存 SHA-256 hash。

**临时分享链接**（创建后 URL 只显示一次）：

```bash
sealtun share create <tunnel-id> --name review --ttl 1h
sealtun share list / rotate / revoke <tunnel-id> review
```

**策略管理**：

```bash
sealtun policy show / set <tunnel-id> --rate-limit 60/m --audit
sealtun policy audit <tunnel-id> --since 10m
```

**轮换 server secret**：`sealtun rotate <tunnel-id> --server-secret`（新 secret 只显示一次）。

## 自定义域名（仅 HTTPS）

```bash
sealtun domain plan <tunnel-id> app.example.com   # 查看需要配置的 DNS
# 在你的 DNS 服务商处配置: CNAME app.example.com -> <sealos-host>
sealtun domain add <tunnel-id> app.example.com --wait   # 绑定并等待证书
sealtun domain verify / status / clear <tunnel-id>
```

CNAME 验证通过后才写入 Ingress 并创建 cert-manager 证书。`domain status --verbose` 输出详细 DNS/Ingress/证书诊断。

## 运维

```bash
sealtun list / --check / --watch                 # 列表、本地探活、持续刷新
sealtun inspect <id>                              # 单隧道详情
sealtun inspect <id> --remote                    # + 远端 K8s 诊断和事件
sealtun inspect <id> --metrics                   # + 指标（旧镜像降级为 warning）
sealtun inspect <id> --resources                 # + 资源清单（Secret 脱敏）
sealtun inspect <id> --watch                     # 持续巡检
sealtun logs <id> --tail 200 --follow            # 远端 Pod 日志
sealtun doctor [--fix --dry-run] [--fix]          # 诊断与保守修复
sealtun stop / start / cleanup <id>               # 停止（可恢复）/ 恢复 / 删除
```

## 声明式配置

```yaml
# sealtun.yaml
version: v1
tunnels:
  - name: web
    localPort: 3000
    protocol: https
    domain: app.example.com
    ttl: 2h
    basicAuth:
      credential: admin:change-me
    accessPolicy:
      bearerTokenEnv: SEALTUN_BEARER_TOKEN
      rateLimit: 60/m
      audit: { enabled: true }
    resources:
      requests: { cpu: 20m, memory: 64Mi }
      limits:   { cpu: 300m, memory: 256Mi }
```

```bash
sealtun apply -f sealtun.yaml --dry-run                 # 执行计划预览（无需登录）
sealtun apply -f sealtun.yaml --dry-run --format diff   # 与本地 session 的字段级对比
sealtun apply -f sealtun.yaml                            # 创建/更新
```

`name` 即稳定 tunnel ID，重复 apply 幂等更新。资源调整只能通过 YAML `resources` + apply，默认 requests `10m/32Mi`、limits `200m/128Mi`。

## 附录

**Codex Skill**：仓库内置 `skills/sealtun`，供 Codex 类 AI agent 理解 Sealtun CLI：`npx skills add https://github.com/gitlayzer/sealtun`。

**环境变量**：`SEALTUN_SERVER_IMAGE` 可覆盖远端 Pod 镜像（开发用）；`SEALTUN_HOME` 可覆盖 `~/.sealtun` 配置目录。

**详细排障**：见 `skills/sealtun/references/troubleshooting.md`。

## 许可证

MIT License.
