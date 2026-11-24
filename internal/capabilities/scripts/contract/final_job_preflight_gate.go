package contract

// FinalJobPreflight is the durable proof that the image contract of ONE
// sealed overlay plan was verified before any overlay render was submitted.
//
// It is keyed by the plan's render digest, not by the run: the digest covers
// the canvas, timing, item order, content and asset addresses, so a repaired
// or re-frozen plan changes the digest and re-arms the gate. A marker without
// a digest never covers a plan — an unidentifiable plan cannot be skipped.
type FinalJobPreflight struct {
	PlanDigest string `json:"plan_digest,omitempty"`
	Passed     bool   `json:"passed"`
}
