import React from 'react';
import { cn } from './cn';

// An address as the server shows it (protect.ShownAddress: `senderAddressShown`,
// `senderEmailShown`, or a `senderLabel` the server says is one), laid out so
// its domain is never cut. The local part may be truncated; "@domain" sits in
// a span that does not shrink and goes to the next line when both do not
// fit, and a domain too long for a line wraps (break-all) rather than lose
// its end: x@paypal.com.verification-centre-securite.evil.io keeps "evil.io"
// in sight, where a truncated line showed "x@paypal.com.verif..." (checked in
// headless Chrome with the built CSS, at 360 px, in both themes). The text
// is split at its last "@", which is the server's: a shown domain holds
// none. A text with no "@" (a screen's fallback, never a From's text: a From
// that names no sender is shown "Expéditeur illisible") is one truncated
// line.
//
// Spans only, so it fits inside a button; `inline` keeps it in a run of
// text (the hotel's history register). `children` go inside, after the
// address (a screen-reader note).
export default function AddressLine({ value, className, inline = false, title, children }) {
  const text = value || '';
  const at = text.lastIndexOf('@');
  if (at < 0) {
    return (
      <span className={cn(inline ? 'inline-block max-w-full align-bottom' : 'block', 'truncate', className)} title={title}>
        {text}
        {children}
      </span>
    );
  }
  return (
    <span
      className={cn(inline ? 'inline-flex max-w-full align-bottom' : 'flex', 'min-w-0 flex-wrap', className)}
      title={title ?? text}
    >
      <span className="min-w-0 truncate">{text.slice(0, at)}</span>
      <span className="max-w-full shrink-0 whitespace-normal break-all">{text.slice(at)}</span>
      {children}
    </span>
  );
}
