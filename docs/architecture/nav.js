// The page list lives HERE ONLY — the one place a page is declared.
// Adding a page = one entry. Nav, active-marking, and prev/next all derive from it.
const PAGES = [
  { href: "index.html",          title: "Overview" },
  { href: "network.html",        title: "The Network" },
  { href: "clock-and-wire.html", title: "Clock & Wire" },
  { href: "content-buffer.html", title: "Content Buffer" },
  { href: "polar-layout.html",   title: "Polar Layout" },
  { href: "ts-editor.html",      title: "TS Editor" },
  { href: "ai-friendliness.html",title: "AI-Friendliness" },
  { href: "model-divergence.html", title: "Model Divergence" },
  { href: "drift-log.html",      title: "Drift Log" },
  { href: "plan.html",           title: "Plan" },
];

const here = (() => {
  const f = location.pathname.split("/").pop();
  return !f || f === "" ? "index.html" : f;
})();

const nav = document.getElementById("tabs");
if (nav) {
  for (const p of PAGES) {
    const a = document.createElement("a");
    a.href = p.href;
    a.textContent = p.title;
    if (p.href === here) a.classList.add("active");
    nav.appendChild(a);
  }
}

// prev/next pager, derived from the same list
const i = PAGES.findIndex((p) => p.href === here);
const pager = document.getElementById("pager");
if (pager && i !== -1) {
  const prev = PAGES[i - 1];
  const next = PAGES[i + 1];
  if (prev) {
    const a = document.createElement("a");
    a.href = prev.href;
    a.textContent = "← " + prev.title;
    pager.appendChild(a);
  }
  const sp = document.createElement("span");
  sp.className = "spacer";
  pager.appendChild(sp);
  if (next) {
    const a = document.createElement("a");
    a.href = next.href;
    a.textContent = next.title + " →";
    pager.appendChild(a);
  }
}
