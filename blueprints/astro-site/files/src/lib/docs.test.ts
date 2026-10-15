import { describe, expect, test } from "bun:test";
import { docHref, sortDocs } from "./docs";

const doc = (title: string, order: number) => ({ data: { title, order } });

describe("sortDocs", () => {
	test("orders by `order`, then by title", () => {
		const sorted = sortDocs([doc("B", 2), doc("C", 1), doc("A", 2)]);
		expect(sorted.map((d) => d.data.title)).toEqual(["C", "A", "B"]);
	});

	test("does not mutate its input", () => {
		const input = [doc("B", 2), doc("A", 1)];
		sortDocs(input);
		expect(input.map((d) => d.data.title)).toEqual(["B", "A"]);
	});
});

describe("docHref", () => {
	test("the index entry is the docs landing page", () => {
		expect(docHref("index")).toBe("/docs/");
	});

	test("other entries end with a slash", () => {
		expect(docHref("getting-started")).toBe("/docs/getting-started/");
	});
});
