package scriptgeneration

import "testing"

// TestApplyOverlayAuditImageBudgetFallsBackToCanvas pins CFG-05: the compiled
// wire plan can omit planner-side intake telemetry, and the audit must then
// report the requested canvas ceiling instead of a misleading zero budget.
func TestApplyOverlayAuditImageBudgetFallsBackToCanvas(t *testing.T) {
	audit := &OverlayAudit{
		Intake:       &OverlayAuditIntake{ImageBudget: 0},
		EntityImages: &OverlayAuditEntityImages{Budget: 0, BudgetUsed: 2},
	}
	applyOverlayAuditImageBudget(audit, 25)
	if audit.EntityImages.Budget != 25 || audit.EntityImages.BudgetUnused != 23 {
		t.Fatalf("entity image budget = %d unused=%d, want 25/23", audit.EntityImages.Budget, audit.EntityImages.BudgetUnused)
	}
	if audit.Intake.ImageBudget != 25 {
		t.Fatalf("intake image budget = %d, want the canvas ceiling 25", audit.Intake.ImageBudget)
	}
}

// TestApplyOverlayAuditImageBudgetKeepsPlannerValue: a plan that DID report a
// budget is authoritative; the canvas fallback must not overwrite it.
func TestApplyOverlayAuditImageBudgetKeepsPlannerValue(t *testing.T) {
	audit := &OverlayAudit{
		Intake:       &OverlayAuditIntake{ImageBudget: 8},
		EntityImages: &OverlayAuditEntityImages{Budget: 8, BudgetUsed: 3},
	}
	applyOverlayAuditImageBudget(audit, 25)
	if audit.EntityImages.Budget != 8 || audit.Intake.ImageBudget != 8 {
		t.Fatalf("budget = %d/%d, want the planner-reported 8", audit.EntityImages.Budget, audit.Intake.ImageBudget)
	}
}
