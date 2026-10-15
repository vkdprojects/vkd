import type { ThemeOptions } from "@codeatlasdev/ui/astro";

/** The one theme object: astro.config.mjs passes it to caUI(), which paints it before first frame. */
export const theme: ThemeOptions = { defaultScheme: "dark" };
