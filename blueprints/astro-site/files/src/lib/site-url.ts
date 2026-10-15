/**
 * Absolute URL of the deployed site, used for the sitemap and canonical links.
 * An unset or empty `SITE_URL` (`.env.example` ships `SITE_URL=`, and atlas exports it as "") means `fallback`.
 */
export function siteUrl(env: { SITE_URL?: string }, fallback: string): string {
	const value = env.SITE_URL?.trim();
	if (!value) return fallback;
	try {
		new URL(value);
	} catch {
		throw new Error(`SITE_URL must be an absolute URL, got "${value}"`);
	}
	return value;
}
