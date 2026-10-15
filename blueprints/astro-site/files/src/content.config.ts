import { defineCollection } from "astro:content";
import { glob } from "astro/loaders";
import { z } from "astro/zod";

export const collections = {
	docs: defineCollection({
		loader: glob({ pattern: "**/*.md", base: "./src/content/docs" }),
		schema: z.object({
			title: z.string(),
			description: z.string(),
			/** Position in the sidebar and index; pages without it come last. */
			order: z.number().default(100),
		}),
	}),
};
