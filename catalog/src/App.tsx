import { useState } from 'react';
import { Routes, Route, useLocation } from 'react-router-dom';
import { useTheme } from './hooks/useTheme';
import { Sidebar } from './components/Sidebar/Sidebar';
import { ThemeToggle } from './components/ThemeToggle/ThemeToggle';
import { MobileMenu } from './components/MobileMenu/MobileMenu';
import { DashboardPage } from './pages/DashboardPage/DashboardPage';
import { ProviderPage } from './pages/ProviderPage/ProviderPage';
import { ChangesPage } from './pages/ChangesPage/ChangesPage';
import { NotFoundPage } from './pages/NotFoundPage/NotFoundPage';

export default function App() {
  const { theme, toggleTheme } = useTheme();
  const [menuOpen, setMenuOpen] = useState(false);
  const location = useLocation();

  // Close the mobile drawer whenever the route changes.
  const handleNavigate = () => setMenuOpen(false);

  return (
    <div className="app-layout">
      <ThemeToggle theme={theme} onToggle={toggleTheme} />
      <MobileMenu onClick={() => setMenuOpen((v) => !v)} />
      <Sidebar open={menuOpen} onClose={handleNavigate} />
      <main className="app-main" key={location.pathname}>
        <Routes>
          <Route path="/" element={<DashboardPage />} />
          <Route path="/provider/:id" element={<ProviderPage />} />
          <Route path="/changes" element={<ChangesPage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Routes>
      </main>
    </div>
  );
}
