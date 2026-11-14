import React, { useEffect, useState } from 'react';

/** Tiny hash router: '#!/path?query' -> { path, params } */
export function useHashRoute() {
  const [hash, setHash] = useState(() => window.location.hash || '#!/overview');
  useEffect(() => {
    const onChange = () => setHash(window.location.hash || '#!/overview');
    window.addEventListener('hashchange', onChange);
    return () => window.removeEventListener('hashchange', onChange);
  }, []);
  const raw = hash.replace(/^#!?/, '') || 'overview';
  const [pathPart, queryPart] = raw.split('?');
  const params = new URLSearchParams(queryPart || '');
  const segments = pathPart.split('/').filter(Boolean);
  return { path: pathPart, segments, params: (k: string) => params.get(k) || '', paramsAll: params };
}
