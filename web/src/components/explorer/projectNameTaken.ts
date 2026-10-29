/**
 * Whether name is a project other than except, ignoring case. Windows file
 * names ignore case, and so does the hub's "already exists" check there, so
 * "MAIN" beside "main" is the same folder; the forms refuse it themselves
 * rather than send it and show the hub's refusal. A rename that only changes
 * a project's own case is not a clash.
 */
export function projectNameTaken(names: Iterable<string>, name: string, except = ""): boolean {
  const wanted = name.toLowerCase();
  const own = except.toLowerCase();
  for (const existing of names) {
    const folded = existing.toLowerCase();
    if (folded === wanted && folded !== own) return true;
  }
  return false;
}
