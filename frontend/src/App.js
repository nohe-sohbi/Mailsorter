import React, { useEffect, useState } from 'react';
import { BrowserRouter as Router, Routes, Route, Navigate } from 'react-router-dom';
import Login from './pages/Login';
import Emails from './pages/Emails';
import Rules from './pages/Rules';
import Setup from './pages/Setup';
import Settings from './pages/Settings';
import AuthCallback from './pages/AuthCallback';
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
      <div className="App">
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
            path="/emails"
            element={
              isConfigured ? <Emails /> : <Navigate to="/setup" replace />
            }
          />
          <Route
            path="/rules"
            element={
              isConfigured ? <Rules /> : <Navigate to="/setup" replace />
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
        </Routes>
      </div>
    </Router>
  );
}

export default App;
