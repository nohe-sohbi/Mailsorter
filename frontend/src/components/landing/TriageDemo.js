import React, { useEffect, useRef, useState } from 'react';
import Door from '../../ui/hotel/Door';
import FloorHeading from './FloorHeading';
import { canPromise } from './features';
import { formatCount } from './useUnreadCounter';
import { prefersReducedMotion } from './scroll';
import { track } from '../../lib/analytics';
import './TriageDemo.css';

// One mail at a time, like the step-by-step triage of the inbox: a real pile
// where the next mail is already visible, a validated card that flies into its
// door, and an Annuler that brings it back out. Made-up mails, no network. A
// mail whose action this instance cannot perform is left out (features.js).
const MAILS = [
  { from: 'Le Monde', av: 'LM', time: '07:02', subj: 'La lettre du matin', snip: 'Les cinq informations à retenir ce jeudi, et un éditorial que vous ne lirez pas non plus.', act: 'Archiver', to: 'archives', done: 'Archivé.' },
  { from: 'Free Mobile', av: 'FM', time: '08:15', subj: 'Votre facture de septembre est disponible', snip: 'Montant : 19,99 €. Prélèvement le 5 octobre.', act: 'Ranger dans Factures', to: 'factures', done: 'Rangé dans Factures.', feature: 'label' },
  { from: 'Zalando', av: 'Z', time: '09:30', subj: '-40 % ce week-end seulement', snip: "Comme le week-end dernier. Et celui d'avant.", act: 'Se désabonner', to: 'desabo', done: 'Désabonné.', feature: 'unsubscribe' },
  { from: 'SNCF Connect', av: 'SN', time: '10:11', subj: 'Votre billet Paris - Lyon du 2 octobre', snip: 'Voiture 14, place 62. Départ 08:04, gare de Lyon.', act: 'Reporter à jeudi, 8 h', to: 'later', done: 'Reporté à jeudi, 8 h.', feature: 'snooze' },
  { from: 'Maman', av: 'M', time: '11:48', subj: 'Pour dimanche, on dit midi ?', snip: 'Et tu ramènes le dessert. Pas comme la dernière fois.', safe: true },
];

const DOORS = [
  { to: 'archives', label: 'Archives', color: '#2F6F73' },
  { to: 'factures', label: 'Factures', color: '#7A2E3B', feature: 'label' },
  { to: 'desabo', label: 'Désabonnements', color: '#E48FA5', feature: 'unsubscribe' },
  { to: 'later', label: 'Plus tard', color: '#C98B22', feature: 'snooze' },
];

const EMPTY = { archives: 0, factures: 0, desabo: 0, later: 0 };

const Star = () => (
  <svg width="30" height="30" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M12 3l2.2 5.6L20 9.5l-4.4 3.8 1.4 5.9L12 16.2 7 19.2l1.4-5.9L4 9.5l5.8-.9z" />
  </svg>
);

const Shield = () => (
  <svg width="30" height="30" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M12 3l8 3v6c0 4.5-3.4 8-8 9-4.6-1-8-4.5-8-9V6z" />
    <path d="M8.5 12l2.5 2.5 4.5-5" />
  </svg>
);

export default function TriageDemo({ count, isConfigured, floorNo, onStart }) {
  const canUndo = canPromise('undo', isConfigured);
  const mails = MAILS.filter((m) => !m.feature || canPromise(m.feature, isConfigured));
  const doors = DOORS.filter((d) => !d.feature || canPromise(d.feature, isConfigured));

  const [out, setOut] = useState({}); // card index -> transform it left with
  const [log, setLog] = useState([]); // [{ i, to }], most recent last
  const [counts, setCounts] = useState(EMPTY);
  const [bumped, setBumped] = useState(null);
  const [toast, setToast] = useState(null); // { msg, undoable }
  const cardRefs = useRef([]);
  const doorRefs = useRef({});
  const timers = useRef([]);

  useEffect(() => () => timers.current.forEach(clearTimeout), []);
  useEffect(() => {
    if (!toast) return undefined;
    const timer = setTimeout(() => setToast(null), 5000);
    return () => clearTimeout(timer);
  }, [toast]);

  const later = (fn, ms) => {
    timers.current.push(setTimeout(fn, ms));
  };

  // The pile is every card not yet sent away, the end card last.
  const gone = new Set(log.map((l) => l.i));
  const pile = [...mails.keys(), mails.length].filter((i) => !gone.has(i));
  const depth = (i) => {
    const d = pile.indexOf(i);
    if (d < 0) return 'gone';
    return d > 2 ? 'far' : String(d);
  };

  const bump = (to, delta) => {
    setCounts((c) => ({ ...c, [to]: Math.max(0, c[to] + delta) }));
    setBumped(to);
    later(() => setBumped(null), 450);
  };

  const leave = (i, transform, to) => {
    setOut((o) => ({ ...o, [i]: transform }));
    setLog((l) => [...l, { i, to }]);
  };

  const validate = (i) => {
    const m = mails[i];
    const card = cardRefs.current[i];
    const door = doorRefs.current[m.to];
    let transform = 'translateY(120px) scale(.1)';
    if (card && door) {
      const a = card.getBoundingClientRect();
      const b = door.getBoundingClientRect();
      const dx = b.left + b.width / 2 - (a.left + a.width / 2);
      const dy = b.top + b.height / 3 - a.top;
      transform = `translate(${dx}px, ${dy}px) scale(.08) rotate(8deg)`;
    }
    leave(i, transform, m.to);
    later(() => {
      bump(m.to, 1);
      setToast({ msg: m.done, undoable: canUndo });
    }, prefersReducedMotion() ? 0 : 480);
    track('landing_demo', { action: 'validate' });
  };

  const keep = (i) => {
    leave(i, 'translateX(-160px) rotate(-8deg)', null);
    later(() => setToast({ msg: 'Gardé dans la boîte de réception.', undoable: canUndo }), 250);
    track('landing_demo', { action: 'skip' });
  };

  // Maman's card: nothing happened to her, so there is nothing to undo either.
  const next = (i) => {
    leave(i, 'translateY(40px) scale(.96)', null);
    setToast(null);
  };

  const undo = () => {
    const last = log[log.length - 1];
    if (!last) return;
    setLog((l) => l.slice(0, -1));
    setOut((o) => {
      const rest = { ...o };
      delete rest[last.i];
      return rest;
    });
    if (last.to) bump(last.to, -1);
    setToast({ msg: "Annulé. Comme si de rien n'était.", undoable: false });
    track('landing_demo', { action: 'undo' });
  };

  // A "Valider" less than 480 ms old still has its door bump and its toast
  // queued: left running, they would land on the fresh pile.
  const restart = () => {
    timers.current.forEach(clearTimeout);
    timers.current = [];
    setBumped(null);
    setLog([]);
    setOut({});
    setCounts(EMPTY);
    setToast(null);
  };

  const cardProps = (i) => {
    const d = depth(i);
    return {
      ref: (el) => {
        cardRefs.current[i] = el;
      },
      className: `hl-mail${d === 'gone' ? ' is-gone' : ''}`,
      'data-depth': d,
      style: out[i] ? { transform: out[i], opacity: 0 } : undefined,
      inert: d !== '0' ? '' : undefined,
      'aria-hidden': d !== '0' ? 'true' : undefined,
    };
  };

  return (
    <section className="hl-section hl-section--alt" id="fonctionnement" data-floor="fonctionnement" aria-labelledby="hl-demo-title">
      <div className="hl-wrap">
        <FloorHeading
          n={floorNo}
          kicker="Fonctionnement"
          id="hl-demo-title"
          lede={`Pour chaque e-mail, Mailsorter vous dit ce qu'il en ferait. Vous validez ou vous passez.${canUndo ? " Et si vous changez d'avis, Annuler est juste là." : ''}`}
        >
          Un mail, une question, <em>un clic.</em>
        </FloorHeading>
        <div className="hl-stage">
          <p className="hl-try">Démo interactive</p>
          <div className="hl-deck">
            {mails.map((m, i) => (
              <article key={m.from} {...cardProps(i)}>
                <header className="hl-mail__head">
                  <span className="hl-mail__av">{m.av}</span>
                  <div className="hl-mail__who">
                    <b>{m.from}</b>
                    <span>Aujourd'hui, {m.time}</span>
                  </div>
                  <span className="hl-mail__left">{mails.length - i} dans la pile</span>
                </header>
                <h3 className="hl-mail__subj">{m.subj}</h3>
                <p className="hl-mail__snip">{m.snip}</p>
                {m.safe ? (
                  <>
                    <div className="ht-prop is-safe">
                      <Shield />
                      <div>
                        <small>Expéditrice protégée</small>
                        <b>Mailsorter n'y touche pas.</b>
                      </div>
                    </div>
                    <div className="hl-mail__acts">
                      <button type="button" className="ht-btn ht-btn-primary" onClick={() => next(i)}>
                        Suivant
                      </button>
                    </div>
                  </>
                ) : (
                  <>
                    <div className="ht-prop">
                      <Star />
                      <div>
                        <small>Mailsorter propose</small>
                        <b>{m.act}</b>
                      </div>
                    </div>
                    <div className="hl-mail__acts">
                      <button type="button" className="ht-btn ht-btn-primary" onClick={() => validate(i)}>
                        Valider
                      </button>
                      <button type="button" className="ht-btn ht-btn-secondary" onClick={() => keep(i)}>
                        Garder tel quel
                      </button>
                    </div>
                  </>
                )}
              </article>
            ))}
            <article {...cardProps(mails.length)} className={`${cardProps(mails.length).className} hl-mail--done`}>
              <h3 className="hl-mail__subj">
                Pile vide. Plus que <span className="hl-count">{formatCount(count)}</span> chez vous.
              </h3>
              <p className="hl-mail__snip">Ceux-là, on ne peut pas les trier sans vous.</p>
              <div className="hl-mail__acts">
                <button type="button" className="ht-btn ht-btn-primary" onClick={onStart}>
                  Faire le tri
                </button>
              </div>
              <button type="button" className="hl-mail__again" onClick={restart}>
                Recommencer la démo
              </button>
            </article>
            <div className={`ht-toast hl-toast${toast ? ' is-on' : ''}`} role="status" aria-live="polite">
              {toast && (
                <>
                  {toast.msg}
                  {toast.undoable && (
                    <button type="button" onClick={undo}>
                      Annuler
                    </button>
                  )}
                </>
              )}
            </div>
          </div>
          <div className="hl-mdoors">
            {doors.map((d) => (
              <div
                key={d.to}
                className={`hl-md${bumped === d.to ? ' is-bumped' : ''}`}
                data-to={d.to}
                ref={(el) => {
                  doorRefs.current[d.to] = el;
                }}
              >
                <span className="ht-badge hl-md__badge">{counts[d.to]}</span>
                <Door color={d.color} crop className="hl-md__door" />
                <b>{d.label}</b>
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  );
}
