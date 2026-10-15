export type Health = { ok: boolean; label: string };

/** Turns the answer of the API health endpoint into what the page shows. Never throws: an unreachable API is a state, not a crash. */
export async function readHealth(
	fetchFn: typeof fetch,
	url = "/api/healthz",
): Promise<Health> {
	try {
		const res = await fetchFn(url);
		return res.ok
			? { ok: true, label: "API reachable" }
			: { ok: false, label: `API answered ${res.status}` };
	} catch {
		return { ok: false, label: "API unreachable" };
	}
}
