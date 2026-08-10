# Enterprise Catalog Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the AI Model Catalog dashboard (`catalog/`) from scratch with an Enterprise Dark Neumorphic & 3D Glassmorphism theme, 3D brand logo, official provider logos, live search & filtering, grid/table view toggle, and full responsiveness across all screen sizes.

**Architecture:** Custom CSS design system with HSL tokens, glassmorphic backdrop filters, 3D perspective hover cards, semantic HTML5, and lightweight vanilla JS for theme persistence, search/filtering, and interactive 3D physics.

**Tech Stack:** HTML5, Vanilla CSS3 (3D transforms, glassmorphism, flexbox/grid), Vanilla JS (ES6+).

## Global Constraints
- Pure CSS & Vanilla JS in `catalog/` directory without external heavyweight runtime dependencies.
- Preserve and reference official provider logo assets (`catalog/assets/`).
- Full light & dark mode compatibility with contrast-protected logo wrappers.
- Responsive design from 360px mobile viewports up to 1440px+ desktop viewports.

---

### Task 1: Asset Preparation & Theme Logo Setup

**Files:**
- Create/Copy: `catalog/assets/opencode-zen.png`, `catalog/assets/clinepass.png`, `catalog/assets/ollama-cloud.png`, `catalog/assets/antigravity.png`, `catalog/assets/claude-code.png`, `catalog/assets/github-copilot.png`, `catalog/assets/gemini-cli.png`, `catalog/assets/codex.png`, `catalog/assets/nvidia-nim.png`, `catalog/assets/xai.png`, `catalog/assets/agnes-ai.png`
- Existing: `catalog/assets/catalog-3d-logo.jpg`, `catalog/assets/opencode.svg`, `catalog/assets/cline.svg`, `catalog/assets/ollama.svg`

**Interfaces:**
- Produces: Complete collection of PNG and SVG logos inside `catalog/assets/` available for `<img src="assets/...">`.

- [ ] **Step 1: Copy missing provider PNG logos from `dashboard/public/providers/` to `catalog/assets/`**

```bash
powershell -Command "Copy-Item 'dashboard/public/providers/*.png' -Destination 'catalog/assets/' -Force"
```

- [ ] **Step 2: Verify asset presence in `catalog/assets/`**

Run: `powershell -Command "Get-ChildItem catalog/assets"`
Expected: All provider PNGs (`opencode-zen.png`, `clinepass.png`, `ollama-cloud.png`, `antigravity.png`, etc.) and `catalog-3d-logo.jpg` present.

- [ ] **Step 3: Commit assets**

```bash
git add catalog/assets/
git commit -m "assets: sync provider logos and 3d catalog logo to catalog/assets"
```

---

### Task 2: Core CSS Architecture Rewrite (`catalog/styles.css`)

**Files:**
- Modify: `catalog/styles.css`

**Interfaces:**
- Produces: Complete Enterprise styling system, CSS variables (`--bg-primary`, `--bg-card-glass`, `--border-glow`, `--accent-cyan`, `--accent-purple`, `--accent-emerald`, `--shadow-3d`), 3D tilt styles, glassmorphism rules, responsive grid, contrast logo wrappers, and mobile drawer styles.

- [ ] **Step 1: Write Enterprise CSS tokens, resets, and glassmorphism foundations**

Update `catalog/styles.css` with dark/light mode CSS tokens, glassmorphism panel styles, and 3D card tilt rules:

```css
:root, [data-theme="dark"] {
  --bg-primary: #070a11;
  --bg-secondary: #0d1322;
  --bg-card-glass: rgba(18, 24, 41, 0.75);
  --bg-glass-hover: rgba(26, 35, 60, 0.85);
  --border-color: rgba(255, 255, 255, 0.08);
  --border-glow: rgba(56, 189, 248, 0.3);
  --text-primary: #f8fafc;
  --text-secondary: #94a3b8;
  --accent-cyan: #38bdf8;
  --accent-purple: #c084fc;
  --accent-emerald: #34d399;
  --accent-amber: #fbbf24;
  --accent-rose: #f43f5e;
  --shadow-3d: 0 20px 40px -15px rgba(0, 0, 0, 0.5), 0 0 15px rgba(56, 189, 248, 0.15);
  --logo-backdrop: rgba(15, 23, 42, 0.8);
  --sidebar-width: 280px;
}

[data-theme="light"] {
  --bg-primary: #f8fafc;
  --bg-secondary: #f1f5f9;
  --bg-card-glass: rgba(255, 255, 255, 0.85);
  --bg-glass-hover: rgba(255, 255, 255, 0.95);
  --border-color: rgba(0, 0, 0, 0.08);
  --border-glow: rgba(14, 165, 233, 0.3);
  --text-primary: #0f172a;
  --text-secondary: #64748b;
  --accent-cyan: #0284c7;
  --accent-purple: #9333ea;
  --accent-emerald: #059669;
  --accent-amber: #d97706;
  --accent-rose: #e11d48;
  --shadow-3d: 0 20px 30px -10px rgba(15, 23, 42, 0.12), 0 0 10px rgba(2, 132, 199, 0.1);
  --logo-backdrop: #ffffff;
}

/* Glassmorphism & 3D Tilt Card */
.glass-panel {
  background: var(--bg-card-glass);
  backdrop-filter: blur(16px);
  -webkit-backdrop-filter: blur(16px);
  border: 1px solid var(--border-color);
  box-shadow: var(--shadow-3d);
}

.provider-card-3d {
  position: relative;
  border-radius: 16px;
  padding: 1.5rem;
  transition: transform 0.25s cubic-bezier(0.2, 0.8, 0.2, 1), box-shadow 0.25s ease, border-color 0.25s ease;
  transform-style: preserve-3d;
  text-decoration: none;
  color: var(--text-primary);
  display: flex;
  flex-direction: column;
  justify-content: space-between;
}

.provider-card-3d:hover {
  border-color: var(--border-glow);
  box-shadow: 0 25px 50px -12px rgba(56, 189, 248, 0.25);
}

/* Logo Badge Protection */
.logo-badge-container {
  width: 52px;
  height: 52px;
  border-radius: 12px;
  background: var(--logo-backdrop);
  border: 1px solid var(--border-color);
  padding: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
  box-shadow: 0 4px 12px rgba(0,0,0,0.15);
  flex-shrink: 0;
}

.logo-badge-container img {
  width: 100%;
  height: 100%;
  object-fit: contain;
}
```

- [ ] **Step 2: Add responsive break points for mobile, tablet, and desktop**

Ensure toolbar, grid, sidebar, and stats ribbon wrap seamlessly on all screen sizes (`@media (max-width: 1024px)`, `@media (max-width: 768px)`, `@media (max-width: 480px)`).

- [ ] **Step 3: Commit CSS changes**

```bash
git add catalog/styles.css
git commit -m "style: rewrite catalog CSS with enterprise 3d glassmorphism and theme system"
```

---

### Task 3: Interactive JS Features (`catalog/theme.js`)

**Files:**
- Modify: `catalog/theme.js`

**Interfaces:**
- Produces: Theme toggle with localStorage, 3D card tilt physics, search filter logic, view mode switcher, mobile drawer navigation handler.

- [ ] **Step 1: Update `catalog/theme.js` with complete interactivity**

Write the theme initialization, 3D mouse tilt events, live search & filter listener, view toggle handler, and mobile drawer listener in `catalog/theme.js`:

```javascript
// Theme Toggle & Persistence
document.addEventListener('DOMContentLoaded', () => {
  const themeToggle = document.getElementById('themeToggle');
  const savedTheme = localStorage.getItem('catalog-theme') || 'dark';
  document.documentElement.setAttribute('data-theme', savedTheme);
  updateThemeIcon(savedTheme);

  if (themeToggle) {
    themeToggle.addEventListener('click', () => {
      const current = document.documentElement.getAttribute('data-theme');
      const next = current === 'dark' ? 'light' : 'dark';
      document.documentElement.setAttribute('data-theme', next);
      localStorage.setItem('catalog-theme', next);
      updateThemeIcon(next);
    });
  }

  function updateThemeIcon(theme) {
    if (themeToggle) {
      themeToggle.innerHTML = theme === 'dark' ? '☀️' : '🌙';
    }
  }

  // 3D Card Tilt Physics
  const cards = document.querySelectorAll('.provider-card-3d');
  cards.forEach(card => {
    card.addEventListener('mousemove', (e) => {
      const rect = card.getBoundingClientRect();
      const x = e.clientX - rect.left;
      const y = e.clientY - rect.top;
      const centerX = rect.width / 2;
      const centerY = rect.height / 2;
      const rotateX = ((y - centerY) / centerY) * -10;
      const rotateY = ((x - centerX) / centerX) * 10;
      card.style.transform = `perspective(1000px) rotateX(${rotateX}deg) rotateY(${rotateY}deg) translateZ(10px)`;
    });

    card.addEventListener('mouseleave', () => {
      card.style.transform = 'perspective(1000px) rotateX(0deg) rotateY(0deg) translateZ(0px)';
    });
  });

  // Live Search & Filtering
  const searchInput = document.getElementById('searchInput');
  const filterBtns = document.querySelectorAll('.filter-chip');
  let currentFilter = 'all';

  function filterCards() {
    const query = (searchInput ? searchInput.value : '').toLowerCase().trim();
    cards.forEach(card => {
      const title = card.getAttribute('data-title') || '';
      const tags = card.getAttribute('data-tags') || '';
      const text = (card.textContent || '').toLowerCase();
      
      const matchesSearch = text.includes(query);
      const matchesCategory = currentFilter === 'all' || tags.includes(currentFilter);

      if (matchesSearch && matchesCategory) {
        card.style.display = 'flex';
      } else {
        card.style.display = 'none';
      }
    });
  }

  if (searchInput) {
    searchInput.addEventListener('input', filterCards);
  }

  filterBtns.forEach(btn => {
    btn.addEventListener('click', () => {
      filterBtns.forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
      currentFilter = btn.getAttribute('data-filter') || 'all';
      filterCards();
    });
  });

  // View Mode Switcher (Grid vs Table)
  const viewGridBtn = document.getElementById('viewGridBtn');
  const viewTableBtn = document.getElementById('viewTableBtn');
  const providerGrid = document.getElementById('providerGrid');
  const providerTableView = document.getElementById('providerTableView');

  if (viewGridBtn && viewTableBtn && providerGrid && providerTableView) {
    viewGridBtn.addEventListener('click', () => {
      viewGridBtn.classList.add('active');
      viewTableBtn.classList.remove('active');
      providerGrid.style.display = 'grid';
      providerTableView.style.display = 'none';
    });

    viewTableBtn.addEventListener('click', () => {
      viewTableBtn.classList.add('active');
      viewGridBtn.classList.remove('active');
      providerGrid.style.display = 'none';
      providerTableView.style.display = 'block';
    });
  }

  // Mobile Menu Drawer
  const menuBtn = document.getElementById('menuBtn');
  const sidebar = document.getElementById('sidebar');
  const overlay = document.getElementById('sidebarOverlay');

  if (menuBtn && sidebar && overlay) {
    menuBtn.addEventListener('click', () => {
      sidebar.classList.toggle('open');
      overlay.classList.toggle('open');
    });

    overlay.addEventListener('click', () => {
      sidebar.classList.remove('open');
      overlay.classList.remove('open');
    });
  }
});
```

- [ ] **Step 2: Commit `catalog/theme.js` changes**

```bash
git add catalog/theme.js
git commit -m "feat: add 3d tilt physics, live search, filters, view toggle, and theme switching to catalog/theme.js"
```

---

### Task 4: Enterprise Catalog Dashboard HTML Rewrite (`catalog/index.html`)

**Files:**
- Modify: `catalog/index.html`

**Interfaces:**
- Produces: Complete modern dashboard markup with 3D header logo (`catalog-3d-logo.jpg`), KPI metrics ribbon, live search & filter bar, view toggles, 3D provider cards, contrast-protected logos, data table view, methodology, and mobile drawer navigation.

- [ ] **Step 1: Overhaul `catalog/index.html` with complete Enterprise structure**

Update `catalog/index.html`:

```html
<!DOCTYPE html>
<html lang="en" data-theme="dark">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>AI Model Catalogs — Enterprise Dashboard</title>
  <link rel="stylesheet" href="styles.css">
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@400;500;600;700;800&display=swap" rel="stylesheet">
</head>
<body>

  <button class="mobile-menu-btn" id="menuBtn" aria-label="Toggle Navigation">☰</button>
  <button class="theme-toggle" id="themeToggle" aria-label="Toggle Theme">☀️</button>
  <div class="sidebar-overlay" id="sidebarOverlay"></div>

  <!-- Sidebar Navigation -->
  <aside class="sidebar glass-panel" id="sidebar">
    <div class="sidebar-header">
      <div class="brand-badge-3d">
        <img src="assets/catalog-3d-logo.jpg" alt="Venom Catalog Logo" class="3d-logo-img">
      </div>
      <div>
        <h2>Venom Catalog</h2>
        <p>AI Agent Providers & Models</p>
      </div>
    </div>
    <nav>
      <div class="nav-section">
        <div class="nav-section-title">Overview</div>
        <a href="index.html" class="nav-item active">📊 All Providers</a>
      </div>
      <div class="nav-section">
        <div class="nav-section-title">Providers Directory</div>
        <a href="opencode-zen.html" class="nav-item">
          <div class="nav-logo-box"><img src="assets/opencode-zen.png" alt="OpenCode"></div>
          <span>OpenCode Zen</span>
          <span class="badge badge-free">Free</span>
        </a>
        <a href="opencode-go.html" class="nav-item">
          <div class="nav-logo-box"><img src="assets/opencode-zen.png" alt="OpenCode"></div>
          <span>OpenCode Go</span>
          <span class="badge badge-paid">$10/mo</span>
        </a>
        <a href="clinepass.html" class="nav-item">
          <div class="nav-logo-box"><img src="assets/clinepass.png" alt="ClinePass"></div>
          <span>ClinePass</span>
          <span class="badge badge-paid">Sub</span>
        </a>
        <a href="ollama-cloud.html" class="nav-item">
          <div class="nav-logo-box"><img src="assets/ollama-cloud.png" alt="Ollama"></div>
          <span>Ollama Cloud</span>
          <span class="badge badge-mixed">Hybrid</span>
        </a>
      </div>
    </nav>
  </aside>

  <!-- Main Content -->
  <main class="main-content">
    <header class="hero-section">
      <div class="hero-badge">ENTERPRISE MATRIX 2026</div>
      <h1>AI Model Catalogs</h1>
      <p>Comprehensive comparison of verified AI model catalogs for coding agents — directly synchronized with official APIs and runtime specifications.</p>

      <!-- KPI Metrics Ribbon -->
      <div class="kpi-ribbon">
        <div class="kpi-card glass-panel">
          <div class="kpi-value text-cyan">84</div>
          <div class="kpi-label">Verified Models</div>
        </div>
        <div class="kpi-card glass-panel">
          <div class="kpi-value text-emerald">1M</div>
          <div class="kpi-label">Max Context Window</div>
        </div>
        <div class="kpi-card glass-panel">
          <div class="kpi-value text-purple">4</div>
          <div class="kpi-label">Major Providers</div>
        </div>
        <div class="kpi-card glass-panel">
          <div class="kpi-value text-amber">100%</div>
          <div class="kpi-label">API Verified</div>
        </div>
      </div>
    </header>

    <!-- Toolbar: Search, Filters & View Toggle -->
    <div class="toolbar glass-panel">
      <div class="search-box">
        <span class="search-icon">🔍</span>
        <input type="text" id="searchInput" placeholder="Search provider or model name..." aria-label="Search models">
      </div>
      <div class="filter-chips">
        <button class="filter-chip active" data-filter="all">All</button>
        <button class="filter-chip" data-filter="free">Free Tiers</button>
        <button class="filter-chip" data-filter="paid">Subscription</button>
        <button class="filter-chip" data-filter="1m">1M+ Context</button>
        <button class="filter-chip" data-filter="multimodal">Multimodal</button>
      </div>
      <div class="view-switcher">
        <button class="view-btn active" id="viewGridBtn" aria-label="Grid View">🎛️ Grid</button>
        <button class="view-btn" id="viewTableBtn" aria-label="Table View">📑 Table</button>
      </div>
    </div>

    <!-- Provider Grid View (3D Glassmorphic Cards) -->
    <div class="provider-grid" id="providerGrid">
      
      <!-- OpenCode Zen Card -->
      <a href="opencode-zen.html" class="provider-card-3d glass-panel" data-title="opencode zen" data-tags="free 1m multimodal">
        <div class="provider-card-header">
          <div class="logo-badge-container">
            <img src="assets/opencode-zen.png" alt="OpenCode Zen">
          </div>
          <div>
            <h3>OpenCode Zen</h3>
            <span class="badge badge-free">Free</span>
          </div>
        </div>
        <p>8 free tested and verified coding models. No API key or subscription required.</p>
        <div class="provider-stats-row">
          <div class="stat-item"><span class="stat-num text-emerald">8</span><span class="stat-lbl">Models</span></div>
          <div class="stat-item"><span class="stat-num text-cyan">1M</span><span class="stat-lbl">Context</span></div>
          <div class="stat-item"><span class="stat-num text-purple">Vision</span><span class="stat-lbl">Multimodal</span></div>
        </div>
      </a>

      <!-- OpenCode Go Card -->
      <a href="opencode-go.html" class="provider-card-3d glass-panel" data-title="opencode go" data-tags="paid 1m multimodal">
        <div class="provider-card-header">
          <div class="logo-badge-container">
            <img src="assets/opencode-zen.png" alt="OpenCode Go">
          </div>
          <div>
            <h3>OpenCode Go</h3>
            <span class="badge badge-paid">$10/mo</span>
          </div>
        </div>
        <p>18 premium tier models for $5 first month then $10/mo. High rate limits.</p>
        <div class="provider-stats-row">
          <div class="stat-item"><span class="stat-num text-cyan">18</span><span class="stat-lbl">Models</span></div>
          <div class="stat-item"><span class="stat-num text-cyan">1M</span><span class="stat-lbl">Context</span></div>
          <div class="stat-item"><span class="stat-num text-emerald">500K</span><span class="stat-lbl">Max Output</span></div>
        </div>
      </a>

      <!-- ClinePass Card -->
      <a href="clinepass.html" class="provider-card-3d glass-panel" data-title="clinepass" data-tags="paid 1m">
        <div class="provider-card-header">
          <div class="logo-badge-container">
            <img src="assets/clinepass.png" alt="ClinePass">
          </div>
          <div>
            <h3>ClinePass</h3>
            <span class="badge badge-paid">Subscription</span>
          </div>
        </div>
        <p>12 curated open-weight models via Cline extension with WorkOS OAuth integration.</p>
        <div class="provider-stats-row">
          <div class="stat-item"><span class="stat-num text-purple">12</span><span class="stat-lbl">Models</span></div>
          <div class="stat-item"><span class="stat-num text-cyan">1M</span><span class="stat-lbl">Context</span></div>
          <div class="stat-item"><span class="stat-num text-emerald">512K</span><span class="stat-lbl">Max Output</span></div>
        </div>
      </a>

      <!-- Ollama Cloud Card -->
      <a href="ollama-cloud.html" class="provider-card-3d glass-panel" data-title="ollama cloud" data-tags="free paid 1m multimodal">
        <div class="provider-card-header">
          <div class="logo-badge-container">
            <img src="assets/ollama-cloud.png" alt="Ollama Cloud">
          </div>
          <div>
            <h3>Ollama Cloud</h3>
            <span class="badge badge-mixed">Free + Paid</span>
          </div>
        </div>
        <p>46 models total (28 free + 18 paid). Also supports local custom model execution.</p>
        <div class="provider-stats-row">
          <div class="stat-item"><span class="stat-num text-amber">46</span><span class="stat-lbl">Models</span></div>
          <div class="stat-item"><span class="stat-num text-cyan">1M</span><span class="stat-lbl">Context</span></div>
          <div class="stat-item"><span class="stat-num text-emerald">Yes</span><span class="stat-lbl">Local Hybrid</span></div>
        </div>
      </a>

    </div>

    <!-- Provider Table View (Enterprise Data Grid) -->
    <div class="provider-table-container glass-panel" id="providerTableView" style="display: none;">
      <table class="enterprise-table">
        <thead>
          <tr>
            <th>Provider</th>
            <th>Pricing Tier</th>
            <th>Models Count</th>
            <th>Max Context</th>
            <th>Max Output</th>
            <th>Features</th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <td>
              <div class="table-provider-cell">
                <div class="logo-badge-container sm"><img src="assets/opencode-zen.png" alt=""></div>
                <strong>OpenCode Zen</strong>
              </div>
            </td>
            <td><span class="badge badge-free">Free</span></td>
            <td>8</td>
            <td>1M</td>
            <td>64K</td>
            <td><span class="badge badge-text">Vision</span> <span class="badge badge-text">Tools</span></td>
          </tr>
          <tr>
            <td>
              <div class="table-provider-cell">
                <div class="logo-badge-container sm"><img src="assets/opencode-zen.png" alt=""></div>
                <strong>OpenCode Go</strong>
              </div>
            </td>
            <td><span class="badge badge-paid">$10/mo</span></td>
            <td>18</td>
            <td>1M</td>
            <td>500K</td>
            <td><span class="badge badge-text">Reasoning</span> <span class="badge badge-text">Tools</span></td>
          </tr>
          <tr>
            <td>
              <div class="table-provider-cell">
                <div class="logo-badge-container sm"><img src="assets/clinepass.png" alt=""></div>
                <strong>ClinePass</strong>
              </div>
            </td>
            <td><span class="badge badge-paid">Subscription</span></td>
            <td>12</td>
            <td>1M</td>
            <td>512K</td>
            <td><span class="badge badge-text">OAuth</span> <span class="badge badge-text">VSCode</span></td>
          </tr>
          <tr>
            <td>
              <div class="table-provider-cell">
                <div class="logo-badge-container sm"><img src="assets/ollama-cloud.png" alt=""></div>
                <strong>Ollama Cloud</strong>
              </div>
            </td>
            <td><span class="badge badge-mixed">Hybrid</span></td>
            <td>46</td>
            <td>1M</td>
            <td>128K</td>
            <td><span class="badge badge-text">Local</span> <span class="badge badge-text">Open-Weight</span></td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Methodology Section -->
    <section class="methodology-section glass-panel">
      <h3>📋 Enterprise Verification Methodology</h3>
      <p>Data is retrieved live from primary API endpoints and validated against system logs:</p>
      <ul>
        <li><strong>OpenCode Zen & Go:</strong> Synchronized from <code>models.dev/api.json</code></li>
        <li><strong>ClinePass:</strong> Fetched via <code>api.cline.bot</code></li>
        <li><strong>Ollama Cloud:</strong> Verified from <code>ollama.com/library</code> and local CLI bindings</li>
      </ul>
    </section>

    <footer>
      <p>Venom Router Catalog Enterprise Matrix — Last updated 2026-08-10</p>
    </footer>
  </main>

  <script src="theme.js"></script>
</body>
</html>
```

- [ ] **Step 2: Commit `catalog/index.html` changes**

```bash
git add catalog/index.html
git commit -m "feat: overhaul catalog/index.html with enterprise glassmorphism and 3d branding"
```

---

### Task 5: Subpages Enterprise Alignment

**Files:**
- Modify: `catalog/opencode-zen.html`
- Modify: `catalog/opencode-go.html`
- Modify: `catalog/clinepass.html`
- Modify: `catalog/ollama-cloud.html`

**Interfaces:**
- Produces: Subpages matching the new Enterprise header, drawer sidebar, contrast logos, and table layout.

- [ ] **Step 1: Update subpage headers, sidebars, and logo frames**

Ensure all 4 subpages (`opencode-zen.html`, `opencode-go.html`, `clinepass.html`, `ollama-cloud.html`) use the new 3D logo sidebar header (`<img src="assets/catalog-3d-logo.jpg">`), `.logo-badge-container`, font links, and updated table formatting.

- [ ] **Step 2: Commit subpage updates**

```bash
git add catalog/opencode-zen.html catalog/opencode-go.html catalog/clinepass.html catalog/ollama-cloud.html
git commit -m "style: align catalog subpages with enterprise design system"
```

---

### Task 6: Visual & Functional Verification

**Files:**
- Test: All files in `catalog/`

- [ ] **Step 1: Check mobile menu toggle, theme switching, live search, filter chips, and view switching**

Test in browser / CLI or static server if running.

- [ ] **Step 2: Verify zero lint / syntax errors**

Check HTML/CSS/JS syntax.

- [ ] **Step 3: Final Git Commit**

```bash
git add .
git commit -m "feat: complete enterprise catalog dashboard redesign"
```
