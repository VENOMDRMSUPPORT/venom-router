import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import App from './App';
import { CatalogProvider } from './hooks/useCatalog';
import { ThemeProvider } from './hooks/useTheme';
import './index.css';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      {/* One catalog fetch for the whole app: the dashboard and the provider
          pages read the same payload, so their counts cannot disagree. */}
      <CatalogProvider>
        <ThemeProvider>
          <App />
        </ThemeProvider>
      </CatalogProvider>
    </BrowserRouter>
  </StrictMode>,
);
