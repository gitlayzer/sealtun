# Sealtun QuickStart

[返回 README](./README.md) | [English QuickStart](./QuickStart_EN.md)

这里集中放置安装、登录、创建隧道、访问控制、自定义域名、TUI、集群访问、观测运维、协议模板和声明式配置用法。计费说明见 [README.md](./README.md#-计费说明)。

## 📦 安装

推荐通过 npm 安装 `sealtun` CLI，也可以直接从 GitHub Releases 下载对应平台的二进制；远端隧道 Pod 使用同版本的 `ghcr.io/gitlayzer/sealtun` 镜像。

开发或预发测试时可以用 `SEALTUN_SERVER_IMAGE` 覆盖远端 Pod 镜像；普通使用不需要设置。

使用 npm 全局安装：

```bash
npm install -g sealtun
sealtun --version
```

使用 npx 临时运行：

```bash
npx sealtun@latest --version
npx sealtun@latest login
```

npm 包会按当前系统自动安装对应平台的可选二进制依赖，当前支持 macOS、Linux、Windows 的 `amd64/x64` 与 `arm64`。

Windows 上如果使用 nvm-windows，或 Node/npm 安装在需要管理员权限的目录，全局安装可能因为 npm global prefix 不可写而失败。优先使用 `npx sealtun@latest --version` 临时运行，或把 npm 全局目录改到用户目录：

```powershell
npm config set prefix "$env:APPDATA\npm"
$env:PATH += ";$env:APPDATA\npm"
npm install -g sealtun
sealtun --version
```

如果提示找不到 `@gitlayzer/sealtun-win32-x64` / `@gitlayzer/sealtun-win32-arm64`，请确认没有使用 `--omit=optional`，也没有设置 `npm config set optional false`。仍然受限时，可使用下方 PowerShell 方式直接下载 GitHub Release 二进制。

macOS / Linux 快速安装：

```bash
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "unsupported arch: $ARCH" >&2; exit 1 ;;
esac

curl -L "https://github.com/gitlayzer/sealtun/releases/latest/download/sealtun_${OS}_${ARCH}.tar.gz" -o sealtun.tar.gz
tar -xzf sealtun.tar.gz sealtun
chmod +x sealtun
sudo mv sealtun /usr/local/bin/sealtun
sealtun --version
```

Windows PowerShell 快速下载：

```powershell
$arch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq "Arm64") { "arm64" } else { "amd64" }
Invoke-WebRequest -Uri "https://github.com/gitlayzer/sealtun/releases/latest/download/sealtun_windows_$arch.zip" -OutFile sealtun.zip
Expand-Archive .\sealtun.zip -DestinationPath .
.\sealtun.exe --version
```

从源码构建本地调试版本：

```bash
git clone https://github.com/gitlayzer/sealtun.git
cd sealtun
make build
./sealtun --version
```

## 🤖 Codex Skill

仓库内置了 `skills/sealtun`，用于让 Codex 类 AI agent 更准确地理解和使用 Sealtun CLI。这个 skill 会在用户提到 `sealtun`、`sealtun.yaml`、内网穿透、本地端口暴露、临时公网预览链接、第三方回调到本地、隧道访问控制、公网 SSH 或 TCP 隧道等场景时被动匹配。

skill 触发后会先判断是否真的属于“本地/dev 服务通过 Sealtun 暴露到公网”的范围，再按用法指导、实际操作或排障流程执行；没有明确要求时，不会擅自运行 `sealtun expose/apply/domain set/stop/cleanup/logout` 这类会改变本地或云端状态的命令。

推荐直接从仓库安装 skill：

```bash
npx skills add https://github.com/gitlayzer/sealtun
```

如果是在本仓库本地开发，也可以把该目录同步到 Codex 的全局技能目录：

```bash
mkdir -p ~/.codex/skills
cp -R skills/sealtun ~/.codex/skills/sealtun
```

## 🚀 快速上手

最短路径：

```bash
# 1. 登录 Sealos
sealtun login

# 2. 发现本地端口并生成推荐命令
sealtun init

# 3. 暴露本地 Web 服务，例如 localhost:3000
sealtun expose 3000

# 4. 查看状态、诊断和日志
sealtun list
sealtun doctor
sealtun logs <tunnel-id>

# 5. 停止、恢复或清理
sealtun stop <tunnel-id>
sealtun start <tunnel-id>
sealtun cleanup <tunnel-id>
```

偏交互式的日常管理可以直接打开终端控制台：

```bash
sealtun tui
# 等价别名
sealtun console
```

TUI 会复用现有 CLI 逻辑。默认焦点在左侧菜单，上下键切换 `Tunnels`、`Create`、`Tools`、`Status`；按 `enter`、`right` 或 `tab` 进入右侧内容，按 `left`、`esc` 或 `tab` 回到菜单。`Tunnels` 用来查看和选择隧道，选中后按 `o` 进入 `Tunnel Actions`，可以执行 inspect/doctor/logs/metrics/events/resources/export、domain/policy/share/resources/rotate/repair/start/stop/cleanup 等对象级操作；`Create` 用来从本地监听端口创建基础 HTTPS/SSH/TCP 隧道；`Tools` 只放全局操作，例如 status/discover/init/template/connect check/domain status/doctor dry-run/export all，以及 YAML apply dry-run/diff/apply。写操作都会先展示等价 CLI 命令并要求确认。`connect` 真实接管网络规则仍建议使用明确的 CLI 命令。

Linux 下访问集群内 Service/Pod：

```bash
sealtun connect --check
sudo sealtun connect
```

### 1. 登录到 Sealos
执行设备认证（类似于 `gh auth login`，无需手动输入密码）：
```bash
sealtun login

# 非交互脚本或想直接指定区域时
sealtun login gzg

# 查看支持的 region
sealtun region list

# 切换到指定 region
sealtun region use hzh

# 登录并保存为命名 profile
sealtun login gzg --profile gzg-main

# 查看和切换已保存 profile
sealtun profile list
sealtun profile use hzh-dev
```
内置 region：

| 名称 | Region API | Ingress 域名后缀 |
| --- | --- | --- |
| `gzg` | `https://gzg.sealos.run` | `sealosgzg.site` |
| `hzh` | `https://hzh.sealos.run` | `sealoshzh.site` |
| `bja` | `https://bja.sealos.run` | `sealosbja.site` |
| `cloud` | `https://cloud.sealos.io` | `cloud.sealos.io` |
| `usw` | `https://usw-1.sealos.io` | `usw-1.sealos.app` |

*注：目前仅支持内置的 Sealos Cloud region。交互终端里执行 `sealtun login` 会先用键盘选择 region；脚本、CI 或明确要固定区域时使用 `sealtun login <region>`。登录会获取 Kubernetes 凭据和当前 region 的 `SEALOS_DOMAIN`，并安全地存储在 `~/.sealtun` 目录中。命名 profile 会保存到 `~/.sealtun/profiles/<name>`，切换 profile 时会同步切换 active `auth.json` 与 `kubeconfig`。*

### 2. 暴露本地端口
例如，让运行在本地 `3000` 端口的 Web 服务可以被公网访问：
```bash
# 首次使用时可先生成推荐命令和 sealtun.yaml，不会创建资源
sealtun init
sealtun init --protocol auto --json

# 确认后再创建推荐隧道
sealtun init --apply

# 日常开发推荐：自动复用当前项目隧道；没有状态时进入完整创建引导
sealtun up
sealtun up --guided
sealtun up 3000
sealtun up --target https://10.0.0.12:8443 --insecure
sealtun up 3000 --rate-limit 60/m --audit

# 默认使用 https 协议 (兼容普通 HTTP 与 WebSocket 应用流量)
sealtun expose 3000

# 也可以把公网 HTTPS 入口转发到当前机器可访问的 HTTP upstream
sealtun expose --target http://10.0.0.12:8080

# 私有 HTTPS upstream 使用自签名证书时，显式关闭 upstream 证书校验
sealtun expose --target https://10.0.0.12:8443 --target-insecure-skip-verify

```

`sealtun up` 是 `expose` 的智能入口：它会优先复用当前目录 `.sealtun/state.json` 记录的 tunnel；没有状态且处于交互终端时，会按“登录检查 -> 选择端口 -> 选择协议 -> 是否加认证 -> 是否加限流 -> 是否加域名 -> 是否保存配置 -> 创建”的流程引导。非交互脚本或已明确端口/`--target` 时，`up` 直接调用同一套 `expose` 创建逻辑；也可以用 `--guided` 强制进入引导。`--target` 只适用于默认 HTTPS 隧道，目标必须是运行 Sealtun CLI 的机器可访问的 `http://` 或 `https://` 地址；SSH/TCP 四层隧道仍使用本地端口和 NodePort 模型。`--target-insecure-skip-verify`/`up --insecure` 只影响 Sealtun 客户端到 HTTPS upstream 的证书校验，默认关闭，仅建议用于私有网络内的自签名证书 upstream。

为公网业务流量启用 Basic Auth：
```bash
# 推荐：从环境变量读取密码，避免进入 shell history
export SEALTUN_BASIC_AUTH_PASSWORD='change-me'
sealtun expose 3000 --basic-auth-user admin --basic-auth-password-env SEALTUN_BASIC_AUTH_PASSWORD

# 也支持一次性写法
sealtun expose 3000 --basic-auth admin:change-me
```

Basic Auth 由 Sealtun server 代理层校验，不依赖 Ingress annotation；它只保护公网业务路径，不会拦截 `/_sealtun/ws` 隧道控制通道、健康检查或受内部 Bearer secret 保护的 metrics。

也可以启用不依赖 Ingress 的访问策略：
```bash
# Bearer Token
export SEALTUN_BEARER_TOKEN='share-secret'
sealtun expose 3000 --bearer-token-env SEALTUN_BEARER_TOKEN

# IP allowlist / denylist，支持单个 IP 或 CIDR
sealtun expose 3000 --ip-allowlist 203.0.113.10,198.51.100.0/24 --ip-denylist 198.51.100.9

# 临时访问链接，默认 1 小时后失效
export SEALTUN_TEMP_TOKEN='review-link-secret'
sealtun expose 3000 --temporary-access-token-env SEALTUN_TEMP_TOKEN --temporary-access-ttl 1h

# 限流和访问审计
sealtun expose 3000 --rate-limit 60/m --audit
```

Bearer Token 和临时链接 token 至少需要 8 个字符，只保存 SHA-256 hash，不会写入 Deployment 参数；临时链接使用 `?_sealtun_token=...` 访问，Sealtun 会在转发到本地服务或 `--target` upstream 前移除该查询参数。IP 规则优先使用 Ingress/代理传入的 `X-Real-IP`，再回退到 `X-Forwarded-For` 中最后一个有效的代理确认客户端 IP。Basic Auth 与 Bearer/临时链接同时配置时，任一认证方式通过即可访问。`--rate-limit` 使用固定窗口格式，例如 `60/m`、`1000/h`；访问审计只记录 allow/deny 原因、状态码、路径和客户端 IP，不记录 token 明文、Authorization header 或 Basic Auth 密码。

为已有 HTTPS 隧道创建、查看和撤销临时分享链接：
```bash
# 自动生成 token，默认 1 小时失效；URL 只会在创建时显示一次
sealtun share create <tunnel-id> --name review --ttl 1h

# 查看链接元数据，不会泄漏 token 明文
sealtun share list <tunnel-id>

# 轮换指定链接，旧 token 立即失效，新 URL 只显示一次
sealtun share rotate <tunnel-id> review --ttl 1h

# 撤销指定名称的分享链接
sealtun share revoke <tunnel-id> review
```

`share` 只适用于 HTTPS 隧道。SSH/TCP 四层入口没有 HTTP query token 认证层，因此不支持临时分享链接。

查看和更新 HTTPS 访问策略：
```bash
sealtun policy show <tunnel-id>
sealtun policy set <tunnel-id> --rate-limit 60/m --audit
sealtun policy set <tunnel-id> --clear-rate-limit
sealtun policy set <tunnel-id> --no-audit

# 查看最近 10 分钟访问审计
sealtun policy audit <tunnel-id> --since 10m
sealtun policy audit <tunnel-id> --since 10m --json
```

轮换隧道 server secret：
```bash
sealtun rotate <tunnel-id> --server-secret
```

新的 server secret 只在本次命令输出中显示一次，并会写回本地 session；远端隧道会滚动到新 secret。`policy`、`share`、`rotate` 都只对当前本地 session 记录对应的隧道生效，HTTPS 访问策略不会应用到 SSH/TCP NodePort 流量。

### 3. SSH 公网访问
如果 Sealos Region 支持公网 TCP NodePort，可以用四层 SSH 模式直接连接公网域名和端口：

```bash
# macOS/Linux 常见 SSH 端口是 22；也可以换成本机 sshd 监听的其他端口
sealtun expose 22 --protocol ssh
```

命令会输出公网 SSH 入口：
```bash
ssh <user>@<public-host> -p <node-port>
```

也可以写进 `~/.ssh/config`，之后直接 `ssh sealtun-dev`：
```sshconfig
Host sealtun-dev
  HostName <public-host>
  User <user>
  Port <node-port>
```

`--protocol ssh` 的公网业务入口只有 TCP NodePort，不会提供默认 HTTPS 业务 URL。Sealtun 仍会保留内部控制通道供本地 daemon 连接远端 Pod，但它不作为 SSH 隧道的用户访问入口。Basic Auth、Bearer Token、临时链接、IP 规则和自定义域名只适用于 HTTPS 隧道，不适用于 SSH 四层入口。旧的 WebSocket ProxyCommand 备用方式仍可用：

```bash
ssh -o ProxyCommand='sealtun ssh connect <tunnel-id>' <user>@sealtun
```

### 4. 通用 TCP 公网访问
除 SSH 外，也可以用通用四层 TCP 暴露本地数据库、调试服务或其他非 HTTP 协议：

```bash
sealtun expose 5432 --protocol tcp
```

命令会输出公网 TCP 入口：
```bash
<public-host>:<node-port>
```

`--protocol tcp` 和 `--protocol ssh` 一样走公网 TCP NodePort，只保留 HTTPS 控制通道供本地 daemon 连接远端 Pod，不提供默认 HTTPS 业务 URL。Basic Auth、Bearer Token、临时链接、IP 规则和自定义域名属于 HTTPS 代理层能力，不适用于 TCP 四层入口。

### 5. 使用自定义域名
新建隧道时先生成官方 Sealos 域名和 CNAME 目标：
```bash
sealtun expose 3000 --domain app.example.com

# 如果你会在命令等待期间配置 DNS，可以等待 CNAME 验证、绑定和证书就绪
sealtun expose 3000 --domain app.example.com --wait-domain
```

或者在 DNS 生效后对已有隧道绑定：
```bash
# 先查看需要配置的 DNS
sealtun domain plan <tunnel-id> app.example.com

# DNS 已经生效后绑定
sealtun domain set <tunnel-id> app.example.com

# 或者等待 DNS 生效后自动绑定，并继续等待证书就绪
sealtun domain add <tunnel-id> app.example.com --wait --timeout 5m
```

Sealtun 会保留一个 Sealos 官方子域名作为隧道控制面和 CNAME 目标。只有 CNAME 已经指向该 Sealos host 后，Sealtun 才会把自定义域名写入 Ingress，并创建 cert-manager `Issuer` 与 `Certificate`。你需要在自己的 DNS 服务商处配置：
```text
CNAME app.example.com -> <sealos-host>
```

验证 CNAME、Ingress 与证书状态：
```bash
sealtun domain verify <tunnel-id>

# 持续等待，直到 DNS 与证书就绪或超时
sealtun domain verify <tunnel-id> --wait --timeout 5m

# 汇总所有自定义域名状态
sealtun domain status

# 对单个域名做更详细诊断
sealtun domain doctor <tunnel-id>
```

移除自定义域名：
```bash
sealtun domain clear <tunnel-id>
```

### 6. 访问集群内服务
`sealtun expose` 是把本地服务暴露到公网；`sealtun connect` 则是反向能力：让本机工具直接访问当前 Sealos/Kubernetes namespace 内的 Service FQDN、Service ClusterIP 或 Pod IP。它只使用当前 active profile/region/namespace 的 Sealtun 登录态和 kubeconfig，不要求用户手动修改 RBAC。

Linux 当前支持透明 TCP 模式：Sealtun 会临时写入本机 `iptables` 和 `/etc/hosts`，把目标 TCP 连接转发到 Kubernetes `pods/portforward`。这不是 SOCKS/HTTP 代理，用户不需要给客户端配置代理。
```bash
sealtun connect --check
sealtun connect --check --json
sudo sealtun connect
sudo sealtun connect --namespace ns-3rgvtt74
```

`sealtun connect` 默认以前台进程运行。保持该终端打开，或在另一个终端执行 `sudo sealtun disconnect` 停止；正常 `Ctrl-C` 或 `disconnect` 会清理 Sealtun 写入的 `iptables` 规则和 `/etc/hosts` 管理块。

连接启动后可以直接访问：
```bash
curl http://my-service.ns-3rgvtt74.svc.cluster.local:8080
curl http://10.96.0.12:8080      # Service ClusterIP
curl http://10.244.0.22:3000     # Pod IP
```

查看或停止当前 cluster connect：
```bash
sealtun connect status
sealtun connect status --json
sudo sealtun disconnect
```

限制：当前透明数据面仅支持 Linux + TCP，需要 root 权限和 `iptables`；不支持 ICMP/ping 和 UDP。macOS/Windows 会明确提示暂不支持。

### 7. 观测和运维
查看远端隧道 Pod 日志：
```bash
sealtun logs <tunnel-id>
sealtun logs <tunnel-id> --tail 200
sealtun logs <tunnel-id> --follow
```

查看隧道指标：
```bash
sealtun metrics <tunnel-id>
sealtun metrics <tunnel-id> --json
```

查看远端 Kubernetes 事件：
```bash
sealtun events <tunnel-id>
sealtun events <tunnel-id> --json
```

发现本机监听端口，并用协议模板提示快速创建隧道：
```bash
sealtun discover
sealtun discover --protocol tcp
sealtun discover --json --limit 20
```

查看单条隧道的 Kubernetes 资源占用提示：
```bash
sealtun resources <tunnel-id>
sealtun resources <tunnel-id> --json
sealtun resources set <tunnel-id> --request-cpu 20m --request-memory 64Mi --limit-cpu 300m --limit-memory 256Mi
sealtun resources unset <tunnel-id>
```
默认远端 Pod 使用 `requests: cpu=10m memory=32Mi` 和 `limits: cpu=200m memory=128Mi`。`resources set` 会更新 Deployment 模板；如果隧道已经 stopped，不会启动 Pod，下一次 `start` 会沿用新资源配置。

一键诊断本地与远端状态：
```bash
# 全局健康检查
sealtun doctor

# 单条隧道诊断，会给出本地端口、daemon、远端资源和下一步建议
sealtun doctor <tunnel-id>
sealtun doctor <tunnel-id> --json
sealtun doctor <tunnel-id> --report
sealtun doctor <tunnel-id> --report --report-file ./doctor.md

# 只展示保守自动修复动作，不执行
sealtun doctor --fix --dry-run
sealtun repair <tunnel-id> --dry-run

# 执行低风险修复：恢复 stopped 隧道、清理 expired/stale 隧道、启动本地 daemon
sealtun doctor --fix
sealtun repair <tunnel-id>
```

`repair` 是单条隧道的安全修复入口，本质上复用 `doctor --fix` 的保守动作：恢复 stopped 隧道、清理 expired/stale 隧道或启动本地 daemon；默认不会执行 `cleanup --all`、不会删除 active 隧道、不会修改 DNS provider。`doctor <id> --report` 会生成脱敏 Markdown 排障报告，适合发给协作者或 issue，不会输出 token、secret、Authorization header、Basic Auth 密码或 kubeconfig。

实时观察隧道或全局状态：
```bash
sealtun watch
sealtun watch <tunnel-id>
sealtun watch <tunnel-id> --json
```

停止、恢复和清理隧道：
```bash
sealtun stop <tunnel-id>
sealtun start <tunnel-id>
sealtun cleanup
sealtun cleanup <tunnel-id>
sealtun cleanup --all
```

`stop` 会保留域名、Service、Ingress、NodePort Service 和本地 session，只把远端 Pod 副本缩到 0；`start` 会重新打开同一条隧道。默认 `cleanup` 只清理 stopped、expired、stale 或 error 隧道，`cleanup <tunnel-id>` 只清理指定的合格隧道；`cleanup --all` 会强制清理所有本地记录关联的远端资源，仅在明确想删除所有隧道时使用。

`metrics` 会聚合本地 session 状态、远端 Deployment/Pod/Ingress 状态，并在远端 Pod 支持时读取受 Bearer secret 保护的 `/_sealtun/metrics` 请求计数。TCP/SSH 四层隧道还会暴露 TCP 连接数、活跃连接数、字节数和错误数。

### 8. 协议模板
不确定该怎么写命令或声明式配置时，可以先生成模板：

```bash
sealtun template https --name web --port 3000 --domain app.example.com
sealtun template ssh
sealtun template postgres
sealtun template redis --name cache
sealtun template mongodb
```

模板会同时输出一次性 `sealtun expose` 命令和可提交到项目内的 `sealtun.yaml` 片段。`mysql`、`postgres`、`redis`、`mongodb`、`mqtt` 模板默认走通用 TCP 四层入口；HTTPS 模板才支持自定义域名和访问控制。

### 9. 声明式配置
创建 `sealtun.yaml`：
```yaml
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
      audit:
        enabled: true
      ipAllowlist:
        - 203.0.113.10
        - 198.51.100.0/24
      ipDenylist:
        - 198.51.100.9
      temporaryLinks:
        - name: review
          tokenEnv: SEALTUN_TEMP_TOKEN
          ttl: 1h
    waitDomain: false
    readyTimeout: 90s
    domainTimeout: 5m
```

远端 HTTP upstream 可以直接写 `target`，不需要本地监听端口：
```yaml
version: v1
tunnels:
  - name: upstream-api
    target: http://10.0.0.12:8080
    protocol: https
```

私有 HTTPS upstream 使用自签名证书时：
```yaml
version: v1
tunnels:
  - name: upstream-api
    target: https://10.0.0.12:8443
    protocol: https
    targetTls:
      insecureSkipVerify: true
    resources:
      requests:
        cpu: 20m
        memory: 64Mi
      limits:
        cpu: 300m
        memory: 256Mi
```

应用配置：
```bash
# 离线校验和预览，不需要登录
sealtun apply -f sealtun.yaml --dry-run

# 对比本地 session 与声明式配置
sealtun diff -f sealtun.yaml

# 创建或更新隧道
sealtun apply -f sealtun.yaml
```

从本地 session 反向导出声明式配置：
```bash
# 导出单条隧道到 stdout
sealtun export <tunnel-id>

# 导出所有本地 session
sealtun export --all -o sealtun.yaml

# 为已经启用 Basic Auth/Bearer/临时链接的隧道生成环境变量占位
sealtun export --all --include-secret-placeholders
```

`export` 不会输出已经 hash 后的密码或 token 明文。默认会保留可安全还原的字段，例如协议、本地端口、自定义域名、TTL、IP allowlist/denylist；如果需要把认证配置也写进 YAML，可以使用 `--include-secret-placeholders` 生成 `passwordEnv`、`bearerTokenEnv` 或 `tokenEnv` 占位，再由用户自己填入对应环境变量。

也可以使用展开的明文写法：
```yaml
basicAuth:
  username: admin
  password: change-me
```

或从环境变量读取密码：
```yaml
basicAuth:
  username: admin
  passwordEnv: SEALTUN_BASIC_AUTH_PASSWORD
```

`name` 会作为稳定 tunnel ID 使用，因此重复执行 `apply` 会更新同一个 `sealtun-<name>` 资源。`tunnels` 支持一次声明多条隧道；`target` 只支持 HTTPS 隧道，目标必须是 `http://` 或 `https://` URL，如果同时写 `localPort`，端口必须和 `target` 端口一致。`targetTls.insecureSkipVerify` 仅适用于 `https://` target，用于私有 upstream 自签名证书场景。`resources` 可声明远端 Pod 的 CPU/内存 requests 和 limits，未写字段会使用 Sealtun 默认值。`ttl` 会写入本地 session 的 `expiresAt`，本地 daemon 发现过期后会自动删除远端资源和本地记录。自定义域名仍然遵循 CNAME 先验证再绑定的规则；新隧道如果 CNAME 未就绪，`apply` 会先保留 Sealos 官方域名并输出后续 `domain set` 指令；已有隧道则会拒绝未验证的自定义域名变更，避免误清理或覆盖正在使用的域名配置。

## 📄 许可证

MIT License.
