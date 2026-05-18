document.documentElement.classList.add("js");

const translations = {
  zh: {
    metaTitle: "Sealtun | 面向 Sealos 的本地服务公网入口",
    metaDescription: "Sealtun 是面向 Sealos Cloud 的本地服务公网入口，支持 HTTPS、SSH、TCP、自定义域名、访问策略、profiles、声明式配置和 AI Skill。",
    skip: "跳到正文",
    "nav.capabilities": "能力",
    "nav.latest": "最新功能",
    "nav.skills": "Skills",
    "nav.install": "安装",
    "nav.cta": "开始使用",
    "hero.badgeSource": "100% source available",
    "hero.title": "让 AI 和团队都能用好的 Sealos 公网隧道。",
    "hero.lead": "Sealtun 已经覆盖 HTTPS 预览、SSH/TCP 四层入口、自定义域名证书、访问策略、region/profile、声明式多隧道和本地观测。先安装 Skill，让 AI 直接替你完成这些操作。",
    "hero.primary": "安装 Skill",
    "hero.secondary": "CLI 安装",
    "trust.skill": "AI Skill 安装",
    "trust.modes": "HTTPS / SSH / TCP",
    "trust.policy": "访问策略",
    "trust.domain": "自定义域名 + 证书",
    "trust.profile": "Region profiles",
    "cap.eyebrow": "What it does",
    "cap.title": "从本地服务到公网入口，保持短路径和可控边界。",
    "cap.card1.title": "一条命令选择入口协议",
    "cap.card1.body": "默认生成公网 HTTPS 地址；需要直连 SSH 或数据库等非 HTTP 协议时，可以切到 ssh 或 tcp 四层入口。",
    "cap.card2.title": "访问策略在代理层执行",
    "cap.card2.body": "Basic Auth、Bearer Token、IP allowlist / denylist 和临时链接都在 Sealtun proxy 层生效，公开预览也能保留边界。",
    "cap.card3.title": "声明、诊断和回收可以复用",
    "cap.card3.body": "支持 dry-run、diff、apply、ttl 和多隧道；doctor、logs、events、metrics、dashboard 让问题定位回到同一条链路。",
    "latest.eyebrow": "Latest capabilities",
    "latest.title": "最新功能已经覆盖从入口创建到持续运维。",
    "latest.body": "Sealtun 现在不只是把 Web 页面发出去。它可以管理公网协议、域名证书、访问控制、账号环境和运行状态，让本地调试更接近真实入口。",
    "latest.modes.kicker": "Entrance modes",
    "latest.modes.title": "HTTPS、SSH、TCP 三种公网入口",
    "latest.modes.body": "Web 预览继续走 HTTPS；本地 sshd 可以用 NodePort 直连；数据库、队列和调试服务可以用通用 TCP 入口。",
    "latest.domain.kicker": "Domains",
    "latest.domain.title": "自定义域名先验证，再绑定证书",
    "latest.domain.body": "Sealtun 保留 Sealos host 作为 CNAME 目标，确认 DNS 归属后再写入 Ingress，并创建 cert-manager Issuer 和 Certificate。",
    "latest.access.kicker": "Access",
    "latest.access.title": "公开预览可以带权限",
    "latest.access.body": "Basic Auth、Bearer Token、IP 规则和临时访问链接都在代理层处理，token 以 hash 形式保存。",
    "latest.profile.kicker": "Profiles",
    "latest.profile.title": "多 region 和多账号切换",
    "latest.profile.body": "内置 gzg、hzh、bja、cloud、usw region，并支持命名 profile 保存不同账号、workspace 和 kubeconfig。",
    "latest.observe.kicker": "Observe",
    "latest.observe.title": "日志、事件、指标和控制台",
    "latest.observe.body": "logs、events、metrics、inspect --remote、doctor 和本地 dashboard 把本地 session 与远端 Kubernetes 状态放在一起看。",
    "latest.declarative.kicker": "Declarative",
    "latest.declarative.title": "声明式多隧道和自动过期",
    "latest.declarative.body": "sealtun.yaml 支持多个稳定命名隧道、dry-run、diff、apply、ttl 和失败回滚，适合团队共享配置。",
    "skill.title": "不用先学 Sealtun，让 AI 先学会。",
    "skill.bodyA": "把这个仓库作为 Skill 安装后，AI agent 会理解 Sealtun 的 CLI、配置、访问策略、四层入口、域名诊断和发布流程。",
    "skill.bodyB": "你可以直接描述目标：暴露本地服务、加 Basic Auth、绑定域名、写 sealtun.yaml、查 doctor，而不是先查参数。",
    "skill.step1.kicker": "Install",
    "skill.step1.body": "一条 npx 命令把 Skill 加进 AI 工作流",
    "skill.step2.kicker": "Ask",
    "skill.step2.body": "用自然语言描述端口、协议、域名和访问规则",
    "skill.step3.kicker": "Review",
    "skill.step3.body": "涉及 expose、apply、domain set、cleanup 等状态变更时先确认",
    "skill.step4.kicker": "Operate",
    "skill.step4.body": "让 AI 处理 doctor、logs、events、metrics 和 dashboard",
    "install.eyebrow": "Install",
    "install.title": "AI 用 Skill，人用 CLI，两条路径都很短。",
    "install.bodyA": "先装 Skill，让 AI 会用 Sealtun；需要自己操作时，再用 npm 或 npx 安装 CLI。",
    "install.bodyB": "团队共享入口可以写进",
    "install.bodyC": "，用 dry-run 和 diff 先看清变更，再 apply。",
    "tabs.skill": "AI Skill",
    "tabs.domain": "域名",
    "tabs.policy": "访问策略",
    "tabs.yaml": "声明式",
    "copy.default": "复制",
    "copy.success": "已复制",
    "copy.error": "复制失败",
    "cta.eyebrow": "Built for local services that need a real entrance",
    "cta.title": "把本地开发服务放到公网时，协议、域名、权限、诊断和回收都应该清楚。",
    "cta.button": "View source",
    "footer.tagline": "Localhost tunnels built for Sealos Cloud.",
    "footer.release": "Latest release"
  },
  en: {
    metaTitle: "Sealtun | Sealos native localhost tunnels",
    metaDescription: "Sealtun exposes local services through Sealos Cloud with HTTPS, SSH, TCP, custom domains, access policies, profiles, declarative config, and AI Skill support.",
    skip: "Skip to content",
    "nav.capabilities": "Capabilities",
    "nav.latest": "Latest",
    "nav.skills": "Skills",
    "nav.install": "Install",
    "nav.cta": "Get Started",
    "hero.badgeSource": "100% source available",
    "hero.title": "Sealos-native public tunnels for AI and teams.",
    "hero.lead": "Sealtun now covers HTTPS previews, SSH/TCP L4 entrances, custom-domain certificates, access policies, region profiles, declarative multi-tunnel config, and local observability. Install the Skill first so AI can operate it for you.",
    "hero.primary": "Install Skill",
    "hero.secondary": "CLI install",
    "trust.skill": "AI Skill install",
    "trust.modes": "HTTPS / SSH / TCP",
    "trust.policy": "Access policies",
    "trust.domain": "Custom domains + certs",
    "trust.profile": "Region profiles",
    "cap.eyebrow": "What it does",
    "cap.title": "A short, controlled path from localhost to a public Sealos entrance.",
    "cap.card1.title": "Choose the public protocol in one command",
    "cap.card1.body": "HTTPS is the default for public previews. Switch to ssh or tcp when you need direct L4 access for sshd, databases, queues, or debugging services.",
    "cap.card2.title": "Run access policy at the proxy layer",
    "cap.card2.body": "Basic Auth, Bearer Token, IP allow / deny rules, and temporary links are enforced by the Sealtun proxy, so public previews keep an explicit boundary.",
    "cap.card3.title": "Reuse declaration, diagnostics, and cleanup",
    "cap.card3.body": "supports dry-run, diff, apply, ttl, and multi-tunnel config. doctor, logs, events, metrics, and dashboard keep troubleshooting on one path.",
    "latest.eyebrow": "Latest capabilities",
    "latest.title": "The latest Sealtun covers creation, domains, access, and operations.",
    "latest.body": "Sealtun is no longer just a way to share a web page. It manages public protocols, domain certificates, access control, account context, and runtime state so local debugging behaves closer to a real entrance.",
    "latest.modes.kicker": "Entrance modes",
    "latest.modes.title": "HTTPS, SSH, and TCP public entrances",
    "latest.modes.body": "Keep web previews on HTTPS, expose a local sshd with direct NodePort SSH, or publish databases, queues, and debugging services through generic TCP.",
    "latest.domain.kicker": "Domains",
    "latest.domain.title": "Verify domains before binding certificates",
    "latest.domain.body": "Sealtun keeps the Sealos host as the CNAME target, writes custom hosts only after DNS ownership is verified, then creates cert-manager Issuer and Certificate resources.",
    "latest.access.kicker": "Access",
    "latest.access.title": "Public previews can stay gated",
    "latest.access.body": "Basic Auth, Bearer tokens, IP rules, and temporary access links are handled in the proxy layer, with tokens stored as hashes.",
    "latest.profile.kicker": "Profiles",
    "latest.profile.title": "Switch regions and accounts",
    "latest.profile.body": "Built-in gzg, hzh, bja, cloud, and usw regions pair with named profiles for different accounts, workspaces, and kubeconfigs.",
    "latest.observe.kicker": "Observe",
    "latest.observe.title": "Logs, events, metrics, and dashboard",
    "latest.observe.body": "logs, events, metrics, inspect --remote, doctor, and the local dashboard put local session state and remote Kubernetes state in one view.",
    "latest.declarative.kicker": "Declarative",
    "latest.declarative.title": "Multi-tunnel config with auto-expiry",
    "latest.declarative.body": "sealtun.yaml supports multiple stable named tunnels, dry-run, diff, apply, ttl, and rollback on failed batches for shared team workflows.",
    "skill.title": "You do not have to learn Sealtun first. Let AI learn it.",
    "skill.bodyA": "After installing this repository as a Skill, your AI agent understands Sealtun's CLI, config, access policies, L4 entrances, domain diagnostics, and release workflow.",
    "skill.bodyB": "Describe the outcome: expose localhost, add Basic Auth, bind a domain, write sealtun.yaml, run doctor. The agent handles the tool details.",
    "skill.step1.kicker": "Install",
    "skill.step1.body": "Add the Skill to your AI workflow with one npx command",
    "skill.step2.kicker": "Ask",
    "skill.step2.body": "Describe the port, protocol, domain, and access rules in natural language",
    "skill.step3.kicker": "Review",
    "skill.step3.body": "Confirm state-changing actions such as expose, apply, domain set, and cleanup",
    "skill.step4.kicker": "Operate",
    "skill.step4.body": "Let AI work through doctor, logs, events, metrics, and dashboard",
    "install.eyebrow": "Install",
    "install.title": "Skill for AI, CLI for humans. Both paths stay short.",
    "install.bodyA": "Install the Skill first so AI can use Sealtun. When you want direct control, install the CLI with npm or npx.",
    "install.bodyB": "Shared tunnel definitions can live in",
    "install.bodyC": ", with dry-run and diff before apply.",
    "tabs.skill": "AI Skill",
    "tabs.domain": "Domains",
    "tabs.policy": "Access policy",
    "tabs.yaml": "Declarative",
    "copy.default": "Copy",
    "copy.success": "Copied",
    "copy.error": "Copy failed",
    "cta.eyebrow": "Built for local services that need a real entrance",
    "cta.title": "When localhost goes public, protocol, domain, permissions, diagnostics, and cleanup should stay explicit.",
    "cta.button": "View source",
    "footer.tagline": "Localhost tunnels built for Sealos Cloud.",
    "footer.release": "Latest release"
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
    { threshold: 0.16 }
  );

  revealElements.forEach(el => revealObserver.observe(el));
} else {
  revealElements.forEach(el => el.classList.add("visible"));
}

const tabs = document.querySelectorAll("[data-target]");
const panes = document.querySelectorAll("[data-pane]");
let currentPane = document.querySelector(".tab.active")?.dataset.target || "skill";

tabs.forEach(tab => {
  tab.addEventListener("click", () => {
    currentPane = tab.dataset.target;
    tabs.forEach(item => item.classList.toggle("active", item === tab));
    panes.forEach(pane => pane.classList.toggle("active", pane.dataset.pane === currentPane));
  });
});

document.querySelector("[data-copy-current]")?.addEventListener("click", async event => {
  const active = document.querySelector(`[data-pane="${currentPane}"] code`);
  if (!active) return;
  const button = event.currentTarget;
  const dictionary = translations[currentLang];
  try {
    await navigator.clipboard.writeText(active.textContent.trim());
    button.textContent = dictionary["copy.success"];
  } catch (_) {
    button.textContent = dictionary["copy.error"];
  }
  window.setTimeout(() => {
    button.textContent = dictionary["copy.default"];
  }, 1400);
});

document.querySelectorAll("[data-copy-nearest]").forEach(button => {
  button.addEventListener("click", async event => {
    const button = event.currentTarget;
    const command = button.closest(".hero-command, .skill-command")?.querySelector("code");
    if (!command) return;
    const dictionary = translations[currentLang];
    try {
      await navigator.clipboard.writeText(command.textContent.trim());
      button.textContent = dictionary["copy.success"];
    } catch (_) {
      button.textContent = dictionary["copy.error"];
    }
    window.setTimeout(() => {
      button.textContent = dictionary["copy.default"];
    }, 1400);
  });
});
