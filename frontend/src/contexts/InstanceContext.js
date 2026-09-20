import React, { createContext, useCallback, useContext, useEffect, useState } from 'react';
import { configService } from '../services/api';

// What this deployment is, fetched once at boot and shared.
//
// Four screens need it and none of them should probe again at boot: the boot
// gate (is there an OAuth client at all), the header (is there anything to
// bill), the pricing page (plans, or an explanation of why there are none), and
// setup (whose instance is the Google project). Before this, App and Pricing
// each called the probe separately and could disagree about the same instance
// for a few hundred milliseconds. Setup still calls it directly from its
// "I have filled the variables" button, which is a deliberate re-check on
// demand rather than a second boot probe.
//
// `edition` is the one that changes behaviour rather than copy. A self-hosted
// instance has no billing and no account with anyone, so every paid surface is
// not "empty" but absent.
const InstanceContext = createContext(null);

export function InstanceProvider({ children }) {
  const [instance, setInstance] = useState(null);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    // Back to the boot screen while the probe runs. Clearing only the error
    // would leave the stale instance on screen, and the guarded routes would
    // render against a deployment that may have changed underneath them.
    setInstance(null);
    setError('');
    try {
      const { data } = await configService.getStatus();
      setInstance(data);
    } catch (err) {
      setError('Connexion au serveur impossible.');
      // A shape, not null: consumers branch on `loading` for "not known yet",
      // and this is "known, and nothing works". Defaulting edition to
      // self-hosted is the safe half of the guess: it hides paid surfaces
      // rather than offering a checkout against a server that is not answering.
      setInstance({ isConfigured: false, mailboxSignIn: false, billingOn: false, edition: 'self-hosted' });
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const value = {
    loading: instance === null,
    error,
    reload: load,
    isConfigured: !!instance?.isConfigured,
    mailboxSignIn: !!instance?.mailboxSignIn,
    // Whether this deployment has ANY way in. It used to be the Gmail
    // credentials alone, which was true while Google was the only door; an
    // instance reachable only over IMAP was sent to a setup screen telling it
    // to create a Google Cloud project it can never use.
    isUsable: !!instance?.isConfigured || !!instance?.mailboxSignIn,
    billingOn: !!instance?.billingOn,
    edition: instance?.edition || 'self-hosted',
    selfHosted: (instance?.edition || 'self-hosted') === 'self-hosted',
  };

  return <InstanceContext.Provider value={value}>{children}</InstanceContext.Provider>;
}

export function useInstance() {
  const ctx = useContext(InstanceContext);
  if (!ctx) {
    throw new Error('useInstance must be used inside an InstanceProvider');
  }
  return ctx;
}
