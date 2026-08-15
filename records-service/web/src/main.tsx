import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import { App } from './App';
import './styles.css';

const mount = document.getElementById('root');
if (!mount) {
  throw new Error('the console has no mount point');
}

createRoot(mount).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
