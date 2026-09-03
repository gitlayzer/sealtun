document.documentElement.classList.add("js");

const translations = {
  zh: {
    metaTitle: "Sealtun | 一条命令，把本地服务送上公网",
    metaDescription: "Sealtun 是 Sealos Cloud 原生的隧道 CLI：一条命令把本地 Web、SSH、数据库暴露到公网，内置认证、域名、限流、审计和声明式 YAML。",
    skip: "跳到正文",
    "nav.features": "核心能力",
    "nav.quickstart": "快速上手",
    "nav.download": "下载",
    "nav.cta": "立即下载",
    "hero.eyebrow": "Sealos Cloud 原生隧道 CLI",
    "hero.title": "一条命令，<br>把本地服务送上公网。",
    "hero.lead": "Sealtun 把本地 Web、SSH、数据库变成公网入口。认证、域名、限流、审计、声明式 YAML，全在这一个二进制里。",
    "hero.installCmd": "npm install -g sealtun",
    "hero.cardNpm.title": "npm 安装",
    "hero.cardNpm.desc": "macOS / Linux / Windows",
    "hero.cardBin.title": "独立二进制",
    "hero.cardBin.desc": "GitHub Releases · 6 平台",
    "hero.cardOci.title": "容器镜像",
    "hero.cardOci.desc": "ghcr.io/gitlayzer/sealtun",
    "hero.github": "在 GitHub 上查看",
    "hero.license": "MIT 开源协议",
    "copy.default": "复制",
    "copy.done": "已复制",
    "features.eyebrow": "FEATURES · 核心能力",
    "features.title": "公网入口这件事，做到极致。",
    "features.lead": "不是功能堆叠，而是把「本地服务公网化」的每一步都做成一条命令。",
    "f.https.title": "一条命令，拿到公网 HTTPS URL",
    "f.https.body": "expose 一个本地端口或远端 upstream，Ingress + 反向代理自动就绪。Basic Auth、Bearer Token、IP 白名单、限流、审计、自定义域名和 cert-manager 证书，全部是内置 flag，不需要你写一行 Ingress 配置。",
    "f.tcp.title": "SSH 和数据库，NodePort 直连",
    "f.tcp.body": "expose 22 --protocol ssh 暴露本机 sshd；expose 5432 --protocol tcp 暴露 Postgres、Redis、MQTT。四层 TCP 直通，不走 HTTP 代理。",
    "f.up.title": "会自己找端口的 up",
    "f.up.body": "sealtun up 自动发现本机监听端口，按协议模板引导创建：Web、SSH、MySQL、Postgres、Redis、MongoDB、MQTT。日常开发一条命令就够。",
    "f.policy.title": "入口安全，做成开关",
    "f.policy.body": "临时分享链接只显示一次、服务端 secret 可轮换、IP 白/黑名单、固定窗口限流、访问审计——策略在服务端代理层强制执行，不靠 Ingress 注解。",
    "f.yaml.title": "把隧道写成 YAML",
    "f.yaml.body": "sealtun.yaml 声明多条隧道：协议、端口、域名、TTL、资源规格、认证策略。apply 幂等创建/更新，--dry-run 预览计划，--format diff 对比变更，失败自动回滚。",
    "f.ops.title": "状态看得见，故障修得动",
    "f.ops.body": "inspect 看单隧道详情（--remote 远端诊断、--metrics 指标、--resources 资源清单），doctor 一键诊断并保守修复，logs 实时日志，list --watch 持续刷新。出问题时不是黑盒。",
    "qs.eyebrow": "QUICKSTART · 快速上手",
    "qs.title": "三步，从安装到公网。",
    "qs.s1.title": "安装并登录",
    "qs.s1.body": "npm 安装后 sealtun login，浏览器里完成设备授权，凭据保存在 ~/.sealtun。支持多 region、多 profile。",
    "qs.s2.title": "暴露本地服务",
    "qs.s2.body": "up 引导创建，或 expose 精确指定。HTTPS 出 URL，SSH/TCP 出 host:port。后台 daemon 常驻，关掉终端也不断线。",
    "qs.s3.title": "治理与分享",
    "qs.s3.body": "加认证、绑域名、开限流审计，或生成一小时后自动失效的临时分享链接发给同事。全部一条命令。",
    "dl.eyebrow": "DOWNLOAD · 下载",
    "dl.title": "选择你的安装方式。",
    "dl.lead": "所有渠道始终指向最新 release。一个二进制，无运行时依赖。",
    "dl.npm.title": "npm（推荐）",
    "dl.npm.body": "自动匹配平台二进制，支持 macOS / Linux / Windows 的 x64 与 arm64。",
    "dl.bin.title": "GitHub Releases",
    "dl.bin.body": "下载对应平台的 tar.gz / zip 单文件。也可 npx sealtun@latest 免安装运行。",
    "dl.oci.title": "容器镜像",
    "dl.oci.body": "远端隧道 Pod 与 CLI 同源镜像，ghcr.io/gitlayzer/sealtun:latest。",
    "dl.alpha": "每次 master 提交自动发布 v*-alpha-N 预发布版，可在 GitHub Releases 的 Pre-release 中抢先体验。",
    "footer.tagline": "Sealos Cloud 原生隧道 CLI",
    "footer.releases": "发布页",
    "footer.docs": "文档",
    "footer.built": "Built for the Sealos ecosystem",
  },
  en: {
    metaTitle: "Sealtun | One command, localhost goes public",
    metaDescription: "Sealtun is a Sealos Cloud-native tunnel CLI: one command publishes local web apps, SSH, and databases to the internet, with built-in auth, domains, rate limiting, audit, and declarative YAML.",
    skip: "Skip to content",
    "nav.features": "Features",
    "nav.quickstart": "Quickstart",
    "nav.download": "Download",
    "nav.cta": "Download",
    "hero.eyebrow": "Sealos Cloud-native tunnel CLI",
    "hero.title": "One command,<br>localhost goes public.",
    "hero.lead": "Sealtun turns local web apps, SSH, and databases into public endpoints. Auth, domains, rate limiting, audit, and declarative YAML — all in a single binary.",
    "hero.installCmd": "npm install -g sealtun",
    "hero.cardNpm.title": "Install with npm",
    "hero.cardNpm.desc": "macOS / Linux / Windows",
    "hero.cardBin.title": "Standalone binary",
    "hero.cardBin.desc": "GitHub Releases · 6 platforms",
    "hero.cardOci.title": "Container image",
    "hero.cardOci.desc": "ghcr.io/gitlayzer/sealtun",
    "hero.github": "View on GitHub",
    "hero.license": "MIT License",
    "copy.default": "Copy",
    "copy.done": "Copied",
    "features.eyebrow": "FEATURES",
    "features.title": "Public access, done to the extreme.",
    "features.lead": "Not a pile of features — every step of taking a local service public is exactly one command.",
    "f.https.title": "One command to a public HTTPS URL",
    "f.https.body": "expose a local port or a remote upstream and the Ingress plus reverse proxy comes up automatically. Basic Auth, Bearer tokens, IP allowlists, rate limiting, audit, custom domains with cert-manager certificates — all built-in flags, no Ingress YAML required.",
    "f.tcp.title": "SSH and databases, direct via NodePort",
    "f.tcp.body": "expose 22 --protocol ssh publishes your local sshd; expose 5432 --protocol tcp publishes Postgres, Redis, or MQTT. Layer-4 TCP straight through, no HTTP proxy in the way.",
    "f.up.title": "up finds the port for you",
    "f.up.body": "sealtun up discovers local listening ports and guides creation with protocol templates: Web, SSH, MySQL, Postgres, Redis, MongoDB, MQTT. One command covers daily development.",
    "f.policy.title": "Entrance security as switches",
    "f.policy.body": "One-time share links, rotatable server secrets, IP allow/deny lists, fixed-window rate limits, and access audit — enforced in the server proxy layer, not Ingress annotations.",
    "f.yaml.title": "Tunnels as YAML",
    "f.yaml.body": "Declare many tunnels in sealtun.yaml: protocol, port, domain, TTL, pod resources, auth policies. apply creates and updates idempotently, --dry-run previews the plan, --format diff compares changes, and failures roll back automatically.",
    "f.ops.title": "See the state, fix the fault",
    "f.ops.body": "inspect shows one tunnel (--remote diagnostics, --metrics counters, --resources inventory), doctor runs diagnostics and conservative fixes, logs streams pod output, list --watch keeps refreshing. Never a black box when things break.",
    "qs.eyebrow": "QUICKSTART",
    "qs.title": "Three steps from install to public.",
    "qs.s1.title": "Install and log in",
    "qs.s1.body": "After npm install, run sealtun login and finish device authorization in the browser. Credentials live in ~/.sealtun, with multiple regions and named profiles.",
    "qs.s2.title": "Expose a local service",
    "qs.s2.body": "Let up guide you, or expose precisely. HTTPS gives a URL, SSH/TCP gives host:port. The background daemon keeps tunnels alive after you close the terminal.",
    "qs.s3.title": "Govern and share",
    "qs.s3.body": "Add auth, bind a domain, enable rate limits and audit, or hand a colleague a temporary share link that expires in an hour. One command each.",
    "dl.eyebrow": "DOWNLOAD",
    "dl.title": "Choose how you install.",
    "dl.lead": "Every channel always points at the latest release. One binary, zero runtime dependencies.",
    "dl.npm.title": "npm (recommended)",
    "dl.npm.body": "Installs the matching platform binary automatically for macOS, Linux, and Windows on x64 and arm64.",
    "dl.bin.title": "GitHub Releases",
    "dl.bin.body": "Grab the tar.gz or zip for your platform, or run without installing via npx sealtun@latest.",
    "dl.oci.title": "Container image",
    "dl.oci.body": "The remote tunnel pod and the CLI ship from the same image: ghcr.io/gitlayzer/sealtun:latest.",
    "dl.alpha": "Every master push publishes a v*-alpha-N prerelease automatically — grab them from GitHub Releases under Pre-release.",
    "footer.tagline": "Sealos Cloud-native tunnel CLI",
    "footer.releases": "Releases",
    "footer.docs": "Docs",
    "footer.built": "Built for the Sealos ecosystem",
  },
};

let currentLang = "zh";

function t(key) {
  return (translations[currentLang] && translations[currentLang][key]) || translations.zh[key] || key;
}

function applyTranslations() {
  document.documentElement.lang = currentLang === "zh" ? "zh-CN" : "en";
  document.title = t("metaTitle");
  const meta = document.querySelector('meta[name="description"]');
  if (meta) meta.setAttribute("content", t("metaDescription"));

  document.querySelectorAll("[data-i18n]").forEach((el) => {
    const key = el.getAttribute("data-i18n");
    const value = t(key);
    if (value !== key) {
      if (/<[a-z][\s\S]*>/i.test(value)) {
        el.innerHTML = value;
      } else {
        el.textContent = value;
      }
    }
  });

  document.querySelectorAll(".lang-option").forEach((btn) => {
    btn.classList.toggle("active", btn.getAttribute("data-lang") === currentLang);
  });
}

function setLanguage(lang) {
  if (!translations[lang]) return;
  currentLang = lang;
  applyTranslations();
  try {
    localStorage.setItem("sealtun-lang", lang);
  } catch (_) {}
}

document.querySelectorAll(".lang-option").forEach((btn) => {
  btn.addEventListener("click", () => setLanguage(btn.getAttribute("data-lang")));
});

// Copy buttons: copy the text of the nearest <code> in the same command bar.
document.querySelectorAll("[data-copy-nearest]").forEach((btn) => {
  btn.addEventListener("click", async () => {
    const bar = btn.closest(".command-bar");
    const code = bar ? bar.querySelector("code") : null;
    if (!code) return;
    const text = code.textContent.trim();
    try {
      await navigator.clipboard.writeText(text);
    } catch (_) {
      const ta = document.createElement("textarea");
      ta.value = text;
      document.body.appendChild(ta);
      ta.select();
      document.execCommand("copy");
      ta.remove();
    }
    const original = t("copy.default");
    btn.textContent = t("copy.done");
    btn.classList.add("copied");
    setTimeout(() => {
      btn.textContent = original;
      btn.classList.remove("copied");
    }, 1600);
  });
});

const yearEl = document.getElementById("year");
if (yearEl) yearEl.textContent = String(new Date().getFullYear());

try {
  const saved = localStorage.getItem("sealtun-lang");
  if (saved && translations[saved]) currentLang = saved;
} catch (_) {}

applyTranslations();
