import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App, createDefaultAppRouter } from './app/App';
import './styles/tokens.css';

const container = document.getElementById('root');
if (container === null) {
  throw new Error('ANT console: missing #root container');
}

createRoot(container).render(
  <StrictMode>
    <App router={createDefaultAppRouter()} />
  </StrictMode>,
);
