import React from 'react';
import { useNavigate, useLocation } from 'react-router-dom';
import '../styles/Header.css';

function Header() {
  const navigate = useNavigate();
  const location = useLocation();
  const userEmail = localStorage.getItem('userEmail');

  const handleLogout = () => {
    localStorage.removeItem('userEmail');
    localStorage.removeItem('accessToken');
    navigate('/');
  };

  // Don't show header on login/setup pages
  if (!userEmail || location.pathname === '/' || location.pathname === '/setup' || location.pathname === '/auth/callback') {
    return null;
  }

  return (
    <header className="app-header">
      <div className="header-left">
        <h1 className="logo" onClick={() => navigate('/inbox')}>
          Mailsorter
        </h1>
        <nav className="main-nav">
          <button
            className={`nav-link ${location.pathname === '/inbox' ? 'active' : ''}`}
            onClick={() => navigate('/inbox')}
          >
            Inbox
          </button>
          <button
            className={`nav-link ${location.pathname === '/settings' ? 'active' : ''}`}
            onClick={() => navigate('/settings')}
          >
            Settings
          </button>
        </nav>
      </div>
      <div className="header-right">
        <span className="user-email">{userEmail}</span>
        <button onClick={handleLogout} className="btn-logout">
          Logout
        </button>
      </div>
    </header>
  );
}

export default Header;
