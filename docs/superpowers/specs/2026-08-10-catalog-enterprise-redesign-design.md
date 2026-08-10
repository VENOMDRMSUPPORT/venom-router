# Design Spec: Enterprise Catalog Dashboard Redesign

**Date:** 2026-08-10  
**Status:** Draft for Review  
**Topic:** Catalog Dashboard Enterprise Redesign with 3D Glassmorphism & Official Brand Logos

---

## 1. Overview & Objectives

Rebuild the catalog dashboard in `catalog/` from the ground up to replace the primitive layout with a true Enterprise-grade SaaS dashboard experience:
- **Design Aesthetic:** Enterprise Dark Neumorphic & 3D Glassmorphism (Option 1). Deep slate/navy background (`#070a11` / `#0d1322`), glowing borders, frosted glass blur, floating 3D perspective hover cards, and smooth CSS keyframe micro-animations.
- **Brand Identity:** High-resolution 3D glowing metallic logo (`assets/catalog-3d-logo.jpg`) with futuristic typography.
- **Provider Logos:** Integration and optimization of all original brand logos sourced directly from `dashboard/public/providers/` and `catalog/assets/` (`opencode`, `clinepass`, `ollama-cloud`, `antigravity`, `claude-code`, `github-copilot`, `gemini-cli`, `codex`, `nvidia-nim`, `xai`, `agnes-ai`). Contrast-protected containers ensure flawless visibility across both Dark and Light themes.
- **Responsiveness:** Full multi-device responsiveness across Desktop (1440px+), Laptop (1024px), Tablet (768px), and Mobile (360px - 480px) with adaptive grid, collapsible mobile drawer navigation, and touch-optimized controls.

---

## 2. Key Features & Components

### 2.1 Enterprise Branding & Hero Section
- **3D Header:** Floating 3D glowing logo badge with glowing border rings (`catalog-3d-logo.jpg`).
- **Live Statistics KPI Ribbon:** 4 key metric cards (Total Verified Models, Max Context Window, Multimodal Providers, Free vs Subscription Breakdown) with gradient counters and trend indicators.

### 2.2 Live Toolbar (Search, Filter, View Toggles)
- **Real-time Search Bar:** Client-side live filtering of provider cards and model matrices.
- **Filter Chips:** Categories (All, Free Tier, Subscription, 1M+ Context, Multimodal).
- **View Switcher:** Toggle between **3D Grid Cards** view and **Enterprise Data Table** view.

### 2.3 3D Glassmorphism Cards & Interactive Elements
- **3D Perspective Hover:** Dynamic CSS/JS tilt effect (`transform: perspective(1000px) rotateX(...) rotateY(...) scale(1.02)`).
- **Logo Container & Theme Protection:** Styled image frames with frosted backdrop and adaptive drop-shadows preventing logos from disappearing on dark or light backgrounds.
- **Model Badges & Status Signals:** Highlighting context length (1M+, 500k, 128k), modality badges (Vision, Audio, Code), and real-time status indicators.

### 2.4 Theme System & Light/Dark Compatibility
- Enterprise Dark Mode (default): `#070a11` deep background with `#121829` glass panels and glowing accents (`#38bdf8` cyan, `#c084fc` purple, `#34d399` emerald).
- Enterprise Light Mode: `#f8fafc` crisp background with `#ffffff` glass panels and subtle soft depth shadows.
- Theme Toggle: Smooth transition for all tokens and backdrop filters without layout shift.

### 2.5 Provider Subpages Uniformity
- Unified layout and design tokens across:
  - `catalog/index.html` (Main Catalog Dashboard)
  - `catalog/opencode-zen.html`
  - `catalog/opencode-go.html`
  - `catalog/clinepass.html`
  - `catalog/ollama-cloud.html`

---

## 3. Architecture & File Strategy

- **`catalog/styles.css`**: Complete rewrite to establish the Enterprise Design System, CSS tokens, 3D tilt effects, glassmorphic styles, responsive break points, and dark/light variables.
- **`catalog/index.html`**: Complete structure overhaul with modern HTML5 semantics (`<header>`, `<main>`, `<aside>`, `<nav>`, `<section>`), search bar, live filters, 3D cards, metrics ribbon, methodology, and drawer navigation.
- **`catalog/theme.js`**: Enhanced script adding live search/filter logic, 3D tilt hover physics, view mode toggling, and theme switching.
- **`catalog/assets/`**: Updated with official PNG/SVG logos copied from `dashboard/public/providers/` + the new 3D logo asset `catalog-3d-logo.jpg`.
- **Subpages (`catalog/*.html`)**: Styled with the new Enterprise sidebar, header, badges, and responsive tables.

---

## 4. Verification & Testing Plan

1. **Visual & UI Verification:**
   - Verify 3D hover effects, glassmorphism blur, and glow shaders.
   - Check dark/light theme switching on all provider logos.
2. **Responsive Layout Testing:**
   - Desktop (1440px): 3-column grid layout with sticky sidebar.
   - Tablet (768px): 2-column grid layout with mobile drawer toggle.
   - Mobile (375px): 1-column responsive card layout with collapsible mobile menu.
3. **Functional Verification:**
   - Live search input filters cards instantly.
   - Category filter buttons update visible cards smoothly.
   - View mode switcher toggles between Grid and Table views flawlessly.
