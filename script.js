document.documentElement.classList.add("js");

const translations = {
  zh: {
    metaTitle: "Sealtun | 面向 Sealos 的本地服务公网入口",
    metaDescription: "Sealtun 是面向 Sealos Cloud 的本地服务公网入口，支持 HTTPS、SSH、TCP、自定义域名、分享链接、dashboard 工作台、模板、export 和 AI Skill。",
    skip: "跳到正文",
    "nav.capabilities": "能力",
    "nav.latest": "最新功能",
    "nav.skills": "Skills",
    "nav.install": "安装",
    "nav.cta": "开始使用",
    "hero.badgeSource": "100% source available",
    "hero.title": "让 AI 和团队都能用好的 Sealos 公网隧道。",
    "hero.lead": "Sealtun 已经覆盖 HTTPS 预览、SSH/TCP 四层入口、自定义域名证书、临时分享链接、dashboard 工作台、模板生成和 YAML 导出。先安装 Skill，让 AI 直接替你完成这些操作。",
    "hero.primary": "安装 Skill",
    "hero.secondary": "CLI 安装",
    "trust.skill": "AI Skill 安装",
    "trust.modes": "HTTPS / SSH / TCP",
    "trust.share": "临时分享链接",
    "trust.domain": "自定义域名 + 证书",
    "trust.dashboard": "Dashboard 工作台",
    "cap.eyebrow": "What it does",
    "cap.title": "从本地服务到公网入口，保持短路径和可控边界。",
    "cap.card1.title": "一条命令选择入口协议",
    "cap.card1.body": "默认生成公网 HTTPS 地址；需要直连 SSH 或数据库等非 HTTP 协议时，可以切到 ssh 或 tcp 四层入口。",
    "cap.card2.title": "访问策略在代理层执行",
    "cap.card2.body": "Basic Auth、Bearer Token、IP allowlist / denylist 和临时链接都在 Sealtun proxy 层生效，公开预览也能保留边界。",
    "cap.card3.title": "模板、声明和导出可以复用",
    "cap.card3.body": "支持 template 生成、dry-run、diff、apply、ttl、多隧道和 export 回写；团队共享配置不用从头拼参数。",
    "latest.eyebrow": "Latest capabilities",
    "latest.title": "最新功能覆盖入口、分享、域名、工作台和配置回收。",
    "latest.body": "Sealtun 现在不只是把 Web 页面发出去。它可以管理公网协议、临时访问、域名证书、账号环境和运行状态，也能把已经运行的隧道反向整理成 YAML。",
    "latest.modes.kicker": "Entrance modes",
    "latest.modes.title": "HTTPS、SSH、TCP 三种公网入口",
    "latest.modes.body": "Web 预览继续走 HTTPS；本地 sshd 可以用 NodePort 直连；数据库、队列和调试服务可以用通用 TCP 入口。",
    "latest.domain.kicker": "Domains",
    "latest.domain.title": "域名先 plan，再 add 和 verify",
    "latest.domain.body": "domain plan 先生成 CNAME 指引，domain add 可以等待 DNS 生效后绑定域名，并继续检查 Ingress 与证书状态。",
    "latest.share.kicker": "Share",
    "latest.share.title": "已有 HTTPS 隧道可以临时分享",
    "latest.share.body": "share create/list/revoke 为隧道生成自动失效的访问链接，URL 只在创建时显示一次，token 不会在列表里泄漏。",
    "latest.dashboard.kicker": "Workbench",
    "latest.dashboard.title": "Dashboard 不只是只读面板",
    "latest.dashboard.body": "本地工作台可以创建 HTTPS/SSH/TCP 隧道，执行 YAML dry-run/diff/apply，查看日志指标事件，并管理域名和 cleanup。",
    "latest.doctor.kicker": "Doctor",
    "latest.doctor.title": "单条隧道诊断更直接",
    "latest.doctor.body": "doctor <tunnel-id> 会把本地端口、daemon、远端 Pod、Service、Ingress 和证书问题串起来，并给出下一步建议。",
    "latest.template.kicker": "Template & export",
    "latest.template.title": "从模板开始，也能从现状导出",
    "latest.template.body": "template https/ssh/tcp/mysql/postgres/redis/mqtt 生成命令和 YAML；export 可把本地 session 安全导出回 sealtun.yaml。",
    "skill.title": "不用先学 Sealtun，让 AI 先学会。",
    "skill.bodyA": "把这个仓库作为 Skill 安装后，AI agent 会理解 Sealtun 的 CLI、配置、分享链接、四层入口、域名诊断、dashboard 和发布流程。",
    "skill.bodyB": "你可以直接描述目标：暴露本地服务、加 Basic Auth、创建临时分享、绑定域名、导出 sealtun.yaml、查 doctor，而不是先查参数。",
    "skill.step1.kicker": "Install",
    "skill.step1.body": "一条 npx 命令把 Skill 加进 AI 工作流",
    "skill.step2.kicker": "Ask",
    "skill.step2.body": "用自然语言描述端口、协议、域名和访问规则",
    "skill.step3.kicker": "Review",
    "skill.step3.body": "涉及 expose、apply、domain set、cleanup 等状态变更时先确认",
    "skill.step4.kicker": "Operate",
    "skill.step4.body": "让 AI 处理 doctor、share、template、export 和 dashboard",
    "install.eyebrow": "Install",
    "install.title": "AI 用 Skill，人用 CLI，两条路径都很短。",
    "install.bodyA": "先装 Skill，让 AI 会用 Sealtun；需要自己操作时，再用 npm 或 npx 安装 CLI。",
    "install.bodyB": "团队共享入口可以写进",
    "install.bodyC": "，用 dry-run 和 diff 先看清变更，再 apply。",
    "tabs.skill": "AI Skill",
    "tabs.domain": "域名",
    "tabs.share": "分享",
    "tabs.yaml": "模板/YAML",
    "copy.default": "复制",
    "copy.success": "已复制",
    "copy.error": "复制失败",
    "cta.eyebrow": "Built for local services that need a real entrance",
    "cta.title": "把本地开发服务放到公网时，入口、分享、域名、诊断和回收都应该清楚。",
    "cta.button": "View source",
    "footer.tagline": "Localhost tunnels built for Sealos Cloud.",
    "footer.release": "Latest release"
  },
  en: {
    metaTitle: "Sealtun | Sealos native localhost tunnels",
    metaDescription: "Sealtun exposes local services through Sealos Cloud with HTTPS, SSH, TCP, custom domains, temporary shares, dashboard workbench, templates, export, and AI Skill support.",
    skip: "Skip to content",
    "nav.capabilities": "Capabilities",
    "nav.latest": "Latest",
    "nav.skills": "Skills",
    "nav.install": "Install",
    "nav.cta": "Get Started",
    "hero.badgeSource": "100% source available",
    "hero.title": "Sealos-native public tunnels for AI and teams.",
    "hero.lead": "Sealtun now covers HTTPS previews, SSH/TCP L4 entrances, custom-domain certificates, temporary share links, the dashboard workbench, template generation, and YAML export. Install the Skill first so AI can operate it for you.",
    "hero.primary": "Install Skill",
    "hero.secondary": "CLI install",
    "trust.skill": "AI Skill install",
    "trust.modes": "HTTPS / SSH / TCP",
    "trust.share": "Temporary shares",
    "trust.domain": "Custom domains + certs",
    "trust.dashboard": "Dashboard workbench",
    "cap.eyebrow": "What it does",
    "cap.title": "A short, controlled path from localhost to a public Sealos entrance.",
    "cap.card1.title": "Choose the public protocol in one command",
    "cap.card1.body": "HTTPS is the default for public previews. Switch to ssh or tcp when you need direct L4 access for sshd, databases, queues, or debugging services.",
    "cap.card2.title": "Run access policy at the proxy layer",
    "cap.card2.body": "Basic Auth, Bearer Token, IP allow / deny rules, and temporary links are enforced by the Sealtun proxy, so public previews keep an explicit boundary.",
    "cap.card3.title": "Reuse templates, declarations, and export",
    "cap.card3.body": "supports template generation, dry-run, diff, apply, ttl, multi-tunnel config, and export back to YAML so teams do not rebuild flags from scratch.",
    "latest.eyebrow": "Latest capabilities",
    "latest.title": "The latest Sealtun covers entrances, shares, domains, workbench, and config recovery.",
    "latest.body": "Sealtun is no longer just a way to share a web page. It manages public protocols, temporary access, domain certificates, account context, and runtime state, and it can export running tunnels back to YAML.",
    "latest.modes.kicker": "Entrance modes",
    "latest.modes.title": "HTTPS, SSH, and TCP public entrances",
    "latest.modes.body": "Keep web previews on HTTPS, expose a local sshd with direct NodePort SSH, or publish databases, queues, and debugging services through generic TCP.",
    "latest.domain.kicker": "Domains",
    "latest.domain.title": "Plan domains, then add and verify",
    "latest.domain.body": "domain plan prints CNAME guidance first. domain add can wait for DNS, attach the host, then continue checking Ingress and certificate readiness.",
    "latest.share.kicker": "Share",
    "latest.share.title": "Existing HTTPS tunnels can be shared temporarily",
    "latest.share.body": "share create/list/revoke generates expiring review links. The URL is shown once at creation time, and token values are not leaked in later lists.",
    "latest.dashboard.kicker": "Workbench",
    "latest.dashboard.title": "Dashboard is more than a read-only panel",
    "latest.dashboard.body": "The local workbench can create HTTPS/SSH/TCP tunnels, run YAML dry-run/diff/apply, view logs, metrics, and events, and manage domains and cleanup.",
    "latest.doctor.kicker": "Doctor",
    "latest.doctor.title": "Single-tunnel diagnostics are direct",
    "latest.doctor.body": "doctor <tunnel-id> connects local port, daemon, remote Pod, Service, Ingress, and certificate checks, then suggests the next step.",
    "latest.template.kicker": "Template & export",
    "latest.template.title": "Start from templates or export reality",
    "latest.template.body": "template https/ssh/tcp/mysql/postgres/redis/mqtt emits commands and YAML. export safely turns local sessions back into sealtun.yaml.",
    "skill.title": "You do not have to learn Sealtun first. Let AI learn it.",
    "skill.bodyA": "After installing this repository as a Skill, your AI agent understands Sealtun's CLI, config, share links, L4 entrances, domain diagnostics, dashboard, and release workflow.",
    "skill.bodyB": "Describe the outcome: expose localhost, add Basic Auth, create a temporary share, bind a domain, export sealtun.yaml, run doctor. The agent handles the tool details.",
    "skill.step1.kicker": "Install",
    "skill.step1.body": "Add the Skill to your AI workflow with one npx command",
    "skill.step2.kicker": "Ask",
    "skill.step2.body": "Describe the port, protocol, domain, and access rules in natural language",
    "skill.step3.kicker": "Review",
    "skill.step3.body": "Confirm state-changing actions such as expose, apply, domain set, and cleanup",
    "skill.step4.kicker": "Operate",
    "skill.step4.body": "Let AI work through doctor, share, template, export, and dashboard",
    "install.eyebrow": "Install",
    "install.title": "Skill for AI, CLI for humans. Both paths stay short.",
    "install.bodyA": "Install the Skill first so AI can use Sealtun. When you want direct control, install the CLI with npm or npx.",
    "install.bodyB": "Shared tunnel definitions can live in",
    "install.bodyC": ", with dry-run and diff before apply.",
    "tabs.skill": "AI Skill",
    "tabs.domain": "Domains",
    "tabs.share": "Share",
    "tabs.yaml": "Template/YAML",
    "copy.default": "Copy",
    "copy.success": "Copied",
    "copy.error": "Copy failed",
    "cta.eyebrow": "Built for local services that need a real entrance",
    "cta.title": "When localhost goes public, entrances, shares, domains, diagnostics, and cleanup should stay explicit.",
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
