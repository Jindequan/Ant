// Only an opaque draft identifier is stored locally; definitions and secrets stay on the server.
function key(namespace: string, principalId: string) {
  return `ant.config-draft:${encodeURIComponent(namespace)}:${encodeURIComponent(principalId)}`;
}
export function rememberedDraft(namespace: string, principalId: string): string {
  try {
    return localStorage.getItem(key(namespace, principalId)) ?? '';
  } catch {
    return '';
  }
}
export function rememberDraft(namespace: string, principalId: string, id: string) {
  try {
    localStorage.setItem(key(namespace, principalId), id);
  } catch {
    /* The saved URL still supports resuming. */
  }
}
export function forgetDraft(namespace: string, principalId: string) {
  try {
    localStorage.removeItem(key(namespace, principalId));
  } catch {
    /* Storage may be disabled. */
  }
}
