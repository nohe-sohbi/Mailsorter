import { useEffect, useState } from 'react';

// Which floor the middle of the screen is on, read from the data-floor
// attribute of each section (an empty value for the hall and the night exit).
// It lights the matching button of the elevator panel.
export function useActiveFloor() {
  const [active, setActive] = useState(null);

  useEffect(() => {
    const sections = Array.from(document.querySelectorAll('[data-floor]'));
    if (!sections.length || typeof IntersectionObserver === 'undefined') return undefined;
    const io = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) setActive(entry.target.dataset.floor || null);
        });
      },
      { rootMargin: '-45% 0px -50% 0px' }
    );
    sections.forEach((s) => io.observe(s));
    return () => io.disconnect();
  }, []);

  return active;
}
