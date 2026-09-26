import React from 'react';
import Spinner from '../../ui/Spinner';
import { Google } from '../../ui/icons';
import './SignupCard.css';

// The hall's registration card. State and side effects come from useAuthForm,
// shared with the classic landing; this component only decides how it looks.
// Field ids and the [data-auth-switch] toggle match the classic form's, so
// both landings answer to the same selectors.
export default function SignupCard({ auth, isConfigured }) {
  const register = auth.mode === 'register';
  const locked = auth.busy || auth.googleBusy;
  return (
    <form className="hl-fiche" id="auth-card" onSubmit={auth.submit}>
      <div className="hl-fiche__top">
        <h2>{register ? 'Créer un compte' : 'Se connecter'}</h2>
        {register && <span>gratuit</span>}
      </div>
      <label className="ht-label" htmlFor="auth-email">
        Adresse e-mail
      </label>
      <input
        id="auth-email"
        className="ht-field"
        type="email"
        required
        autoComplete="email"
        value={auth.email}
        onChange={(e) => auth.setEmail(e.target.value)}
        placeholder="vous@exemple.fr"
      />
      <label className="ht-label" htmlFor="auth-password">
        Mot de passe
      </label>
      <input
        id="auth-password"
        className="ht-field"
        type="password"
        required
        minLength={register ? 8 : undefined}
        autoComplete={register ? 'new-password' : 'current-password'}
        value={auth.password}
        onChange={(e) => auth.setPassword(e.target.value)}
        placeholder={register ? '8 caractères minimum' : 'Votre mot de passe'}
      />
      <button type="submit" className="ht-btn ht-btn-primary hl-fiche__go" disabled={locked}>
        {auth.busy ? <Spinner size={16} /> : null}
        {auth.busy ? 'Un instant' : register ? 'Faire le tri' : 'Entrer'}
      </button>
      {isConfigured && (
        <button type="button" className="ht-btn hl-fiche__google" onClick={auth.startGoogle} disabled={locked}>
          {auth.googleBusy ? <Spinner size={16} /> : <Google size={18} />}
          Continuer avec Google
        </button>
      )}
      {auth.error && (
        <p className="hl-fiche__error" role="alert">
          {auth.error}
        </p>
      )}
      <p className="hl-fiche__alt">
        {register ? 'Déjà un compte ?' : 'Pas encore de compte ?'}{' '}
        <button type="button" data-auth-switch={register ? 'login' : 'register'} onClick={() => auth.setMode(register ? 'login' : 'register')}>
          {register ? 'Se connecter' : 'Créer un compte'}
        </button>
      </p>
      <p className="hl-fiche__fine">Gratuit jusqu'à 200 tris par mois. Sans carte bancaire.</p>
    </form>
  );
}
