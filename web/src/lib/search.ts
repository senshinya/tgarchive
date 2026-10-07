/** One run of a search snippet: matched text is marked. */
export interface SnippetPart {
  text: string;
  mark: boolean;
}

/** Cuts a snippet at the server's match ranges ([start, length] in UTF-16 units, sorted);
 * ranges out of bounds or overlapping an earlier one are ignored. */
export function highlightParts(snippet: string, ranges: [number, number][]): SnippetPart[] {
  const out: SnippetPart[] = [];
  let at = 0;
  for (const [start, len] of ranges) {
    if (start < at || len <= 0 || start + len > snippet.length) continue;
    if (start > at) out.push({ text: snippet.slice(at, start), mark: false });
    out.push({ text: snippet.slice(start, start + len), mark: true });
    at = start + len;
  }
  if (at < snippet.length) out.push({ text: snippet.slice(at), mark: false });
  return out;
}
