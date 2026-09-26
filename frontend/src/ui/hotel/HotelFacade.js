import React, { useId } from 'react';

// The hotel in cross-section: each room is a category of mail, the elevator
// carries an envelope up and down, the concierge waits at the desk. The night
// version is the same building, windows lit. Colours are fixed on purpose:
// the drawing is the same object by day and by night, only the page around it
// changes. Text styles (hf-*) and the elevator motion live in styles/hotel.css.
const INK = '#2B1B1E';
const PLUM = '#7A2E3B';
const MUST = '#E3A93B';
const TEAL = '#2F6F73';
const CREAM = '#FBF1E4';
const PINK = '#EFB7C3';
const PINK2 = '#F7D6DC';
const CARPET = '#B8586B';
const KRAFT = '#C99A62';
const PAPER = '#FFFAF2';

function Env({ id, x, y, w = 20, h = 14, rotate }) {
  return <use href={`#${id}`} x={x} y={y} width={w} height={h} transform={rotate ? `rotate(${rotate})` : undefined} />;
}

// A room at (x, y), 80 x 92: wallpaper, carpet, brass number plate, label.
function Room({ x, y, n, label, wp, vip = false, children }) {
  const wide = n.length > 3;
  return (
    <g>
      <rect x={x} y={y} width="80" height="92" fill={vip ? PINK2 : `url(#${wp})`} stroke={INK} strokeWidth="1.5" />
      <rect x={x} y={y + 84} width="80" height="8" fill={vip ? PLUM : CARPET} />
      <rect x={x + (wide ? 20 : 23)} y={y + 6} width={wide ? 40 : 34} height="13" rx="2" fill={MUST} stroke={INK} />
      <text x={x + 40} y={y + 15.6} className="hf-num">
        {n}
      </text>
      <text x={x + 40} y={y + 32} className="hf-lab">
        {label}
      </text>
      {children}
    </g>
  );
}

function DayFacade({ uid, title, className }) {
  const wp = `hwp-${uid}`;
  const aw = `haw-${uid}`;
  const ck = `hck-${uid}`;
  const env = `henv-${uid}`;
  return (
    <svg className={className} viewBox="0 0 600 530" role="img" aria-label={title}>
      <defs>
        <pattern id={wp} width="8" height="8" patternUnits="userSpaceOnUse">
          <rect width="8" height="8" fill="#FBEADF" />
          <circle cx="4" cy="4" r="1" fill="#EBB0BD" />
        </pattern>
        <pattern id={aw} width="12" height="12" patternUnits="userSpaceOnUse">
          <rect width="6" height="12" fill={PLUM} />
          <rect x="6" width="6" height="12" fill={CREAM} />
        </pattern>
        <pattern id={ck} width="16" height="16" patternUnits="userSpaceOnUse">
          <rect width="16" height="16" fill={CREAM} />
          <rect width="8" height="8" fill={PLUM} />
          <rect x="8" y="8" width="8" height="8" fill={PLUM} />
        </pattern>
        <symbol id={env} viewBox="0 0 20 14">
          <rect x=".7" y=".7" width="18.6" height="12.6" fill={PAPER} stroke={INK} strokeWidth="1.2" />
          <path d="M1 1.2l9 6.6 9-6.6" fill="none" stroke={INK} strokeWidth="1.2" />
        </symbol>
      </defs>

      <g fill={MUST} stroke={INK} strokeWidth="1">
        <polygon points="36,70 40,62 44,70 40,78" />
        <polygon points="560,86 564,78 568,86 564,94" />
        <polygon points="22,200 25,194 28,200 25,206" />
        <polygon points="576,230 579,224 582,230 579,236" />
      </g>
      <g stroke={INK} strokeWidth="1.5">
        <line x1="88" y1="122" x2="88" y2="74" />
        <polygon points="88,74 110,81 88,88" fill={MUST} />
        <line x1="512" y1="122" x2="512" y2="74" />
        <polygon points="512,74 490,81 512,88" fill={MUST} />
      </g>
      <polygon points="66,124 300,38 534,124" fill={PLUM} stroke={INK} strokeWidth="2" />
      <polygon points="112,116 300,54 488,116" fill={PINK2} stroke={INK} strokeWidth="1.2" />
      <circle cx="300" cy="92" r="18" fill={CREAM} stroke={INK} strokeWidth="2" />
      <g stroke={INK} strokeWidth="1.4" strokeLinecap="round">
        <line x1="300" y1="92" x2="300" y2="80" />
        <line x1="300" y1="92" x2="309" y2="96" />
        <line x1="300" y1="76" x2="300" y2="78" />
        <line x1="316" y1="92" x2="314" y2="92" />
        <line x1="300" y1="108" x2="300" y2="106" />
        <line x1="284" y1="92" x2="286" y2="92" />
      </g>
      <rect x="58" y="122" width="484" height="12" fill={CREAM} stroke={INK} strokeWidth="2" />
      <rect x="80" y="134" width="440" height="366" fill={PINK} stroke={INK} strokeWidth="2" />
      <rect x="128" y="143" width="344" height="26" fill={PLUM} stroke={INK} strokeWidth="1.5" />
      <text x="300" y="160.5" className="hf-sign">
        MAILSORTER
      </text>

      <Room x={96} y={180} n="201" label="NEWSLETTERS" wp={wp}>
        <Env id={env} x={100} y={248} />
        <Env id={env} x={118} y={249} rotate="4 128 256" />
        <Env id={env} x={108} y={236} rotate="-6 118 243" />
        <Env id={env} x={126} y={236} rotate="5 136 243" />
        <Env id={env} x={116} y={223} rotate="-3 126 230" />
        <g transform="rotate(-9 160 244)">
          <rect x="146" y="238" width="28" height="11" fill={PLUM} stroke={INK} />
          <text x="160" y="245.8" className="hf-tiny">
            COMPLET
          </text>
        </g>
        <line x1="160" y1="249" x2="160" y2="264" stroke={INK} strokeWidth="1.2" />
      </Room>
      <Room x={184} y={180} n="202" label="PROMOS" wp={wp}>
        <Env id={env} x={192} y={244} rotate="-16 202 251" />
        <Env id={env} x={222} y={246} rotate="12 232 253" />
        <Env id={env} x={207} y={228} rotate="6 217 235" />
        <g fill={MUST} stroke={INK} strokeWidth=".6">
          <rect x="194" y="222" width="4" height="4" transform="rotate(20 196 224)" />
          <rect x="248" y="230" width="4" height="4" transform="rotate(-25 250 232)" />
          <rect x="240" y="220" width="3" height="3" />
        </g>
        <g fill={TEAL}>
          <circle cx="200" cy="236" r="1.8" />
          <circle cx="252" cy="246" r="1.8" />
          <circle cx="236" cy="226" r="1.5" />
        </g>
      </Room>
      <Room x={336} y={180} n="203" label="TRAVAIL" wp={wp}>
        <rect x="348" y="246" width="54" height="6" fill={PLUM} stroke={INK} />
        <g stroke={INK} strokeWidth="1.4">
          <line x1="352" y1="252" x2="352" y2="264" />
          <line x1="398" y1="252" x2="398" y2="264" />
          <line x1="392" y1="246" x2="392" y2="228" />
        </g>
        <polygon points="384,230 400,230 395,221 389,221" fill={MUST} stroke={INK} />
        <Env id={env} x={356} y={232} />
      </Room>
      <Room x={424} y={180} n="204" label="VOYAGES" wp={wp}>
        <path d="M456 234 v-6 h16 v6" fill="none" stroke={INK} strokeWidth="1.6" />
        <rect x="444" y="234" width="40" height="28" rx="3" fill={TEAL} stroke={INK} strokeWidth="1.4" />
        <circle cx="454" cy="246" r="5" fill={MUST} stroke={INK} strokeWidth=".8" />
        <circle cx="472" cy="252" r="4" fill={PINK} stroke={INK} strokeWidth=".8" />
        <rect x="466" y="238" width="12" height="7" fill={CREAM} stroke={INK} strokeWidth=".8" transform="rotate(8 472 241)" />
      </Room>

      <Room x={96} y={284} n="101" label="FACTURES" wp={wp}>
        <Env id={env} x={106} y={352} w={22} h={15} />
        <Env id={env} x={106} y={343} w={22} h={15} />
        <Env id={env} x={106} y={334} w={22} h={15} />
        <rect x="140" y="336" width="26" height="31" fill="#4A3A3E" stroke={INK} strokeWidth="1.4" />
        <circle cx="153" cy="351" r="6" fill={MUST} stroke={INK} />
        <line x1="153" y1="351" x2="156" y2="347" stroke={INK} strokeWidth="1.2" />
      </Room>
      <Room x={184} y={284} n="102" label="COLIS" wp={wp}>
        <g stroke={INK} strokeWidth="1.3" fill={KRAFT}>
          <rect x="194" y="342" width="26" height="26" />
          <rect x="222" y="348" width="22" height="20" />
          <rect x="204" y="324" width="22" height="18" />
        </g>
        <g stroke={INK} strokeWidth=".9" opacity=".7">
          <line x1="207" y1="342" x2="207" y2="368" />
          <line x1="233" y1="348" x2="233" y2="368" />
          <line x1="215" y1="324" x2="215" y2="342" />
        </g>
      </Room>
      <Room x={336} y={284} n="103" label="PERSO" wp={wp}>
        <Env id={env} x={352} y={348} w={22} h={15} />
        <path d="M363 336 c-3 -4.5 -9 -1 -5.5 3.5 l5.5 5.5 l5.5 -5.5 c3.5 -4.5 -2.5 -8 -5.5 -3.5z" fill={CARPET} stroke={INK} strokeWidth=".9" />
        <path d="M392 368 l2 -14 h12 l2 14z" fill={TEAL} stroke={INK} strokeWidth="1.2" />
        <path d="M400 354 q-2 -10 -8 -14 M400 354 q2 -12 8 -16" fill="none" stroke={INK} strokeWidth="1.1" />
        <circle cx="392" cy="339" r="4" fill={MUST} stroke={INK} strokeWidth=".9" />
        <circle cx="408" cy="337" r="4" fill={PINK} stroke={INK} strokeWidth=".9" />
      </Room>
      <Room x={424} y={284} n="SUITE" label="VIP" wp={wp} vip>
        <polygon points="464,322 466.5,328 473,328 468,332 470,338 464,334.5 458,338 460,332 455,328 461.5,328" fill={MUST} stroke={INK} strokeWidth=".8" />
        <Env id={env} x={453} y={340} w={22} h={15} />
        <g stroke={INK} strokeWidth="1.2" fill={MUST}>
          <rect x="436" y="346" width="5" height="22" />
          <rect x="487" y="346" width="5" height="22" />
        </g>
        <path d="M440 350 Q464 366 488 350" fill="none" stroke={PLUM} strokeWidth="3.2" strokeLinecap="round" />
      </Room>
      {/* Lit when the elevator stops at floor 1 (ht-blink, in step with ht-lift). */}
      <rect className="hf-lamp" x="96" y="284" width="80" height="92" fill="#FFE39A" opacity="0" style={{ mixBlendMode: 'multiply' }} />

      <rect x="276" y="176" width="48" height="204" fill={TEAL} stroke={INK} strokeWidth="1.5" />
      <g stroke={INK} strokeWidth="1.2" opacity=".8">
        <line x1="292" y1="176" x2="292" y2="380" />
        <line x1="308" y1="176" x2="308" y2="380" />
      </g>
      <g className="hf-cab">
        <rect x="282" y="212" width="36" height="54" fill={MUST} stroke={INK} strokeWidth="1.5" />
        <Env id={env} x={290} y={242} />
        <g stroke={INK} strokeWidth=".8" opacity=".75">
          <line x1="288" y1="216" x2="288" y2="262" />
          <line x1="294" y1="216" x2="294" y2="262" />
          <line x1="300" y1="216" x2="300" y2="262" />
          <line x1="306" y1="216" x2="306" y2="262" />
          <line x1="312" y1="216" x2="312" y2="262" />
        </g>
      </g>

      <rect x="96" y="388" width="408" height="100" fill={PINK2} stroke={INK} strokeWidth="1.5" />
      <rect x="96" y="486" width="408" height="14" fill={`url(#${ck})`} stroke={INK} strokeWidth="1.2" />
      <path d="M272 486 V446 A28 28 0 0 1 328 446 V486 Z" fill={TEAL} stroke={INK} strokeWidth="1.5" />
      <g stroke="#9CC9C3" strokeWidth="1.4">
        <line x1="300" y1="420" x2="300" y2="486" />
        <line x1="284" y1="440" x2="316" y2="480" />
      </g>
      <rect x="262" y="398" width="76" height="12" fill={`url(#${aw})`} stroke={INK} strokeWidth="1.2" />
      <g fill={PLUM} stroke={INK} strokeWidth=".8">
        {[266.5, 275.5, 284.5, 293.5, 302.5, 311.5, 320.5, 329.5, 333.5].map((cx) => (
          <circle key={cx} cx={cx} cy="411" r="4.5" />
        ))}
      </g>
      <g stroke={INK} strokeWidth="1.2">
        <path d="M244 486 l3 -16 h14 l3 16z" fill={MUST} />
        <circle cx="254" cy="460" r="11" fill="#4F8E88" />
        <path d="M336 486 l3 -16 h14 l3 16z" fill={MUST} />
        <circle cx="346" cy="460" r="11" fill="#4F8E88" />
      </g>
      <polygon points="160,452 190,452 187,428 163,428" fill={PLUM} stroke={INK} strokeWidth="1.2" />
      <polygon points="170,428 180,428 175,436" fill={CREAM} stroke={INK} strokeWidth=".8" />
      <g fill={MUST}>
        <circle cx="175" cy="441" r="1.6" />
        <circle cx="175" cy="447" r="1.6" />
      </g>
      <circle cx="175" cy="419" r="8.5" fill="#F2C7A6" stroke={INK} strokeWidth="1.2" />
      <rect x="168" y="403.5" width="14" height="8" fill={PLUM} stroke={INK} strokeWidth="1" />
      <rect x="168" y="409" width="14" height="2" fill={MUST} />
      <g fill={INK}>
        <circle cx="172" cy="418" r="1" />
        <circle cx="178" cy="418" r="1" />
      </g>
      <path d="M171 423 q4 3 8 0" fill="none" stroke={INK} strokeWidth="1" />
      <rect x="118" y="452" width="116" height="34" fill={MUST} stroke={INK} strokeWidth="1.5" />
      <rect x="124" y="458" width="104" height="22" fill="none" stroke={INK} strokeWidth=".9" />
      <text x="176" y="472" className="hf-lab hf-lab--wide">
        RÉCEPTION
      </text>
      <path d="M206 452 a9 8 0 0 1 18 0 z" fill="#F4CF6B" stroke={INK} strokeWidth="1.2" />
      <circle cx="215" cy="443" r="2" fill={INK} />
      <rect x="386" y="398" width="72" height="15" fill={PLUM} stroke={INK} />
      <text x="422" y="408.5" className="hf-tiny hf-tiny--lg">
        ARRIVÉES
      </text>
      <path d="M394 476 V438 Q422 420 450 438 V476" fill="none" stroke={MUST} strokeWidth="3.5" />
      <path d="M394 476 V438 Q422 420 450 438 V476" fill="none" stroke={INK} strokeWidth=".8" />
      <rect x="388" y="474" width="68" height="5" fill={MUST} stroke={INK} />
      <circle cx="396" cy="483" r="4" fill={INK} />
      <circle cx="448" cy="483" r="4" fill={INK} />
      <rect x="400" y="454" width="22" height="20" fill={KRAFT} stroke={INK} strokeWidth="1.2" />
      <rect x="424" y="452" width="20" height="22" rx="2" fill={TEAL} stroke={INK} strokeWidth="1.2" />
      <Env id={env} x={404} y={440} rotate="-8 414 447" />
      <Env id={env} x={424} y={438} rotate="6 434 445" />
      <rect x="36" y="500" width="528" height="10" fill={PLUM} stroke={INK} strokeWidth="1.5" />
    </svg>
  );
}

function NightFacade({ uid, title, className }) {
  const glow = `hglow-${uid}`;
  const LIT = '#F4CF6B';
  const OFF = '#2A1520';
  const EDGE = '#0E080C';
  const ROOF = '#3A1D28';
  const windows = [
    [104, 176, true], [160, 176, false], [216, 176, true], [348, 176, true], [404, 176, true], [460, 176, false],
    [104, 244, false], [160, 244, true], [216, 244, true], [348, 244, false], [404, 244, true], [460, 244, true],
  ];
  return (
    <svg className={className} viewBox="0 0 600 400" role="img" aria-label={title}>
      <defs>
        <filter id={glow} x="-50%" y="-50%" width="200%" height="200%">
          <feGaussianBlur stdDeviation="4" result="b" />
          <feMerge>
            <feMergeNode in="b" />
            <feMergeNode in="SourceGraphic" />
          </feMerge>
        </filter>
      </defs>
      <circle cx="530" cy="60" r="26" fill="#F4E3B5" />
      <circle cx="520" cy="54" r="5" fill="#E6D19C" />
      <circle cx="538" cy="70" r="3.5" fill="#E6D19C" />
      <polygon points="70,112 300,30 530,112" fill={ROOF} stroke={EDGE} strokeWidth="2" />
      <circle cx="300" cy="80" r="16" fill={LIT} filter={`url(#${glow})`} />
      <g stroke={ROOF} strokeWidth="1.6" strokeLinecap="round">
        <line x1="300" y1="80" x2="300" y2="70" />
        <line x1="300" y1="80" x2="307" y2="84" />
      </g>
      <rect x="62" y="110" width="476" height="12" fill={OFF} stroke={EDGE} strokeWidth="2" />
      <rect x="80" y="122" width="440" height="266" fill="#4A2433" stroke={EDGE} strokeWidth="2" />
      <rect x="128" y="132" width="344" height="26" fill={OFF} stroke={EDGE} />
      <text x="300" y="150" className="hf-sign hf-sign--lit" filter={`url(#${glow})`}>
        MAILSORTER
      </text>
      <g stroke={EDGE} strokeWidth="1.5">
        {windows.map(([x, y, lit]) => (
          <rect key={`${x}-${y}`} x={x} y={y} width="36" height="46" fill={lit ? LIT : OFF} filter={lit ? `url(#${glow})` : undefined} />
        ))}
        <rect x="276" y="170" width="48" height="126" fill={TEAL} />
      </g>
      <g stroke={ROOF} strokeWidth="1.2" opacity=".6">
        {windows.filter(([, , lit]) => lit).map(([x, y]) => (
          <line key={`m-${x}-${y}`} x1={x + 18} y1={y} x2={x + 18} y2={y + 46} />
        ))}
      </g>
      <rect x="284" y="210" width="32" height="40" fill={LIT} stroke={EDGE} filter={`url(#${glow})`} />
      <path d="M272 388 V350 A28 28 0 0 1 328 350 V388 Z" fill={LIT} stroke={EDGE} strokeWidth="1.5" filter={`url(#${glow})`} />
      <rect x="262" y="304" width="76" height="12" fill={PLUM} stroke={EDGE} />
      <g fill="#1F3A38" stroke={EDGE} strokeWidth="1.2">
        <circle cx="246" cy="364" r="12" />
        <circle cx="354" cy="364" r="12" />
      </g>
      <rect x="36" y="388" width="528" height="12" fill={PLUM} stroke={EDGE} strokeWidth="1.5" />
    </svg>
  );
}

export default function HotelFacade({
  night = false,
  animated = true,
  className = '',
  title = "Un hôtel en coupe, dont chaque chambre est une catégorie d'e-mails",
}) {
  // Pattern and filter ids must be unique per drawing: the page can hold the
  // day and the night facade at once.
  const uid = useId().replace(/:/g, '');
  const cls = ['ht-facade', animated && !night ? 'is-animated' : '', className].filter(Boolean).join(' ');
  return night ? <NightFacade uid={uid} title={title} className={cls} /> : <DayFacade uid={uid} title={title} className={cls} />;
}
