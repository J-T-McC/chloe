/**
 * Shell-style Tab completion for the console's command word: the first Tab extends
 * the word to the longest prefix shared by all matches (or the whole command when
 * only one matches); further Tabs cycle through the matches.
 */
export function tabComplete(word: string, matches: string[], last: string | null): string | null {
  if (!matches.length) return null;
  if (matches.length === 1) return matches[0] + " ";
  // Cycling: the word is one of the matches we offered before.
  if (last !== null) {
    const i = matches.indexOf(last.trim());
    if (i >= 0) return matches[(i + 1) % matches.length];
  }
  let lcp = matches[0];
  for (const m of matches) while (!m.startsWith(lcp)) lcp = lcp.slice(0, -1);
  return lcp.length > word.length ? lcp : matches[0];
}
