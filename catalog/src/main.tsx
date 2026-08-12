import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import App from './App';
import { CatalogProvider } from './hooks/useCatalog';
import './index.css';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      {/* One catalog fetch for the whole app: the dashboard and the provider
          pages read the same payload, so their counts cannot disagree. */}
      <CatalogProvider>
        <App />
      </CatalogProvider>
    </BrowserRouter>
  </StrictMode>,
);
