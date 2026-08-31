package plans

import "testing"

func TestDefaultsValid(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("Defaults().Validate() = %v, want nil", err)
	}
}

func TestDefaultsContainAllPlans(t *testing.T) {
	d := Defaults()
	for _, p := range []Plan{PlanTrial, PlanFree, PlanPro} {
		if _, ok := d[p]; !ok {
			t.Errorf("plan %q missing from defaults", p)
		}
	}
}

func TestGetFallsBackToTrial(t *testing.T) {
	d := Defaults()

	if got := d.Get(Plan("nonexistent")); got != d[PlanTrial] {
		t.Error("unknown plan should fall back to trial policy")
	}
	if got := d.Get(PlanTrial); got != d[PlanTrial] {
		t.Error("trial plan should return its own policy")
	}

	var nilSet PolicySet
	if got := nilSet.Get(PlanPro); got != Defaults()[PlanTrial] {
		t.Error("nil set should fall back to trial policy")
	}
}

func TestValidateMissingTrial(t *testing.T) {
	set := PolicySet{PlanPro: Defaults()[PlanPro]}
	if err := set.Validate(); err == nil {
		t.Error("Validate() should fail when the trial fallback is missing")
	}
}

func TestValidateNegativeLimit(t *testing.T) {
	set := Defaults()
	bad := set[PlanFree]
	bad.RateLimit = -1
	set[PlanFree] = bad
	if err := set.Validate(); err == nil {
		t.Error("Validate() should fail for a negative rate limit")
	}
}
