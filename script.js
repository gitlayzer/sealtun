document.documentElement.classList.add("js");

const translations = {
  zh: {
    metaTitle: "Sealtun | 面向 Sealos 的本地服务公网入口",
    metaDescription: "Sealtun 是面向 Sealos Cloud 的本地服务公网入口，支持 HTTPS、SSH、TCP、init 引导、资源可见性、watch、doctor 自动修复、Dashboard 命令预览和 AI Skill。",
    skip: "跳到正文",
    "nav.capabilities": "能力",
    "nav.latest": "最新功能",
    "nav.skills": "Skills",
    "nav.install": "安装",
    "nav.cta": "开始使用",
    "hero.eyebrow": "Sealos-native tunnel",
    "hero.title": "Sealtun 把 localhost 变成可控的 Sealos 公网入口。",
    "hero.lead": "HTTPS 预览、SSH/TCP 四层入口、访问策略、自定义域名证书、临时分享、init 引导、资源可见性、watch、doctor 自动修复和 Dashboard 命令预览都已经接好。更重要的是，AI 可以通过 Skill 直接替你操作。",
    "hero.primary": "安装 Skill",
    "hero.secondary": "查看 CLI 用法",
    "trust.login": "OAuth2 登录 / region / profile",
    "trust.protocols": "HTTPS / SSH / TCP",
    "trust.policy": "Basic Auth / Bearer / IP rules",
    "trust.resources": "Init / Watch / Resources",
    "trust.config": "Template / apply / export",
    "trust.skill": "Codex Skill",
    "statement.eyebrow": "What Sealtun does",
    "statement.title": "从本地端口到公网入口，路径应该短，边界应该清楚。",
    "statement.body": "Sealtun 使用 Sealos Cloud 的 Kubernetes、Service 与 Ingress 能力，把本地服务通过加密 WebSocket 隧道转发出去。开发预览、第三方回调、SSH 调试、数据库临时访问和团队评审链接，都可以走同一套 CLI。",
    "protocol.eyebrow": "Public entrances",
    "protocol.title": "不同协议有不同入口，不混在一起。",
    "protocol.body": "HTTPS 适合 Web 和回调，SSH/TCP 走四层公网 NodePort。访问控制、临时分享和自定义域名属于 HTTPS 代理层能力，规则清楚，排障也更直接。",
    "protocol.https.title": "HTTPS 预览",
    "protocol.https.body": "一条 `sealtun expose 3000` 获得受信任的公网 URL，兼容普通 HTTP 和 WebSocket 应用流量。",
    "protocol.ssh.title": "公网 SSH",
    "protocol.ssh.body": "本地 sshd 可以通过 `--protocol ssh` 暴露成公网 TCP 入口，适合临时调试机器或开发环境。",
    "protocol.tcp.title": "通用 TCP",
    "protocol.tcp.body": "Postgres、Redis、MQTT 或其他非 HTTP 服务可以走 `--protocol tcp`，输出公网 host 和 port。",
    "latest.eyebrow": "Latest capabilities",
    "latest.title": "最新功能已经覆盖引导、资源可见性、自动修复和工作台命令预览。",
    "latest.domain.kicker": "Domains",
    "latest.domain.title": "域名先 plan，再 add、verify 和 doctor",
    "latest.domain.body": "`domain plan` 先生成 CNAME 指引，`domain add --wait` 等待 DNS 和证书就绪，`domain doctor` 用来串起 Ingress 与证书状态。",
    "latest.profile.kicker": "Guided UX",
    "latest.profile.title": "init 会把第一次使用变成推荐流程",
    "latest.profile.body": "`sealtun init` 会检查登录状态、当前 region/profile 和本地监听端口，输出推荐命令与 `sealtun.yaml`；只有加 `--apply` 才会创建资源。",
    "latest.share.kicker": "Share",
    "latest.share.title": "已有 HTTPS 隧道可以临时分享",
    "latest.share.body": "`share create/list/revoke` 生成自动失效的评审链接，URL 只在创建时显示一次，后续列表不泄漏 token。",
    "latest.policy.kicker": "Access policy",
    "latest.policy.title": "访问策略在代理层执行",
    "latest.policy.body": "Basic Auth、Bearer Token、临时 token、IP allowlist 和 denylist 都由 Sealtun proxy 校验，不依赖 Ingress annotation。",
    "latest.workbench.kicker": "Workbench",
    "latest.workbench.title": "Dashboard 已经是完整工作台",
    "latest.workbench.body": "可以创建 HTTPS/SSH/TCP 隧道，执行 YAML dry-run/diff/apply，查看 logs、metrics、events/resources，并管理 domain、stop/start/cleanup；写操作确认前会展示等价 CLI 命令。",
    "latest.config.kicker": "YAML",
    "latest.config.title": "模板、apply、TTL 和 export 可以闭环",
    "latest.config.body": "`template` 生成命令和 `sealtun.yaml`，`apply` 用稳定名称幂等创建或更新，TTL 可自动清理过期资源，`export --all --include-secret-placeholders` 安全回收配置。",
    "latest.diagnose.kicker": "Diagnostics",
    "latest.diagnose.title": "单条隧道诊断更直接",
    "latest.diagnose.body": "`doctor --fix --dry-run` 先展示保守修复计划，`doctor --fix` 只执行低风险动作，例如恢复 stopped 隧道、清理 expired/stale 隧道或启动本地 daemon。",
    "latest.resources.kicker": "Live resources",
    "latest.resources.title": "Live、Resources 和 Discover 放在工作台里",
    "latest.resources.body": "`resources` 命令和 Dashboard Resources tab 会展示 Deployment、Pod、Service、Ingress、Certificate、Issuer 和 Secret 元数据；`watch` 可以持续观察隧道或全局状态变化。",
    "skill.title": "不用先学 Sealtun，让 AI 先学会。",
    "skill.body": "通过一条 npx 命令把仓库里的 Skill 装进 AI 工作流。之后你可以直接描述目标，比如初始化推荐流程、暴露本地服务、加认证、创建临时分享、绑定域名、打开 dashboard、查看 resources、watch 状态或跑 doctor。",
    "skill.step1": "安装 Skill，让 AI 知道 Sealtun 的 CLI、YAML 和安全边界。",
    "skill.step2": "用自然语言描述端口、协议、域名和访问规则。",
    "skill.step3": "涉及 expose、apply、domain、cleanup 等状态变更时先确认。",
    "skill.step4": "让 AI 处理命令细节、诊断输出和后续配置整理。",
    "install.eyebrow": "Install and operate",
    "install.title": "AI 用 Skill，人用 CLI，团队用 sealtun.yaml。",
    "install.body": "安装路径保持短，运行路径保持可审计。首次使用可以先跑 init 获取推荐命令；团队配置写进 YAML 后再 dry-run、diff、apply，必要时再通过 dashboard 查看资源、事件和命令预览。",
    "tabs.skill": "AI Skill",
    "tabs.domain": "域名",
    "tabs.share": "分享",
    "tabs.dashboard": "工作台",
    "tabs.diagnostics": "诊断",
    "tabs.yaml": "YAML",
    "copy.default": "复制",
    "copy.success": "已复制",
    "copy.error": "复制失败",
    "cta.eyebrow": "Open source and Sealos native",
    "cta.title": "把公网入口、访问策略、资源观测、诊断和配置回收放进同一个工具。",
    "cta.button": "查看源码",
    "footer.tagline": "Localhost tunnels built for Sealos Cloud.",
    "footer.back": "回到顶部"
  },
  en: {
    metaTitle: "Sealtun | Sealos-native localhost tunnels",
    metaDescription: "Sealtun exposes local services through Sealos Cloud with HTTPS, SSH, TCP, guided init, resources, watch, doctor fixes, Dashboard command previews, and AI Skill support.",
    skip: "Skip to content",
    "nav.capabilities": "Capabilities",
    "nav.latest": "Latest",
    "nav.skills": "Skills",
    "nav.install": "Install",
    "nav.cta": "Get started",
    "hero.eyebrow": "Sealos-native tunnel",
    "hero.title": "Sealtun turns localhost into a controlled Sealos public entrance.",
    "hero.lead": "HTTPS previews, SSH/TCP L4 entrances, access policies, custom-domain certificates, temporary shares, guided init, resources, watch, doctor fixes, and Dashboard command previews are already wired together. More importantly, AI can operate Sealtun through the Skill.",
    "hero.primary": "Install Skill",
    "hero.secondary": "See CLI usage",
    "trust.login": "OAuth2 login / region / profile",
    "trust.protocols": "HTTPS / SSH / TCP",
    "trust.policy": "Basic Auth / Bearer / IP rules",
    "trust.resources": "Init / Watch / Resources",
    "trust.config": "Template / apply / export",
    "trust.skill": "Codex Skill",
    "statement.eyebrow": "What Sealtun does",
    "statement.title": "From local port to public entrance, the path should be short and the boundary explicit.",
    "statement.body": "Sealtun uses Sealos Cloud Kubernetes, Service, and Ingress primitives to forward local services through encrypted WebSocket tunnels. Dev previews, third-party callbacks, SSH debugging, temporary database access, and team review links can all use the same CLI.",
    "protocol.eyebrow": "Public entrances",
    "protocol.title": "Different protocols get different entrances, without mixing layers.",
    "protocol.body": "HTTPS fits web apps and callbacks. SSH/TCP use public L4 NodePort. Access policy, temporary shares, and custom domains belong to the HTTPS proxy layer, which keeps behavior clear and diagnostics direct.",
    "protocol.https.title": "HTTPS previews",
    "protocol.https.body": "Run `sealtun expose 3000` to get a trusted public URL for regular HTTP and WebSocket application traffic.",
    "protocol.ssh.title": "Public SSH",
    "protocol.ssh.body": "Expose local sshd through `--protocol ssh` as a public TCP entrance for temporary machine or dev-environment debugging.",
    "protocol.tcp.title": "Generic TCP",
    "protocol.tcp.body": "Postgres, Redis, MQTT, or other non-HTTP services can use `--protocol tcp` and receive a public host and port.",
    "latest.eyebrow": "Latest capabilities",
    "latest.title": "The latest work covers guided onboarding, resource visibility, safe fixes, and workbench command previews.",
    "latest.domain.kicker": "Domains",
    "latest.domain.title": "Plan domains, then add, verify, and doctor",
    "latest.domain.body": "`domain plan` prints CNAME guidance, `domain add --wait` waits for DNS and certificates, and `domain doctor` connects Ingress and certificate status.",
    "latest.profile.kicker": "Guided UX",
    "latest.profile.title": "init turns first use into a recommendation flow",
    "latest.profile.body": "`sealtun init` checks login state, active region/profile, and local listening ports, then prints a recommended command and `sealtun.yaml`; only `--apply` creates resources.",
    "latest.share.kicker": "Share",
    "latest.share.title": "Existing HTTPS tunnels can be shared temporarily",
    "latest.share.body": "`share create/list/revoke` creates expiring review links. The URL is shown once at creation time, and later lists do not leak token values.",
    "latest.policy.kicker": "Access policy",
    "latest.policy.title": "Access policy runs at the proxy layer",
    "latest.policy.body": "Basic Auth, Bearer Token, temporary tokens, IP allowlist, and denylist are enforced by the Sealtun proxy without relying on Ingress annotations.",
    "latest.workbench.kicker": "Workbench",
    "latest.workbench.title": "Dashboard is now a full workbench",
    "latest.workbench.body": "Create HTTPS/SSH/TCP tunnels, run YAML dry-run/diff/apply, view logs, metrics, events/resources, and manage domain, stop/start, and cleanup. Write confirmations preview the equivalent CLI command.",
    "latest.config.kicker": "YAML",
    "latest.config.title": "Templates, apply, TTL, and export close the loop",
    "latest.config.body": "`template` emits commands and `sealtun.yaml`; `apply` idempotently creates or updates stable names; TTL can clean expired resources; `export --all --include-secret-placeholders` recovers config safely.",
    "latest.diagnose.kicker": "Diagnostics",
    "latest.diagnose.title": "Single-tunnel diagnostics are direct",
    "latest.diagnose.body": "`doctor --fix --dry-run` shows a conservative repair plan first. `doctor --fix` only executes low-risk actions such as resuming stopped tunnels, cleaning expired/stale tunnels, or starting the local daemon.",
    "latest.resources.kicker": "Live resources",
    "latest.resources.title": "Live, Resources, and Discover live in the workbench",
    "latest.resources.body": "`resources` and the Dashboard Resources tab show Deployment, Pod, Service, Ingress, Certificate, Issuer, and Secret metadata. `watch` continuously follows tunnel or global state changes.",
    "skill.title": "You do not have to learn Sealtun first. Let AI learn it.",
    "skill.body": "Install the repository Skill into your AI workflow with one npx command. Then describe the outcome: run guided init, expose localhost, add auth, create a temporary share, bind a domain, open the dashboard, view resources, watch state, or run doctor.",
    "skill.step1": "Install the Skill so AI understands Sealtun CLI, YAML, and safety boundaries.",
    "skill.step2": "Describe the port, protocol, domain, and access rules in natural language.",
    "skill.step3": "Confirm state-changing actions such as expose, apply, domain, and cleanup.",
    "skill.step4": "Let AI handle command details, diagnostic output, and config recovery.",
    "install.eyebrow": "Install and operate",
    "install.title": "Skill for AI, CLI for humans, sealtun.yaml for teams.",
    "install.body": "Install paths stay short and operation stays auditable. First use can start with init recommendations; put team config in YAML, run dry-run, diff, and apply, then inspect resources, events, and command previews through the dashboard.",
    "tabs.skill": "AI Skill",
    "tabs.domain": "Domains",
    "tabs.share": "Share",
    "tabs.dashboard": "Workbench",
    "tabs.diagnostics": "Diagnostics",
    "tabs.yaml": "YAML",
    "copy.default": "Copy",
    "copy.success": "Copied",
    "copy.error": "Copy failed",
    "cta.eyebrow": "Open source and Sealos native",
    "cta.title": "Keep public entrances, access policy, resource visibility, diagnostics, and config recovery in one tool.",
    "cta.button": "View source",
    "footer.tagline": "Localhost tunnels built for Sealos Cloud.",
    "footer.back": "Back to top"
  }
};

const supportedLangs = Object.keys(translations);
const storedLang = window.localStorage.getItem("sealtun-lang");
const browserLang = navigator.language?.toLowerCase().startsWith("en") ? "en" : "zh";
let currentLang = supportedLangs.includes(storedLang) ? storedLang : browserLang;

const applyLanguage = lang => {
  currentLang = supportedLangs.includes(lang) ? lang : "zh";
  const dictionary = translations[currentLang];
  document.documentElement.lang = currentLang === "zh" ? "zh-CN" : "en";
  document.title = dictionary.metaTitle;
  document.querySelector('meta[name="description"]')?.setAttribute("content", dictionary.metaDescription);
  document.querySelector('meta[property="og:title"]')?.setAttribute("content", dictionary.metaTitle);
  document.querySelector('meta[property="og:description"]')?.setAttribute("content", dictionary.metaDescription);
  document.querySelectorAll("[data-i18n]").forEach(element => {
    const key = element.dataset.i18n;
    if (dictionary[key]) element.textContent = dictionary[key];
  });
  document.querySelectorAll("[data-lang]").forEach(button => {
    const isActive = button.dataset.lang === currentLang;
    button.classList.toggle("active", isActive);
    button.setAttribute("aria-pressed", String(isActive));
  });
  window.localStorage.setItem("sealtun-lang", currentLang);
};

document.querySelectorAll("[data-lang]").forEach(button => {
  button.addEventListener("click", () => applyLanguage(button.dataset.lang));
});

applyLanguage(currentLang);

const revealElements = document.querySelectorAll(".reveal");

if ("IntersectionObserver" in window) {
  const revealObserver = new IntersectionObserver(
    entries => {
      entries.forEach(entry => {
        if (entry.isIntersecting) {
          entry.target.classList.add("visible");
          revealObserver.unobserve(entry.target);
        }
      });
    },
    { threshold: 0.04 }
  );

  revealElements.forEach(element => revealObserver.observe(element));
} else {
  revealElements.forEach(element => element.classList.add("visible"));
}

const tabs = document.querySelectorAll("[data-target]");
const panes = document.querySelectorAll("[data-pane]");
let currentPane = document.querySelector(".tab.active")?.dataset.target || "skill";

tabs.forEach(tab => {
  tab.addEventListener("click", () => {
    currentPane = tab.dataset.target;
    tabs.forEach(item => {
      const isActive = item === tab;
      item.classList.toggle("active", isActive);
      item.setAttribute("aria-selected", String(isActive));
    });
    panes.forEach(pane => pane.classList.toggle("active", pane.dataset.pane === currentPane));
  });
});

const writeClipboard = async text => {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }

  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.left = "-9999px";
  textarea.style.top = "0";
  document.body.appendChild(textarea);
  textarea.select();
  const copied = document.execCommand("copy");
  textarea.remove();
  if (!copied) throw new Error("copy failed");
};

const copyText = async (button, text) => {
  const dictionary = translations[currentLang];
  try {
    await writeClipboard(text.trim());
    button.textContent = dictionary["copy.success"];
  } catch (_) {
    button.textContent = dictionary["copy.error"];
  }
  window.setTimeout(() => {
    button.textContent = dictionary["copy.default"];
  }, 1400);
};

document.querySelector("[data-copy-current]")?.addEventListener("click", event => {
  const active = document.querySelector(`[data-pane="${currentPane}"] code`);
  if (active) copyText(event.currentTarget, active.textContent);
});

document.querySelectorAll("[data-copy-nearest]").forEach(button => {
  button.addEventListener("click", event => {
    const command = event.currentTarget.closest(".command-bar")?.querySelector("code");
    if (command) copyText(event.currentTarget, command.textContent);
  });
});
