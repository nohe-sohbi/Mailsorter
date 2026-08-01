/** @type {import('tailwindcss').Config} */

// Every colour is a CSS variable holding bare RGB channels, so a single class
// like `bg-ink-50` resolves to the right value in both themes without any page
// carrying a `dark:` variant. Flipping the theme is one class on <html>; see
// the token tables in src/index.css.
//
// `<alpha-value>` is Tailwind's placeholder: it keeps opacity modifiers such as
// `bg-ink-950/50` and `ring-brand-500/50` working against variable colours.
const withAlpha = (name) => `rgb(var(${name}) / <alpha-value>)`;

const scale = (prefix, steps) =>
  Object.fromEntries(steps.map((s) => [s, withAlpha(`--${prefix}-${s}`)]));

const RAMP = [50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 950];

module.exports = {
  darkMode: 'class',
  content: ['./src/**/*.{js,jsx,ts,tsx}', './public/index.html'],
  theme: {
    extend: {
      colors: {
        // Accent unique : bleu profond "encre". Pas de second accent, pas de dégradé.
        brand: scale('brand', RAMP),
        // Neutres froids, la base calme. En thème sombre, la rampe s'inverse :
        // ink-50 devient le fond de page, ink-900 le texte principal.
        ink: scale('ink', RAMP),

        // Sémantiques. À préférer aux valeurs brutes : ce sont elles qui portent
        // le contrat de contraste et la bascule de thème.
        //
        // surface  : fond des cartes, champs, en-tête (remplace bg-white)
        // muted    : texte secondaire lisible (≥ 4,5:1 dans les deux thèmes)
        // subtle   : décor uniquement — jamais de contenu
        // hairline : filets et bordures
        surface: {
          DEFAULT: withAlpha('--surface'),
          raised: withAlpha('--surface-raised'),
          sunken: withAlpha('--surface-sunken'),
        },
        muted: withAlpha('--muted'),
        subtle: withAlpha('--subtle'),
        hairline: withAlpha('--hairline'),

        // Teintes de statut, également variables pour tenir en thème sombre.
        positive: scale('positive', [50, 100, 500, 600, 700]),
        caution: scale('caution', [50, 100, 500, 600, 700]),
        danger: scale('danger', [50, 100, 500, 600, 700]),
        info: scale('info', [50, 100, 500, 600, 700]),
      },
      fontFamily: {
        sans: ['"Hanken Grotesk"', 'ui-sans-serif', 'system-ui', 'sans-serif'],
        display: ['"General Sans"', '"Hanken Grotesk"', 'ui-sans-serif', 'sans-serif'],
      },
      boxShadow: {
        // Échelle d'ombres sobre et froide, aucune lueur colorée. En thème
        // sombre l'ombre portée ne se voit pas : c'est la bordure qui sépare.
        soft: '0 1px 2px 0 rgb(var(--shadow) / 0.04), 0 1px 3px 0 rgb(var(--shadow) / 0.06)',
        card: '0 1px 3px 0 rgb(var(--shadow) / 0.05), 0 10px 28px -14px rgb(var(--shadow) / 0.12)',
        lift: '0 2px 6px -1px rgb(var(--shadow) / 0.08), 0 16px 36px -12px rgb(var(--shadow) / 0.16)',
      },
      keyframes: {
        'fade-in': { '0%': { opacity: 0 }, '100%': { opacity: 1 } },
        'fade-up': {
          '0%': { opacity: 0, transform: 'translateY(10px)' },
          '100%': { opacity: 1, transform: 'translateY(0)' },
        },
        'slide-in-right': {
          '0%': { opacity: 0, transform: 'translateX(20px)' },
          '100%': { opacity: 1, transform: 'translateX(0)' },
        },
        'slide-up': {
          '0%': { opacity: 0, transform: 'translateY(100%)' },
          '100%': { opacity: 1, transform: 'translateY(0)' },
        },
        'scale-in': {
          '0%': { opacity: 0, transform: 'scale(0.97)' },
          '100%': { opacity: 1, transform: 'scale(1)' },
        },
        shimmer: {
          '0%': { backgroundPosition: '-200% 0' },
          '100%': { backgroundPosition: '200% 0' },
        },
      },
      animation: {
        'fade-in': 'fade-in 0.35s ease-out both',
        'fade-up': 'fade-up 0.45s cubic-bezier(0.22, 1, 0.36, 1) both',
        'slide-in-right': 'slide-in-right 0.3s cubic-bezier(0.22, 1, 0.36, 1) both',
        'slide-up': 'slide-up 0.28s cubic-bezier(0.22, 1, 0.36, 1) both',
        'scale-in': 'scale-in 0.22s cubic-bezier(0.22, 1, 0.36, 1) both',
        shimmer: 'shimmer 1.6s linear infinite',
      },
    },
  },
  plugins: [],
};
