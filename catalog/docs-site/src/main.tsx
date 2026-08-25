import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import App from './App';

const basename = (import.meta.env.BASE_URL || '/').replace(/\/$/, '') || undefined;

document.documentElement.dataset.theme = 'dark';

document.title = 'Venom Catalog Docs';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter basename={basename}>
      <App />
    </BrowserRouter>
  </StrictMode>,
);
