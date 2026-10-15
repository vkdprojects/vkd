import { invoke } from "@tauri-apps/api/core";

/** Mirrors `AppInfo` in crates/desktop/src/commands.rs: serde keeps the field names as written (snake_case). */
export interface AppInfo {
	name: string;
	version: string;
	os: string;
}

/** True inside the Tauri window; false in a plain browser (`bun run dev` opened by hand, tests). */
export function inTauri(): boolean {
	return typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;
}

export async function appInfo(): Promise<AppInfo | null> {
	if (!inTauri()) return null;
	return invoke<AppInfo>("app_info");
}
