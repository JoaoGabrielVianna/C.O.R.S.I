/**
 * The context-reference type Job Radar owns.
 *
 * It matches `tools.OpportunityReferenceType` in the backend
 * (`internal/jobradar/tools/references.go`), which is the authority: the
 * backend refuses a type it has no resolver for, so a drift here fails
 * loudly at attach time rather than silently attaching nothing.
 *
 * It lives in its own file so the two consumers — the button that attaches
 * one and any future test — share a constant instead of repeating a string
 * literal that only one of them would be updated.
 */
export const OPPORTUNITY_REFERENCE_TYPE = "job_radar.opportunity";
