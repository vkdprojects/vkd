import { describe, expect, test } from "bun:test";
import { appInfo, inTauri } from "./native.ts";

describe("native", () => {
	test("outside the Tauri window there is no bridge and no call is made", async () => {
		expect(inTauri()).toBe(false);
		expect(await appInfo()).toBeNull();
	});
});
