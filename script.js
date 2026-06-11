document.documentElement.classList.add("js");

const translations = {
  zh: {
    metaTitle: "Sealtun | 本地服务公网入口与集群访问",
    metaDescription: "Sealtun 用一套 CLI 把本地服务发布到公网，并让本机直接访问 Sealos/Kubernetes 集群内服务。",
    skip: "跳到正文",
    "nav.capabilities": "能力",
    "nav.latest": "最新功能",
    "nav.skills": "Skills",
    "nav.install": "安装",
    "nav.cta": "开始使用",
    "hero.eyebrow": "Sealos-native tunnel CLI",
    "hero.title": "把本地服务发布到公网，也让本机直接访问集群内服务。",
    "hero.lead": "Sealtun 面向 Sealos/Kubernetes：开发预览用 expose，集群联调用 connect，认证、域名、审计、诊断和 Dashboard 都在同一套命令里。",
    "hero.primary": "安装 Skill",
    "hero.secondary": "查看 CLI 用法",
    "trust.public": "localhost -> public URL",
    "trust.cluster": "local machine -> cluster Service",
    "trust.protocols": "HTTPS / SSH / TCP",
    "trust.policy": "Auth / rate limit / audit",
    "trust.ops": "Dashboard / doctor / resources",
    "trust.skill": "AI Skill ready",
    "statement.eyebrow": "What Sealtun does",
    "statement.title": "一句话：Sealtun 连接本地、集群和公网。",
    "statement.body": "要给别人访问本地服务，用 expose；要从本机调集群内 Service、ClusterIP 或 Pod IP，用 connect；要治理入口，再加认证、临时链接、审计、诊断和资源查看。",
    "protocol.eyebrow": "Public entrances",
    "protocol.title": "三类入口，按场景选择。",
    "protocol.body": "Web 走 HTTPS，SSH 和数据库走 TCP，集群内服务访问走 connect。边界清楚，命令也短。",
    "protocol.https.title": "HTTPS 预览",
    "protocol.https.body": "本地 Web、回调、预览环境，一条命令得到公网 HTTPS URL。",
    "protocol.ssh.title": "公网 SSH",
    "protocol.ssh.body": "本地 sshd 变成公网 host 和 port，适合临时远程调试。",
    "protocol.tcp.title": "通用 TCP",
    "protocol.tcp.body": "Postgres、Redis、MQTT 等非 HTTP 服务走四层 TCP。",
    "latest.eyebrow": "v0.0.25 capabilities",
    "latest.title": "常用能力压成四件事。",
    "latest.public.kicker": "Expose",
    "latest.public.title": "把本地端口发到公网",
    "latest.public.body": "HTTPS、SSH、TCP 都能发布；HTTPS 还能绑定域名、加认证、开限流和审计。",
    "latest.connect.kicker": "Connect",
    "latest.connect.title": "从本机访问集群内服务",
    "latest.connect.body": "Linux 上执行 connect 后，本机工具可直接访问 Service FQDN、ClusterIP 和 Pod IP。",
    "latest.operate.kicker": "Operate",
    "latest.operate.title": "看状态，找问题，安全修复",
    "latest.operate.body": "Dashboard、watch、resources、events、logs 和 doctor 覆盖日常排障。",
    "latest.config.kicker": "Declare",
    "latest.config.title": "把隧道写成 YAML",
    "latest.config.body": "template、apply、diff、export 和 TTL 让团队配置可复用。",
    "skill.title": "不用先学 Sealtun，让 AI 先学会。",
    "skill.body": "装上 Skill 后，直接告诉 AI 你要暴露哪个端口、访问哪个集群服务、绑定什么域名或加什么认证，它会按 Sealtun 的命令流程执行。",
    "skill.step1": "安装 Skill，让 AI 知道 Sealtun 的 CLI、YAML 和安全边界。",
    "skill.step2": "用自然语言描述端口、协议、域名和访问规则。",
    "skill.step3": "涉及 expose、apply、domain、cleanup 等状态变更时先确认。",
    "skill.step4": "让 AI 处理命令细节、诊断输出和后续配置整理。",
    "install.eyebrow": "Install and operate",
    "install.title": "三步开始：登录、暴露或连接、再用 Dashboard 排障。",
    "install.body": "CLI 给人用，Skill 给 AI 用，sealtun.yaml 给团队复用。安装后先 login，再按场景选择 expose、connect 或 apply。",
    "tabs.skill": "AI Skill",
    "tabs.domain": "域名",
    "tabs.share": "集群访问",
    "tabs.dashboard": "工作台",
    "tabs.diagnostics": "诊断",
    "tabs.yaml": "YAML",
    "copy.default": "复制",
    "copy.success": "已复制",
    "copy.error": "复制失败",
    "cta.eyebrow": "Open source and Sealos native",
    "cta.title": "一句话：Sealtun 让本地、集群和公网互相可达，而且可治理。",
    "cta.button": "查看源码",
    "footer.tagline": "Localhost tunnels built for Sealos Cloud.",
    "footer.back": "回到顶部"
  },
  en: {
    metaTitle: "Sealtun | Public tunnels and cluster access",
    metaDescription: "Sealtun publishes local services to the internet and lets your machine access Sealos/Kubernetes in-cluster services through one CLI.",
    skip: "Skip to content",
    "nav.capabilities": "Capabilities",
    "nav.latest": "Latest",
    "nav.skills": "Skills",
    "nav.install": "Install",
    "nav.cta": "Get started",
    "hero.eyebrow": "Sealos-native tunnel CLI",
    "hero.title": "Publish local services to the internet, and access cluster services from your machine.",
    "hero.lead": "Sealtun is built for Sealos/Kubernetes: use expose for public previews, connect for cluster debugging, and one command set for auth, domains, audit, diagnostics, and Dashboard operations.",
    "hero.primary": "Install Skill",
    "hero.secondary": "See CLI usage",
    "trust.public": "localhost -> public URL",
    "trust.cluster": "local machine -> cluster Service",
    "trust.protocols": "HTTPS / SSH / TCP",
    "trust.policy": "Auth / rate limit / audit",
    "trust.ops": "Dashboard / doctor / resources",
    "trust.skill": "AI Skill ready",
    "statement.eyebrow": "What Sealtun does",
    "statement.title": "In one sentence: Sealtun connects your machine, cluster, and the public internet.",
    "statement.body": "Use expose when someone needs your local service. Use connect when your machine needs a cluster Service, ClusterIP, or Pod IP. Add auth, temporary links, audit, diagnostics, and resource views when the entrance needs governance.",
    "protocol.eyebrow": "Public entrances",
    "protocol.title": "Three entrances, chosen by scenario.",
    "protocol.body": "Web uses HTTPS, SSH and databases use TCP, and in-cluster access uses connect. Clear boundary, short commands.",
    "protocol.https.title": "HTTPS previews",
    "protocol.https.body": "Local web apps, callbacks, and previews get a public HTTPS URL with one command.",
    "protocol.ssh.title": "Public SSH",
    "protocol.ssh.body": "Turn local sshd into a public host and port for temporary remote debugging.",
    "protocol.tcp.title": "Generic TCP",
    "protocol.tcp.body": "Postgres, Redis, MQTT, and other non-HTTP services use L4 TCP.",
    "latest.eyebrow": "v0.0.25 capabilities",
    "latest.title": "The common workflow is now four things.",
    "latest.public.kicker": "Expose",
    "latest.public.title": "Publish a local port",
    "latest.public.body": "HTTPS, SSH, and TCP are supported. HTTPS can also use domains, auth, rate limits, and audit.",
    "latest.connect.kicker": "Connect",
    "latest.connect.title": "Reach cluster services locally",
    "latest.connect.body": "On Linux, connect lets local tools reach Service FQDNs, ClusterIPs, and Pod IPs directly.",
    "latest.operate.kicker": "Operate",
    "latest.operate.title": "See state, find issues, fix safely",
    "latest.operate.body": "Dashboard, watch, resources, events, logs, and doctor cover daily debugging.",
    "latest.config.kicker": "Declare",
    "latest.config.title": "Write tunnels as YAML",
    "latest.config.body": "template, apply, diff, export, and TTL make team configuration reusable.",
    "skill.title": "You do not have to learn Sealtun first. Let AI learn it.",
    "skill.body": "After installing the Skill, tell AI which port to expose, which cluster service to reach, which domain to bind, or which auth to add. It follows the Sealtun command flow.",
    "skill.step1": "Install the Skill so AI understands Sealtun CLI, YAML, and safety boundaries.",
    "skill.step2": "Describe the port, protocol, domain, and access rules in natural language.",
    "skill.step3": "Confirm state-changing actions such as expose, apply, domain, and cleanup.",
    "skill.step4": "Let AI handle command details, diagnostic output, and config recovery.",
    "install.eyebrow": "Install and operate",
    "install.title": "Start in three steps: login, expose or connect, then inspect in Dashboard.",
    "install.body": "CLI is for humans, Skill is for AI, and sealtun.yaml is for teams. Install, login, then choose expose, connect, or apply.",
    "tabs.skill": "AI Skill",
    "tabs.domain": "Domains",
    "tabs.share": "Connect",
    "tabs.dashboard": "Workbench",
    "tabs.diagnostics": "Diagnostics",
    "tabs.yaml": "YAML",
    "copy.default": "Copy",
    "copy.success": "Copied",
    "copy.error": "Copy failed",
    "cta.eyebrow": "Open source and Sealos native",
    "cta.title": "In one sentence: Sealtun makes local, cluster, and public network paths reachable and governable.",
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
