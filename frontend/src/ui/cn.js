// Tiny classnames helper — joins truthy class fragments.
export function cn(...args) {
  return args.filter(Boolean).join(' ');
}
