// Package mediacert — certifier.go is the single canonical entry point that
// turns a Spec + MediaResult into a Report. It is the function the CLI
// (cmd/mediacert) and the `make verify-vidrush-semantic` target call.
//
// The certifier is fail-closed: CERTIFIED is true ONLY when every rule
// passed. A run with JobStatus=SUCCEEDED but a boxing clip for Greek Salad
// must return CERTIFIED=false — that is the whole point of MediaCert, and
// the explicit rejection of the count-only test that declared success at a
// semantically broken pipeline.
package mediacert

// Certify applies every rule in AllRules() to the given result against the
// given spec and returns the folded Report. CERTIFIED is true only when
// every CheckResult.Passed is true. The JobStatus is carried through so the
// report can make the "technically successful but semantically wrong"
// distinction visible.
func Certify(spec Spec, result MediaResult) Report {
	rules := AllRules()
	checks := make([]CheckResult, 0, len(rules))
	allPassed := true
	for _, rule := range rules {
		cr := rule(spec, result)
		if !cr.Passed {
			allPassed = false
		}
		checks = append(checks, cr)
	}
	return Report{
		JobStatus: result.JobStatus,
		Certified: allPassed,
		Checks:    checks,
	}
}

// IsCertified is the convenience predicate for callers that only need the
// verdict and not the full report.
func IsCertified(spec Spec, result MediaResult) bool {
	return Certify(spec, result).Certified
}
