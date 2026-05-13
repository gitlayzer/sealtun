document.documentElement.classList.add("js");

const translations = {
  zh: {
    metaTitle: "Sealtun | 面向 Sealos 的本地服务公网入口",
    metaDescription: "Sealtun 是面向 Sealos Cloud 的本地服务公网入口，支持 AI Skill、访问策略、自定义域名、声明式配置和诊断观测。",
    skip: "跳到正文",
    "nav.capabilities": "能力",
    "nav.skills": "Skills",
    "nav.install": "安装",
    "nav.cta": "开始使用",
    "hero.badgeSource": "100% source available",
    "hero.title": "让 AI 和团队都能用好的 Sealos 本地隧道。",
    "hero.lead": "先用一条命令把 Sealtun Skill 装进 AI，再让它替你完成公网预览、访问控制、声明式配置和排障。你不需要先记住每个 CLI 参数。",
    "hero.primary": "安装 Skill",
    "hero.secondary": "CLI 安装",
    "trust.skill": "AI Skill 安装",
    "trust.https": "公网 HTTPS",
    "trust.policy": "访问策略",
    "trust.domain": "自定义域名",
    "cap.eyebrow": "What it does",
    "cap.title": "从本地服务到公网入口，保持短路径和可控边界。",
    "cap.card1.title": "直接生成 Sealos 原生入口",
    "cap.card1.body": "登录后暴露本地端口，远端使用 Kubernetes 资源承载隧道代理，生成可访问的 HTTPS 地址。",
    "cap.card2.title": "访问策略在代理层执行",
    "cap.card2.body": "Basic Auth、Bearer Token、IP allowlist / denylist 和临时链接都在 Sealtun proxy 层生效，避免把业务访问边界交给人工约定。",
    "cap.card3.title": "声明式配置和诊断可以复用",
    "cap.card3.body": "支持 dry-run、diff 和 apply；doctor、logs、metrics、dashboard 让问题定位回到同一条链路。",
    "skill.title": "不用先学 Sealtun，让 AI 先学会。",
    "skill.bodyA": "把这个仓库作为 Skill 安装后，AI agent 会理解 Sealtun 的 CLI、配置、访问策略、排障和发布流程。",
    "skill.bodyB": "你可以直接描述目标：暴露本地服务、加 Basic Auth、写 sealtun.yaml、查 doctor，而不是先查参数。",
    "skill.step1.kicker": "Install",
    "skill.step1.body": "一条 npx 命令把 Skill 加进 AI 工作流",
    "skill.step2.kicker": "Ask",
    "skill.step2.body": "用自然语言描述要暴露哪个端口和访问规则",
    "skill.step3.kicker": "Review",
    "skill.step3.body": "涉及 expose、apply、发布等状态变更时先确认",
    "skill.step4.kicker": "Operate",
    "skill.step4.body": "让 AI 处理 doctor、logs、metrics 和 cleanup",
    "install.eyebrow": "Install",
    "install.title": "AI 用 Skill，人用 CLI，两条路径都很短。",
    "install.bodyA": "先装 Skill，让 AI 会用 Sealtun；需要自己操作时，再用 npm 或 npx 安装 CLI。",
    "install.bodyB": "团队共享入口可以写进",
    "install.bodyC": "，用 dry-run 和 diff 先看清变更。",
    "tabs.skill": "AI Skill",
    "tabs.policy": "访问策略",
    "tabs.yaml": "声明式",
    "copy.default": "复制",
    "copy.success": "已复制",
    "copy.error": "复制失败",
    "cta.eyebrow": "Built for local services that need a real entrance",
    "cta.title": "把本地开发服务放到公网时，入口、权限和回收都应该清楚。",
    "cta.button": "View source",
    "footer.tagline": "Localhost tunnels built for Sealos Cloud.",
    "footer.release": "Latest release"
  },
  en: {
    metaTitle: "Sealtun | Sealos native localhost tunnels",
    metaDescription: "Sealtun exposes local services through Sealos Cloud with AI Skill support, access policies, custom domains, declarative config, and diagnostics.",
    skip: "Skip to content",
    "nav.capabilities": "Capabilities",
    "nav.skills": "Skills",
    "nav.install": "Install",
    "nav.cta": "Get Started",
    "hero.badgeSource": "100% source available",
    "hero.title": "Sealos-native tunnels your AI can use.",
    "hero.lead": "Install the Sealtun Skill with one command, then ask your AI agent to create public previews, add access control, write declarative config, and troubleshoot without memorizing CLI flags first.",
    "hero.primary": "Install Skill",
    "hero.secondary": "CLI install",
    "trust.skill": "AI Skill install",
    "trust.https": "Public HTTPS",
    "trust.policy": "Access policies",
    "trust.domain": "Custom domains",
    "cap.eyebrow": "What it does",
    "cap.title": "A short, controlled path from localhost to a public Sealos entrance.",
    "cap.card1.title": "Create a native Sealos entrance",
    "cap.card1.body": "Expose a local port after login. Sealtun uses Kubernetes resources on the remote side to serve a public HTTPS tunnel endpoint.",
    "cap.card2.title": "Run access policy at the proxy layer",
    "cap.card2.body": "Basic Auth, Bearer Token, IP allow / deny rules, and temporary links are enforced by the Sealtun proxy instead of informal team agreements.",
    "cap.card3.title": "Reuse declarative config and diagnostics",
    "cap.card3.body": "supports dry-run, diff, and apply. doctor, logs, metrics, and dashboard keep troubleshooting on one path.",
    "skill.title": "You do not have to learn Sealtun first. Let AI learn it.",
    "skill.bodyA": "After installing this repository as a Skill, your AI agent understands Sealtun's CLI, config, access policies, diagnostics, and release workflow.",
    "skill.bodyB": "Describe the outcome: expose localhost, add Basic Auth, write sealtun.yaml, run doctor. The agent handles the tool details.",
    "skill.step1.kicker": "Install",
    "skill.step1.body": "Add the Skill to your AI workflow with one npx command",
    "skill.step2.kicker": "Ask",
    "skill.step2.body": "Describe the port and access rules in natural language",
    "skill.step3.kicker": "Review",
    "skill.step3.body": "Confirm state-changing actions such as expose, apply, and release",
    "skill.step4.kicker": "Operate",
    "skill.step4.body": "Let AI work through doctor, logs, metrics, and cleanup",
    "install.eyebrow": "Install",
    "install.title": "Skill for AI, CLI for humans. Both paths stay short.",
    "install.bodyA": "Install the Skill first so AI can use Sealtun. When you want direct control, install the CLI with npm or npx.",
    "install.bodyB": "Shared tunnel definitions can live in",
    "install.bodyC": ", with dry-run and diff before changes are applied.",
    "tabs.skill": "AI Skill",
    "tabs.policy": "Access policy",
    "tabs.yaml": "Declarative",
    "copy.default": "Copy",
    "copy.success": "Copied",
    "copy.error": "Copy failed",
    "cta.eyebrow": "Built for local services that need a real entrance",
    "cta.title": "When localhost goes public, the entrance, permissions, and cleanup should stay explicit.",
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
