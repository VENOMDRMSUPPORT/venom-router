import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import App from './App';
import { CatalogProvider } from './hooks/useCatalog';
import { ThemeProvider } from './hooks/useTheme';
import './index.css';

const isDocsRoute = window.location.pathname === '/docs' || window.location.pathname.startsWith('/docs/');

function Root() {
  const app = <App />;
  return (
    <StrictMode>
      <BrowserRouter>
        <ThemeProvider>
          {isDocsRoute ? app : <CatalogProvider>{app}</CatalogProvider>}
        </ThemeProvider>
      </BrowserRouter>
    </StrictMode>
  );
}

createRoot(document.getElementById('root')!).render(<Root />);
