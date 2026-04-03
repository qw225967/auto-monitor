import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Routes, Route } from 'react-router-dom'
import './index.css'
import './App.css'
import { Layout } from './components/Layout'
import { ArbitrageMonitor } from './pages/ArbitrageMonitor'
import { Opportunities } from './pages/Opportunities'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <Layout>
        <Routes>
          <Route path="/" element={<ArbitrageMonitor />} />
          <Route path="/opportunities" element={<Opportunities />} />
        </Routes>
      </Layout>
    </BrowserRouter>
  </StrictMode>,
)
