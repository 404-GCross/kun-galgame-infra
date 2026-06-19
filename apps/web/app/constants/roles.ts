// Role → chip color, centralized so the user table, role modal, and profile
// badges stay in sync. They previously drifted (a new role had to be added in
// three inline ternaries — and `creator`/`ren` were already missing in some);
// add a new role's color here once. Mirrors the OAuth roles: `ren` (莲) is the
// DB-only super admin, `creator` the trusted-publisher role.
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
