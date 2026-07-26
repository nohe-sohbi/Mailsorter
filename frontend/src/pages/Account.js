import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { accountService } from '../services/api';
import { useToast } from '../ui/Toast';
import { cn } from '../ui/cn';
import { track } from '../lib/analytics';
import Spinner from '../ui/Spinner';
import { Shield, Trash, Alert, Bolt, Settings as SettingsIcon, Check } from '../ui/icons';

const ACTION_LABELS = { archive: 'Archivés', delete: 'Supprimés', label: 'Étiquetés', keep: 'Gardés' };
const ACTION_COLORS = {
  archive: 'bg-sky-500',
  delete: 'bg-rose-500',
  label: 'bg-amber-500',
  keep: 'bg-emerald-500',
};

// Go zero-value dates come back as year 1, which would render as "1 janvier 1".
// Treat anything before Mailsorter existed as "unknown" rather than printing it.
function formatJoinDate(iso) {
  if (!iso) return null;
  const d = new Date(iso);
  if (Number.isNaN(d.getTime()) || d.getFullYear() < 2020) return null;
  return d.toLocaleDateString('fr-FR', { day: 'numeric', month: 'long', year: 'numeric' });
}

// RGPD controls: export everything Mailsorter stores about you (portability),
// and permanently erase your account and all derived data (right to erasure).
// Your Gmail mailbox is never touched, only the artifacts Mailsorter created.
// These belong to the account, which is why they live here rather than in the
// preferences screen.
function PrivacyData() {
  const toast = useToast();
  const [exporting, setExporting] = useState(false);
  const [confirm, setConfirm] = useState('');
  const [deleting, setDeleting] = useState(false);

  const exportData = async () => {
    setExporting(true);
    try {
      const { data } = await accountService.exportData();
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `mailsorter-export-${new Date().toISOString().slice(0, 10)}.json`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
      track('data_export');
      toast.success('Export téléchargé.');
    } catch (err) {
      toast.error('Export impossible.');
    } finally {
      setExporting(false);
    }
  };

  const deleteAccount = async () => {
    setDeleting(true);
    try {
      await accountService.deleteAccount();
      toast.success('Compte et données supprimés. À bientôt.');
      localStorage.removeItem('accessToken');
      localStorage.removeItem('userEmail');
      setTimeout(() => window.location.assign('/'), 800);
    } catch (err) {
      toast.error('Suppression impossible.');
      setDeleting(false);
    }
  };

  return (
    <div className="card animate-fade-up mt-6 p-7">
      <div className="mb-1 flex items-center gap-2">
        <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-brand-50 text-brand-600">
          <Shield size={18} />
        </span>
        <h2 className="text-lg font-bold text-ink-900">Données &amp; confidentialité</h2>
      </div>
      <p className="mb-5 text-sm text-ink-500">
        Vos emails ne quittent jamais votre contrôle. Récupérez tout ce que Mailsorter stocke à votre sujet,
        ou effacez définitivement votre compte.
      </p>

      <div className="flex flex-wrap items-center gap-3">
        <button onClick={exportData} disabled={exporting} className="btn-secondary">
          {exporting ? <Spinner size={16} /> : <Shield size={16} />} Exporter mes données
        </button>
        <span className="text-xs text-ink-400">Un fichier JSON : règles, expéditeurs protégés, historique, réglages…</span>
      </div>

      <div className="mt-6 rounded-xl border border-rose-200 bg-rose-50/60 p-5">
        <div className="mb-1 flex items-center gap-2 text-rose-700">
          <Alert size={16} />
          <h3 className="text-sm font-bold">Zone de danger</h3>
        </div>
        <p className="mb-4 text-sm text-rose-600/90">
          La suppression efface définitivement votre compte et toutes vos données Mailsorter (règles, protections,
          reports, historique). Action <span className="font-semibold">irréversible</span>. Votre boîte Gmail n'est pas affectée.
        </p>
        <div className="flex flex-wrap items-center gap-3">
          <input
            className="input max-w-[220px] border-rose-200"
            placeholder="Tapez SUPPRIMER"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            aria-label="Confirmation de suppression"
          />
          <button
            onClick={deleteAccount}
            disabled={deleting || confirm.trim().toUpperCase() !== 'SUPPRIMER'}
            className="btn-danger"
          >
            {deleting ? <Spinner size={16} /> : <Trash size={16} />} Supprimer mon compte
          </button>
        </div>
      </div>
    </div>
  );
}

// Account answers "who am I on this instance": identity, since when, on which
// plan, how much of it is used, and what Mailsorter has done lately. It is what
// clicking your own address in the header should have opened all along.
function Account() {
  const navigate = useNavigate();
  const [profile, setProfile] = useState(null);
  const [usage, setUsage] = useState(null);
  const [activity, setActivity] = useState(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!localStorage.getItem('userEmail')) {
      navigate('/');
      return;
    }
    let alive = true;
    Promise.allSettled([
      accountService.getProfile(),
      accountService.getUsage(),
      accountService.getActivity(),
    ]).then(([p, u, a]) => {
      if (!alive) return;
      if (p.status === 'fulfilled') setProfile(p.value.data);
      if (u.status === 'fulfilled') setUsage(u.value.data);
      if (a.status === 'fulfilled') setActivity(a.value.data);
      setLoading(false);
    });
    return () => {
      alive = false;
    };
  }, [navigate]);

  if (loading) {
    return (
      <div className="flex min-h-[60vh] items-center justify-center">
        <Spinner size={28} className="text-brand-500" />
      </div>
    );
  }

  const email = profile?.email || localStorage.getItem('userEmail') || '';
  const initial = (email[0] || '?').toUpperCase();
  const isPro = (profile?.plan || usage?.plan) === 'pro';
  const joinedOn = formatJoinDate(profile?.createdAt);
  const usedPct = usage && usage.limit > 0 ? Math.min(100, Math.round((usage.used / usage.limit) * 100)) : 0;
  const maxDay = activity ? Math.max(1, ...activity.days.map((d) => d.count)) : 1;

  return (
    <div className="mx-auto max-w-3xl px-4 py-10 sm:px-6">
      <div className="mb-8 flex flex-wrap items-center gap-4">
        <span className="flex h-14 w-14 items-center justify-center rounded-2xl bg-brand-600 text-xl font-bold text-white">
          {initial}
        </span>
        <div className="min-w-0">
          <h1 className="truncate font-display text-2xl font-extrabold tracking-tight text-ink-900">{email}</h1>
          <p className="text-sm text-ink-500">
            {joinedOn ? `Membre depuis le ${joinedOn}.` : 'Compte Mailsorter.'}
          </p>
        </div>
      </div>

      <div className="card animate-fade-up p-7">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-lg font-bold text-ink-900">Votre plan</h2>
          <span className={cn('chip', isPro ? 'bg-brand-600 text-white' : 'bg-ink-100 text-ink-600')}>
            {isPro ? (
              <>
                <Bolt size={13} /> Pro
              </>
            ) : (
              'Free'
            )}
          </span>
        </div>

        <div className="mb-2 flex items-baseline gap-1.5">
          <span className="font-display text-3xl font-extrabold text-ink-900">{usage?.used ?? 0}</span>
          <span className="text-sm text-ink-500">
            {isPro ? 'emails analysés ce mois · illimité' : `/ ${usage?.limit ?? 200} emails analysés ce mois`}
          </span>
        </div>
        <div className="h-2.5 w-full overflow-hidden rounded-full bg-ink-100">
          <div
            className={cn(
              'h-full rounded-full transition-all duration-500',
              isPro ? 'bg-emerald-500' : usedPct >= 100 ? 'bg-rose-500' : 'bg-brand-600'
            )}
            style={{ width: isPro ? '100%' : `${usedPct}%` }}
          />
        </div>
        <p className="mt-3 text-xs text-ink-400">
          {usage?.period ? `Période ${usage.period}. ` : ''}
          Le cache et l'auto-pilote ne consomment pas votre quota.
        </p>

        <div className="mt-5 flex flex-wrap gap-3">
          <button onClick={() => navigate('/pricing')} className="btn-secondary">
            {isPro ? <Check size={16} /> : <Bolt size={16} />} {isPro ? 'Voir les tarifs' : 'Découvrir Pro'}
          </button>
          <button onClick={() => navigate('/settings')} className="btn-ghost">
            <SettingsIcon size={16} /> Réglages
          </button>
        </div>
      </div>

      <div className="card animate-fade-up mt-6 p-7">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-lg font-bold text-ink-900">Cette semaine</h2>
          <span className="text-sm text-ink-500">
            <span className="font-display text-xl font-extrabold text-ink-900">{activity?.total ?? 0}</span> emails triés
          </span>
        </div>

        <div className="flex h-24 items-end gap-1.5">
          {(activity?.days || []).map((d) => (
            <div key={d.date} className="flex flex-1 flex-col items-center gap-1.5">
              <div
                className="w-full rounded-t-md bg-brand-500/80 transition-all"
                style={{ height: `${Math.max(4, (d.count / maxDay) * 72)}px` }}
                title={`${d.count} le ${d.date}`}
              />
              <span className="text-[10px] text-ink-400">{d.date.slice(8)}</span>
            </div>
          ))}
        </div>

        {activity && Object.keys(activity.byAction || {}).length > 0 && (
          <div className="mt-5 flex flex-wrap gap-4">
            {Object.entries(activity.byAction).map(([action, count]) => (
              <span key={action} className="flex items-center gap-2 text-sm text-ink-600">
                <span className={cn('h-2.5 w-2.5 rounded-full', ACTION_COLORS[action] || 'bg-ink-300')} />
                {ACTION_LABELS[action] || action}
                <span className="font-semibold text-ink-900">{count}</span>
              </span>
            ))}
          </div>
        )}

        <button onClick={() => navigate('/history')} className="btn-ghost mt-5 px-0">
          Voir l'historique complet
        </button>
      </div>

      <PrivacyData />
    </div>
  );
}

export default Account;
