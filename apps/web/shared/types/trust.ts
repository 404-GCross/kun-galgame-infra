// Admin types for the Trust & Safety service (kun_trust), served by cmd/trust
// under /api/v1/admin/trust/* (a SEPARATE backend base — see
// runtimeConfig.public.trustApiBase and useApi('trust')).
//
// All API shapes are GENERATED from the OpenAPI spec
// (docs/trust/admin-openapi.yaml, exported code-first from the Go Huma
// handlers) — see shared/types/generated/trust-admin-api.ts and
// `pnpm gen:types:trust-admin`. UI-only label/color maps live in
// app/constants/trust.ts.
import type { components } from './generated/trust-admin-api'

type Schemas = components['schemas']

// Review inbox
export type TrustReviewItem = Schemas['ReviewItemView']
export type TrustReviewItemPage = Schemas['PageReviewItemView']
export type TrustReviewItemDetail = Schemas['ReviewItemDetail']
export type TrustDecideData = Schemas['DecideData']

// Registries
export type TrustSubjectKind = Schemas['SubjectKindView']
export type TrustReason = Schemas['ReasonView']
export type TrustCreateSubjectKindRequest = Schemas['CreateSubjectKindRequest']
export type TrustPatchSubjectKindRequest = Schemas['PatchSubjectKindRequest']
// Batch subject-kind registration (step 06): declarative convergence, shared by
// the admin batch endpoint and the S2S ensure face.
export type TrustEnsureSubjectKindItem = Schemas['EnsureSubjectKindItem']
export type TrustBatchSubjectKindsRequest = Schemas['BatchSubjectKindsRequest']
export type TrustEnsureSubjectKindsResponse =
  Schemas['EnsureSubjectKindsResponse']
export type TrustEnsureSubjectKindResult =
  Schemas['EnsureSubjectKindResultView']
export type TrustCreateReasonRequest = Schemas['CreateReasonRequest']
export type TrustPatchReasonRequest = Schemas['PatchReasonRequest']

// Tier0 word list (step 05/06)
export type TrustTerm = Schemas['TermView']
export type TrustTermsResponse = Schemas['TermsResponse']
export type TrustCreateTermRequest = Schemas['CreateTermRequest']

// Dead-letter dispositions
export type TrustDispositionPage = Schemas['PageDispositionView']
