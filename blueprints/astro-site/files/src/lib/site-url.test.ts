import { describe, expect, test } from "bun:test";
import { siteUrl } from "./site-url";

describe("siteUrl", () => {
	test("uses SITE_URL when it is set", () => {
		expect(
			siteUrl({ SITE_URL: "https://example.com" }, "https://fallback.dev"),
		).toBe("https://example.com");
	});

	test("falls back when SITE_URL is unset", () => {
		expect(siteUrl({}, "https://fallback.dev")).toBe("https://fallback.dev");
	});

	test("falls back when SITE_URL is empty (.env.example ships `SITE_URL=`)", () => {
		expect(siteUrl({ SITE_URL: "" }, "https://fallback.dev")).toBe(
			"https://fallback.dev",
		);
	});

	test("falls back when SITE_URL is only whitespace", () => {
		expect(siteUrl({ SITE_URL: "  " }, "https://fallback.dev")).toBe(
			"https://fallback.dev",
		);
	});

	test("trims surrounding whitespace", () => {
		expect(
			siteUrl({ SITE_URL: " https://example.com " }, "https://fallback.dev"),
		).toBe("https://example.com");
	});

	test("rejects a value that is not an absolute URL, naming the variable", () => {
		expect(() =>
			siteUrl({ SITE_URL: "example.com" }, "https://fallback.dev"),
		).toThrow(/SITE_URL/);
	});
});
