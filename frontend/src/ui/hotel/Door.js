import React from 'react';

// A hotel door drawn at 240 x 230, the object beside it telling what it stands
// for. `crop` frames the door alone (the demo's small doors). Fixed colours:
// the drawing reads the same by day and by night.
const INK = '#2B1B1E';
const PLUM = '#7A2E3B';
const MUST = '#E3A93B';
const CREAM = '#FBF1E4';
const TEAL = '#2F6F73';

function Signpost() {
  return (
    <g>
      <rect x="206" y="98" width="5" height="132" fill={PLUM} stroke={INK} />
      <polygon points="180,104 226,104 236,112 226,120 180,120" fill={MUST} stroke={INK} />
      <text x="206" y="114.6" className="hf-sg" fill={INK}>
        FACTURES
      </text>
      <polygon points="188,128 232,128 232,144 188,144 178,136" fill={CREAM} stroke={INK} />
      <text x="207" y="138.6" className="hf-sg" fill={INK}>
        PROMOS
      </text>
      <polygon points="180,152 226,152 236,160 226,168 180,168" fill={PLUM} stroke={INK} />
      <text x="206" y="162.6" className="hf-sg" fill={CREAM}>
        COLIS
      </text>
    </g>
  );
}

function Alarm() {
  return (
    <g>
      <ellipse cx="34" cy="178" rx="24" ry="5" fill={PLUM} stroke={INK} strokeWidth="1.3" />
      <line x1="34" y1="182" x2="34" y2="224" stroke={INK} strokeWidth="2.4" />
      <ellipse cx="34" cy="226" rx="14" ry="3" fill={PLUM} stroke={INK} />
      <circle cx="23" cy="146" r="5.5" fill={MUST} stroke={INK} />
      <circle cx="45" cy="146" r="5.5" fill={MUST} stroke={INK} />
      <circle cx="34" cy="159" r="15" fill={CREAM} stroke={INK} strokeWidth="2" />
      <g stroke={INK} strokeWidth="1.6" strokeLinecap="round">
        <line x1="34" y1="159" x2="34" y2="149" />
        <line x1="34" y1="159" x2="41" y2="162" />
        <line x1="26" y1="173" x2="23" y2="177" />
        <line x1="42" y1="173" x2="45" y2="177" />
      </g>
      <text x="44" y="134" className="hf-z">
        z
      </text>
      <text x="51" y="123" className="hf-z hf-z--sm">
        z
      </text>
    </g>
  );
}

function Rope() {
  return (
    <g>
      <polygon points="120,82 124,92 135,92 126,98 129,108 120,102 111,108 114,98 105,92 116,92" fill={MUST} stroke={INK} />
      <g stroke={INK} strokeWidth="1.3" fill={MUST}>
        <rect x="36" y="182" width="7" height="48" />
        <rect x="197" y="182" width="7" height="48" />
        <circle cx="39.5" cy="179" r="5.5" />
        <circle cx="200.5" cy="179" r="5.5" />
      </g>
      <path d="M43 190 Q120 224 197 190" fill="none" stroke={PLUM} strokeWidth="6" strokeLinecap="round" />
      <path d="M43 190 Q120 224 197 190" fill="none" stroke={INK} strokeWidth="1" opacity=".5" />
    </g>
  );
}

function DoNotDisturb({ color }) {
  return (
    <g>
      <g transform="rotate(-7 152 126)">
        <rect x="134" y="118" width="38" height="76" rx="4" fill={CREAM} stroke={INK} strokeWidth="1.4" />
        <circle cx="152" cy="130" r="6.5" fill={color} stroke={INK} strokeWidth="1.2" />
        <text x="153" y="153" className="hf-hg">
          NE PAS
        </text>
        <text x="153" y="162" className="hf-hg">
          DÉRANGER
        </text>
        <line x1="142" y1="170" x2="164" y2="170" stroke={MUST} strokeWidth="2" />
      </g>
      <circle cx="152" cy="126" r="3.5" fill={MUST} stroke={INK} />
    </g>
  );
}

function Newspaper() {
  return (
    <g>
      <g transform="rotate(-5 120 219)">
        <rect x="88" y="208" width="64" height="20" fill={CREAM} stroke={INK} strokeWidth="1.3" />
        <text x="120" y="217.5" className="hf-hg hf-hg--ink">
          LE RÉCAP
        </text>
        <line x1="94" y1="222" x2="146" y2="222" stroke={INK} strokeWidth=".7" opacity=".5" />
        <line x1="94" y1="225" x2="136" y2="225" stroke={INK} strokeWidth=".7" opacity=".5" />
      </g>
      <ellipse cx="200" cy="226" rx="17" ry="3.5" fill={CREAM} stroke={INK} strokeWidth="1.2" />
      <path d="M190 206 h20 v10 a10 9 0 0 1 -20 0z" fill={CREAM} stroke={INK} strokeWidth="1.3" />
      <path d="M210 209 a5 5 0 0 1 0 9" fill="none" stroke={INK} strokeWidth="1.3" />
      <path d="M196 200 q-3 -5 0 -9 M203 200 q-3 -5 0 -9" fill="none" stroke={INK} strokeWidth="1" opacity=".55" />
    </g>
  );
}

function WallClock() {
  return (
    <g>
      <circle cx="32" cy="72" r="22" fill={CREAM} stroke={MUST} strokeWidth="4" />
      <circle cx="32" cy="72" r="22" fill="none" stroke={INK} strokeWidth="1.2" />
      <path d="M32 72 L32 52 A20 20 0 0 1 32 92 Z" fill="#EFB7C3" opacity=".8" />
      <g stroke={INK} strokeWidth="1.8" strokeLinecap="round">
        <line x1="32" y1="72" x2="32" y2="56" />
        <line x1="32" y1="72" x2="32" y2="88" />
      </g>
      <circle cx="32" cy="72" r="2.2" fill={INK} />
      <rect x="8" y="102" width="48" height="16" fill={PLUM} stroke={INK} />
      <text x="32" y="113" className="hf-sg" fill={CREAM}>
        30 MIN
      </text>
    </g>
  );
}

const PROPS = { signpost: Signpost, alarm: Alarm, rope: Rope, dnd: DoNotDisturb, newspaper: Newspaper, wallclock: WallClock };

export default function Door({ color = TEAL, number, prop, crop = false, className, title }) {
  const Prop = prop ? PROPS[prop] : null;
  return (
    <svg
      className={className}
      viewBox={crop ? '58 18 124 214' : '0 0 240 230'}
      role={title ? 'img' : undefined}
      aria-label={title}
      aria-hidden={title ? undefined : 'true'}
    >
      <rect x="62" y="22" width="116" height="208" fill={CREAM} stroke={INK} strokeWidth="2" />
      <rect x="68" y="28" width="104" height="202" fill="none" stroke={INK} strokeWidth="1" />
      <rect x="74" y="34" width="92" height="196" fill={color} stroke={INK} strokeWidth="1.6" />
      <rect x="84" y="46" width="72" height="72" fill="none" stroke={INK} strokeOpacity=".45" strokeWidth="1.5" />
      <rect x="84" y="128" width="72" height="90" fill="none" stroke={INK} strokeOpacity=".45" strokeWidth="1.5" />
      <rect x="148" y="118" width="8" height="18" rx="2" fill={MUST} stroke={INK} />
      <circle cx="152" cy="126" r="3.5" fill={MUST} stroke={INK} />
      {number && (
        <>
          <rect x="104" y="56" width="32" height="15" rx="2" fill={MUST} stroke={INK} />
          <text x="120" y="67" className="hf-dn">
            {number}
          </text>
        </>
      )}
      {Prop && <Prop color={color} />}
    </svg>
  );
}
