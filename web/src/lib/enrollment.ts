/** Tokens that can still enroll a node or recursor: not used and not expired. */
export const pendingTokens = <T extends { used_at: string | null; expires_at: string }>(tokens: T[], now = Date.now()) =>
  tokens.filter((t) => t.used_at === null && new Date(t.expires_at).getTime() > now)
