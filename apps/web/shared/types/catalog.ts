// Admin types for the catalog service (kun_catalog), served by cmd/catalog
// under /api/v1/admin/catalog/* (a SEPARATE backend base — see
// runtimeConfig.public.catalogApiBase and useApi('catalog')).
//
// All API shapes are GENERATED from the OpenAPI spec
// (docs/catalog/admin-openapi.yaml, exported code-first from the Go Huma
// handlers) — see shared/types/generated/catalog-admin-api.ts and
// `pnpm gen:types:catalog-admin`. UI-only label/color maps live in
// app/constants/catalog.ts.
import type { components } from './generated/catalog-admin-api'

type Schemas = components['schemas']

export type CatalogEntitySummary = Schemas['EntitySummary']
export type CatalogCandidateItem = Schemas['CandidateItem']
export type CatalogProposalItem = Schemas['ProposalItem']
export type CatalogProbableRefItem = Schemas['ProbableRefItem']
export type CatalogCandidatePage = Schemas['PageCandidateItem']
export type CatalogProposalPage = Schemas['PageProposalItem']
export type CatalogProbableRefPage = Schemas['PageProbableRefItem']
export type CatalogDecideCandidateData = Schemas['DecideCandidateData']
export type CatalogDetachNameData = Schemas['DetachNameData']
export type CatalogProposalActionData = Schemas['ProposalActionData']
export type CatalogRefActionData = Schemas['RefActionData']
export type CatalogImageReferenceItem = Schemas['ImageReferenceItem']
export type CatalogImageReferencesData = Schemas['ImageReferencesData']
export type CatalogDetachImageReferencesData =
  Schemas['DetachImageReferencesData']
