import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useInstance } from '../contexts/InstanceContext';
import { configService } from '../services/api';
import { useTheme } from '../ui/theme';
import { isAuthed } from '../lib/session';
import { useAuthForm } from '../lib/useAuthForm';
import { useHotelFonts } from '../ui/hotel/useHotelFonts';
import HotelHeader, { floorsFor } from '../components/landing/HotelHeader';
import ElevatorRail from '../components/landing/ElevatorRail';
import HotelFooter from '../components/landing/HotelFooter';
import { useActiveFloor } from '../components/landing/useActiveFloor';
import { scrollToId } from '../components/landing/scroll';
import { useUnreadCounter } from '../components/landing/useUnreadCounter';
import HallHero from '../components/landing/HallHero';
import FeatureCorridor from '../components/landing/FeatureCorridor';
import TriageDemo from '../components/landing/TriageDemo';
import PrivacyVault from '../components/landing/PrivacyVault';
import ProviderBoard from '../components/landing/ProviderBoard';
import RateCard from '../components/landing/RateCard';
import FaqBoard from '../components/landing/FaqBoard';
import NightExit from '../components/landing/NightExit';
import '../styles/hotel.css';
import '../components/landing/landing.css';

// The "Grand Hotel" landing, served on / when UI_THEME=hotel (lib/uiTheme.js).
// It owns the auth form state so the header, the hall, the demo and the night
// exit all open the same form, in the right mode.
export default function HotelLanding() {
  const navigate = useNavigate();
  const { isConfigured, selfHosted, billingOn } = useInstance();
  const { isDark } = useTheme();
  const count = useUnreadCounter();
  useHotelFonts();
  const auth = useAuthForm((to) => navigate(to));
  const floors = useMemo(() => floorsFor(selfHosted), [selfHosted]);
  const floorNo = useMemo(() => Object.fromEntries(floors.map((f) => [f.id, f.n])), [floors]);
  const active = useActiveFloor();

  // The mailbox catalog, fetched once here because two floors read it: the key
  // board draws it, and the FAQ answers "Outlook ?" from it. null while it
  // loads; `failed` tells the board to say so rather than stay empty.
  const [providers, setProviders] = useState(null);
  const [providersFailed, setProvidersFailed] = useState(false);
  useEffect(() => {
    let cancelled = false;
    configService
      .getProviders()
      .then(({ data }) => {
        if (!cancelled) setProviders(data.providers || []);
      })
      .catch(() => {
        if (!cancelled) setProvidersFailed(true);
      });
    return () => {
      cancelled = true;
    };
  }, []);

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
      <div className="hl-floors">
        <HallHero count={count} auth={auth} isConfigured={isConfigured} night={isDark} />
        <TriageDemo count={count} isConfigured={isConfigured} floorNo={floorNo.fonctionnement} onStart={() => openAuth('register')} />
        <FeatureCorridor isConfigured={isConfigured} />
        <PrivacyVault floorNo={floorNo.confidentialite} isConfigured={isConfigured} />
        <ProviderBoard isConfigured={isConfigured} providers={providers} failed={providersFailed} />
        {!selfHosted && <RateCard floorNo={floorNo.tarifs} billingOn={billingOn} />}
        <FaqBoard floorNo={floorNo.questions} isConfigured={isConfigured} selfHosted={selfHosted} billingOn={billingOn} providers={providers} />
        <NightExit count={count} onStart={() => openAuth('register')} onSignIn={() => openAuth('login')} />
      </div>
      <HotelFooter />
    </div>
  );
}
