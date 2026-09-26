import { useEffect } from 'react';

// The theme's two families, requested only when a hotel screen mounts: the
// classic landing and today's dashboard never pay for them. A <link> rather
// than an @import in hotel.css, because CRA concatenates the chunk's CSS in an
// order we do not control, and an @import that is not first is dropped.
// index.html already preconnects to both Google Fonts hosts.
const HREF =
  'https://fonts.googleapis.com/css2?family=Bodoni+Moda:ital,opsz,wght@0,6..96,400..900;1,6..96,400..900&family=Jost:wght@300..700&display=swap';

export function useHotelFonts() {
  useEffect(() => {
    if (document.querySelector('link[data-hotel-fonts]')) return;
    const link = document.createElement('link');
    link.rel = 'stylesheet';
    link.href = HREF;
    link.dataset.hotelFonts = 'true';
    document.head.appendChild(link);
  }, []);
}
