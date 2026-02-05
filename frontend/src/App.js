import React, { useEffect, useState } from 'react';
import { BrowserRouter as Router, Routes, Route, Navigate } from 'react-router-dom';
import Login from './pages/Login';
import Inbox from './pages/Inbox';
import Setup from './pages/Setup';
import Settings from './pages/Settings';
import AuthCallback from './pages/AuthCallback';
import Header from './components/Header';
import { EmailProvider } from './contexts/EmailContext';
import { configService } from './services/api';
import './styles/App.css';

function App() {
  const [isConfigured, setIsConfigured] = useState(null);
  const [error, setError] = useState(null);

  useEffect(() => {
    checkConfiguration();
  }, []);

  const checkConfiguration = async () => {
    try {
      const response = await configService.getStatus();
      setIsConfigured(response.data.isConfigured);
    } catch (err) {
      setError('Cannot connect to backend');
      setIsConfigured(false);
    }
  };

  if (isConfigured === null) {
    return (
      <div className="app-loading">
        <div className="spinner"></div>
        <p>Loading...</p>
      </div>
    );
  }

  if (error && !isConfigured) {
    return (
      <div className="app-loading">
        <p className="error">{error}</p>
        <p>Please make sure the backend server is running.</p>
      </div>
    );
  }

  return (
    <Router>
      <EmailProvider>
        <div className="App">
          <Header />
          <Routes>
          <Route
            path="/setup"
            element={
              isConfigured ? (
                <Navigate to="/" replace />
              ) : (
                <Setup onComplete={() => setIsConfigured(true)} />
              )
            }
          />
          <Route
            path="/"
            element={
              isConfigured ? <Login /> : <Navigate to="/setup" replace />
            }
          />
          <Route
            path="/inbox"
            element={
              isConfigured ? <Inbox /> : <Navigate to="/setup" replace />
            }
          />
          <Route
            path="/settings"
            element={
              isConfigured ? <Settings /> : <Navigate to="/setup" replace />
            }
          />
          <Route
            path="/auth/callback"
            element={<AuthCallback />}
          />
          {/* Redirects for old routes */}
          <Route path="/emails" element={<Navigate to="/inbox" replace />} />
          <Route path="/triage" element={<Navigate to="/inbox" replace />} />
        </Routes>
      </div>
    </EmailProvider>
    </Router>
  );
}

export default App;
