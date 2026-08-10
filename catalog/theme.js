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
      themeToggle.textContent = theme === 'dark' ? '☀️' : '🌙';
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
      const title = (card.getAttribute('data-title') || '').toLowerCase();
      const tags = (card.getAttribute('data-tags') || '').toLowerCase();
      const text = (card.textContent || '').toLowerCase();

      const matchesSearch = !query || text.includes(query) || title.includes(query);
      const matchesCategory = currentFilter === 'all' || tags.includes(currentFilter.toLowerCase());

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

  if (viewGridBtn && providerGrid && providerTableView) {
    viewGridBtn.addEventListener('click', () => {
      viewGridBtn.classList.add('active');
      if (viewTableBtn) viewTableBtn.classList.remove('active');
      providerGrid.style.display = 'grid';
      providerTableView.style.display = 'none';
    });
  }

  if (viewTableBtn && providerGrid && providerTableView) {
    viewTableBtn.addEventListener('click', () => {
      viewTableBtn.classList.add('active');
      if (viewGridBtn) viewGridBtn.classList.remove('active');
      providerGrid.style.display = 'none';
      providerTableView.style.display = 'block';
    });
  }

  // Mobile Menu Drawer
  const menuBtn = document.getElementById('menuBtn');
  const sidebar = document.getElementById('sidebar');
  const overlay = document.getElementById('sidebarOverlay');

  if (menuBtn) {
    menuBtn.addEventListener('click', () => {
      if (sidebar) sidebar.classList.toggle('open');
      if (overlay) overlay.classList.toggle('open');
    });
  }

  if (overlay) {
    overlay.addEventListener('click', () => {
      if (sidebar) sidebar.classList.remove('open');
      if (overlay) overlay.classList.remove('open');
    });
  }
});
