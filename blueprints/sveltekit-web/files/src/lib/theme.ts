import type { ThemeOptions } from "@codeatlasdev/ui";

/** One theme object for the app: the Vite plugin and the server hook both read it. */
export const theme: ThemeOptions = {
	presets: [{ id: "brand", color: "#2563eb", label: "Brand" }],
	defaultPreset: "brand",
	defaultScheme: "system",
};
