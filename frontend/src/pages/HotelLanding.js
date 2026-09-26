import React, { useCallback, useEffect, useMemo } from 'react';
import { useNavigate } from 'react-router-dom';
import { useInstance } from '../contexts/InstanceContext';
import { isAuthed } from '../lib/session';
import { useAuthForm } from '../lib/useAuthForm';
import { useHotelFonts } from '../ui/hotel/useHotelFonts';
import HotelHeader, { floorsFor } from '../components/landing/HotelHeader';
import ElevatorRail from '../components/landing/ElevatorRail';
import HotelFooter from '../components/landing/HotelFooter';
import { useActiveFloor } from '../components/landing/useActiveFloor';
import { scrollToId } from '../components/landing/scroll';
import '../styles/hotel.css';
import '../components/landing/landing.css';

// The "Grand Hotel" landing, served on / when UI_THEME=hotel (lib/uiTheme.js).
// It owns the auth form state so the header, the hall, the demo and the night
// exit all open the same form, in the right mode.
export default function HotelLanding() {
  const navigate = useNavigate();
  const { selfHosted } = useInstance();
  useHotelFonts();
  const auth = useAuthForm((to) => navigate(to));
  const floors = useMemo(() => floorsFor(selfHosted), [selfHosted]);
  const active = useActiveFloor();

  // Same rule as the classic landing: someone already signed in has nothing
  // to do here.
  useEffect(() => {
    if (isAuthed()) navigate(localStorage.getItem('hasMailbox') ? '/inbox' : '/connect');
  }, [navigate]);

  const { setMode } = auth;
  const openAuth = useCallback(
    (mode) => {
      setMode(mode);
      scrollToId('hall');
      window.setTimeout(() => {
        const field = document.getElementById('auth-email');
        if (field) field.focus({ preventScroll: true });
      }, 350);
    },
    [setMode]
  );

  return (
    <div className="theme-hotel hl-page" data-landing="hotel">
      <HotelHeader floors={floors} active={active} onSignIn={() => openAuth('login')} />
      <ElevatorRail />
      <div className="hl-floors" />
      <HotelFooter />
    </div>
  );
}
