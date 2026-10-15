/** Docs in reading order: by `order`, then by title so the result never depends on file-system order. */
export function sortDocs<T extends { data: { order: number; title: string } }>(
	docs: T[],
): T[] {
	return [...docs].sort(
		(a, b) =>
			a.data.order - b.data.order || a.data.title.localeCompare(b.data.title),
	);
}

/** Site-relative URL of a doc page. The collection entry `index` is the docs landing page. */
export function docHref(id: string): string {
	return id === "index" ? "/docs/" : `/docs/${id}/`;
}
