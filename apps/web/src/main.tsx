import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './styles/app.css'
import './styles/dashboard.css'
import './styles/mobile.css'
import './styles/brand.css' // identity tokens + fonts: last, so they win
import App from './App'
import { detectSource } from './api/paths'

// One build for the Pi and the cloud: learn which server served us before the first render.
void detectSource().then(() => createRoot(document.getElementById('root')!).render(<StrictMode><App /></StrictMode>))
