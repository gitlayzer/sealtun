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

document.querySelectorAll(".reveal").forEach(el => revealObserver.observe(el));

const tabs = document.querySelectorAll("[data-target]");
const panes = document.querySelectorAll("[data-pane]");
let currentPane = "mac";

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
  const original = button.textContent;
  try {
    await navigator.clipboard.writeText(active.textContent.trim());
    button.textContent = "已复制";
  } catch (_) {
    button.textContent = "复制失败";
  }
  window.setTimeout(() => {
    button.textContent = original;
  }, 1400);
});
