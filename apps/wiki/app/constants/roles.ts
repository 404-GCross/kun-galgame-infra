// Role → chip color + display label for the account switcher. Mirrors the OAuth
// roles: `ren` (莲) is the super admin, `admin` the site admin, `creator` the
// trusted-publisher role. Kept in sync with apps/web's constants/roles.ts.
export const ROLE_COLOR: Record<
  string,
  'primary' | 'success' | 'warning' | 'secondary' | 'default'
> = {
  ren: 'secondary',
  admin: 'primary',
  moderator: 'warning',
  creator: 'success',
  user: 'default',
}

export const roleColor = (role: string) => ROLE_COLOR[role] ?? 'default'

export const ROLE_LABEL: Record<string, string> = {
  ren: '莲',
  admin: '管理员',
  moderator: '版主',
  creator: '创作者',
  user: '用户',
}

export const roleLabel = (role: string) => ROLE_LABEL[role] ?? role

// The single most significant role to badge (highest priority first); '' when
// the account has no notable role (a plain user shows no chip).
const ROLE_PRIORITY = ['ren', 'admin', 'moderator', 'creator']
export const primaryRole = (roles: string[] = []) =>
  ROLE_PRIORITY.find((r) => roles.includes(r)) ?? ''

// Roles that require re-authentication to switch INTO — mirrors the OP step-up
// gate (the OP refuses a silent switch into these with 10016, forcing re-login).
export const STEP_UP_ROLES = ['admin', 'ren']
export const needsStepUp = (roles: string[] = []) =>
  roles.some((r) => STEP_UP_ROLES.includes(r))
