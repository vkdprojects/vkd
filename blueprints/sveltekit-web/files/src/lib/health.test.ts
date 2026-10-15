import { describe, expect, it } from "vitest";
import { readHealth } from "./health.ts";

const respond = (status: number) =>
	(async () => new Response(null, { status })) as typeof fetch;

describe("readHealth", () => {
	it("is ok when the endpoint answers 2xx", async () => {
		expect(await readHealth(respond(200))).toEqual({
			ok: true,
			label: "API reachable",
		});
	});

	it("reports the status when the endpoint answers an error", async () => {
		expect(await readHealth(respond(503))).toEqual({
			ok: false,
			label: "API answered 503",
		});
	});

	it("turns a network failure into a state instead of throwing", async () => {
		const down = (async () => {
			throw new TypeError("fetch failed");
		}) as typeof fetch;
		expect(await readHealth(down)).toEqual({
			ok: false,
			label: "API unreachable",
		});
	});

	it("asks the given url", async () => {
		const seen: string[] = [];
		const spy = (async (input: RequestInfo | URL) => {
			seen.push(String(input));
			return new Response(null, { status: 200 });
		}) as typeof fetch;
		await readHealth(spy, "/other");
		expect(seen).toEqual(["/other"]);
	});
});
