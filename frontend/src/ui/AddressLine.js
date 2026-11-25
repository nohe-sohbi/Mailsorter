import React from 'react';
import { cn } from './cn';

// An address as the server shows it (protect.ShownAddress: `senderAddressShown`,
// `senderEmailShown`), laid out so its domain is never cut. The local part may
// be truncated; "@domain" sits in a span that does not shrink and goes to
// the next line when both do not fit, and a domain too long for a line wraps
// (break-all) rather than lose its end:
// x@paypal.com.verification-centre-securite.evil.io keeps "evil.io" in sight,
// where a truncated line showed "x@paypal.com.verif…" (checked in headless
// Chrome with the built CSS, at 300 and 360 px). The text is split at
// its last "@", which is the server's: a shown domain holds none. A text with
// no "@" (a From that names no sender) is one truncated line.
export default function AddressLine({ value, className }) {
  const text = value || '';
  const at = text.lastIndexOf('@');
  if (at < 0) {
    return <div className={cn('truncate', className)}>{text}</div>;
  }
  return (
    <div className={cn('flex min-w-0 max-w-full flex-wrap', className)} title={text}>
      <span className="min-w-0 truncate">{text.slice(0, at)}</span>
      <span className="max-w-full shrink-0 whitespace-normal break-all">{text.slice(at)}</span>
    </div>
  );
}
