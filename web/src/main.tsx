import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import './styles/base.css'
import './styles/lookout.css'
import './styles/watches.css'
import './styles/form.css'
import './styles/changes.css'
import './styles/app.css'
import './styles/settings.css'
import { AppProvider, useApp } from './lib/app-context'
import { Loading, ToastProvider } from './components/ui'
import Layout from './components/Layout'
import { ErrorBoundary } from './components/ErrorBoundary'
import Login from './pages/Login'
import Watches from './pages/Watches'
import WatchPage from './pages/WatchPage'
import AddWatch from './pages/AddWatch'
import Changes from './pages/Changes'
import ChangePage from './pages/ChangePage'
import Settings from './pages/Settings'

function Gate({ children }: { children: React.ReactNode }) {
  const { status, user } = useApp()
  const loc = useLocation()
  if (!status) return <div className="boot"><img src="/favicon.svg" alt="" /><Loading /></div>
  if (status.needs_setup) return <Navigate to="/setup" replace />
  if (!user) return <Navigate to="/login" replace state={{ from: loc.pathname + loc.search }} />
  return <>{children}</>
}

function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login mode="login" />} />
      <Route path="/setup" element={<Login mode="setup" />} />
      <Route path="/settings/:tab?" element={<Gate><ErrorBoundary where="settings"><Settings /></ErrorBoundary></Gate>} />
      <Route element={<Gate><ErrorBoundary where="lookout"><Layout /></ErrorBoundary></Gate>}>
        <Route path="/" element={<Watches />} />
        {/* /add?url=… works from a bookmarklet or the phone's share sheet */}
        <Route path="/add" element={<AddWatch />} />
        <Route path="/watches/:id" element={<WatchPage />} />
        <Route path="/watches/:id/edit" element={<AddWatch />} />
        <Route path="/changes" element={<Changes />} />
        <Route path="/changes/:id" element={<ChangePage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <ErrorBoundary where="app"><ToastProvider><AppProvider><App /></AppProvider></ToastProvider></ErrorBoundary>
    </BrowserRouter>
  </React.StrictMode>,
)
