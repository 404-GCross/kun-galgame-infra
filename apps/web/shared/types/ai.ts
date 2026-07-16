// Admin types for the AI-gateway usage dashboard (kun_ai), served by cmd/ai
// under /api/v1/admin/ai/* (a SEPARATE backend base — see
// runtimeConfig.public.aiApiBase and useApi('ai')).
//
// All API shapes are GENERATED from the OpenAPI spec
// (docs/ai/admin-openapi.yaml, exported code-first from the Go Huma handlers) —
// see shared/types/generated/ai-admin-api.ts and `pnpm gen:types:ai-admin`.
// UI-only label/color maps live in app/constants/ai.ts.
import type { components } from './generated/ai-admin-api'

type Schemas = components['schemas']

// Usage summary (site×route×channel)
export type AiUsageSummary = Schemas['UsageSummary']
export type AiUsageOverview = Schemas['UsageOverview']
export type AiSummaryRow = Schemas['SummaryRow']

// Daily trend
export type AiDailySeries = Schemas['DailySeries']
export type AiDailyPoint = Schemas['DailyPoint']

// Budget fuse config
export type AiBudget = Schemas['BudgetView']
export type AiUpsertBudgetRequest = Schemas['UpsertBudgetRequest']
