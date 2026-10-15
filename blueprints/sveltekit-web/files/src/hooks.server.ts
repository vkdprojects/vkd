import { caThemeHandle, createErrorHandler } from "@codeatlasdev/ui/kit";
import { theme } from "#lib";

// Brand preset and scheme are in the server-rendered HTML, so the first paint is already on theme.
export const handle = caThemeHandle(theme);

export const handleError = createErrorHandler();
