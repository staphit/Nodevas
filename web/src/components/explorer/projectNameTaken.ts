/**
 * The project that already holds name, ignoring case, other than except; null
 * when the name is free. Windows file names ignore case, and so does the hub's
 * "already exists" check there, so "MAIN" beside "main" is the same folder;
 * the forms refuse it themselves rather than send it and show the hub's
 * refusal. A rename that only changes a project's own case is not a clash.
 */
export function projectNameClash(names: Iterable<string>, name: string, except = ""): string | null {
  const wanted = name.toLowerCase();
  const own = except.toLowerCase();
  for (const existing of names) {
    const folded = existing.toLowerCase();
    if (folded === wanted && folded !== own) return existing;
  }
  return null;
}

/**
 * The refusal for a name another project holds: the form's own message when
 * the names match exactly, and when only the case differs, one that names the
 * holder and says case does not count — "already exists" beside a list that
 * shows no such spelling reads as a bug.
 */
export function projectNameClashMessage(
  clash: string,
  name: string,
  sameName: string,
  t: (key: string, values?: Record<string, string>) => string,
): string {
  if (clash === name) return sameName;
  return t("explorer.nameTakenOtherCase", { name: clash.slice(clash.lastIndexOf("/") + 1) });
}
