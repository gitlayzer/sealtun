const translations = {
  zh: {
    metaTitle: "Sealtun | 一条命令，把本地服务送上公网",
    metaDescription: "Sealtun 是 Sealos Cloud 原生的隧道 CLI：一条命令把本地 Web、SSH、数据库暴露到公网，内置认证、域名、限流、审计和声明式 YAML。",
    "nav.badge": "Tunnel CLI",
    "nav.quickstart": "快速上手",
    "nav.releases": "发布页",
    "hero.title": "一条命令，<br>把本地服务送上公网。",
    "hero.lead": "Sealtun 把本地 Web、SSH、数据库变成公网入口。认证、域名、限流、审计、声明式 YAML，全在这一个二进制里。",
    "hero.download": "立即下载",
    "hero.github": "查看 GitHub",
    "hero.stars": "—",
    "hero.statDownloads": "安装包累计下载",
    "hero.statStars": "GitHub Stars",
    "community.discussions": "GitHub Discussions",
    "footer.docs": "文档",
    "footer.releases": "发布页",
    "footer.disclaimer": "Sealtun 是独立的开源隧道工具，与 Sealos 官方无隶属关系。各上游组件遵循其各自的许可与商标政策。",
    "footer.built": "Built for the Sealos ecosystem",
  },
  en: {
    metaTitle: "Sealtun | One command, localhost goes public",
    metaDescription: "Sealtun is a Sealos Cloud-native tunnel CLI: one command publishes local web apps, SSH, and databases to the internet, with built-in auth, domains, rate limiting, audit, and declarative YAML.",
    "nav.badge": "Tunnel CLI",
    "nav.quickstart": "Quickstart",
    "nav.releases": "Releases",
    "hero.title": "One command,<br>localhost goes public.",
    "hero.lead": "Sealtun turns local web apps, SSH, and databases into public endpoints. Auth, domains, rate limiting, audit, and declarative YAML — all in a single binary.",
    "hero.download": "Download",
    "hero.github": "View on GitHub",
    "hero.stars": "—",
    "hero.statDownloads": "Installer downloads",
    "hero.statStars": "GitHub Stars",
    "community.discussions": "GitHub Discussions",
    "footer.docs": "Docs",
    "footer.releases": "Releases",
    "footer.disclaimer": "Sealtun is an independent open-source tunnel tool, not affiliated with Sealos. Upstream components follow their own licenses and trademark policies.",
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

  const label = document.getElementById("langLabel");
  if (label) label.textContent = currentLang === "zh" ? "中文" : "EN";

  // Re-applying translations resets the star/download counters to their
  // placeholder; refetch them so toggling language never wipes live stats.
  loadStats();
}

const toggle = document.getElementById("langToggle");
if (toggle) {
  toggle.addEventListener("click", () => {
    currentLang = currentLang === "zh" ? "en" : "zh";
    applyTranslations();
    try { localStorage.setItem("sealtun-lang", currentLang); } catch (_) {}
  });
}

function formatCompact(n) {
  if (n >= 1000) {
    const v = (n / 1000).toFixed(1).replace(/\.0$/, "");
    return v + "K";
  }
  return String(n);
}

async function loadStats() {
  // GitHub stars
  try {
    const res = await fetch("https://api.github.com/repos/gitlayzer/sealtun");
    if (res.ok) {
      const data = await res.json();
      const stars = data.stargazers_count;
      const text = formatCompact(stars);
      const starBtn = document.getElementById("ghStars");
      if (starBtn) starBtn.textContent = text;
      const starStat = document.getElementById("ghStarsStat");
      if (starStat) starStat.textContent = stars.toLocaleString("en-US");
    }
  } catch (_) {}

  // Total installer downloads across all GitHub release assets
  try {
    const res = await fetch("https://api.github.com/repos/gitlayzer/sealtun/releases?per_page=100");
    if (res.ok) {
      const releases = await res.json();
      let total = 0;
      for (const rel of releases) {
        for (const asset of rel.assets || []) {
          total += asset.download_count || 0;
        }
      }
      const el = document.getElementById("npmDownloads");
      if (el && total > 0) {
        el.textContent = total.toLocaleString("en-US");
      }
    }
  } catch (_) {}
}

const yearEl = document.getElementById("year");
if (yearEl) yearEl.textContent = String(new Date().getFullYear());

try {
  const saved = localStorage.getItem("sealtun-lang");
  if (saved && translations[saved]) currentLang = saved;
} catch (_) {}

applyTranslations();
loadStats();
